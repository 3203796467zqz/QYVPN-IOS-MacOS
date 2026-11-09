// SPDX-License-Identifier: GPL-2.0-or-later
//go:build live

// 这两个用例会连真实节点，默认不跑：go test -tags live ./internal/session/
package session

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"qingyou/qyvpn/internal/cloud"
)

// mockTunnel 在没有 TUN 的设备上模拟隧道端点：注入一个 IP 包，回收服务器返回的包。
type mockTunnel struct {
	inject   []byte
	out      chan []byte
	mu       sync.Mutex
	injected bool
}

func newMockTunnel(packet []byte) *mockTunnel {
	return &mockTunnel{inject: packet, out: make(chan []byte, 16)}
}

func (m *mockTunnel) Configure(net.IP, []net.IP, int) error { return nil }
func (m *mockTunnel) Close() error                          { close(m.out); return nil }

func (m *mockTunnel) Read(buf []byte) (int, error) {
	m.mu.Lock()
	if !m.injected && len(m.inject) > 0 {
		m.injected = true
		n := copy(buf, m.inject)
		m.mu.Unlock()
		return n, nil
	}
	m.mu.Unlock()
	// 之后阻塞，避免空转。
	time.Sleep(time.Hour)
	return 0, nil
}

func (m *mockTunnel) Write(p []byte) (int, error) {
	dup := append([]byte(nil), p...)
	select {
	case m.out <- dup:
	default:
	}
	return len(p), nil
}

func checksum(data []byte) uint16 {
	var sum uint32
	for i := 0; i+1 < len(data); i += 2 {
		sum += uint32(binary.BigEndian.Uint16(data[i:]))
	}
	if len(data)%2 == 1 {
		sum += uint32(data[len(data)-1]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 0xFFFF) + (sum >> 16)
	}
	return ^uint16(sum)
}

// buildICMPEcho 组装 IPv4 + ICMP Echo Request。
func buildICMPEcho(src, dst net.IP, id, seq uint16) []byte {
	icmp := make([]byte, 8+16)
	icmp[0] = 8 // Echo Request
	icmp[1] = 0
	binary.BigEndian.PutUint16(icmp[4:], id)
	binary.BigEndian.PutUint16(icmp[6:], seq)
	copy(icmp[8:], []byte("qingyou-macos-vpn"))
	binary.BigEndian.PutUint16(icmp[2:], checksum(icmp))

	ip := make([]byte, 20, 20+len(icmp))
	ip[0] = 0x45
	binary.BigEndian.PutUint16(ip[2:], uint16(20+len(icmp)))
	ip[8] = 64
	ip[9] = 1 // ICMP
	copy(ip[12:], src.To4())
	copy(ip[16:], dst.To4())
	binary.BigEndian.PutUint16(ip[10:], checksum(ip))
	return append(ip, icmp...)
}

// TestLiveDataPlane 验证隧道内真的能收发 IP 包：注入一个 ICMP Echo，
// 期望从服务器侧收到同 id/seq 的 Echo Reply。没有令牌时跳过。
func TestLiveDataPlane(t *testing.T) {
	token, base := privateToken()
	if token == "" {
		t.Skip("没有客户端令牌，跳过真实数据面测试")
	}
	client, err := cloud.New(base, token)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cfg, err := client.Sync(ctx)
	if err != nil {
		t.Fatalf("拉取节点失败：%v", err)
	}
	var node = cfg.Nodes[0]
	for _, n := range cfg.Nodes {
		if n.BlockReason() == "" {
			node = n
			break
		}
	}

	var tun *mockTunnel
	var local net.IP
	var once sync.Once
	done := make(chan string, 1)
	runCtx, runCancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer runCancel()

	meter := &Meter{}
	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(runCtx, node, Options{
			MTU:   1400,
			Meter: meter,
			Log:   func(string, ...any) {},
			OnIPCP: func(ip string, dns []string) {
				local = net.ParseIP(ip).To4()
			},
			Factory: func(ip net.IP, dns []net.IP, mtu int) (Tunnel, error) {
				if ip.To4() == nil {
					return nil, errBadIP
				}
				tun = newMockTunnel(buildICMPEcho(ip.To4(), net.IPv4(1, 1, 1, 1).To4(), 0x5159, 1))
				return tun, nil
			},
			OnConnected: func() {
				go func() {
					deadline := time.After(20 * time.Second)
					for {
						select {
						case pkt := <-tun.out:
							if len(pkt) < 28 || pkt[9] != 1 || pkt[20] != 0 {
								continue
							}
							if binary.BigEndian.Uint16(pkt[24:]) == 0x5159 && binary.BigEndian.Uint16(pkt[26:]) == 1 {
								once.Do(func() { done <- "ok" })
								return
							}
						case <-deadline:
							once.Do(func() { done <- "timeout" })
							return
						}
					}
				}()
			},
		})
	}()

	select {
	case result := <-done:
		if result != "ok" {
			t.Fatalf("隧道内没有收到 ICMP 回包（本机 %v）", local)
		}
		t.Logf("数据面验证通过：ICMP Echo Reply 从隧道内返回（本机地址 %v）", local)
		if meter.RX() <= 0 || meter.TX() <= 0 {
			t.Fatalf("流量计数异常：rx=%d tx=%d", meter.RX(), meter.TX())
		}
		t.Logf("流量计数：下载 %d 字节，上传 %d 字节", meter.RX(), meter.TX())
		runCancel()
	case err := <-errCh:
		t.Fatalf("连接提前结束：%v", err)
	case <-time.After(40 * time.Second):
		t.Fatalf("等待回包超时（本机 %v）", local)
	}
	runCancel()
}

var errBadIP = errors.New("IPCP 未分配有效地址")
