// SPDX-License-Identifier: GPL-2.0-or-later
// Package agent 是连接内核的门面：节点同步、连接状态机、自动重连、流量统计、
// 客户端上报与设置持久化。界面进程只通过 internal/ipc 与它交互。
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"qingyou/qyvpn/internal/cloud"
	"qingyou/qyvpn/internal/model"
	"qingyou/qyvpn/internal/paths"
	"qingyou/qyvpn/internal/session"
	"qingyou/qyvpn/internal/tunnel"
)

// 连接状态。
const (
	StateIdle          = "idle"
	StateConnecting    = "connecting"
	StateConnected     = "connected"
	StateDisconnecting = "disconnecting"
	StateReconnecting  = "reconnecting"
)

// Settings 是界面可配置项（与 Windows 客户端的偏好设置对应）。
type Settings struct {
	SelectedNode  string `json:"selectedNode"`
	BypassEnabled bool   `json:"bypassEnabled"`
	BypassChina   bool   `json:"bypassChina"`
	BypassDomains string `json:"bypassDomains"`
	AutoConnect   bool   `json:"autoConnect"`
}

// Status 是界面每次轮询拿到的一次快照。
type Status struct {
	State      string `json:"state"`
	Stage      string `json:"stage"`
	NodeID     string `json:"nodeId"`
	NodeName   string `json:"nodeName"`
	Since      string `json:"since"`
	Duration   int64  `json:"duration"`
	RxRate     int64  `json:"rxRate"`
	TxRate     int64  `json:"txRate"`
	SessionRx  int64  `json:"sessionRx"`
	SessionTx  int64  `json:"sessionTx"`
	MonthUsed  int64  `json:"monthUsed"`
	MonthLimit int64  `json:"monthLimit"`
	TunnelIP   string `json:"tunnelIp"`
	DNS        string `json:"dns"`
	Error      string `json:"error"`
	Version    string `json:"version"`

	// 账号态。后台 1.9 起节点按用户套餐下发：未登录时 nodes 为空，
	// 界面必须区分「要登录」和「没有节点」，否则用户只会看到一片空白。
	NeedLogin bool            `json:"needLogin"`
	LoggedIn  bool            `json:"loggedIn"`
	NoNodes   bool            `json:"noNodes"`
	Account   json.RawMessage `json:"account,omitempty"`
	Plan      json.RawMessage `json:"plan,omitempty"`
}

// NodeView 是下发给界面的节点信息（不含密码）。
type NodeView struct {
	ID        string       `json:"id"`
	Name      string       `json:"name"`
	Host      string       `json:"host"`
	Port      int          `json:"port"`
	Icon      string       `json:"icon"`
	Available bool         `json:"available"`
	Login     string       `json:"login"`
	Usage     *model.Usage `json:"usage,omitempty"`
	MaxUsers  int          `json:"maxUsers"`
	Block     string       `json:"block"`
}

// OpenTunnel 是隧道创建入口。macOS 上由 internal/tunnel 提供 utun 实现；
// iOS 的 NetworkExtension 扩展里既没有 utun 设备，也没有执行 route/networksetup
// 的权限，由扩展侧注入一个把 IP 包桥接给 NEPacketTunnelFlow 的实现（见 mobile 包）。
type OpenTunnel func(ip net.IP, dns []net.IP, mtu int, opts tunnel.Options) (session.Tunnel, error)

// Agent 是连接内核的运行时。
type Agent struct {
	mu       sync.Mutex
	client   *cloud.Client
	log      *logFile
	ledger   *ledger
	settings Settings

	cfg   *model.Config
	nodes []*model.Node

	state    string
	stage    string
	nodeID   string
	since    time.Time
	errMsg   string
	tunnelIP string
	dnsText  string

	dev      tunnel.Device
	openTun  OpenTunnel
	meter    *session.Meter
	stopFlag bool
	cancel   context.CancelFunc
	wg       sync.WaitGroup

	rxRate, txRate int64
	sessRx, sessTx int64
	lastRx, lastTx uint64
	monthUsed      int64
	monthLimit     int64

	probeMu   sync.Mutex
	lastProbe map[string]ProbeResult
	// probeDNS 缓存节点域名的解析结果。DNS 往往比 TCP 握手慢一个数量级，
	// 把它算进「延迟」会得到「亚洲节点 800ms」这种假数字。
	probeDNS map[string]dnsEntry
}

