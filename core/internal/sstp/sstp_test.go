// SPDX-License-Identifier: GPL-2.0-or-later
package sstp

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestCallConnectRequestBytes(t *testing.T) {
	got := CallConnectRequest()
	want := []byte{
		0x10, 0x01, 0x00, 0x0E, 0x00, 0x01, 0x00, 0x01,
		0x00, 0x01, 0x00, 0x06, 0x00, 0x01,
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("CALL_CONNECT_REQUEST = % x, want % x", got, want)
	}
}

func TestControlRoundTrip(t *testing.T) {
	nonce := bytes.Repeat([]byte{0xAB}, 32)
	attr := Attribute{ID: AttrCryptoBindingRequest, Value: append([]byte{0, 0, 0, 0x03}, nonce...)}
	frame := EncodeControl(MsgCallConnectAck, []Attribute{attr})
	if int(binary.BigEndian.Uint16(frame[2:])) != len(frame) {
		t.Fatalf("长度字段 %d 与包长 %d 不一致", binary.BigEndian.Uint16(frame[2:]), len(frame))
	}
	c, err := ParseControl(frame[4:])
	if err != nil {
		t.Fatal(err)
	}
	if c.Type != MsgCallConnectAck {
		t.Fatalf("消息类型 = %d", c.Type)
	}
	req, err := ParseCryptoBindingRequest(c)
	if err != nil {
		t.Fatal(err)
	}
	proto, err := req.HashProtocol()
	if err != nil {
		t.Fatal(err)
	}
	if proto != HashSHA256 || !bytes.Equal(req.Nonce, nonce) {
		t.Fatalf("crypto binding 解析错误：%+v", req)
	}
}

func TestCallConnectedLayout(t *testing.T) {
	nonce := bytes.Repeat([]byte{0x11}, 32)
	cert := bytes.Repeat([]byte{0x22}, 32)
	hlak := bytes.Repeat([]byte{0x33}, 32)
	frame, err := CallConnected(nonce, cert, HashSHA256, hlak)
	if err != nil {
		t.Fatal(err)
	}
	if len(frame) != 112 {
		t.Fatalf("CALL_CONNECTED 长度 = %d, want 112", len(frame))
	}
	if int(binary.BigEndian.Uint16(frame[2:])) != 112 {
		t.Fatal("长度字段错误")
	}
	if binary.BigEndian.Uint16(frame[4:]) != MsgCallConnected {
		t.Fatal("消息类型错误")
	}
	if frame[9] != AttrCryptoBinding {
		t.Fatal("属性 ID 错误")
	}
	if frame[15] != HashSHA256 {
		t.Fatal("哈希算法字段错误")
	}
	if !bytes.Equal(frame[16:48], nonce) || !bytes.Equal(frame[48:80], cert) {
		t.Fatal("nonce/证书哈希位置错误")
	}
	if bytes.Equal(frame[80:112], make([]byte, 32)) {
		t.Fatal("compound MAC 未写入")
	}

	// 同一输入必须得到同一 CMAC（确定性），改动一个字节必须变化。
	again, _ := CallConnected(nonce, cert, HashSHA256, hlak)
	if !bytes.Equal(frame, again) {
		t.Fatal("CMAC 计算不确定")
	}
	other, _ := CallConnected(nonce, cert, HashSHA256, bytes.Repeat([]byte{0x34}, 32))
	if bytes.Equal(frame[80:], other[80:]) {
		t.Fatal("更换 HLAK 后 CMAC 未变化")
	}
}

func TestCallConnectedRejectsBadInput(t *testing.T) {
	if _, err := CallConnected(make([]byte, 32), make([]byte, 32), HashSHA256, nil); err == nil {
		t.Fatal("缺少 HLAK 时应报错")
	}
	if _, err := CallConnected(make([]byte, 8), make([]byte, 32), HashSHA256, make([]byte, 32)); err == nil {
		t.Fatal("nonce 长度非法时应报错")
	}
}

func TestDataFrame(t *testing.T) {
	ip := []byte{0x45, 0x00, 0x00, 0x28}
	frame := EncodeData(ip, 0x0021)
	if len(frame) != 12 {
		t.Fatalf("DATA 帧长度 = %d", len(frame))
	}
	if binary.BigEndian.Uint16(frame[2:]) != 12 {
		t.Fatal("DATA 长度字段错误")
	}
	proto, data, err := SplitData(frame[4:])
	if err != nil {
		t.Fatal(err)
	}
	if proto != 0x0021 || !bytes.Equal(data, ip) {
		t.Fatalf("DATA 解析错误：%d % x", proto, data)
	}
}
