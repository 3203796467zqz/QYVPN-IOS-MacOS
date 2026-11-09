// SPDX-License-Identifier: GPL-2.0-or-later
// 与 Go 内核（internal/ipc、internal/agent）一一对应的数据结构。
import Foundation

/// 节点用量。
struct Usage: Codable, Identifiable {
    var id: String { month }
    var source: String = ""
    var estimated: Bool = false
    var incomplete: Bool = false
    var month: String = ""
    var uploadBytes: Int64 = 0
    var downloadBytes: Int64 = 0
    var usedBytes: Int64 = 0
    var limitBytes: Int64 = 0
    var online: Int = 0
    var fresh: Bool = false
    var configured: Bool = false
    var full: Bool = false
    var exhausted: Bool = false
    var blocked: Bool = false
}

/// 节点（界面用，不含密码）。
struct NodeView: Codable, Identifiable {
    var id: String
    var name: String
    var host: String
    var port: Int
    var icon: String = ""
    var available: Bool = true
    var login: String = ""
    var usage: Usage?
    var maxUsers: Int = 0
    var block: String = ""

    var loadLabel: String {
        guard let usage, usage.configured else { return "统计未接入" }
        guard usage.fresh else { return "统计待更新" }
        if usage.exhausted { return "流量用完" }
        if usage.full { return "负载已满" }
        return "负载正常"
    }

    var usedText: String {
        guard let usage, usage.configured else { return "统计尚未接入" }
        return "本月已用 \(Format.quota(usage.usedBytes)) / \(Format.quota(usage.limitBytes))"
    }
}

/// 连接状态快照。
struct EngineStatus: Codable {
    var state: String = "idle"
    var stage: String = "未连接"
    var nodeId: String = ""
    var nodeName: String = ""
    var since: String = ""
    var duration: Int64 = 0
    var rxRate: Int64 = 0
    var txRate: Int64 = 0
    var sessionRx: Int64 = 0
    var sessionTx: Int64 = 0
    var monthUsed: Int64 = 0
    var monthLimit: Int64 = 0
    var tunnelIp: String = ""
    var dns: String = ""
    var error: String = ""
    var version: String = ""

    var isBusy: Bool { state == "connecting" || state == "reconnecting" || state == "disconnecting" }
    var isConnected: Bool { state == "connected" }

    // 账号态（后台 1.9 起节点按套餐下发）。
    // 用可选类型而不是带默认值的 Bool：内核没下发这个键时（旧版本），
    // Swift 合成的 Decodable 会因 keyNotFound 整体解码失败。
    var needLogin: Bool?
    var loggedIn: Bool?
    var noNodes: Bool?
    var account: UserAccount?
    var plan: PlanInfo?

    /// 需要登录：以后台说法为准，本地没有令牌时也算。
    var needsLogin: Bool { needLogin ?? !(loggedIn ?? false) }

    var title: String {
        switch state {
        case "connected": return "节点通道已连接"
        case "connecting": return "正在连接"
        case "reconnecting": return "等待重新连接"
        case "disconnecting": return "正在断开"
        default: return "未连接"
        }
    }

    var durationText: String {
        guard isConnected, duration > 0 else { return "00:00:00" }
        let h = duration / 3600
        let m = (duration % 3600) / 60
        let s = duration % 60
        return String(format: "%02d:%02d:%02d", h, m, s)
    }
}

/// 账号信息（对应后台 userView）。
///
/// 后台对 expire / lastLogin / platform / planName 等字段用了 omitempty，
/// 未激活账号就可能整键缺失。Swift 合成的 Decodable 在缺失时直接抛 keyNotFound，
/// 会让「整个 EngineStatus 解码失败」，界面表现为一片空白——所以这里逐个 decodeIfPresent。
struct UserAccount: Codable {
    var id: String = ""
    var username: String = ""
    var email: String = ""
    var member: String = ""
    var type: String = ""
    var expire: String = ""
    var active: Bool = false
    var daysLeft: Int = 0
    var created: String = ""
    var lastLogin: String = ""
    var platform: String = ""
    var disabled: Bool = false
    var note: String = ""
    var planId: String = ""
    var planName: String = ""

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decodeIfPresent(String.self, forKey: .id) ?? ""
        username = try c.decodeIfPresent(String.self, forKey: .username) ?? ""
        email = try c.decodeIfPresent(String.self, forKey: .email) ?? ""
        member = try c.decodeIfPresent(String.self, forKey: .member) ?? ""
        type = try c.decodeIfPresent(String.self, forKey: .type) ?? ""
        expire = try c.decodeIfPresent(String.self, forKey: .expire) ?? ""
        active = try c.decodeIfPresent(Bool.self, forKey: .active) ?? false
        daysLeft = try c.decodeIfPresent(Int.self, forKey: .daysLeft) ?? 0
        created = try c.decodeIfPresent(String.self, forKey: .created) ?? ""
        lastLogin = try c.decodeIfPresent(String.self, forKey: .lastLogin) ?? ""
        platform = try c.decodeIfPresent(String.self, forKey: .platform) ?? ""
        disabled = try c.decodeIfPresent(Bool.self, forKey: .disabled) ?? false
        note = try c.decodeIfPresent(String.self, forKey: .note) ?? ""
        planId = try c.decodeIfPresent(String.self, forKey: .planId) ?? ""
        planName = try c.decodeIfPresent(String.self, forKey: .planName) ?? ""
    }

    /// 「会员 · 剩余 30 天」这类一行摘要。
    var summary: String {
        var parts: [String] = []
        if !type.isEmpty { parts.append(type) }
        if active, daysLeft > 0 {
            parts.append("剩余 \(daysLeft) 天")
        } else if !active {
            parts.append("未激活")
        }
        if !expire.isEmpty { parts.append("到期 \(expire)") }
        return parts.joined(separator: " · ")
    }
}

