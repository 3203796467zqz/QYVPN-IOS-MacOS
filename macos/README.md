# 清幽私人云 macOS 客户端

给苹果电脑（Apple Silicon / Intel）使用的原生客户端，功能对齐 Windows 版 1.4.x：
节点列表与测速、连接/断开、实时上下行速率、本月流量额度、客户端统计上报、
断线自动重连、菜单栏常驻、域名直连、连接日志、检查更新。

界面是原生 SwiftUI，连接内核用 Go 编写。**之所以自带协议栈**：服务端 1.6.44 起只保留
SSTP（TCP 443 / SoftEther）一条通道，而 macOS 系统本身不支持 SSTP，
所以客户端必须自己实现 TLS + SSTP + PPP（含认证与 IPCP）并接管 `utun` 与路由。

```
清幽私人云.app
 ├── Contents/MacOS/QingyouPrivateCloud   SwiftUI 界面（普通用户运行）
 └── Contents/Resources/qyvpn-engine      Go 连接内核（安装到特权目录，root 运行）
        └── /Library/PrivilegedHelperTools/cn.qo8.qyvpn.engine
```

界面与内核通过 `/var/run/cn.qo8.qyvpn.engine.sock` 通信，每个请求都带本地令牌
（安装时生成的 32 字节随机值，界面与内核各存一份，权限 0600）。

---

## 一、构建（在有 Xcode 命令行工具的 Mac 上）

```bash
# 1. 准备令牌（发布包必需，否则无法同步节点）
mkdir -p .private
printf 'apiToken=你的客户端令牌\n' > .private/client.properties

# 2. 构建
cd macos
./build.sh                 # 通用二进制（arm64 + x86_64），需要本机装 Go
./build.sh --prebuilt      # 用 dist/engine 里已编译好的通用内核，Mac 上不需要 Go
./build.sh --arm64         # 只构建 Apple Silicon，约快一倍
./build.sh --no-zip        # 只产出 .app
```

产物（都在 `macos/dist/`）：

| 文件 | 说明 |
| --- | --- |
| `清幽私人云.app` | 应用包，可直接拖进「应用程序」 |
| `清幽私人云_macOS_v1.0.0.zip` | 发布包（ditto 打包，保留符号链接与权限） |
| `SHA256SUMS.txt` | 内核、界面可执行文件、zip 的校验值 |
| `发布说明_v1.0.0.txt` | 随包给用户看的安装与注意事项 |
| `engine/qyvpn-engine` | 通用内核（arm64 + x86_64） |

要求：macOS 13+、Xcode 命令行工具（`xcode-select --install`）；用 `--prebuilt` 时不需要 Go，
从源码构建内核时需要 Go 1.21+。不需要 Xcode 图形界面，也不需要 Apple 开发者账号（见「签名与分发」）。

`dist/engine/` 里已经放了预先编译好的内核（通用二进制 + 两个单架构 + 校验值）。
用 `--prebuilt` 出包时注意：**版本号与客户端令牌是编译内核时写死的**，
要改版本或换令牌必须重新编译内核（在装了 Go 的机器上跑不带 `--prebuilt` 的 `./build.sh`）。

应用图标直接沿用 App 的图标：`tools/make-icon.py` 读取
`app/src/main/res/drawable-nodpi/qingyou_logo.png`（Android 端 `android:icon` 用的就是它），
只做 macOS 的形状适配（圆角 + 透明边距）后打包成 `app/Resources/AppIcon.icns`；
窗口内的品牌标识也读同一个 icns，不再另画一套。换图标只改那张 PNG 再跑一次脚本即可。
`tools/make-fat.py` 是在没有 Mac 的机器上合并通用二进制用的。

### 手上暂时没有 Mac

`tools/make-bundle.py` 会把**源码 + 预编译内核**打成一份可以直接拷走的包（Windows/Linux 上也能生成）：

```bash
cd macos
python3 tools/make-bundle.py --version 1.0.0
# 产物：dist/清幽私人云_macOS_v1.0.0_构建包.zip
```

拿到 Mac 上解开后，**双击 `出包.command`** 即可（等价于 `cd macos && ./build.sh --prebuilt`），
成功后会自动打开 `dist` 目录。这条路径只依赖 Xcode 命令行工具，不需要 Go，也不需要联网。

想改版本号：`QYVPN_VERSION=1.0.1 QYVPN_BUILD=2 ./build.sh`。
想换后台域名：改 `engine/internal/cloud/cloud.go` 的 `DefaultBase` 一处即可，客户端界面不提供地址输入。

## 二、安装与首次运行

