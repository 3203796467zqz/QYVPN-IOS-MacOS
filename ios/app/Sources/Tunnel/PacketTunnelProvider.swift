// SPDX-License-Identifier: GPL-2.0-or-later
// PacketTunnelProvider.swift：隧道扩展进程。
//
// 这是 iOS 客户端与 macOS 版最大的结构差异：iOS 不允许第三方进程直接开 utun、
// 也不允许常驻后台，所有 VPN 数据必须由系统启动的这个扩展进程接管。
// 扩展里做三件事：
//   1. 创建 Go 内核（gomobile 编出来的 Engine 框架），把用户令牌与节点 ID 交给它；
//   2. 轮询内核的隧道参数，IPCP 完成后按真实地址/DNS/MTU 配置系统隧道，
//      并把节点服务器与后台域名从隧道路由里排除，避免自己的流量绕回隧道；
//   3. 双向搬包：packetFlow.readPackets → 内核；内核 ReadPacket() → packetFlow.writePackets。
//
// 为什么用「轮询 + 阻塞读」而不是回调：gomobile 把 Go 接口映射成 ObjC 类型时形状并不
// 稳定（本项目实测生成的是类而不是协议，Swift 无法实现），跟着生成器改签名太脆弱。
// 现在内核只暴露 ReadPacket / TunnelInfoJSON / StatusJSON 三个普通方法，跨语言面最小。

import NetworkExtension
import os.log
import Engine

final class PacketTunnelProvider: NEPacketTunnelProvider {

    private let log = OSLog(subsystem: "cn.qo8.qyvpn.client.tunnel", category: "tunnel")

    /// 所有与内核、packetFlow 相关的状态都收敛到这条串行队列上。
    private let queue = DispatchQueue(label: "cn.qo8.qyvpn.tunnel.work")

    private var engine: MobileEngine?
    private var tunnelReady = false
    private var stopped = false
    private var statusTimer: DispatchSourceTimer?
    private var readyTimer: DispatchSourceTimer?
    private var excludedCIDRs: [String] = []

    // MARK: - 生命周期

    override func startTunnel(options: [String: NSObject]?,
                              completionHandler: @escaping (Error?) -> Void) {
        guard let bootstrap = loadBootstrap() else {
            completionHandler(TunnelStartError.badConfiguration)
            return
        }
        guard let dir = SharedConfig.engineURL else {
            completionHandler(TunnelStartError.missingAppGroup)
            return
        }
        // 状态目录必须存在，Go 侧只负责创建子文件。
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)

