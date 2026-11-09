// SPDX-License-Identifier: GPL-2.0-or-later
// Keychain.swift：保存用户令牌。
//
// 令牌放 Keychain 而不是 UserDefaults：它是账号的唯一凭据，泄露等于账号被拿走。
// 扩展进程拿不到这条 Keychain 记录（两个 App 的访问组不同），所以启动隧道时
// 由 App 把令牌放进 providerConfiguration 交给扩展，扩展自己再存一份到 App Group。

import Foundation
import Security

enum Keychain {
    private static let service = "cn.qo8.qyvpn.client"
    private static let account = "user-token"

    static func save(_ token: String) {
        guard !token.isEmpty else { return }
        delete()
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
            kSecValueData as String: Data(token.utf8),
            kSecAttrAccessible as String: kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly,
        ]
        SecItemAdd(query as CFDictionary, nil)
    }

    static func load() -> String? {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
            kSecReturnData as String: true,
            kSecMatchLimit as String: kSecMatchLimitOne,
        ]
        var item: CFTypeRef?
        guard SecItemCopyMatching(query as CFDictionary, &item) == errSecSuccess,
              let data = item as? Data,
              let token = String(data: data, encoding: .utf8),
              !token.isEmpty else {
            return nil
        }
        return token
    }

    static func delete() {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
        ]
        SecItemDelete(query as CFDictionary)
    }
}