// dnsEntry 是一次解析结果，10 分钟内复用。
type dnsEntry struct {
	ips []string
	at  time.Time
}

// ProbeResult 是一次 TCP 443 测速结果。
type ProbeResult struct {
	MS    int64     `json:"ms"`
	Error string    `json:"error,omitempty"`
	At    time.Time `json:"-"`
}

// New 创建 Agent 并加载本地设置、账本与日志。
func New(apiBase, token string) (*Agent, error) {
	client, err := cloud.New(apiBase, token)
	if err != nil {
		return nil, err
	}
	if err := paths.EnsureStateDir(); err != nil {
		return nil, err
	}
	lf, err := openLog(paths.LogFile())
	if err != nil {
		return nil, err
	}
	a := &Agent{
		client:    client,
		log:       lf,
		state:     StateIdle,
		stage:     "未连接",
		lastProbe: map[string]ProbeResult{},
		probeDNS:  map[string]dnsEntry{},
	}
	a.ledger = loadLedger(filepath.Join(paths.StateDir(), "ledger.json"))
	a.settings = loadSettings(paths.SettingsFile())
	// 上次登录的用户令牌：读回来直接带上，重建内核后不用重新登录。
	if token := loadUserToken(); token != "" {
		client.SetUserToken(token)
	}
	a.logf("客户端启动，后台 %s，版本 %s", client.Base(), cloud.Version)
	return a, nil
}

// Close 停止连接并关闭日志。
func (a *Agent) Close() {
	a.Disconnect()
	a.wg.Wait()
	a.log.Close()
}

// SetTunnelOpener 替换隧道创建入口，nil 表示恢复默认的 utun 实现。
// 必须在 Connect 之前调用；已开始连接时改动只对下一次连接生效。
func (a *Agent) SetTunnelOpener(fn OpenTunnel) {
	a.mu.Lock()
	a.openTun = fn
	a.mu.Unlock()
}

// openTunnelWith 按当前配置创建隧道。注入实现返回 err 时不能把 nil 指针塞进
// interface（否则调用方拿到的是非 nil 的 session.Tunnel），所以两条路径分开返回。
func (a *Agent) openTunnelWith(ip net.IP, dns []net.IP, mtu int, opts tunnel.Options) (session.Tunnel, error) {
	a.mu.Lock()
	fn := a.openTun
	a.mu.Unlock()
	if fn != nil {
		tun, err := fn(ip, dns, mtu, opts)
		if err != nil {
			return nil, err
		}
		if tun == nil {
			return nil, errors.New("隧道实现返回了空设备")
		}
		return tun, nil
	}
	dev, err := tunnel.Open(ip, dns, mtu, opts)
	if err != nil {
		return nil, err
	}
	return dev, nil
}

func (a *Agent) logf(format string, args ...any) {
	if a.log != nil {
		a.log.write(format, args...)
	}
}

// Client 返回后台客户端（供更新检查复用）。
func (a *Agent) Client() *cloud.Client { return a.client }

// Refresh 从后台同步节点列表。
func (a *Agent) Refresh(ctx context.Context) error {
	cfg, err := a.client.Sync(ctx)
	if err != nil {
		a.logf("同步节点失败：%v", err)
		return err
	}
	a.mu.Lock()
	a.cfg = cfg
	a.nodes = cfg.Nodes
	if a.settings.SelectedNode == "" && len(cfg.Nodes) > 0 {
		a.settings.SelectedNode = cfg.Nodes[0].ID
		_ = saveSettings(paths.SettingsFile(), a.settings)
	}
	a.mu.Unlock()
	a.logf("节点已同步：%d 台", len(cfg.Nodes))
	// 后台预热域名解析，避免第一次测速把 DNS 时间算进延迟。
	go a.warmDNS(cfg.Nodes)
	return nil
}

// ---- 账号 ----