        queue.async { [weak self] in
            guard let self else { return }
            do {
                // 客户端令牌已在编译期注入 Go 二进制（见 ios/build.sh），这里传空。
                // MobileNewEngine 是 gomobile 生成的 C 函数（不是 ObjC 方法），
                // Swift 不会把它桥接成 throws，必须自己传 NSError 指针。
                var newError: NSError?
                guard let engine = MobileNewEngine(dir.path, bootstrap.apiBase, "", &newError) else {
                    throw newError ?? TunnelStartError.badConfiguration
                }
                try engine.setUserToken(bootstrap.token)
                self.engine = engine

                // 扩展进程自己拉一次节点：SSTP 登录口令在这里。
                try engine.refresh()

                // 连接参数里只有节点 ID，服务器地址要从节点列表里取出来再解析成
                // /32 排除路由——否则隧道一起来，SSTP 自己的 TCP 连接就会被路由进去。
                self.excludedCIDRs = Self.excludedRoutes(nodesJSON: engine.nodesJSON() ?? "",
                                                         nodeID: bootstrap.nodeId)
                if self.excludedCIDRs.isEmpty {
                    os_log("没有解析出节点地址，隧道可能因环路无法保持",
                           log: self.log, type: .error)
                }

                try engine.connect(bootstrap.nodeId)
                self.startStatusTimer()
                self.pollTunnelInfo(completionHandler)
            } catch {
                self.writeStatus(raw: self.failureStatus(error.localizedDescription))
                completionHandler(error)
            }
        }
    }

    override func stopTunnel(with reason: NEProviderStopReason,
                             completionHandler: @escaping () -> Void) {
        queue.async { [weak self] in
            guard let self else {
                completionHandler()
                return
            }
            self.stopped = true
            self.statusTimer?.cancel()
            self.statusTimer = nil
            self.readyTimer?.cancel()
            self.readyTimer = nil
            self.engine?.disconnect()
            self.engine?.close()
            self.engine = nil
            self.writeStatus(raw: self.failureStatus("已断开"))
            completionHandler()
        }
    }

    // MARK: - 等内核走到 IPCP

    /// 轮询 TunnelInfoJSON。IPCP 完成后立刻配置隧道；连接失败则提前返回错误，
    /// 否则系统会一直显示「正在连接」直到用户放弃。
    private func pollTunnelInfo(_ completionHandler: @escaping (Error?) -> Void) {
        var attempts = 0
        let timer = DispatchSource.makeTimerSource(queue: queue)
        timer.schedule(deadline: .now() + .milliseconds(200), repeating: .milliseconds(200))
        timer.setEventHandler { [weak self] in
            guard let self, let engine = self.engine else { return }
            attempts += 1

            let infoJSON = engine.tunnelInfoJSON() ?? ""
            if !infoJSON.isEmpty,
               let data = infoJSON.data(using: .utf8),
               let info = try? JSONDecoder().decode(TunnelReadyInfo.self, from: data) {
                timer.cancel()
                self.readyTimer = nil
                self.configureTunnel(info: info) { ok in
                    guard ok else {
                        completionHandler(TunnelStartError.configureFailed)
                        return
                    }
                    self.startPacketPumps()
                    completionHandler(nil)
                }
                return
            }

            // 每 5 秒看一次内核状态：已经回到 idle 且带错误，说明连不上，别再等。
            if attempts % 25 == 0 {
                if let st = (engine.statusJSON() ?? "").data(using: .utf8),
                   let status = try? JSONDecoder().decode(EngineStatus.self, from: st),
                   status.state == "idle",
                   let err = status.error, !err.isEmpty {
                    timer.cancel()
                    self.readyTimer = nil
                    completionHandler(TunnelStartError.inner(err))
                    return
                }
            }
            // 60 秒还没完成 IPCP，按超时处理。
            if attempts > 300 {
                timer.cancel()
                self.readyTimer = nil
                completionHandler(TunnelStartError.timeout)
            }
        }
        timer.resume()
        readyTimer = timer
    }

    // MARK: - 隧道配置

    private func configureTunnel(info: TunnelReadyInfo, completion: @escaping (Bool) -> Void) {
        let remote = excludedCIDRs.first ?? info.ip
        let settings = NEPacketTunnelNetworkSettings(tunnelRemoteAddress: remote)

        let ipv4 = NEIPv4Settings(addresses: [info.ip], subnetMasks: ["255.255.255.0"])
        ipv4.includedRoutes = [NEIPv4Route.default()]
        ipv4.excludedRoutes = buildExcludedRoutes()
        settings.ipv4Settings = ipv4

        if !info.dns.isEmpty {
            let dns = NEDNSSettings(servers: info.dns)
            // 空 matchDomains 表示「所有域名都走这组 DNS」。
            dns.matchDomains = [""]
            settings.dnsSettings = dns
        }
        settings.mtu = NSNumber(value: info.mtu)

        setTunnelNetworkSettings(settings) { [weak self] error in
            guard let self else {
                completion(false)
                return
            }
            if let error {
                os_log("配置隧道失败：%{public}@", log: self.log, type: .error,
                       error.localizedDescription)
                completion(false)
                return
            }
            self.queue.async {
                self.tunnelReady = true
                completion(true)
            }
        }
    }

    /// 服务器 IP 与后台域名必须走物理网络：否则隧道建立后，SSTP 自己的 TCP 连接
    /// 和刷新节点的 HTTPS 请求都会被路由进隧道，形成环路。
    private func buildExcludedRoutes() -> [NEIPv4Route] {
        var routes: [NEIPv4Route] = []
        var seen = Set<String>()

        for cidr in excludedCIDRs {
            guard let (address, mask) = PacketTunnelProvider.splitCIDR(cidr),
                  !seen.contains(cidr) else { continue }
            seen.insert(cidr)
            routes.append(NEIPv4Route(destinationAddress: address, subnetMask: mask))
        }

        // 台账里没解析出服务器地址时，至少把后台域名排除掉，保证还能刷新节点。
        let apiHost = SharedConfig.apiBase.replacingOccurrences(of: "https://", with: "")
        for host in PacketTunnelProvider.resolveIPv4(apiHost) {
            let cidr = "\(host)/32"
            guard !seen.contains(cidr) else { continue }
            seen.insert(cidr)
            routes.append(NEIPv4Route(destinationAddress: host, subnetMask: "255.255.255.255"))
        }
        return routes
    }

    // MARK: - 数据面

    private func startPacketPumps() {
        startReadingFromHost()
        startReadingFromEngine()
    }

    /// 上行：packetFlow → 内核。
    private func startReadingFromHost() {
        packetFlow.readPackets { [weak self] packets, _ in
            guard let self else { return }
            self.queue.async {
                guard let engine = self.engine, self.tunnelReady, !self.stopped else { return }
                for packet in packets {
                    _ = try? engine.writePacket(packet)
                }
                // readPackets 是一次性的，必须每次读完继续挂上下一次。
                self.startReadingFromHost()
            }
        }
    }

    /// 下行：内核 → packetFlow。ReadPacket 是阻塞调用，所以单独占一条线程。
    private func startReadingFromEngine() {
        guard let engine else { return }
        Thread.detachNewThread { [weak self] in
            while true {
                guard let self, !self.stopped else { return }
                guard let packet = engine.readPacket() else {
                    // 隧道关闭时返回 nil，退出循环（不能重试，否则会空转）。
                    return
                }
                let family = PacketTunnelProvider.protocolFamily(packet)
                self.packetFlow.writePackets([packet], withProtocols: [NSNumber(value: family)])
            }
        }
    }

    private static func protocolFamily(_ packet: Data) -> Int32 {
        guard let first = packet.first else { return AF_INET }
        return (first >> 4) == 6 ? AF_INET6 : AF_INET
    }

    // MARK: - 状态文件

    private func startStatusTimer() {
        let timer = DispatchSource.makeTimerSource(queue: queue)
        timer.schedule(deadline: .now() + .milliseconds(500), repeating: .seconds(1))
        timer.setEventHandler { [weak self] in
            guard let self, let engine = self.engine else { return }
            self.writeStatus(raw: engine.statusJSON() ?? "{}")
        }
        timer.resume()
        statusTimer = timer
    }

    private func writeStatus(raw: String) {
        guard let url = SharedConfig.statusFileURL, let data = raw.data(using: .utf8) else { return }
        // 原子写：主 App 每秒读一次，不能让它读到半个 JSON。
        try? data.write(to: url, options: .atomic)
    }

    private func failureStatus(_ message: String) -> String {
        let escaped = message
            .replacingOccurrences(of: "\\", with: "\\\\")
            .replacingOccurrences(of: "\"", with: "\\\"")
            .replacingOccurrences(of: "\n", with: " ")
        return "{\"state\":\"idle\",\"stage\":\"未连接\",\"error\":\"\(escaped)\"}"
    }

    // MARK: - 启动参数

    private func loadBootstrap() -> TunnelBootstrap? {
        guard let proto = protocolConfiguration as? NETunnelProviderProtocol,
              let dict = proto.providerConfiguration,
              let data = try? JSONSerialization.data(withJSONObject: dict) else {
            return nil
        }
        return try? JSONDecoder().decode(TunnelBootstrap.self, from: data)
    }
}

