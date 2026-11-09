// SPDX-License-Identifier: GPL-2.0-or-later
// AppModel.swift：界面状态与业务流程。
//
// 职责划分：
//   AppModel       —— 账号、节点列表、选中节点、提示条（业务状态）
//   TunnelController —— 系统隧道与内核状态（连接状态）
// 界面只读这两个对象，不直接碰网络或 NetworkExtension。

import Foundation
import SwiftUI
import UIKit
import Combine

enum SessionState: Equatable {
    case loading
    case login
    case ready
}

struct Banner: Identifiable, Equatable {
    enum Kind { case info, success, error }
    let id = UUID()
    var text: String
    var kind: Kind
}

@MainActor
final class AppModel: ObservableObject {
    @Published private(set) var session: SessionState = .loading
    @Published private(set) var account: Account?
    @Published private(set) var plan: PlanBrief?
    @Published private(set) var nodes: [NodeView] = []
    @Published private(set) var selectedNodeID: String = ""
    @Published private(set) var probes: [String: Int] = [:]
    @Published private(set) var probing = false
    @Published var banner: Banner?
    @Published private(set) var working = false
    @Published private(set) var updateRelease: IOSRelease?

    @Published var tunnel = TunnelController()

    private let api = APIClient.shared
    private var refreshTask: Task<Void, Never>?
    private var lastRefresh = Date.distantPast

    var selectedNode: NodeView? {
        nodes.first { $0.id == selectedNodeID } ?? nodes.first
    }

    /// 后台构建号高于本机时提示更新（走 TestFlight）。
    var updateAvailable: Bool {
        updateRelease?.isNewerThanInstalled ?? false
    }

    var currentSpeed: (rx: Int64, tx: Int64) {
        (tunnel.engine.rxRate ?? 0, tunnel.engine.txRate ?? 0)
    }

    // MARK: - 启动

    func bootstrap() async {
        if let token = Keychain.load() {
            api.userToken = token
            await refresh(quiet: true)
        }
        if api.userToken == nil || session != .ready {
            session = .login
        }
        await checkRelease()
    }

    // MARK: - 登录 / 退出

    func login(username: String, password: String) async {
        guard !working else { return }
        working = true
        defer { working = false }
        do {
            let reply = try await api.login(username: username, password: password)
            Keychain.save(reply.token)
            account = reply.user
            await refresh(quiet: true)
            if session != .ready {
                session = .ready
            }
            show("登录成功", .success)
        } catch let error as APIError {
            show(error.message, .error)
        } catch {
            show("登录失败，请稍后再试", .error)
        }
    }

    func logout() async {
        tunnel.disconnect()
        await api.logout()
        Keychain.delete()
        account = nil
        plan = nil
        nodes = []
        selectedNodeID = ""
        probes = [:]
        session = .login
    }

    // MARK: - 数据同步

    /// 拉一次节点与账号信息。quiet=true 时失败不弹提示（用于启动、下拉刷新）。
    func refresh(quiet: Bool = false) async {
        guard api.userToken != nil else {
            session = .login
            return
        }
        do {
            let cfg = try await api.config()
            if cfg.needLogin == true {
                // 后台不认这个令牌：清掉本地登录态，回登录页。
                Keychain.delete()
                api.userToken = nil
                session = .login
                if !quiet { show("登录已失效，请重新登录", .error) }
                return
            }
            account = cfg.user ?? account
            plan = cfg.plan ?? plan
            let list = cfg.nodes ?? []
            nodes = list
            if let saved = SharedConfig.sharedDefaults?.string(forKey: SharedConfig.settingsKeySelectedNode),
               list.contains(where: { $0.id == saved }) {
                selectedNodeID = saved
            } else if !list.contains(where: { $0.id == selectedNodeID }) {
                selectedNodeID = list.first?.id ?? ""
            }
            session = .ready
            lastRefresh = Date()

            // 节点在线人数与用量在 /status 里，失败不影响列表本身。
            if let status = try? await api.status() {
                merge(status: status.nodes ?? [])
            }
        } catch let error as APIError {
            if error.needLogin {
                Keychain.delete()
                api.userToken = nil
                session = .login
            }
            if !quiet { show(error.message, .error) }
        } catch {
            if !quiet { show("同步节点失败，请检查网络", .error) }
        }
    }

    private func merge(status: [NodeStatus]) {
        var byID: [String: NodeStatus] = [:]
        for row in status { byID[row.id] = row }
        nodes = nodes.map { node in
            var copy = node
            if let row = byID[node.id] {
                copy.usage = row.usage
                copy.maxUsers = row.maxUsers ?? copy.maxUsers
                copy.quotaGB = row.quotaGB ?? copy.quotaGB
                copy.quotaUsedGB = row.quotaUsedGB ?? copy.quotaUsedGB
            }
            return copy
        }
    }

    // MARK: - 节点

    func select(node: NodeView) {
        guard node.id != selectedNodeID else { return }
        selectedNodeID = node.id
        SharedConfig.sharedDefaults?.set(node.id, forKey: SharedConfig.settingsKeySelectedNode)
        show("已切换到 \(node.name)", .info)
    }

    func probeAll() async {
        guard !probing, !nodes.isEmpty else { return }
        probing = true
        defer { probing = false }
        // 显式取出客户端引用：TaskGroup 的闭包不能隐式捕获 self（MainActor 隔离）。
        let client = api
        let targets = nodes.map { ($0.id, $0.host, $0.port) }
        var result: [String: Int] = [:]
        await withTaskGroup(of: (String, Int?).self) { group in
            for target in targets {
                group.addTask {
                    (target.0, await client.probe(host: target.1, port: target.2))
                }
            }
            for await (id, ms) in group {
                if let ms { result[id] = ms }
            }
        }
        probes = result
    }

    // MARK: - 连接

    func toggle() async {
        if tunnel.isActive {
            tunnel.disconnect()
            return
        }
        guard let node = selectedNode else {
            show("当前账号没有可用节点，请联系管理员", .error)
            return
        }
        guard !node.exhausted else {
            show("该节点本月流量已用完", .error)
            return
        }
        guard let token = api.userToken else {
            session = .login
            return
        }
        do {
            try await tunnel.connect(nodeId: node.id, token: token)
        } catch let error as TunnelError {
            show(error.errorDescription ?? "连接失败", .error)
        } catch {
            show("连接失败：\(error.localizedDescription)", .error)
        }
    }

    // MARK: - iOS 内测更新

    /// 后台只存版本号与 TestFlight 邀请链接：iOS 没装 App 时根本没有下载入口，
    /// 版本提示只能把用户带到 TestFlight。
    private func checkRelease() async {
        do {
            updateRelease = try await api.iosRelease()
        } catch {
            // 后台没开通通道（404）或网络异常都静默，不影响主流程。
        }
    }

    func openTestFlight() {
        guard let url = updateRelease?.testflightURL else { return }
        UIApplication.shared.open(url)
    }

    // MARK: - 提示

    func show(_ text: String, _ kind: Banner.Kind) {
        banner = Banner(text: text, kind: kind)
        Task { @MainActor in
            try? await Task.sleep(nanoseconds: 3_000_000_000)
            if banner?.text == text { banner = nil }
        }
    }
}
