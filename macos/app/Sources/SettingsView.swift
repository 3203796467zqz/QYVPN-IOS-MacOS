// SPDX-License-Identifier: GPL-2.0-or-later
// 偏好设置：域名直连与网络组件。
import SwiftUI

struct SettingsView: View {
    @EnvironmentObject private var state: AppState
    @State private var draft = EngineSettings()
    @State private var loaded = false

    var body: some View {
        ScrollView {
            VStack(spacing: 16) {
                Card {
                    VStack(alignment: .leading, spacing: 14) {
                        CardTitle("连接偏好")
                        Toggle(isOn: $draft.autoConnect) {
                            VStack(alignment: .leading, spacing: 2) {
                                Text("开机后自动连接").font(.system(size: 13))
                                Text("启动客户端时自动连接上次使用的节点").font(.system(size: 11)).foregroundStyle(Color.inkSecondary)
                            }
                        }
                        .toggleStyle(.switch)
                    }
                }

                Card {
                    VStack(alignment: .leading, spacing: 14) {
                        CardTitle("域名直连")
                        Toggle(isOn: $draft.bypassEnabled) {
                            VStack(alignment: .leading, spacing: 2) {
                                Text("启用域名直连").font(.system(size: 13))
                                Text("命中的地址不经过节点通道，也不计入节点流量；下次连接生效").font(.system(size: 11)).foregroundStyle(Color.inkSecondary)
                            }
                        }
                        .toggleStyle(.switch)

                        Divider().opacity(0.5)

                        Toggle(isOn: $draft.bypassChina) {
                            VStack(alignment: .leading, spacing: 2) {
                                Text("国内地址直连").font(.system(size: 13))
                                Text("内置国内网段表，访问国内网站走本地宽带").font(.system(size: 11)).foregroundStyle(Color.inkSecondary)
                            }
                        }
                        .toggleStyle(.switch)
                        .disabled(!draft.bypassEnabled)

                        VStack(alignment: .leading, spacing: 6) {
                            Text("自定义域名（每行一个，最多 200 条）").font(.system(size: 12))
                            TextEditor(text: $draft.bypassDomains)
                                .font(.system(size: 12, design: .monospaced))
                                .frame(minHeight: 110)
                                .padding(6)
                                .background(RoundedRectangle(cornerRadius: 8).fill(Color.primary.opacity(0.04)))
                                .disabled(!draft.bypassEnabled)
                            Text("连接时按域名解析出的 IPv4 地址安装直连路由；域名很多或使用 CDN 的站点可能只覆盖部分地址。")
                                .font(.system(size: 10))
                                .foregroundStyle(Color.inkSecondary)
                        }
                    }
                }

                Card {
                    VStack(alignment: .leading, spacing: 12) {
                        CardTitle("网络组件")
                        HStack(spacing: 10) {
                            Image(systemName: state.engineReady ? "checkmark.seal.fill" : "exclamationmark.triangle.fill")
                                .foregroundStyle(state.engineReady ? Color.brandGreen : Color.brandAmber)
                            Text(state.engineReady ? "连接内核运行中（特权组件已安装）" : (state.installed ? "已安装但内核未响应" : "尚未安装"))
                                .font(.system(size: 12))
                            Spacer()
                            Button("重新安装") { state.installHelper() }
                                .buttonStyle(.borderless)
                            Button("卸载") { state.uninstallHelper() }
                                .buttonStyle(.borderless)
                                .foregroundStyle(Color.brandRed)
                        }
                        Text("组件安装在 /Library/PrivilegedHelperTools，由 launchd 以 root 运行；卸载会一并删除。")
                            .font(.system(size: 10))
                            .foregroundStyle(Color.inkSecondary)
                    }
                }

                HStack {
                    Spacer()
                    Button("保存设置") {
                        var snapshot = draft
                        if snapshot.selectedNode.isEmpty {
                            snapshot.selectedNode = state.settings.selectedNode
                        }
                        state.applySettings(snapshot)
                        state.saveSettings()
                        state.show("设置已保存，下次连接生效", .success)
                    }
                    .buttonStyle(.borderedProminent)
                    .tint(Color.brandBlue)
                }
            }
            .padding(20)
        }
        .onAppear {
            if !loaded {
                draft = state.settings
                loaded = true
            }
        }
        .onChange(of: state.settings) { newValue in
            if !loaded { draft = newValue; loaded = true }
        }
    }
}