// MARK: - 工具

enum TunnelStartError: LocalizedError {
    case badConfiguration
    case missingAppGroup
    case configureFailed
    case timeout
    case inner(String)

    var errorDescription: String? {
        switch self {
        case .badConfiguration:
            return "启动参数缺失，请重新打开 App 后重试"
        case .missingAppGroup:
            return "App Group 未配置，扩展无法读写状态目录"
        case .configureFailed:
            return "系统拒绝配置隧道路由"
        case .timeout:
            return "连接节点超时，请检查网络后重试"
        case .inner(let message):
            return message
        }
    }
}

extension PacketTunnelProvider {
    /// "1.2.3.4/24" → ("1.2.3.0", "255.255.255.0")。只处理 IPv4，失败返回 nil。
    static func splitCIDR(_ cidr: String) -> (String, String)? {
        let parts = cidr.split(separator: "/")
        guard parts.count == 2, let bits = Int(parts[1]), bits >= 0, bits <= 32 else { return nil }
        let address = String(parts[0])
        var octets = address.split(separator: ".").compactMap { UInt8(String($0)) }
        guard octets.count == 4 else { return nil }
        let mask: UInt32 = bits == 0 ? 0 : ~UInt32(0) << (32 - UInt32(bits))
        let value = octets.reduce(UInt32(0)) { ($0 << 8) | UInt32($1) }
        let network = value & mask
        octets = [
            UInt8((network >> 24) & 0xff), UInt8((network >> 16) & 0xff),
            UInt8((network >> 8) & 0xff), UInt8(network & 0xff),
        ]
        let maskText = [
            UInt8((mask >> 24) & 0xff), UInt8((mask >> 16) & 0xff),
            UInt8((mask >> 8) & 0xff), UInt8(mask & 0xff),
        ].map(String.init).joined(separator: ".")
        return (octets.map(String.init).joined(separator: "."), maskText)
    }

