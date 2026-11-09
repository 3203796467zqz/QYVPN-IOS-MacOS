// SPDX-License-Identifier: GPL-2.0-or-later
// Package ppp 编解码 SSTP 隧道内的 PPP 帧（LCP / CHAP / EAP / IPCP 与 IPv4 数据）。
package ppp

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// PPP 协议号。
const (
	ProtoIPv4   uint16 = 0x0021
	ProtoIPv6   uint16 = 0x0057
	ProtoLCP    uint16 = 0xC021
	ProtoPAP    uint16 = 0xC023
	ProtoCHAP   uint16 = 0xC223
	ProtoEAP    uint16 = 0xC227
	ProtoIPCP   uint16 = 0x8021
	ProtoIPv6CP uint16 = 0x8057
)

// 配置类代码（LCP 与 IPCP 共用编号）。
const (
	CodeConfigureRequest byte = 1
	CodeConfigureAck     byte = 2
	CodeConfigureNak     byte = 3
	CodeConfigureReject  byte = 4
	CodeTerminateRequest byte = 5
	CodeTerminateAck     byte = 6
	CodeCodeReject       byte = 7
	CodeProtocolReject   byte = 8
	CodeEchoRequest      byte = 9
	CodeEchoReply        byte = 10
	CodeDiscardRequest   byte = 11
)

// CHAP / EAP 代码。
const (
	CodeChallenge byte = 1
	CodeResponse  byte = 2
	CodeSuccess   byte = 3
	CodeFailure   byte = 4
)

// EAP 类型。
const (
	EAPIdentity     byte = 1
	EAPNotification byte = 2
	EAPNak          byte = 3
	EAPMSAuth       byte = 26 // EAP-MSCHAPv2
)

// LCP 选项类型。
const (
	OptMRU  byte = 1
	OptAuth byte = 3
)

// IPCP 选项类型。
const (
	OptIP  byte = 3
	OptDNS byte = 0x81
)

// CHAP 算法：MSCHAPv2。
const ChapMSCHAPv2 byte = 0x81

// Option 是一个 PPP 配置选项（类型 + 值，不含 2 字节头）。
type Option struct {
	Type  byte
	Value []byte
}

// EncodePPP 把 PPP 包封装成 SSTP DATA 帧。
func EncodePPP(protocol uint16, code, id byte, data []byte) []byte {
	total := 12 + len(data)
	buf := make([]byte, 12, total)
	binary.BigEndian.PutUint16(buf[0:], 0x1000) // SSTP DATA
	binary.BigEndian.PutUint16(buf[2:], uint16(total))
	buf[4], buf[5] = 0xFF, 0x03
	binary.BigEndian.PutUint16(buf[6:], protocol)
	buf[8], buf[9] = code, id
	binary.BigEndian.PutUint16(buf[10:], uint16(4+len(data)))
	return append(buf, data...)
}

// ParsePPP 解析 DATA 帧里的 PPP 头，返回 code、id 与数据段。
func ParsePPP(data []byte) (code, id byte, payload []byte, err error) {
	if len(data) < 4 {
		return 0, 0, nil, errors.New("PPP 帧过短")
	}
	length := int(binary.BigEndian.Uint16(data[2:]))
	if length < 4 || length > len(data) {
		return 0, 0, nil, fmt.Errorf("PPP 长度无效：%d", length)
	}
	return data[0], data[1], data[4:length], nil
}

// EncodeOptions 把选项列表编码成配置帧数据段。
func EncodeOptions(opts []Option) []byte {
	size := 0
	for _, o := range opts {
		size += 2 + len(o.Value)
	}
	out := make([]byte, 0, size)
	for _, o := range opts {
		out = append(out, o.Type, byte(2+len(o.Value)))
		out = append(out, o.Value...)
	}
	return out
}

// ParseOptions 解析配置帧里的选项；未知类型按原始值保留。
func ParseOptions(data []byte) ([]Option, error) {
	var out []Option
	for len(data) >= 2 {
		length := int(data[1])
		if length < 2 || length > len(data) {
			return out, errors.New("PPP 选项长度无效")
		}
		out = append(out, Option{Type: data[0], Value: append([]byte(nil), data[2:length]...)})
		data = data[length:]
	}
	return out, nil
}

// Find 返回指定类型的第一个选项。
func Find(opts []Option, typ byte) *Option {
	for i := range opts {
		if opts[i].Type == typ {
			return &opts[i]
		}
	}
	return nil
}

// MRUOption 构造 MRU 选项。
func MRUOption(mru int) Option {
	b := make([]byte, 2)
	binary.BigEndian.PutUint16(b, uint16(mru))
	return Option{Type: OptMRU, Value: b}
}

// AuthOption 构造认证协议选项（LCP type 3）。
func AuthOption(protocol uint16, algo byte) Option {
	b := make([]byte, 2, 3)
	binary.BigEndian.PutUint16(b, protocol)
	if algo != 0 {
		b = append(b, algo)
	}
	return Option{Type: OptAuth, Value: b}
}

// AddressOption 构造 IP 或 DNS 选项（4 字节地址）。
func AddressOption(typ byte, addr [4]byte) Option {
	return Option{Type: typ, Value: append([]byte(nil), addr[:]...)}
}

// Addr 从选项值里读 4 字节 IPv4。
func (o *Option) Addr() ([4]byte, bool) {
	var a [4]byte
	if o == nil || len(o.Value) < 4 {
		return a, false
	}
	copy(a[:], o.Value[:4])
	return a, true
}

// Uint16 从选项值里读一个 16 位整数（MRU）。
func (o *Option) Uint16() (int, bool) {
	if o == nil || len(o.Value) < 2 {
		return 0, false
	}
	return int(binary.BigEndian.Uint16(o.Value)), true
}

// Protocol 返回认证选项里的协议号。
func (o *Option) Protocol() (uint16, bool) {
	if o == nil || len(o.Value) < 2 {
		return 0, false
	}
	return binary.BigEndian.Uint16(o.Value), true
}

// Echo 构造 LCP Echo-Request / Echo-Reply（带 magic number 与可选数据）。
func Echo(code, id byte, magic uint32, data []byte) []byte {
	b := make([]byte, 4, 4+len(data))
	binary.BigEndian.PutUint32(b, magic)
	b = append(b, data...)
	return EncodePPP(ProtoLCP, code, id, b)
}

// ProtocolReject 构造 LCP Protocol-Reject。
func ProtocolReject(id byte, rejected uint16, info []byte) []byte {
	b := make([]byte, 2, 2+len(info))
	binary.BigEndian.PutUint16(b, rejected)
	b = append(b, info...)
	return EncodePPP(ProtoLCP, CodeProtocolReject, id, b)
}
