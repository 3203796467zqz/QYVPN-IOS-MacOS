#!/bin/bash
# SPDX-License-Identifier: GPL-2.0-or-later
#
# 双击运行：在 macOS 上一键出包。
#   · 有 dist/engine 里的预编译内核 → 用它（不需要 Go）
#   · 没有预编译内核 → 从源码编（需要 Go 与客户端令牌）
#
# 如果提示「无法打开，因为无法验证开发者」：右键 → 打开。
# 任何一步失败都会生成「出包日志.txt」，把它发回来即可定位问题。
cd "$(dirname "$0")" || exit 1

LOG="$PWD/出包日志.txt"
: > "$LOG" || { echo "无法写入日志文件，请把 macos 目录换个位置再试"; exit 1; }

clear
echo "=============================================="
echo "  清幽私人云 · macOS 客户端出包"
echo "=============================================="
echo

# 先把环境信息写进日志：每次排查最缺的就是这些。
{
  echo "===== 环境 ====="
  echo "时间      : $(date '+%Y-%m-%d %H:%M:%S')"
  echo "系统      : $(sw_vers -productName 2>/dev/null) $(sw_vers -productVersion 2>/dev/null) ($(uname -m))"
  echo "Xcode 路径: $(xcode-select -p 2>/dev/null || echo 未安装)"
  echo "Swift     : $(swift --version 2>/dev/null | head -1 || echo 无)"
  echo "Go        : $(go version 2>/dev/null || echo 无)"
  echo "目录      : $PWD"
  echo "预编译内核: $([ -f dist/engine/qyvpn-engine ] && echo 有 || echo 无)"
  echo "令牌      : $([ -n "${QYVPN_API_TOKEN:-}" ] && echo '来自环境变量' || ([ -f ../.private/client.properties ] && echo '来自 .private/client.properties' || echo 无))"
  echo
} >> "$LOG"

fail() {
  echo
  echo "--------------------------------------------------"
  echo "出包失败。"
  echo "日志已保存到：$LOG"
  echo "请把这个文件发过来（已自动打开，可直接拖进聊天窗口）。"
  echo "--------------------------------------------------"
  open -e "$LOG" 2>/dev/null || open -t "$LOG" 2>/dev/null || true
  echo
  read -n 1 -s -r -p "按任意键关闭窗口"
  exit 1
}

step() { echo "$*"; echo "$*" >> "$LOG"; }

if [ "$(uname -s)" != "Darwin" ]; then
  step "这个脚本要在 macOS 上运行。"
  fail
fi

step "==> 1/3 检查 Xcode 命令行工具"
if ! xcode-select -p >/dev/null 2>&1; then
  step "还没有安装 Xcode 命令行工具，正在弹出安装对话框…"
  xcode-select --install 2>/dev/null || true
  step "请在弹出的窗口里点「安装」，装完（约 10 分钟）再双击一次本文件。"
  fail
fi
step "    $(xcode-select -p)"

step "==> 2/3 检查内核与依赖"
MODE=""
if [ -f dist/engine/qyvpn-engine ]; then
  MODE="--prebuilt"
  step "    使用预编译内核（不需要 Go）"
else
  if ! command -v go >/dev/null 2>&1; then
    step "    没有预编译内核，也没有安装 Go，无法从源码构建。"
    step ""
    step "    两个办法二选一："
    step "      A. 找我要一份「构建包」（里面已带预编译内核），解压后双击即可，不用装 Go。"
    step "      B. 装 Go：https://go.dev/dl/ 下载 macOS 安装包，双击装完再来一次。"
    fail
  fi
  step "    从源码构建，Go: $(go version)"
  if [ -z "${QYVPN_API_TOKEN:-}" ] && [ ! -f ../.private/client.properties ]; then
    step "    缺少客户端令牌：请让发你源码的人给你一串令牌，然后这样运行："
    step ""
    step "      QYVPN_API_TOKEN=那一串令牌 ./出包.command"
    step ""
    step "    （双击运行时拿不到环境变量，需要在「终端」里执行上面这行）"
    fail
  fi
fi

step "==> 3/3 开始构建（首次编译界面要几分钟到十几分钟，别关窗口）"
set +e
./build.sh $MODE 2>&1 | tee -a "$LOG"
status=${PIPESTATUS[0]}
set -e

if [ $status -ne 0 ]; then
  echo >> "$LOG"
  echo "===== 结果：失败（退出码 $status）=====" >> "$LOG"
  fail
fi

ZIP=$(ls -1 dist/*.zip 2>/dev/null | head -1)
{
  echo
  echo "===== 结果：成功 ====="
  ls -la dist 2>/dev/null
  [ -n "$ZIP" ] && shasum -a 256 "$ZIP"
} >> "$LOG" 2>&1

echo
echo "=============================================="
echo "  出包成功"
echo "=============================================="
if [ -n "$ZIP" ]; then
  echo "安装包：$ZIP"
  echo "大小  ：$(du -h "$ZIP" | cut -f1)"
  echo
  echo "把这个 zip 发给对方即可（不要发 .app 文件夹，压缩后传输更稳）。"
else
  echo "没有找到 zip，请检查 dist 目录。"
fi
echo
open dist 2>/dev/null || true
read -n 1 -s -r -p "按任意键关闭窗口"
