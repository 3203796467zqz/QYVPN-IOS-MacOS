// SPDX-License-Identifier: GPL-2.0-or-later
// Package auth 实现 MSCHAPv2（RFC 2759）所需的 MD4、DES 与密钥派生。
// Go 标准库没有 MD4，这里按 RFC 1320 自带一份，避免引入外部依赖。
package auth

import (
	"crypto/des"
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"math/bits"
)

// ErrBadChallenge 表示挑战值长度不是 16 字节。
var ErrBadChallenge = errors.New("MSCHAPv2 挑战值长度无效")

// MD4 计算 MD4 摘要（RFC 1320）。
func MD4(p []byte) [16]byte {
	var a, b, c, d uint32 = 0x67452301, 0xefcdab89, 0x98badcfe, 0x10325476

	msg := append([]byte(nil), p...)
	bitLen := uint64(len(msg)) * 8
	msg = append(msg, 0x80)
	for len(msg)%64 != 56 {
		msg = append(msg, 0)
	}
	var lenBuf [8]byte
	binary.LittleEndian.PutUint64(lenBuf[:], bitLen)
	msg = append(msg, lenBuf[:]...)

	f := func(x, y, z uint32) uint32 { return x&y | ^x&z }
	g := func(x, y, z uint32) uint32 { return x&y | x&z | y&z }
	h := func(x, y, z uint32) uint32 { return x ^ y ^ z }
	rot := bits.RotateLeft32

	var x [16]uint32
	for off := 0; off < len(msg); off += 64 {
		for i := 0; i < 16; i++ {
			x[i] = binary.LittleEndian.Uint32(msg[off+4*i:])
		}
		aa, bb, cc, dd := a, b, c, d

		// 第 1 轮
		a = rot(a+f(b, c, d)+x[0], 3)
		d = rot(d+f(a, b, c)+x[1], 7)
		c = rot(c+f(d, a, b)+x[2], 11)
		b = rot(b+f(c, d, a)+x[3], 19)
		a = rot(a+f(b, c, d)+x[4], 3)
		d = rot(d+f(a, b, c)+x[5], 7)
		c = rot(c+f(d, a, b)+x[6], 11)
		b = rot(b+f(c, d, a)+x[7], 19)
		a = rot(a+f(b, c, d)+x[8], 3)
		d = rot(d+f(a, b, c)+x[9], 7)
		c = rot(c+f(d, a, b)+x[10], 11)
		b = rot(b+f(c, d, a)+x[11], 19)
		a = rot(a+f(b, c, d)+x[12], 3)
		d = rot(d+f(a, b, c)+x[13], 7)
		c = rot(c+f(d, a, b)+x[14], 11)
		b = rot(b+f(c, d, a)+x[15], 19)

		// 第 2 轮
		a = rot(a+g(b, c, d)+x[0]+0x5a827999, 3)
		d = rot(d+g(a, b, c)+x[4]+0x5a827999, 5)
		c = rot(c+g(d, a, b)+x[8]+0x5a827999, 9)
		b = rot(b+g(c, d, a)+x[12]+0x5a827999, 13)
		a = rot(a+g(b, c, d)+x[1]+0x5a827999, 3)
		d = rot(d+g(a, b, c)+x[5]+0x5a827999, 5)
		c = rot(c+g(d, a, b)+x[9]+0x5a827999, 9)
		b = rot(b+g(c, d, a)+x[13]+0x5a827999, 13)
		a = rot(a+g(b, c, d)+x[2]+0x5a827999, 3)
		d = rot(d+g(a, b, c)+x[6]+0x5a827999, 5)
		c = rot(c+g(d, a, b)+x[10]+0x5a827999, 9)
		b = rot(b+g(c, d, a)+x[14]+0x5a827999, 13)
		a = rot(a+g(b, c, d)+x[3]+0x5a827999, 3)
		d = rot(d+g(a, b, c)+x[7]+0x5a827999, 5)
		c = rot(c+g(d, a, b)+x[11]+0x5a827999, 9)
		b = rot(b+g(c, d, a)+x[15]+0x5a827999, 13)

		// 第 3 轮
		a = rot(a+h(b, c, d)+x[0]+0x6ed9eba1, 3)
		d = rot(d+h(a, b, c)+x[8]+0x6ed9eba1, 9)
		c = rot(c+h(d, a, b)+x[4]+0x6ed9eba1, 11)
		b = rot(b+h(c, d, a)+x[12]+0x6ed9eba1, 15)
		a = rot(a+h(b, c, d)+x[2]+0x6ed9eba1, 3)
		d = rot(d+h(a, b, c)+x[10]+0x6ed9eba1, 9)
		c = rot(c+h(d, a, b)+x[6]+0x6ed9eba1, 11)
		b = rot(b+h(c, d, a)+x[14]+0x6ed9eba1, 15)
		a = rot(a+h(b, c, d)+x[1]+0x6ed9eba1, 3)
		d = rot(d+h(a, b, c)+x[9]+0x6ed9eba1, 9)
		c = rot(c+h(d, a, b)+x[5]+0x6ed9eba1, 11)
		b = rot(b+h(c, d, a)+x[13]+0x6ed9eba1, 15)
		a = rot(a+h(b, c, d)+x[3]+0x6ed9eba1, 3)
		d = rot(d+h(a, b, c)+x[11]+0x6ed9eba1, 9)
		c = rot(c+h(d, a, b)+x[7]+0x6ed9eba1, 11)
		b = rot(b+h(c, d, a)+x[15]+0x6ed9eba1, 15)

		a, b, c, d = a+aa, b+bb, c+cc, d+dd
	}

	var out [16]byte
	binary.LittleEndian.PutUint32(out[0:], a)
	binary.LittleEndian.PutUint32(out[4:], b)
	binary.LittleEndian.PutUint32(out[8:], c)
	binary.LittleEndian.PutUint32(out[12:], d)
	return out
}

