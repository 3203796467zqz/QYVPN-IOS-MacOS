// SPDX-License-Identifier: GPL-2.0-or-later
//go:build darwin

package tunnel

import (
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"
)

const (
	afSystem     = 32
	afSysControl = 2
	sysprotoCtrl = 2
	// CTLIOCGINFO = _IOWR('N', 3, struct ctl_info) —— 结构体 100 字节。
	ctlIOCGInfo = 0xc064_4e03

	utunControlName = "com.apple.net.utun_control"
	dnsStateKey     = "State:/Network/Service/cn.qo8.qyvpn/DNS"
)

type ctlInfo struct {
	ID   uint32
	Name [96]byte
}

type sockaddrCtl struct {
	Len      uint8
	Family   uint8
	SysAddr  uint16
	ID       uint32
	Unit     uint32
	Reserved [5]uint32
}

// utunDevice 是 darwin 上的隧道实现。
type utunDevice struct {
	fd     int
	name   string
	opts   Options
	routes []string // 已安装的路由（关闭时逐条删除）
	dnsOn  bool
	closed bool
}

// Open 创建 utun 设备、配置地址、路由与 DNS。需要 root 权限。
func Open(ip net.IP, dns []net.IP, mtu int, opts Options) (Device, error) {
	if ip == nil || ip.To4() == nil {
		return nil, fmt.Errorf("隧道地址无效：%v", ip)
	}
	if mtu <= 0 || mtu > 1500 {
		mtu = 1400
	}
	dev := &utunDevice{fd: -1, opts: opts}

	fd, name, err := createUtun()
	if err != nil {
		return nil, err
	}
	dev.fd = fd
	dev.name = name
	opts.logs("已创建隧道接口 %s（本机 %s，MTU %d）", name, ip, mtu)

	// 先记录物理网关，稍后为服务器地址保留主机路由。
	gateway, physIface := "", ""
	if out, err := run("route", "-n", "get", "default"); err == nil {
		gateway, physIface = parseDefaultRoute(out)
	}
	if gateway == "" && physIface == "" {
		dev.closeFD()
		return nil, fmt.Errorf("没有找到默认路由，当前网络可能未连接")
	}

	setTimeout := func(seconds int) {
		tv := syscall.Timeval{Sec: int64(seconds)}
		_ = syscall.SetsockoptTimeval(fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &tv)
		_ = syscall.SetsockoptTimeval(fd, syscall.SOL_SOCKET, syscall.SO_SNDTIMEO, &tv)
	}
	setTimeout(1)

	// 1) 接口地址：点对点隧道，对端与本端同址即可。
	// 不同 macOS 版本对 netmask 参数宽容度不同，失败时退回不带 netmask 的写法。
	if _, err := run("ifconfig", name, "inet", ip.String(), ip.String(), "netmask", "255.255.255.255", "mtu", fmt.Sprint(mtu), "up"); err != nil {
		if _, err2 := run("ifconfig", name, "inet", ip.String(), ip.String(), "mtu", fmt.Sprint(mtu), "up"); err2 != nil {
			dev.closeFD()
			return nil, err
		}
	}

	// 2) 服务器主机路由必须仍然走物理网关，否则隧道会自己套自己。
	if opts.ServerHost != "" {
		target := opts.ServerHost
		if ip := net.ParseIP(target); ip == nil {
			// 域名：用当前解析结果（连接已经建立，解析结果可用）。
			if addrs, err := net.LookupIP(target); err == nil && len(addrs) > 0 {
				target = addrs[0].String()
			}
		}
		if net.ParseIP(target) != nil {
			if gateway != "" {
				if _, err := run("route", "-n", "add", "-host", target, gateway); err == nil {
					dev.routes = append(dev.routes, "host:"+target)
					opts.logs("已为服务器 %s 保留物理网关路由（%s）", target, gateway)
				}
			} else if physIface != "" {
				if _, err := run("route", "-n", "add", "-host", target, "-interface", physIface); err == nil {
					dev.routes = append(dev.routes, "host:"+target)
				}
			}
		}
	}

	// 3) 默认路由改用两段 /1 覆盖，避免直接改写系统默认路由。
	for _, netw := range []string{"0.0.0.0/1", "128.0.0.0/1"} {
		if _, err := run("route", "-n", "add", "-net", netw, "-interface", name); err != nil {
			dev.Close()
			return nil, fmt.Errorf("安装隧道路由失败：%w", err)
		}
		dev.routes = append(dev.routes, "net:"+netw)
	}
	opts.logs("已把默认路由切到 %s", name)

	// 4) 直连网段（可选）：走回原网关。
	if len(opts.BypassCIDRs) > 0 && gateway != "" {
		count := dev.addBypassRoutes(opts.BypassCIDRs, gateway)
		opts.logs("已安装 %d 条直连路由（不经过隧道）", count)
	}

	// 5) DNS：只对本次连接生效，断开时移除。
	if len(dns) > 0 {
		if err := dev.applyDNS(dns); err != nil {
			opts.logs("设置隧道 DNS 失败（将沿用系统 DNS）：%v", err)
		} else {
			dev.dnsOn = true
			opts.logs("隧道 DNS 已生效：%s", joinIPs(dns))
		}
	}
	return dev, nil
}