    /// 解析域名的 IPv4 地址（只用于生成排除路由）。
    static func resolveIPv4(_ host: String) -> [String] {
        let trimmed = host.trimmingCharacters(in: .whitespaces)
        guard !trimmed.isEmpty else { return [] }
        if isIPv4(trimmed) { return [trimmed] }
        var hints = addrinfo(ai_flags: 0, ai_family: AF_INET, ai_socktype: SOCK_STREAM,
                             ai_protocol: 0, ai_addrlen: 0, ai_canonname: nil,
                             ai_addr: nil, ai_next: nil)
        var info: UnsafeMutablePointer<addrinfo>?
        guard getaddrinfo(trimmed, nil, &hints, &info) == 0, info != nil else { return [] }
        defer { freeaddrinfo(info) }
        var out: [String] = []
        var cursor = info
        while let node = cursor {
            var buffer = [CChar](repeating: 0, count: Int(INET_ADDRSTRLEN))
            var addr = node.pointee.ai_addr.withMemoryRebound(to: sockaddr_in.self, capacity: 1) { $0.pointee }
            if inet_ntop(AF_INET, &addr.sin_addr, &buffer, socklen_t(INET_ADDRSTRLEN)) != nil {
                out.append(String(cString: buffer))
            }
            cursor = node.pointee.ai_next
        }
        return out
    }

    static func isIPv4(_ text: String) -> Bool {
        var addr = in_addr()
        return inet_pton(AF_INET, text, &addr) == 1
    }

    /// 从内核下发的节点列表里找出本次要连的节点，把它的服务器地址转成 /32 路由。
    static func excludedRoutes(nodesJSON: String, nodeID: String) -> [String] {
        guard let data = nodesJSON.data(using: .utf8),
              let rows = try? JSONSerialization.jsonObject(with: data) as? [[String: Any]] else {
            return []
        }
        var routes: [String] = []
        for row in rows {
            guard let id = row["id"] as? String, id == nodeID,
                  let host = row["host"] as? String else { continue }
            for ip in resolveIPv4(host) {
                routes.append("\(ip)/32")
            }
        }
        return routes
    }
}
