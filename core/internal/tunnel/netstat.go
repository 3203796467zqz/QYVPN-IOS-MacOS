// SPDX-License-Identifier: GPL-2.0-or-later
package tunnel

import (
	"errors"
	"strconv"
	"strings"
)

// parseNetstat 从 `netstat -ibn` 的输出里取出指定接口的累计收发字节。
//
// 输出示例（macOS）：
//
//	Name  Mtu   Network       Address            Ipkts Ierrs     Ibytes    Opkts Oerrs     Obytes  Coll
//	utun4 1400  <Link#12>                        12345     0    1234567     1234     0     7654321     0
//	utun4 1400  10.77.88.11   10.77.88.11         12345     0    1234567     1234     0     7654321     0
//
// 只取 <Link#> 那一行（链路层累计值），从右往左数固定列，避免 Address 缺列时错位。
func parseNetstat(out, iface string) (Stats, error) {
	var best *Stats
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 || fields[0] != iface {
			continue
		}
		if !strings.HasPrefix(fields[2], "<Link#") {
			continue
		}
		n := len(fields)
		rx, err1 := strconv.ParseUint(fields[n-5], 10, 64)
		tx, err2 := strconv.ParseUint(fields[n-2], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		s := Stats{Rx: rx, Tx: tx}
		best = &s
		break
	}
	if best == nil {
		return Stats{}, errors.New("没有找到接口 " + iface + " 的统计行")
	}
	return *best, nil
}
