#!/bin/bash
# SPDX-License-Identifier: GPL-2.0-or-later
#
# 构建清幽私人云 macOS 客户端：Go 连接内核 + SwiftUI 界面，产出 .app 与 zip。
# 只在 macOS 上运行（需要 Xcode 命令行工具；用 --prebuilt 时不需要 Go）。
#
# 用法：
#   ./build.sh                  # 构建通用二进制并打包 dist/
#   ./build.sh --arm64          # 只构建 Apple Silicon（更快）
#   ./build.sh --intel          # 只构建 Intel
#   ./build.sh --prebuilt       # 使用 dist/engine 里预编译好的内核（Mac 上不需要 Go）
#   ./build.sh --no-zip         # 只产出 .app
#   QYVPN_VERSION=1.0.1 QYVPN_BUILD=2 ./build.sh
#   QYVPN_API_TOKEN=xxx ./build.sh          # 临时指定客户端令牌
#
set -euo pipefail

cd "$(dirname "$0")"
ROOT="$(pwd)"
VERSION="${QYVPN_VERSION:-1.0.0}"
BUILD_NUMBER="${QYVPN_BUILD:-1}"
APP_NAME="清幽私人云"
BUNDLE_ID="cn.qo8.qyvpn.client"
OUT="$ROOT/dist"
APP="$OUT/$APP_NAME.app"

ARCHS=("arm64" "x86_64")
MAKE_ZIP=1
PREBUILT=0

for arg in "$@"; do
  case "$arg" in
    --arm64) ARCHS=("arm64") ;;
    --intel) ARCHS=("x86_64") ;;
    --prebuilt) PREBUILT=1 ;;
    --no-zip) MAKE_ZIP=0 ;;
    -h|--help) sed -n '2,20p' "$0"; exit 0 ;;
    *) echo "未知参数：$arg" >&2; exit 2 ;;
  esac
done

# 内核源码位置随仓库形态变化：
#   - 完整仓库：macos/engine（Android/Windows/iOS 之外的那套）
#   - 只公开客户端的导出仓库：仓库根的 core/（与 iOS 共用同一份 Go 代码）
if [ -d "$ROOT/engine/cmd/qyvpn-engine" ]; then
  ENGINE_SRC="$ROOT/engine"
elif [ -d "$ROOT/../core/cmd/qyvpn-engine" ]; then
  ENGINE_SRC="$ROOT/../core"
else
  echo "找不到内核源码：macos/engine 与 ../core 都不存在" >&2
  exit 1
fi

echo "==> 检查工具链"
command -v swift >/dev/null || { echo "缺少 swift：请先安装 Xcode 命令行工具（xcode-select --install）"; exit 1; }
swift --version | head -1
if [ "$PREBUILT" = "0" ]; then
  command -v go >/dev/null || { echo "缺少 go：请安装 Go 1.21+，或用 --prebuilt"; exit 1; }
  go version
fi

mkdir -p "$OUT/engine"

UNIVERSAL=0
if [ "$PREBUILT" = "1" ]; then
  echo "==> 使用预编译内核（dist/engine）"
  if [ -f "$OUT/engine/qyvpn-engine" ]; then
    # 已经合好的通用二进制，Mac 上连 lipo 都不需要。
    UNIVERSAL=1
    ENGINE_BINS=("$OUT/engine/qyvpn-engine")
    echo "    使用通用内核：$OUT/engine/qyvpn-engine"
  else
    ENGINE_BINS=()
    for arch in "${ARCHS[@]}"; do
      bin="$OUT/engine/qyvpn-engine-$arch"
      [ -f "$bin" ] || { echo "缺少 ${bin}（请先在能编译 Go 的机器上生成）"; exit 1; }
      ENGINE_BINS+=("$bin")
    done
  fi
else
  # 客户端令牌：优先环境变量，其次仓库里的私有配置。
  TOKEN="${QYVPN_API_TOKEN:-}"
  if [ -z "$TOKEN" ] && [ -f "$ROOT/../.private/client.properties" ]; then
    TOKEN="$(grep -E '^apiToken=' "$ROOT/../.private/client.properties" | head -1 | cut -d= -f2- | tr -d '[:space:]')"
  fi
  if [ -z "$TOKEN" ] || [ "$TOKEN" = "REPLACE_WITH_PRIVATE_READ_ONLY_CLIENT_TOKEN" ]; then
    echo "警告：没有找到客户端访问令牌，构建出的客户端将无法同步节点。"
    echo "      请把 apiToken=... 写入 .private/client.properties，或用 QYVPN_API_TOKEN 指定。"
  fi

  LDFLAGS="-s -w -X qingyou/qyvpn/internal/cloud.Version=$VERSION"
  if [ -n "$TOKEN" ]; then
    LDFLAGS="$LDFLAGS -X qingyou/qyvpn/internal/cloud.DefaultToken=$TOKEN"
  fi

  echo "==> 编译连接内核"
  ENGINE_BINS=()
  for arch in "${ARCHS[@]}"; do
    echo "    darwin/$arch"
    ( cd "$ENGINE_SRC" && \
      CGO_ENABLED=0 GOOS=darwin GOARCH="$arch" \
      go build -trimpath -ldflags "$LDFLAGS" -o "$OUT/engine/qyvpn-engine-$arch" ./cmd/qyvpn-engine )
    ENGINE_BINS+=("$OUT/engine/qyvpn-engine-$arch")
  done
