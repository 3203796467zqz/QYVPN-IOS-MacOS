// SPDX-License-Identifier: GPL-2.0-or-later
// 连接首页：当前节点、电源按钮、上下行速率、本月流量。
import SwiftUI

struct HomeView: View {
    @EnvironmentObject private var state: AppState

    private var node: NodeView? { state.selectedNode }
    private var usage: Usage? { node?.usage }

    var body: some View {
        ScrollView {
            VStack(spacing: 16) {
                if !state.engineReady {
                    InstallCard(working: state.working) { state.installHelper() }
                }
                nodeCard
                powerSection
                HStack(spacing: 12) {
                    RateColumn(title: "下载速率", value: Format.rate(state.status.rxRate), systemImage: "arrow.down")
                    RateColumn(title: "上传速率", value: Format.rate(state.status.txRate), systemImage: "arrow.up")
                }
                TrafficCard(
                    used: state.status.monthUsed > 0 ? state.status.monthUsed : (usage?.usedBytes ?? 0),
                    limit: state.status.monthLimit > 0 ? state.status.monthLimit : (usage?.limitBytes ?? 0),
                    upload: usage?.uploadBytes ?? 0,
                    download: usage?.downloadBytes ?? 0,
                    load: node?.loadLabel ?? "统计未接入",
                    onlinePeople: usage?.online ?? 0,
                    configured: usage?.configured ?? false
                )
                footer
            }
            .padding(20)
        }
    }

    private var nodeCard: some View {
        Card {
            HStack(spacing: 14) {
                NodeAvatar(icon: node?.icon ?? "", name: node?.name ?? "未选择")
                VStack(alignment: .leading, spacing: 5) {
                    HStack(spacing: 8) {
                        Text(node?.name ?? "尚未选择节点")
                            .font(.system(size: 16, weight: .semibold))
                        if let node, node.block.isEmpty == false {
                            Badge(text: "不可用", color: .brandRed)
                        }
                    }
                    Text(node?.host ?? "请到「节点列表」选择一台节点")
                        .font(.system(size: 11))
                        .foregroundStyle(Color.inkSecondary)
                    Text(node?.usage?.fresh == true && usage?.configured == true
                         ? "\(usage?.online ?? 0) 人  ·  \(node?.loadLabel ?? "")"
                         : (node?.loadLabel ?? "统计未接入"))
                        .font(.system(size: 11))
                        .foregroundStyle(Color.inkSecondary)
                }
                Spacer()
                VStack(alignment: .trailing, spacing: 6) {
                    Badge(text: state.status.isConnected ? "系统已连接" : "未连接",
                          color: state.status.isConnected ? .brandGreen : .inkSecondary)
                    if !state.status.tunnelIp.isEmpty {
                        Text(state.status.tunnelIp)
                            .font(.system(size: 11).monospacedDigit())
                            .foregroundStyle(Color.inkSecondary)
                    }
                }
            }
        }
    }

    private var powerSection: some View {
        VStack(spacing: 12) {
            PowerButton(state: state.status.state) { state.toggle() }
            Text(state.status.title)
                .font(.system(size: 20, weight: .semibold))
            if state.status.isConnected {
                Text(state.status.durationText)
                    .font(.system(size: 13).monospacedDigit())
                    .foregroundStyle(Color.inkSecondary)
            } else if !state.status.error.isEmpty {
                Text(state.status.error)
                    .font(.system(size: 12))
                    .foregroundStyle(Color.brandRed)
                    .multilineTextAlignment(.center)
                    .frame(maxWidth: 460)
            } else if state.status.state == "reconnecting" {
                Text(state.status.stage)
                    .font(.system(size: 12))
                    .foregroundStyle(Color.brandAmber)
            }
        }
        .frame(maxWidth: .infinity)
        .padding(.vertical, 6)
    }

    private var footer: some View {
        HStack {
            Label("本次流量 \(Format.bytes(state.status.sessionRx)) 下载 · \(Format.bytes(state.status.sessionTx)) 上传",
                  systemImage: "chart.bar")
                .font(.system(size: 10))
                .foregroundStyle(Color.inkSecondary)
            Spacer()
            if !state.status.dns.isEmpty {
                Text("DNS \(state.status.dns)")
                    .font(.system(size: 10))
                    .foregroundStyle(Color.inkSecondary)
            }
        }
    }
}

/// 节点国旗/首字头像。
struct NodeAvatar: View {
    let icon: String
    let name: String
    var size: CGFloat = 38

    var body: some View {
        ZStack {
            RoundedRectangle(cornerRadius: size * 0.28, style: .continuous)
                .fill(Color.brandBlue.opacity(0.10))
            if let emoji = NodeAvatar.flag(for: icon) {
                Text(emoji).font(.system(size: size * 0.5))
            } else {
                Text(String(name.prefix(1)))
                    .font(.system(size: size * 0.42, weight: .semibold))
                    .foregroundStyle(Color.brandBlue)
            }
        }
        .frame(width: size, height: size)
    }

    /// 后台下发的是两位国家代码（hk / us），换成旗帜 emoji；没有就用首字。
    static func flag(for code: String) -> String? {
        let trimmed = code.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        guard trimmed.count == 2,
              trimmed.unicodeScalars.allSatisfy({ $0.value >= 97 && $0.value <= 122 }) else {
            return nil
        }
        var scalars = String.UnicodeScalarView()
        for scalar in trimmed.unicodeScalars {
            if let base = UnicodeScalar(127397 + scalar.value) {
                scalars.append(base)
            }
        }
        return String(scalars)
    }
}
