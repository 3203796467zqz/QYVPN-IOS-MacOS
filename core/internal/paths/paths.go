// SPDX-License-Identifier: GPL-2.0-or-later
// Package paths 统一管理客户端在本机的文件位置。
//
// 特权组件（root）与界面进程共用同一套路径；测试与开发时可用 QYVPN_STATE_DIR 覆盖。
package paths

import (
	"os"
	"path/filepath"
	"runtime"
)

const appName = "QingyouPrivateCloud"

// StateDir 返回客户端状态目录（日志、设置、设备键）。
func StateDir() string {
	if v := os.Getenv("QYVPN_STATE_DIR"); v != "" {
		return v
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join("/Library/Application Support", appName)
	}
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, appName)
	}
	return filepath.Join(os.TempDir(), appName)
}

// EnsureStateDir 创建状态目录（0700）。
func EnsureStateDir() error {
	if err := os.MkdirAll(StateDir(), 0o700); err != nil {
		return err
	}
	_ = os.Chmod(StateDir(), 0o700)
	return nil
}

// LogFile 返回连接日志路径（约 1 MiB 轮转）。
func LogFile() string { return filepath.Join(StateDir(), "connection.log") }

// SettingsFile 返回界面设置文件（JSON）。
func SettingsFile() string { return filepath.Join(StateDir(), "settings.json") }

// DeviceKeyFile 返回统计上报的设备标识。
func DeviceKeyFile() string { return filepath.Join(StateDir(), "device.key") }

// UserTokenFile 返回登录后的用户令牌（0600）。
//
// 后台 1.9 起按用户套餐下发节点，令牌等价于账号：只有 root 可读，
// 退出登录时删除。与移动端 mobile 包用的是同一个文件名，便于排查。
func UserTokenFile() string { return filepath.Join(StateDir(), "user.token") }

// SocketPath 是界面进程与特权组件通信的 unix socket。
func SocketPath() string {
	if v := os.Getenv("QYVPN_SOCKET"); v != "" {
		return v
	}
	return "/var/run/cn.qo8.qyvpn.engine.sock"
}

// HelperBinary 是特权组件安装后的位置。
func HelperBinary() string {
	return "/Library/PrivilegedHelperTools/cn.qo8.qyvpn.engine"
}

// HelperLabel 是 launchd 服务标识。
const HelperLabel = "cn.qo8.qyvpn.engine"

// HelperPlist 是 launchd 描述文件位置。
func HelperPlist() string {
	return "/Library/LaunchDaemons/" + HelperLabel + ".plist"
}

// SupportDir 是界面进程自己的数据目录（~/Library/Application Support/...）。
func UserSupportDir() string {
	if v := os.Getenv("QYVPN_USER_DIR"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return StateDir()
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", appName)
	}
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, appName)
	}
	return filepath.Join(home, "."+appName)
}
