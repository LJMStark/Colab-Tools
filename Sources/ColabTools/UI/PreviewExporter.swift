import AppKit
import ColabToolsCore
import SwiftUI

#if os(macOS)
/// Renders crisp @2x Retina showcase previews of the actual live SwiftUI views (using sanitized demo data)
/// when launched with `COLABTOOLS_RENDER_PREVIEW=/path/to/output_dir`.
@MainActor
enum PreviewExporter {
    private enum ExportError: Error {
        case emptySize(URL)
        case bitmapAllocFailed(URL)
        case pngEncodeFailed(URL)
    }

    static func runIfRequested() -> Bool {
        guard let outDir = ProcessInfo.processInfo.environment["COLABTOOLS_RENDER_PREVIEW"], !outDir.isEmpty else {
            return false
        }
        let dirURL = URL(fileURLWithPath: outDir, isDirectory: true)
        do {
            try FileManager.default.createDirectory(at: dirURL, withIntermediateDirectories: true)

            // 1. Mole-style Main Desktop App: Status Bento Dashboard + Live Sessions Table
            let mainStatusStore = makeMainDemoStore(section: .status)
            try renderPanel(
                MainAppView(store: mainStatusStore, isPreviewMode: true),
                to: dirURL.appendingPathComponent("preview-main-status.png"),
                dark: true
            )

            // 2. Single-account HIG streamlined menu bar popover showcase
            let singleStore = makeDemoStore(multiAccount: false)
            try renderPanel(
                RootPopoverView(store: singleStore, isPreviewMode: true),
                to: dirURL.appendingPathComponent("preview-dashboard.png"),
                dark: true
            )

            // 3. Multi-account overview popover showcase
            let multiStore = makeDemoStore(multiAccount: true)
            multiStore.selectedViewMode = .overview
            try renderPanel(
                RootPopoverView(store: multiStore, isPreviewMode: true),
                to: dirURL.appendingPathComponent("preview-multi-account.png"),
                dark: true
            )

            // 4. Main App: Compute & Queue Workbench showcase
            let queueStore = makeMainDemoStore(section: .compute)
            try renderPanel(
                MainAppView(store: queueStore, isPreviewMode: true),
                to: dirURL.appendingPathComponent("preview-settings-queue.png"),
                dark: true
            )

            // 5. Main App: Multi-Client Integrations Hub showcase
            let integrationsStore = makeMainDemoStore(section: .integrations)
            try renderPanel(
                MainAppView(store: integrationsStore, isPreviewMode: true),
                to: dirURL.appendingPathComponent("preview-settings-integrations.png"),
                dark: true
            )

            // 6. First-Launch Onboarding & Environment Doctor showcase
            let onboardStore = makeOnboardingDemoStore()
            try renderPanel(
                MainAppView(store: onboardStore, isPreviewMode: true),
                to: dirURL.appendingPathComponent("preview-onboarding.png"),
                dark: true
            )

            // Optional QA suite (Light mode + Settings Doctor)
            if let qaDir = ProcessInfo.processInfo.environment["COLABTOOLS_RENDER_QA"], !qaDir.isEmpty {
                let qaURL = URL(fileURLWithPath: qaDir, isDirectory: true)
                try FileManager.default.createDirectory(at: qaURL, withIntermediateDirectories: true)

                try renderPanel(
                    MainAppView(store: makeMainDemoStore(section: .status), isPreviewMode: true),
                    to: qaURL.appendingPathComponent("qa-main-status-light.png"),
                    dark: false
                )
                try renderPanel(
                    MainAppView(store: makeOnboardingDemoStore(), isPreviewMode: true),
                    to: qaURL.appendingPathComponent("qa-onboarding-light.png"),
                    dark: false
                )
                try renderPanel(
                    MainAppView(store: makeMainDemoStore(section: .settings), isPreviewMode: true),
                    to: qaURL.appendingPathComponent("qa-main-settings-dark.png"),
                    dark: true
                )
                try renderPanel(
                    RootPopoverView(store: makeDemoStore(multiAccount: false), isPreviewMode: false),
                    to: qaURL.appendingPathComponent("qa-dashboard-light.png"),
                    dark: false
                )
                try renderPanel(
                    MainAppView(store: makeNarrowIdleLongNameDemoStore(), isPreviewMode: true, previewWidth: 980),
                    to: qaURL.appendingPathComponent("qa-main-status-narrow-idle.png"),
                    dark: true
                )
            }
            return true
        } catch {
            fputs("[ERROR] Preview export failed: \(error)\n", stderr)
            exit(1)
        }
    }

