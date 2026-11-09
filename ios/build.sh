#!/bin/bash
# SPDX-License-Identifier: GPL-2.0-or-later
#
# 构建清幽加速器 iOS 客户端：Go 连接内核（gomobile）+ SwiftUI 界面 + 隧道扩展。
# 只在 macOS 上运行（需要 Xcode、Go 1.21+、xcodegen、gomobile）。
#
# 用法：
#   ./build.sh                      # 编译内核 → 生成工程 → archive → 导出 ipa
#   ./build.sh --skip-engine        # 复用已有的 Engine.xcframework（只改界面时更快）
#   ./build.sh --verify             # 只验证能否编译（不签名，不需要开发者账号）
#   ./build.sh --build-only         # 只编译到模拟器（真机调试用）
#   QYVPN_VERSION=1.2.0 QYVPN_BUILD=3 ./build.sh
#   QYVPN_API_TOKEN=xxx ./build.sh  # 临时指定客户端令牌
#   QYVPN_TEAM=ABCDE12345 ./build.sh
#
# 产物：
#   build/QYVPN.xcarchive
#   build/export/QYVPN.ipa        （TestFlight / App Store Connect 上传用）

set -euo pipefail

cd "$(dirname "$0")"
IOS_DIR="$(pwd)"
ROOT="$(cd "$IOS_DIR/.." && pwd)"
APP_DIR="$IOS_DIR/app"

# Go 内核位置随仓库形态变化：
#   - 私有单体仓库：macos/engine（与 macOS 客户端共用一套内核）
#   - 只公开 iOS 的导出仓库：core/（发布脚本把必需的那几个包拷出来）
if [ -d "$ROOT/core/mobile" ]; then
  ENGINE_DIR="$ROOT/core"
elif [ -d "$ROOT/macos/engine/mobile" ]; then
  ENGINE_DIR="$ROOT/macos/engine"
else
  echo "找不到 Go 内核：core/ 与 macos/engine/ 都不存在。" >&2
  echo "在私有仓库里请确认 macos/engine 完整；在导出仓库里请重新运行 tools/publish-client-repo.sh。" >&2
  exit 1
fi

OUT="$IOS_DIR/build"
FRAMEWORK_DIR="$APP_DIR/Frameworks"

VERSION="${QYVPN_VERSION:-1.0.0}"
BUILD_NUMBER="${QYVPN_BUILD:-1}"
TEAM="${QYVPN_TEAM:-}"
API_BASE="${QYVPN_API_BASE:-https://vpn.qo8.cn}"
GOMOBILE_TARGET="${QYVPN_GOMOBILE_TARGET:-ios}"

SKIP_ENGINE=0
BUILD_ONLY=0
VERIFY=0

for arg in "$@"; do
  case "$arg" in
    --skip-engine) SKIP_ENGINE=1 ;;
    --build-only) BUILD_ONLY=1 ;;
    --verify) VERIFY=1 ;;
    -h|--help) sed -n '2,25p' "$0"; exit 0 ;;
    *) echo "未知参数：$arg" >&2; exit 2 ;;
  esac
done

echo "==> 检查工具链"
command -v xcodebuild >/dev/null || { echo "缺少 xcodebuild：请安装 Xcode"; exit 1; }
xcodebuild -version | head -2
command -v xcodegen >/dev/null || { echo "缺少 xcodegen：brew install xcodegen"; exit 1; }
if [ "$SKIP_ENGINE" = "0" ]; then
  command -v go >/dev/null || { echo "缺少 go：请安装 Go 1.21+"; exit 1; }
  go version
fi

# ---------------------------------------------------------------- 客户端令牌
TOKEN="${QYVPN_API_TOKEN:-}"
if [ -z "$TOKEN" ] && [ -f "$ROOT/.private/client.properties" ]; then
  TOKEN="$(grep -E '^apiToken=' "$ROOT/.private/client.properties" | head -1 | cut -d= -f2- | tr -d '[:space:]')"
fi
if [ -z "$TOKEN" ] || [ "$TOKEN" = "REPLACE_WITH_PRIVATE_READ_ONLY_CLIENT_TOKEN" ]; then
  echo "警告：没有找到客户端访问令牌，构建出的客户端将无法同步节点。"
  echo "      请把 apiToken=... 写入 .private/client.properties，或用 QYVPN_API_TOKEN 指定。"
fi

# 令牌作为构建设置传进去，由 project.yml 展开到 Info.plist 的 QYVPNClientToken。
# 不落任何一个 .swift 文件，源码里永远看不到它。
SETTING_TOKEN="QYVPN_CLIENT_TOKEN=$TOKEN"

echo "==> 版本 $VERSION（build $BUILD_NUMBER）"

# ------------------------------------------------------------------ Go 内核
if [ "$SKIP_ENGINE" = "0" ]; then
  if ! command -v gomobile >/dev/null; then
    if [ -x "$(go env GOPATH)/bin/gomobile" ]; then
      export PATH="$(go env GOPATH)/bin:$PATH"
    else
      echo "==> 安装 gomobile（首次构建需要，约一分钟）"
      go install golang.org/x/mobile/cmd/gomobile@latest
      export PATH="$(go env GOPATH)/bin:$PATH"
    fi
  fi
  # gomobile 需要知道 Xcode 路径；未初始化时 bind 会自己初始化。
  gomobile init >/dev/null 2>&1 || true

  LDFLAGS="-s -w"
  LDFLAGS="$LDFLAGS -X qingyou/qyvpn/internal/cloud.Version=$VERSION"
  LDFLAGS="$LDFLAGS -X qingyou/qyvpn/mobile.Version=$VERSION"
  LDFLAGS="$LDFLAGS -X qingyou/qyvpn/internal/cloud.DefaultBase=$API_BASE"
  if [ -n "$TOKEN" ]; then
    LDFLAGS="$LDFLAGS -X qingyou/qyvpn/internal/cloud.DefaultToken=$TOKEN"
  fi

  mkdir -p "$FRAMEWORK_DIR"
  echo "==> 编译 Go 内核（target=$GOMOBILE_TARGET）"
  ( cd "$ENGINE_DIR" && \
    gomobile bind -target="$GOMOBILE_TARGET" -ldflags "$LDFLAGS" \
      -o "$FRAMEWORK_DIR/Engine.xcframework" ./mobile )
  echo "    框架：$FRAMEWORK_DIR/Engine.xcframework"
