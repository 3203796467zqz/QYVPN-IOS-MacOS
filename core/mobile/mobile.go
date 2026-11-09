// SPDX-License-Identifier: GPL-2.0-or-later
package mobile

// mobile.go：iOS 客户端使用的连接内核入口。
//
// 桌面端是「特权组件进程 + unix socket」，iOS 不允许这种结构：NetworkExtension
// 的扩展进程既不能 fork 子进程，也无法常驻后台。所以这里把 internal/agent 直接
// 编进扩展进程，界面与扩展之间只通过 NETunnelProviderManager 的 IPC 通信，
// 而扩展与内核之间走同进程调用，用 gomobile bind 生成 ObjC 框架给 Swift 用。
//
// 对外只暴露四种类型：字符串（JSON）、整数、[]byte 和 Delegate 回调。
// gomobile 不支持 Go 结构体跨语言传递，所以节点、状态、设置一律序列化成 JSON，
// 由 Swift 侧用 Codable 解析。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"qingyou/qyvpn/internal/agent"
	"qingyou/qyvpn/internal/cloud"
	"qingyou/qyvpn/internal/session"
	"qingyou/qyvpn/internal/tunnel"
)

// Version 是客户端版本号，构建时用 -ldflags "-X qingyou/qyvpn/mobile.Version=" 注入，
// 与 Info.plist 的 CFBundleShortVersionString 保持一致。
var Version = "1.0.0"

// Delegate 把内核的数据面与生命周期事件推给扩展进程。
//
// 三个方法都在后台协程里调用，宿主实现必须自己保证线程安全；
// OnPacket 是热路径，每秒可能上千次，里面不要做重活。
//
// 参数只用 string 与 []byte：gomobile 生成的 ObjC 签名里分别对应 NSString 与
// NSData，跨语言最稳；结构化的内容一律用 JSON 传，避免为类型映射踩坑。
type Delegate interface {
	// OnPacket 交付一个下行 IP 包（已经复制过，宿主可以持有）。
	OnPacket(p []byte)
	// OnTunnelReady 在 IPCP 完成后回调，参数是
	// {"ip":"10.77.88.11","dns":["10.77.88.1"],"mtu":1400}。
	// 宿主必须在这里配置 NEPacketTunnelNetworkSettings，之后才开始 readPackets。
	OnTunnelReady(configJSON string)
	// OnLog 输出一行连接日志。
	OnLog(line string)
}

// Engine 是一次客户端运行期唯一的连接内核。
type Engine struct {
	mu       sync.Mutex
	agent    *agent.Agent
	tun      *packetTunnel
	delegate Delegate
	stateDir string
	closed   bool
}

// NewEngine 创建内核。stateDir 必须是 App Group 容器里可写的目录（日志、设置、
// 流量账本、设备标识都落在那里）；apiBase 为空时用内置后台地址。
func NewEngine(stateDir, apiBase, token string) (*Engine, error) {
	if strings.TrimSpace(stateDir) == "" {
		return nil, errors.New("缺少状态目录")
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, fmt.Errorf("无法创建状态目录：%w", err)
	}
	// internal/paths 通过这个环境变量定位状态目录；iOS 上不存在桌面端的
	// /Library/Application Support，必须显式指到 App Group 容器。
	if err := os.Setenv("QYVPN_STATE_DIR", stateDir); err != nil {
		return nil, fmt.Errorf("无法设置状态目录：%w", err)
	}

	// 后台凭据与版本在编译期注入（见 ios/build.sh），运行时不再需要配置文件。
	cloud.Product = "iOS"
	cloud.Version = Version

	a, err := agent.New(apiBase, token)
	if err != nil {
		return nil, err
	}
	e := &Engine{agent: a, stateDir: stateDir}
	a.SetTunnelOpener(e.openTunnel)

	// 上次的登录态存在 App Group 容器里：用户不必每次开 App 都重新登录。
	// 令牌失效时后台会返回 needLogin，界面据此回到登录页。
	if saved := e.loadUserToken(); saved != "" {
		a.Client().SetUserToken(saved)
	}
	return e, nil
}

// userTokenFile 是用户令牌的落盘位置（0600，仅 App Group 内可读）。
func (e *Engine) userTokenFile() string { return filepath.Join(e.stateDir, "user.token") }