    private static func makeOnboardingDemoStore() -> WorkspaceStore {
        let store = WorkspaceStore(skipAutoBootstrap: true)
        store.cliInstalled = false
        store.showingOnboarding = true
        store.envReport = EnvironmentCheckReport(
            colabInstalled: false,
            colabPath: nil,
            colabVersion: nil,
            uvPath: "/opt/homebrew/bin/uv",
            pipxPath: nil,
            brewPath: "/opt/homebrew/bin/brew",
            python3Path: "/usr/bin/python3",
            recommendedMethod: .uvTool
        )
        return store
    }

    /// Regression scenario mirroring a 980pt minimum-width window with an offline high-CU account and a long username.
    private static func makeNarrowIdleLongNameDemoStore() -> WorkspaceStore {
        let store = WorkspaceStore(skipAutoBootstrap: true)
        store.cliInstalled = true
        store.showingOnboarding = false
        store.selectedMainSection = .status
        let acc = AccountProfile(
            displayName: "demolongusername9876",
            email: "demolongusername9876@example.com",
            colorTag: .amber,
            tier: .aiUltra,
            authMode: .systemDefaultOAuth2,
            sessionName: "llm-coder",
            preferredAccelerator: .autoBest,
            highMem: false,
            workloadKind: .llm,
            enableLLMModule: true,
            autoShutdownEnabled: true,
            idleTimeoutMinutes: 40
        )
        var settings = AppSettings()
        settings.accounts = [acc]
        settings.selectedAccountId = acc.id
        settings.hasCompletedOnboarding = true
        store.settings = settings
        var rt = AccountRuntimeState()
        rt.phase = .offline
        rt.cuBalance = 2362.2
        rt.lastUsageFetchAt = Date()
        store.runtimeByAccount[acc.id] = rt
        return store
    }

    private static func makeMainDemoStore(section: WorkspaceStore.MainSection) -> WorkspaceStore {
        let store = makeDemoStore(multiAccount: true)
        store.selectedMainSection = section
        store.showingOnboarding = false
        store.cliInstalled = true
        store.envReport = EnvironmentCheckReport(
            colabInstalled: true,
            colabPath: "~/.local/bin/colab",
            colabVersion: "0.8.2",
            uvPath: "/opt/homebrew/bin/uv",
            pipxPath: nil,
            brewPath: "/opt/homebrew/bin/brew",
            python3Path: "/usr/bin/python3",
            recommendedMethod: .uvTool
        )
        if var acc = store.settings.accounts.first {
            acc.enablePoliteQueue = true
            acc.queueTargets = [.g4, .h100]
            acc.queueFallbackEnabled = true
            acc.queueFallbackAfterRounds = 2
            acc.queueFallbackTarget = .l4
            acc.scheduledStartEnabled = true
            acc.scheduledStartHour = 7
            acc.scheduledStartMinute = 0
            acc.scheduledStartRepeatDaily = true
            acc.autoSyncCCSwitch = true
            acc.autoSyncOpenCode = true
            acc.autoSyncCodex = false
            store.settings.accounts[0] = acc
            if var st = store.runtimeByAccount[acc.id] {
                if section == .compute {
                    st.phase = .offline
                } else {
                    st.phase = .running
                    st.publicURL = "https://demo-colab-node.trycloudflare.com"
                }
                st.ccSwitchSynced = true
                st.openCodeSynced = true
                store.runtimeByAccount[acc.id] = st
            }
        }
        store.clientDetection = ClientDetectionSnapshot(
            ccSwitchInstalled: true,
            openCodeInstalled: true,
            codexInstalled: true,
            claudeCodeInstalled: true
        )
        store.insights = AllocationInsights(bucketsByHardware: [
            "G4": [
                "1": AllocationBucket(attempts: 4, successes: 2),
                "6": AllocationBucket(attempts: 8, successes: 7),
                "10": AllocationBucket(attempts: 6, successes: 3),
                "14": AllocationBucket(attempts: 5, successes: 1),
                "18": AllocationBucket(attempts: 5, successes: 1),
                "22": AllocationBucket(attempts: 6, successes: 2)
            ],
            "H100": [
                "6": AllocationBucket(attempts: 5, successes: 4),
                "10": AllocationBucket(attempts: 4, successes: 2),
                "22": AllocationBucket(attempts: 5, successes: 1)
            ]
        ])
        return store
    }

