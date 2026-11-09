// SPDX-License-Identifier: GPL-2.0-or-later
// NodesView.swift：节点列表。
//
// 节点由后台按账号套餐下发，客户端不能自建：列表里出现的每一台都是这台账号
// 有权使用的。流量用完的节点后台会直接不下发，或标记 exhausted 后置灰。

import SwiftUI

struct NodesView: View {
    @EnvironmentObject private var model: AppModel
    @Environment(\.dismiss) private var dismiss
    var embedded = false

    var body: some View {
        ScrollView {
            LazyVStack(spacing: 10) {
                if model.nodes.isEmpty {
                    emptyState
                } else {
                    ForEach(model.nodes) { node in
                        nodeRow(node)
                    }
                    Text("共 \(model.nodes.count) 个节点 · 由后台按套餐下发")
                        .font(.system(size: 11))
                        .foregroundStyle(Color.inkSecondary)
                        .padding(.top, 6)
                }
            }
            .padding(.horizontal, 18)
            .padding(.vertical, 12)
        }
        .background(Color.pageBackground)
        .navigationTitle("节点")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .navigationBarTrailing) {
                Button {
                    Task { await model.probeAll() }
                } label: {
                    if model.probing {
                        ProgressView()
                    } else {
                        Text("测速")
                    }
                }
                .disabled(model.probing || model.nodes.isEmpty)
            }
            if embedded {
                ToolbarItem(placement: .navigationBarLeading) {
                    Button("关闭") { dismiss() }
                }
            }
        }
        .refreshable { await model.refresh() }
    }

    private func nodeRow(_ node: NodeView) -> some View {
        let selected = node.id == model.selectedNodeID
        let blocked = node.exhausted
        return Button {
            guard !blocked else {
                model.show("该节点本月流量已用完", .error)
                return
            }
            model.select(node: node)
            if embedded { dismiss() }
        } label: {
            Card(padding: 14) {
                HStack(spacing: 12) {
                    ZStack {
                        Circle()
                            .fill(selected ? Color.brandBlue : Color.brandBlue.opacity(0.08))
                            .frame(width: 34, height: 34)
                        Image(systemName: "globe.asia.australia.fill")
                            .font(.system(size: 14))
                            .foregroundStyle(selected ? .white : Color.brandBlue)
                    }
                    VStack(alignment: .leading, spacing: 3) {
                        HStack(spacing: 6) {
                            Text(node.name)
                                .font(.system(size: 15, weight: .semibold))
                                .foregroundStyle(blocked ? Color.inkSecondary : Color.inkPrimary)
                            if blocked {
                                Badge(text: "流量已用完", color: .brandRed)
                            }
                        }
                        Text("\(node.host):\(node.port)")
                            .font(.system(size: 11).monospaced())
                            .foregroundStyle(Color.inkSecondary)
                        Text(node.quotaText ?? node.peopleText)
                            .font(.system(size: 11))
                            .foregroundStyle(node.exhausted ? Color.brandRed : Color.inkSecondary)
                    }
                    Spacer()
                    VStack(alignment: .trailing, spacing: 4) {
                        if let ms = model.probes[node.id] {
                            Text("\(ms) ms")
                                .font(.system(size: 12, weight: .medium).monospacedDigit())
                                .foregroundStyle(pingColor(ms))
                        }
                        if selected {
                            Image(systemName: "checkmark.circle.fill")
                                .font(.system(size: 16))
                                .foregroundStyle(Color.brandBlue)
                        }
                    }
                }
            }
        }
        .buttonStyle(.plain)
        .opacity(blocked ? 0.6 : 1)
    }

    private func pingColor(_ ms: Int) -> Color {
        switch ms {
        case ..<120: return .brandGreen
        case ..<300: return .brandAmber
        default: return .brandRed
        }
    }

    private var emptyState: some View {
        Card {
            VStack(alignment: .leading, spacing: 8) {
                Text("当前账号没有可用节点").font(.system(size: 15, weight: .semibold))
                Text("可能是套餐还没分配节点，或者节点的本月流量已全部用完。下拉刷新试试，仍然为空请联系客服。")
                    .font(.system(size: 12))
                    .foregroundStyle(Color.inkSecondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .padding(.top, 20)
    }
}
