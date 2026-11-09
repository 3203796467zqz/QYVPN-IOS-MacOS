// SPDX-License-Identifier: GPL-2.0-or-later
// LoginView.swift：账号登录页。
//
// 后台从 1.9 起按用户套餐下发节点，没有登录态就拿不到任何节点，所以这是
// 首次使用的第一屏。账号由管理员开通，客户端不提供注册入口。

import SwiftUI

struct LoginView: View {
    @EnvironmentObject private var model: AppModel

    @State private var username = ""
    @State private var password = ""
    @FocusState private var focus: Field?

    private enum Field { case username, password }

    private var canSubmit: Bool {
        !username.trimmingCharacters(in: .whitespaces).isEmpty
            && !password.isEmpty
            && !model.working
    }

    var body: some View {
        ScrollView {
            VStack(spacing: 22) {
                header

                Card {
                    VStack(spacing: 14) {
                        field(title: "账号", systemImage: "person") {
                            TextField("请输入账号", text: $username)
                                .textContentType(.username)
                                .textInputAutocapitalization(.never)
                                .autocorrectionDisabled()
                                .focused($focus, equals: .username)
                                .submitLabel(.next)
                                .onSubmit { focus = .password }
                        }
                        Divider()
                        field(title: "密码", systemImage: "lock") {
                            SecureField("请输入密码", text: $password)
                                .textContentType(.password)
                                .focused($focus, equals: .password)
                                .submitLabel(.go)
                                .onSubmit { submit() }
                        }
                    }
                }

                Button(action: submit) {
                    HStack(spacing: 8) {
                        if model.working { ProgressView().tint(.white) }
                        Text(model.working ? "登录中…" : "登录")
                            .font(.system(size: 16, weight: .semibold))
                    }
                    .frame(maxWidth: .infinity)
                    .padding(.vertical, 14)
                    .background(
                        RoundedRectangle(cornerRadius: 14, style: .continuous)
                            .fill(canSubmit ? Color.brandBlue : Color(.systemGray4))
                    )
                    .foregroundStyle(.white)
                }
                .disabled(!canSubmit)

                Text("账号由管理员开通，如需续费或增加设备请联系客服。")
                    .font(.system(size: 12))
                    .foregroundStyle(Color.inkSecondary)
                    .multilineTextAlignment(.center)
                    .padding(.horizontal, 30)
            }
            .padding(.horizontal, 18)
            .padding(.top, 40)
        }
        .scrollDismissesKeyboard(.interactively)
        .onAppear { focus = .username }
    }

    private var header: some View {
        VStack(spacing: 10) {
            ZStack {
                RoundedRectangle(cornerRadius: 22, style: .continuous)
                    .fill(
                        LinearGradient(colors: [Color.brandBlue, Color.brandBlue.opacity(0.72)],
                                       startPoint: .topLeading, endPoint: .bottomTrailing)
                    )
                    .frame(width: 76, height: 76)
                Image(systemName: "shield.lefthalf.filled")
                    .font(.system(size: 34, weight: .semibold))
                    .foregroundStyle(.white)
            }
            .shadow(color: Color.brandBlue.opacity(0.3), radius: 12, y: 6)

            Text("清幽加速器")
                .font(.system(size: 22, weight: .bold))
            Text("登录后使用你的专属节点")
                .font(.system(size: 13))
                .foregroundStyle(Color.inkSecondary)
        }
        .padding(.bottom, 6)
    }

    private func field<Content: View>(title: String, systemImage: String,
                                      @ViewBuilder content: () -> Content) -> some View {
        HStack(spacing: 10) {
            Image(systemName: systemImage)
                .font(.system(size: 14))
                .foregroundStyle(Color.inkSecondary)
                .frame(width: 20)
            VStack(alignment: .leading, spacing: 3) {
                Text(title).font(.system(size: 11)).foregroundStyle(Color.inkSecondary)
                content().font(.system(size: 15))
            }
        }
    }

    private func submit() {
        guard canSubmit else { return }
        focus = nil
        let name = username.trimmingCharacters(in: .whitespaces)
        Task { await model.login(username: name, password: password) }
    }
}
