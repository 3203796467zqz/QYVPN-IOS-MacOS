# 清幽加速器 · iOS 客户端

iPhone / iPad 客户端源码：SwiftUI 界面 + NetworkExtension 隧道扩展 + Go 编写的
连接内核。通道是 **SSTP（TCP 443）**。

> **本仓库只有 iOS 客户端。** 不含服务端、不含 Android / Windows / macOS 客户端，
> 也不含任何节点配置、账号或访问凭据。编译出来的 App 需要填入你自己的客户端
> 令牌与账号才能工作，见 [BUILDING.md](BUILDING.md)。

## 两个目录

| 目录 | 内容 |
| --- | --- |
| `ios/` | Xcode 工程（由 XcodeGen 生成）：主 App、隧道扩展、entitlements、构建脚本与文档 |
| `core/` | Go 连接内核：TLS + SSTP + PPP + MSCHAPv2 + IPCP，以及后台接口客户端 |

`core/` 不是可选的——iOS 系统不支持 SSTP，协议栈必须自己实现；它被 `gomobile bind`
编成 `Engine.xcframework` 链接进隧道扩展。`core/mobile` 是这层桥接：
把 IP 包从 Go 的内存队列交给 `NEPacketTunnelFlow`。

## 它是怎么工作的

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
  └─ 双向搬包：packetFlow.readPackets ↔ Engine.WritePacket / Delegate.OnPacket
```

## 快速开始

```bash
brew install xcodegen
cd ios
QYVPN_API_TOKEN=你的令牌 ./build.sh --verify   # 先只验证能不能编过（不需要开发者账号）
```

要真正出包（需要 Apple 开发者账号）：

```bash
QYVPN_TEAM=你的TeamID ./build.sh
```

详见 [BUILDING.md](BUILDING.md)；发布到 TestFlight 的完整步骤见 `ios/发布流程.md`。

## 云端构建

`.github/workflows/ios-build.yml` 在 GitHub 的 macOS 机器上出包：

- 没配签名 secrets → 只做编译验证（不签名、不需要开发者账号）
- 配齐签名 → 产出 `.ipa`，可选直接上传 TestFlight

## 许可

GPL-2.0-or-later，见 [COPYING](COPYING)。

## 不提供什么

- 不提供节点、账号、订阅或任何形式的服务
- 不提供安装包（请从你信任的渠道获取，或自行编译）
- 不接受「帮我配一个能用的服务器」这类请求
