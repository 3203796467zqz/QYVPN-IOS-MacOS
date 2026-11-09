// SPDX-License-Identifier: GPL-2.0-or-later
// 界面状态：轮询内核、维护节点列表与设置、处理安装与更新。
import Foundation
import SwiftUI
import AppKit

struct Banner: Identifiable, Equatable {
    enum Kind { case info, error, success }
    let id = UUID()
    let text: String
    let kind: Kind
}

@MainActor
final class AppState: ObservableObject {
    @Published private(set) var status = EngineStatus()
    @Published private(set) var nodes: [NodeView] = []
    @Published private(set) var settings = EngineSettings()
    @Published private(set) var probes: [String: ProbeResult] = [:]
    @Published private(set) var installed = HelperInstaller.isInstalled
    @Published private(set) var engineReady = false
    @Published private(set) var logLines: [String] = []

    @Published var banner: Banner?
    @Published var updateInfo: UpdateInfo?
    @Published var working = false
    @Published var showLog = false

    private var client: EngineClient?
    private var token: String = ""
    private var pollTask: Task<Void, Never>?
    private var ticks = 0
    private var bannerTask: Task<Void, Never>?
    private var bootstrapped = false

    var selectedNode: NodeView? {
        nodes.first { $0.id == settings.selectedNode }
    }

    var canConnect: Bool {
        selectedNode != nil && !status.isBusy
    }

    // MARK: - 生命周期

    func bootstrap() {
        // 菜单栏里“打开主窗口”会重建视图，避免重复启动轮询。
        guard !bootstrapped else { return }
        bootstrapped = true
        do {
            token = try TokenStore.loadOrCreate()
            client = EngineClient(token: token)
            installed = HelperInstaller.isInstalled
            try ping()
            engineReady = true
            refreshNodes(quiet: true)
            loadLog()
        } catch {
            engineReady = false
            // 内核没装上不弹错误，界面上给出安装引导即可。
            if installed {
                show("连接内核未响应，请重新安装网络组件", .error)
            }
        }
        startPolling()
    }

    private func ping() throws {
        guard let client else { throw EngineError(message: "内核客户端未初始化") }
        _ = try client.call("ping")
    }

    private func startPolling() {
        pollTask?.cancel()
        pollTask = Task { [weak self] in
            while !Task.isCancelled {
                await self?.pollOnce()
                try? await Task.sleep(nanoseconds: 1_000_000_000)
            }
        }
    }

    private func pollOnce() async {
        guard let client else { return }
        ticks += 1
        do {
            let response = try await blocking { try client.call("status") }
            engineReady = true
            installed = true
            if let st = response.status { status = st }
            // 空闲时每 20 秒同步一次节点，保证在线人数与额度不过期。
            if ticks % 20 == 0 && status.state == "idle" {
                refreshNodes(quiet: true)
            }
        } catch {
            if engineReady {
                engineReady = false
            }
            if ticks % 5 == 0 {
                status = EngineStatus()
            }
        }
    }

    // MARK: - 操作

    func refreshNodes(quiet: Bool = false) {
        guard let client else { return }
        Task {
            do {
                let response = try await blocking { try client.call("refresh") }
                if let list = response.nodes { nodes = list }
                if let s = response.settings { settings = s }
                if !quiet { show("节点已刷新", .success) }
            } catch {
                if !quiet { show(error.localizedDescription, .error) }
            }
        }
    }

    func connect() {
        guard let client, let node = selectedNode else { return }
        if node.block.isEmpty == false {
            show(node.block, .error)
            return
        }
        Task {
            do {
                _ = try await blocking { try client.call("connect", ["nodeId": node.id]) }
            } catch {
                show(error.localizedDescription, .error)
            }
        }
    }

    func disconnect() {
        guard let client else { return }
        Task {
            do {
                _ = try await blocking { try client.call("disconnect") }
            } catch {
                show(error.localizedDescription, .error)
            }
        }
    }

    /// 退出应用时同步断开（本地 socket 调用，最多几百毫秒）。
    func disconnectOnQuit() {
        guard let client, status.isConnected || status.isBusy else { return }
        _ = try? client.call("disconnect")
    }

    func toggle() {
        if status.isConnected || status.isBusy {
            disconnect()
        } else {
            connect()
        }
    }

    func select(node: NodeView) {
        settings.selectedNode = node.id
        saveSettings()
    }

    func probe() {
        guard let client else { return }
        let ids = nodes.map(\.id)
        Task {
            do {
                let response = try await blocking { try client.call("probe", ["nodeIds": ids]) }
                if let result = response.probes { probes = result }
            } catch {
                show(error.localizedDescription, .error)
            }
        }
    }

    /// 供界面在保存前覆盖设置（设置页用草稿编辑）。
    func applySettings(_ value: EngineSettings) {
        settings = value
    }

    func saveSettings() {
        guard let client else { return }
        let snapshot = settings
        Task {
            do {
                let response = try await blocking {
                    try client.call("settings.set", ["settings": snapshot.dictionary])
                }
                if let s = response.settings { settings = s }
            } catch {
                show(error.localizedDescription, .error)
            }
        }
    }

