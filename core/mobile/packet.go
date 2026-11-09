// SPDX-License-Identifier: GPL-2.0-or-later
package mobile

// packet.go：把内核的 IP 包收发接到宿主（NetworkExtension 的 NEPacketTunnelFlow）。
//
// 桌面端由 internal/tunnel 打开 utun 设备、自己执行 route/networksetup；iOS 的
// 扩展进程既没有 /dev/utun，也不允许调用这些命令，IP 包必须由扩展用
// packetFlow.readPackets / writePackets 搬运。所以这里实现 session.Tunnel 的
// 最小接口（Read / Write / Close），用两只有界队列把两侧接起来：
//
//	上行：扩展 readPackets → Engine.WritePacket → in  → 内核 Read  → TLS
//	下行：TLS → 内核 Write → out → 投递协程 → Delegate.OnPacket → 扩展 writePackets
//
// 两侧都用「满了丢最老的包」而不是阻塞：扩展被系统限流时，阻塞会把 TLS 读取
// 循环一起卡死（心跳发不出去，连接被判死），而丢包在 IP 层会由上层重传。

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

// 队列长度按单包 1400 字节估算：64 个约 90 KB，128 个约 180 KB。
// NetworkExtension 扩展的常驻内存上限约 50 MB，不能照搬桌面端的大缓冲。
const (
	inboundQueue  = 64
	outboundQueue = 128
)

var errTunnelClosed = errors.New("隧道已关闭")

// packetTunnel 是一个只存在于内存里的「隧道设备」。
type packetTunnel struct {
	in  chan []byte // 宿主 → 内核
	out chan []byte // 内核 → 宿主

	closed chan struct{}
	once   sync.Once

	// 丢弃计数只用于日志与排查，不参与业务判断。
	droppedIn  atomic.Int64
	droppedOut atomic.Int64
}

func newPacketTunnel() *packetTunnel {
	return &packetTunnel{
		in:     make(chan []byte, inboundQueue),
		out:    make(chan []byte, outboundQueue),
		closed: make(chan struct{}),
	}
}

// Read 供 session 的转发协程调用，阻塞到有上行包或隧道关闭。
func (t *packetTunnel) Read(buf []byte) (int, error) {
	select {
	case <-t.closed:
		return 0, errTunnelClosed
	case p, ok := <-t.in:
		if !ok {
			return 0, errTunnelClosed
		}
		if len(p) > len(buf) {
			// session 给的缓冲是 MTU+64，正常报文不可能超；超了说明收到异常大包，
			// 这里明确报错而不是截断，避免把半个包写进 TLS 造成协议错乱。
			return 0, fmt.Errorf("上行数据包 %d 字节超过隧道缓冲区 %d", len(p), len(buf))
		}
		return copy(buf, p), nil
	}
}

// Write 供 session 把从服务器收到的下行包交给宿主。
func (t *packetTunnel) Write(p []byte) (int, error) {
	select {
	case <-t.closed:
		return 0, errTunnelClosed
	default:
	}
	if len(p) == 0 {
		return 0, nil
	}
	// session 的读取缓冲会复用，必须复制一份再交给异步投递。
	cp := make([]byte, len(p))
	copy(cp, p)
	enqueue(t.out, cp, &t.droppedOut, t.closed)
	return len(p), nil
}

// WriteFromHost 由扩展把 packetFlow 读到的上行包送进内核。
func (t *packetTunnel) WriteFromHost(p []byte) error {
	select {
	case <-t.closed:
		return errTunnelClosed
	default:
	}
	if len(p) == 0 {
		return nil
	}
	cp := make([]byte, len(p))
	copy(cp, p)
	enqueue(t.in, cp, &t.droppedIn, t.closed)
	return nil
}

// TakeOutbound 由投递协程取包；隧道关闭时返回 false。
func (t *packetTunnel) TakeOutbound() ([]byte, bool) {
	select {
	case <-t.closed:
		return nil, false
	case p, ok := <-t.out:
		return p, ok
	}
}

// Close 可重复调用：session 结束时会关闭设备，扩展停止时也会关一次。
func (t *packetTunnel) Close() error {
	t.once.Do(func() { close(t.closed) })
	return nil
}

// enqueue 在队列满时淘汰最老的一个包再放入新包。
func enqueue(q chan []byte, p []byte, dropped *atomic.Int64, closed <-chan struct{}) {
	select {
	case q <- p:
		return
	default:
	}
	select {
	case <-q:
		dropped.Add(1)
	default:
	}
	select {
	case q <- p:
	case <-closed:
	}
}
