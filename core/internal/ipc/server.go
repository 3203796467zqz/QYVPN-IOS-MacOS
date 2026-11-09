// SPDX-License-Identifier: GPL-2.0-or-later
// Package ipc 提供界面进程与特权组件之间的本地 JSON 通信。
//
// 传输方式是 unix domain socket，一次请求一行 JSON。socket 放在 /var/run，
// 权限 0666 但每个请求必须带 token（安装时生成的 32 字节随机值），
// 因此同机其它用户即使能连上也无法操作。
package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"qingyou/qyvpn/internal/agent"
	"qingyou/qyvpn/internal/paths"
)

// Request 是一次调用。
type Request struct {
	Token    string          `json:"token"`
	Cmd      string          `json:"cmd"`
	NodeID   string          `json:"nodeId,omitempty"`
	NodeIDs  []string        `json:"nodeIds,omitempty"`
	Settings *agent.Settings `json:"settings,omitempty"`
	Lines    int             `json:"lines,omitempty"`

	// 账号相关（login / register / redeem）。
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Email    string `json:"email,omitempty"`
	Code     string `json:"code,omitempty"`
}

// Response 是调用结果。
type Response struct {
	OK       bool                         `json:"ok"`
	Error    string                       `json:"error,omitempty"`
	Version  string                       `json:"version"`
	Status   *agent.Status                `json:"status,omitempty"`
	Nodes    []agent.NodeView             `json:"nodes,omitempty"`
	Settings *agent.Settings              `json:"settings,omitempty"`
	Probes   map[string]agent.ProbeResult `json:"probes,omitempty"`
	Log      []string                     `json:"log,omitempty"`
	Update   *agent.UpdateInfo            `json:"update,omitempty"`
	Path     string                       `json:"path,omitempty"`

	// 卡密兑换结果（redeem 命令）。
	Days   int    `json:"days,omitempty"`
	Expire string `json:"expire,omitempty"`
}

// Server 是特权组件侧的监听器。
type Server struct {
	agent    *agent.Agent
	token    string
	listener net.Listener
	quit     chan struct{}
	quitOnce sync.Once
}

// NewServer 创建监听器。
//
// 上一次异常退出可能留下 socket 文件：先尝试直接监听，失败时用一次连通性探测
// 判断是否真的有内核在跑，确认没人应答再清理残留文件重试一次。
func NewServer(a *agent.Agent, token string) (*Server, error) {
	path := paths.SocketPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		if !isLive(path) {
			_ = os.Remove(path)
			ln, err = net.Listen("unix", path)
		}
		if err != nil {
			return nil, fmt.Errorf("监听 %s 失败：%w", path, err)
		}
	}
	if err := os.Chmod(path, 0o666); err != nil {
		ln.Close()
		return nil, err
	}
	return &Server{agent: a, token: token, listener: ln, quit: make(chan struct{})}, nil
}

// isLive 探测 socket 是否已有内核在监听。
func isLive(path string) bool {
	conn, err := net.DialTimeout("unix", path, 500*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// Serve 接受连接直到 Close。
func (s *Server) Serve() error {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.quit:
				return nil
			default:
			}
			if strings.Contains(err.Error(), "use of closed") {
				return nil
			}
			continue
		}
		go s.handle(conn)
	}
}

// Close 停止监听并删除 socket 文件。
func (s *Server) Close() {
	s.quitOnce.Do(func() { close(s.quit) })
	_ = s.listener.Close()
	_ = os.Remove(paths.SocketPath())
}

// Stop 只发出退出信号（由 main 负责收尾）。
func (s *Server) Stop() {
	s.quitOnce.Do(func() { close(s.quit) })
	_ = s.listener.Close()
}

// Quit 返回「界面要求退出」的信号通道。
func (s *Server) Quit() <-chan struct{} { return s.quit }

func (s *Server) handle(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(60 * time.Second))
	reader := bufio.NewReaderSize(conn, 1<<20)
	line, err := reader.ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return
	}
	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		writeJSON(conn, Response{OK: false, Error: "请求格式无效", Version: s.agent.Status().Version})
		return
	}
	if req.Token != s.token {
		writeJSON(conn, Response{OK: false, Error: "未授权的本地请求"})
		return
	}
	resp := s.dispatch(req)
	writeJSON(conn, resp)
}

