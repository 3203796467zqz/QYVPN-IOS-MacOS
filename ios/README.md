# 清幽加速器 iOS 客户端

给 iPhone / iPad 使用的原生客户端。界面是 SwiftUI，隧道扩展里跑 Go 编写的
SSTP 内核（与 Android / Windows / macOS 客户端同一套后台、同一套协议）。

```
ios/
├── build.sh                 构建脚本：gomobile bind → xcodegen → archive → ipa
├── app/
│   ├── project.yml          XcodeGen 工程定义（两个 target）
│   ├── Sources/
│   │   ├── App/             主 App：登录、节点列表、状态、设置（纯 Swift，不链接 Go）
│   │   ├── Tunnel/          隧道扩展：Go 内核 + NEPacketTunnelFlow 搬包
│   │   └── Shared/          两个 target 共用：常量、状态模型、构建期常量
│   ├── Resources/
│   │   └── Assets.xcassets/ App 图标（1024、无透明通道，符合 App Store 要求）
│   └── Frameworks/          Engine.xcframework（构建产物，不提交）
├── build/                   归档与 ipa（构建产物，不提交）
└── 发布流程.md              从开发者账号到 TestFlight 的完整步骤（含被拒排查）
```

## 与 macOS / Windows 版的结构差异

| | macOS | iOS |
| --- | --- | --- |
| 连通方式 | 特权 helper 进程开 utun + 改路由 | NetworkExtension 隧道扩展接管全部网络 |
| 内核位置 | 独立可执行文件，走 unix socket | 编成 xcframework，编译进扩展进程 |
| 隧道实现 | `internal/tunnel`（utun 设备） | `mobile/packet.go`（内存队列 + packetFlow） |
| 取证授权 | 安装时输入一次管理员密码 | 首次连接时系统弹窗「想要添加 VPN 配置」 |
| 分发 | 自己的下载页发 ZIP | TestFlight / App Store（不能侧载） |

内核复用情况：`sstp` / `ppp` / `auth` / `session` / `cloud` / `model` 全部直接复用，
只重写了「IP 包从哪里进出」这一层（约 200 行）。这层桥接位于 Go 内核的 `mobile` 包
（私有仓库里是 `macos/engine/mobile`，只公开 iOS 的导出仓库里是 `core/mobile`）。

## 一、准备工作（Apple 开发者后台，一次性）

1. **加入 Apple Developer Program**（个人 $99/年，公司 $99/年 + D-U-N-S 编号）。
   VPN 类 App 上架审核对「组织账号」更友好，个人账号被拒的概率明显更高。
2. 创建两个 **App ID**：
   - `cn.qo8.qyvpn.client`（主 App）
   - `cn.qo8.qyvpn.client.tunnel`（扩展，必须在主 App 的 Bundle ID 下）
3. 两个 App ID 都勾选 **App Groups**，创建 `group.cn.qo8.qyvpn` 并都绑定。
4. 只给扩展那个 App ID 勾选 **Network Extensions**，并在 Capabilities 里勾
   **Packet Tunnel Provider**。
5. 如果要在 App Store 上架，还需要：隐私政策 URL、支持 URL、公司信息。
   **中国大陆区上架 VPN 类 App 需要主管部门批复文件，通常走不通**，
   实际分发建议先走 TestFlight，海外区再评估。

## 二、构建

在 Mac 上（需要 Xcode 15+、Go 1.26+）：

```bash
brew install xcodegen
cd ios
QYVPN_TEAM=你的TeamID ./build.sh
```

脚本会依次做：读 `.private/client.properties` 里的客户端令牌 → `gomobile bind` 编出
`Engine.xcframework` → `xcodegen generate` → `xcodebuild archive`（令牌以构建设置
`QYVPN_CLIENT_TOKEN=...` 传入，展开进 Info.plist）→ 导出 `build/export/QYVPN.ipa`。

只改界面时加 `--skip-engine` 跳过 Go 编译；只想在模拟器跑用 `--build-only`；
**没有 Mac 或者只想确认代码能不能编过**，用 `--verify`（不签名、不需要开发者账号）。

> 完全没有 Mac 也能出包：`.github/workflows/ios-build.yml` 会在 GitHub 的 macOS
> 机器上完成编译、签名与上传，配置方法见 **[CI.md](CI.md)**。

> 模拟器注意：默认 `-target=ios` 只编真机架构（arm64）。要在模拟器里跑，
> 用 `QYVPN_GOMOBILE_TARGET=ios,iossimulator ./build.sh`（需要较新的 gomobile）。

