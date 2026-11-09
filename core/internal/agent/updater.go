// SPDX-License-Identifier: GPL-2.0-or-later
package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"qingyou/qyvpn/internal/cloud"
	"qingyou/qyvpn/internal/paths"
)

// UpdateInfo 是界面展示的更新检查结果。
type UpdateInfo struct {
	Available bool   `json:"available"`
	Version   string `json:"version"`
	Notes     string `json:"notes"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
	Message   string `json:"message"`
}

// CheckUpdate 查询后台的 macOS 更新通道。
func (a *Agent) CheckUpdate(ctx context.Context) UpdateInfo {
	rel, err := a.client.CheckRelease(ctx)
	if err != nil {
		if errors.Is(err, cloud.ErrNoReleaseChannel) {
			return UpdateInfo{Message: "当前已是最新版本"}
		}
		return UpdateInfo{Message: "检查更新失败：" + err.Error()}
	}
	if compareVersion(rel.Version, cloud.Version) <= 0 {
		return UpdateInfo{Message: "当前已是最新版本"}
	}
	return UpdateInfo{
		Available: true,
		Version:   rel.Version,
		Notes:     rel.Notes,
		Size:      rel.Size,
		SHA256:    rel.SHA256,
		Message:   "发现新版本 " + rel.Version,
	}
}

// DownloadUpdate 下载更新包到用户下载目录，返回文件路径。
// 安装仍需用户手动完成：macOS 的 .app 不能像 Windows 那样原地替换运行中的程序。
func (a *Agent) DownloadUpdate(ctx context.Context) (string, error) {
	rel, err := a.client.CheckRelease(ctx)
	if err != nil {
		return "", err
	}
	data, err := a.client.Download(ctx, rel)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(paths.UserSupportDir(), "updates")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	name := "清幽私人云_macOS_" + rel.Version + ".zip"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", err
	}
	a.logf("已下载更新包 %s（%d 字节）", path, len(data))
	return path, nil
}

// compareVersion 比较 "1.2.3" 形式的版本号，返回 -1/0/1。
func compareVersion(a, b string) int {
	parse := func(s string) []int {
		var out []int
		n := 0
		seen := false
		for i := 0; i <= len(s); i++ {
			if i == len(s) || s[i] == '.' {
				if seen {
					out = append(out, n)
				}
				n, seen = 0, false
				continue
			}
			if s[i] < '0' || s[i] > '9' {
				break
			}
			n = n*10 + int(s[i]-'0')
			seen = true
		}
		return out
	}
	av, bv := parse(a), parse(b)
	for i := 0; i < len(av) || i < len(bv); i++ {
		var x, y int
		if i < len(av) {
			x = av[i]
		}
		if i < len(bv) {
			y = bv[i]
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

// 供 main 里「等待一次后台可达」时使用。
func (a *Agent) waitBackend(ctx context.Context) error {
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, err := a.client.Config(ctx); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("后台暂时不可达")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}
