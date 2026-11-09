// SPDX-License-Identifier: GPL-2.0-or-later
// Package cloud 访问清幽私人云的后台客户端接口（只读配置/状态 + 统计上报）。
package cloud

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"qingyou/qyvpn/internal/model"
)

// DefaultToken 在构建时通过 -ldflags "-X qingyou/qyvpn/internal/cloud.DefaultToken=..." 注入。
// 发布包里的令牌只有「读配置/读状态/写客户端统计」权限，不含后台管理权限。
var DefaultToken = ""

// DefaultBase 是后台地址，换域名只需改这一处并重新构建。
var DefaultBase = "https://vpn.qo8.cn"

// Product 是统计上报与日志里区分客户端平台的标识。iOS 走 NetworkExtension，
// 由 mobile 包在初始化时改成 "iOS"，其余平台保持 "macOS"。
var Product = "macOS"

// BackendError 表示后台返回的非 2xx。
type BackendError struct {
	Status int
	Body   string
}

func (e *BackendError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("后台请求失败（HTTP %d）", e.Status)
	}
	return fmt.Sprintf("后台请求失败（HTTP %d）：%s", e.Status, e.Body)
}

// Permanent 表示这条错误重试也不会成功（节点已不存在、参数被拒）。
func (e *BackendError) Permanent() bool {
	switch e.Status {
	case 400, 401, 403, 404, 410:
		return true
	}
	return false
}

// Client 是后台客户端。全部请求不走系统代理，直连后台域名。
type Client struct {
	base  *url.URL
	token string
	http  *http.Client

	// user 是登录后的用户令牌（X-User-Token）。后台 1.9 起节点按用户套餐下发，
	// 只带客户端令牌的请求会拿到 needLogin=true 和空节点列表。
	userMu sync.RWMutex
	user   string
}

// New 创建客户端。base 必须是 https 域名（不带路径）。
func New(base, token string) (*Client, error) {
	if base == "" {
		base = DefaultBase
	}
	if token == "" {
		token = DefaultToken
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.Host == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("后台地址必须是 HTTPS 域名，不含路径或查询参数")
	}
	if u.User != nil {
		return nil, errors.New("后台地址不能包含用户信息")
	}
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("客户端访问令牌缺失，请使用官方发布包或联系管理员")
	}
	transport := &http.Transport{
		Proxy:               nil, // 连接后台永远走物理网络，不套自己的隧道
		DialContext:         (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
		ForceAttemptHTTP2:   true,
		MaxIdleConns:        4,
		IdleConnTimeout:     90 * time.Second,
	}
	return &Client{
		base:  u,
		token: token,
		http: &http.Client{
			Transport: transport,
			Timeout:   20 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

// Base 返回后台地址（用于日志与界面展示）。
func (c *Client) Base() string { return c.base.String() }

func (c *Client) do(ctx context.Context, method, path string, body any, limit int64) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base.String()+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	c.userMu.RLock()
	user := c.user
	c.userMu.RUnlock()
	if user != "" {
		req.Header.Set("X-User-Token", user)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Qingyou-"+Product+"/"+Version)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("后台响应过大")
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, &BackendError{Status: resp.StatusCode, Body: errorMessage(data)}
	}
	return data, nil
}

func errorMessage(data []byte) string {
	var v struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(data, &v) == nil && v.Error != "" {
		return v.Error
	}
	s := strings.TrimSpace(string(data))
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// Config 拉取节点列表。
func (c *Client) Config(ctx context.Context) (*model.Config, error) {
	data, err := c.do(ctx, http.MethodGet, "/api/app/config", nil, 1<<20)
	if err != nil {
		return nil, err
	}
	return model.UnmarshalConfig(data)
}

// Status 拉取节点在线状态与用量。
func (c *Client) Status(ctx context.Context) (*model.StatusConfig, error) {
	data, err := c.do(ctx, http.MethodGet, "/api/app/status", nil, 256<<10)
	if err != nil {
		return nil, err
	}
	var st model.StatusConfig
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// Sync 拉取配置并合并在线状态。
func (c *Client) Sync(ctx context.Context) (*model.Config, error) {
	cfg, err := c.Config(ctx)
	if err != nil {
		return nil, err
	}
	if st, err := c.Status(ctx); err == nil {
		model.MergeStatus(cfg, st)
	} else {
		model.Stale(cfg)
	}
	return cfg, nil
}

// Report 是一次客户端统计上报，累计值按「节点 + 月」单调递增。
type Report struct {
	NodeID    string `json:"nodeId"`
	Month     string `json:"month"`
	DeviceKey string `json:"deviceKey"`
	Sequence  int64  `json:"sequence"`
	Upload    int64  `json:"uploadBytes"`
	Download  int64  `json:"downloadBytes"`
	Connected bool   `json:"connected"`
	Estimated bool   `json:"estimated"`
	Available bool   `json:"available"`
}

// ReportReply 是上报的响应，block 表示后台认为该节点已不可用。
type ReportReply struct {
	Block bool         `json:"block"`
	Usage *model.Usage `json:"usage,omitempty"`
}

// Telemetry 上报一次统计。
func (c *Client) Telemetry(ctx context.Context, r Report) (*ReportReply, error) {
	data, err := c.do(ctx, http.MethodPost, "/api/app/telemetry", r, 64<<10)
	if err != nil {
		return nil, err
	}
	var reply ReportReply
	if err := json.Unmarshal(data, &reply); err != nil {
		return nil, err
	}
	return &reply, nil
}

// Release 是后台下发的 macOS 更新信息。
type Release struct {
	Version   string `json:"version"`
	Notes     string `json:"notes"`
	SHA256    string `json:"sha256"`
	Size      int64  `json:"size"`
	Published string `json:"published"`
}

// ErrNoReleaseChannel 表示后台还没有 macOS 更新通道。
var ErrNoReleaseChannel = errors.New("后台尚未开通 macOS 更新通道")

// CheckRelease 查询 macOS 更新。后台未开通时返回 ErrNoReleaseChannel，不视为故障。
func (c *Client) CheckRelease(ctx context.Context) (*Release, error) {
	data, err := c.do(ctx, http.MethodGet, "/api/app/macos-release", nil, 64<<10)
	if err != nil {
		var be *BackendError
		if errors.As(err, &be) && (be.Status == 404 || be.Status == 503) {
			return nil, ErrNoReleaseChannel
		}
		return nil, err
	}
	var reply struct {
		Release *Release `json:"release"`
	}
	if err := json.Unmarshal(data, &reply); err != nil {
		return nil, err
	}
	if reply.Release == nil || reply.Release.Version == "" {
		return nil, ErrNoReleaseChannel
	}
	return reply.Release, nil
}

// Download 下载更新包到内存，并校验大小与 SHA256。
func (c *Client) Download(ctx context.Context, rel *Release) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base.String()+"/api/app/macos/"+rel.SHA256, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("更新下载失败（HTTP %d），请重新检查更新", resp.StatusCode)
	}
	if resp.ContentLength > 0 && resp.ContentLength != rel.Size {
		return nil, errors.New("更新包大小与发布信息不符")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, rel.Size+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != rel.Size {
		return nil, errors.New("更新包下载不完整")
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != rel.SHA256 {
		return nil, errors.New("更新包校验失败，请重新下载")
	}
	return data, nil
}

// Version 是客户端版本，构建时可用 -ldflags -X 覆盖。
var Version = "1.0.0"

// DeviceKey 返回设备标识（64 位十六进制），与统计上报的 deviceKey 对应。
func DeviceKey() (string, error) {
	return deviceKeyFromMachine()
}