    private static func makeDemoStore(multiAccount: Bool) -> WorkspaceStore {
        let store = WorkspaceStore(skipAutoBootstrap: true)
        store.clientDetection = ClientDetectionSnapshot(
            ccSwitchInstalled: true,
            openCodeInstalled: true,
            codexInstalled: true,
            claudeCodeInstalled: true
        )

        let id1 = UUID()
        let acc1 = AccountProfile(
            id: id1,
            displayName: "Personal",
            email: "dev@example.com",
            colorTag: .amber,
            tier: .aiUltra,
            authMode: .systemDefaultOAuth2,
            sessionName: "llm-coder",
            preferredAccelerator: .autoBest,
            highMem: false,
            enableLLMModule: true,
            enablePoliteQueue: true,
            queueTargets: [.g4, .h100],
            queueFallbackEnabled: true,
            queueFallbackAfterRounds: 2,
            queueFallbackTarget: .l4,
            scheduledStartEnabled: true,
            scheduledStartHour: 7,
            scheduledStartMinute: 0,
            scheduledStartRepeatDaily: true,
            autoShutdownEnabled: true,
            idleTimeoutMinutes: 40,
            warnBeforeMinutes: 2,
            autoSyncCCSwitch: true,
            autoSyncOpenCode: true
        )

        var st1 = AccountRuntimeState()
        st1.phase = .running
        st1.cuBalance = 2374.8
        st1.cuRatePerHour = 2.33
        st1.activeAssignments = 1
        st1.activeHardware = "L4"
        st1.gpuName = "NVIDIA L4"
        st1.vramUsedMB = 21930
        st1.vramTotalMB = 23034
        st1.gpuUtilPercent = 18
        st1.sessionUptimeSeconds = 12 * 3600 + 46 * 60
        st1.modelFile = "Huihui-Qwen3.8-27B-abliterated-UD-Q4_K_XL.gguf"
        st1.modelAlias = "qwen3.8-27b-uncensored"
        st1.contextLimit = 262144
        st1.kvQuant = "q4_0"
        st1.publicURL = "https://demo-colab-node.trycloudflare.com"
        st1.serverHealthy = true
        st1.activitySignal = .idle(observedAt: Date())
        st1.lastTaskID = 118
        st1.promptTokensUsed = 58200
        st1.idleSeconds = 180
        st1.ccSwitchSynced = true
        st1.openCodeSynced = true
        st1.lastUsageFetchAt = Date()
        st1.gpuUtilHistory = [6, 12, 44, 68, 52, 24, 15, 9, 8, 35, 74, 61, 28, 18, 14, 18]
        st1.sessions = [
            ColabSessionItem(name: "llm-coder", endpoint: "gpu-l4-demo", hardware: "L4", shape: "Standard", variant: "GPU")
        ]

        store.settings.accounts = [acc1]
        store.settings.selectedAccountId = id1
        store.runtimeByAccount[id1] = st1

        if multiAccount {
            let id2 = UUID()
            let acc2 = AccountProfile(
                id: id2,
                displayName: "Work",
                email: "work@company.dev",
                colorTag: .emerald,
                tier: .colabProPlus,
                authMode: .isolatedKeychainADC,
                sessionName: "train-a100",
                preferredAccelerator: .a100,
                highMem: true,
                enableLLMModule: false,
                autoShutdownEnabled: false
            )
            var st2 = AccountRuntimeState()
            st2.phase = .running
            st2.cuBalance = 584.2
            st2.cuRatePerHour = 12.10
            st2.activeAssignments = 1
            st2.activeHardware = "A100"
            st2.gpuName = "NVIDIA A100-SXM4-40GB"
            st2.vramUsedMB = 31200
            st2.vramTotalMB = 40960
            st2.gpuUtilPercent = 82
            st2.sessionUptimeSeconds = 3 * 3600 + 20 * 60
            st2.sessions = [
                ColabSessionItem(name: "train-a100", endpoint: "gpu-a100-demo", hardware: "A100", shape: "High-RAM", variant: "GPU")
            ]

            let id3 = UUID()
            let acc3 = AccountProfile(
                id: id3,
                displayName: "Lab",
                email: "lab@example.org",
                colorTag: .cyan,
                tier: .free,
                authMode: .isolatedKeychainADC,
                sessionName: "t4-eval",
                preferredAccelerator: .t4,
                enableLLMModule: false,
                autoShutdownEnabled: false
            )
            var st3 = AccountRuntimeState()
            st3.phase = .offline
            st3.cuBalance = 0
            st3.lastUsageFetchAt = Date()

            store.settings.accounts.append(contentsOf: [acc2, acc3])
            store.runtimeByAccount[id2] = st2
            store.runtimeByAccount[id3] = st3
        }

        return store
    }

