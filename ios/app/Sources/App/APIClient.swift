// SPDX-License-Identifier: GPL-2.0-or-later
// APIClient.swift：App 端访问后台的 HTTP 客户端。
//
// 隧道扩展里有 Go 内核自己的一套客户端（负责鉴权、上报流量），App 端只做
// 「登录、列节点、看用量」这三件事，所以用 URLSession 单独实现一份，不共享 Go 框架。
// 两条鉴权头缺一不可：
//   Authorization: Bearer <客户端令牌>   —— 构建期注入，证明是官方客户端
//   X-User-Token:  <用户令牌>            —— 登录后拿到，决定下发哪些节点

import Foundation

/// 构建期注入的信息。
///
/// 客户端令牌不写在源码里：build.sh 用 `QYVPN_CLIENT_TOKEN=xxx` 作为构建设置传进来，
/// Xcode 把它展开进 Info.plist 的 `QYVPNClientToken` 键，运行时从这里读。
/// 这样即使有人把仓库公开、或者翻 App 包里的字符串，也拿不到一个可用的令牌
/// （令牌只存在于你自己构建出来的 Info.plist 里）。
enum BuildInfo {
    static var clientToken: String {
        (Bundle.main.object(forInfoDictionaryKey: "QYVPNClientToken") as? String) ?? ""
    }

    static var version: String {
        (Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String) ?? "1.0.0"
    }

    static var buildNumber: String {
        (Bundle.main.object(forInfoDictionaryKey: "CFBundleVersion") as? String) ?? "1"
    }
}

/// 接口错误：message 直接取后台返回的中文提示，界面可以原样显示。
struct APIError: LocalizedError {
    let status: Int
    let message: String

    var errorDescription: String? { message }

    /// 需要重新登录（未登录 / 令牌过期 / 账号被停用）。
    var needLogin: Bool { status == 401 }

    /// 网络没通或超时，界面应当提示检查网络而不是「密码错误」。
    var isNetwork: Bool { status == 0 }
}

final class APIClient {
    static let shared = APIClient()

    private let base: URL
    private let session: URLSession
    private let decoder = JSONDecoder()

    /// 用户令牌。登录成功或从 Keychain 恢复后设置。
    var userToken: String?

    init(base: String = SharedConfig.apiBase) {
        self.base = URL(string: base)!
        let config = URLSessionConfiguration.ephemeral
        config.timeoutIntervalForRequest = 20
        config.timeoutIntervalForResource = 30
        // 连接后台永远走物理网络：iOS 上由扩展排除路由保证，这里不需要额外处理。
        config.requestCachePolicy = .reloadIgnoringLocalCacheData
        self.session = URLSession(configuration: config)
    }

    // MARK: - 账号

    func login(username: String, password: String) async throws -> LoginReply {
        let reply: LoginReply = try await post("/api/app/user/login", body: [
            "username": username,
            "password": password,
            "platform": SharedConfig.platform,
        ])
        guard reply.token.count >= 16 else {
            throw APIError(status: 0, message: "登录响应异常，请稍后再试")
        }
        userToken = reply.token
        return reply
    }

    /// 退出登录。后台吊销失败也要清掉本地令牌，否则用户退不出去。
    func logout() async {
        guard userToken != nil else { return }
        do {
            let _: EmptyReply = try await post("/api/app/user/logout", body: [String: String]())
        } catch {
            // 忽略：本地令牌在调用方统一清理。
        }
        userToken = nil
    }

    func me() async throws -> Account {
        let reply: MeReply = try await get("/api/app/user/me")
        guard let user = reply.user else {
            throw APIError(status: 401, message: "登录已失效，请重新登录")
        }
        return user
    }

    // MARK: - 节点

    func config() async throws -> ConfigResponse {
        try await get("/api/app/config")
    }

    func status() async throws -> StatusResponse {
        try await get("/api/app/status")
    }

    /// 查询 iOS 内测版本。后台未配置时返回 404，这里归一化成 nil。
    func iosRelease() async throws -> IOSRelease? {
        do {
            let reply: IOSReleaseReply = try await get("/api/app/ios-release")
            guard let release = reply.release, !release.version.isEmpty else { return nil }
            return release
        } catch let error as APIError where error.status == 404 {
            return nil
        }
    }

