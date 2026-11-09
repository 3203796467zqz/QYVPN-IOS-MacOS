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

func TestRegisterSendsEmailAndStoresToken(t *testing.T) {
	var body map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/app/user/register" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("解析注册请求体：%v", err)
		}
		_, _ = w.Write([]byte(`{"token":"abcdef0123456789abcd","user":{"username":"bob"}}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	if _, err := c.Register(context.Background(), " bob ", "pass1234", "bob@example.com", "macos"); err != nil {
		t.Fatalf("注册失败：%v", err)
	}
	if body["username"] != "bob" {
		t.Fatalf("用户名未去空格：%q", body["username"])
	}
	if body["platform"] != "macos" || body["email"] != "bob@example.com" {
		t.Fatalf("注册参数不对：%+v", body)
	}
	if c.UserToken() != "abcdef0123456789abcd" {
		t.Fatalf("登录态未保存：%q", c.UserToken())
	}
}

func TestRedeemNormalizesCodeAndKeepsUserToken(t *testing.T) {
	var (
		body map[string]string
		seen string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/app/user/redeem" {
			http.NotFound(w, r)
			return
		}
		seen = r.Header.Get("X-User-Token")
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("解析兑换请求体：%v", err)
		}
		_, _ = w.Write([]byte(`{"user":{"username":"alice","daysLeft":60},"days":30,"expire":"2027-01-08"}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	c.SetUserToken("user-token-value")
	reply, err := c.Redeem(context.Background(), "  abcd1234ef567890 ")
	if err != nil {
		t.Fatalf("兑换失败：%v", err)
	}
	if body["code"] != "ABCD1234EF567890" {
		t.Fatalf("卡密未规范化为大写去空格：%q", body["code"])
	}
	if seen != "user-token-value" {
		t.Fatalf("未带用户令牌：%q", seen)
	}
	if reply.Days != 30 || reply.Expire != "2027-01-08" {
		t.Fatalf("兑换结果 = %+v", reply)
	}
}

func TestRedeemSurfacesBackendMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"该卡密已被使用"}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	c.SetUserToken("user-token-value")
	_, err := c.Redeem(context.Background(), "ABCD1234EF567890")
	if err == nil || !strings.Contains(err.Error(), "该卡密已被使用") {
		t.Fatalf("错误信息未透出：%v", err)
	}
}
