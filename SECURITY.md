# 安全策略

## 报告问题

如果你在本项目中发现问题（例如协议实现缺陷、证书校验绕过、内存越界、
iOS 隧道扩展的权限边界问题），请**不要**开公开 Issue，改用：

- 邮件：security@qo8.cn
- 或在 GitHub 上用 Security → Report a vulnerability 私密提交

请附上：受影响的版本、复现步骤、影响面判断。我们会在 7 天内确认收到，
修复发布后致谢（如果你希望署名）。

## 范围

属于安全问题的：

- SSTP / PPP / MSCHAPv2 协议实现中的缺陷
- TLS 与证书校验（未校验主机名、允许降级、CA 固定逻辑错误）
- iOS 隧道扩展的权限边界、App Group 数据暴露
- macOS 特权组件（`qyvpn-engine`）的权限边界、socket 认证、launchd 安装过程
- 本地凭据存储（Keychain、文件权限、状态目录权限）问题
- 会让流量发往错误目的地的路由/排除路由/DNS 缺陷（含 macOS 的 utun 路由与 iOS 的 excludedRoutes）

不属于安全问题的：

- 需要物理接触设备或已越狱的攻击场景
- App Store / TestFlight 分发与审核问题
- macOS 未签名/未公证导致 Gatekeeper 拦截（这是预期行为，不是漏洞）
- macOS 未签名/未公证导致 Gatekeeper 拦截（这是预期行为，不是漏洞）
- 你自己签发的令牌或账号被滥用
- 服务端部署问题（本仓库不含服务端）

## 关于凭据

本仓库**不含**任何可用的客户端令牌、账号、节点配置或证书私钥。
`apiToken` 由使用者在本地通过 `.private/client.properties` 或环境变量提供，
已被 `.gitignore` 排除。

如果发现仓库里意外出现了真实凭据，请立刻按上面的邮箱告知，我们会作废并轮换。