    /// 测速：对节点做一次 TCP 443 握手，返回毫秒数。失败返回 nil，不影响列表展示。
    func probe(host: String, port: Int, timeout: TimeInterval = 3) async -> Int? {
        await withCheckedContinuation { continuation in
            let queue = DispatchQueue(label: "cn.qo8.qyvpn.probe")
            queue.async {
                let start = Date()
                let fd = socket(AF_INET, SOCK_STREAM, 0)
                guard fd >= 0 else {
                    continuation.resume(returning: nil)
                    return
                }
                defer { close(fd) }
                var tv = timeval(tv_sec: Int(timeout), tv_usec: 0)
                setsockopt(fd, SOL_SOCKET, SO_SNDTIMEO, &tv, socklen_t(MemoryLayout<timeval>.size))
                var addr = sockaddr_in()
                addr.sin_family = sa_family_t(AF_INET)
                addr.sin_port = in_port_t(UInt16(port).bigEndian)
                guard inet_pton(AF_INET, host, &addr.sin_addr) == 1 else {
                    // 域名交给 DNS 解析后再连，避免在扩展外做额外解析。
                    guard let resolved = APIClient.resolveIPv4(host) else {
                        continuation.resume(returning: nil)
                        return
                    }
                    guard inet_pton(AF_INET, resolved, &addr.sin_addr) == 1 else {
                        continuation.resume(returning: nil)
                        return
                    }
                    APIClient.connect(fd: fd, addr: &addr, start: start, continuation: continuation)
                    return
                }
                APIClient.connect(fd: fd, addr: &addr, start: start, continuation: continuation)
            }
        }
    }

    private static func connect(fd: Int32, addr: inout sockaddr_in, start: Date,
                                continuation: CheckedContinuation<Int?, Never>) {
        let result = withUnsafePointer(to: &addr) { pointer in
            pointer.withMemoryRebound(to: sockaddr.self, capacity: 1) { sockaddrPointer in
                Darwin.connect(fd, sockaddrPointer, socklen_t(MemoryLayout<sockaddr_in>.size))
            }
        }
        if result == 0 {
            let ms = Int(Date().timeIntervalSince(start) * 1000)
            continuation.resume(returning: ms)
        } else {
            continuation.resume(returning: nil)
        }
    }

    /// 简易 IPv4 解析（只用于测速，失败就算了）。
    static func resolveIPv4(_ host: String) -> String? {
        var hints = addrinfo(ai_flags: 0, ai_family: AF_INET, ai_socktype: SOCK_STREAM,
                             ai_protocol: 0, ai_addrlen: 0, ai_canonname: nil,
                             ai_addr: nil, ai_next: nil)
        var info: UnsafeMutablePointer<addrinfo>?
        guard getaddrinfo(host, nil, &hints, &info) == 0, let first = info else { return nil }
        defer { freeaddrinfo(info) }
        var buffer = [CChar](repeating: 0, count: Int(INET_ADDRSTRLEN))
        var addr = first.pointee.ai_addr.withMemoryRebound(to: sockaddr_in.self, capacity: 1) { $0.pointee }
        guard inet_ntop(AF_INET, &addr.sin_addr, &buffer, socklen_t(INET_ADDRSTRLEN)) != nil else {
            return nil
        }
        return String(cString: buffer)
    }

    // MARK: - 请求

    private func get<T: Decodable>(_ path: String) async throws -> T {
        try await request(path: path, method: "GET", body: nil)
    }

    private func post<T: Decodable, B: Encodable>(_ path: String, body: B) async throws -> T {
        let data = try JSONEncoder().encode(body)
        return try await request(path: path, method: "POST", body: data)
    }

    private func request<T: Decodable>(path: String, method: String, body: Data?) async throws -> T {
        guard !BuildInfo.clientToken.isEmpty else {
            throw APIError(status: 0, message: "客户端未正确打包（缺少访问令牌），请使用官方安装包")
        }
        guard let url = URL(string: base.absoluteString + path) else {
            throw APIError(status: 0, message: "请求地址无效")
        }
        var request = URLRequest(url: url)
        request.httpMethod = method
        request.setValue("Bearer \(BuildInfo.clientToken)", forHTTPHeaderField: "Authorization")
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        request.setValue("Qingyou-iOS/\(BuildInfo.version)", forHTTPHeaderField: "User-Agent")
        if let token = userToken, !token.isEmpty {
            request.setValue(token, forHTTPHeaderField: "X-User-Token")
        }
        if let body {
            request.httpBody = body
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }

        let data: Data
        let response: URLResponse
        do {
            (data, response) = try await session.data(for: request)
        } catch {
            throw APIError(status: 0, message: "网络连接失败，请检查网络后重试")
        }
        let status = (response as? HTTPURLResponse)?.statusCode ?? 0
        guard (200..<300).contains(status) else {
            throw APIError(status: status, message: APIClient.errorMessage(data, status: status))
        }
        do {
            return try decoder.decode(T.self, from: data)
        } catch {
            throw APIError(status: status, message: "后台返回的数据无法解析，请更新客户端")
        }
    }

    private static func errorMessage(_ data: Data, status: Int) -> String {
        if let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
           let message = obj["error"] as? String, !message.isEmpty {
            return message
        }
        switch status {
        case 401: return "登录已失效，请重新登录"
        case 429: return "操作太频繁，请稍后再试"
        default: return "后台请求失败（HTTP \(status)）"
        }
    }
}

private struct EmptyReply: Codable {}
private struct MeReply: Codable { var user: Account? }
