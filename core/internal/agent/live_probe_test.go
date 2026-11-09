// SPDX-License-Identifier: GPL-2.0-or-later
//go:build live

package agent

// 真实链路延迟实测（默认不跑）：
//   go test -tags live ./internal/agent/ -run TestLiveProbeTiming -v
//
// 用来回答一个问题：用户看到的「1000ms+」到底是链路慢，还是把 DNS 解析
// 和 TCP 重传算进去了。分别打印 DNS 耗时与三次握手耗时。

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func liveNode(t *testing.T) (string, int) {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		path := filepath.Join(dir, ".private", "sstp-hk-test.properties")
		if data, err := os.ReadFile(path); err == nil {
			host, port := "", 443
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "host=") {
					host = strings.TrimSpace(strings.TrimPrefix(line, "host="))
				}
				if strings.HasPrefix(line, "port=") {
					if v, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "port="))); err == nil {
						port = v
					}
				}
			}
			if host != "" {
				return host, port
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Skip("没有找到 .private/sstp-hk-test.properties，跳过真实测速")
	return "", 0
}

func TestLiveProbeTiming(t *testing.T) {
	host, port := liveNode(t)

	// 1) DNS 耗时（不缓存，量一次）
	start := time.Now()
	ips := make([]string, 0, 2)
	if addrs, err := netLookup(host); err == nil {
		for _, a := range addrs {
			ips = append(ips, a)
		}
	}
	dnsMS := time.Since(start).Milliseconds()
	t.Logf("DNS %s 耗时 %d ms，解析到 %v", host, dnsMS, ips)

	// 2) 直接对 IP 做三次握手，看每次的真实耗时
	if len(ips) == 0 {
		t.Fatalf("解析失败，无法继续")
	}
	a := &Agent{probeDNS: map[string]dnsEntry{}}
	addrs := a.probeAddrs(host, port)
	t.Logf("候选地址：%v", addrs)
	for i := 1; i <= 3; i++ {
		ms, err := dialBest(addrs, 1500*time.Millisecond)
		if err != nil {
			t.Fatalf("第 %d 次握手失败：%v", i, err)
		}
		t.Logf("第 %d 次 TCP 握手（最快地址）：%d ms", i, ms)
	}

	// 3) 走完整测速函数（含最小值策略）
	ms, err := rttWithSample(func() (int64, error) { return dialBest(addrs, 1500*time.Millisecond) })
	if err != nil {
		t.Fatalf("rttWithSample 失败：%v", err)
	}
	t.Logf("最终上报延迟：%d ms（旧实现会把 DNS 与重传一并算入）", ms)
}
