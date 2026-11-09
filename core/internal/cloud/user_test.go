// SPDX-License-Identifier: GPL-2.0-or-later
package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// newTestClient 绕过 New 的 https 校验，直接构造一个指向本地测试服务器的客户端。
func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("解析测试地址：%v", err)
	}
	return &Client{base: u, token: "client-token", http: srv.Client()}
}

func TestLoginSendsPlatformAndStoresToken(t *testing.T) {
	var (
		gotBody  map[string]string
		gotAuth  string
		gotUser  string
		meCalled bool
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/app/user/login":
			if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
				t.Errorf("解析登录请求体：%v", err)
			}
			gotAuth = r.Header.Get("Authorization")
			_, _ = w.Write([]byte(`{"token":"0123456789abcdef0123","user":{"username":"alice","daysLeft":30}}`))
		case "/api/app/user/me":
			meCalled = true
			gotUser = r.Header.Get("X-User-Token")
			_, _ = w.Write([]byte(`{"user":{"username":"alice"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	reply, err := c.Login(context.Background(), " alice ", "secret", "ios")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if gotBody["username"] != "alice" || gotBody["password"] != "secret" || gotBody["platform"] != "ios" {
		t.Fatalf("登录请求体 = %v，用户名应当去掉首尾空格、platform 必须是 ios", gotBody)
	}
	if gotAuth != "Bearer client-token" {
		t.Fatalf("Authorization = %q，客户端令牌必须始终携带", gotAuth)
	}
	if c.UserToken() != reply.Token {
		t.Fatal("登录成功后应当记住用户令牌")
	}
	if len(reply.User) == 0 {
		t.Fatal("登录响应里的 user 没有透传")
	}

	// 后续请求必须自动带上 X-User-Token，否则后台不下发节点。
	if _, err := c.Me(context.Background()); err != nil {
		t.Fatalf("Me: %v", err)
	}
	if !meCalled || gotUser != reply.Token {
		t.Fatalf("X-User-Token = %q，期望 %q", gotUser, reply.Token)
	}
}

func TestLoginRejectsMissingToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"user":{"username":"alice"}}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	if _, err := c.Login(context.Background(), "alice", "secret", "ios"); err == nil {
		t.Fatal("响应缺少令牌时应当报错")
	}
}

func TestLoginRequiresCredentials(t *testing.T) {
	c := &Client{}
	if _, err := c.Login(context.Background(), "  ", "secret", "ios"); err == nil {
		t.Fatal("用户名为空时应当本地拦下，不发请求")
	}
	if _, err := c.Login(context.Background(), "alice", "", "ios"); err == nil {
		t.Fatal("密码为空时应当本地拦下")
	}
}

func TestLoginSurfacesBackendMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"用户名或密码不正确"}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	_, err := c.Login(context.Background(), "alice", "bad", "ios")
	if err == nil || !NeedLogin(err) {
		t.Fatalf("err = %v，应当被识别为需要登录", err)
	}
	var be *BackendError
	if !errors.As(err, &be) || be.Status != http.StatusUnauthorized {
		t.Fatalf("err = %v，期望 401 后端错误", err)
	}
	if err.Error() == "" || !strings.Contains(err.Error(), "用户名或密码不正确") {
		t.Fatalf("err = %v，应当带上后台的中文提示", err)
	}
}

// 后台不可达时也要清掉本地令牌，否则用户永远退不出登录态。
func TestLogoutClearsTokenOnFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"内部错误"}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	c.SetUserToken("0123456789abcdef0123")
	if err := c.Logout(context.Background()); err == nil {
		t.Fatal("后台返回 500 时应当把错误抛给上层")
	}
	if c.UserToken() != "" {
		t.Fatal("退出登录后本地令牌必须清空")
	}
}

func TestMeWithoutTokenParsesEmptyUser(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	if _, err := c.Me(context.Background()); !errors.Is(err, ErrNeedLogin) {
		t.Fatalf("err = %v，期望 ErrNeedLogin", err)
	}
}
