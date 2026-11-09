// SPDX-License-Identifier: GPL-2.0-or-later
// TunnelController.swift：管理系统的 VPN 配置与连接状态。
//
// iOS 上「连接」不是一个函数调用，而是三步：
//   1. 把隧道扩展的配置写进系统偏好（NETunnelProviderManager.saveToPreferences），
//      第一次会让用户在系统弹窗里确认；
//   2. startVPNTunnel()，系统启动扩展进程；
//   3. 扩展进程里的 Go 内核真正去连 SSTP 节点。
// 所以这里的 status 是「系统隧道状态」，engine 是「内核状态」，两者要合起来看。

import Foundation
import NetworkExtension
import Combine

@MainActor
final class TunnelController: ObservableObject {
    /// 系统侧隧道状态。
    @Published private(set) var status: NEVPNStatus = .invalid
    /// 内核侧状态（由扩展写 status.json，这里轮询）。
    @Published private(set) var engine: EngineStatus = .idle

    private var manager: NETunnelProviderManager?
    private var statusObserver: NSObjectProtocol?
    private var pollTimer: Timer?

    init() {
        statusObserver = NotificationCenter.default.addObserver(
            forName: .NEVPNStatusDidChange, object: nil, queue: .main
        ) { [weak self] _ in
            Task { @MainActor in
                guard let self else { return }
                self.status = self.manager?.connection.status ?? .invalid
            }
        }
        startPolling()
    }

    deinit {
        if let observer = statusObserver {
            NotificationCenter.default.removeObserver(observer)
        }
        pollTimer?.invalidate()
    }

    /// 系统隧道是否处于「连接中/已连接」。
    var isActive: Bool {
        switch status {
        case .connected, .connecting, .reasserting: return true
        default: return false
        }
    }

    var statusText: String {
        if engine.isConnected { return "已连接" }
        if engine.isBusy { return engine.displayText }
        switch status {
        case .connected: return "已连接"
        case .connecting: return "正在连接节点"
        case .disconnecting: return "正在断开"
        case .reasserting: return "正在重连"
        case .invalid: return "网络组件未就绪"
        case .disconnected: return engine.state == "idle" && (engine.error?.isEmpty == false)
            ? (engine.error ?? "未连接") : "未连接"
        @unknown default: return "未连接"
        }
    }

    // MARK: - 配置

    /// 加载（或首次创建）系统里的隧道配置。必须在连接前调用一次。
    func prepare() async throws {
        let managers = try await loadAll()
        let matched = managers.first { manager in
            (manager.protocolConfiguration as? NETunnelProviderProtocol)?
                .providerBundleIdentifier == SharedConfig.tunnelBundleID
        }
        if let matched {
            manager = matched
            status = matched.connection.status
            return
        }
        let created = NETunnelProviderManager()
        let proto = NETunnelProviderProtocol()
        proto.providerBundleIdentifier = SharedConfig.tunnelBundleID
        proto.serverAddress = "清幽加速器"
        created.protocolConfiguration = proto
        created.localizedDescription = "清幽加速器"
        created.isEnabled = true
        created.isOnDemandEnabled = false
        try await save(created)
        manager = created
        status = created.connection.status
    }

    /// 连接指定节点。nodeId 与用户令牌都通过 providerConfiguration 交给扩展。
    func connect(nodeId: String, token: String) async throws {
        if manager == nil {
            try await prepare()
        }
        guard let manager else {
            throw TunnelError.notPrepared
        }
        guard let proto = manager.protocolConfiguration as? NETunnelProviderProtocol else {
            throw TunnelError.notPrepared
        }
        let bootstrap = TunnelBootstrap(nodeId: nodeId, token: token, apiBase: SharedConfig.apiBase)
        proto.providerConfiguration = try JSONEncoder().encode(bootstrap).jsonObject()
        proto.serverAddress = "清幽加速器 · \(nodeId)"
        // 每次连接都保存一次：节点选择变了，扩展需要拿到新的 nodeId。
        manager.protocolConfiguration = proto
        manager.isEnabled = true
        try await save(manager)

        // 清掉上一次的残留状态，避免界面先闪一下旧数据。
        clearStatusFile()
        engine = .idle
        // 新 SDK 里 startVPNTunnel 是 throwing：配置没保存好、扩展未签名等都会抛错，
        // 这里向上抛给界面显示，而不是静默失败让用户看着「未连接」发呆。
        try manager.connection.startVPNTunnel()
        status = manager.connection.status
    }

    func disconnect() {
        manager?.connection.stopVPNTunnel()
    }

    // MARK: - 系统接口封装

    private func loadAll() async throws -> [NETunnelProviderManager] {
        try await withCheckedThrowingContinuation { continuation in
            NETunnelProviderManager.loadAllFromPreferences { managers, error in
                if let error {
                    continuation.resume(throwing: TunnelError.system(error.localizedDescription))
                    return
                }
                continuation.resume(returning: managers ?? [])
            }
        }
    }

    private func save(_ manager: NETunnelProviderManager) async throws {
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            manager.saveToPreferences { error in
                if let error {
                    continuation.resume(throwing: TunnelError.system(error.localizedDescription))
                    return
                }
                // 保存后必须再读一次，否则 connection 对象还是旧的。
                manager.loadFromPreferences { _ in
                    continuation.resume(returning: ())
                }
            }
        }
    }

    // MARK: - 内核状态轮询

    private func startPolling() {
        pollTimer = Timer.scheduledTimer(withTimeInterval: 0.8, repeats: true) { [weak self] _ in
            Task { @MainActor in self?.readStatusFile() }
        }
    }

    private func readStatusFile() {
        guard let url = SharedConfig.statusFileURL,
              let data = try? Data(contentsOf: url),
              let decoded = try? JSONDecoder().decode(EngineStatus.self, from: data) else {
            return
        }
        engine = decoded
    }

    private func clearStatusFile() {
        guard let url = SharedConfig.statusFileURL else { return }
        try? FileManager.default.removeItem(at: url)
    }
}

enum TunnelError: LocalizedError {
    case notPrepared
    case system(String)

    var errorDescription: String? {
        switch self {
        case .notPrepared:
            return "网络组件尚未配置，请重新打开 App 后重试"
        case .system(let message):
            return "系统拒绝了 VPN 配置：\(message)"
        }
    }
}

private extension Data {
    /// providerConfiguration 只接受 plist 兼容类型，JSON 先转成字典再交给系统。
    func jsonObject() throws -> [String: Any] {
        guard let obj = try JSONSerialization.jsonObject(with: self) as? [String: Any] else {
            throw TunnelError.notPrepared
        }
        return obj
    }
}
