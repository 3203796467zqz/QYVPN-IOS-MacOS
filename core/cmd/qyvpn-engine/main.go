// SPDX-License-Identifier: GPL-2.0-or-later
// qyvpn-engine 是清幽私人云 macOS 客户端的连接内核。
//
// 它有两个身份：
//  1. 由 launchd 以 root 运行的特权组件（run），负责 utun、路由、DNS 与连接；
//  2. 给界面和排查用的命令行客户端（status / connect / disconnect / nodes / log / selftest）。
//
// 安装与卸载（install / uninstall）需要 root，由界面通过一次管理员授权调用。
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"qingyou/qyvpn/internal/agent"
	"qingyou/qyvpn/internal/cloud"
	"qingyou/qyvpn/internal/ipc"
	"qingyou/qyvpn/internal/paths"
	"qingyou/qyvpn/internal/session"
)

func main() {
	args := os.Args[1:]
	cmd := "run"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "run":
		runDaemon()
	case "install":
		mustInstall(args)
	case "uninstall":
		mustUninstall()
	case "version":
		fmt.Println("清幽私人云 macOS 内核 " + cloud.Version)
	case "status", "nodes", "connect", "disconnect", "probe", "log", "refresh", "update.check", "quit":
		mustClient(cmd, args)
	case "selftest":
		mustSelfTest(args)
	case "token":
		printToken()
	default:
		fmt.Fprintf(os.Stderr, "未知命令：%s\n", cmd)
		fmt.Fprintf(os.Stderr, "用法：qyvpn-engine [run|install|uninstall|status|nodes|connect <id>|disconnect|probe|log|refresh|selftest|version]\n")
		os.Exit(2)
	}
}

// apiBase 返回后台地址（换域名只需改 cloud.DefaultBase 或用环境变量覆盖）。
func apiBase() string {
	if v := os.Getenv("QYVPN_API_BASE"); v != "" {
		return v
	}
	return cloud.DefaultBase
}

// runDaemon 是特权组件主循环。
func runDaemon() {
	if !isRoot() {
		fmt.Fprintln(os.Stderr, "连接内核需要 root 权限运行（由 launchd 启动）")
		os.Exit(1)
	}
	token, err := ipc.ReadToken()
	if err != nil {
		fmt.Fprintf(os.Stderr, "读取本地通信令牌失败：%v\n", err)
		os.Exit(1)
	}
	a, err := agent.New(apiBase(), cloud.DefaultToken)
	if err != nil {
		fmt.Fprintf(os.Stderr, "初始化失败：%v\n", err)
		os.Exit(1)
	}
	defer a.Close()

	srv, err := ipc.NewServer(a, token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "启动本地接口失败：%v\n", err)
		os.Exit(1)
	}
	go func() {
		if err := srv.Serve(); err != nil {
			fmt.Fprintf(os.Stderr, "本地接口异常退出：%v\n", err)
		}
	}()

	// 启动时同步一次节点；失败不影响内核运行（界面可以手动刷新）。
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	if err := a.Refresh(ctx); err == nil {
		if s := a.Settings(); s.AutoConnect {
			if id := s.SelectedNode; id != "" {
				_ = a.Connect(id)
			}
		}
	}
	cancel()

	sig := make(chan os.Signal, 2)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-sig:
	case <-srv.Quit():
	}
	a.Disconnect()
	srv.Close()
	time.Sleep(300 * time.Millisecond) // 给断开流程一点收尾时间
}

// mustInstall 安装特权组件。
func mustInstall(args []string) {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	token := fs.String("token", "", "本地通信令牌（32 字节随机值，十六进制）")
	_ = fs.Parse(args)
	if !isRoot() {
		fmt.Fprintln(os.Stderr, "install 需要 root 权限")
		os.Exit(1)
	}
	if *token == "" {
		fmt.Fprintln(os.Stderr, "缺少 --token")
		os.Exit(1)
	}
	if err := installHelper(*token); err != nil {
		fmt.Fprintf(os.Stderr, "安装失败：%v\n", err)
		os.Exit(1)
	}
	fmt.Println("OK")
}

