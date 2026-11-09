// SPDX-License-Identifier: GPL-2.0-or-later
package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"qingyou/qyvpn/internal/cloud"
)

// privateToken 从环境变量或仓库的 .private/client.properties 读取客户端令牌。
func privateToken() (string, string) {
	if v := os.Getenv("QYVPN_API_TOKEN"); v != "" {
		base := os.Getenv("QYVPN_API_BASE")
		if base == "" {
			base = "https://vpn.qo8.cn"
		}
		return v, base
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", ""
	}
	for i := 0; i < 8; i++ {
		candidate := filepath.Join(dir, ".private", "client.properties")
		if data, err := os.ReadFile(candidate); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "apiToken=") {
					return strings.TrimSpace(strings.SplitN(line, "=", 2)[1]), "https://vpn.qo8.cn"
				}
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", ""
}

// TestLiveHandshakeFromBackend 用后台真实下发的节点跑一次完整握手，验证
// 云接口 + TLS + SSTP + PPP + MSCHAPv2 + IPCP 全链路。没有令牌时自动跳过。
func TestLiveHandshakeFromBackend(t *testing.T) {
	token, base := privateToken()
	if token == "" {
		t.Skip("没有客户端令牌，跳过真实节点握手")
	}
	client, err := cloud.New(base, token)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cfg, err := client.Sync(ctx)
	if err != nil {
		t.Fatalf("拉取节点失败：%v", err)
	}
	var node = cfg.Nodes[0]
	for _, n := range cfg.Nodes {
		if n.BlockReason() == "" {
			node = n
			break
		}
	}
	t.Logf("使用节点：%s（%s:%d，账号 %s）", node.Name, node.Host, node.Port, node.Login())

	runCtx, runCancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer runCancel()
	start := time.Now()
	stage := make(chan string, 1)
	err = Run(runCtx, node, Options{
		MTU: 1400,
		Log: func(format string, args ...any) {
			t.Logf("[%5.1fs] %s", time.Since(start).Seconds(), fmt.Sprintf(format, args...))
		},
		OnIPCP: func(ip string, dns []string) {
			t.Logf("IPCP 分配地址 %s，DNS %v", ip, dns)
		},
		OnConnected: func() {
			select {
			case stage <- "connected":
			default:
			}
			runCancel()
		},
	})
	select {
	case s := <-stage:
		if s != "connected" {
			t.Fatalf("连接失败：%v", err)
		}
	default:
		t.Fatalf("握手未完成：%v", err)
	}
	if !errors.Is(err, ErrCanceled) {
		t.Fatalf("取消后应返回 ErrCanceled，实际：%v", err)
	}
}
