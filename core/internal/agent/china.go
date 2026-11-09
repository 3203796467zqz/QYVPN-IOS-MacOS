// SPDX-License-Identifier: GPL-2.0-or-later
package agent

import (
	_ "embed"
	"fmt"
	"strings"
)

// chinaCIDRs 内置的国内直连网段表（见 assets/china-cidrs.txt 的说明与来源）。
//
//go:embed assets/china-cidrs.txt
var chinaCIDRs string

// loadChinaCIDRs 解析内置的国内网段表。
func loadChinaCIDRs() ([]string, error) {
	var out []string
	for _, line := range strings.Split(chinaCIDRs, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("国内直连规则为空")
	}
	return out, nil
}
