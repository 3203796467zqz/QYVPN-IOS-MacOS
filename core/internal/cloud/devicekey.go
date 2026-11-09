// SPDX-License-Identifier: GPL-2.0-or-later
package cloud

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"sync"

	"qingyou/qyvpn/internal/paths"
)

var (
	deviceKeyOnce  sync.Once
	deviceKeyValue string
	deviceKeyErr   error
)

// deviceKeyFromMachine 返回本机统计用的设备键：首次运行时随机生成 32 字节并落盘，
// 之后保持不变。它只用于把同一台设备的累计流量去重，不含任何硬件信息。
func deviceKeyFromMachine() (string, error) {
	deviceKeyOnce.Do(func() {
		if err := paths.EnsureStateDir(); err != nil {
			deviceKeyErr = err
			return
		}
		file := paths.DeviceKeyFile()
		if data, err := os.ReadFile(file); err == nil {
			key := strings.TrimSpace(string(data))
			if validKey(key) {
				deviceKeyValue = key
				return
			}
		}
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err != nil {
			deviceKeyErr = err
			return
		}
		key := hex.EncodeToString(buf)
		if err := os.WriteFile(file, []byte(key+"\n"), 0o600); err != nil {
			deviceKeyErr = err
			return
		}
		_ = os.Chmod(file, 0o600)
		deviceKeyValue = key
	})
	if deviceKeyErr != nil {
		return "", deviceKeyErr
	}
	if deviceKeyValue == "" {
		return "", errors.New("设备标识不可用")
	}
	return deviceKeyValue, nil
}

func validKey(key string) bool {
	if len(key) != 64 {
		return false
	}
	for _, c := range key {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