## 三、TestFlight 分发

完整的发布清单（账号 → App ID → 建 App → 出包 → 上传 → TestFlight → 回填后台 →
被拒排查）见 **[发布流程.md](发布流程.md)**，这里只列要点：

1. App Store Connect 里新建 App（Bundle ID 选 `cn.qo8.qyvpn.client`）。
2. 用 **Transporter** 上传 `build/export/QYVPN.ipa`，或在 Xcode Organizer 里直接上传。
3. 上传后等处理完成（约 10–30 分钟），在 TestFlight 里：
   - **内部测试**：最多 100 人，不需要审核，加人即可用；
   - **外部测试**：最多 10000 人，需要过一次 Beta App Review（比正式审核宽松，
     但仍会检查 VPN 用途说明）。
4. 外部测试要生成 **公开链接**（`https://testflight.apple.com/join/xxxx`），
   把这条链接填进后台的 iOS 发布通道（管理端 `POST /api/admin/ios-release`），
   下载页就会出现「加入 TestFlight 内测」按钮，App 内也会提示更新。
5. 每个构建 **90 天过期**，到期前必须上传新构建，否则测试员打不开。

## 四、首次连接的流程

1. 用户输入账号密码登录（账号由后台管理员开通，客户端不提供注册）。
2. 点首页的大按钮 → App 把节点 ID 与用户令牌写进 `providerConfiguration`，
   调用 `saveToPreferences`，系统弹出「清幽加速器 想要添加 VPN 配置」→ 用户允许。
3. 系统启动 `QYVPNTunnel` 扩展；扩展创建 Go 内核，注入令牌，自己再拉一次节点列表，
   拿到这台节点的 SSTP 账号口令后开始连接。
4. 内核完成 TLS + SSTP + PPP + MSCHAPv2 + IPCP 后回调 `OnTunnelReady`，
   扩展据此配置系统隧道（地址、DNS、MTU、0.0.0.0/0 路由），
   并把「节点服务器 IP」和「后台域名」加入排除路由，避免自己的连接被绕进隧道。
5. 之后扩展双向搬包，同时每秒把内核状态写进 App Group 的 `status.json`，
   主 App 轮询它刷新速率、时长、隧道地址。

## 五、已知限制

- **国内直连（分流）在 iOS 上还没做**。桌面版靠给 utun 加几千条国内网段路由实现，
  iOS 的隧道扩展要复刻这套得走 `excludedRoutes`（数量大，扩展内存吃紧）
  或自己实现用户态分流。目前 iOS 是全局代理。
- 扩展常驻内存上限约 50 MB，超过会被系统直接杀掉。Go 侧队列按 64/128 个包控制，
  不要在扩展里加缓存大对象的逻辑。
- 隧道建立前会有几百毫秒的「配置中」阶段，此时下行包在扩展里排队（最多 64 个）。
- 不支持 iPad 分屏（`UIRequiresFullScreen`），避免分屏下扩展被频繁挂起。

## 六、常见问题

**`saveToPreferences` 报 "operation not permitted"**
扩展的 App ID 没开 Network Extensions，或描述文件里没有 `packet-tunnel-provider`。
到开发者后台补上后重新下载描述文件（Xcode 会自动）。

**扩展启动后立刻退出，日志里是 "App Group 未配置"**
两个 target 的 App Group 没绑到同一个 `group.cn.qo8.qyvpn`；
检查 `Resources/*.entitlements` 与开发者后台是否一致。

**Swift 报某个 `MobileXxx` 类型不是协议 / 方法签名不匹配**
gomobile 生成的类型形状会随版本变化（本项目踩过一次：Go 接口被生成成类而不是协议）。
当前设计已经避开了这个问题——内核不再用回调，扩展只调 `readPacket()` /
`tunnelInfoJSON()` / `statusJSON()` 三个普通方法。若仍报错，打开
`app/Frameworks/Engine.xcframework/ios-arm64/Engine.framework/Headers/Engine.h`
按实际签名调整即可。

**连上后所有网站都打不开**
看扩展日志（Xcode → Window → Devices and Simulators → 选中设备 → Open Console，
过滤 `cn.qo8.qyvpn.client.tunnel`）。常见原因是节点服务器 IP 没进排除路由，
导致 SSTP 自己的 TCP 连接被路由进隧道形成环路。