// loadUserToken 读回上次登录保存的用户令牌；读不到或长度异常都当作未登录。
func loadUserToken() string {
	data, err := os.ReadFile(paths.UserTokenFile())
	if err != nil {
		return ""
	}
	token := strings.TrimSpace(string(data))
	if len(token) < 16 {
		return ""
	}
	return token
}

// saveUserToken 把登录态落盘（0600）。失败不致命：本次仍可用，只是重启内核后要重新登录。
func saveUserToken(token string) error {
	if token == "" {
		return clearUserToken()
	}
	if err := os.WriteFile(paths.UserTokenFile(), []byte(token), 0o600); err != nil {
		return err
	}
	return os.Chmod(paths.UserTokenFile(), 0o600)
}

func clearUserToken() error {
	if err := os.Remove(paths.UserTokenFile()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Login 用用户名口令登录，成功后立刻同步一次节点。
// platform 固定传 macos，后台据此统计各端活跃情况。
func (a *Agent) Login(ctx context.Context, username, password string) error {
	reply, err := a.client.Login(ctx, username, password, "macos")
	if err != nil {
		a.logf("登录失败：%v", err)
		return err
	}
	if err := saveUserToken(reply.Token); err != nil {
		a.logf("登录态保存失败（本次仍可用）：%v", err)
	}
	a.logf("已登录：%s", username)
	return a.Refresh(ctx)
}

// Register 注册新账号并直接登录。参数校验与错误提示都由后台给出。
func (a *Agent) Register(ctx context.Context, username, password, email string) error {
	reply, err := a.client.Register(ctx, username, password, email, "macos")
	if err != nil {
		a.logf("注册失败：%v", err)
		return err
	}
	if err := saveUserToken(reply.Token); err != nil {
		a.logf("登录态保存失败（本次仍可用）：%v", err)
	}
	a.logf("注册成功并已登录：%s", username)
	return a.Refresh(ctx)
}

// Redeem 用卡密给当前账号续期；兑换成功后重新同步节点（套餐变化会带来新的节点与额度）。
func (a *Agent) Redeem(ctx context.Context, code string) (*cloud.RedeemReply, error) {
	reply, err := a.client.Redeem(ctx, code)
	if err != nil {
		a.logf("卡密兑换失败：%v", err)
		return nil, err
	}
	a.logf("卡密兑换成功：增加 %d 天，到期 %s", reply.Days, reply.Expire)
	if err := a.Refresh(ctx); err != nil {
		a.logf("兑换后同步节点失败：%v", err)
	}
	return reply, nil
}

// Logout 退出登录：先断开隧道，再吊销后台令牌、清除本地令牌与节点缓存。
func (a *Agent) Logout(ctx context.Context) {
	a.Disconnect()
	_ = a.client.Logout(ctx)
	if err := clearUserToken(); err != nil {
		a.logf("清除登录态失败：%v", err)
	}
	a.mu.Lock()
	a.cfg = nil
	a.nodes = nil
	a.mu.Unlock()
	a.logf("已退出登录")
}

// AccountJSON 返回后台随配置下发的用户信息（账号、会员到期、套餐节点数）。
// 未登录时返回空字符串，界面据此切到登录页。
func (a *Agent) AccountJSON() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cfg == nil || len(a.cfg.User) == 0 {
		return ""
	}
	return string(a.cfg.User)
}

// PlanJSON 返回用户套餐摘要（名称、节点数、剩余天数）。
func (a *Agent) PlanJSON() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cfg == nil || len(a.cfg.Plan) == 0 {
		return ""
	}
	return string(a.cfg.Plan)
}

// NeedLogin 表示后台要求用户登录：这次同步没有携带有效用户令牌，
// 节点列表为空是「未登录」而不是「没有节点」。
func (a *Agent) NeedLogin() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg != nil && a.cfg.NeedLogin
}

// Nodes 返回当前节点视图。
func (a *Agent) Nodes() []NodeView {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]NodeView, 0, len(a.nodes))
	for _, n := range a.nodes {
		out = append(out, NodeView{
			ID:        n.ID,
			Name:      n.Name,
			Host:      n.Host,
			Port:      n.Port,
			Icon:      n.Icon,
			Available: n.Available,
			Login:     n.Login(),
			Usage:     n.Usage,
			MaxUsers:  n.MaxUsers,
			Block:     n.BlockReason(),
		})
	}
	return out
}

