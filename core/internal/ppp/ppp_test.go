// SPDX-License-Identifier: GPL-2.0-or-later
package ppp

import (
	"bytes"
	"testing"
)

func TestEncodeParseRoundTrip(t *testing.T) {
	frame := EncodePPP(ProtoLCP, CodeConfigureRequest, 7, []byte{1, 2, 3, 4})
	if len(frame) != 16 {
		t.Fatalf("帧长 = %d, want 16", len(frame))
	}
	// SSTP 头 + PPP HDLC 头 + 协议 + code/id/长度。
	if frame[4] != 0xFF || frame[5] != 0x03 {
		t.Fatal("缺少 PPP HDLC 头")
	}
	code, id, payload, err := ParsePPP(frame[8:])
	if err != nil {
		t.Fatal(err)
	}
	if code != CodeConfigureRequest || id != 7 || !bytes.Equal(payload, []byte{1, 2, 3, 4}) {
		t.Fatalf("解析结果 code=%d id=%d payload=% x", code, id, payload)
	}
}

func TestOptions(t *testing.T) {
	opts := []Option{MRUOption(1400), AuthOption(ProtoCHAP, ChapMSCHAPv2)}
	data := EncodeOptions(opts)
	parsed, err := ParseOptions(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed) != 2 {
		t.Fatalf("选项数量 = %d", len(parsed))
	}
	if v, ok := parsed[0].Uint16(); !ok || v != 1400 {
		t.Fatalf("MRU = %d", v)
	}
	proto, ok := parsed[1].Protocol()
	if !ok || proto != ProtoCHAP || parsed[1].Value[2] != ChapMSCHAPv2 {
		t.Fatalf("认证选项 = %d % x", proto, parsed[1].Value)
	}
}

func TestParseOptionsRejectsBadLength(t *testing.T) {
	if _, err := ParseOptions([]byte{1, 9, 0}); err == nil {
		t.Fatal("长度越界应报错")
	}
}

func TestAddressOption(t *testing.T) {
	opt := AddressOption(OptIP, [4]byte{10, 77, 88, 11})
	addr, ok := opt.Addr()
	if !ok || addr != [4]byte{10, 77, 88, 11} {
		t.Fatalf("地址 = %v", addr)
	}
	if _, ok := (&Option{Type: OptIP}).Addr(); ok {
		t.Fatal("空选项不应返回地址")
	}
}
