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

            // 1. Single-account dashboard showcase
            let singleStore = makeDemoStore(multiAccount: false)
            try renderPanel(
                RootPopoverView(store: singleStore, isPreviewMode: true),
                to: dirURL.appendingPathComponent("preview-dashboard.png")
            )

            // 2. Multi-account overview showcase
            let multiStore = makeDemoStore(multiAccount: true)
            multiStore.selectedViewMode = .overview
            try renderPanel(
                RootPopoverView(store: multiStore, isPreviewMode: true),
                to: dirURL.appendingPathComponent("preview-multi-account.png"),
                dark: true
            )

            // 3. Optional extra visual QA checks (Light/Dark Settings, Queue, Multimodal, About)
            if let qaDir = ProcessInfo.processInfo.environment["COLABTOOLS_RENDER_QA"], !qaDir.isEmpty {
                let qaURL = URL(fileURLWithPath: qaDir, isDirectory: true)
                try FileManager.default.createDirectory(at: qaURL, withIntermediateDirectories: true)

                let settingsStore = makeDemoStore(multiAccount: false)
                settingsStore.selectedTab = .settings
                settingsStore.settings.menuBarMode = .iconOnly
                try renderPanel(
                    RootPopoverView(store: settingsStore, isPreviewMode: false),
                    to: qaURL.appendingPathComponent("qa-settings-light-icononly.png"),
                    dark: false
                )
                try renderPanel(
                    RootPopoverView(store: settingsStore, isPreviewMode: false),
                    to: qaURL.appendingPathComponent("qa-settings-dark-icononly.png"),
                    dark: true
                )
                let liveDashStore = makeDemoStore(multiAccount: false)
                try renderPanel(
                    RootPopoverView(store: liveDashStore, isPreviewMode: false),
                    to: qaURL.appendingPathComponent("qa-dashboard-live-light.png"),
                    dark: false
                )

                let queueStore = makeDemoStore(multiAccount: false)
                if var acc = queueStore.settings.accounts.first {
                    acc.enablePoliteQueue = true
                    acc.queueTargets = [.g4, .h100]
                    queueStore.settings.accounts = [acc]
                    var qst = queueStore.state(for: acc.id)
                    qst.phase = .queuing(
                        attempt: 2,
                        maxAttempts: PoliteQueuePolicy.maxAttempts,
                        nextRetryAt: Date().addingTimeInterval(258),
                        targetsSummary: "G4 → H100"
                    )
                    qst.vramUsedMB = 0
                    qst.vramTotalMB = 0
                    qst.gpuUtilPercent = 0
                    qst.serverHealthy = false
                    qst.sessions = []
                    queueStore.runtimeByAccount[acc.id] = qst
                }
                try renderPanel(
                    RootPopoverView(store: queueStore, isPreviewMode: false),
                    to: qaURL.appendingPathComponent("qa-queue-active-dark.png"),
                    dark: true
                )
                try renderPanel(
                    RootPopoverView(store: queueStore, isPreviewMode: false),
                    to: qaURL.appendingPathComponent("qa-queue-active-light.png"),
                    dark: false
                )

                let aboutStore = makeDemoStore(multiAccount: false)
                aboutStore.selectedTab = .about
                try renderPanel(
                    RootPopoverView(store: aboutStore, isPreviewMode: false),
                    to: qaURL.appendingPathComponent("qa-about-noqr-dark.png"),
                    dark: true
                )

                let videoStore = makeDemoStore(multiAccount: false)
                if var acc = videoStore.settings.accounts.first {
                    acc.workloadKind = .video
                    acc.remoteServicePort = 8188
                    acc.localSSHTunnelPort = 8188
                    acc.autoShutdownEnabled = true
                    videoStore.settings.accounts = [acc]
                    var vst = videoStore.state(for: acc.id)
                    vst.gpuName = "NVIDIA H100 80GB HBM3"
                    vst.activeHardware = "H100"
                    vst.vramUsedMB = 54200
                    vst.vramTotalMB = 81559
                    vst.gpuUtilPercent = 94
                    vst.cpuUtilPercent = 38
                    vst.cudaProcessCount = 1
                    vst.cuRatePerHour = 18.0
                    vst.workloadName = "Wan2.2-14B (ComfyUI)"
                    vst.workloadMetricLabel = "渲染进度"
                    vst.workloadMetricValue = "18/30"
                    vst.workloadMetricUnit = "steps"
                    vst.workloadMetricRatio = 0.60
                    vst.workloadDetail = "运行 1 · 排队 2 · 720p"
                    vst.queueRunning = 1
                    vst.queuePending = 2
                    vst.detectedServicePort = 8188
                    vst.displayUIURL = "https://wan-comfy-demo.trycloudflare.com"
                    vst.publicURL = ""
                    vst.activitySignal = .busy(observedAt: Date())
                    vst.signalSource = .statusFile
                    vst.gpuUtilHistory = [42, 78, 92, 96, 89, 95, 94, 91, 96, 94]
                    vst.sessions = [
                        ColabSessionItem(name: "wan-video", endpoint: "gpu-h100-demo", hardware: "H100", shape: "High-RAM", variant: "GPU")
                    ]
                    videoStore.runtimeByAccount[acc.id] = vst
                }
                try renderPanel(
                    RootPopoverView(store: videoStore, isPreviewMode: false),
                    to: qaURL.appendingPathComponent("qa-multimodal-video-dark.png"),
                    dark: true
                )

                let musicStore = makeDemoStore(multiAccount: false)
                if var acc = musicStore.settings.accounts.first {
                    acc.workloadKind = .music
                    acc.remoteServicePort = 7860
                    acc.localSSHTunnelPort = 7860
                    acc.autoShutdownEnabled = true
                    musicStore.settings.accounts = [acc]
                    var mst = musicStore.state(for: acc.id)
                    mst.gpuName = "NVIDIA L4"
                    mst.activeHardware = "L4"
                    mst.vramUsedMB = 11400
                    mst.vramTotalMB = 23034
                    mst.gpuUtilPercent = 2
                    mst.cpuUtilPercent = 9
                    mst.cudaProcessCount = 1
                    mst.workloadName = "ACE-Step 音乐工坊"
                    mst.workloadMetricLabel = ""
                    mst.workloadMetricValue = ""
                    mst.detectedServicePort = 7860
                    mst.displayUIURL = ""
                    mst.publicURL = ""
                    mst.sshTunnelActive = true
                    mst.serverHealthy = true
                    mst.activitySignal = .idle(observedAt: Date())
                    mst.signalSource = .hardwareDualLowLoad
                    mst.idleSeconds = 420
                    musicStore.runtimeByAccount[acc.id] = mst
                }
                try renderPanel(
                    RootPopoverView(store: musicStore, isPreviewMode: false),
                    to: qaURL.appendingPathComponent("qa-multimodal-music-light.png"),
                    dark: false
                )
            }
            return true
        } catch {
            fputs("[ERROR] Preview export failed: \(error)\n", stderr)
            exit(1)
        }
    }

    private static func makeDemoStore(multiAccount: Bool) -> WorkspaceStore {
        let store = WorkspaceStore(skipAutoBootstrap: true)

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
            autoShutdownEnabled: true,
            idleTimeoutMinutes: 40,
            warnBeforeMinutes: 2
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