1. 把 `清幽私人云.app` 拖到「应用程序」。
2. 第一次打开：在图标上**右键 → 打开**（客户端没有 Apple 公证签名，直接双击会被 Gatekeeper 拦下）。
3. 首次进首页会提示「需要安装网络组件」，点「安装」，输入一次管理员密码。
   - 这一步把内核复制到 `/Library/PrivilegedHelperTools/`，并注册 launchd 服务
     `cn.qo8.qyvpn.engine`（`KeepAlive` 只在异常退出时重启，正常退出不会反复拉起）。
4. 到「节点列表」选一台节点，回首页点大按钮连接。

卸载：设置页「网络组件 → 卸载」，或 `sudo /Library/PrivilegedHelperTools/cn.qo8.qyvpn.engine uninstall`。

## 三、使用

- **账号（必需）**：后台 1.9 起节点按「用户套餐」下发，未登录时接口只返回 `needLogin=true`
  与空列表。首次打开首页会直接给出登录/注册表单；「我的账号」页可以看会员到期时间、
  输入 16 位卡密续期、退出登录。
  - 注册是开放的，但**注册后仍需要在后台给账号分配套餐**，否则登录了也是 0 个节点
    （节点页会明确提示「当前套餐没有可用节点」，不会让人误以为软件坏了）。
  - 登录态保存在 `/Library/Application Support/QingyouPrivateCloud/user.token`（0600），
    内核重启或系统重启后自动恢复，不用每天重登。
- **连接**：首页大圆按钮。未连接点一下开始连接；连接中再点一下取消；已连接点一下断开。
- **节点列表**：进入自动测速，绿 ≤250 ms、黄 ≤400 ms、红为更慢或失败；60 秒内不重复测。
  测速方式见下方「延迟是怎么测的」。
  卡片显示本月已用量、负载与不可用原因（停用、流量用完）。
- **本月流量**：按北京时间每月 1 日 0 点清零；断线后保留当月累计（本地账本 + 后台统计），
  统计未刷新时会写明「待更新」，不会把旧数字当新数据。
- **域名直连**（设置页）：启用后，国内网段与自定义域名走本地宽带，不经过隧道、不计节点流量；
  自定义域名在连接时解析成 IPv4 并安装 `/32` 直连路由，最多 200 条。**下次连接生效。**
- **自动重连**：意外断开自动重试（5/10/20/40/60 秒，最多 5 次）；账号认证失败、
  服务器拒绝、节点下线、流量用完不重试，直接提示原因。
- **菜单栏**：关窗口不会退出，点菜单栏盾牌图标可以连接/断开或打开主窗口。

### 延迟是怎么测的

只做一次 **TCP 三次握手**，不建 TLS、不发 HTTP——握手耗时本身就是链路往返时间。三个细节决定数字可不可信：

1. **DNS 单独缓存（10 分钟）**。域名解析往往比握手慢一个数量级（实测一个失效域名要 696 ms），
   把它算进延迟就会出现「亚洲节点 800 ms」这种假数字。节点同步完成后会后台预热解析。
2. **多 IP 并发取最快**。一个域名常解析出多个地址，其中个别不可达；逐个串行试会把「等超时」算进延迟，
   所以对最多 4 个候选地址并发握手，取最小值。
3. **慢时多测两次取最小**。SYN 丢一个包时 TCP 会等 1 秒重传，单次采样必然显示 1000 ms+，
   而真实 RTT 可能只有 80 ms。第一次就 ≤150 ms 的节点不再复测，整体不会变慢。

已知局限：**连接状态下测其他节点**，流量会走隧道（只有当前节点的服务器 IP 走物理网卡），
数字反映的是「经隧道到另一台服务器」，会比直连偏高。

## 四、协议与实现要点

| 阶段 | 实现 |
| --- | --- |
| 传输 | TLS 1.2/1.3，按节点域名校验证书（当前全部节点使用 Let's Encrypt 公网证书） |
| 引导 | `SSTP_DUPLEX_POST /sra_{BA195980-...}/` + `SSTPCORRELATIONID`，要求 `HTTP/1.x 200` |
| 建链 | `SSTP_MSG_CALL_CONNECT_REQUEST` → `CALL_CONNECT_ACK`（取 nonce 与哈希位图） |
| PPP | LCP（MRU/认证协商）→ CHAP-MSCHAPv2 / EAP-MSCHAPv2 / PAP → `CALL_CONNECTED`（含 CMAC 绑定）→ IPCP |
| 数据 | SSTP DATA 帧（`0x1000` + PPP HDLC 头 + 协议号），只走 IPv4 |
| 隧道 | 内核 `utun`（`PF_SYSTEM` + `com.apple.net.utun_control`），`ifconfig` 配地址，`0.0.0.0/1` + `128.0.0.0/1` 接管默认路由，服务器地址单独保留物理网关主机路由 |
| DNS | `scutil` 写入 `State:/Network/Service/cn.qo8.qyvpn/DNS`，断开时移除并刷新缓存 |
| 统计 | 每秒读接口计数算速率，按「节点 + 月」累计到本地账本，每 15 秒上报后台 |