else
  [ -d "$FRAMEWORK_DIR/Engine.xcframework" ] || {
    echo "缺少 Engine.xcframework，请先不带 --skip-engine 构建一次"; exit 1; }
  echo "==> 复用已有内核：$FRAMEWORK_DIR/Engine.xcframework"
fi

# --------------------------------------------------------------------- 工程
echo "==> 生成 Xcode 工程"
( cd "$APP_DIR" && xcodegen generate --quiet )

if [ "$VERIFY" = "1" ]; then
  # 只验证代码能不能编译：不做签名、不碰证书，因此不需要开发者账号。
  # CI 在还没配好签名时用它做「红灯」检查，本地换 Mac 后第一次也建议先跑这个。
  echo "==> 编译验证（不签名，device 架构）"
  ( cd "$APP_DIR" && xcodebuild -project QYVPN.xcodeproj -scheme QYVPN \
      -configuration Debug -destination "generic/platform=iOS" \
      CODE_SIGNING_ALLOWED=NO CODE_SIGNING_REQUIRED=NO CODE_SIGN_IDENTITY="" \
      MARKETING_VERSION="$VERSION" CURRENT_PROJECT_VERSION="$BUILD_NUMBER" \
      "$SETTING_TOKEN" build )
  echo "验证通过：代码与 Go 框架链接正常（产出的 .app 未签名，不能安装）"
  exit 0
fi

if [ "$BUILD_ONLY" = "1" ]; then
  echo "==> 编译（不打包）"
  ( cd "$APP_DIR" && xcodebuild -project QYVPN.xcodeproj -scheme QYVPN \
      -configuration Debug -sdk iphonesimulator build \
      MARKETING_VERSION="$VERSION" CURRENT_PROJECT_VERSION="$BUILD_NUMBER" \
      "$SETTING_TOKEN" ${TEAM:+DEVELOPMENT_TEAM="$TEAM"} )
  echo "完成：用 Xcode 打开 $APP_DIR/QYVPN.xcodeproj 即可在模拟器/真机运行"
  exit 0
fi

# ------------------------------------------------------------------- archive
if [ -z "$TEAM" ]; then
  echo "警告：没有设置 QYVPN_TEAM，自动签名可能失败。"
  echo "      请用 QYVPN_TEAM=<你的 Team ID> ./build.sh 指定（钥匙串里的开发者证书对应团队）。"
fi

mkdir -p "$OUT"

# App Store Connect API Key：CI 上没法用 Apple ID 交互登录，改用 .p8 密钥让
# xcodebuild 自己去后台创建/更新描述文件（三个环境变量都给了才启用）。
# 注意：密钥路径不能带空格。
AUTH_FLAGS=""
if [ -n "${QYVPN_API_KEY_PATH:-}" ] && [ -n "${QYVPN_API_KEY_ID:-}" ] && [ -n "${QYVPN_API_ISSUER:-}" ]; then
  AUTH_FLAGS="-allowProvisioningUpdates -authenticationKeyPath $QYVPN_API_KEY_PATH -authenticationKeyID $QYVPN_API_KEY_ID -authenticationKeyIssuerID $QYVPN_API_ISSUER"
  echo "==> 使用 App Store Connect API Key 自动管理描述文件（key $QYVPN_API_KEY_ID）"
fi

echo "==> 归档"
( cd "$APP_DIR" && xcodebuild -project QYVPN.xcodeproj -scheme QYVPN \
    -configuration Release -destination "generic/platform=iOS" \
    -archivePath "$OUT/QYVPN.xcarchive" archive \
    MARKETING_VERSION="$VERSION" CURRENT_PROJECT_VERSION="$BUILD_NUMBER" \
    "$SETTING_TOKEN" ${TEAM:+DEVELOPMENT_TEAM="$TEAM"} $AUTH_FLAGS )

# ------------------------------------------------------------------- 导出
PLIST="$OUT/ExportOptions.plist"
cat > "$PLIST" <<PLISTXML
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>method</key><string>app-store-connect</string>
  <key>destination</key><string>export</string>
  <key>signingStyle</key><string>automatic</string>
  <key>uploadSymbols</key><true/>
  ${TEAM:+<key>teamID</key><string>$TEAM</string>}
</dict>
</plist>
PLISTXML

echo "==> 导出 ipa"
rm -rf "$OUT/export"
xcodebuild -exportArchive -archivePath "$OUT/QYVPN.xcarchive" \
  -exportPath "$OUT/export" -exportOptionsPlist "$PLIST" $AUTH_FLAGS

IPA="$(find "$OUT/export" -name '*.ipa' | head -1)"
if [ -n "$IPA" ]; then
  echo
  echo "构建完成："
  echo "  $IPA"
  ( cd "$(dirname "$IPA")" && shasum -a 256 "$(basename "$IPA")" )
  echo
  echo "下一步：用 Xcode → Open Developer Tool → Transporter 上传这个 ipa，"
  echo "       或在 App Store Connect 里交给 TestFlight 分发给测试员。"
else
  echo "没有找到导出的 ipa，请检查签名与 Team ID" >&2
  exit 1
fi
