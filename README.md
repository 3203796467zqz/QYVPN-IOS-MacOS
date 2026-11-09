# 清幽加速器 · 客户端（iOS / macOS）

两端客户端源码：SwiftUI 界面 + Go 编写的连接内核。通道是 **SSTP（TCP 443）**。

> **本仓库只有 iOS 与 macOS 客户端。** 不含服务端、不含 Android / Windows 客户端，
> 也不含任何节点配置、账号或访问凭据。编译出来的 App 需要填入你自己的客户端
> 令牌与账号才能工作，见 [BUILDING.md](BUILDING.md)。

## 三个目录

| 目录 | 内容 |
| --- | --- |
| `ios/` | iPhone/iPad：Xcode 工程（XcodeGen 生成），主 App + NetworkExtension 隧道扩展 |
| `macos/` | Mac：SwiftUI 界面 + Go 特权组件（`utun` 隧道、路由与 DNS）+ 打包脚本 |
| `core/` | Go 连接内核：TLS + SSTP + PPP + MSCHAPv2 + IPCP，以及后台接口客户端 |

`core/` 不是可选的——**macOS 与 iOS 系统本身都不支持 SSTP**，协议栈必须自己实现：

- iOS 把它 `gomobile bind` 编成 `Engine.xcframework` 链接进隧道扩展，
  `core/mobile` 是桥接层，把 IP 包从 Go 的内存队列交给 `NEPacketTunnelFlow`；
- macOS 把它编译成特权组件可执行文件（`cmd/qyvpn-engine`），由界面通过
  unix socket 驱动，自己开 utun、配路由。

## 它是怎么工作的

**iOS**

```
主 App（SwiftUI）
  ├─ 登录、拉节点、显示状态（URLSession + Keychain）
  └─ NETunnelProviderManager 启动隧道，把「节点 ID + 用户令牌」传给扩展
           ↓
隧道扩展 QYVPNTunnel（NEPacketTunnelProvider）
  ├─ 创建 core.Engine，注入令牌，自己再拉一次节点
  ├─ core 连 SSTP：TLS → SSTP → LCP → MSCHAPv2 → IPCP
  ├─ IPCP 完成后配置 NEPacketTunnelNetworkSettings（地址/DNS/MTU/0.0.0.0/0），
  │  并把「节点服务器 IP」「后台域名」加入排除路由，避免自己绕进隧道
  └─ 双向搬包：packetFlow.readPackets ↔ Engine.WritePacket / Engine.ReadPacket
```

**macOS**

```
清幽私人云.app（SwiftUI 界面）
  └─ 通过 unix socket 下发指令（每个请求带本地令牌）
           ↓
特权组件 qyvpn-engine（root，launchd 常驻）
  ├─ 接 core 的 SSTP 会话，自己开 utun、配地址/路由/DNS
  ├─ 服务器地址保留物理网关主机路由，默认路由拆成 0.0.0.0/1 + 128.0.0.0/1
  └─ 断线重连、流量统计、统计上报
```

两者共用同一份 `core/`：协议实现完全一致，差别只在「IP 包从哪里进出」。

## 快速开始

**iOS**（需要 Apple 开发者账号才能出可安装的包）

```bash
brew install xcodegen
cd ios
QYVPN_API_TOKEN=你的令牌 ./build.sh --verify   # 先只验证能不能编过（不需要开发者账号）
QYVPN_TEAM=你的TeamID ./build.sh               # 正式出 ipa
```

**macOS**（不需要开发者账号，ad-hoc 签名即可）

```bash
QYVPN_API_TOKEN=你的令牌 ./macos/build.sh
# 只编 Apple Silicon 更快：./macos/build.sh --arm64
```

详见 [BUILDING.md](BUILDING.md)；iOS 发布到 TestFlight 的完整步骤见 `ios/发布流程.md`。

## 云端构建

两条工作流都在 GitHub 的 macOS 机器上跑，不需要本地 Mac：

- `.github/workflows/ios-build.yml`：没配签名 secrets 时只做编译验证；
  配齐证书与 App Store Connect API Key 后产出 `.ipa`，可选直接上传 TestFlight
- `.github/workflows/macos-build.yml`：产出 macOS `.app` 与 `.zip`（ad-hoc 签名）

## 许可

GPL-2.0-or-later，见 [COPYING](COPYING)。

## 不提供什么

- 不提供节点、账号、订阅或任何形式的服务
- 不提供安装包（请从你信任的渠道获取，或自行编译）
- 不接受「帮我配一个能用的服务器」这类请求