func (s *Server) dispatch(req Request) Response {
	ver := s.agent.Status().Version
	base := func() Response { return Response{OK: true, Version: ver} }

	switch req.Cmd {
	case "ping":
		return base()
	case "status":
		r := base()
		st := s.agent.Status()
		r.Status = &st
		return r
	case "nodes":
		r := base()
		r.Nodes = s.agent.Nodes()
		settings := s.agent.Settings()
		r.Settings = &settings
		return r
	case "refresh":
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		if err := s.agent.Refresh(ctx); err != nil {
			return Response{OK: false, Error: err.Error(), Version: ver}
		}
		r := base()
		r.Nodes = s.agent.Nodes()
		return r
	case "connect":
		if err := s.agent.Connect(req.NodeID); err != nil {
			return Response{OK: false, Error: err.Error(), Version: ver}
		}
		return base()
	case "login":
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		if err := s.agent.Login(ctx, req.Username, req.Password); err != nil {
			return Response{OK: false, Error: err.Error(), Version: ver}
		}
		r := base()
		st := s.agent.Status()
		r.Status = &st
		r.Nodes = s.agent.Nodes()
		return r
	case "register":
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		if err := s.agent.Register(ctx, req.Username, req.Password, req.Email); err != nil {
			return Response{OK: false, Error: err.Error(), Version: ver}
		}
		r := base()
		st := s.agent.Status()
		r.Status = &st
		r.Nodes = s.agent.Nodes()
		return r
	case "redeem":
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		reply, err := s.agent.Redeem(ctx, req.Code)
		if err != nil {
			return Response{OK: false, Error: err.Error(), Version: ver}
		}
		r := base()
		st := s.agent.Status()
		r.Status = &st
		r.Nodes = s.agent.Nodes()
		r.Days = reply.Days
		r.Expire = reply.Expire
		return r
	case "logout":
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		s.agent.Logout(ctx)
		r := base()
		st := s.agent.Status()
		r.Status = &st
		r.Nodes = []agent.NodeView{}
		return r
	case "disconnect":
		s.agent.Disconnect()
		return base()
	case "probe":
		r := base()
		r.Probes = s.agent.Probe(req.NodeIDs)
		return r
	case "settings.get":
		r := base()
		st := s.agent.Settings()
		r.Settings = &st
		return r
	case "settings.set":
		if req.Settings == nil {
			return Response{OK: false, Error: "缺少设置内容", Version: ver}
		}
		if err := s.agent.UpdateSettings(*req.Settings); err != nil {
			return Response{OK: false, Error: err.Error(), Version: ver}
		}
		r := base()
		st := s.agent.Settings()
		r.Settings = &st
		return r
	case "log":
		r := base()
		r.Log = s.agent.LogTail(req.Lines)
		return r
	case "update.check":
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		info := s.agent.CheckUpdate(ctx)
		r := base()
		r.Update = &info
		return r
	case "update.download":
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		path, err := s.agent.DownloadUpdate(ctx)
		if err != nil {
			return Response{OK: false, Error: err.Error(), Version: ver}
		}
		r := base()
		r.Path = path
		return r
	case "quit":
		go func() {
			time.Sleep(200 * time.Millisecond)
			s.Stop()
		}()
		return base()
	}
	return Response{OK: false, Error: "未知指令：" + req.Cmd, Version: ver}
}

func writeJSON(conn net.Conn, v Response) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	_, _ = conn.Write(append(data, '\n'))
}

// --- 客户端 ---

// Call 向特权组件发送一次请求。
func Call(token string, req Request) (*Response, error) {
	req.Token = token
	conn, err := net.DialTimeout("unix", paths.SocketPath(), 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("无法连接连接内核（特权组件未运行？）：%w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Minute))
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Write(append(data, '\n')); err != nil {
		return nil, err
	}
	reader := bufio.NewReaderSize(conn, 1<<20)
	line, err := reader.ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return nil, err
	}
	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, errors.New("内核返回了无法解析的响应")
	}
	return &resp, nil
}

// ReadToken 从状态目录读取本地通信令牌。
func ReadToken() (string, error) {
	data, err := os.ReadFile(filepath.Join(paths.StateDir(), "ipc.token"))
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(data))
	if len(token) < 32 {
		return "", errors.New("本地令牌无效")
	}
	return token, nil
}

// WriteToken 写入本地通信令牌（仅 root 可读）。
func WriteToken(token string) error {
	if err := paths.EnsureStateDir(); err != nil {
		return err
	}
	path := filepath.Join(paths.StateDir(), "ipc.token")
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}
