// SPDX-License-Identifier: GPL-2.0-or-later
// Models.swift：后台 /api/app/* 的响应模型。
//
// 只声明界面真正用到的字段：后台的节点结构比这里大得多（还带 SSTP 账号口令），
// App 端不需要也不应该持有节点口令——真正的连接参数由扩展自己拉取。

import Foundation

// MARK: - 节点

/// 节点本月用量与在线人数，对应后台 NodeUsage。
struct NodeUsage: Codable, Equatable {
    var source: String?
    var estimated: Bool?
    var incomplete: Bool?
    var month: String?
    var uploadBytes: Int64?
    var downloadBytes: Int64?
    var usedBytes: Int64?
    var limitBytes: Int64?
    var online: Int?
    var fresh: Bool?
    var configured: Bool?
    var full: Bool?
    var exhausted: Bool?
    var blocked: Bool?
}

/// 后台下发的一台节点（App 只取展示需要的字段）。
struct NodeView: Codable, Identifiable, Equatable {
    var id: String
    var name: String
    var host: String
    var port: Int
    var icon: String?
    var maxUsers: Int?
    var quotaGB: Double?
    var quotaUsedGB: Double?
    var usage: NodeUsage?

    /// 节点在线人数，用于「当前节点」那行小字。
    var peopleText: String {
        guard let u = usage, u.fresh == true else { return "状态同步中" }
        let online = u.online ?? 0
        if let max = maxUsers, max > 0 {
            return "连接 \(online) 人 · 上限 \(max)"
        }
        return "连接 \(online) 人"
    }

    /// 本月额度文案；后台没给节点配额度时不显示。
    var quotaText: String? {
        guard let quota = quotaGB, quota > 0 else { return nil }
        let used = quotaUsedGB ?? 0
        return String(format: "本月 %.1f / %.0f GB", used, quota)
    }

    /// 是否因为流量用完被后台限制。
    var exhausted: Bool {
        if let u = usage, u.exhausted == true { return true }
        guard let quota = quotaGB, quota > 0 else { return false }
        return (quotaUsedGB ?? 0) >= quota
    }
}

// MARK: - 账号与套餐

/// 账号信息，字段对应后台 userView。
struct Account: Codable, Equatable {
    var id: String?
    var username: String?
    var email: String?
    var member: String?
    var type: String?
    var expire: String?
    var active: Bool?
    var daysLeft: Int?
    var created: String?
    var lastLogin: String?
    var platform: String?
    var disabled: Bool?
    var note: String?
    var planId: String?
    var planName: String?
    var nodes: [String]?

    /// 「会员 · 剩余 30 天」这类副标题。
    var summary: String {
        var parts: [String] = []
        if let t = type, !t.isEmpty { parts.append(t) }
        if let d = daysLeft {
            parts.append(d > 0 ? "剩余 \(d) 天" : "已到期")
        }
        if let p = planName, !p.isEmpty { parts.append(p) }
        return parts.isEmpty ? "账号信息同步中" : parts.joined(separator: " · ")
    }
}

/// 套餐摘要，对应后台 planBrief。
struct PlanBrief: Codable, Equatable {
    var id: String?
    var name: String?
    var days: Int?
    var nodes: Int?
}

// MARK: - 接口响应

struct LoginReply: Codable {
    var token: String
    var user: Account?
}

/// /api/app/config：未登录时 needLogin 为真、nodes 为空，界面据此跳登录页。
struct ConfigResponse: Codable {
    var nodes: [NodeView]?
    var needLogin: Bool?
    var noNodes: Bool?
    var quotaLimited: Int?
    var user: Account?
    var plan: PlanBrief?
}

/// /api/app/status 里的单节点状态。
struct NodeStatus: Codable {
    var id: String
    var maxUsers: Int?
    var usage: NodeUsage?
    var quotaGB: Double?
    var quotaUsedGB: Double?
}

struct StatusResponse: Codable {
    var nodes: [NodeStatus]?
}

// MARK: - iOS 内测通道

/// 后台配置的 iOS 版本信息。iOS 不能侧载安装包，所以这里只有版本号与
/// TestFlight 邀请链接，客户端据此提示「去 TestFlight 更新」。
struct IOSRelease: Codable, Equatable {
    var version: String
    var build: Int
    var testflight: String
    var notes: String?
    var published: String?

    var testflightURL: URL? { URL(string: testflight) }

    /// 与当前安装的构建号比较：只有后台更高才提示更新。
    var isNewerThanInstalled: Bool {
        let current = Int(Bundle.main.infoDictionary?["CFBundleVersion"] as? String ?? "0") ?? 0
        return build > current
    }
}

struct IOSReleaseReply: Codable {
    var release: IOSRelease?
}

// MARK: - 设置与偏好

/// 本机偏好，存在 App Group 的 UserDefaults 里，App 与扩展都能读。
struct AppSettings: Equatable {
    var selectedNode: String
    var autoReconnect: Bool
}

// MARK: - 格式化

enum Format {
    /// 字节数按 1024 进制格式化成 KB/MB/GB。
    static func bytes(_ value: Int64) -> String {
        let v = Double(max(0, value))
        let units = ["B", "KB", "MB", "GB", "TB"]
        var idx = 0
        var n = v
        while n >= 1024 && idx < units.count - 1 {
            n /= 1024
            idx += 1
        }
        if idx == 0 { return String(format: "%.0f %@", n, units[idx]) }
        return String(format: n >= 100 ? "%.0f %@" : "%.1f %@", n, units[idx])
    }

    /// 速率：固定带单位，避免数字跳动时单位跟着变。
    static func rate(_ value: Int64) -> String {
        bytes(value) + "/s"
    }

    /// 额度用 GB 展示，与后台的 quotaGB 单位一致。
    static func quota(_ value: Int64) -> String {
        if value <= 0 { return "不限量" }
        let gb = Double(value) / 1024 / 1024 / 1024
        return String(format: gb >= 100 ? "%.0f GB" : "%.1f GB", gb)
    }

    /// 连接时长：mm:ss / hh:mm:ss。
    static func duration(_ seconds: Int64) -> String {
        let s = max(0, seconds)
        if s < 3600 { return String(format: "%02d:%02d", s / 60, s % 60) }
        return String(format: "%d:%02d:%02d", s / 3600, (s % 3600) / 60, s % 60)
    }
}
