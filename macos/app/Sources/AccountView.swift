// SPDX-License-Identifier: GPL-2.0-or-later
// 账号：登录 / 注册 / 卡密续期 / 账号信息。
//
// 后台 1.9 起节点按「用户套餐」下发，未登录时接口只回 needLogin=true 与空列表，
// 所以这一页是连接的前置条件，不是可选项。
import SwiftUI

/// 登录 / 注册表单。首页在未登录时直接内嵌它，不必让用户先去找入口。
struct LoginForm: View {
    @EnvironmentObject private var state: AppState

    private enum Mode: String, CaseIterable, Identifiable {
        case login = "登录"
        case register = "注册"
        var id: String { rawValue }
    }

    @State private var mode: Mode = .login
    @State private var username = ""
    @State private var password = ""
    @State private var email = ""

    private var canSubmit: Bool {
        !state.working
            && !username.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            && !password.isEmpty
    }

    var body: some View {
        Card {
            VStack(alignment: .leading, spacing: 14) {
                HStack(spacing: 8) {
                    Image(systemName: "person.crop.circle")
                        .foregroundStyle(Color.brandBlue)
                    Text("登录后即可使用节点")
                        .font(.system(size: 14, weight: .semibold))
                }
                Text("节点按账号套餐下发。没有账号可以直接注册，注册后可到「我的账号」用卡密开通会员。")
                    .font(.system(size: 11))
                    .foregroundStyle(Color.inkSecondary)
                    .fixedSize(horizontal: false, vertical: true)

                Picker("", selection: $mode) {
                    ForEach(Mode.allCases) { item in
                        Text(item.rawValue).tag(item)
                    }
                }
                .pickerStyle(.segmented)
                .labelsHidden()

                VStack(alignment: .leading, spacing: 8) {
                    TextField("用户名", text: $username)
                        .textFieldStyle(.roundedBorder)
                    SecureField("密码", text: $password)
                        .textFieldStyle(.roundedBorder)
                    if mode == .register {
                        TextField("邮箱（可选，用于找回密码）", text: $email)
                            .textFieldStyle(.roundedBorder)
                    }
                }

                HStack(spacing: 10) {
                    Button {
                        submit()
                    } label: {
                        Text(mode == .login ? "登录" : "注册并登录")
                            .frame(minWidth: 96)
                    }
                    .buttonStyle(.borderedProminent)
                    .tint(Color.brandBlue)
                    .disabled(!canSubmit)

                    if state.working {
                        ProgressView().controlSize(.small)
                    }
                    Spacer()
                }

                Text(mode == .login
                     ? "忘记密码请联系管理员重置。"
                     : "用户名 3–20 位，密码至少 8 位；具体规则以服务端提示为准。")
                    .font(.system(size: 10))
                    .foregroundStyle(Color.inkSecondary)
            }
        }
    }

    private func submit() {
        // 登录成功后清掉密码，避免留在输入框里。
        let name = username
        let pass = password
        let mail = email
        let current = mode
        password = ""
        if current == .login {
            state.login(username: name, password: pass)
        } else {
            state.register(username: name, password: pass, email: mail)
        }
    }
}

/// 「我的账号」页：账号信息、卡密续期、退出登录。
struct AccountView: View {
    @EnvironmentObject private var state: AppState
    @State private var code = ""

    var body: some View {
        ScrollView {
            VStack(spacing: 16) {
                if !state.engineReady {
                    InstallCard(working: state.working) { state.installHelper() }
                }
                if state.status.needsLogin {
                    LoginForm()
                } else {
                    accountCard
                    redeemCard
                    logoutCard
                }
            }
            .padding(20)
        }
    }

    private var accountCard: some View {
        Card {
            VStack(alignment: .leading, spacing: 12) {
                HStack(spacing: 10) {
                    Image(systemName: "person.crop.circle.fill")
                        .font(.system(size: 30))
                        .foregroundStyle(Color.brandBlue)
                    VStack(alignment: .leading, spacing: 3) {
                        Text(state.account?.username ?? "已登录")
                            .font(.system(size: 16, weight: .semibold))
                        if let account = state.account, !account.summary.isEmpty {
                            Text(account.summary)
                                .font(.system(size: 11))
                                .foregroundStyle(Color.inkSecondary)
                        }
                    }
                    Spacer()
                    if let account = state.account {
                        Badge(text: account.active ? "会员有效" : "未激活",
                              color: account.active ? .brandGreen : .brandAmber)
                    }
                }

                Divider()

                infoRow("套餐", state.plan?.summary ?? "未分配套餐")
                if let account = state.account {
                    if !account.email.isEmpty { infoRow("邮箱", account.email) }
                    if !account.created.isEmpty { infoRow("注册时间", account.created) }
                    if !account.lastLogin.isEmpty { infoRow("最近登录", account.lastLogin) }
                }
            }
        }
    }

    private var redeemCard: some View {
        Card {
            VStack(alignment: .leading, spacing: 10) {
                CardTitle("卡密续期")
                Text("输入 16 位卡密为账号延长有效期，时长会累加在当前到期时间之后。")
                    .font(.system(size: 11))
                    .foregroundStyle(Color.inkSecondary)
                    .fixedSize(horizontal: false, vertical: true)
                HStack(spacing: 10) {
                    TextField("卡密", text: $code)
                        .textFieldStyle(.roundedBorder)
                        .font(.system(size: 13).monospaced())
                        .onSubmit { submit() }
                    Button("兑换") { submit() }
                        .buttonStyle(.borderedProminent)
                        .tint(Color.brandBlue)
                        .disabled(state.working || code.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                }
            }
        }
    }

    private var logoutCard: some View {
        Card {
            HStack(alignment: .center, spacing: 12) {
                VStack(alignment: .leading, spacing: 3) {
                    Text("退出登录").font(.system(size: 13, weight: .semibold))
                    Text("退出后会断开当前连接，并清除本机保存的登录状态。")
                        .font(.system(size: 11))
                        .foregroundStyle(Color.inkSecondary)
                }
                Spacer()
                Button("退出") {
                    state.logout()
                }
                .buttonStyle(.bordered)
                .disabled(state.working)
            }
        }
    }

    private func submit() {
        let value = code
        code = ""
        state.redeem(code: value)
    }

    private func infoRow(_ title: String, _ value: String) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: 10) {
            Text(title)
                .font(.system(size: 11))
                .foregroundStyle(Color.inkSecondary)
                .frame(width: 64, alignment: .leading)
            Text(value)
                .font(.system(size: 12))
                .textSelection(.enabled)
            Spacer()
        }
    }
}

/// 首页顶部的一行账号摘要，点击可跳到「我的账号」。
struct AccountStrip: View {
    @EnvironmentObject private var state: AppState

    var body: some View {
        HStack(spacing: 8) {
            Image(systemName: "person.crop.circle")
                .foregroundStyle(Color.brandBlue)
            Text(state.account?.username ?? "已登录")
                .font(.system(size: 12, weight: .medium))
            if let account = state.account, !account.summary.isEmpty {
                Text(account.summary)
                    .font(.system(size: 11))
                    .foregroundStyle(Color.inkSecondary)
            }
            Spacer()
            if let plan = state.plan, !plan.name.isEmpty {
                Badge(text: plan.name, color: .brandBlue)
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
        .background(
            RoundedRectangle(cornerRadius: 9, style: .continuous)
                .fill(Color.brandBlue.opacity(0.07))
        )
    }
}
