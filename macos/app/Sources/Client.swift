// SPDX-License-Identifier: GPL-2.0-or-later
// 与连接内核（特权组件）通信的本地客户端。
import Foundation
import Darwin
import Security

struct EngineError: LocalizedError {
    let message: String
    var errorDescription: String? { message }
}

/// 通过 unix domain socket 调用内核，一次请求一行 JSON。
struct EngineClient {
    /// 与 Go 端 paths.SocketPath() 保持一致。
    static let socketPath = "/var/run/cn.qo8.qyvpn.engine.sock"
    let token: String

    func call(_ cmd: String, _ params: [String: Any] = [:]) throws -> EngineResponse {
        var body = params
        body["cmd"] = cmd
        body["token"] = token
        let payload = try JSONSerialization.data(withJSONObject: body)
        let reply = try transact(payload + Data([0x0A]))
        let response = try JSONDecoder().decode(EngineResponse.self, from: reply)
        if !response.ok {
            throw EngineError(message: response.error.isEmpty ? "内核返回失败" : response.error)
        }
        return response
    }

    private func transact(_ payload: Data) throws -> Data {
        let fd = socket(AF_UNIX, SOCK_STREAM, 0)
        guard fd >= 0 else { throw EngineError(message: "无法创建本地连接") }
        defer { close(fd) }

        var one: Int32 = 1
        setsockopt(fd, SOL_SOCKET, SO_NOSIGPIPE, &one, socklen_t(MemoryLayout<Int32>.size))
        var tv = timeval(tv_sec: 15, tv_usec: 0)
        setsockopt(fd, SOL_SOCKET, SO_RCVTIMEO, &tv, socklen_t(MemoryLayout<timeval>.size))
        setsockopt(fd, SOL_SOCKET, SO_SNDTIMEO, &tv, socklen_t(MemoryLayout<timeval>.size))

        var addr = sockaddr_un()
        addr.sun_family = sa_family_t(AF_UNIX)
        let pathBytes = Array(EngineClient.socketPath.utf8)
        let capacity = MemoryLayout.size(ofValue: addr.sun_path)
        guard pathBytes.count < capacity else { throw EngineError(message: "socket 路径过长") }
        withUnsafeMutablePointer(to: &addr.sun_path) { pointer in
            pointer.withMemoryRebound(to: CChar.self, capacity: capacity) { dst in
                for (index, byte) in pathBytes.enumerated() {
                    dst[index] = CChar(bitPattern: byte)
                }
                dst[pathBytes.count] = 0
            }
        }
        let length = socklen_t(MemoryLayout<sockaddr_un>.size)
        let result = withUnsafePointer(to: &addr) { pointer -> Int32 in
            pointer.withMemoryRebound(to: sockaddr.self, capacity: 1) { sa in
                connect(fd, sa, length)
            }
        }
        guard result == 0 else {
            throw EngineError(message: "内核未运行（请先安装网络组件）")
        }

        try payload.withUnsafeBytes { (raw: UnsafeRawBufferPointer) throws -> Void in
            guard let base = raw.baseAddress else { return }
            var sent = 0
            while sent < raw.count {
                let n = send(fd, base.advanced(by: sent), raw.count - sent, 0)
                if n <= 0 { throw EngineError(message: "写入内核失败") }
                sent += n
            }
        }

        var buffer = Data()
        var chunk = [UInt8](repeating: 0, count: 8192)
        while true {
            let n = chunk.withUnsafeMutableBytes { raw -> Int in
                read(fd, raw.baseAddress, raw.count)
            }
            if n <= 0 { break }
            buffer.append(contentsOf: chunk[0..<n])
            if buffer.contains(0x0A) { break }
            if buffer.count > 4 * 1024 * 1024 { throw EngineError(message: "内核响应过大") }
        }
        guard !buffer.isEmpty else { throw EngineError(message: "内核没有响应") }
        return buffer
    }
}

/// 本地令牌：安装时生成，界面保有一份，内核保有一份（root 只读）。
enum TokenStore {
    static var directory: URL {
        let base = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first
            ?? URL(fileURLWithPath: NSHomeDirectory())
        return base.appendingPathComponent("QingyouPrivateCloud", isDirectory: true)
    }

