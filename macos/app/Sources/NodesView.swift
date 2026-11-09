// SPDX-License-Identifier: GPL-2.0-or-later
// 节点列表：两列卡片、进入即自动测速、可用/全部筛选。
import SwiftUI

struct NodesView: View {
    @EnvironmentObject private var state: AppState
    @State private var filter = 0
    @State private var didProbe = false

    private var filtered: [NodeView] {
        filter == 1 ? state.nodes.filter { $0.available && $0.block.isEmpty } : state.nodes
    }

    private let columns = [GridItem(.adaptive(minimum: 260), spacing: 12)]

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider().opacity(0.5)
            if state.nodes.isEmpty {
                emptyState
            } else {
                ScrollView {
                    LazyVGrid(columns: columns, spacing: 12) {
                        ForEach(filtered) { node in
                            NodeCard(
                                node: node,
                                selected: node.id == state.settings.selectedNode,
                                probe: state.probes[node.id],
                                onSelect: { state.select(node: node) },
                                onProbe: { state.probe() }
                            )
                        }
                    }
                    .padding(20)
                }
            }
        }
        .onAppear {
            if !didProbe {
                didProbe = true
                state.probe()
            }
        }
    }

    private var header: some View {
        HStack(spacing: 12) {
            Picker("", selection: $filter) {
                Text("全部节点").tag(0)
                Text("可用节点").tag(1)
            }
            .pickerStyle(.segmented)
            .frame(width: 200)

            Spacer()

            Text("\(state.nodes.count) 台节点")
                .font(.system(size: 11))
                .foregroundStyle(Color.inkSecondary)

            Button {
                state.probe()
            } label: {
                Label("测速", systemImage: "speedometer")
            }
            .buttonStyle(.borderless)

            Button {
                state.refreshNodes()
            } label: {
                Label("刷新", systemImage: "arrow.clockwise")
            }
            .buttonStyle(.borderless)
        }
        .padding(.horizontal, 20)
        .padding(.vertical, 12)
    }

    private var emptyState: some View {
        VStack(spacing: 12) {
            Spacer()
            Image(systemName: "globe.asia.australia")
                .font(.system(size: 40))
                .foregroundStyle(Color.inkSecondary)
            Text(state.engineReady ? "还没有可用节点" : "连接内核尚未就绪")
                .font(.system(size: 14, weight: .medium))
            Text(state.engineReady ? "请检查网络后刷新节点列表" : "请先在首页安装网络组件")
                .font(.system(size: 12))
                .foregroundStyle(Color.inkSecondary)
            if state.engineReady {
                Button("刷新节点") { state.refreshNodes() }
                    .buttonStyle(.borderedProminent)
                    .tint(Color.brandBlue)
            } else {
                Button("安装网络组件") { state.installHelper() }
                    .buttonStyle(.borderedProminent)
                    .tint(Color.brandBlue)
            }
            Spacer()
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}

struct NodeCard: View {
    let node: NodeView
    let selected: Bool
    let probe: ProbeResult?
    let onSelect: () -> Void
    let onProbe: () -> Void

    private var latencyColor: Color {
        guard let probe, probe.error.isEmpty else { return .brandRed }
        if probe.ms <= 250 { return .brandGreen }
        if probe.ms <= 400 { return .brandAmber }
        return .brandRed
    }

    private var latencyText: String {
        guard let probe else { return "测速中…" }
        if !probe.error.isEmpty { return probe.error }
        return "\(probe.ms) ms"
    }

    var body: some View {
        Button(action: onSelect) {
            VStack(alignment: .leading, spacing: 8) {
                HStack(spacing: 10) {
                    NodeAvatar(icon: node.icon, name: node.name, size: 32)
                    VStack(alignment: .leading, spacing: 2) {
                        Text(node.name)
                            .font(.system(size: 14, weight: .semibold))
                            .lineLimit(1)
                            .truncationMode(.tail)
                        Text(node.host)
                            .font(.system(size: 10))
                            .foregroundStyle(Color.inkSecondary)
                            .lineLimit(1)
                    }
                    Spacer(minLength: 6)
                    if selected {
                        Image(systemName: "checkmark.circle.fill")
                            .foregroundStyle(Color.brandBlue)
                    }
                }

                HStack(spacing: 8) {
                    Text(latencyText)
                        .font(.system(size: 11, weight: .medium).monospacedDigit())
                        .foregroundStyle(latencyColor)
                    Spacer()
                    Text(node.block.isEmpty ? node.loadLabel : node.block)
                        .font(.system(size: 10))
                        .foregroundStyle(node.block.isEmpty ? Color.inkSecondary : Color.brandRed)
                        .lineLimit(1)
                }

                HStack {
                    Text(node.usedText)
                        .font(.system(size: 10))
                        .foregroundStyle(Color.inkSecondary)
                        .lineLimit(1)
                    Spacer()
                    Button {
                        onProbe()
                    } label: {
                        Image(systemName: "arrow.triangle.2.circlepath")
                            .font(.system(size: 10))
                    }
                    .buttonStyle(.plain)
                    .foregroundStyle(Color.inkSecondary)
                    .help("重新测试延迟")
                }
            }
            .padding(12)
            .frame(maxWidth: .infinity, minHeight: 92, alignment: .topLeading)
            .background(
                RoundedRectangle(cornerRadius: 12, style: .continuous)
                    .fill(Color.cardBackground)
            )
            .overlay(
                RoundedRectangle(cornerRadius: 12, style: .continuous)
                    .stroke(selected ? Color.brandBlue : Color.primary.opacity(0.07),
                            lineWidth: selected ? 2 : 1)
            )
        }
        .buttonStyle(.plain)
        .help(node.block.isEmpty ? "点击选择 \(node.name)" : node.block)
    }
}
