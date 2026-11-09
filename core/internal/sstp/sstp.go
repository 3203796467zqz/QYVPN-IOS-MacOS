// SPDX-License-Identifier: GPL-2.0-or-later
// Package sstp 实现 Secure Socket Tunneling Protocol 的控制包编解码与
// crypto binding（RFC 风格见 MS-SSTP）。所有多字节整数均为网络字节序。
package sstp

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
)

// 包类型：DATA 承载 PPP 帧，CONTROL 承载建链/心跳/断开。
const (
	PacketTypeData    uint16 = 0x1000
	PacketTypeControl uint16 = 0x1001
)

// 控制包消息类型。
const (
	MsgCallConnectRequest uint16 = 1
	MsgCallConnectAck     uint16 = 2
	MsgCallConnectNak     uint16 = 3
	MsgCallConnected      uint16 = 4
	MsgCallAbort          uint16 = 5
	MsgCallDisconnect     uint16 = 6
	MsgCallDisconnectAck  uint16 = 7
	MsgEchoRequest        uint16 = 8
	MsgEchoResponse       uint16 = 9
)

// 属性 ID。
const (
	AttrEncapsulatedProtocol byte = 1
	AttrStatusInfo           byte = 2
	AttrCryptoBinding        byte = 3
	AttrCryptoBindingRequest byte = 4
)

// 证书哈希算法。
const (
	HashSHA1   byte = 1
	HashSHA256 byte = 2
)

// HeaderSize 是 SSTP 包的固定头长度。
const HeaderSize = 4

// MaxFrame 是允许的最大 SSTP 帧长（PPP MRU 上限 + 头部）。
const MaxFrame = 4096

// PPP HDLC 固定前缀：地址 0xFF、控制 0x03。
var hdlcHeader = []byte{0xFF, 0x03}

// Attribute 是一个 SSTP 属性。
type Attribute struct {
	ID    byte
	Value []byte // 不含 4 字节属性头
}

// Control 是一个解码后的控制包。
type Control struct {
	Type       uint16
	Attributes []Attribute
	Extra      []byte // CALL_CONNECT_NAK 可能带尾随数据
}

// Attr 返回指定 ID 的第一个属性。
func (c *Control) Attr(id byte) *Attribute {
	for i := range c.Attributes {
		if c.Attributes[i].ID == id {
			return &c.Attributes[i]
		}
	}
	return nil
}

// EncodeControl 组装控制包。
func EncodeControl(msgType uint16, attrs []Attribute) []byte {
	size := 8
	for _, a := range attrs {
		size += 4 + len(a.Value)
	}
	buf := make([]byte, 8, size)
	binary.BigEndian.PutUint16(buf[0:], PacketTypeControl)
	binary.BigEndian.PutUint16(buf[2:], uint16(size))
	binary.BigEndian.PutUint16(buf[4:], msgType)
	binary.BigEndian.PutUint16(buf[6:], uint16(len(attrs)))
	for _, a := range attrs {
		var head [4]byte
		head[0] = 0
		head[1] = a.ID
		binary.BigEndian.PutUint16(head[2:], uint16(4+len(a.Value)))
		buf = append(buf, head[:]...)
		buf = append(buf, a.Value...)
	}
	return buf
}

// ParseControl 解析控制包（不含 4 字节 SSTP 头）。
func ParseControl(payload []byte) (*Control, error) {
	if len(payload) < 4 {
		return nil, errors.New("SSTP 控制包过短")
	}
	c := &Control{Type: binary.BigEndian.Uint16(payload[0:])}
	count := int(binary.BigEndian.Uint16(payload[2:]))
	rest := payload[4:]
	for i := 0; i < count; i++ {
		if len(rest) < 4 {
			return nil, errors.New("SSTP 属性长度无效")
		}
		length := int(binary.BigEndian.Uint16(rest[2:]))
		if length < 4 || length > len(rest) {
			return nil, errors.New("SSTP 属性长度无效")
		}
		c.Attributes = append(c.Attributes, Attribute{ID: rest[1], Value: rest[4:length]})
		rest = rest[length:]
	}
	c.Extra = append([]byte(nil), rest...)
	return c, nil
}

// CallConnectRequest 生成建链请求：Encapsulated-Protocol = 1（PPP）。
func CallConnectRequest() []byte {
	return EncodeControl(MsgCallConnectRequest, []Attribute{
		{ID: AttrEncapsulatedProtocol, Value: []byte{0x00, 0x01}},
	})
}

// EchoRequest / EchoResponse / Disconnect / DisconnectAck / Abort 都是无属性控制包。
func EchoRequest() []byte   { return EncodeControl(MsgEchoRequest, nil) }
func EchoResponse() []byte  { return EncodeControl(MsgEchoResponse, nil) }
func Disconnect() []byte    { return EncodeControl(MsgCallDisconnect, nil) }
func DisconnectAck() []byte { return EncodeControl(MsgCallDisconnectAck, nil) }
func Abort() []byte         { return EncodeControl(MsgCallAbort, nil) }