fi

# 通用二进制：多架构用 lipo 合并（Xcode 命令行工具自带）。
if [ "$UNIVERSAL" = "1" ]; then
  echo "    已是通用二进制，跳过 lipo"
elif [ "${#ENGINE_BINS[@]}" -gt 1 ]; then
  if command -v lipo >/dev/null; then
    lipo -create "${ENGINE_BINS[@]}" -output "$OUT/engine/qyvpn-engine"
  else
    echo "缺少 lipo，退回单架构"; cp "${ENGINE_BINS[0]}" "$OUT/engine/qyvpn-engine"
  fi
else
  cp "${ENGINE_BINS[0]}" "$OUT/engine/qyvpn-engine"
fi
chmod 0755 "$OUT/engine/qyvpn-engine"
echo "    内核架构：$(lipo -archs "$OUT/engine/qyvpn-engine" 2>/dev/null || echo 通用二进制)"

echo "==> 编译界面"
# 只有完整版 Xcode 才带 XCBuild.framework。只装了命令行工具（xcode-select 装的）的机器上，
# 一旦给 swift build 传 --arch（编通用二进制），就会报：
#   error: xcbuild executable at .../XCBuild.framework/.../xcbuild does not exist
# 所以这里先试目标架构，失败且是缺 XCBuild，就退回本机架构，保证一定能出包。
NATIVE_ARCH="$(uname -m)"          # arm64 或 x86_64
ARCH_FLAGS=()

# 判断装的是完整 Xcode 还是只有命令行工具：只有完整 Xcode 带 XCBuild.framework。
XCODE_DEV="$(xcode-select -p 2>/dev/null || true)"
case "$XCODE_DEV" in
  *.app/Contents/Developer) HAS_FULL_XCODE=1 ;;
  *) HAS_FULL_XCODE=0 ;;
esac

if [ "${#ARCHS[@]}" -gt 1 ] && [ "$HAS_FULL_XCODE" = "0" ]; then
  echo "    这台 Mac 只有 Xcode 命令行工具（$XCODE_DEV），缺 XCBuild，编不出通用二进制。"
  echo "    改为只编本机架构（$NATIVE_ARCH）；想同时支持 Apple Silicon 与 Intel，"
  echo "    需要安装完整版 Xcode（App Store，约 10 GB）。"
  ARCHS=("$NATIVE_ARCH")
fi

if [ "${#ARCHS[@]}" -eq 1 ] && [ "${ARCHS[0]}" = "$NATIVE_ARCH" ]; then
  echo "    目标架构与本机一致（$NATIVE_ARCH），不指定 --arch，直接本地编译"
else
  for arch in "${ARCHS[@]}"; do ARCH_FLAGS+=(--arch "$arch"); done
  echo "    目标架构：${ARCHS[*]}"
fi

UI_LOG="$OUT/swift-build.log"
set +e
( cd "$ROOT/app" && swift build -c release "${ARCH_FLAGS[@]}" ) 2>&1 | tee "$UI_LOG"
UI_RC=${PIPESTATUS[0]}
set -e

if [ "$UI_RC" -ne 0 ] && [ "${#ARCH_FLAGS[@]}" -gt 0 ] && grep -q "xcbuild" "$UI_LOG" 2>/dev/null; then
  echo
  echo "    这台 Mac 只有 Xcode 命令行工具，缺 XCBuild，编不出通用二进制。"
  echo "    改为只编本机架构（$NATIVE_ARCH），出包继续进行。"
  echo "    想同时支持 Apple Silicon 与 Intel，需要安装完整版 Xcode（App Store，约 10 GB）。"
  ARCHS=("$NATIVE_ARCH")
  ARCH_FLAGS=()
  ( cd "$ROOT/app" && swift build -c release ) 2>&1 | tee "$UI_LOG"
fi

BIN_DIR="$( cd "$ROOT/app" && swift build -c release "${ARCH_FLAGS[@]}" --show-bin-path )"
UI_BIN="$BIN_DIR/QingyouPrivateCloud"
if [ ! -f "$UI_BIN" ]; then
  UI_BIN="$(find "$ROOT/app/.build" -type f -name QingyouPrivateCloud -perm -111 | head -1)"
