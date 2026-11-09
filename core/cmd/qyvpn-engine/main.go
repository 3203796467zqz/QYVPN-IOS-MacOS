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
	dir := filepath.Dir(paths.HelperBinary())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建 %s 失败：%w", dir, err)
	}
	_ = os.Chown(dir, 0, 0)
	_ = os.Chmod(dir, 0o755)

	// 复制自身到特权目录；若已经在目标位置运行则跳过，避免覆盖正在执行的二进制。
	if filepath.Clean(self) != filepath.Clean(paths.HelperBinary()) {
		data, err := os.ReadFile(self)
		if err != nil {
			return fmt.Errorf("读取内核可执行文件失败：%w", err)
		}
		if err := os.WriteFile(paths.HelperBinary(), data, 0o755); err != nil {
			return fmt.Errorf("写入 %s 失败：%w", paths.HelperBinary(), err)
		}
	}
	if err := os.Chmod(paths.HelperBinary(), 0o755); err != nil {
		return err
	}
	_ = os.Chown(paths.HelperBinary(), 0, 0)
	// 从「下载来的 App」里带出的 com.apple.quarantine 会让 launchd 拒绝加载，
	// 表现就是 Bootstrap failed: 5: Input/output error。复制出的新文件通常没有，
	// 但用户从浏览器/网盘解压时属性可能被继承，这里统一清一遍。
	clearXattrs(paths.HelperBinary())

	if err := ipc.WriteToken(token); err != nil {
		return err
	}

	// launchd 要能打开 StandardOutPath，否则可能因为打不开日志而加载失败。
	logPath := "/Library/Logs/cn.qo8.qyvpn.engine.log"
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return fmt.Errorf("创建 /Library/Logs 失败：%w", err)
	}
	if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		_ = f.Close()
	}

	// ProcessType 不要写 Interactive：那是给与窗口服务器交互的作业用的，
	// 系统域的守护进程用默认值即可，写了反而可能被 launchd 拒绝。
	plist := helperPlist(paths.HelperLabel, paths.HelperBinary(), logPath)

	plistPath := paths.HelperPlist()
	if err := os.WriteFile(plistPath, []byte(plist), 0o644); err != nil {
		return fmt.Errorf("写入 %s 失败：%w", plistPath, err)
	}
	// launchd 要求 plist 属于 root:wheel 且不可被其他用户写。
	_ = os.Chown(plistPath, 0, 0)
	_ = os.Chmod(plistPath, 0o644)
	clearXattrs(plistPath)

	// 1) 先卸掉可能残留的旧注册（上次安装失败、或换过路径）。
	runQuiet("launchctl", "bootout", "system/"+paths.HelperLabel)
	// 2) 如果这个 label 曾被 disable，bootstrap 会直接失败，先确保是启用状态。
	runQuiet("launchctl", "enable", "system/"+paths.HelperLabel)

	out, err := runCmd("launchctl", "bootstrap", "system", plistPath)
	if err != nil {
		// 3) 新接口失败时退回旧接口再试一次（老系统与个别被托管的环境仍吃这一套）。
		if out2, err2 := runCmd("launchctl", "load", "-w", "-F", plistPath); err2 != nil {
			return fmt.Errorf("launchctl bootstrap 失败：%s；load 兜底也失败：%s%s",
				orNone(out), orNone(out2), diagnose())
		}
	}

	// 4) 不轻信返回码：确认服务真的注册上了，否则界面会一直显示「未安装」。
	if out, err := runCmd("launchctl", "print", "system/"+paths.HelperLabel); err != nil {
		return fmt.Errorf("launchctl 报告成功但服务未注册：%s%s", orNone(out), diagnose())
	}
	return nil
}

// helperPlist 生成 LaunchDaemon 描述。
//
// 单独抽出来是为了能单测：launchd 对 plist 的要求很严（必须是合法 XML、Label 与方法名匹配），
// 一旦写错只会得到 Bootstrap failed: 5，很难从现象反推。
func helperPlist(label, binary, logPath string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
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
  <key>ThrottleInterval</key><integer>5</integer>
  <key>StandardOutPath</key><string>%s</string>
  <key>StandardErrorPath</key><string>%s</string>
</dict>
</plist>
`, label, binary, logPath, logPath)
}

func runCmd(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func runQuiet(name string, args ...string) {
	_ = exec.Command(name, args...).Run()
}

// clearXattrs 清掉文件上的扩展属性（尤其是 com.apple.quarantine）。
func clearXattrs(path string) {
	if _, err := os.Stat("/usr/bin/xattr"); err != nil {
		return
	}
	_ = exec.Command("/usr/bin/xattr", "-c", path).Run()
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "（无输出）"
	}
	return s
}

// diagnose 收集安装失败时的现场信息，一次性随错误抛出，省得来回问。
func diagnose() string {
	var b strings.Builder
	if info, err := os.Stat(paths.HelperBinary()); err == nil {
		fmt.Fprintf(&b, "；内核 %d 字节 权限 %v", info.Size(), info.Mode().Perm())
	} else {
		fmt.Fprintf(&b, "；内核不存在：%v", err)
	}
	if info, err := os.Stat(paths.HelperPlist()); err == nil {
		fmt.Fprintf(&b, "；plist %d 字节 权限 %v", info.Size(), info.Mode().Perm())
	} else {
		fmt.Fprintf(&b, "；plist 不存在：%v", err)
	}
	if out, err := runCmd("launchctl", "print-disabled", "system"); err == nil {
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, paths.HelperLabel) {
				fmt.Fprintf(&b, "；disabled 覆盖：%s", strings.TrimSpace(line))
			}
		}
	}
	if data, err := os.ReadFile("/Library/Logs/cn.qo8.qyvpn.engine.log"); err == nil && len(data) > 0 {
		tail := data
		if len(tail) > 300 {
			tail = tail[len(tail)-300:]
		}
		fmt.Fprintf(&b, "；内核日志尾部：%s", strings.TrimSpace(string(tail)))
	}
	return b.String()
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