    static var fileURL: URL { directory.appendingPathComponent("ipc.token") }

    /// 读取已有令牌，没有则生成一个新的并落盘（0600）。
    static func loadOrCreate() throws -> String {
        if let data = try? Data(contentsOf: fileURL),
           let text = String(data: data, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines),
           text.count >= 32 {
            return text
        }
        var bytes = [UInt8](repeating: 0, count: 32)
        guard SecRandomCopyBytes(kSecRandomDefault, bytes.count, &bytes) == errSecSuccess else {
            throw EngineError(message: "无法生成随机令牌")
        }
        let token = bytes.map { String(format: "%02x", $0) }.joined()
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        try token.write(to: fileURL, atomically: true, encoding: .utf8)
        try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: fileURL.path)
        return token
    }

    /// 该文件在安装时被复制给内核使用（root 只读取一份）。
    static var installedTokenURL: URL {
        URL(fileURLWithPath: "/Library/Application Support/QingyouPrivateCloud/ipc.token")
    }
}

/// 特权组件的安装/卸载（一次管理员授权）。
enum HelperInstaller {
    static let label = "cn.qo8.qyvpn.engine"
    static let binaryPath = "/Library/PrivilegedHelperTools/cn.qo8.qyvpn.engine"
    static let plistPath = "/Library/LaunchDaemons/cn.qo8.qyvpn.engine.plist"

    /// 内核可执行文件位于 app 包的 Resources 下。
    static func engineURL() -> URL? {
        if let url = Bundle.main.url(forResource: "qyvpn-engine", withExtension: nil) {
            return url
        }
        // 直接 `swift run` 调试时的兜底路径。
        let fallback = Bundle.main.bundleURL
            .deletingLastPathComponent()
            .appendingPathComponent("qyvpn-engine")
        return FileManager.default.fileExists(atPath: fallback.path) ? fallback : nil
    }

    static var isInstalled: Bool {
        FileManager.default.fileExists(atPath: binaryPath)
    }

    /// 安装：复制内核到特权目录、写入 launchd 描述、注册服务。
    static func install(token: String) throws {
        guard let engine = engineURL() else {
            throw EngineError(message: "安装包缺少连接内核（qyvpn-engine）")
        }
        let shell = [
            "/usr/bin/install -d -m 0755 /Library/PrivilegedHelperTools",
            "\(shellQuote(engine.path)) install --token \(shellQuote(token))"
        ].joined(separator: " && ")
        _ = try runAsAdmin(shell)
    }

    static func uninstall() throws {
        let shell = "\(shellQuote(binaryPath)) uninstall"
        _ = try runAsAdmin(shell)
    }

    /// 用一次管理员授权执行命令；用户取消时抛出可读错误。
    @discardableResult
    private static func runAsAdmin(_ shell: String) throws -> String {
        let script = "do shell script \(appleScriptQuote(shell)) with administrator privileges"
        let process = Process()
        process.executableURL = URL(fileURLWithPath: "/usr/bin/osascript")
        process.arguments = ["-e", script]
        let out = Pipe()
        let err = Pipe()
        process.standardOutput = out
        process.standardError = err
        try process.run()
        process.waitUntilExit()
        let output = String(data: out.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? ""
        let errorText = String(data: err.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? ""
        if process.terminationStatus != 0 {
            if errorText.contains("-128") || errorText.contains("User canceled") {
                throw EngineError(message: "已取消管理员授权")
            }
            throw EngineError(message: errorText.isEmpty ? "安装失败" : errorText.trimmingCharacters(in: .whitespacesAndNewlines))
        }
        return output
    }

    private static func shellQuote(_ text: String) -> String {
        "'" + text.replacingOccurrences(of: "'", with: "'\\''") + "'"
    }

    private static func appleScriptQuote(_ text: String) -> String {
        "\"" + text.replacingOccurrences(of: "\\", with: "\\\\")
            .replacingOccurrences(of: "\"", with: "\\\"") + "\""
    }
}
