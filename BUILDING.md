# 构建说明

iOS 客户端需要一个 **客户端访问令牌**（`apiToken`）才能同步节点。这个令牌由服务端
管理员签发，只具备「读配置 / 读状态 / 上报统计」权限，不含管理权限。本仓库不提供令牌。

准备方式（任选其一）：

```bash
mkdir -p .private
printf 'apiToken=你的令牌\n' > .private/client.properties
```

或者直接给环境变量 `QYVPN_API_TOKEN`。**不要把你自己的令牌提交进仓库**，
`.gitignore` 已经排除了 `.private/`。令牌不会出现在任何源码文件里——
`build.sh` 以构建设置 `QYVPN_CLIENT_TOKEN=...` 传给 xcodebuild，Xcode 展开进 Info.plist。

---

## 一、依赖

| 工具 | 版本 | 用途 |
| --- | --- | --- |
| Xcode | 15 或更新 | 编译 Swift、打包 ipa |
| Go | 1.21+ | 编译 `core/` 内核 |
| gomobile | 最新 | 把 Go 编译成 `Engine.xcframework`（`build.sh` 会自动安装） |
| XcodeGen | 最新 | 由 `ios/app/project.yml` 生成 Xcode 工程 |

```bash
brew install xcodegen
go install golang.org/x/mobile/cmd/gomobile@latest
```

## 二、构建

```bash
cd ios

# 1. 先验证能不能编过：不签名、不需要开发者账号
./build.sh --verify

# 2. 正式出包：需要 Apple 开发者账号与 Team ID
QYVPN_TEAM=ABCDE12345 QYVPN_VERSION=1.0.0 QYVPN_BUILD=1 ./build.sh
```

产物：

- `ios/build/QYVPN.xcarchive`
- `ios/build/export/QYVPN.ipa`

其它参数：

| 参数 / 变量 | 作用 |
| --- | --- |
| `--skip-engine` | 复用已有的 `Engine.xcframework`，只改界面时快很多 |
| `--build-only` | 编到模拟器，不归档 |
| `--verify` | 只编译验证（不签名） |
| `QYVPN_BUILD` | `CFBundleVersion`，**每次上传 App Store Connect 必须比上次大** |
| `QYVPN_API_TOKEN` | 客户端令牌 |
| `QYVPN_API_KEY_PATH/ID/ISSUER` | App Store Connect API Key，CI 上用它自动同步描述文件 |

首次用 Xcode 打开工程的话，先跑一次 `./build.sh --verify` 生成
`Engine.xcframework`，否则会报找不到符号。直接编译也能过，只是没有令牌连不上后台。

## 三、需要准备什么（Apple 侧）

1. Apple Developer Program 账号（个人/公司，$99/年）。
2. 两个 App ID：`cn.qo8.qyvpn.client` 与 `cn.qo8.qyvpn.client.tunnel`。
3. 两个 App ID 都绑定同一个 App Group（默认 `group.cn.qo8.qyvpn`）。
4. **只给扩展**那个 App ID 开启 Network Extensions → Packet Tunnel Provider。

Bundle ID、App Group 若要改，alter 三个地方：`ios/app/project.yml`、
`ios/app/Sources/Shared/SharedConfig.swift`、开发者后台。

## 四、云端构建

没有 Mac 也能出包：把仓库设为 Public 后，Actions 里的
「iOS 客户端构建」会用 GitHub 的 macOS 机器编译。

- 未配置签名 secrets：自动降级为 `./build.sh --verify`，只验证编译
- 配齐 `QYVPN_TEAM_ID` / `QYVPN_BUILD_CERT_P12` / `QYVPN_CERT_PASSWORD` /
  `APPSTORE_KEY_ID` / `APPSTORE_ISSUER_ID` / `APPSTORE_KEY_P8`：正式出 ipa，可上传 TestFlight

> 产出的 ipa 内嵌受限客户端令牌，**只放 Actions Artifacts，不要传到 Releases**。

## 五、常见问题

**提示「客户端未正确打包（缺少访问令牌）」**：构建时没有提供 `QYVPN_API_TOKEN`，
Info.plist 里的 `QYVPNClientToken` 是空的。

**`MobileDelegate` 方法签名不匹配**：gomobile 生成的头文件在
`ios/app/Frameworks/Engine.xcframework/ios-arm64/Engine.framework/Headers/Engine.h`，
按里面的 `@protocol MobileDelegate` 改 `PacketTunnelProvider` 的三个回调签名。

**`saveToPreferences` 失败 / operation not permitted**：扩展的 App ID 没开
Network Extensions，或描述文件缺少 `packet-tunnel-provider`。

**连上后所有网站打不开**：多半是节点服务器 IP 没进排除路由，SSTP 自己的 TCP
连接被路由进隧道形成环路。看扩展日志（Xcode → Devices → Open Console，
过滤 `cn.qo8.qyvpn.client.tunnel`）。

**后台返回 `needLogin`**：令牌或账号失效。客户端需要先登录拿用户令牌，
只带客户端令牌是拿不到节点列表的。