func (e *Engine) loadUserToken() string {
	data, err := os.ReadFile(e.userTokenFile())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func (e *Engine) saveUserToken(token string) error {
	if token == "" {
		return e.clearUserToken()
	}
	if err := os.WriteFile(e.userTokenFile(), []byte(token), 0o600); err != nil {
		return fmt.Errorf("保存登录态失败：%w", err)
	}
	return nil
}

func (e *Engine) clearUserToken() error {
	e.agent.Client().SetUserToken("")
	if err := os.Remove(e.userTokenFile()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// SetUserToken 由宿主注入已登录的用户令牌。
//
// iOS 上登录发生在主 App（有输入框、有 Keychain），连接发生在扩展进程里，
// 两个进程不共享内存，所以由 App 通过 providerConfiguration 把令牌带进来，
// 扩展再交给内核；同时落盘一份，扩展被系统回收重启后还能继续用。
func (e *Engine) SetUserToken(token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return errors.New("缺少用户令牌，请先在 App 里登录")
	}
	e.agent.Client().SetUserToken(token)
	return e.saveUserToken(token)
}

// Login 用用户名口令登录，成功后立刻同步一次节点。
// platform 固定传 "ios"，后台据此统计各端活跃用户。
func (e *Engine) Login(username, password string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	reply, err := e.agent.Client().Login(ctx, username, password, "ios")
	if err != nil {
		return err
	}
	if err := e.saveUserToken(reply.Token); err != nil {
		// 落盘失败不影响本次使用，只是下次要重新登录。
		e.agent.Client().SetUserToken(reply.Token)
	}
	return e.Refresh()
}

// Logout 退出登录：吊销后台令牌、清掉本地令牌，并把节点列表清空。
func (e *Engine) Logout() {
	e.agent.Disconnect()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = e.agent.Client().Logout(ctx)
	if err := e.clearUserToken(); err != nil {
		e.logf("清理登录态失败：%v", err)
	}
	// 清掉内存里的节点，避免退出登录后仍显示上一账号的节点。
	_ = e.agent.Refresh(context.Background())
}

// LoggedIn 表示本地存有用户令牌（不代表后台仍然认可，以 NeedLogin 为准）。
func (e *Engine) LoggedIn() bool {
	return e.agent.Client().UserToken() != ""
}

// NeedLogin 表示后台要求登录：界面应当显示登录页而不是「没有节点」。
func (e *Engine) NeedLogin() bool {
	return e.agent.NeedLogin() || e.agent.Client().UserToken() == ""
}

// AccountJSON 返回账号信息（用户名、会员类型、到期时间、剩余天数）。
func (e *Engine) AccountJSON() string {
	if v := e.agent.AccountJSON(); v != "" {
		return v
	}
	return "null"
}

// PlanJSON 返回当前套餐摘要（名称、节点数）。
func (e *Engine) PlanJSON() string {
	if v := e.agent.PlanJSON(); v != "" {
		return v
	}
	return "null"
}

// SetDelegate 注册回调。必须在 Connect 之前调用。
func (e *Engine) SetDelegate(d Delegate) {
	e.mu.Lock()
	e.delegate = d
	e.mu.Unlock()
}

func (e *Engine) currentDelegate() Delegate {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.delegate
}

// Refresh 从后台拉一次节点与在线状态。令牌失效时清掉本地登录态，
// 让界面回到登录页而不是卡在「没有节点」。
func (e *Engine) Refresh() error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err := e.agent.Refresh(ctx)
	if err != nil && cloud.NeedLogin(err) {
		if clearErr := e.clearUserToken(); clearErr != nil {
			e.logf("清理失效登录态失败：%v", clearErr)
		}
	}
	return err
}

// logf 把内核日志转给宿主（扩展写入 os_log，界面在「连接日志」里显示）。
func (e *Engine) logf(format string, args ...any) {
	if d := e.currentDelegate(); d != nil {
		d.OnLog(fmt.Sprintf(format, args...))
	}
}

// NodesJSON 返回节点列表（不含密码），字段与 macOS 客户端一致。
func (e *Engine) NodesJSON() string {
	return marshal(e.agent.Nodes())
}

// StatusJSON 返回一次连接状态快照，界面每秒轮询一次。
func (e *Engine) StatusJSON() string {
	return marshal(e.agent.Status())
}

// SettingsJSON 返回本机保存的偏好设置。
func (e *Engine) SettingsJSON() string {
	return marshal(e.agent.Settings())
}

// SaveSettingsJSON 保存偏好设置（只认 JSON 里出现的字段）。
func (e *Engine) SaveSettingsJSON(raw string) error {
	var s agent.Settings
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return fmt.Errorf("设置格式不正确：%w", err)
	}
	if err := e.agent.UpdateSettings(s); err != nil {
		return err
	}
	// 改动网络相关设置（节点、直连规则）后必须重连才生效，这里不主动断开，
	// 由界面决定何时重连。
	return nil
}

