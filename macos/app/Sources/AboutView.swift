// SPDX-License-Identifier: GPL-2.0-or-later
// 关于：版本信息、更新与连接日志。
import SwiftUI
import AppKit

struct AboutView: View {
    @EnvironmentObject private var state: AppState

    var body: some View {
        ScrollView {
            VStack(spacing: 16) {
                Card {
                    HStack(spacing: 16) {
                        AppLogo(size: 56)
                        VStack(alignment: .leading, spacing: 4) {
                            Text("清幽私人云").font(.system(size: 17, weight: .semibold))
                            Text("macOS 客户端 \(state.status.version.isEmpty ? "1.0.0" : state.status.version)")
                                .font(.system(size: 12))
                                .foregroundStyle(Color.inkSecondary)
                            Text("系统连接协议：SSTP（TCP 443）")
                                .font(.system(size: 11))
                                .foregroundStyle(Color.inkSecondary)
                        }
                        Spacer()
                    }
                }

                Card {
                    VStack(alignment: .leading, spacing: 12) {
                        CardTitle("软件更新")
                        HStack(spacing: 10) {
                            Text(state.updateInfo?.message ?? "点击检查是否有新版本")
                                .font(.system(size: 12))
                                .foregroundStyle(Color.inkSecondary)
                            Spacer()
                            Button {
                                state.checkUpdate()
                            } label: {
                                if state.working {
                                    ProgressView().controlSize(.small)
                                } else {
                                    Text("检查更新")
                                }
                            }
                            .buttonStyle(.borderedProminent)
                            .tint(Color.brandBlue)
                            .disabled(state.working)
                        }
                        if let info = state.updateInfo, info.available {
                            if !info.notes.isEmpty {
                                Text(info.notes)
                                    .font(.system(size: 11))
                                    .foregroundStyle(Color.inkSecondary)
                                    .fixedSize(horizontal: false, vertical: true)
                            }
                            HStack {
                                Button("下载更新包") { state.downloadUpdate() }
                                    .buttonStyle(.bordered)
                                Spacer()
                                Text(Format.bytes(info.size))
                                    .font(.system(size: 10))
                                    .foregroundStyle(Color.inkSecondary)
                            }
                        }
                    }
                }

                Card {
                    VStack(alignment: .leading, spacing: 10) {
                        CardTitle("连接日志") {
                            Button(state.showLog ? "收起" : "展开") {
                                state.showLog.toggle()
                                if state.showLog { state.loadLog() }
                            }
                            .buttonStyle(.borderless)
                        }
                        if state.showLog {
                            ScrollView {
                                VStack(alignment: .leading, spacing: 2) {
                                    ForEach(Array(state.logLines.enumerated()), id: \.offset) { _, line in
                                        Text(line)
                                            .font(.system(size: 10, design: .monospaced))
                                            .foregroundStyle(Color.inkSecondary)
                                            .textSelection(.enabled)
                                            .frame(maxWidth: .infinity, alignment: .leading)
                                    }
                                }
                                .padding(8)
                            }
                            .frame(height: 180)
                            .background(RoundedRectangle(cornerRadius: 8).fill(Color.primary.opacity(0.04)))
                            Text("日志位于 /Library/Application Support/QingyouPrivateCloud/connection.log，密码与令牌已脱敏。")
                                .font(.system(size: 10))
                                .foregroundStyle(Color.inkSecondary)
                        } else {
                            Text("连接过程、认证与断开原因都记录在本地日志里，排查问题时可以展开查看。")
                                .font(.system(size: 11))
                                .foregroundStyle(Color.inkSecondary)
                        }
                    }
                }
            }
            .padding(20)
        }
    }
}

/// 应用图标：直接复用打包进 .app 的 AppIcon.icns（与 Android 客户端同一张图），
/// 万一资源缺失就退回一个纯色占位，不让界面崩掉。
struct AppLogo: View {
    var size: CGFloat = 56

    private static let brand: NSImage? = {
        guard let url = Bundle.main.url(forResource: "AppIcon", withExtension: "icns") else { return nil }
        let image = NSImage(contentsOf: url)
        image?.size = NSSize(width: 512, height: 512)
        return image
    }()

    var body: some View {
        Group {
            if let image = Self.brand {
                Image(nsImage: image)
                    .resizable()
                    .interpolation(.high)
            } else {
                ZStack {
                    RoundedRectangle(cornerRadius: size * 0.24, style: .continuous)
                        .fill(Color.brandBlue)
                    Image(systemName: "shield.lefthalf.filled")
                        .font(.system(size: size * 0.46, weight: .medium))
                        .foregroundStyle(.white)
                }
            }
        }
        .frame(width: size, height: size)
        .clipShape(RoundedRectangle(cornerRadius: size * 0.13, style: .continuous))
    }
}
