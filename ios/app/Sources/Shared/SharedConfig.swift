// SPDX-License-Identifier: GPL-2.0-or-later
// SharedConfig.swift：主 App 与隧道扩展共用的常量。
//
// 这个文件同时编进两个 target，改一处两边一起生效：App Group 标识、
// Bundle ID、后台地址、状态文件位置都必须完全一致，否则扩展读不到 App 写下的设置。

import Foundation

enum SharedConfig {
    /// App Group：App 与扩展共享状态文件的地方（需要开发者后台开通同名 Group）。
    static let appGroup = "group.cn.qo8.qyvpn"

    /// 主 App 的 Bundle ID，与 macOS 客户端保持同一套命名。
    static let appBundleID = "cn.qo8.qyvpn.client"

    /// 隧道扩展的 Bundle ID，必须是主 App 的子标识。
    static let tunnelBundleID = "cn.qo8.qyvpn.client.tunnel"

    /// 后台地址。换域名时改这里并重新出包。
    static let apiBase = "https://vpn.qo8.cn"

    /// 后台统计用的平台标识。
    static let platform = "ios"

    /// 连接内核的状态目录（日志、流量账本、设备标识）。
    static let engineDirName = "engine"

    /// 扩展每秒写一次的状态快照，App 轮询它来刷新界面。
    static let statusFileName = "status.json"

    /// 连接日志的文件名（由 Go 内核写入状态目录）。
    static let logFileName = "connection.log"

    /// App Group 容器里的 UserDefaults，存「上次选中的节点」这类轻量设置。
    static let settingsKeySelectedNode = "selectedNode"
    static let settingsKeyAutoReconnect = "autoReconnect"

    /// App Group 容器根目录。没有开通 App Group 时返回 nil，两个进程都会因此报错，
    /// 所以调用方要当成致命错误处理。
    static var containerURL: URL? {
        FileManager.default.containerURL(forSecurityApplicationGroupIdentifier: appGroup)
    }

    /// 内核对外的状态目录。
    static var engineURL: URL? {
        containerURL?.appendingPathComponent(engineDirName, isDirectory: true)
    }

    static var statusFileURL: URL? {
        containerURL?.appendingPathComponent(statusFileName)
    }

    static var sharedDefaults: UserDefaults? {
        UserDefaults(suiteName: appGroup)
    }
}
