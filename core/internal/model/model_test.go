// SPDX-License-Identifier: GPL-2.0-or-later
package model

import (
	"testing"
	"time"
)

func node() *Node {
	return &Node{
		ID:       "8f1b2c3d-0000-4000-8000-000000000001",
		Name:     "香港",
		Host:     "hk1.example.com",
		Port:     443,
		Username: "user1",
		Password: "secret",
		SSTPHub:  "VPN",
		Enabled:  true,
	}
}

func TestLoginUsesHubSuffix(t *testing.T) {
	n := node()
	if got := n.Login(); got != "user1@VPN" {
		t.Fatalf("Login = %q", got)
	}
	if got := n.BareLogin(); got != "user1" {
		t.Fatalf("BareLogin = %q", got)
	}
	n.Username = "user1@VPN"
	if got := n.Login(); got != "user1@VPN" {
		t.Fatalf("已是 user@HUB 时不应重复追加：%q", got)
	}
	if got := n.BareLogin(); got != "" {
		t.Fatalf("BareLogin 应为空：%q", got)
	}
	n.Username = "user2"
	n.WindowsUser = "override@VPN"
	if got := n.Login(); got != "override@VPN" {
		t.Fatalf("后台指定账号时应优先使用：%q", got)
	}
}

func TestValidate(t *testing.T) {
	n := node()
	if err := n.Validate(); err != nil {
		t.Fatalf("正常节点被拒：%v", err)
	}
	bad := node()
	bad.Host = "hk1.example.com; rm -rf /"
	if err := bad.Validate(); err == nil {
		t.Fatal("非法地址应被拒绝")
	}
	bad = node()
	bad.Password = ""
	if err := bad.Validate(); err == nil {
		t.Fatal("缺密码应被拒绝")
	}
}

func TestQuotaFormat(t *testing.T) {
	cases := map[int64]string{
		0:                "0 B",
		1023:             "1023 B",
		1024:             "1 KB",
		110 * 1 << 30:    "110 GB",
		1536 * binaryMB:  "1.5 GB",
		5 * binaryGB / 4: "1.25 GB",
	}
	for in, want := range cases {
		if got := Quota(in); got != want {
			t.Fatalf("Quota(%d) = %q, want %q", in, got, want)
		}
	}
}

const binaryGB = 1 << 30
const binaryMB = 1 << 20

func TestMonthAndReset(t *testing.T) {
	// 北京时间 2026-11-09 03:42 对应 UTC 2026-11-08 19:42。
	utc := time.Date(2026, 11, 8, 19, 42, 0, 0, time.UTC)
	if got := Month(utc); got != "2026-11" {
		t.Fatalf("Month = %q", got)
	}
	if got := PreviousMonth(utc); got != "2026-10" {
		t.Fatalf("PreviousMonth = %q", got)
	}
	if got := ResetDays(utc); got != 22 {
		t.Fatalf("ResetDays = %d（11 月 9 日到 12 月 1 日应为 22 天）", got)
	}
	// 跨年边界。
	utc = time.Date(2026, 12, 31, 20, 0, 0, 0, time.UTC) // 北京时间 2027-01-01 04:00
	if got := Month(utc); got != "2027-01" {
		t.Fatalf("跨年 Month = %q", got)
	}
	if got := PreviousMonth(utc); got != "2026-12" {
		t.Fatalf("跨年 PreviousMonth = %q", got)
	}
}

func TestMergeStatus(t *testing.T) {
	cfg := &Config{Nodes: []*Node{node(), {ID: "gone", Name: "已下线", Host: "x.example.com", Port: 443, Username: "u", Password: "p"}}}
	st := &StatusConfig{}
	st.Nodes = append(st.Nodes, struct {
		ID       string `json:"id"`
		MaxUsers int    `json:"maxUsers"`
		Usage    *Usage `json:"usage"`
	}{ID: cfg.Nodes[0].ID, MaxUsers: 10, Usage: &Usage{Fresh: true, Configured: true, Online: 3, Used: 100, Limit: 1000}})

	MergeStatus(cfg, st)
	if !cfg.Nodes[0].Available || cfg.Nodes[0].Usage == nil {
		t.Fatal("在线节点应合并到用量")
	}
	if cfg.Nodes[1].Available {
		t.Fatal("未下发状态行的节点应标为不可用")
	}
	if got := cfg.Nodes[0].People(); got != "节点连接人数 3 人  ·  负载正常" {
		t.Fatalf("People = %q", got)
	}
	if reason := cfg.Nodes[1].BlockReason(); reason == "" {
		t.Fatal("不可用节点应给出原因")
	}
}

func TestBlockReasonForExhausted(t *testing.T) {
	n := node()
	n.Available = true
	n.Usage = &Usage{Fresh: true, Configured: true, Exhausted: true}
	if got := n.BlockReason(); got != "该节点本月流量已用完" {
		t.Fatalf("BlockReason = %q", got)
	}
}