// Settings 返回当前设置。
func (a *Agent) Settings() Settings {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.settings
}

// UpdateSettings 保存设置；切换直连规则需要重新连接才生效（与 Windows 版一致）。
func (a *Agent) UpdateSettings(s Settings) error {
	a.mu.Lock()
	if s.SelectedNode != "" {
		a.settings.SelectedNode = s.SelectedNode
	}
	a.settings.BypassEnabled = s.BypassEnabled
	a.settings.BypassChina = s.BypassChina
	a.settings.BypassDomains = s.BypassDomains
	a.settings.AutoConnect = s.AutoConnect
	a.mu.Unlock()
	return saveSettings(paths.SettingsFile(), s)
}

// Status 返回一次状态快照。
func (a *Agent) Status() Status {
	a.mu.Lock()
	defer a.mu.Unlock()
	st := Status{
		State:      a.state,
		Stage:      a.stage,
		NodeID:     a.nodeID,
		Since:      "",
		RxRate:     a.rxRate,
		TxRate:     a.txRate,
		SessionRx:  a.sessRx,
		SessionTx:  a.sessTx,
		MonthUsed:  a.monthUsed,
		MonthLimit: a.monthLimit,
		TunnelIP:   a.tunnelIP,
		DNS:        a.dnsText,
		Error:      a.errMsg,
		Version:    cloud.Version,
	}
	if !a.since.IsZero() {
		st.Since = a.since.UTC().Format(time.RFC3339)
		st.Duration = int64(time.Since(a.since).Seconds())
	}
	for _, n := range a.nodes {
		if n.ID == a.nodeID {
			st.NodeName = n.Name
		}
	}
	st.LoggedIn = a.client.UserToken() != ""
	if a.cfg != nil {
		// NeedLogin 以后台的说法为准：本地有令牌但已失效时，后台也会给 true。
		st.NeedLogin = a.cfg.NeedLogin || !st.LoggedIn
		st.NoNodes = a.cfg.NoNodes
		st.Account = a.cfg.User
		st.Plan = a.cfg.Plan
	} else {
		st.NeedLogin = !st.LoggedIn
	}
	return st
}

// Connect 连接到指定节点（空字符串表示当前选中节点）。
func (a *Agent) Connect(nodeID string) error {
	a.mu.Lock()
	if a.state == StateConnecting || a.state == StateConnected || a.state == StateReconnecting {
		a.mu.Unlock()
		return errors.New("已经在连接或已连接")
	}
	if nodeID != "" {
		a.settings.SelectedNode = nodeID
	}
	nodeID = a.settings.SelectedNode
	var node *model.Node
	for _, n := range a.nodes {
		if n.ID == nodeID {
			node = n
			break
		}
	}
	if node == nil {
		a.mu.Unlock()
		return errors.New("请先选择节点")
	}
	if reason := node.BlockReason(); reason != "" {
		a.mu.Unlock()
		return errors.New(reason)
	}
	a.state = StateConnecting
	a.stage = "正在连接节点"
	a.nodeID = node.ID
	a.errMsg = ""
	a.stopFlag = false
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	a.mu.Unlock()

	_ = saveSettings(paths.SettingsFile(), a.Settings())
	a.wg.Add(1)
	go a.run(ctx, node)
	return nil
}