// utf16le 把字符串按 UTF-16LE 编码（MSCHAPv2 的密码哈希输入）。
func utf16le(s string) []byte {
	r := []rune(s)
	out := make([]byte, 0, len(r)*2)
	for _, c := range r {
		if c > 0xFFFF {
			c -= 0x10000
			hi := 0xD800 + (c >> 10)
			lo := 0xDC00 + (c & 0x3FF)
			out = append(out, byte(hi), byte(hi>>8), byte(lo), byte(lo>>8))
			continue
		}
		out = append(out, byte(c), byte(c>>8))
	}
	return out
}

// desKey 把 7 字节展开成带奇校验位的 8 字节 DES 密钥。
func desKey(chunk []byte) [8]byte {
	var u uint64
	for i := 0; i < 7; i++ {
		u = u<<8 | uint64(chunk[i])
	}
	var key [8]byte
	for i := 0; i < 8; i++ {
		b := byte((u >> (7 * (7 - i))) & 0x7F)
		b <<= 1
		if bits.OnesCount8(b)%2 == 0 {
			b |= 1
		}
		key[i] = b
	}
	return key
}

// desEncryptBlock 用单块 DES 加密 8 字节。
func desEncryptBlock(key [8]byte, block []byte) []byte {
	c, err := des.NewCipher(key[:])
	if err != nil {
		return make([]byte, 8)
	}
	out := make([]byte, 8)
	c.Encrypt(out, block)
	return out
}

var (
	magic1           = []byte("Magic server to client signing constant")
	magic2           = []byte("Pad to make it do more than one iteration")
	hlakMagicSend    = []byte("On the client side, this is the send key; on the server side, it is the receive key.")
	hlakMagicReceive = []byte("On the client side, this is the receive key; on the server side, it is the send key.")
)

// MSCHAPv2 保存一次认证过程里的挑战与响应。
type MSCHAPv2 struct {
	Username      string
	Password      string
	authChallenge [16]byte
	peerChallenge [16]byte
	ntResponse    [24]byte

	pwHash     [16]byte // MD4(UTF16LE(password))
	pwHashHash [16]byte // MD4(pwHash)
}

// New 创建认证器。
func New(username, password string) *MSCHAPv2 {
	return &MSCHAPv2{Username: username, Password: password}
}

