// SPDX-License-Identifier: GPL-2.0-or-later
package agent

import (
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

// TestRRTTakesMinimum 一次 SYN 丢包会让 TCP 等 1 秒重传，单次采样就是 1000ms+。
// 取最小值才能反映链路本身的往返时间。
func TestRRTTakesMinimum(t *testing.T) {
	values := []int64{1000, 82, 95}
	calls := 0
	sample := func() (int64, error) {
		v := values[calls]
		calls++
		return v, nil
	}
	got, err := rttWithSample(sample)
	if err != nil {
		t.Fatalf("不应报错：%v", err)
	}
	if got != 82 {
		t.Fatalf("应取最小值 82，实际 %d", got)
	}
	if calls != 3 {
		t.Fatalf("慢节点应采 3 次，实际 %d 次", calls)
	}
}

// TestRTTSkipsExtraSamplesWhenFast 已经很快的节点不该再多测，避免整体变慢。
func TestRTTSkipsExtraSamplesWhenFast(t *testing.T) {
	calls := 0
	sample := func() (int64, error) {
		calls++
		return 40, nil
	}
	got, err := rttWithSample(sample)
	if err != nil || got != 40 {
		t.Fatalf("结果 = %d, err = %v", got, err)
	}
	if calls != 1 {
		t.Fatalf("快节点只该采 1 次，实际 %d 次", calls)
	}
}

// TestRTTStopsOnError 连不上就别再试，否则一个坏节点要等好几秒。
func TestRTTStopsOnError(t *testing.T) {
	calls := 0
	sample := func() (int64, error) {
		calls++
		return 0, errors.New("dial tcp: connection refused")
	}
	if _, err := rttWithSample(sample); err == nil {
		t.Fatal("应返回错误")
	}
	if calls != 1 {
		t.Fatalf("失败后不该重试，实际 %d 次", calls)
	}
}

// TestDialBestPicksReachable 多 A 记录里有一个不可达时，不该被它拖慢：
// 并发探测取最快的一个，而不是串行等到超时。
func TestDialBestPicksReachable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	dead, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadAddr := dead.Addr().String()
	_ = dead.Close()

	start := time.Now()
	ms, err := dialBest([]string{deadAddr, ln.Addr().String()}, 1500*time.Millisecond)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("应有一个地址可用：%v", err)
	}
	if ms < 0 || ms > 150 {
		t.Fatalf("本机握手耗时异常：%d ms", ms)
	}
	// 关闭的端口会立刻被拒（不是等超时），所以整体应该很快。
	if elapsed > time.Second {
		t.Fatalf("不该被不可达地址拖住，耗时 %v", elapsed)
	}
}

// TestDialBestAllFail 全部不可达时要报错，而不是返回 0。
func TestDialBestAllFail(t *testing.T) {
	addr := "127.0.0.1:1"
	if _, err := dialBest([]string{addr}, 300*time.Millisecond); err == nil {
		t.Fatal("应返回错误")
	}
}

// TestDialBestEmpty 空列表直接报错，避免调用方拿到 0ms 假数据。
func TestDialBestEmpty(t *testing.T) {
	if _, err := dialBest(nil, time.Second); err == nil {
		t.Fatal("空地址列表应报错")
	}
}

// TestTCPOnceMeasuresLocalListener 本机监听的握手应该几乎为 0，确认计时单位是毫秒。
func TestTCPOnceMeasuresLocalListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	ms, err := tcpOnce(ln.Addr().String(), time.Second)
	if err != nil {
		t.Fatalf("本机握手失败：%v", err)
	}
	if ms < 0 || ms > 150 {
		t.Fatalf("本机握手耗时异常：%d ms", ms)
	}
}

// TestProbeAddrsKeepsIPLiteral IP 节点不该走解析，直接拼端口。
func TestProbeAddrsKeepsIPLiteral(t *testing.T) {
	a := &Agent{}
	got := a.probeAddrs("203.0.113.7", 443)
	if len(got) != 1 || got[0] != "203.0.113.7:443" {
		t.Fatalf("地址 = %v", got)
	}
	if len(a.probeDNS) != 0 {
		t.Fatal("IP 字面量不该写入 DNS 缓存")
	}
}

// TestProbeAddrsCachesDomain 域名只解析一次，第二次必须命中缓存，
// 否则每次测速都把 DNS 时间算进延迟。
func TestProbeAddrsCachesDomain(t *testing.T) {
	a := &Agent{probeDNS: map[string]dnsEntry{}}
	first := a.probeAddrs("localhost", 443)
	if len(first) == 0 {
		t.Fatal("没有返回地址")
	}
	for _, addr := range first {
		if !strings.HasSuffix(addr, ":443") {
			t.Fatalf("地址 = %q", addr)
		}
	}
	if entry, ok := a.probeDNS["localhost"]; !ok || len(entry.ips) == 0 {
		t.Fatalf("未写入缓存：%+v", a.probeDNS)
	}
	second := a.probeAddrs("localhost", 443)
	if len(second) != len(first) || second[0] != first[0] {
		t.Fatalf("缓存前后结果不同：%v / %v", first, second)
	}
}

// TestHostPorts 端口拼接不走解析，顺序保持不变。
func TestHostPorts(t *testing.T) {
	got := hostPorts([]string{"1.1.1.1", "2.2.2.2"}, "443")
	if len(got) != 2 || got[0] != "1.1.1.1:443" || got[1] != "2.2.2.2:443" {
		t.Fatalf("结果 = %v", got)
	}
}
