// SPDX-License-Identifier: GPL-2.0-or-later
package cloud

// user.go：用户登录态与账号信息。
//
// 后台从 1.9 起把节点按「用户套餐」下发，鉴权变成两层：
//
//	Authorization: Bearer <构建期注入的客户端令牌>   证明请求来自官方客户端
//	X-User-Token:  <登录后拿到的用户令牌>           决定下发哪些节点、剩余多少流量
//
// 只带客户端令牌请求 /api/app/config 会得到 needLogin=true 与空节点列表，
// 所以客户端必须先登录，再把用户令牌带给后续所有接口。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// LoginReply 是 /api/app/user/login 的响应：令牌 + 用户视图（字段见后台 userView）。
type LoginReply struct {
	Token string          `json:"token"`
	User  json.RawMessage `json:"user"`
}

// SetUserToken 设置用户令牌，空字符串表示退出登录。
func (c *Client) SetUserToken(token string) {
	c.userMu.Lock()
	c.user = strings.TrimSpace(token)
	c.userMu.Unlock()
}

// UserToken 返回当前的用户令牌。
func (c *Client) UserToken() string {
	c.userMu.RLock()
	defer c.userMu.RUnlock()
	return c.user
}

// Login 用用户名口令换用户令牌。platform 会记到后台用户档案里（ios/android/...），
// 后台据此统计各端活跃情况；传空字符串时后台按 User-Agent 推断。
func (c *Client) Login(ctx context.Context, username, password, platform string) (*LoginReply, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return nil, errors.New("请输入用户名和密码")
	}
	data, err := c.do(ctx, http.MethodPost, "/api/app/user/login", map[string]string{
		"username": username,
		"password": password,
		"platform": platform,
	}, 64<<10)
	if err != nil {
		return nil, userMessage(err)
	}
	var reply LoginReply
	if err := json.Unmarshal(data, &reply); err != nil {
		return nil, fmt.Errorf("登录响应格式不正确：%w", err)
	}
	if len(reply.Token) < 16 {
		return nil, errors.New("登录响应缺少用户令牌")
	}
	c.SetUserToken(reply.Token)
	return &reply, nil
}

// Register 注册新账号。用户名可用、口令强度、邮箱格式都由后台校验，
// 失败时返回的错误文本可直接展示给用户（后台给的是中文提示）。
func (c *Client) Register(ctx context.Context, username, password, email, platform string) (*LoginReply, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return nil, errors.New("请输入用户名和密码")
	}
	data, err := c.do(ctx, http.MethodPost, "/api/app/user/register", map[string]string{
		"username": username,
		"password": password,
		"email":    strings.TrimSpace(email),
		"platform": platform,
	}, 64<<10)
	if err != nil {
		return nil, userMessage(err)
	}
	var reply LoginReply
	if err := json.Unmarshal(data, &reply); err != nil {
		return nil, fmt.Errorf("注册响应格式不正确：%w", err)
	}
	if len(reply.Token) < 16 {
		return nil, errors.New("注册成功但响应缺少用户令牌，请直接登录")
	}
	c.SetUserToken(reply.Token)
	return &reply, nil
}

// RedeemReply 是卡密兑换的结果：兑换后的用户视图 + 本次增加的时长与到期时间。
type RedeemReply struct {
	User   json.RawMessage `json:"user"`
	Days   int             `json:"days"`
	Expire string          `json:"expire"`
}

// Redeem 用卡密给当前账号续期。必须已登录（后台按 X-User-Token 识别账号），
// 卡密无效、已使用、被停用等情况的提示同样来自后台。
func (c *Client) Redeem(ctx context.Context, code string) (*RedeemReply, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return nil, errors.New("请输入卡密")
	}
	data, err := c.do(ctx, http.MethodPost, "/api/app/user/redeem", map[string]string{
		"code": code,
	}, 64<<10)
	if err != nil {
		return nil, userMessage(err)
	}
	var reply RedeemReply
	if err := json.Unmarshal(data, &reply); err != nil {
		return nil, fmt.Errorf("兑换响应格式不正确：%w", err)
	}
	return &reply, nil
}

// Logout 通知后台吊销当前用户令牌，并清空本地登录态。
// 网络失败也清空本地令牌：否则用户会卡在「登不出去」的状态里。
func (c *Client) Logout(ctx context.Context) error {
	if c.UserToken() == "" {
		return nil
	}
	_, err := c.do(ctx, http.MethodPost, "/api/app/user/logout", map[string]string{}, 16<<10)
	c.SetUserToken("")
	return err
}

// Me 拉取当前用户信息（账号、会员到期时间、套餐）。未登录或令牌失效时返回
// ErrNeedLogin，调用方据此回到登录页。
func (c *Client) Me(ctx context.Context) (json.RawMessage, error) {
	data, err := c.do(ctx, http.MethodGet, "/api/app/user/me", nil, 64<<10)
	if err != nil {
		return nil, err
	}
	var reply struct {
		User json.RawMessage `json:"user"`
	}
	if err := json.Unmarshal(data, &reply); err != nil {
		return nil, fmt.Errorf("账号信息格式不正确：%w", err)
	}
	if len(reply.User) == 0 {
		return nil, ErrNeedLogin
	}
	return reply.User, nil
}

// userMessage 把后台的业务提示原样交给界面。
//
// BackendError 的 Error() 形如「后台请求失败（HTTP 401）：用户名或密码不正确」，
// 登录/注册/兑换这类接口的提示本身就是给用户看的，套上 HTTP 状态码只会让人困惑。
func userMessage(err error) error {
	var be *BackendError
	if errors.As(err, &be) {
		if body := strings.TrimSpace(be.Body); body != "" {
			// 保留底层 BackendError：NeedLogin 还要靠 HTTP 状态码判断「令牌失效」，
			// 只换掉展示文案，不破坏错误链。
			return &userFacingError{msg: body, err: be}
		}
	}
	return err
}

// userFacingError 用后台的中文提示作为展示文案，同时把原始错误挂在链上。
type userFacingError struct {
	msg string
	err error
}

func (e *userFacingError) Error() string { return e.msg }
func (e *userFacingError) Unwrap() error { return e.err }

// ErrNeedLogin 表示需要用户登录（未登录、令牌过期或被停用）。
var ErrNeedLogin = errors.New("请先登录账号")

// NeedLogin 判断错误是不是「需要登录」。后台对未登录的写接口返回 401 +
// {"code":"user"}，对读接口则直接下发 needLogin=true，两种都要识别。
func NeedLogin(err error) bool {
	var be *BackendError
	if errors.As(err, &be) {
		return be.Status == http.StatusUnauthorized
	}
	return errors.Is(err, ErrNeedLogin)
}