// CryptoBindingRequest 是 ACK 里带回来的哈希能力位图与服务器 nonce。
// 位图 bit0=SHA1、bit1=SHA256（值为 1 表示只支持 SHA1，2/3 表示支持 SHA256）。
type CryptoBindingRequest struct {
	Bitmask byte
	Nonce   []byte
}

// HashProtocol 按位图选择证书哈希算法：优先 SHA256。
func (r *CryptoBindingRequest) HashProtocol() (byte, error) {
	switch {
	case r.Bitmask&0x02 != 0:
		return HashSHA256, nil
	case r.Bitmask&0x01 != 0:
		return HashSHA1, nil
	}
	return 0, fmt.Errorf("不支持的 SSTP 证书哈希位图：%d", r.Bitmask)
}

// ParseCryptoBindingRequest 从 ACK 包中取哈希位图与 nonce。
func ParseCryptoBindingRequest(c *Control) (*CryptoBindingRequest, error) {
	a := c.Attr(AttrCryptoBindingRequest)
	if a == nil || len(a.Value) < 36 {
		return nil, errors.New("SSTP 建链响应缺少 crypto binding 请求")
	}
	return &CryptoBindingRequest{Bitmask: a.Value[3], Nonce: append([]byte(nil), a.Value[4:36]...)}, nil
}

// CallConnected 生成建链完成包（含 CMAC）。
//
// cmac 的算法固定为 MS-SSTP 规定的做法：
//
//	CMK  = HMAC(HLAK, "SSTP inner method derived CMK" || HashSize(2) || 0x01)
//	CMAC = HMAC(CMK, 整个 CALL_CONNECTED 包，其中 compound MAC 位置填 0)
func CallConnected(nonce, certHash []byte, hashProtocol byte, hlak []byte) ([]byte, error) {
	size := 32
	if hashProtocol == HashSHA1 {
		size = 20
	}
	if len(nonce) != 32 {
		return nil, errors.New("SSTP nonce 长度无效")
	}
	if len(certHash) < size {
		return nil, errors.New("服务器证书哈希长度无效")
	}
	if len(hlak) < 16 {
		return nil, errors.New("MSCHAPv2 密钥不可用")
	}
	body := make([]byte, 0, 104)
	body = append(body, 0, 0, 0, hashProtocol)
	body = append(body, nonce...)
	body = append(body, certHash[:size]...)
	body = append(body, make([]byte, 32)...) // compound MAC 占位
	frame := EncodeControl(MsgCallConnected, []Attribute{{ID: AttrCryptoBinding, Value: body}})

	seed := []byte("SSTP inner method derived CMK")
	cmkInput := make([]byte, 0, len(seed)+3)
	cmkInput = append(cmkInput, seed...)
	cmkInput = append(cmkInput, byte(size>>8), byte(size), 0x01)

	mac := hmacFor(hashProtocol, hlak)
	cmk := mac(cmkInput)
	macCMK := hmacFor(hashProtocol, cmk)
	cmac := macCMK(frame)

	// compound MAC 位于包尾 32 字节。
	copy(frame[len(frame)-32:], cmac)
	return frame, nil
}

func hmacFor(hashProtocol byte, key []byte) func([]byte) []byte {
	return func(data []byte) []byte {
		var h hash.Hash
		if hashProtocol == HashSHA1 {
			h = hmac.New(sha1.New, key)
		} else {
			h = hmac.New(sha256.New, key)
		}
		h.Write(data)
		return h.Sum(nil)
	}
}

// CertHash 返回服务器证书的哈希（按协商算法）。
func CertHash(hashProtocol byte, certDER []byte) []byte {
	if hashProtocol == HashSHA1 {
		sum := sha1.Sum(certDER)
		return sum[:]
	}
	sum := sha256.Sum256(certDER)
	return sum[:]
}

// EncodeData 把 IP 包封装成 SSTP DATA 帧。
func EncodeData(ip []byte, protocol uint16) []byte {
	size := 8 + len(ip)
	buf := make([]byte, 8, size)
	binary.BigEndian.PutUint16(buf[0:], PacketTypeData)
	binary.BigEndian.PutUint16(buf[2:], uint16(size))
	copy(buf[4:], hdlcHeader)
	binary.BigEndian.PutUint16(buf[6:], protocol)
	return append(buf, ip...)
}

// SplitData 从 DATA 帧中取出 PPP 协议号与载荷。
func SplitData(payload []byte) (protocol uint16, data []byte, err error) {
	if len(payload) < 4 {
		return 0, nil, errors.New("SSTP 数据帧过短")
	}
	if payload[0] != 0xFF || payload[1] != 0x03 {
		return 0, nil, errors.New("SSTP 数据帧缺少 PPP HDLC 头")
	}
	return binary.BigEndian.Uint16(payload[2:]), payload[4:], nil
}