// createUtun 打开内核 utun 控制通道，返回 fd 与接口名。
func createUtun() (int, string, error) {
	fd, err := syscall.Socket(afSystem, syscall.SOCK_DGRAM, sysprotoCtrl)
	if err != nil {
		return -1, "", fmt.Errorf("创建 utun 控制通道失败（需要管理员权限）：%w", err)
	}
	var info ctlInfo
	copy(info.Name[:], utunControlName)
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), ctlIOCGInfo, uintptr(unsafe.Pointer(&info))); errno != 0 {
		syscall.Close(fd)
		return -1, "", fmt.Errorf("查询 utun 控制通道失败：%v", errno)
	}

	sa := sockaddrCtl{
		Len:     uint8(unsafe.Sizeof(sockaddrCtl{})),
		Family:  afSystem,
		SysAddr: afSysControl,
		ID:      info.ID,
		Unit:    0, // 由内核分配
	}
	if _, _, errno := syscall.Syscall(syscall.SYS_CONNECT, uintptr(fd), uintptr(unsafe.Pointer(&sa)), uintptr(sa.Len)); errno != 0 {
		syscall.Close(fd)
		return -1, "", fmt.Errorf("连接 utun 控制通道失败：%v", errno)
	}

	var got sockaddrCtl
	got.Len = uint8(unsafe.Sizeof(got))
	length := uint32(unsafe.Sizeof(got))
	if _, _, errno := syscall.Syscall(syscall.SYS_GETSOCKNAME, uintptr(fd), uintptr(unsafe.Pointer(&got)), uintptr(unsafe.Pointer(&length))); errno != 0 {
		syscall.Close(fd)
		return -1, "", fmt.Errorf("读取 utun 接口编号失败：%v", errno)
	}
	if got.Unit == 0 {
		syscall.Close(fd)
		return -1, "", fmt.Errorf("utun 接口编号无效")
	}
	return fd, fmt.Sprintf("utun%d", got.Unit-1), nil
}

func (d *utunDevice) Name() string { return d.name }

// Read 读取一个 IP 包（内核会在前面附加 4 字节地址族头）。
func (d *utunDevice) Read(buf []byte) (int, error) {
	for {
		if d.closed {
			return 0, os.ErrClosed
		}
		n, err := syscall.Read(d.fd, buf)
		if err != nil {
			if err == syscall.EAGAIN || err == syscall.EWOULDBLOCK || err == syscall.EINTR {
				continue
			}
			return 0, err
		}
		if n <= 4 {
			continue
		}
		// 头部 4 字节是 AF_INET(2) / AF_INET6(30)，剥离后交给上层。
		copy(buf, buf[4:n])
		return n - 4, nil
	}
}

// Write 写入一个 IP 包，需要补回 4 字节地址族头。
func (d *utunDevice) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	family := uint32(syscall.AF_INET)
	if p[0]>>4 == 6 {
		family = 30 // AF_INET6
	}
	packet := make([]byte, 4+len(p))
	binary.BigEndian.PutUint32(packet, family)
	copy(packet[4:], p)
	n, err := syscall.Write(d.fd, packet)
	if err != nil {
		return 0, err
	}
	if n <= 4 {
		return 0, nil
	}
	return n - 4, nil
}

