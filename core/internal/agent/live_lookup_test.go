// SPDX-License-Identifier: GPL-2.0-or-later
//go:build live

package agent

import "net"

// netLookup 让联调用例拿到解析结果，同时保留错误。
func netLookup(host string) ([]string, error) {
	addrs, err := net.LookupIP(host)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(addrs))
	for _, ip := range addrs {
		out = append(out, ip.String())
	}
	return out, nil
}