// SetAuthChallenge 记录服务器下发的 16 字节挑战。
func (m *MSCHAPv2) SetAuthChallenge(c []byte) error {
	if len(c) != 16 {
		return ErrBadChallenge
	}
	copy(m.authChallenge[:], c)
	return nil
}

// Response 计算 CHAP 响应值（24 字节）。peerChallenge 是本次随机生成的 16 字节。
func (m *MSCHAPv2) Response(peerChallenge []byte) ([]byte, error) {
	if len(peerChallenge) != 16 {
		return nil, ErrBadChallenge
	}
	copy(m.peerChallenge[:], peerChallenge)
	m.pwHash = MD4(utf16le(m.Password))
	m.pwHashHash = MD4(m.pwHash[:])

	h := sha1.New()
	h.Write(m.peerChallenge[:])
	h.Write(m.authChallenge[:])
	h.Write([]byte(m.Username))
	challenge := h.Sum(nil)[:8]

	// 21 字节：16 字节密码哈希 + 5 个 0，分成三把 7 字节 DES 密钥。
	z := make([]byte, 21)
	copy(z, m.pwHash[:])
	resp := make([]byte, 0, 24)
	for i := 0; i < 3; i++ {
		resp = append(resp, desEncryptBlock(desKey(z[i*7:(i+1)*7]), challenge)...)
	}
	copy(m.ntResponse[:], resp)
	return resp, nil
}

// VerifyAuthenticator 校验服务器返回的 "S=<hex>" 认证响应。
func (m *MSCHAPv2) VerifyAuthenticator(message []byte) bool {
	expected := m.AuthenticatorResponse()
	return len(message) >= len(expected) && equalFoldBytes(message[:len(expected)], expected)
}

// AuthenticatorResponse 计算期望的 "S=<hex>"（42 字节）。
func (m *MSCHAPv2) AuthenticatorResponse() []byte {
	// RFC 2759 §8.7：输入是 PasswordHashHash = MD4(PasswordHash)，只做两次 MD4。
	h := sha1.New()
	h.Write(m.peerChallenge[:])
	h.Write(m.authChallenge[:])
	h.Write([]byte(m.Username))
	challenge := h.Sum(nil)[:8]

	h = sha1.New()
	h.Write(m.pwHashHash[:])
	h.Write(m.ntResponse[:])
	h.Write(magic1)
	digest := h.Sum(nil)

	h = sha1.New()
	h.Write(digest)
	h.Write(challenge)
	h.Write(magic2)
	sum := h.Sum(nil)

	out := make([]byte, 0, 42)
	out = append(out, 'S', '=')
	out = append(out, []byte(hexUpper(sum))...)
	return out
}

// HLAK 派生 SSTP crypto binding 用的 32 字节 MSK 的一半（HLak）。
func (m *MSCHAPv2) HLAK() []byte {
	h := sha1.New()
	h.Write(m.pwHashHash[:])
	h.Write(m.ntResponse[:])
	h.Write(magic1)
	master := h.Sum(nil)[:16]

	pad1 := make([]byte, 40)
	pad2 := make([]byte, 40)
	for i := range pad2 {
		pad2[i] = 0xF2
	}

	hlak := make([]byte, 32)
	h = sha1.New()
	h.Write(master)
	h.Write(pad1)
	h.Write(hlakMagicSend)
	h.Write(pad2)
	copy(hlak[0:16], h.Sum(nil))

	h = sha1.New()
	h.Write(master)
	h.Write(pad1)
	h.Write(hlakMagicReceive)
	h.Write(pad2)
	copy(hlak[16:32], h.Sum(nil))
	return hlak
}

// NTResponse 返回已计算的 NT-Response（供测试断言）。
func (m *MSCHAPv2) NTResponse() []byte { return m.ntResponse[:] }

func hexUpper(b []byte) string {
	const table = "0123456789ABCDEF"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = table[v>>4]
		out[i*2+1] = table[v&0x0F]
	}
	return string(out)
}

func equalFoldBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if x >= 'a' && x <= 'z' {
			x -= 32
		}
		if y >= 'a' && y <= 'z' {
			y -= 32
		}
		if x != y {
			return false
		}
	}
	return true
}
