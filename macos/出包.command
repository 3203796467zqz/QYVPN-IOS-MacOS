#!/bin/bash
# SPDX-License-Identifier: GPL-2.0-or-later
#
# 双击运行：在 macOS 上一键出包（使用 dist/engine 里的预编译内核，不需要 Go）。
# 如果提示「无法打开，因为无法验证开发者」，右键 → 打开，或在终端执行：
#   chmod +x 出包.command && ./出包.command
#
cd "$(dirname "$0")" || exit 1

clear
echo "=============================================="
echo "  清幽私人云 · macOS 客户端出包"
echo "=============================================="
echo

if [ "$(uname -s)" != "Darwin" ]; then
  echo "这个脚本要在 macOS 上运行。"
  read -n 1 -s -r -p "按任意键关闭窗口"
  exit 1
fi

if ! xcode-select -p >/dev/null 2>&1; then
  echo "还没有安装 Xcode 命令行工具，正在尝试安装（会弹出系统对话框）…"
  xcode-select --install 2>/dev/null
  echo
  echo "装完后重新双击本文件即可。"
  read -n 1 -s -r -p "按任意键关闭窗口"
  exit 1
fi

if [ -f dist/engine/qyvpn-engine ]; then
  echo "使用已预编译的内核（通用二进制）。"
  ./build.sh --prebuilt
else
  echo "没有找到预编译内核，改为从源码构建（需要已安装 Go）。"
  ./build.sh
fi

status=$?
echo
if [ $status -eq 0 ]; then
  echo "出包成功，正在打开产物目录：macos/dist"
  open dist 2>/dev/null
else
  echo "出包失败，请把上面的报错发给我。"
fi
read -n 1 -s -r -p "按任意键关闭窗口"