    func installHelper() {
        working = true
        Task {
            defer { working = false }
            do {
                let token = try TokenStore.loadOrCreate()
                self.token = token
                try await blockingVoid { try HelperInstaller.install(token: token) }
                installed = true
                engineReady = false
                // 等 launchd 把内核拉起来。
                for _ in 0..<20 {
                    try? await Task.sleep(nanoseconds: 300_000_000)
                    if (try? await blocking { try EngineClient(token: token).call("ping") }) != nil {
                        client = EngineClient(token: token)
                        engineReady = true
                        break
                    }
                }
                if engineReady {
                    show("网络组件已安装", .success)
                    refreshNodes(quiet: true)
                } else {
                    show("网络组件已安装，但内核尚未响应，请稍后重试", .error)
                }
            } catch {
                show(error.localizedDescription, .error)
            }
        }
    }

    func uninstallHelper() {
        working = true
        Task {
            defer { working = false }
            do {
                try await blockingVoid { try HelperInstaller.uninstall() }
                installed = false
                engineReady = false
                status = EngineStatus()
                nodes = []
                show("网络组件已卸载", .success)
            } catch {
                show(error.localizedDescription, .error)
            }
        }
    }

    func loadLog() {
        guard let client else { return }
        Task {
            if let response = try? await blocking({ try client.call("log", ["lines": 200]) }),
               let lines = response.log {
                logLines = lines.reversed()
            }
        }
    }

    func checkUpdate() {
        guard let client else { return }
        working = true
        Task {
            defer { working = false }
            do {
                let response = try await blocking { try client.call("update.check") }
                updateInfo = response.update
            } catch {
                show(error.localizedDescription, .error)
            }
        }
    }

    func downloadUpdate() {
        guard let client else { return }
        working = true
        Task {
            defer { working = false }
            do {
                let response = try await blocking { try client.call("update.download") }
                if let path = response.path {
                    show("更新包已下载：\(path)", .success)
                    NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: path)])
                }
            } catch {
                show(error.localizedDescription, .error)
            }
        }
    }

    // MARK: - 账号

    var account: UserAccount? { status.account }
    var plan: PlanInfo? { status.plan }
    var needsLogin: Bool { status.needsLogin }

    /// 登录成功后内核会带回新的状态与节点列表，直接套用，省一次往返。
    private func applyAccount(_ response: EngineResponse) {
        if let st = response.status { status = st }
        if let list = response.nodes { nodes = list }
    }

    func login(username: String, password: String) {
        guard let client else { return }
        let name = username.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !name.isEmpty, !password.isEmpty else {
            show("请输入用户名和密码", .error)
            return
        }
        working = true
        Task {
            defer { working = false }
            do {
                let response = try await blocking {
                    try client.call("login", ["username": name, "password": password])
                }
                applyAccount(response)
                if nodes.isEmpty { refreshNodes(quiet: true) }
                show("已登录：\(name)", .success)
            } catch {
                show(error.localizedDescription, .error)
            }
        }
    }

    func register(username: String, password: String, email: String) {
        guard let client else { return }
        let name = username.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !name.isEmpty, !password.isEmpty else {
            show("请输入用户名和密码", .error)
            return
        }
        working = true
        Task {
            defer { working = false }
            do {
                let response = try await blocking {
                    try client.call("register", [
                        "username": name,
                        "password": password,
                        "email": email.trimmingCharacters(in: .whitespacesAndNewlines)
                    ])
                }
                applyAccount(response)
                if nodes.isEmpty { refreshNodes(quiet: true) }
                show("注册成功，已登录", .success)
            } catch {
                show(error.localizedDescription, .error)
            }
        }
    }

    func redeem(code: String) {
        guard let client else { return }
        let value = code.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !value.isEmpty else {
            show("请输入卡密", .error)
            return
        }
        working = true
        Task {
            defer { working = false }
            do {
                let response = try await blocking { try client.call("redeem", ["code": value]) }
                applyAccount(response)
                if nodes.isEmpty { refreshNodes(quiet: true) }
                if let days = response.days, days > 0 {
                    show("兑换成功，已增加 \(days) 天", .success)
                } else {
                    show("兑换成功", .success)
                }
            } catch {
                show(error.localizedDescription, .error)
            }
        }
    }

    func logout() {
        guard let client else { return }
        working = true
        Task {
            defer { working = false }
            do {
                let response = try await blocking { try client.call("logout") }
                nodes = []
                status = response.status ?? EngineStatus()
                show("已退出登录", .success)
            } catch {
                show(error.localizedDescription, .error)
            }
        }
    }

    // MARK: - 提示

    func show(_ text: String, _ kind: Banner.Kind) {
        let banner = Banner(text: text, kind: kind)
        self.banner = banner
        bannerTask?.cancel()
        bannerTask = Task { [weak self] in
            try? await Task.sleep(nanoseconds: 4_000_000_000)
            if self?.banner?.id == banner.id {
                self?.banner = nil
            }
        }
    }

    // MARK: - 线程桥接

    private func blocking<T: Sendable>(_ work: @escaping @Sendable () throws -> T) async throws -> T {
        try await withCheckedThrowingContinuation { continuation in
            DispatchQueue.global(qos: .userInitiated).async {
                do { continuation.resume(returning: try work()) }
                catch { continuation.resume(throwing: error) }
            }
        }
    }

    private func blockingVoid(_ work: @escaping @Sendable () throws -> Void) async throws {
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            DispatchQueue.global(qos: .userInitiated).async {
                do { try work(); continuation.resume() }
                catch { continuation.resume(throwing: error) }
            }
        }
    }
}

extension EngineSettings {
    /// 转成 JSONSerialization 能接受的形式。
    var dictionary: [String: Any] {
        [
            "selectedNode": selectedNode,
            "bypassEnabled": bypassEnabled,
            "bypassChina": bypassChina,
            "bypassDomains": bypassDomains,
            "autoConnect": autoConnect
        ]
    }
}