// Disconnect 断开当前连接。
func (a *Agent) Disconnect() {
	a.mu.Lock()
	a.stopFlag = true
	cancel := a.cancel
	if a.state == StateConnected || a.state == StateConnecting || a.state == StateReconnecting {
		a.state = StateDisconnecting
		a.stage = "正在断开"
	}
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// run 是一次连接（含自动重连）的主循环。
func (a *Agent) run(ctx context.Context, node *model.Node) {
	defer a.wg.Done()
	backoff := []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second, 40 * time.Second, 60 * time.Second}
	attempt := 0

	for {
		err := a.connectOnce(ctx, node)
		if ctx.Err() != nil || a.stopped() {
			a.setState(StateIdle, "未连接", "")
			a.clearSession()
			return
		}
		if err == nil {
			return
		}
		if !retryable(err) {
			a.setState(StateIdle, "连接失败", err.Error())
			a.logf("连接结束：%v", err)
			a.clearSession()
			return
		}
		if attempt >= len(backoff) {
			a.setState(StateIdle, "连接失败", err.Error())
			a.logf("自动重连已达上限：%v", err)
			a.clearSession()
			return
		}
		wait := backoff[attempt]
		attempt++
		a.setState(StateReconnecting, fmt.Sprintf("%d 秒后第 %d 次重连", int(wait.Seconds()), attempt), err.Error())
		a.logf("连接中断，%d 秒后重连（第 %d 次）：%v", int(wait.Seconds()), attempt, err)
		select {
		case <-ctx.Done():
			a.setState(StateIdle, "未连接", "")
			a.clearSession()
			return
		case <-time.After(wait):
		}
		// 重连前刷新节点，避免连到已下线的机器。
		rctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		_ = a.Refresh(rctx)
		cancel()
		if fresh := a.findNode(node.ID); fresh != nil {
			node = fresh
		} else {
			a.setState(StateIdle, "连接失败", "节点已下线")
			a.clearSession()
			return
		}
	}
}

func (a *Agent) connectOnce(ctx context.Context, node *model.Node) error {
	a.mu.Lock()
	a.state = StateConnecting
	a.stage = "正在连接节点"
	a.errMsg = ""
	a.since = time.Time{}
	a.sessRx, a.sessTx = 0, 0
	a.rxRate, a.txRate = 0, 0
	a.lastRx, a.lastTx = 0, 0
	a.mu.Unlock()

	a.logf("开始连接：%s（%s:%d）", node.Name, node.Host, node.Port)

	opts := a.tunnelOptions(node)
	meter := &session.Meter{}
	a.mu.Lock()
	a.meter = meter
	a.mu.Unlock()

	factory := func(ip net.IP, dns []net.IP, mtu int) (session.Tunnel, error) {
		tun, err := a.openTunnelWith(ip, dns, mtu, opts)
		if err != nil {
			return nil, err
		}
		// macOS 的 utun 实现同时提供网卡计数，iOS 的桥接实现没有 Statistics，
		// 只靠 meter 统计即可，所以这里按接口能力判断而不是强转。
		if dev, ok := tun.(tunnel.Device); ok {
			a.mu.Lock()
			a.dev = dev
			a.mu.Unlock()
		}
		a.mu.Lock()
		a.tunnelIP = ip.String()
		if len(dns) > 0 {
			parts := make([]string, 0, len(dns))
			for _, d := range dns {
				parts = append(parts, d.String())
			}
			a.dnsText = strings.Join(parts, ", ")
		}
		a.mu.Unlock()
		return tun, nil
	}

	// 先把本月累计读出来，界面在连接后立刻能看到额度。
	nodeID := node.ID
	a.mu.Lock()
	a.monthUsed = a.ledger.used(nodeID, model.Month(time.Now()))
	if node.Usage != nil {
		a.monthLimit = node.Usage.Limit
	}
	a.mu.Unlock()

	err := session.Run(ctx, node, session.Options{
		MTU:     1400,
		Factory: factory,
		Log: func(format string, args ...any) {
			a.logf(format, args...)
		},
		OnIPCP: func(ip string, dns []string) {
			a.mu.Lock()
			a.tunnelIP = ip
			a.dnsText = strings.Join(dns, ", ")
			a.stage = "正在建立隧道"
			a.mu.Unlock()
		},
		OnConnected: func() {
			a.mu.Lock()
			a.state = StateConnected
			a.stage = "节点通道已连接"
			a.since = time.Now()
			a.lastRx, a.lastTx = 0, 0
			a.mu.Unlock()
			a.logf("节点通道已连接：%s", node.Name)
			a.wg.Add(2)
			go a.sampleLoop(ctx)
			go a.telemetryLoop(ctx, node)
			a.reportOnce(node, true)
		},
	})
	if err != nil {
		a.logf("连接失败：%v", err)
	}
	return err
}

