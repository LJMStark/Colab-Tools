// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "ColabTools",
    platforms: [
        .macOS(.v14)
    ],
    products: [
        .library(name: "ColabToolsCore", targets: ["ColabToolsCore"]),
        .executable(name: "ColabTools", targets: ["ColabTools"])
    ],
    targets: [
        .target(
            name: "ColabToolsCore",
            path: "Sources/ColabToolsCore",
            linkerSettings: [
                .linkedLibrary("sqlite3"),
                .linkedFramework("Security")
            ]
        ),
        .executableTarget(
            name: "ColabTools",
            dependencies: ["ColabToolsCore"],
            path: "Sources/ColabTools",
            linkerSettings: [
                .linkedFramework("AppKit"),
                .linkedFramework("ServiceManagement"),
                .linkedFramework("SwiftUI"),
                .linkedFramework("UserNotifications")
            ]
        ),
        .testTarget(
            name: "ColabToolsTests",
            dependencies: ["ColabToolsCore"],
            path: "Tests/ColabToolsTests"
        )
    ]
)
