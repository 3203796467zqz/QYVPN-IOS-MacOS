// SPDX-License-Identifier: GPL-2.0-or-later
// QYVPNApp.swift：iOS 客户端入口。
//
// 与 macOS 版不同，这里没有「安装网络组件」这一步：iOS 的隧道权限由
// NetworkExtension 扩展在系统设置里授权，第一次连接时系统会弹出
// 「清幽加速器 想要添加 VPN 配置」，点允许即可。

import SwiftUI

@main
struct QYVPNApp: App {
    @StateObject private var model = AppModel()

    var body: some Scene {
        WindowGroup {
            RootView()
                .environmentObject(model)
                .task { await model.bootstrap() }
        }
    }
}

struct RootView: View {
    @EnvironmentObject private var model: AppModel

    var body: some View {
        ZStack(alignment: .top) {
            Color.pageBackground.ignoresSafeArea()

            switch model.session {
            case .loading:
                ProgressView().controlSize(.large)
            case .login:
                LoginView()
            case .ready:
                MainTabView()
            }

            if let banner = model.banner {
                BannerView(banner: banner)
                    .padding(.top, 8)
                    .animation(.easeInOut(duration: 0.2), value: banner.id)
            }
        }
        .animation(.easeInOut(duration: 0.2), value: model.session == .ready)
    }
}

struct MainTabView: View {
    var body: some View {
        TabView {
            HomeView()
                .tabItem { Label("首页", systemImage: "house.fill") }
            NodesView()
                .tabItem { Label("节点", systemImage: "globe.asia.australia.fill") }
            SettingsView()
                .tabItem { Label("我的", systemImage: "person.crop.circle.fill") }
        }
        .tint(Color.brandBlue)
    }
}
