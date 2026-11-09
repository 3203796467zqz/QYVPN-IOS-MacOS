// SPDX-License-Identifier: GPL-2.0-or-later
//go:build live

package agent

// 真实后台联调（默认不跑）：go test -tags live ./internal/agent/ -run TestLiveAccount -v
//
// 只验证「账号态」这条链路，不创建账号、不兑换卡密，避免在线上留数据：
//  1. 匿名同步必须拿到 needLogin=true 与空节点，界面据此显示登录页；
//  2. 错误口令必须把后台的中文提示原样带出来，而不是笼统的 401。
//
// 令牌从仓库的 .private/client.properties 读取（与发布包同一个客户端令牌）。

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func liveClientToken(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		path := filepath.Join(dir, ".private", "client.properties")
		if data, err := os.ReadFile(path); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if strings.HasPrefix(line, "apiToken=") {
					return strings.TrimSpace(strings.TrimPrefix(line, "apiToken="))
				}
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Skip("没有找到 .private/client.properties，跳过真实后台联调")
	return ""
}

func TestLiveAccountState(t *testing.T) {
	token := liveClientToken(t)
	t.Setenv("QYVPN_STATE_DIR", t.TempDir())

	a, err := New("https://vpn.qo8.cn", token)
	if err != nil {
		t.Fatalf("创建内核：%v", err)
	}
	defer a.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	// 1) 匿名：应拿到 needLogin，而不是报错
	if err := a.Refresh(ctx); err != nil {
		t.Fatalf("匿名同步失败：%v", err)
	}
	st := a.Status()
	if !st.NeedLogin {
		t.Fatalf("线上应要求登录，Status = %+v", st)
	}
	if n := len(a.Nodes()); n != 0 {
		t.Fatalf("未登录不应拿到节点，实际 %d 台", n)
	}
	t.Logf("匿名同步符合预期：needLogin=%v，节点 %d 台", st.NeedLogin, len(a.Nodes()))

	// 2) 错误口令：后台的中文提示要透出给界面
	go func() {
		time.Sleep(60 * time.Second)
		cancel()
	}()
	err = a.Login(ctx, "qy-no-such-user-2026", "definitely-wrong-password")
	if err == nil {
		t.Fatal("错误口令不应登录成功")
	}
	if !strings.Contains(err.Error(), "用户名或密码") {
		t.Fatalf("未透出后台提示：%v", err)
	}
	t.Logf("错误口令返回：%v", err)

	// 失败的登录不应在本地留下登录态
	if a.Client().UserToken() != "" || loadUserToken() != "" {
		t.Fatal("登录失败后不应留下用户令牌")
	}
}
