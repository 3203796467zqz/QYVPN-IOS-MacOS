// SPDX-License-Identifier: GPL-2.0-or-later
// Components.swift：iOS 界面的通用组件。
//
// 视觉沿用 macOS 客户端的品牌色与卡片语言（圆角 16、细描边、蓝/绿/红三色状态），
// 但尺寸按手机重排：电源按钮 168pt、卡片内边距 16、左右页边距 18。

import SwiftUI

extension Color {
    static let brandBlue = Color(red: 0.188, green: 0.412, blue: 0.961)   // #3069F5
    static let brandGreen = Color(red: 0.129, green: 0.718, blue: 0.376)  // #21B760
    static let brandRed = Color(red: 0.918, green: 0.365, blue: 0.361)    // #EA5D5C
    static let brandAmber = Color(red: 0.945, green: 0.639, blue: 0.196)  // #F1A332
    static let inkPrimary = Color(.label)
    static let inkSecondary = Color(.secondaryLabel)
    static let cardBackground = Color(.secondarySystemGroupedBackground)
    static let pageBackground = Color(.systemGroupedBackground)
}

/// 白色（深色模式下为分组底色）卡片容器。
struct Card<Content: View>: View {
    var padding: CGFloat = 16
    @ViewBuilder var content: Content

    var body: some View {
        content
            .padding(padding)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(
                RoundedRectangle(cornerRadius: 16, style: .continuous)
                    .fill(Color.cardBackground)
            )
            .overlay(
                RoundedRectangle(cornerRadius: 16, style: .continuous)
                    .stroke(Color.primary.opacity(0.06), lineWidth: 1)
            )
    }
}

/// 卡片标题。
struct CardTitle: View {
    let text: String
    var trailing: String?

    init(_ text: String, trailing: String? = nil) {
        self.text = text
        self.trailing = trailing
    }

    var body: some View {
        HStack {
            Text(text).font(.system(size: 13, weight: .semibold)).foregroundStyle(Color.inkSecondary)
            Spacer()
            if let trailing {
                Text(trailing).font(.system(size: 11)).foregroundStyle(Color.inkSecondary)
            }
        }
    }
}

/// 状态胶囊。
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

/// 大圆电源按钮。状态灯颜色与旋转动画沿用 macOS 版。
struct PowerButton: View {
    let state: String
    let enabled: Bool
    let action: () -> Void

    @State private var spinning = false

    private var busy: Bool {
        state == "connecting" || state == "reconnecting" || state == "disconnecting"
    }

    private var connected: Bool { state == "connected" }

    private var ringColor: Color {
        if connected { return .brandGreen }
        if busy { return .brandBlue }
        return Color(.tertiaryLabel)
    }

    var body: some View {
        Button(action: action) {
            ZStack {
                Circle().fill(ringColor.opacity(connected ? 0.12 : 0.08)).frame(width: 168, height: 168)
                Circle().stroke(ringColor.opacity(0.22), lineWidth: 1.5).frame(width: 168, height: 168)
                if busy {
                    Circle()
                        .trim(from: 0, to: 0.22)
                        .stroke(Color.brandBlue, style: StrokeStyle(lineWidth: 4, lineCap: .round))
                        .frame(width: 168, height: 168)
                        .rotationEffect(.degrees(spinning ? 360 : 0))
                        .animation(.linear(duration: 1.6).repeatForever(autoreverses: false), value: spinning)
                }
                Circle()
                    .fill(enabled ? ringColor : Color(.systemGray3))
                    .frame(width: 126, height: 126)
                    .shadow(color: ringColor.opacity(0.35), radius: 14, y: 6)
                Image(systemName: "power")
                    .font(.system(size: 44, weight: .light))
                    .foregroundStyle(.white)
            }
            .contentShape(Circle())
        }
        .buttonStyle(.plain)
        .disabled(!enabled)
        .onAppear { spinning = busy }
        .onChange(of: state) { newValue in
            spinning = newValue == "connecting" || newValue == "reconnecting"
        }
    }
}

/// 上行/下行速率块。
struct RateTile: View {
    let title: String
    let value: String
    let systemImage: String

    var body: some View {
        HStack(spacing: 10) {
            ZStack {
                Circle().fill(Color.brandBlue.opacity(0.10)).frame(width: 28, height: 28)
                Image(systemName: systemImage)
                    .font(.system(size: 12, weight: .semibold))
                    .foregroundStyle(Color.brandBlue)
            }
            VStack(alignment: .leading, spacing: 1) {
                Text(title).font(.system(size: 11)).foregroundStyle(Color.inkSecondary)
                Text(value).font(.system(size: 14, weight: .semibold).monospacedDigit())
                    .lineLimit(1).minimumScaleFactor(0.7)
            }
            Spacer(minLength: 0)
        }
        .padding(12)
        .background(
            RoundedRectangle(cornerRadius: 14, style: .continuous).fill(Color.cardBackground)
        )
        .overlay(
            RoundedRectangle(cornerRadius: 14, style: .continuous)
                .stroke(Color.primary.opacity(0.06), lineWidth: 1)
        )
    }
}

/// 本月流量卡片：已用 / 额度 + 进度条。
struct TrafficCard: View {
    let used: Int64
    let limit: Int64
    let planName: String?
    let nodeText: String

    private var ratio: Double {
        guard limit > 0 else { return 0 }
        return min(1, max(0, Double(used) / Double(limit)))
    }

    private var exhausted: Bool { limit > 0 && used >= limit }

    var body: some View {
        Card {
            VStack(alignment: .leading, spacing: 10) {
                HStack {
                    Text("本月流量").font(.system(size: 13, weight: .semibold))
                    Spacer()
                    if let planName, !planName.isEmpty {
                        Text(planName).font(.system(size: 11)).foregroundStyle(Color.inkSecondary)
                    }
                }
                HStack(alignment: .firstTextBaseline, spacing: 6) {
                    Text(Format.bytes(used))
                        .font(.system(size: 24, weight: .bold).monospacedDigit())
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

                Text(nodeText)
                    .font(.system(size: 11))
                    .foregroundStyle(Color.inkSecondary)
            }
        }
    }
}

/// 顶部提示条。
struct BannerView: View {
    let banner: Banner

    private var color: Color {
        switch banner.kind {
        case .info: return .brandBlue
        case .success: return .brandGreen
        case .error: return .brandRed
        }
    }

    var body: some View {
        Text(banner.text)
            .font(.system(size: 13, weight: .medium))
            .foregroundStyle(.white)
            .padding(.horizontal, 16)
            .padding(.vertical, 10)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(RoundedRectangle(cornerRadius: 12, style: .continuous).fill(color))
            .padding(.horizontal, 18)
            .transition(.move(edge: .top).combined(with: .opacity))
    }
}