// tunnelOptions 组装 utun 参数：服务器主机路由 + 可选直连网段。
func (a *Agent) tunnelOptions(node *model.Node) tunnel.Options {
	s := a.Settings()
	opts := tunnel.Options{
		ServerHost: node.Host,
		Log: func(format string, args ...any) {
			a.logf(format, args...)
		},
	}
	if s.BypassEnabled && s.BypassChina {
		if cidrs, err := loadChinaCIDRs(); err == nil {
			opts.BypassCIDRs = append(opts.BypassCIDRs, cidrs...)
		} else {
			a.logf("国内直连规则不可用：%v", err)
		}
	}
	if s.BypassEnabled && strings.TrimSpace(s.BypassDomains) != "" {
		for _, line := range strings.Split(s.BypassDomains, "\n") {
			host := strings.TrimSpace(line)
			if host == "" || strings.HasPrefix(host, "#") {
				continue
			}
			ips, err := net.LookupIP(host)
			if err != nil {
				a.logf("直连域名 %s 解析失败：%v", host, err)
				continue
			}
			for _, ip := range ips {
				if v4 := ip.To4(); v4 != nil {
					opts.BypassCIDRs = append(opts.BypassCIDRs, v4.String()+"/32")
				}
			}
		}
	}
	return opts
}

// sampleLoop 每秒取一次接口计数，计算速率并累加本月流量。
func (a *Agent) sampleLoop(ctx context.Context) {
	defer a.wg.Done()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	persist := time.NewTicker(15 * time.Second)
	defer persist.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.mu.Lock()
			meter := a.meter
			prevRx, prevTx := a.lastRx, a.lastTx
			nodeID := a.nodeID
			a.mu.Unlock()
			if meter == nil {
				continue
			}
			rx := uint64(max64(0, meter.RX()))
			tx := uint64(max64(0, meter.TX()))
			var drx, dtx uint64
			if rx >= prevRx {
				drx = rx - prevRx
			}
			if tx >= prevTx {
				dtx = tx - prevTx
			}
			a.mu.Lock()
			a.lastRx, a.lastTx = rx, tx
			a.rxRate, a.txRate = int64(drx), int64(dtx)
			a.sessRx += int64(drx)
			a.sessTx += int64(dtx)
			a.mu.Unlock()
			a.ledger.add(nodeID, model.Month(time.Now()), int64(dtx), int64(drx))
			a.mu.Lock()
			a.monthUsed = a.ledger.used(nodeID, model.Month(time.Now()))
			a.mu.Unlock()
		case <-persist.C:
			a.ledger.save()
		}
	}
}

// telemetryLoop 每 15 秒上报一次本月累计流量，直到断开。
func (a *Agent) telemetryLoop(ctx context.Context, node *model.Node) {
	defer a.wg.Done()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			a.reportOnce(node, false)
			return
		case <-ticker.C:
			a.reportOnce(node, true)
		}
	}
}

// reportOnce 上报一次统计；后台要求阻断时会主动断开。
func (a *Agent) reportOnce(node *model.Node, connected bool) {
	key, err := cloud.DeviceKey()
	if err != nil {
		a.logf("设备标识不可用，跳过统计上报：%v", err)
		return
	}
	month := model.Month(time.Now())
	up, down := a.ledger.totals(node.ID, month)
	seq := a.ledger.nextSeq()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	reply, err := a.client.Telemetry(ctx, cloud.Report{
		NodeID:    node.ID,
		Month:     month,
		DeviceKey: key,
		Sequence:  seq,
		Upload:    up,
		Download:  down,
		Connected: connected,
		Available: true,
	})
	if err != nil {
		a.logf("流量统计上报失败：%v", err)
		return
	}
	if reply != nil && reply.Usage != nil {
		a.mu.Lock()
		a.monthUsed = reply.Usage.Used
		a.monthLimit = reply.Usage.Limit
		if u := a.nodeUsageLocked(node.ID); u != nil {
			*u = *reply.Usage
		}
		a.mu.Unlock()
	}
	if reply != nil && reply.Block {
		a.logf("后台要求断开：该节点本月流量已用完")
		a.mu.Lock()
		a.errMsg = "该节点本月流量已用完"
		a.stage = "流量已用完"
		a.mu.Unlock()
		a.Disconnect()
	}
}

