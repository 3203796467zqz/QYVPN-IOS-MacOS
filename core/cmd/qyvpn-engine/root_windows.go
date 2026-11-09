// SPDX-License-Identifier: GPL-2.0-or-later
//go:build windows

package main

// isRoot 在 Windows 上只用于开发期编译，直接返回 true。
func isRoot() bool { return true }
