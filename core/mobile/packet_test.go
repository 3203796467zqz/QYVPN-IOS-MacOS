// SPDX-License-Identifier: GPL-2.0-or-later
package mobile

import (
	"bytes"
	"sync"
	"testing"
	"time"
)

func TestPacketTunnelDownlinkCopiesPacket(t *testing.T) {
	tun := newPacketTunnel()
	defer tun.Close()

	src := []byte{0x45, 0x00, 0x00, 0x14}
	if _, err := tun.Write(src); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// session 的读取缓冲会复用，写完立刻改写源数据模拟这一点。
	copy(src, []byte{0xff, 0xff, 0xff, 0xff})

	got, ok := tun.TakeOutbound()
	if !ok {
		t.Fatal("TakeOutbound 没有拿到包")
	}
	want := []byte{0x45, 0x00, 0x00, 0x14}
	if !bytes.Equal(got, want) {
		t.Fatalf("下行包 = %x，期望 %x（Write 必须复制）", got, want)
	}
}

func TestPacketTunnelUplinkRoundTrip(t *testing.T) {
	tun := newPacketTunnel()
	defer tun.Close()

	pkt := bytes.Repeat([]byte{0x60}, 1400)
	if err := tun.WriteFromHost(pkt); err != nil {
		t.Fatalf("WriteFromHost: %v", err)
	}
	buf := make([]byte, 1464)
	n, err := tun.Read(buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if n != len(pkt) || !bytes.Equal(buf[:n], pkt) {
		t.Fatalf("Read 拿到 %d 字节，与写入不一致", n)
	}
}

func TestPacketTunnelRejectsOversize(t *testing.T) {
	tun := newPacketTunnel()
	defer tun.Close()

	if err := tun.WriteFromHost(make([]byte, 2000)); err != nil {
		t.Fatalf("WriteFromHost: %v", err)
	}
	if _, err := tun.Read(make([]byte, 100)); err == nil {
		t.Fatal("缓冲区装不下时应当报错，而不是截断")
	}
}

func TestPacketTunnelCloseUnblocksRead(t *testing.T) {
	tun := newPacketTunnel()
	done := make(chan error, 1)
	go func() {
		_, err := tun.Read(make([]byte, 1500))
		done <- err
	}()
	// 让 Read 先进入阻塞，再关闭。
	time.Sleep(20 * time.Millisecond)
	_ = tun.Close()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("关闭后 Read 应当返回错误")
		}
	case <-time.After(time.Second):
		t.Fatal("关闭后 Read 仍阻塞")
	}
	// Close 必须可重复调用：session 结束时关一次，扩展停止时还会再关一次。
	if err := tun.Close(); err != nil {
		t.Fatalf("重复 Close 报错：%v", err)
	}
	if _, err := tun.Write([]byte{1}); err == nil {
		t.Fatal("关闭后 Write 应当返回错误")
	}
}

func TestPacketTunnelQueueDropsOldestInsteadOfBlocking(t *testing.T) {
	tun := newPacketTunnel()
	defer tun.Close()

	// 塞满并溢出：WriteFromHost 必须立即返回，不能阻塞 session 的读取循环。
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < inboundQueue*3; i++ {
			if err := tun.WriteFromHost([]byte{byte(i)}); err != nil {
				return
			}
		}
	}()
	finished := make(chan struct{})
	go func() { wg.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("队列满时 WriteFromHost 阻塞了")
	}
	if tun.droppedIn.Load() == 0 {
		t.Fatal("溢出后应当有丢弃计数")
	}
	if len(tun.in) != inboundQueue {
		t.Fatalf("队列长度 = %d，期望 %d", len(tun.in), inboundQueue)
	}
}

func TestPacketTunnelEmptyPacketIgnored(t *testing.T) {
	tun := newPacketTunnel()
	defer tun.Close()

	if n, err := tun.Write(nil); err != nil || n != 0 {
		t.Fatalf("空包 Write = (%d,%v)，期望 (0,nil)", n, err)
	}
	if err := tun.WriteFromHost(nil); err != nil {
		t.Fatalf("空包 WriteFromHost: %v", err)
	}
	if len(tun.out) != 0 || len(tun.in) != 0 {
		t.Fatal("空包不应进入队列")
	}
}