func (a *Agent) nodeUsageLocked(id string) *model.Usage {
	for _, n := range a.nodes {
		if n.ID == id {
			if n.Usage == nil {
				n.Usage = &model.Usage{Configured: true}
			}
			return n.Usage
		}
	}
	return nil
}

// Probe 测速：对每台节点做一次 TCP 443 握手，返回毫秒数。
func (a *Agent) Probe(ids []string) map[string]ProbeResult {
	a.mu.Lock()
	nodes := append([]*model.Node(nil), a.nodes...)
	a.mu.Unlock()
	if len(ids) == 0 {
		for _, n := range nodes {
			ids = append(ids, n.ID)
		}
	}
	wanted := map[string]bool{}
	for _, id := range ids {
		wanted[id] = true
	}
	out := map[string]ProbeResult{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	// 同时最多探 6 台：节点多时一并发起，本地出口的排队时间会被算进结果。
	sem := make(chan struct{}, 6)
	a.probeMu.Lock()
	if a.probeDNS == nil {
		a.probeDNS = map[string]dnsEntry{}
	}
	a.probeMu.Unlock()

	for _, n := range nodes {
		if !wanted[n.ID] {
			continue
		}
		// 60 秒内的结果直接复用。
		a.probeMu.Lock()
		if prev, ok := a.lastProbe[n.ID]; ok && time.Since(prev.At) < 60*time.Second {
			out[n.ID] = prev
			a.probeMu.Unlock()
			continue
		}
		a.probeMu.Unlock()

		wg.Add(1)
		go func(node *model.Node) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			res := a.probeOne(node)
			mu.Lock()
			out[node.ID] = res
			mu.Unlock()
			a.probeMu.Lock()
			a.lastProbe[node.ID] = res
			a.probeMu.Unlock()
		}(n)
	}
	wg.Wait()
	return out
}

// probeOne 只做 TCP 三次握手：不建 TLS、不发 HTTP，握手耗时就是链路往返时间。
func (a *Agent) probeOne(node *model.Node) ProbeResult {
	res := ProbeResult{At: time.Now()}
	addrs := a.probeAddrs(node.Host, node.Port)
	ms, err := rttWithSample(func() (int64, error) {
		return dialBest(addrs, 1500*time.Millisecond)
	})
	if err != nil {
		res.Error = "连接失败"
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			res.Error = "超时"
		}
		return res
	}
	res.MS = ms
	return res
}

// rttWithSample 先测一次；很快（≤150ms）就直接用，慢就再测两次取最小值。
//
// 为什么要取最小值：SYN 丢一个包时 TCP 会等 1 秒才重传，单次采样就会稳定
// 显示「1000ms 以上」，而真实 RTT 可能只有 80ms。最小值最接近链路本身。
func rttWithSample(sample func() (int64, error)) (int64, error) {
	best, err := sample()
	if err != nil {
		return 0, err
	}
	if best <= 150 {
		return best, nil
	}
	for i := 0; i < 2; i++ {
		if ms, e := sample(); e == nil && ms < best {
			best = ms
		}
	}
	return best, nil
}

// dialBest 并发探测所有候选地址，返回最快的一次握手耗时。
//
// 一个域名常常解析出多个 IP，其中个别不可达（被墙、已下线）。逐个串行试会
// 把「等待超时」的时间算进延迟；并发探完取最小值，才是链路真实水平。
func dialBest(addrs []string, timeout time.Duration) (int64, error) {
	if len(addrs) == 0 {
		return 0, errors.New("没有可用的节点地址")
	}
	type outcome struct {
		ms  int64
		err error
	}
	results := make(chan outcome, len(addrs))
	for _, addr := range addrs {
		go func(target string) {
			ms, err := tcpOnce(target, timeout)
			results <- outcome{ms: ms, err: err}
		}(addr)
	}
	best := int64(-1)
	var lastErr error
	for range addrs {
		item := <-results
		if item.err != nil {
			lastErr = item.err
			continue
		}
		if best < 0 || item.ms < best {
			best = item.ms
		}
	}
	if best < 0 {
		if lastErr == nil {
			lastErr = errors.New("连接失败")
		}
		return 0, lastErr
	}
	return best, nil
}