// Connect 连接指定节点，空字符串表示沿用上次选中的节点。调用立即返回，
// 进度通过 StatusJSON 与 Delegate 反馈。
func (e *Engine) Connect(nodeID string) error {
	// 扩展进程单独启动时最常见的问题就是令牌没带进来：这里直接给出可读的原因，
	// 否则用户只会看到「请先选择节点」这种驴唇不对马嘴的提示。
	if e.agent.Client().UserToken() == "" || e.agent.NeedLogin() {
		return errors.New("登录已失效，请回到 App 重新登录")
	}
	return e.agent.Connect(nodeID)
}

// Disconnect 断开当前连接。
func (e *Engine) Disconnect() {
	e.agent.Disconnect()
}

// WritePacket 由扩展把 packetFlow 读到的上行包送进内核。
func (e *Engine) WritePacket(p []byte) error {
	e.mu.Lock()
	tun := e.tun
	e.mu.Unlock()
	if tun == nil {
		// 连接还没走到 IPCP，此时宿主读到的包没有意义，直接丢弃而不是报错，
		// 否则扩展的读取循环会被错误打断。
		return nil
	}
	return tun.WriteFromHost(p)
}

// ProbeJSON 对给定节点做 TCP 443 握手测速，idsJSON 是节点 ID 的 JSON 数组。
func (e *Engine) ProbeJSON(idsJSON string) string {
	var ids []string
	if err := json.Unmarshal([]byte(idsJSON), &ids); err != nil {
		return marshal(map[string]string{"error": "节点列表格式不正确"})
	}
	return marshal(e.agent.Probe(ids))
}

// LogTailJSON 返回最近的连接日志（最多 lines 行）。
func (e *Engine) LogTailJSON(lines int) string {
	if lines <= 0 || lines > 2000 {
		lines = 300
	}
	return marshal(e.agent.LogTail(lines))
}

// APIHost 返回后台域名，扩展需要把它从隧道路由里排除，否则刷新节点会绕进隧道。
func (e *Engine) APIHost() string {
	u, err := url.Parse(e.agent.Client().Base())
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// Close 停止连接并释放内核资源，扩展停止时调用。
func (e *Engine) Close() {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return
	}
	e.closed = true
	tun := e.tun
	e.tun = nil
	e.mu.Unlock()

	if tun != nil {
		_ = tun.Close()
	}
	e.agent.Close()
}

// openTunnel 是给 agent 注入的隧道实现：不开 utun，只把包交给宿主。
func (e *Engine) openTunnel(ip net.IP, dns []net.IP, mtu int, _ tunnel.Options) (session.Tunnel, error) {
	tun := newPacketTunnel()
	e.mu.Lock()
	e.tun = tun
	e.mu.Unlock()

	if d := e.currentDelegate(); d != nil {
		parts := make([]string, 0, len(dns))
		for _, v := range dns {
			parts = append(parts, v.String())
		}
		d.OnTunnelReady(marshal(map[string]any{
			"ip":  ip.String(),
			"dns": parts,
			"mtu": mtu,
		}))
	}
	go e.pumpOutbound(tun)
	return tun, nil
}

// pumpOutbound 独占一个协程把下行包投给宿主，避免阻塞 TLS 读取循环。
func (e *Engine) pumpOutbound(t *packetTunnel) {
	for {
		pkt, ok := t.TakeOutbound()
		if !ok {
			return
		}
		if d := e.currentDelegate(); d != nil {
			d.OnPacket(pkt)
		}
	}
}

func marshal(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}
