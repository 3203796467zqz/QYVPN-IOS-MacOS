// SPDX-License-Identifier: GPL-2.0-or-later
// 通用界面组件：卡片、电源按钮、状态徽标、流量卡。
import SwiftUI
import AppKit

extension Color {
    static let brandBlue = Color(red: 0.188, green: 0.412, blue: 0.961)   // #3069F5
    static let brandGreen = Color(red: 0.129, green: 0.718, blue: 0.376)  // #21B760
    static let brandRed = Color(red: 0.918, green: 0.365, blue: 0.361)    // #EA5D5C
    static let brandAmber = Color(red: 0.945, green: 0.639, blue: 0.196)  // #F1A332
    static let inkPrimary = Color(nsColor: .labelColor)
    static let inkSecondary = Color(nsColor: .secondaryLabelColor)
    static let cardBackground = Color(nsColor: .controlBackgroundColor)
}

/// 白色卡片容器。
struct Card<Content: View>: View {
    var padding: CGFloat = 18
    @ViewBuilder var content: Content

    var body: some View {
        content
            .padding(padding)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(
                RoundedRectangle(cornerRadius: 14, style: .continuous)
                    .fill(Color.cardBackground)
            )
            .overlay(
                RoundedRectangle(cornerRadius: 14, style: .continuous)
                    .stroke(Color.primary.opacity(0.06), lineWidth: 1)
            )
    }
}

/// 卡片标题。
struct CardTitle: View {
    let text: String
    var trailing: AnyView?

    init(_ text: String) {
        self.text = text
        self.trailing = nil
    }

    init<T: View>(_ text: String, @ViewBuilder trailing: () -> T) {
        self.text = text
        self.trailing = AnyView(trailing())
    }

    var body: some View {
        HStack {
            Text(text).font(.system(size: 13, weight: .semibold)).foregroundStyle(Color.inkSecondary)
            Spacer()
            trailing
        }
    }
}

/// 状态徽标。
struct Badge: View {
    let text: String
    var color: Color = .brandBlue

    var body: some View {
        Text(text)
            .font(.system(size: 11, weight: .medium))
            .padding(.horizontal, 8)
            .padding(.vertical, 3)
            .background(Capsule().fill(color.opacity(0.12)))
            .foregroundStyle(color)
    }
}

/// 大圆电源按钮。
struct PowerButton: View {
    let state: String
    let action: () -> Void
    @State private var spinning = false

    private var busy: Bool { state == "connecting" || state == "reconnecting" || state == "disconnecting" }
    private var connected: Bool { state == "connected" }
    private var ringColor: Color {
        if connected { return .brandGreen }
        if busy { return .brandBlue }
        return Color(nsColor: .tertiaryLabelColor)
    }

    var body: some View {
        Button(action: action) {
            ZStack {
                Circle()
                    .fill(ringColor.opacity(connected ? 0.12 : 0.08))
                    .frame(width: 196, height: 196)
                Circle()
                    .stroke(ringColor.opacity(0.22), lineWidth: 1.5)
                    .frame(width: 196, height: 196)
                if busy {
                    Circle()
                        .trim(from: 0, to: 0.22)
                        .stroke(Color.brandBlue, style: StrokeStyle(lineWidth: 4, lineCap: .round))
                        .frame(width: 196, height: 196)
                        .rotationEffect(.degrees(spinning ? 360 : 0))
                        .animation(.linear(duration: 1.6).repeatForever(autoreverses: false), value: spinning)
                }
                Circle()
                    .fill(ringColor)
                    .frame(width: 148, height: 148)
                    .shadow(color: ringColor.opacity(0.35), radius: 14, y: 6)
                Image(systemName: "power")
                    .font(.system(size: 52, weight: .light))
                    .foregroundStyle(.white)
            }
            .contentShape(Circle())
        }
        .buttonStyle(.plain)
        .onAppear { spinning = busy }
        .onChange(of: state) { newValue in
            spinning = (newValue == "connecting" || newValue == "reconnecting")
        }
        .help(connected ? "点击断开连接" : "点击连接所选节点")
    }
}

/// 速率列（上传 / 下载）。
struct RateColumn: View {
    let title: String
    let value: String
    let systemImage: String

