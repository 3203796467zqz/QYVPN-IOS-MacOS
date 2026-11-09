# 用「服务器上的 macOS」出 iOS 包

结论：**编译、签名、打包、上传，全部可以在云端的 macOS 机器上完成，不需要买 Mac。**
但 Apple 开发者账号（¥688/年）是身份，不能替代。

## 一、哪些能替代，哪些不能

| 环节 | 服务器能替代吗 | 说明 |
| --- | --- | --- |
| 写代码 | 能 | Windows 上照常写，iOS 目录只是文本 |
| 编译 / gomobile bind | 能 | 需要 macOS + Xcode，GitHub 的 runner 自带 |
| 代码签名 | 能 | 把发布证书 `.p12` 导入临时钥匙串 |
| 描述文件 | 能 | 用 App Store Connect API Key 让 xcodebuild 自动拉取 |
| 上传 App Store Connect | 能 | `xcrun altool` + API Key，无需 Apple ID 登录 |
| **开发者账号** | **不能** | ¥688/年，是身份不是机器 |
| **Beta 审核 / 上架审核** | **不能** | Apple 人工审，跟机器无关 |

## 二、三种「服务器镜像」方案

| 方案 | 适合 | 成本 | 坑 |
| --- | --- | --- | --- |
| **GitHub Actions（macos runner）** | 已在用 GitHub 的团队 | 公开仓库免费；私有仓库按 10 倍分钟计费 | 代码要能上 GitHub |
| **租云 Mac**（MacStadium、AWS `mac2.metal`、Scaleway 等） | 代码不能出内网 | 按小时/按月，AWS 最短 24 小时起 | 要自己配 SSH、Xcode、证书 |
| **本地黑苹果 / VMware 装 macOS** | 不推荐 | 看似免费 | **违反 Apple 许可协议**，Xcode 版本难对齐，签名玄学问题多 |

> 阿里云 / 腾讯云 / 华为云**没有** macOS 实例（Apple 不允许在普通虚拟化上跑 macOS），
> 所以国内「买个云服务器装 Xcode」这条路不存在。

**你们仓库里已经有 `.github/workflows/macos-build.yml`**，macOS 客户端就是跑在
GitHub 的 macOS 机器上的。iOS 只是多了一步签名，已经写好
`.github/workflows/ios-build.yml`，走同一条路。

## 三、GitHub Actions 怎么用

### 1. 先跑一次「不签名的编译验证」

什么都不用配，直接 Actions → 「iOS 客户端构建」→ Run workflow。
没有签名 secrets 时它会自动只做 `./build.sh --verify`：
不签名、不碰证书、不需要开发者账号，**只验证 Swift + Go 框架能不能编过**。

这一步的意义：在花钱买账号之前，先把代码层面的问题全部暴露出来。

### 2. 准备签名材料（买完账号后）

**a. 发布证书 `.p12`**
在 Mac 上（或让有 Mac 的同事帮忙，一次性）：
钥匙串访问 → 找到 `Apple Distribution: xxx` → 右键导出 → 设密码 → 得到 `.p12`。
（也可以用开发者后台生成 CSR 再签发，但导出 p12 最省事。）

```bash
base64 -i cert.p12 | pbcopy        # macOS
base64 cert.p12 > cert.b64          # Linux
```

**b. App Store Connect API Key**
App Store Connect → 用户和访问 → 集成 → App Store Connect API → 生成密钥，
角色选 **App Manager**（不要给 Admin，权限过大）。下载到的 `.p8` **只能下载一次**。

```bash
base64 -i AuthKey_XXXXXXXXXX.p8 > key.b64
```

**c. 团队 ID**
开发者后台 → Membership → Team ID（10 位，形如 `ABCDE12345`）。

### 3. 填 6 个 Secrets

仓库 Settings → Secrets and variables → Actions：

| Secret | 内容 |
| --- | --- |
| `QYVPN_TEAM_ID` | 团队 ID |
| `QYVPN_BUILD_CERT_P12` | 证书 p12 的 base64 |
| `QYVPN_CERT_PASSWORD` | p12 导出密码 |
| `APPSTORE_KEY_ID` | API Key 的 Key ID |
| `APPSTORE_ISSUER_ID` | API Key 的 Issuer ID |
| `APPSTORE_KEY_P8` | p8 文件的 base64 |
| `QYVPN_API_TOKEN` | 后台客户端令牌（不配则产物不能发布） |

再建一个仓库变量 `QYVPN_VERSION`（Settings → Variables），填 `1.0.0` 这类版本号。
build 号用 `github.run_number` 自动递增，永远比上次大。

### 4. 出包并上传

Run workflow 时把 `upload` 勾上 → 构建完自动 `altool` 上传，
10~30 分钟后 App Store Connect 的 TestFlight 里就能看到构建。

不勾 `upload` 就只产出 `QYVPN-iOS-<版本>-<build>` 这个 Artifact（zip 里的 ipa），
手动用 Transporter 上传也行。

## 四、费用大概什么量级

- **公开仓库**：macOS runner 免费，随便跑。
- **私有仓库**：免费额度 2000 分钟/月，但 macOS runner **按 10 倍扣**，
  也就是实际只有约 200 分钟 ≈ **3~4 次完整构建/月**。
  一次完整构建（gomobile + archive）大约 15~25 分钟，比本地 Mac 慢。
- 超了就买分钟数，或改成本地/自建 runner（一台二手 Mac mini 两三千，长期更划算）。

省时间的技巧：只改界面时用 `--skip-engine` 复用已编好的 xcframework（需要缓存）。

## 五、几个一定会踩的坑

| 现象 | 原因 | 处理 |
| --- | --- | --- |
| `User interaction is not allowed` | 钥匙串没解锁/没授权 codesign | 工作流里已用 `set-key-partition-list` 处理 |
| `No signing certificate "iOS Distribution" found` | 证书密码错或 p12 里没有私钥 | 导出时确认包含私钥 |
| `Provisioning profile doesn't match` | Team ID 填错，或 App ID 没开 Network Extension | 回开发者后台核对两个 App ID 的能力 |
| `ITMS-90032` 缺图标 | 图标缺失或带 alpha | 工程已内置合规图标 |
| 上传报 `build number already used` | build 号重复 | 用 `run_number`，别手写固定值 |
| 卡在 `正在处理` 很久 | Apple 排队 | 超过 2 小时联系 Apple |
| Go 编译报 `unsupported GOOS` | gomobile 未初始化 | build.sh 会自动 `gomobile init` |

## 六、如果代码不能上 GitHub

租一台云 Mac（按月），当成「出包机」用：

```bash
# 本地 Windows 上
rsync -avz --exclude .git --exclude build ./ 用户@云Mac:~/QYVPN/

# 云 Mac 上（首次）
brew install xcodegen go
go install golang.org/x/mobile/cmd/gomobile@latest
# 把证书和 API Key 放到固定位置，然后
cd ~/QYVPN/ios && ./build.sh
```

出完包把 `build/export/QYVPN.ipa` 拉回本地，用 Transporter（Windows 上没有，
可以用 `xcrun altool` 在云 Mac 上直接传）提交。

这种方式证书长期放在那台机器上，**务必只给自己用、开防火墙、别放共享机器**。
