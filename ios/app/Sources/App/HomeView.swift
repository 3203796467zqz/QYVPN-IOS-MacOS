// SPDX-License-Identifier: GPL-2.0-or-later
// HomeView.swift：首页——账号、连接按钮、速率、当前节点、本月流量。
//
// 数据来源有两处，界面上不区分：
//   model（App 进程）—— 账号、节点列表、额度，来自后台 HTTP
//   model.tunnel（扩展进程）—— 已连接状态、实时速率、本月已用，来自 status.json

import SwiftUI

struct HomeView: View {
    @EnvironmentObject private var model: AppModel
    @State private var showNodes = false
    @Environment(\.scenePhase) private var scenePhase

    private var engine: EngineStatus { model.tunnel.engine }

    private var stateText: String {
        if model.tunnel.isActive || engine.state != nil {
            return engine.state ?? "idle"
        }
        return "idle"
    }

    private var statusLine: String {
        if let error = engine.error, !error.isEmpty, !model.tunnel.isActive {
            return error
        }
        if model.tunnel.isActive { return model.tunnel.statusText }
        return engine.displayText
    }

    private var usedBytes: Int64 {
        if let used = engine.monthUsed, used > 0 { return used }
        return model.selectedNode?.usage?.usedBytes ?? 0
    }

    private var limitBytes: Int64 {
        if let limit = engine.monthLimit, limit > 0 { return limit }
        if let limit = model.selectedNode?.usage?.limitBytes, limit > 0 { return limit }
        // 套餐按节点配额度时后台给的是 GB，换算成字节统一计算。
        if let gb = model.selectedNode?.quotaGB, gb > 0 {
            return Int64(gb * 1024 * 1024 * 1024)
        }
        return 0
    }

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(spacing: 16) {
                    accountRow
                    if model.updateAvailable { updateRow }

                    VStack(spacing: 12) {
                        PowerButton(state: stateText, enabled: !model.working) {
                            Task { await model.toggle() }
                        }
                        .padding(.top, 6)

                        Text(statusLine)
                            .font(.system(size: 14, weight: .medium))
                            .foregroundStyle(statusColor)
                            .multilineTextAlignment(.center)
                            .frame(maxWidth: .infinity)
                            .padding(.horizontal, 24)

                        if model.tunnel.isActive, let duration = engine.duration, duration > 0 {
                            Text("已连接 \(Format.duration(duration))")
                                .font(.system(size: 12).monospacedDigit())
                                .foregroundStyle(Color.inkSecondary)
                        }
                    }
                    .padding(.vertical, 8)

                    HStack(spacing: 10) {
                        RateTile(title: "下载", value: Format.rate(model.currentSpeed.rx),
                                 systemImage: "arrow.down")
                        RateTile(title: "上传", value: Format.rate(model.currentSpeed.tx),
                                 systemImage: "arrow.up")
                    }

                    nodeCard
                    TrafficCard(used: usedBytes, limit: limitBytes,
                                planName: model.plan?.name,
                                nodeText: model.selectedNode?.peopleText ?? "尚未选择节点")
                }
                .padding(.horizontal, 18)
                .padding(.bottom, 24)
            }
            .background(Color.pageBackground)
            .navigationTitle("清幽加速器")
            .navigationBarTitleDisplayMode(.inline)
            .refreshable { await model.refresh() }
            .sheet(isPresented: $showNodes) {
                NodesView(embedded: true)
            }
        }
        .onChange(of: scenePhase) { phase in
            // 从后台回来时刷新一次：套餐或节点可能已被管理员改动。
            if phase == .active { Task { await model.refresh(quiet: true) } }
        }
    }

    private var statusColor: Color {
        if model.tunnel.isActive { return .brandGreen }
        if engine.isBusy { return .brandBlue }
        if let error = engine.error, !error.isEmpty { return .brandRed }
        return .inkSecondary
    }

    private var accountRow: some View {
        HStack(spacing: 10) {
            ZStack {
                Circle().fill(Color.brandBlue.opacity(0.12)).frame(width: 36, height: 36)
                Image(systemName: "person.fill")
                    .font(.system(size: 15))
                    .foregroundStyle(Color.brandBlue)
            }
            VStack(alignment: .leading, spacing: 2) {
                Text(model.account?.username ?? "已登录")
                    .font(.system(size: 14, weight: .semibold))
                Text(model.account?.summary ?? "账号信息同步中")
                    .font(.system(size: 11))
                    .foregroundStyle(Color.inkSecondary)
            }
            Spacer()
            if let days = model.account?.daysLeft, days <= 7, days >= 0 {
                Badge(text: days == 0 ? "今日到期" : "\(days) 天后到期", color: .brandAmber)
            }
        }
        .padding(.top, 6)
    }

    private var updateRow: some View {
        Button {
            model.openTestFlight()
        } label: {
            HStack(spacing: 10) {
                Image(systemName: "arrow.down.circle.fill")
                    .foregroundStyle(Color.brandBlue)
                VStack(alignment: .leading, spacing: 2) {
                    Text("发现新版本 \(model.updateRelease?.version ?? "")")
                        .font(.system(size: 13, weight: .semibold))
                        .foregroundStyle(Color.inkPrimary)
                    Text("前往 TestFlight 更新")
                        .font(.system(size: 11))
                        .foregroundStyle(Color.inkSecondary)
                }
                Spacer()
                Image(systemName: "chevron.right")
                    .font(.system(size: 12, weight: .semibold))
                    .foregroundStyle(Color.inkSecondary)
            }
            .padding(12)
            .background(
                RoundedRectangle(cornerRadius: 14, style: .continuous)
                    .fill(Color.brandBlue.opacity(0.08))
            )
        }
        .buttonStyle(.plain)
    }

    private var nodeCard: some View {
        Card {
            VStack(alignment: .leading, spacing: 10) {
                CardTitle("当前节点", trailing: model.selectedNode?.host)
                HStack(alignment: .center, spacing: 10) {
                    VStack(alignment: .leading, spacing: 3) {
                        Text(model.selectedNode?.name ?? "还没有可用节点")
                            .font(.system(size: 16, weight: .semibold))
                        Text(model.selectedNode?.quotaText ?? model.selectedNode?.peopleText ?? "")
                            .font(.system(size: 11))
                            .foregroundStyle(Color.inkSecondary)
                    }
                    Spacer()
                    Button {
                        showNodes = true
                    } label: {
                        Text("切换")
                            .font(.system(size: 13, weight: .medium))
                            .padding(.horizontal, 14)
                            .padding(.vertical, 7)
                            .background(Capsule().fill(Color.brandBlue.opacity(0.1)))
                            .foregroundStyle(Color.brandBlue)
                    }
                    .buttonStyle(.plain)
                }
                if let ip = engine.tunnelIp, !ip.isEmpty, model.tunnel.isActive {
                    Divider()
                    HStack(spacing: 14) {
                        Label(ip, systemImage: "network")
                        if let dns = engine.dns, !dns.isEmpty {
                            Label(dns, systemImage: "globe")
                        }
                    }
                    .font(.system(size: 11))
                    .foregroundStyle(Color.inkSecondary)
                    .lineLimit(1)
                }
            }
        }
    }
}