    var body: some View {
        Card(padding: 16) {
            HStack(spacing: 12) {
                ZStack {
                    Circle().fill(Color.brandBlue.opacity(0.10)).frame(width: 30, height: 30)
                    Image(systemName: systemImage)
                        .font(.system(size: 13, weight: .semibold))
                        .foregroundStyle(Color.brandBlue)
                }
                VStack(alignment: .leading, spacing: 2) {
                    Text(title).font(.system(size: 12)).foregroundStyle(Color.inkSecondary)
                    Text(value).font(.system(size: 15, weight: .semibold).monospacedDigit())
                }
                Spacer()
            }
        }
    }
}

/// 本月流量卡片。
struct TrafficCard: View {
    let used: Int64
    let limit: Int64
    let upload: Int64
    let download: Int64
    let load: String
    let onlinePeople: Int
    let configured: Bool

    private var ratio: Double {
        guard limit > 0 else { return 0 }
        return min(1, max(0, Double(used) / Double(limit)))
    }

    private var exhausted: Bool { limit > 0 && used >= limit }

    var body: some View {
        Card {
            VStack(alignment: .leading, spacing: 12) {
                HStack {
                    Text("本月流量").font(.system(size: 13, weight: .semibold))
                    Spacer()
                    Text("\(Format.resetDays()) 日后重置")
                        .font(.system(size: 11)).foregroundStyle(Color.inkSecondary)
                }
                HStack(alignment: .firstTextBaseline, spacing: 6) {
                    Text(configured ? Format.quota(used) : "—")
                        .font(.system(size: 26, weight: .bold).monospacedDigit())
                        .foregroundStyle(exhausted ? Color.brandRed : Color.inkPrimary)
                    Text(limit > 0 ? "/ \(Format.quota(limit))" : "/ 不限量")
                        .font(.system(size: 12)).foregroundStyle(Color.inkSecondary)
                    Spacer()
                    if limit > 0 {
                        Text("\(Int(ratio * 100))%")
                            .font(.system(size: 12, weight: .medium).monospacedDigit())
                            .foregroundStyle(exhausted ? Color.brandRed : Color.inkSecondary)
                    }
                }
                GeometryReader { geo in
                    ZStack(alignment: .leading) {
                        Capsule().fill(Color.primary.opacity(0.07))
                        Capsule()
                            .fill(exhausted ? Color.brandRed : Color.brandBlue)
                            .frame(width: max(0, geo.size.width * ratio))
                    }
                }
                .frame(height: 7)

                HStack(spacing: 14) {
                    Label(Format.bytes(download), systemImage: "arrow.down")
                    Label(Format.bytes(upload), systemImage: "arrow.up")
                }
                .font(.system(size: 11))
                .foregroundStyle(Color.inkSecondary)

                HStack(spacing: 4) {
                    Text(remainingText)
                    Text("·")
                    Text(configured ? "连接 \(onlinePeople) 人 · \(load)" : load)
                }
                .font(.system(size: 11))
                .foregroundStyle(exhausted ? Color.brandRed : Color.inkSecondary)
            }
        }
    }

    private var remainingText: String {
        guard configured else { return "统计尚未接入" }
        if limit <= 0 { return "本月不限量" }
        let remain = max(0, limit - used)
        return remain == 0 ? "额度已用完" : "剩余 \(Format.quota(remain))"
    }
}

/// 安装引导卡片。
struct InstallCard: View {
    let working: Bool
    let install: () -> Void

    var body: some View {
        Card {
            HStack(spacing: 14) {
                Image(systemName: "lock.shield")
                    .font(.system(size: 26))
                    .foregroundStyle(Color.brandBlue)
                VStack(alignment: .leading, spacing: 4) {
                    Text("需要安装网络组件").font(.system(size: 14, weight: .semibold))
                    Text("建立节点通道需要一次管理员授权，用于创建系统隧道、配置路由与 DNS。组件只与清幽私人云后台通信。")
                        .font(.system(size: 11))
                        .foregroundStyle(Color.inkSecondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
                Spacer()
                Button(action: install) {
                    if working {
                        ProgressView().controlSize(.small)
                    } else {
                        Text("安装")
                    }
                }
                .buttonStyle(.borderedProminent)
                .tint(Color.brandBlue)
                .disabled(working)
            }
        }
    }
}
