// SPDX-License-Identifier: GPL-2.0-or-later
package auth

import (
	"encoding/hex"
	"testing"
)

// MD4 的测试向量来自 RFC 1320 附录 A.5。
func TestMD4Vectors(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "31d6cfe0d16ae931b73c59d7e0c089c0"},
		{"a", "bde52cb31de33e46245e05fbdbd6fb24"},
		{"abc", "a448017aaf21d8525fc10ae87aa6729d"},
		{"message digest", "d9130a8164549fe818874806e1c7014b"},
		{"abcdefghijklmnopqrstuvwxyz", "d79e1c308aa5bbcdeea8ed63df412da9"},
		{"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789", "043f8582f241db351ce627e153e7f0e4"},
		{"12345678901234567890123456789012345678901234567890123456789012345678901234567890", "e33b4ddc9c38f2199c3e7b164fcc0536"},
	}
	for _, c := range cases {
		got := MD4([]byte(c.in))
		if hex.EncodeToString(got[:]) != c.want {
			t.Fatalf("MD4(%q) = %x, want %s", c.in, got, c.want)
		}
	}
}

// MSCHAPv2 的测试向量来自 RFC 2759 第 9.2 节。
func TestMSCHAPv2RFCVector(t *testing.T) {
	// RFC 2759 §9.2 的完整测试向量（AuthenticatorChallenge 的倒数第二字节为 0x26）。
	authChallenge, _ := hex.DecodeString("5B5D7C7D7B3F2F3E3C2C602132262628")
	peerChallenge, _ := hex.DecodeString("21402324255E262A28295F2B3A337C7E")

	m := New("User", "clientPass")
	if err := m.SetAuthChallenge(authChallenge); err != nil {
		t.Fatal(err)
	}
	resp, err := m.Response(peerChallenge)
	if err != nil {
		t.Fatal(err)
	}
	wantResp := "82309ecd8d708b5ea08faa3981cd83544233114a3d85d6df"
	if got := hex.EncodeToString(resp); got != wantResp {
		t.Fatalf("NT-Response = %s, want %s", got, wantResp)
	}
	wantAuth := "S=407A5589115FD0D6209F510FE9C04566932CDA56"
	if got := string(m.AuthenticatorResponse()); got != wantAuth {
		t.Fatalf("AuthenticatorResponse = %s, want %s", got, wantAuth)
	}
	if !m.VerifyAuthenticator([]byte(wantAuth)) {
		t.Fatal("VerifyAuthenticator 未通过 RFC 测试向量")
	}
	if m.VerifyAuthenticator([]byte("S=0000000000000000000000000000000000000000")) {
		t.Fatal("VerifyAuthenticator 接受了错误响应")
	}
}

func TestMSCHAPv2HLAKStable(t *testing.T) {
	authChallenge, _ := hex.DecodeString("5B5D7C7D7B3F2F3E3C2C602132261128")
	peerChallenge, _ := hex.DecodeString("21402324255E262A28295F2B3A337C7E")
	m := New("User", "clientPass")
	if err := m.SetAuthChallenge(authChallenge); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Response(peerChallenge); err != nil {
		t.Fatal(err)
	}
	hlak := m.HLAK()
	if len(hlak) != 32 {
		t.Fatalf("HLAK 长度 = %d", len(hlak))
	}
	if hex.EncodeToString(hlak[:16]) == hex.EncodeToString(hlak[16:]) {
		t.Fatal("HLAK 两半不应相同（说明 magic 常量用错）")
	}
}

func TestUTF16LE(t *testing.T) {
	if got := hex.EncodeToString(utf16le("a中")); got != "61002d4e" {
		t.Fatalf("utf16le = %s", got)
	}
}
