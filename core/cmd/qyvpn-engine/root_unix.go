// SPDX-License-Identifier: GPL-2.0-or-later
//go:build !windows

package main

import "os"

// isRoot 判断当前是否以 root 运行（安装与内核运行都需要）。
func isRoot() bool { return os.Geteuid() == 0 }
