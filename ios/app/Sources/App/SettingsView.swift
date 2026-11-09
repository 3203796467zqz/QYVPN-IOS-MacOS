// SPDX-License-Identifier: GPL-2.0-or-later
// SettingsView.swift：「我的」页——账号、连接日志、关于与退出登录。
//
// 连接日志由扩展进程里的 Go 内核写进 App Group 的 engine/connection.log，
// App 直接读文件展示（不走网络，也不依赖扩展是否在运行）。

import SwiftUI

struct SettingsView: View {
    @EnvironmentObject private var model: AppModel
    @State private var confirmLogout = false

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(spacing: 14) {
                    accountCard
                    linkCard
                    aboutCard
                    Button {
                        confirmLogout = true
                    } label: {
                        Text("退出登录")
                            .font(.system(size: 15, weight: .semibold))
                            .foregroundStyle(Color.brandRed)
                            .frame(maxWidth: .infinity)
                            .padding(.vertical, 14)
                            .background(
                                RoundedRectangle(cornerRadius: 14, style: .continuous)
                                    .fill(Color.cardBackground)
                            )
                            .overlay(
                                RoundedRectangle(cornerRadius: 14, style: .continuous)
                                    .stroke(Color.brandRed.opacity(0.25), lineWidth: 1)
                            )
                    }
                    .padding(.top, 6)
                }
                .padding(.horizontal, 18)
                .padding(.vertical, 12)
            }
            .background(Color.pageBackground)
            .navigationTitle("我的")
            .navigationBarTitleDisplayMode(.inline)
            .confirmationDialog("退出后需要重新输入密码登录，确定退出？",
                                isPresented: $confirmLogout, titleVisibility: .visible) {
                Button("退出登录", role: .destructive) {
                    Task { await model.logout() }
                }
                Button("取消", role: .cancel) {}
            }
        }
    }

    private var accountCard: some View {
        Card {
            VStack(alignment: .leading, spacing: 12) {
                HStack(spacing: 12) {
                    ZStack {
                        Circle().fill(Color.brandBlue.opacity(0.12)).frame(width: 44, height: 44)
                        Image(systemName: "person.fill")
                            .font(.system(size: 18))
                            .foregroundStyle(Color.brandBlue)
                    }
                    VStack(alignment: .leading, spacing: 3) {
                        Text(model.account?.username ?? "—")
                            .font(.system(size: 16, weight: .semibold))
                        Text(model.account?.summary ?? "—")
                            .font(.system(size: 12))
                            .foregroundStyle(Color.inkSecondary)
                    }
                    Spacer()
                }
                Divider()
                row("账号类型", model.account?.type ?? "—")
                if let email = model.account?.email, !email.isEmpty {
                    row("邮箱", email)
                }
                if let expire = model.account?.expire, !expire.isEmpty {
                    row("到期时间", String(expire.prefix(10)))
                }
                row("可用节点", "\(model.nodes.count) 个")
                if let plan = model.plan?.name, !plan.isEmpty {
                    row("当前套餐", plan)
                }
            }
        }
    }

    private var linkCard: some View {
        Card(padding: 0) {
            VStack(spacing: 0) {
                NavigationLink {
                    LogView()
                } label: {
                    HStack {
                        Label("连接日志", systemImage: "doc.text.magnifyingglass")
                            .font(.system(size: 14))
                            .foregroundStyle(Color.inkPrimary)
                        Spacer()
                        Image(systemName: "chevron.right")
                            .font(.system(size: 12, weight: .semibold))
                            .foregroundStyle(Color.inkSecondary)
                    }
                    .padding(16)
                }
                .buttonStyle(.plain)
                if model.updateAvailable {
                    Divider().padding(.leading, 16)
                    Button {
                        model.openTestFlight()
                    } label: {
                        HStack {
                            Label("发现新版本 \(model.updateRelease?.version ?? "")", systemImage: "arrow.down.circle")
                                .font(.system(size: 14))
                                .foregroundStyle(Color.brandBlue)
                            Spacer()
                            Image(systemName: "arrow.up.right")
                                .font(.system(size: 12, weight: .semibold))
                                .foregroundStyle(Color.inkSecondary)
                        }
                        .padding(16)
                    }
                    .buttonStyle(.plain)
                }
            }
        }
    }

    private var aboutCard: some View {
        Card {
            VStack(alignment: .leading, spacing: 10) {
                Text("关于").font(.system(size: 13, weight: .semibold)).foregroundStyle(Color.inkSecondary)
                row("版本", "\(BuildInfo.version)（\(BuildInfo.buildNumber)）")
                row("通道", "SSTP（TCP 443）")
                row("后台", SharedConfig.apiBase.replacingOccurrences(of: "https://", with: ""))
                Text("GPL-2.0-or-later · 仅限授权账号使用")
                    .font(.system(size: 11))
                    .foregroundStyle(Color.inkSecondary)
                    .padding(.top, 2)
            }
        }
    }

    private func row(_ title: String, _ value: String) -> some View {
        HStack {
            Text(title).font(.system(size: 13)).foregroundStyle(Color.inkSecondary)
            Spacer()
            Text(value)
                .font(.system(size: 13, weight: .medium))
                .foregroundStyle(Color.inkPrimary)
                .multilineTextAlignment(.trailing)
        }
    }
}

/// 连接日志：直接读扩展写下的 connection.log，末尾 400 行。
struct LogView: View {
    @State private var lines: [String] = []
    @State private var loading = false

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 4) {
                if lines.isEmpty {
                    Text(loading ? "读取中…" : "暂无日志。连接一次节点后再来看。")
                        .font(.system(size: 12))
                        .foregroundStyle(Color.inkSecondary)
                } else {
                    ForEach(Array(lines.enumerated()), id: \.offset) { _, line in
                        Text(line)
                            .font(.system(size: 11, design: .monospaced))
                            .foregroundStyle(Color.inkPrimary)
                            .textSelection(.enabled)
                            .frame(maxWidth: .infinity, alignment: .leading)
                    }
                }
            }
            .padding(16)
        }
        .background(Color.pageBackground)
        .navigationTitle("连接日志")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .navigationBarTrailing) {
                Button("刷新") { load() }
            }
        }
        .onAppear(perform: load)
    }

    private func load() {
        loading = true
        defer { loading = false }
        guard let dir = SharedConfig.engineURL else { lines = []; return }
        let url = dir.appendingPathComponent(SharedConfig.logFileName)
        guard let text = try? String(contentsOf: url, encoding: .utf8) else {
            lines = []
            return
        }
        let all = text.split(separator: "\n", omittingEmptySubsequences: true).map(String.init)
        lines = Array(all.suffix(400))
    }
}
