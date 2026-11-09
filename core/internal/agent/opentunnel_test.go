// SPDX-License-Identifier: GPL-2.0-or-later
package agent

import (
	"errors"
	"net"
	"testing"

	"qingyou/qyvpn/internal/session"
	"qingyou/qyvpn/internal/tunnel"
)

// stubTunnel 是最小的 session.Tunnel 实现，用来验证隧道工厂被正确调用。
type stubTunnel struct{ closed bool }

func (s *stubTunnel) Read([]byte) (int, error)    { return 0, errors.New("没有数据") }
func (s *stubTunnel) Write(p []byte) (int, error) { return len(p), nil }
func (s *stubTunnel) Close() error                { s.closed = true; return nil }

// iOS 扩展没有 utun，连接内核必须允许外部接管隧道创建。
func TestOpenTunnelUsesInjectedOpener(t *testing.T) {
	a := &Agent{}
	var (
		called bool
		gotIP  net.IP
		gotMTU int
	)
	stub := &stubTunnel{}
	a.SetTunnelOpener(func(ip net.IP, dns []net.IP, mtu int, opts tunnel.Options) (session.Tunnel, error) {
		called = true
		gotIP, gotMTU = ip, mtu
		if opts.ServerHost != "" {
			t.Errorf("ServerHost = %q，注入实现不应依赖桌面端的路由参数", opts.ServerHost)
		}
		return stub, nil
	})

	tun, err := a.openTunnelWith(net.ParseIP("10.77.88.11"), []net.IP{net.ParseIP("10.77.88.1")}, 1400, tunnel.Options{})
	if err != nil {
		t.Fatalf("openTunnelWith: %v", err)
	}
	if !called {
		t.Fatal("注入的隧道实现没有被调用")
	}
	if gotIP.String() != "10.77.88.11" || gotMTU != 1400 {
		t.Fatalf("参数传递错误：ip=%v mtu=%d", gotIP, gotMTU)
	}
	if tun != session.Tunnel(stub) {
		t.Fatal("返回的隧道不是注入的实现")
	}
}

// 注入实现返回 nil、nil 时不能把空指针塞进 interface。
func TestOpenTunnelRejectsNilDevice(t *testing.T) {
	a := &Agent{}
	a.SetTunnelOpener(func(net.IP, []net.IP, int, tunnel.Options) (session.Tunnel, error) {
		return nil, nil
	})
	if _, err := a.openTunnelWith(nil, nil, 0, tunnel.Options{}); err == nil {
		t.Fatal("隧道实现返回空设备时应当报错")
	}
}

// 注入实现的错误必须原样抛给上层，方便界面区分「认证失败」和「隧道创建失败」。
func TestOpenTunnelPropagatesError(t *testing.T) {
	a := &Agent{}
	want := errors.New("扩展已停止")
	a.SetTunnelOpener(func(net.IP, []net.IP, int, tunnel.Options) (session.Tunnel, error) {
		return nil, want
	})
	if _, err := a.openTunnelWith(nil, nil, 0, tunnel.Options{}); !errors.Is(err, want) {
		t.Fatalf("err = %v，期望 %v", err, want)
	}
}
