// SPDX-License-Identifier: GPL-2.0-or-later
// Package tunnel 管理 macOS 的 utun 设备、路由与 DNS。
package tunnel

import (
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strings"
)

// ErrUnsupported 表示当前平台没有实现 utun（在非 macOS 上构建时）。
var ErrUnsupported = errors.New("当前平台不支持系统隧道")

// Stats 是接口累计收发字节数。
type Stats struct {
	Rx uint64
	Tx uint64
}

// Device 是一条已经配置好地址与路由的隧道。
type Device interface {
	// Name 返回接口名，例如 utun4。
	Name() string
	// Read 读取一个 IP 包。
	Read(buf []byte) (int, error)
	// Write 写入一个 IP 包。
	Write(p []byte) (int, error)
	// Stats 返回接口累计流量。
	Stats() (Stats, error)
	// Close 撤销路由/DNS 并关闭设备。
	Close() error
}

// Options 是创建隧道的参数。
type Options struct {
	// ServerHost 是 SSTP 服务器地址，需要为它保留一条走物理网关的主机路由，
	// 否则全量路由会把隧道自己的流量也送进隧道。
	ServerHost string
	// BypassCIDRs 是直连网段（例如国内地址），这些网段不经过隧道。
	BypassCIDRs []string
	// Log 用于记录命令执行结果。
	Log func(format string, args ...any)
}

func (o *Options) logs(format string, args ...any) {
	if o != nil && o.Log != nil {
		o.Log(format, args...)
	}
}

// 命令执行的统一封装：失败时带上 stderr 便于排查。
func run(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		return text, fmt.Errorf("%s %s 失败：%s", name, strings.Join(args, " "), text)
	}
	return text, nil
}

// parseDefaultRoute 从 `route -n get default` 的输出里取默认网关与出口网卡。
func parseDefaultRoute(out string) (gateway, iface string) {
	for _, line := range strings.Split(out, "\n") {
		fields := strings.SplitN(strings.TrimSpace(line), ":", 2)
		if len(fields) != 2 {
			continue
		}
		key := strings.TrimSpace(fields[0])
		value := strings.TrimSpace(fields[1])
		switch key {
		case "gateway":
			gateway = value
		case "interface":
			iface = value
		}
	}
	return gateway, iface
}

// normalizeCIDR 把 "1.2.3.0/24" 之类的字符串校验一遍，非法值直接丢弃。
func normalizeCIDR(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasPrefix(s, "#") {
		return "", false
	}
	if !strings.Contains(s, "/") {
		if net.ParseIP(s) == nil {
			return "", false
		}
		return s + "/32", true
	}
	_, ipnet, err := net.ParseCIDR(s)
	if err != nil {
		return "", false
	}
	return ipnet.String(), true
}
