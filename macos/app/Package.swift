// swift-tools-version:5.9
// SPDX-License-Identifier: GPL-2.0-or-later
import PackageDescription

let package = Package(
    name: "QingyouPrivateCloud",
    platforms: [.macOS(.v13)],
    products: [
        .executable(name: "QingyouPrivateCloud", targets: ["QingyouPrivateCloud"])
    ],
    targets: [
        .executableTarget(
            name: "QingyouPrivateCloud",
            path: "Sources"
        )
    ]
)