MSCHAPv2 的 MD4 / DES / 认证响应与 HLAK 派生用 RFC 1320、RFC 2759 的官方测试向量做了单元测试
（`internal/auth/mschapv2_test.go`）。当前线上节点协商的是 PAP，CHAP/EAP 两条路径保留以兼容后台变更。

## 五、故障排查

```bash
# 内核是否在跑
sudo launchctl print system/cn.qo8.qyvpn.engine | head -20

# 看状态 / 节点 / 手动连接（命令行调试，需 root 才能读令牌）
sudo /Library/PrivilegedHelperTools/cn.qo8.qyvpn.engine status
sudo /Library/PrivilegedHelperTools/cn.qo8.qyvpn.engine nodes
sudo /Library/PrivilegedHelperTools/cn.qo8.qyvpn.engine connect <节点ID>
sudo /Library/PrivilegedHelperTools/cn.qo8.qyvpn.engine disconnect

# 只跑一次握手、不建隧道（验证账号与线路）
sudo /Library/PrivilegedHelperTools/cn.qo8.qyvpn.engine selftest

# 日志
tail -f "/Library/Application Support/QingyouPrivateCloud/connection.log"
```

- 连接后网页打不开：先看 `ifconfig utunN` 是否有地址、`netstat -rn | head` 是否有 `0/1`、`128/1` 两条路由。
- 装了别的 VPN 客户端时，先断开它们再连本客户端（路由会互相覆盖）。
- 关掉浏览器/系统的加密 DNS（DoH），否则域名解析不经过隧道 DNS。

## 六、签名与分发

构建脚本使用 **ad-hoc 签名**（`codesign --sign -`），所以：

- 只能右键打开，且每次重新构建后都要重新允许；
- 没有 Apple 公证，别人拿到 zip 后同样需要右键打开；
- 若以后申请了 Apple 开发者账号，把 `build.sh` 里的签名改成
  `codesign --force --deep --options runtime --sign "Developer ID Application: ..."` 并加 `notarytool` 流程即可。

客户端**不使用** NetworkExtension（不需要向 Apple 申请网络扩展权限），
隧道由特权组件用系统 `utun` 建立。

## 七、已知限制

1. **utun、路由与 DNS 尚未在真机验证**：内核的协议部分已对线上节点跑通握手与 ICMP 往返，
   但 `utun` 创建、路由与 DNS 只能在 Mac 上实测。首次运行请按第五节逐项确认，日志已记录每一步命令结果。
2. **在线更新**：后台 1.6.49 起提供 macOS 发布通道（「macOS 客户端 → 发布 macOS 版本」上传 zip），
   客户端「关于 → 软件更新」会显示新版本并下载到本地；因为 macOS 不能原地替换运行中的 .app，
   下载完成后需要在 Finder 里手动拖进「应用程序」覆盖旧版本。
3. **域名直连按网段与解析结果实现**：国内直连用内置网段表（7456 条，`engine/internal/agent/assets/china-cidrs.txt`），
   自定义域名在连接时解析成 IPv4 后加 `/32` 路由；对使用 CDN、IP 经常变化的站点覆盖不完整，
   这是与 Windows 版（域名级 DNS 分流）的实现差异。
4. **只支持 IPv4 隧道**（与后台当前节点一致）；纯 IPv6 网络下无法连接。
5. 客户端不做应用级分流（macOS 上没有对应的系统接口，需要 NetworkExtension 权限）。

## 八、目录结构

```
macos/
├── build.sh                     一键构建（内核 + 界面 + 打包 + 校验值 + 发布说明）
├── README.md                    本文件
├── BUILDING.txt                 构建与验证记录（开发者用）
├── tools/                       图标生成、通用二进制合并、构建包打包
├── dist/                        产物目录（已忽略入库；engine/ 内是预编译内核）
├── engine/                      Go 连接内核
│   ├── cmd/qyvpn-engine/        入口：run / install / uninstall / status / selftest …
│   └── internal/
│       ├── agent/               状态机、统计账本、上报、日志、更新、国内网段
│       ├── auth/                MD4、DES、MSCHAPv2（RFC 2759）
│       ├── cloud/               后台接口客户端
│       ├── ipc/                 unix socket 协议与本地令牌
│       ├── model/               节点/用量/格式化
│       ├── paths/               本机路径约定
│       ├── ppp/                 PPP 帧与选项编解码
│       ├── session/             SSTP + PPP 会话状态机
│       ├── sstp/                SSTP 控制包与 CMAC
│       └── tunnel/              utun、路由、DNS、接口计数
└── app/                         SwiftUI 界面
    ├── Package.swift
    └── Sources/                 App / 首页 / 节点 / 设置 / 关于 / 组件 / IPC 客户端
```
