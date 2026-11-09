// SPDX-License-Identifier: GPL-2.0-or-later
// Package model 是客户端与后台之间的数据模型，字段与后台 /api/app/* 一一对应。
// 与 Windows 客户端 (windows/src/Models.cs) 保持同一套语义：本项目只有 SSTP 一条通道，
// 登录账号在 SSTP 下必须是“后台账号@HUB”。
package model

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Usage 是节点本月用量/在线数，对应后台 NodeUsage。
type Usage struct {
	Source     string `json:"source"`
	Estimated  bool   `json:"estimated"`
	Incomplete bool   `json:"incomplete"`
	Month      string `json:"month"`
	Upload     int64  `json:"uploadBytes"`
	Download   int64  `json:"downloadBytes"`
	Used       int64  `json:"usedBytes"`
	Limit      int64  `json:"limitBytes"`
	Online     int    `json:"online"`
	Fresh      bool   `json:"fresh"`
	Configured bool   `json:"configured"`
	Full       bool   `json:"full"`
	Exhausted  bool   `json:"exhausted"`
	Blocked    bool   `json:"blocked"`
}

// Node 是后台下发的一台 SSTP 节点。
type Node struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	Protocol       string  `json:"protocol"`
	Host           string  `json:"host"`
	Port           int     `json:"port"`
	Username       string  `json:"username"`
	Password       string  `json:"password,omitempty"`
	RemoteID       string  `json:"remoteId"`
	LocalID        string  `json:"localId"`
	CA             string  `json:"ca"`
	Icon           string  `json:"icon,omitempty"`
	SSTP           bool    `json:"sstp,omitempty"`
	SSTPHub        string  `json:"sstpHub,omitempty"`
	WindowsUser    string  `json:"windowsUsername,omitempty"`
	WindowsProto   string  `json:"windowsProtocol,omitempty"`
	Enabled        bool    `json:"enabled"`
	Order          int     `json:"order"`
	MaxUsers       int     `json:"maxUsers"`
	TrafficLimitGB float64 `json:"trafficLimitGB"`
	Usage          *Usage  `json:"usage,omitempty"`

	// Available 由客户端合并 /api/app/status 后得出，后台不下发。
	Available bool `json:"-"`
}

// Config 是 /api/app/config 的响应。
type Config struct {
	Nodes   []*Node      `json:"nodes"`
	Release *ReleaseInfo `json:"release,omitempty"`

	// 下面几项只在后台按「用户套餐」下发时出现（后台 1.9 起）：
	// NeedLogin 为真表示这次请求没有带有效的用户令牌，nodes 一定是空的，
	// 客户端应当回到登录页而不是提示「没有节点」。
	NeedLogin bool            `json:"needLogin,omitempty"`
	NoNodes   bool            `json:"noNodes,omitempty"`
	User      json.RawMessage `json:"user,omitempty"`
	Plan      json.RawMessage `json:"plan,omitempty"`
}

// StatusConfig 是 /api/app/status 的响应（只有 id / maxUsers / usage）。
type StatusConfig struct {
	Nodes []struct {
		ID       string `json:"id"`
		MaxUsers int    `json:"maxUsers"`
		Usage    *Usage `json:"usage"`
	} `json:"nodes"`
}

// ReleaseInfo 是 Android 端的发布信息（客户端只用它判断后台是否健康）。
type ReleaseInfo struct {
	Code      int    `json:"code"`
	Version   string `json:"version"`
	Notes     string `json:"notes"`
	SHA256    string `json:"sha256"`
	Size      int64  `json:"size"`
	Published string `json:"published"`
}

// HubName 返回 SoftEther 的 HUB 名，未下发时按部署脚本的固定值 VPN 处理。
func (n *Node) HubName() string {
	if h := strings.TrimSpace(n.SSTPHub); h != "" {
		return h
	}
	return "VPN"
}

// Login 返回 SSTP 登录账号：后台账号@HUB；已经是 user@HUB 形式时原样使用。
func (n *Node) Login() string {
	if n.WindowsUser != "" {
		return n.WindowsUser
	}
	if n.Username == "" {
		return ""
	}
	if strings.Contains(n.Username, "@") {
		return n.Username
	}
	return n.Username + "@" + n.HubName()
}

// BareLogin 返回不带 HUB 后缀的裸账号，用于账号格式的第二次尝试。
func (n *Node) BareLogin() string {
	if n.WindowsUser != "" || n.Username == "" || strings.Contains(n.Username, "@") {
		return ""
	}
	if n.Login() == n.Username {
		return ""
	}
	return n.Username
}

// RemoteIdentity 返回 TLS 校验使用的服务器身份（SNI / 证书域名）。
func (n *Node) RemoteIdentity() string {
	if n.RemoteID != "" {
		return n.RemoteID
	}
	return n.Host
}

var hostRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.\-]{0,252}$`)

// Validate 校验后台下发的节点字段，避免把异常数据带进连接流程。
func (n *Node) Validate() error {
	if n.ID == "" || len(n.ID) > 64 {
		return fmt.Errorf("节点标识无效")
	}
	if strings.TrimSpace(n.Name) == "" || len(n.Name) > 150 {
		return fmt.Errorf("节点名称无效")
	}
	if !hostRE.MatchString(n.Host) {
		return fmt.Errorf("节点地址无效")
	}
	if n.Port < 1 || n.Port > 65535 {
		return fmt.Errorf("节点端口无效")
	}
	if n.Username == "" || n.Password == "" {
		return fmt.Errorf("节点配置不完整")
	}
	if len(n.Login()) > 256 || len(n.Password) > 256 || strings.ContainsRune(n.Password, 0) {
		return fmt.Errorf("节点凭据格式不受支持")
	}
	return nil
}

// BlockReason 返回不能连接的原因；为空表示可以连接。
func (n *Node) BlockReason() string {
	if !n.Available {
		return "节点已停用或已移除，请刷新列表"
	}
	if n.Usage != nil && n.Usage.Fresh && n.Usage.Exhausted {
		return "该节点本月流量已用完"
	}
	return ""
}

// People 是首页“当前节点”状态行：连接人数 + 负载。
func (n *Node) People() string {
	if n.Usage == nil || !n.Usage.Configured {
		return "统计未接入"
	}
	if !n.Usage.Fresh {
		return "统计待更新"
	}
	return fmt.Sprintf("节点连接人数 %d 人  ·  %s", n.Usage.Online, n.Load())
}

// Load 返回负载标签。
func (n *Node) Load() string {
	if n.Usage == nil {
		return "统计未接入"
	}
	if n.Usage.Exhausted {
		return "流量用完"
	}
	if n.Usage.Full {
		return "负载已满"
	}
	return "负载正常"
}

// MergeStatus 把 /api/app/status 的在线状态与用量合并进配置列表。
func MergeStatus(cfg *Config, st *StatusConfig) {
	byID := make(map[string]*Usage, len(st.Nodes))
	maxUsers := make(map[string]int, len(st.Nodes))
	for _, row := range st.Nodes {
		byID[row.ID] = row.Usage
		maxUsers[row.ID] = row.MaxUsers
	}
	for _, n := range cfg.Nodes {
		u, ok := byID[n.ID]
		n.Available = ok
		if ok {
			n.Usage = u
			n.MaxUsers = maxUsers[n.ID]
		} else if n.Usage != nil {
			n.Usage.Fresh = false
		}
	}
}

// Stale 把所有用量标记为过时（后台请求失败时使用，绝不保留旧数字冒充新数据）。
func Stale(cfg *Config) {
	for _, n := range cfg.Nodes {
		if n.Usage != nil {
			n.Usage.Fresh = false
		}
	}
}

// Bytes 用于速率显示（1024 进制，保留合适的小数）。
func Bytes(v int64) string {
	if v < 0 {
		v = 0
	}
	switch {
	case v < 1024:
		return strconv.FormatInt(v, 10) + " B"
	case v < 1024*1024:
		return trim(strconv.FormatFloat(float64(v)/1024, 'f', 1, 64)) + " KB"
	case v < 1024*1024*1024:
		return trim(strconv.FormatFloat(float64(v)/(1024*1024), 'f', 2, 64)) + " MB"
	default:
		return trim(strconv.FormatFloat(float64(v)/(1024*1024*1024), 'f', 2, 64)) + " GB"
	}
}

// Quota 用于额度显示：整数不带 .00，零头最多两位。
func Quota(v int64) string {
	if v < 0 {
		v = 0
	}
	switch {
	case v < 1024:
		return strconv.FormatInt(v, 10) + " B"
	case v < 1024*1024:
		return strconv.FormatInt(int64(math.Round(float64(v)/1024)), 10) + " KB"
	case v < 1024*1024*1024:
		return strconv.FormatInt(int64(math.Round(float64(v)/(1024*1024))), 10) + " MB"
	}
	return trim(strconv.FormatFloat(float64(v)/(1024*1024*1024), 'f', 2, 64)) + " GB"
}

func trim(s string) string {
	if strings.Contains(s, ".") {
		s = strings.TrimRight(s, "0")
		s = strings.TrimRight(s, ".")
	}
	return s
}

// Month 返回北京时间当前计费月，例如 2026-11。
func Month(t time.Time) string {
	return t.UTC().Add(8 * time.Hour).Format("2006-01")
}

// PreviousMonth 返回上一个计费月，用于跨月补报。
func PreviousMonth(t time.Time) string {
	bj := t.UTC().Add(8 * time.Hour)
	return time.Date(bj.Year(), bj.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1).Format("2006-01")
}

// ResetDays 返回距离北京时区下月 1 日的自然日数。
func ResetDays(t time.Time) int {
	bj := t.UTC().Add(8 * time.Hour)
	next := time.Date(bj.Year(), bj.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, 1, 0)
	days := int(next.Sub(time.Date(bj.Year(), bj.Month(), bj.Day(), 0, 0, 0, 0, time.UTC)).Hours() / 24)
	if days < 1 {
		days = 1
	}
	return days
}

// UnmarshalConfig 解析 /api/app/config，并校验每个节点。
func UnmarshalConfig(data []byte) (*Config, error) {
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if len(cfg.Nodes) > 100 {
		return nil, fmt.Errorf("后台节点列表无效")
	}
	for _, n := range cfg.Nodes {
		if n == nil {
			return nil, fmt.Errorf("节点为空")
		}
		if err := n.Validate(); err != nil {
			return nil, err
		}
		n.Available = true
	}
	return &cfg, nil
}
