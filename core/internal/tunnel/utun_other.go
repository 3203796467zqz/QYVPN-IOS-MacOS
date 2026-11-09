// SPDX-License-Identifier: GPL-2.0-or-later
//go:build !darwin

package tunnel

import "net"

// Open 在非 macOS 平台不可用：内核 utun 是 darwin 专有的。
func Open(ip net.IP, dns []net.IP, mtu int, opts Options) (Device, error) {
	return nil, ErrUnsupported
}

func ifaceStats(name string) (Stats, error) { return Stats{}, ErrUnsupported }
