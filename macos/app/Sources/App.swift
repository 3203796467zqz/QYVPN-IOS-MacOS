// SPDX-License-Identifier: GPL-2.0-or-later
// 应用入口：主窗口 + 菜单栏常驻。
import SwiftUI
import AppKit

@main
struct QingyouApp: App {
    @StateObject private var state = AppState()
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var delegate

    var body: some Scene {
        WindowGroup(id: "main") {
            RootView()
                .environmentObject(state)
                .frame(minWidth: 940, minHeight: 640)
                .onAppear { state.bootstrap() }
        }
        .defaultSize(width: 980, height: 700)
        .commands {
            CommandGroup(replacing: .newItem) {}
        }

        MenuBarExtra {
            MenuBarView()
                .environmentObject(state)
        } label: {
            Image(systemName: state.status.isConnected ? "shield.lefthalf.filled" : "shield")
        }
        .menuBarExtraStyle(.menu)
    }
}

final class AppDelegate: NSObject, NSApplicationDelegate {
    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
        // 关掉窗口后继续在菜单栏运行（与 Windows 客户端最小化到托盘一致）。
        false
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        NSApp.setActivationPolicy(.regular)
    }
}

/// 主窗口：左侧导航 + 右侧内容。
struct RootView: View {
    enum Page: String, CaseIterable, Identifiable {
        case home = "连接首页"
        case nodes = "节点列表"
        case settings = "偏好设置"
        case about = "关于"

        var id: String { rawValue }

        var symbol: String {
            switch self {
            case .home: return "house"
            case .nodes: return "globe"
            case .settings: return "gearshape"
            case .about: return "info.circle"
            }
        }
    }

    @EnvironmentObject private var state: AppState
    @State private var page: Page = .home

    var body: some View {
        NavigationSplitView {
            sidebar
                .navigationSplitViewColumnWidth(min: 176, ideal: 190, max: 220)
        } detail: {
            ZStack(alignment: .top) {
                content
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                    .background(Color(nsColor: .windowBackgroundColor))
                if let banner = state.banner {
                    BannerView(banner: banner)
                        .padding(.top, 10)
                        .transition(.move(edge: .top).combined(with: .opacity))
                }
            }
            .animation(.easeInOut(duration: 0.2), value: state.banner)
            .onReceive(NotificationCenter.default.publisher(for: NSApplication.willTerminateNotification)) { _ in
                state.disconnectOnQuit()
            }
        }
    }

    private var sidebar: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 10) {
                AppLogo(size: 30)
                VStack(alignment: .leading, spacing: 1) {
                    Text("清幽私人云").font(.system(size: 13, weight: .semibold))
                    Text(state.status.isConnected ? "节点通道已连接" : "未连接")
                        .font(.system(size: 10))
                        .foregroundStyle(state.status.isConnected ? Color.brandGreen : Color.inkSecondary)
                }
                Spacer()
            }
            .padding(.horizontal, 12)
            .padding(.top, 14)
            .padding(.bottom, 10)

            ForEach(Page.allCases) { item in
                Button {
                    page = item
                } label: {
                    HStack(spacing: 9) {
                        Image(systemName: item.symbol)
                            .font(.system(size: 13))
                            .frame(width: 18)
                        Text(item.rawValue).font(.system(size: 13))
                        Spacer()
                    }
                    .padding(.vertical, 7)
                    .padding(.horizontal, 10)
                    .background(
                        RoundedRectangle(cornerRadius: 7, style: .continuous)
                            .fill(page == item ? Color.brandBlue.opacity(0.12) : Color.clear)
                    )
                    .foregroundStyle(page == item ? Color.brandBlue : Color.inkPrimary)
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
            }
            Spacer()
            if !state.engineReady {
                Button {
                    state.installHelper()
                } label: {
                    Label("安装网络组件", systemImage: "lock.shield")
                        .font(.system(size: 11))
                }
                .buttonStyle(.plain)
                .foregroundStyle(Color.brandAmber)
                .padding(.horizontal, 12)
                .padding(.bottom, 12)
            }
        }
        .padding(.horizontal, 8)
    }

    @ViewBuilder
    private var content: some View {
        switch page {
        case .home: HomeView()
        case .nodes: NodesView()
        case .settings: SettingsView()
        case .about: AboutView()
        }
    }
}

/// 顶部提示条。
struct BannerView: View {
    let banner: Banner

    private var color: Color {
        switch banner.kind {
        case .info: return .brandBlue
        case .error: return .brandRed
        case .success: return .brandGreen
        }
    }

    var body: some View {
        HStack(spacing: 8) {
            Image(systemName: banner.kind == .error ? "exclamationmark.circle" : "checkmark.circle")
            Text(banner.text).font(.system(size: 12))
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 8)
        .background(Capsule().fill(color.opacity(0.14)))
        .foregroundStyle(color)
        .overlay(Capsule().stroke(color.opacity(0.25), lineWidth: 1))
    }
}

/// 菜单栏面板。
struct MenuBarView: View {
    @EnvironmentObject private var state: AppState
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        Text(state.status.nodeName.isEmpty ? "清幽私人云 · 未选择节点" : "\(state.status.nodeName) · \(state.status.title)")
        Text("下载 \(Format.rate(state.status.rxRate))  上传 \(Format.rate(state.status.txRate))")
        Divider()
        Button(state.status.isConnected || state.status.isBusy ? "断开连接" : "连接") {
            state.toggle()
        }
        .disabled(!state.engineReady || state.selectedNode == nil)
        Button("打开主窗口") {
            openWindow(id: "main")
            NSApp.activate(ignoringOtherApps: true)
        }
        Divider()
        Button("退出清幽私人云") {
            NSApp.terminate(nil)
        }
    }
}