/// 当前套餐摘要（后台 plan: {id, name, nodes}）。
struct PlanInfo: Codable {
    var id: String = ""
    var name: String = ""
    var nodes: Int = 0

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decodeIfPresent(String.self, forKey: .id) ?? ""
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? ""
        nodes = try c.decodeIfPresent(Int.self, forKey: .nodes) ?? 0
    }

    var summary: String {
        name.isEmpty ? "\(nodes) 台节点" : "\(name) · \(nodes) 台节点"
    }
}

/// 偏好设置。
struct EngineSettings: Codable, Equatable {
    var selectedNode: String = ""
    var bypassEnabled: Bool = false
    var bypassChina: Bool = true
    var bypassDomains: String = ""
    var autoConnect: Bool = false
}

/// 测速结果。
struct ProbeResult: Codable {
    var ms: Int64 = 0
    var error: String = ""
}

/// 更新信息。
struct UpdateInfo: Codable {
    var available: Bool = false
    var version: String = ""
    var notes: String = ""
    var size: Int64 = 0
    var sha256: String = ""
    var message: String = ""
}

/// 内核统一响应。
struct EngineResponse: Codable {
    var ok: Bool = false
    var error: String = ""
    var version: String = ""
    var status: EngineStatus?
    var nodes: [NodeView]?
    var settings: EngineSettings?
    var probes: [String: ProbeResult]?
    var log: [String]?
    var update: UpdateInfo?
    var path: String?

    // 卡密兑换结果。
    var days: Int?
    var expire: String?
}

/// 数值格式化（与 Android / Windows 客户端同一套规则）。
enum Format {
    static func bytes(_ value: Int64) -> String {
        let v = max(0, value)
        switch v {
        case ..<1024:
            return "\(v) B"
        case ..<(1024 * 1024):
            return trim(String(format: "%.1f", Double(v) / 1024)) + " KB"
        case ..<(1024 * 1024 * 1024):
            return trim(String(format: "%.2f", Double(v) / 1024 / 1024)) + " MB"
        default:
            return trim(String(format: "%.2f", Double(v) / 1024 / 1024 / 1024)) + " GB"
        }
    }

    static func quota(_ value: Int64) -> String {
        let v = max(0, value)
        switch v {
        case ..<1024:
            return "\(v) B"
        case ..<(1024 * 1024):
            return "\(Int((Double(v) / 1024).rounded())) KB"
        case ..<(1024 * 1024 * 1024):
            return "\(Int((Double(v) / 1024 / 1024).rounded())) MB"
        default:
            return trim(String(format: "%.2f", Double(v) / 1024 / 1024 / 1024)) + " GB"
        }
    }

    static func rate(_ value: Int64) -> String {
        value <= 0 ? "0 B/s" : bytes(value) + "/s"
    }

    private static func trim(_ text: String) -> String {
        guard text.contains(".") else { return text }
        var t = text
        while t.hasSuffix("0") { t.removeLast() }
        if t.hasSuffix(".") { t.removeLast() }
        return t
    }

    /// 距离北京时间下月 1 日的天数。
    static func resetDays() -> Int {
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = TimeZone(identifier: "Asia/Shanghai") ?? .current
        let now = Date()
        guard let startOfMonth = calendar.date(from: calendar.dateComponents([.year, .month], from: now)),
              let next = calendar.date(byAdding: .month, value: 1, to: startOfMonth) else { return 0 }
        let days = calendar.dateComponents([.day], from: calendar.startOfDay(for: now), to: next).day ?? 0
        return max(1, days)
    }
}