func installHelper(token string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(paths.HelperBinary()), 0o755); err != nil {
		return err
	}
	// 复制自身到特权目录；若已经在目标位置运行则跳过，避免覆盖正在执行的二进制。
	if filepath.Clean(self) != filepath.Clean(paths.HelperBinary()) {
		data, err := os.ReadFile(self)
		if err != nil {
			return err
		}
		if err := os.WriteFile(paths.HelperBinary(), data, 0o755); err != nil {
			return err
		}
	}
	if err := os.Chmod(paths.HelperBinary(), 0o755); err != nil {
		return err
	}
	_ = os.Chown(paths.HelperBinary(), 0, 0)

	if err := ipc.WriteToken(token); err != nil {
		return err
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>run</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key>
  <dict>
    <key>SuccessfulExit</key><false/>
  </dict>
  <key>ProcessType</key><string>Interactive</string>
  <key>StandardOutPath</key><string>/Library/Logs/cn.qo8.qyvpn.engine.log</string>
  <key>StandardErrorPath</key><string>/Library/Logs/cn.qo8.qyvpn.engine.log</string>
</dict>
</plist>
`, paths.HelperLabel, paths.HelperBinary())
	if err := os.WriteFile(paths.HelperPlist(), []byte(plist), 0o644); err != nil {
		return err
	}
	// 先卸载旧服务（忽略错误），再按新配置加载。
	_ = exec.Command("launchctl", "bootout", "system/"+paths.HelperLabel).Run()
	if out, err := exec.Command("launchctl", "bootstrap", "system", paths.HelperPlist()).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl bootstrap 失败：%s %v", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func mustUninstall() {
	if !isRoot() {
		fmt.Fprintln(os.Stderr, "uninstall 需要 root 权限")
		os.Exit(1)
	}
	_ = exec.Command("launchctl", "bootout", "system/"+paths.HelperLabel).Run()
	for _, path := range []string{paths.HelperPlist(), paths.HelperBinary(), paths.SocketPath(), filepath.Join(paths.StateDir(), "ipc.token")} {
		_ = os.Remove(path)
	}
	fmt.Println("OK")
}

// mustClient 是命令行侧（调试用）。
func mustClient(cmd string, args []string) {
	token, err := ipc.ReadToken()
	if err != nil {
		fatal("读取本地令牌失败（需要管理员权限）：" + err.Error())
	}
	req := ipc.Request{Cmd: cmd}
	switch cmd {
	case "connect":
		if len(args) == 0 {
			fatal("用法：connect <节点ID>")
		}
		req.NodeID = args[0]
		req.Cmd = "connect"
	case "log":
		req.Cmd = "log"
		req.Lines = 100
	case "probe":
		req.Cmd = "probe"
	}
	resp, err := ipc.Call(token, req)
	if err != nil {
		fatal(err.Error())
	}
	if !resp.OK {
		fatal(resp.Error)
	}
	printJSON(resp)
}

// mustSelfTest 在没有界面的情况下跑一次握手，用于部署后确认线路。
func mustSelfTest(args []string) {
	fs := flag.NewFlagSet("selftest", flag.ExitOnError)
	nodeID := fs.String("node", "", "节点 ID（留空自动选第一台可用节点）")
	_ = fs.Parse(args)

	client, err := cloud.New(apiBase(), cloud.DefaultToken)
	if err != nil {
		fatal(err.Error())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cfg, err := client.Sync(ctx)
	if err != nil {
		fatal("拉取节点失败：" + err.Error())
	}
	var node = cfg.Nodes[0]
	for _, n := range cfg.Nodes {
		if *nodeID != "" && n.ID == *nodeID {
			node = n
			break
		}
		if *nodeID == "" && n.BlockReason() == "" {
			node = n
			break
		}
	}
	if node == nil {
		fatal("没有可用节点")
	}
	fmt.Printf("测试节点：%s（%s:%d）\n", node.Name, node.Host, node.Port)

	runCtx, runCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer runCancel()
	start := time.Now()
	ok := false
	err = session.Run(runCtx, node, session.Options{
		MTU: 1400,
		Log: func(format string, args ...any) {
			fmt.Printf("[%5.1fs] %s\n", time.Since(start).Seconds(), fmt.Sprintf(format, args...))
		},
		OnConnected: func() {
			ok = true
			runCancel()
		},
	})
	if !ok {
		fatal("握手失败：" + err.Error())
	}
	fmt.Println("握手成功（未建立隧道，仅验证协议与账号）")
}

func printToken() {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		fatal(err.Error())
	}
	fmt.Println(hex.EncodeToString(buf))
}

func printJSON(v any) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fatal(err.Error())
	}
	fmt.Println(string(data))
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}
