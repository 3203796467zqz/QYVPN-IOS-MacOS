// SPDX-License-Identifier: GPL-2.0-or-later
// SharedModels.swift：App 与扩展之间传递的数据结构。
//
// 扩展不直接向 App 发消息（那需要 provider message 往返、还得处理 App 不在前台
// 的情况），而是把内核状态原样写进 App Group 里的 status.json，App 定时读取。
// 两边的字段定义都放在这里，避免各写一份导致对不上。

import Foundation

/// 启动隧道时通过 providerConfiguration 传给扩展的参数。
struct TunnelBootstrap: Codable {
    var nodeId: String
    var token: String
    var apiBase: String
}

/// 内核在 IPCP 完成后回调的隧道参数（对应 Go 侧 OnTunnelReady 的 JSON）。
struct TunnelReadyInfo: Codable {
    var ip: String
    var dns: [String]
    var mtu: Int
}

/// 连接状态快照，字段与 Go 内核 agent.Status 的 JSON 一一对应。
/// 全部用可选类型：内核在不同阶段会省略部分字段，收紧成非可选会让解析整条失败。
struct EngineStatus: Codable {
    var state: String?
    var stage: String?
    var nodeId: String?
    var nodeName: String?
    var since: String?
    var duration: Int64?
    var rxRate: Int64?
    var txRate: Int64?
    var sessionRx: Int64?
    var sessionTx: Int64?
    var monthUsed: Int64?
    var monthLimit: Int64?
    var tunnelIp: String?
    var dns: String?
    var error: String?
    var version: String?

    /// 显式空初始化：默认值的那几个字段在未连接时也要能安全读取。
    init() {}

    /// 未连接时的占位值。用计算属性而不是常量，避免被意外共享修改。
    static var idle: EngineStatus {
        var status = EngineStatus()
        status.state = "idle"
        status.stage = "未连接"
        return status
    }

    var isConnected: Bool { state == "connected" }
    var isBusy: Bool {
        state == "connecting" || state == "reconnecting" || state == "disconnecting"
    }

    /// 给界面看的一句话状态。后台没连上时优先显示错误原因。
    var displayText: String {
        if let err = error, !err.isEmpty, state == "idle" { return err }
        return stage ?? "未连接"
    }
}
