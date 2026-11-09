// SPDX-License-Identifier: GPL-2.0-or-later
package agent

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"qingyou/qyvpn/internal/paths"
)

const testUserToken = "0123456789abcdef0123456789abcdef"

// TestUserTokenRoundTrip 登录态必须能跨内核重启保留，且文件只有属主可读。
func TestUserTokenRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("QYVPN_STATE_DIR", dir)
	if err := paths.EnsureStateDir(); err != nil {
		t.Fatal(err)
	}

	if got := loadUserToken(); got != "" {
		t.Fatalf("初始状态应为未登录，却读到 %q", got)
	}
	if err := saveUserToken(testUserToken); err != nil {
		t.Fatalf("保存登录态：%v", err)
	}
	if got := loadUserToken(); got != testUserToken {
		t.Fatalf("读回登录态 = %q", got)
	}

	path := filepath.Join(dir, "user.token")
	if info, err := os.Stat(path); err != nil {
		t.Fatalf("登录态文件不存在：%v", err)
	} else if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("登录态文件权限 = %v，应为 0600", info.Mode().Perm())
	}

	if err := clearUserToken(); err != nil {
		t.Fatalf("清除登录态：%v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("退出登录后文件应被删除，err=%v", err)
	}
	if got := loadUserToken(); got != "" {
		t.Fatalf("清除后仍能读到 %q", got)
	}
}

// TestNewRestoresLogin 内核重启后应自动带上上次的令牌，界面不必再登一次。
func TestNewRestoresLogin(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("QYVPN_STATE_DIR", dir)
	if err := paths.EnsureStateDir(); err != nil {
		t.Fatal(err)
	}
	if err := saveUserToken(testUserToken); err != nil {
		t.Fatal(err)
	}

	a, err := New("https://example.com", "client-token-1234567890")
	if err != nil {
		t.Fatalf("创建内核：%v", err)
	}
	defer a.Close()

	if got := a.Client().UserToken(); got != testUserToken {
		t.Fatalf("启动时未恢复登录态：%q", got)
	}
	st := a.Status()
	if !st.LoggedIn {
		t.Fatal("Status.LoggedIn 应为 true")
	}
	if st.NeedLogin {
		t.Fatal("已登录时 NeedLogin 应为 false")
	}
}

// TestStatusNeedsLoginWhenAnonymous 未登录时必须明确告知界面「要登录」，
// 否则界面只能显示「暂无节点」，用户完全不知道发生了什么。
func TestStatusNeedsLoginWhenAnonymous(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("QYVPN_STATE_DIR", dir)

	a, err := New("https://example.com", "client-token-1234567890")
	if err != nil {
		t.Fatalf("创建内核：%v", err)
	}
	defer a.Close()

	st := a.Status()
	if st.LoggedIn {
		t.Fatal("未登录时 LoggedIn 应为 false")
	}
	if !st.NeedLogin {
		t.Fatal("未登录时需要 NeedLogin=true，界面据此显示登录页")
	}
}