// Stats 读取接口累计流量。
func (d *utunDevice) Stats() (Stats, error) { return ifaceStats(d.name) }

// applyDNS 通过 scutil 增加一个「所有域名都匹配」的补充解析器。
func (d *utunDevice) applyDNS(dns []net.IP) error {
	var servers []string
	for _, ip := range dns {
		if ip != nil {
			servers = append(servers, ip.String())
		}
	}
	if len(servers) == 0 {
		return nil
	}
	script := "d.init\n" +
		"d.add ServerAddresses * " + strings.Join(servers, " ") + "\n" +
		"d.add SupplementalMatchDomains * \"\"\n" +
		"set " + dnsStateKey + "\n"
	return scutil(script)
}

func (d *utunDevice) removeDNS() error {
	return scutil("remove " + dnsStateKey + "\n")
}

func scutil(script string) error {
	cmd := exec.Command("/usr/sbin/scutil")
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("scutil 执行失败：%s %v", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// addBypassRoutes 用一份脚本一次性安装直连路由，避免几千次 fork。
func (d *utunDevice) addBypassRoutes(cidrs []string, gateway string) int {
	var sb strings.Builder
	sb.WriteString("#!/bin/sh\n")
	count := 0
	for _, raw := range cidrs {
		cidr, ok := normalizeCIDR(raw)
		if !ok {
			continue
		}
		sb.WriteString("route -n add -net " + cidr + " " + gateway + " >/dev/null 2>&1\n")
		d.routes = append(d.routes, "bypass:"+cidr)
		count++
	}
	if count == 0 {
		return 0
	}
	if err := runScript(sb.String()); err != nil {
		d.opts.logs("安装直连路由时出现问题：%v", err)
	}
	return count
}

// Close 撤销 DNS、删除路由并关闭设备。
func (d *utunDevice) Close() error {
	if d.closed {
		return nil
	}
	d.closed = true
	if d.dnsOn {
		if err := d.removeDNS(); err != nil {
			d.opts.logs("移除隧道 DNS 失败：%v", err)
		}
		d.dnsOn = false
		_ = runScript("/usr/bin/dscacheutil -flushcache >/dev/null 2>&1\n")
	}
	if len(d.routes) > 0 {
		var sb strings.Builder
		sb.WriteString("#!/bin/sh\n")
		for _, r := range d.routes {
			switch {
			case strings.HasPrefix(r, "host:"):
				sb.WriteString("route -n delete -host " + strings.TrimPrefix(r, "host:") + " >/dev/null 2>&1\n")
			case strings.HasPrefix(r, "net:"):
				sb.WriteString("route -n delete -net " + strings.TrimPrefix(r, "net:") + " >/dev/null 2>&1\n")
			case strings.HasPrefix(r, "bypass:"):
				sb.WriteString("route -n delete -net " + strings.TrimPrefix(r, "bypass:") + " >/dev/null 2>&1\n")
			}
		}
		_ = runScript(sb.String())
		d.routes = nil
	}
	return d.closeFD()
}

func (d *utunDevice) closeFD() error {
	if d.fd < 0 {
		return nil
	}
	err := syscall.Close(d.fd)
	d.fd = -1
	return err
}

// ifaceStats 解析 `netstat -ibn` 取接口累计字节。
func ifaceStats(name string) (Stats, error) {
	out, err := run("netstat", "-ibn")
	if err != nil {
		return Stats{}, err
	}
	return parseNetstat(out, name)
}

func joinIPs(ips []net.IP) string {
	parts := make([]string, 0, len(ips))
	for _, ip := range ips {
		parts = append(parts, ip.String())
	}
	return strings.Join(parts, ", ")
}

// runScript 把一批命令写成临时脚本一次性执行，避免为每条路由 fork 一次。
func runScript(script string) error {
	f, err := os.CreateTemp("", "qyvpn-*.sh")
	if err != nil {
		return err
	}
	path := f.Name()
	defer os.Remove(path)
	if _, err := f.WriteString(script); err != nil {
		f.Close()
		return err
	}
	f.Close()
	if err := os.Chmod(path, 0o700); err != nil {
		return err
	}
	out, err := exec.Command("/bin/sh", path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	return nil
}