fi
[ -f "$UI_BIN" ] || { echo "找不到界面可执行文件"; exit 1; }
echo "    界面架构：$(lipo -archs "$UI_BIN" 2>/dev/null || echo 未知)"

echo "==> 组装 .app"
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
cp "$UI_BIN" "$APP/Contents/MacOS/QingyouPrivateCloud"
cp "$OUT/engine/qyvpn-engine" "$APP/Contents/Resources/qyvpn-engine"
chmod 0755 "$APP/Contents/MacOS/QingyouPrivateCloud" "$APP/Contents/Resources/qyvpn-engine"

cat > "$APP/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleDevelopmentRegion</key><string>zh_CN</string>
  <key>CFBundleExecutable</key><string>QingyouPrivateCloud</string>
  <key>CFBundleIdentifier</key><string>$BUNDLE_ID</string>
  <key>CFBundleInfoDictionaryVersion</key><string>6.0</string>
  <key>CFBundleName</key><string>$APP_NAME</string>
  <key>CFBundleDisplayName</key><string>$APP_NAME</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>$VERSION</string>
  <key>CFBundleVersion</key><string>$BUILD_NUMBER</string>
  <key>LSMinimumSystemVersion</key><string>13.0</string>
  <key>NSHighResolutionCapable</key><true/>
  <key>NSHumanReadableCopyright</key><string>清幽私人云 · GPL-2.0-or-later</string>
  <key>LSApplicationCategoryType</key><string>public.app-category.utilities</string>
  <key>NSPrincipalClass</key><string>NSApplication</string>
</dict>
</plist>
PLIST

if [ -f "$ROOT/app/Resources/AppIcon.icns" ]; then
  cp "$ROOT/app/Resources/AppIcon.icns" "$APP/Contents/Resources/AppIcon.icns"
  /usr/libexec/PlistBuddy -c "Add :CFBundleIconFile string AppIcon" "$APP/Contents/Info.plist"
fi

printf 'APPL????' > "$APP/Contents/PkgInfo"

echo "==> 签名（ad-hoc）"
codesign --force --deep --sign - "$APP" >/dev/null
codesign --verify --deep --strict "$APP" && echo "    签名校验通过"

ZIP=""
if [ "$MAKE_ZIP" = "1" ]; then
  ZIP="$OUT/清幽私人云_macOS_v${VERSION}.zip"
  rm -f "$ZIP"
  echo "==> 打包"
  ( cd "$OUT" && ditto -c -k --sequesterRsrc --keepParent "$APP" "$ZIP" )
fi

echo "==> 生成校验与发布说明"
: > "$OUT/SHA256SUMS.txt"
for f in "$OUT/engine/qyvpn-engine" "$APP/Contents/MacOS/QingyouPrivateCloud"; do
  [ -f "$f" ] || continue
  ( cd "$(dirname "$f")" && shasum -a 256 "$(basename "$f")" ) >> "$OUT/SHA256SUMS.txt"
done
if [ -n "$ZIP" ] && [ -f "$ZIP" ]; then
  ( cd "$OUT" && shasum -a 256 "$(basename "$ZIP")" ) >> "$OUT/SHA256SUMS.txt"
fi

cat > "$OUT/发布说明_v${VERSION}.txt" <<NOTES
清幽私人云 macOS 客户端 ${VERSION}（build ${BUILD_NUMBER}）

安装
  1. 解压后把「清幽私人云.app」拖到「应用程序」。
  2. 第一次打开请右键 → 打开（未做 Apple 公证，直接双击会被 Gatekeeper 拦下）。
  3. 首页提示「需要安装网络组件」时点安装，输入一次管理员密码。

说明
  - 支持 Apple Silicon 与 Intel，要求 macOS 13 或更新版本。
  - 通道为 SSTP（TCP 443），与 Android / Windows 客户端使用同一套后台。
  - 卸载：设置页「网络组件 → 卸载」。

校验
  见同目录 SHA256SUMS.txt。

注意事项
  - 客户端内置受限访问令牌（仅配置读取、状态读取、统计上报），不含后台管理权限，
    请勿把安装包当作公开源码包分发。
  - 修改系统路由与 DNS 需要管理员授权；退出客户端会断开当前连接。
NOTES

echo
echo "构建完成："
echo "  $APP"
[ -n "$ZIP" ] && echo "  $ZIP"
echo "  $OUT/SHA256SUMS.txt"
echo "  $OUT/发布说明_v${VERSION}.txt"
echo
cat "$OUT/SHA256SUMS.txt"
echo
echo "首次运行：右键 → 打开（客户端未做 Apple 公证，属于正常提示）。"