    private static func renderPanel<V: View>(_ content: V, to url: URL, dark: Bool = true) throws {
        let appearance = NSAppearance(named: dark ? .darkAqua : .aqua)
        let framed = ZStack {
            LinearGradient(
                colors: dark
                    ? [
                        Color(red: 0.105, green: 0.095, blue: 0.075),
                        Color(red: 0.078, green: 0.082, blue: 0.096)
                    ]
                    : [
                        Color(red: 0.86, green: 0.87, blue: 0.89),
                        Color(red: 0.80, green: 0.82, blue: 0.85)
                    ],
                startPoint: .topLeading,
                endPoint: .bottomTrailing
            )
            content
                .background(
                    LinearGradient(
                        colors: dark
                            ? [
                                Color(red: 0.145, green: 0.135, blue: 0.112),
                                Color(red: 0.118, green: 0.115, blue: 0.106)
                            ]
                            : [
                                Color(red: 0.965, green: 0.965, blue: 0.972),
                                Color(red: 0.935, green: 0.940, blue: 0.950)
                            ],
                        startPoint: .top,
                        endPoint: .bottom
                    )
                )
                .clipShape(RoundedRectangle(cornerRadius: 16, style: .continuous))
                .overlay(
                    RoundedRectangle(cornerRadius: 16, style: .continuous)
                        .strokeBorder(dark ? Color.white.opacity(0.11) : Color.black.opacity(0.10), lineWidth: 0.8)
                )
                .shadow(color: .black.opacity(dark ? 0.55 : 0.20), radius: 22, y: 10)
                .padding(26)
        }
        .environment(\.colorScheme, dark ? .dark : .light)
        .fixedSize()

        let hosting = NSHostingView(rootView: framed)
        hosting.appearance = appearance
        let size = hosting.fittingSize
        guard size.width > 0, size.height > 0 else {
            throw ExportError.emptySize(url)
        }

        let window = NSWindow(
            contentRect: NSRect(origin: NSPoint(x: -8000, y: -8000), size: size),
            styleMask: [.borderless],
            backing: .buffered,
            defer: false
        )
        defer { window.close() }
        window.appearance = appearance
        window.isOpaque = false
        window.backgroundColor = .clear
        window.contentView = hosting
        hosting.frame = NSRect(origin: .zero, size: size)
        window.orderBack(nil)

        hosting.layoutSubtreeIfNeeded()
        hosting.displayIfNeeded()
        RunLoop.main.run(until: Date(timeIntervalSinceNow: 0.18))
        hosting.layoutSubtreeIfNeeded()
        let finalSize = hosting.fittingSize
        if abs(finalSize.height - size.height) > 2 || abs(finalSize.width - size.width) > 2 {
            window.setContentSize(finalSize)
            hosting.frame = NSRect(origin: .zero, size: finalSize)
            hosting.layoutSubtreeIfNeeded()
            RunLoop.main.run(until: Date(timeIntervalSinceNow: 0.1))
        }
        let renderSize = hosting.bounds.size

        let scale: CGFloat = 2.0
        let pixelW = Int((renderSize.width * scale).rounded())
        let pixelH = Int((renderSize.height * scale).rounded())
        guard let rep = NSBitmapImageRep(
            bitmapDataPlanes: nil,
            pixelsWide: pixelW,
            pixelsHigh: pixelH,
            bitsPerSample: 8,
            samplesPerPixel: 4,
            hasAlpha: true,
            isPlanar: false,
            colorSpaceName: .deviceRGB,
            bytesPerRow: 0,
            bitsPerPixel: 0
        ) else {
            throw ExportError.bitmapAllocFailed(url)
        }
        rep.size = renderSize
        hosting.cacheDisplay(in: hosting.bounds, to: rep)

        guard let png = rep.representation(using: .png, properties: [:]) else {
            throw ExportError.pngEncodeFailed(url)
        }
        try png.write(to: url, options: .atomic)
        print("Exported preview -> \(url.path) (\(pixelW)x\(pixelH))")
    }
}
#endif