// tcpOnce 是一次握手的耗时，失败立刻返回，不重试。
func tcpOnce(addr string, timeout time.Duration) (int64, error) {
	start := time.Now()
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return 0, err
	}
	ms := time.Since(start).Milliseconds()
	_ = conn.Close()
	if ms < 0 {
		ms = 0
	}
	return ms, nil
}

// probeAddrs 返回节点所有候选的 ip:port。
//
// 域名解析结果缓存 10 分钟：DNS 往往比握手慢一个数量级（实测失效域名要 696ms），
// 每次都重新解析会让「延迟」变成 DNS 耗时。解析失败时退回原域名，交给 Dial 自己处理。
func (a *Agent) probeAddrs(host string, port int) []string {
	portText := strconv.Itoa(port)
	if net.ParseIP(host) != nil {
		return []string{net.JoinHostPort(host, portText)}
	}

	a.probeMu.Lock()
	if e, ok := a.probeDNS[host]; ok && len(e.ips) > 0 && time.Since(e.at) < 10*time.Minute {
		ips := append([]string(nil), e.ips...)
		a.probeMu.Unlock()
		return hostPorts(ips, portText)
	}
	a.probeMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	found, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(found) == 0 {
		return []string{net.JoinHostPort(host, portText)}
	}
	ips := make([]string, 0, len(found))
	seen := map[string]bool{}
	for _, item := range found {
		ip := item.IP.String()
		if seen[ip] {
			continue
		}
		seen[ip] = true
		ips = append(ips, ip)
		if len(ips) == 4 { // 最多探 4 个，够覆盖常见多 A 记录
			break
		}
	}
	a.probeMu.Lock()
	a.probeDNS[host] = dnsEntry{ips: ips, at: time.Now()}
	a.probeMu.Unlock()
	return hostPorts(ips, portText)
}

func hostPorts(ips []string, port string) []string {
	out := make([]string, 0, len(ips))
	for _, ip := range ips {
		out = append(out, net.JoinHostPort(ip, port))
	}
	return out
}

// warmDNS 提前把节点域名解析好：用户第一次点「测速」时就不必等 DNS。
func (a *Agent) warmDNS(nodes []*model.Node) {
	for _, n := range nodes {
		if n == nil || n.Host == "" {
			continue
		}
		_ = a.probeAddrs(n.Host, n.Port)
	}
}

// LogTail 返回最近的日志行。
func (a *Agent) LogTail(lines int) []string {
	return a.log.tail(lines)
}

func (a *Agent) setState(state, stage, errMsg string) {
	a.mu.Lock()
	a.state = state
	a.stage = stage
	if errMsg != "" {
		a.errMsg = errMsg
	}
	if state == StateIdle {
		a.nodeID = ""
		a.since = time.Time{}
		a.rxRate, a.txRate = 0, 0
	}
	a.mu.Unlock()
}

func (a *Agent) clearSession() {
	a.mu.Lock()
	a.dev = nil
	a.meter = nil
	a.tunnelIP = ""
	a.dnsText = ""
	a.monthUsed = 0
	a.monthLimit = 0
	a.mu.Unlock()
}

func (a *Agent) stopped() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.stopFlag
}

func (a *Agent) findNode(id string) *model.Node {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, n := range a.nodes {
		if n.ID == id {
			return n
		}
	}
	return nil
}

// retryable 判断错误是否值得自动重连。
func retryable(err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, session.ErrAuth),
		errors.Is(err, session.ErrCertificate),
		errors.Is(err, session.ErrRejected),
		errors.Is(err, session.ErrCanceled):
		return false
	}
	return true
}

// SortNodes 按后台给出的顺序稳定排序。
func SortNodes(nodes []*model.Node) {
	sort.SliceStable(nodes, func(i, j int) bool { return nodes[i].Order < nodes[j].Order })
}

// --- 设置持久化 ---

func loadSettings(path string) Settings {
	var s Settings
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &s)
	}
	return s
}

func saveSettings(path string, s Settings) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
