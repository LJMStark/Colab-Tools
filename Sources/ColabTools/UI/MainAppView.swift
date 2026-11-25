import AppKit
import ColabToolsCore
import ServiceManagement
import SwiftUI

/// Mole-inspired native macOS main desktop dashboard & settings workspace.
/// Features a floating centered capsule navigation bar, 4x2 Bento live status grid + live session table,
/// and Apple macOS HIG typography (13.5pt body / 12pt captions / regular control size).
struct MainAppView: View {
    @ObservedObject var store: WorkspaceStore
    var isPreviewMode: Bool = false
    var previewWidth: CGFloat = 1160

    @Environment(\.colorScheme) private var colorScheme
    @State private var loginItemStatus: SMAppService.Status = .notRegistered
    @State private var selectedCopyProtocol: CopyProtocolKind = .openAI
    @State private var selectedInsightHW: String = ""
    @State private var showingAddAccountSheet: Bool = false
    @State private var remotePortDraft: String = ""
    @State private var localPortDraft: String = ""
    @State private var settingsSubTab: SettingsSubTab = .general

    // Unified compact table column widths so narrow windows (980pt) still leave >= 320pt for long account names
    private let colHardwareW: CGFloat = 114
    private let colVRAMW: CGFloat = 124
    private let colCUW: CGFloat = 84
    private let colRateW: CGFloat = 76
    private let colActionW: CGFloat = 120

    enum SettingsSubTab: String, CaseIterable, Identifiable {
        case general = "通用与关机"
        case doctor = "环境体检"
        case advanced = "高级参数"
        case about = "关于"
        var id: String { rawValue }
    }

    private var selectedAccount: AccountProfile? {
        store.selectedAccount
    }

    var body: some View {
        let _ = store.queueTickVersion
        ZStack {
            windowBackground

            if store.showingOnboarding {
                OnboardingGuideView(store: store, isPreviewMode: isPreviewMode)
            } else {
                VStack(spacing: 0) {
                    topCapsuleHeader
                    Divider().opacity(0.28)
                    mainContentScroll
                }
            }
        }
        .frame(
            minWidth: isPreviewMode ? previewWidth : 980,
            idealWidth: previewWidth,
            maxWidth: isPreviewMode ? previewWidth : .infinity,
            minHeight: isPreviewMode ? 700 : 660,
            idealHeight: isPreviewMode ? nil : 760,
            maxHeight: isPreviewMode ? nil : .infinity,
            alignment: .top
        )
        .overlay(alignment: .bottomTrailing) {
            if let toast = store.toastMessage {
                HStack(spacing: 8) {
                    Image(systemName: "checkmark.circle.fill")
                        .font(.system(size: 14))
                        .foregroundStyle(ColabTheme.emerald)
                    Text(toast)
                        .font(ColabTypography.bodyMedium)
                        .lineLimit(1)
                }
                .padding(.horizontal, 16)
                .padding(.vertical, 10)
                .background(.regularMaterial, in: Capsule())
                .overlay(Capsule().strokeBorder(Color.primary.opacity(0.14), lineWidth: 0.7))
                .shadow(color: .black.opacity(0.20), radius: 12, y: 4)
                .padding(20)
                .transition(.opacity)
            }
        }
        .sheet(isPresented: $showingAddAccountSheet) {
            AddAccountSheetView(store: store, embedded: false) {
                showingAddAccountSheet = false
            }
            .padding(22)
            .frame(width: 480)
        }
        .onAppear {
            refreshLoginItemStatus()
            syncPortDrafts()
        }
        .onChange(of: store.settings.selectedAccountId) { _, _ in
            syncPortDrafts()
        }
        .onChange(of: selectedAccount?.remoteServicePort) { _, _ in
            syncPortDrafts()
        }
        .onChange(of: selectedAccount?.localSSHTunnelPort) { _, _ in
            syncPortDrafts()
        }
        .onReceive(NotificationCenter.default.publisher(for: NSWindow.didBecomeKeyNotification)) { _ in
            refreshLoginItemStatus()
        }
    }

    // Warm dark / soft light canvas inspired by Mole
    private var windowBackground: some View {
        LinearGradient(
            colors: colorScheme == .dark
                ? [
                    Color(red: 0.148, green: 0.132, blue: 0.102),
                    Color(red: 0.112, green: 0.105, blue: 0.092)
                ]
                : [
                    Color(red: 0.962, green: 0.960, blue: 0.955),
                    Color(red: 0.935, green: 0.938, blue: 0.946)
                ],
            startPoint: .top,
            endPoint: .bottom
        )
        .ignoresSafeArea()
    }

    // MARK: - Top Mole-Style Floating Capsule Bar
    private var topCapsuleHeader: some View {
        ZStack {
            FloatingCapsuleNavBar(selection: $store.selectedMainSection)

            HStack {
                // Left area (clears macOS traffic lights when live)
                HStack(spacing: 8) {
                    if store.settings.accounts.count >= 2, let acc = selectedAccount {
                        Menu {
                            ForEach(store.settings.accounts) { item in
                                Button {
                                    store.settings.selectedAccountId = item.id
                                } label: {
                                    HStack {
                                        Text("\(item.displayName) · \(item.tier.shortBadge) (\(Formatters.cu(store.state(for: item.id).cuBalance)) CU)")
                                        if item.id == acc.id { Image(systemName: "checkmark") }
                                    }
                                }
                            }
                        } label: {
                            HStack(spacing: 6) {
                                Circle()
                                    .fill(ColabTheme.color(for: acc.colorTag))
                                    .frame(width: 8, height: 8)
                                Text(acc.displayName)
                                    .font(ColabTypography.bodyMedium)
                                Image(systemName: "chevron.up.chevron.down")
                                    .font(.system(size: 10, weight: .semibold))
                                    .foregroundStyle(.secondary)
                            }
                            .padding(.horizontal, 11)
                            .padding(.vertical, 6)
                            .background(Color.primary.opacity(0.06), in: Capsule())
                        }
                        .menuStyle(.borderlessButton)
                        .menuIndicator(.hidden)
                    }
                }
                .padding(.leading, isPreviewMode ? 22 : 84)

                Spacer()

                // Right live status pill (mirrors Mole's top-right badge)
                HStack(spacing: 8) {
                    Button {
                        if !store.cliInstalled {
                            store.reopenOnboarding()
                        } else {
                            Task { await store.refreshAllAccounts(force: true, silent: false) }
                        }
                    } label: {
                        HStack(spacing: 6) {
                            Circle()
                                .fill(topRightPillColor)
                                .frame(width: 7.5, height: 7.5)
                            Text(topRightPillLabel)
                                .font(ColabTypography.subheadlineMedium)
                                .monospacedDigit()
                                .foregroundStyle(.primary.opacity(0.88))
                        }
                        .padding(.horizontal, 12)
                        .padding(.vertical, 6)
                        .background(Color.primary.opacity(0.07), in: Capsule())
                        .overlay(Capsule().strokeBorder(Color.primary.opacity(0.10), lineWidth: 0.6))
                    }
                    .buttonStyle(.plain)
                    .help(store.cliInstalled ? "点击立即刷新全部账号状态" : "缺少 google-colab-cli，点击打开安装引导")
                }
                .padding(.trailing, 22)
            }
        }
        .padding(.top, isPreviewMode ? 18 : 26)
        .padding(.bottom, 14)
    }

    private var topRightPillColor: Color {
        if !store.cliInstalled { return ColabTheme.coral }
        if !store.runningAccounts.isEmpty { return ColabTheme.emerald }
        if store.settings.accounts.contains(where: { store.state(for: $0.id).phase.isQueuing }) { return ColabTheme.amber }
        return Color(nsColor: .tertiaryLabelColor)
    }

    private var topRightPillLabel: String {
        if !store.cliInstalled { return "需安装 Colab CLI" }
        let running = store.runningAccounts
        if running.count >= 2 { return "\(running.count) 运行中 · 刷新" }
        if let first = running.first { return "运行中 · \(first.1.shortGPUName)" }
        if let qAcc = store.settings.accounts.first(where: { store.state(for: $0.id).phase.isQueuing }),
           case .queuing(let a, let m, _, _) = store.state(for: qAcc.id).phase {
            return "排队 \(a)/\(m)"
        }
        let ver = (Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String) ?? "0.3.6"
        return "v\(ver) · 刷新"
    }

    // MARK: - Main Content Container
    @ViewBuilder
    private var mainContentScroll: some View {
        let content = Group {
            switch store.selectedMainSection {
            case .status:
                statusDashboardPage
            case .compute:
                computeAndQueuePage
                    .frame(maxWidth: 880, alignment: .leading)
                    .frame(maxWidth: .infinity)
            case .integrations:
                integrationsPage
                    .frame(maxWidth: 880, alignment: .leading)
                    .frame(maxWidth: .infinity)
            case .accounts:
                accountsPage
                    .frame(maxWidth: 880, alignment: .leading)
                    .frame(maxWidth: .infinity)
            case .settings:
                settingsPage
                    .frame(maxWidth: 880, alignment: .leading)
                    .frame(maxWidth: .infinity)
            }
        }
        .padding(.horizontal, 24)
        .padding(.top, 18)
        .padding(.bottom, 24)

        if isPreviewMode {
            content
        } else {
            ScrollView(.vertical, showsIndicators: true) {
                content
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
    }

    // MARK: - Tab 1: Mole-Style Status Dashboard (4x2 Bento Grid + Live Table)
    @ViewBuilder
    private var statusDashboardPage: some View {
        VStack(alignment: .leading, spacing: 14) {
            if !store.cliInstalled {
                missingCLIBanner
            }

            if let acc = selectedAccount {
                let rt = store.state(for: acc.id)

                if case .queuing(let a, let m, let nextAt, let summary) = rt.phase {
                    bentoQueueBanner(attempt: a, maxAttempts: m, nextRetryAt: nextAt, summary: summary, accountId: acc.id)
                } else if let until = rt.queueCircuitBreakUntil, until > Date() {
                    let remMin = max(1, Int(ceil(until.timeIntervalSinceNow / 60.0)))
                    HStack(spacing: 10) {
                        Image(systemName: "hand.raised.fill").font(.system(size: 14)).foregroundStyle(ColabTheme.coral)
                        Text("已触发防风控硬熔断保护，冷却剩余约 \(remMin) 分钟后可重新申请。")
                            .font(ColabTypography.bodyMedium)
                        Spacer()
                    }
                    .padding(.horizontal, 14)
                    .padding(.vertical, 10)
                    .background(ColabTheme.coral.opacity(0.13), in: RoundedRectangle(cornerRadius: 12, style: .continuous))
                }

                // 4x2 Mole-Style Bento Matrix
                let cols = Array(repeating: GridItem(.flexible(), spacing: 14), count: 4)
                LazyVGrid(columns: cols, spacing: 14) {
                    bentoHealthCUCard(acc: acc, rt: rt)
                    bentoVRAMCard(rt: rt)
                    bentoGPUCard(acc: acc, rt: rt)
                    bentoWorkloadCard(acc: acc, rt: rt)
                    bentoBatteryQuotaCard(acc: acc, rt: rt)
                    bentoIdleWatchdogCard(acc: acc, rt: rt)
                    bentoStrategyCard(acc: acc)
                    bentoIntegrationsQuickCard(acc: acc, rt: rt)
                }

                // Bottom Mole-Style Sessions & Multi-Account Live Table
                liveSessionsAndAccountsTable
            } else {
                emptyWorkspaceHero
            }
        }
    }

    private var missingCLIBanner: some View {
        HStack(spacing: 12) {
            Image(systemName: "exclamationmark.triangle.fill")
                .font(.system(size: 16))
                .foregroundStyle(ColabTheme.amber)
            VStack(alignment: .leading, spacing: 2) {
                Text("未检测到官方 google-colab-cli")
                    .font(ColabTypography.bodySemibold)
                Text("打开安装向导可一键自动为你安装并配置好 Google 账号。")
                    .font(ColabTypography.subheadline)
                    .foregroundStyle(.secondary)
            }
            Spacer()
            Button("打开一键安装向导") {
                store.reopenOnboarding()
            }
            .buttonStyle(.borderedProminent)
            .tint(ColabTheme.amber)
            .controlSize(.regular)
        }
        .padding(14)
        .background(ColabTheme.amber.opacity(0.12), in: RoundedRectangle(cornerRadius: 14, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 14, style: .continuous).strokeBorder(ColabTheme.amber.opacity(0.32), lineWidth: 0.7))
    }

    private func bentoQueueBanner(attempt: Int, maxAttempts: Int, nextRetryAt: Date?, summary: String, accountId: UUID) -> some View {
        let countdown: String = {
            guard let nextRetryAt else { return "正在探测库存…" }
            let rem = max(0, Int(ceil(nextRetryAt.timeIntervalSinceNow)))
            return String(format: "距下次探测 %02d:%02d", rem / 60, rem % 60)
        }()
        return HStack(spacing: 10) {
            Image(systemName: "clock.badge.checkmark").font(.system(size: 16)).foregroundStyle(ColabTheme.amber)
            Text("合规排队中 · 第 \(attempt)/\(maxAttempts) 轮（\(summary)）· \(countdown)")
                .font(ColabTypography.bodyMedium)
                .monospacedDigit()
            Spacer()
            Button("取消排队") {
                store.cancelQueue(for: accountId)
            }
            .buttonStyle(.bordered)
            .controlSize(.regular)
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 10)
        .background(ColabTheme.amber.opacity(0.13), in: RoundedRectangle(cornerRadius: 12, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 12, style: .continuous).strokeBorder(ColabTheme.amber.opacity(0.35), lineWidth: 0.7))
    }

    // Bento 1: Health & Compute Units (Mole Hero Card)
    private func bentoHealthCUCard(acc: AccountProfile, rt: AccountRuntimeState) -> some View {
        let statusWord: String = {
            switch rt.phase {
            case .running: return rt.isProcessing ? "推理中" : "很好"
            case .queuing(let a, let m, _, _): return "排队 \(a)/\(m)"
            case .starting: return "启动中"
            case .stopping: return "关机中"
            case .error: return "需检查"
            case .offline: return rt.cuRatePerHour > 0 ? "仍有计费" : "已待机"
            }
        }()
        let statusTint: Color = {
            if rt.phase.isRunning { return ColabTheme.emerald }
            if rt.cuRatePerHour > 0 { return ColabTheme.amber }
            return .secondary
        }()
        return VStack(alignment: .leading, spacing: 10) {
            TileHeader(icon: "sun.max.fill", iconTint: ColabTheme.emerald, title: "算力状态", badgeText: acc.tier.shortBadge)

            HStack(alignment: .center, spacing: 8) {
                VStack(alignment: .leading, spacing: 4) {
                    Text(Formatters.cu(rt.cuBalance))
                        .font(.system(size: 28, weight: .bold, design: .rounded))
                        .monospacedDigit()
                        .lineLimit(1)
                        .minimumScaleFactor(0.65)
                        .layoutPriority(1)
                    HStack(spacing: 5) {
                        Text(statusWord)
                            .font(.system(size: 12.5, weight: .semibold))
                            .foregroundStyle(statusTint)
                            .fixedSize()
                        Text("·")
                            .font(ColabTypography.subheadline)
                            .foregroundStyle(.tertiary)
                        Text(
                            rt.phase.isRunning
                                ? Formatters.duration(rt.sessionUptimeSeconds)
                                : (rt.cuRatePerHour > 0 ? String(format: "%.2f CU/h", rt.cuRatePerHour) : "剩余 CU")
                        )
                            .font(ColabTypography.subheadline)
                            .monospacedDigit()
                            .foregroundStyle(rt.cuRatePerHour > 0 && !rt.phase.isRunning ? ColabTheme.amber : .secondary)
                            .lineLimit(1)
                            .truncationMode(.tail)
                    }
                }
                Spacer(minLength: 4)
                StatusAuraOrb(phase: rt.phase, size: 42)
            }

            Spacer(minLength: 0)

            bentoPowerButton(acc: acc, rt: rt)
        }
        .padding(16)
        .frame(height: 174)
        .bentoCard()
    }

    @ViewBuilder
    private func bentoPowerButton(acc: AccountProfile, rt: AccountRuntimeState) -> some View {
        let isCoolingDown = (rt.queueCircuitBreakUntil ?? .distantPast) > Date()
        if rt.phase.isBusy {
            HStack(spacing: 6) {
                ProgressView().controlSize(.small)
                Text("正在执行官方指令…").font(ColabTypography.subheadline).foregroundStyle(.secondary)
            }
        } else if rt.phase.isQueuing {
            Button {
                store.cancelQueue(for: acc.id)
            } label: {
                Label("取消排队", systemImage: "xmark.circle")
                    .font(.system(size: 12.5, weight: .semibold))
                    .foregroundStyle(ColabTheme.coral)
                    .frame(maxWidth: .infinity)
                    .padding(.vertical, 6)
                    .background(ColabTheme.coral.opacity(0.14), in: RoundedRectangle(cornerRadius: 8, style: .continuous))
            }
            .buttonStyle(.plain)
        } else if rt.phase.isRunning {
            Button {
                Task { await store.stopAccountSession(acc.id) }
            } label: {
                Label("关机止损", systemImage: "power")
                    .font(.system(size: 12.5, weight: .semibold))
                    .foregroundStyle(ColabTheme.coral)
                    .frame(maxWidth: .infinity)
                    .padding(.vertical, 6)
                    .background(ColabTheme.coral.opacity(0.14), in: RoundedRectangle(cornerRadius: 8, style: .continuous))
                    .overlay(RoundedRectangle(cornerRadius: 8, style: .continuous).strokeBorder(ColabTheme.coral.opacity(0.32), lineWidth: 0.7))
            }
            .buttonStyle(.plain)
        } else {
            let blocked = acc.enablePoliteQueue && isCoolingDown
            Button {
                Task { await store.startAccountSession(acc.id) }
            } label: {
                Label(acc.enablePoliteQueue ? "排队启动" : "一键启动", systemImage: acc.enablePoliteQueue ? "clock.arrow.circlepath" : "play.fill")
                    .font(.system(size: 12.5, weight: .semibold))
                    .foregroundStyle(blocked ? Color.secondary : Color(nsColor: .windowBackgroundColor))
                    .frame(maxWidth: .infinity)
                    .padding(.vertical, 6)
                    .background(blocked ? Color.primary.opacity(0.08) : Color.primary, in: RoundedRectangle(cornerRadius: 8, style: .continuous))
            }
            .buttonStyle(.plain)
            .disabled(blocked || !store.cliInstalled)
        }
    }

    // Bento 2: VRAM Segments
    private func bentoVRAMCard(rt: AccountRuntimeState) -> some View {
        let pct = Int((rt.vramRatio * 100).rounded())
        let status = rt.vramTotalMB == 0 ? "未加载" : (pct >= 94 ? "高占用" : "压力正常")
        return VStack(alignment: .leading, spacing: 10) {
            TileHeader(icon: "memorychip", iconTint: ColabTheme.emerald, title: "显存", badgeText: status, badgeTint: pct >= 94 ? ColabTheme.amber : nil)
            BigMetricValue(number: rt.vramTotalMB > 0 ? "\(pct)" : "—", unit: rt.vramTotalMB > 0 ? "%" : "", fontSize: 28)
            MicroSegmentsBar(progress: rt.vramRatio, tint: pct >= 94 ? ColabTheme.amber : ColabTheme.emerald, maxHeight: 24)
            Spacer(minLength: 0)
            Text(rt.vramTotalMB > 0 ? String(format: "%.1f GB / %.1f GB", Double(rt.vramUsedMB) / 1024.0, Double(rt.vramTotalMB) / 1024.0) : "等待 GPU 分配")
                .font(ColabTypography.subheadline)
                .monospacedDigit()
                .foregroundStyle(.secondary)
                .lineLimit(1)
        }
        .padding(16)
        .frame(height: 174)
        .bentoCard()
    }

    // Bento 3: GPU Utilization Sparkline
    private func bentoGPUCard(acc: AccountProfile, rt: AccountRuntimeState) -> some View {
        let billingWarning = !rt.phase.isRunning && rt.cuRatePerHour > 0
        let footerText: String = {
            if rt.phase.isRunning {
                let rateStr = rt.lastUsageFetchAt != nil || rt.cuRatePerHour > 0
                    ? String(format: "%.2f CU/h", rt.cuRatePerHour)
                    : "速率待同步"
                return rt.isProcessing ? "高负载 · \(rateStr)" : "低负载 · \(rateStr)"
            }
            if rt.cuRatePerHour > 0 {
                return String(format: "其他会话计费 · %.2f CU/h", rt.cuRatePerHour)
            }
            return rt.lastUsageFetchAt == nil ? "等待同步用量" : "当前未计费"
        }()
        return VStack(alignment: .leading, spacing: 10) {
            TileHeader(
                icon: "cpu",
                iconTint: ColabTheme.amber,
                title: "GPU",
                badgeText: rt.phase.isRunning ? rt.shortGPUName : (billingWarning ? "仍在计费" : acc.preferredAccelerator.shortChipLabel),
                badgeTint: billingWarning ? ColabTheme.amber : nil
            )
            BigMetricValue(number: rt.phase.isRunning ? "\(rt.gpuUtilPercent)" : "—", unit: rt.phase.isRunning ? "%" : "", fontSize: 28)
            MicroSparkline(
                points: rt.gpuUtilHistory.isEmpty ? [Double(rt.gpuUtilPercent), Double(rt.gpuUtilPercent)] : rt.gpuUtilHistory,
                tint: ColabTheme.amber,
                chartHeight: 26
            )
            Spacer(minLength: 0)
            Text(footerText)
                .font(ColabTypography.subheadline)
                .monospacedDigit()
                .foregroundStyle(billingWarning ? ColabTheme.amber : .secondary)
                .lineLimit(1)
        }
        .padding(16)
        .frame(height: 174)
        .bentoCard()
    }

    // Bento 4: Workload / Context Horizon Bar
    private func bentoWorkloadCard(acc: AccountProfile, rt: AccountRuntimeState) -> some View {
        let kind = acc.workloadKind == .general ? (rt.detectedWorkloadKind ?? .general) : acc.workloadKind
        let tint = ColabTheme.color(for: kind)
        let pct = Int((rt.contextRatio * 100).rounded())

        return VStack(alignment: .leading, spacing: 10) {
            if kind == .llm {
                TileHeader(icon: kind.iconName, iconTint: tint, title: "上下文", badgeText: "\(rt.contextLimit / 1024)K")
                BigMetricValue(
                    number: (rt.phase.isRunning && rt.serverHealthy) ? Formatters.tokens(rt.promptTokensUsed) : "—",
                    unit: (rt.phase.isRunning && rt.serverHealthy) ? "tok" : "",
                    fontSize: 28
                )
                HorizonBar(progress: rt.serverHealthy ? rt.contextRatio : 0, tint: tint, height: 8)
                    .padding(.vertical, 6)
                Spacer(minLength: 0)
                Text(rt.serverHealthy ? "已用 \(pct)% · \(rt.kvQuant) KV · #\(max(0, rt.lastTaskID))" : "等待模型服务在线")
                    .font(ColabTypography.subheadline)
                    .monospacedDigit()
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            } else {
                let port = rt.detectedServicePort ?? acc.remoteServicePort
                TileHeader(icon: kind.iconName, iconTint: tint, title: kind.metricCardTitle, badgeText: ":\(String(port))")
                BigMetricValue(
                    number: rt.phase.isRunning ? (rt.workloadMetricValue.isEmpty ? "\(rt.cpuUtilPercent)" : rt.workloadMetricValue) : "—",
                    unit: rt.phase.isRunning ? (rt.workloadMetricValue.isEmpty ? "% CPU" : rt.workloadMetricUnit) : "",
                    fontSize: 28
                )
                HorizonBar(
                    progress: rt.phase.isRunning ? (rt.workloadMetricRatio ?? (Double(rt.cpuUtilPercent) / 100.0)) : 0,
                    tint: tint,
                    height: 8
                )
                .padding(.vertical, 6)
                Spacer(minLength: 0)
                Text(rt.workloadDetail.isEmpty ? "\(kind.displayName) · CUDA \(rt.cudaProcessCount) 进程" : rt.workloadDetail)
                    .font(ColabTypography.subheadline)
                    .monospacedDigit()
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
        }
        .padding(16)
        .frame(height: 174)
        .bentoCard()
    }

    // Bento 5: Quota & Remaining Hours Ring Gauge (Mole Battery Card)
    private func bentoBatteryQuotaCard(acc: AccountProfile, rt: AccountRuntimeState) -> some View {
        let capSeconds = Double(max(1, acc.tier.maxSessionHours * 3600))
        let remainingRatio = rt.phase.isRunning ? Double(rt.remainingCapSeconds(for: acc.tier)) / capSeconds : 1.0
        let est = rt.estimatedHoursRemaining(for: acc)
        return VStack(alignment: .leading, spacing: 10) {
            TileHeader(
                icon: "bolt.batteryblock.fill",
                iconTint: ColabTheme.emerald,
                title: "算力续航",
                badgeText: "上限 \(acc.tier.maxSessionHours)h"
            )

            HStack(alignment: .center, spacing: 8) {
                if let est {
                    HStack(alignment: .firstTextBaseline, spacing: 3) {
                        Text(String(format: "%.0f", est.hours))
                            .font(.system(size: 28, weight: .bold, design: .rounded))
                            .monospacedDigit()
                            .lineLimit(1)
                            .minimumScaleFactor(0.65)
                        Text("小时")
                            .font(.system(size: 13.5, weight: .medium))
                            .foregroundStyle(.secondary)
                            .fixedSize()
                    }
                    .layoutPriority(1)
                } else {
                    BigMetricValue(number: acc.tier == .free ? "免费池" : "—", unit: "", fontSize: 24)
                }
                Spacer(minLength: 4)
                RingGauge(
                    progress: remainingRatio,
                    tint: remainingRatio < 0.15 ? ColabTheme.amber : ColabTheme.emerald,
                    icon: "clock.fill",
                    size: 42
                )
            }

            Spacer(minLength: 0)

            Text(
                rt.phase.isRunning
                    ? "本轮剩余 \(Formatters.shortMinutes(rt.remainingCapSeconds(for: acc.tier))) · \(acc.workloadKind.displayName)"
                    : "\(est.map { "按 \($0.basisLabel) 估算" } ?? acc.tier.displayName) · \(acc.workloadKind.displayName)"
            )
            .font(ColabTypography.subheadline)
            .monospacedDigit()
            .foregroundStyle(.secondary)
            .lineLimit(1)
            .truncationMode(.tail)
        }
        .padding(16)
        .frame(height: 174)
        .bentoCard()
    }

    // Bento 6: Idle Auto-Shutdown 3-Pill Selector (Mole Fan Card)
    private func bentoIdleWatchdogCard(acc: AccountProfile, rt: AccountRuntimeState) -> some View {
        let wd = store.watchdogStatus(for: acc)
        let remMin = max(0, (rt.remainingIdleSeconds(timeoutMinutes: acc.idleTimeoutMinutes) + 59) / 60)

        return VStack(alignment: .leading, spacing: 10) {
            HStack(spacing: 6) {
                Image(systemName: "timer")
                    .font(.system(size: 13, weight: .medium))
                    .foregroundStyle(acc.autoShutdownEnabled ? ColabTheme.cyan : .secondary)
                Text("空闲关机")
                    .font(ColabTypography.popoverSubMedium)
                    .foregroundStyle(.secondary)
                Spacer()
                Toggle("", isOn: binding(for: acc, \.autoShutdownEnabled))
                    .toggleStyle(.switch)
                    .tint(ColabTheme.emerald)
                    .controlSize(.mini)
                    .labelsHidden()
            }

            HStack(alignment: .firstTextBaseline) {
                if !acc.autoShutdownEnabled {
                    BigMetricValue(number: "OFF", unit: "", fontSize: 26)
                } else if !rt.phase.isRunning {
                    BigMetricValue(number: "\(acc.idleTimeoutMinutes)", unit: "min", fontSize: 26)
                } else if wd == .pausedNoSignal {
                    BigMetricValue(number: "暂停", unit: "", fontSize: 24)
                } else if rt.isProcessing {
                    BigMetricValue(number: "活跃", unit: "", fontSize: 24)
                } else {
                    BigMetricValue(number: "\(remMin)", unit: "min", fontSize: 26)
                }
                Spacer()
                if rt.phase.isRunning && acc.autoShutdownEnabled && wd != .pausedNoSignal {
                    Button("续命") {
                        store.resetIdleTimer(for: acc.id)
                    }
                    .buttonStyle(.bordered)
                    .controlSize(.small)
                }
            }

            // Mole-style 3-mode preset buttons
            HStack(spacing: 6) {
                ForEach([15, 40, 60], id: \.self) { m in
                    let selected = acc.idleTimeoutMinutes == m
                    Button {
                        var copy = acc
                        copy.idleTimeoutMinutes = m
                        copy.autoShutdownEnabled = true
                        store.updateAccount(copy)
                    } label: {
                        Text("\(m)m")
                            .font(.system(size: 12, weight: selected ? .semibold : .medium))
                            .monospacedDigit()
                            .frame(maxWidth: .infinity)
                            .padding(.vertical, 5)
                            .background(
                                selected ? ColabTheme.amber.opacity(0.24) : Color.primary.opacity(0.06),
                                in: RoundedRectangle(cornerRadius: 7, style: .continuous)
                            )
                            .foregroundStyle(selected ? .primary : .secondary)
                    }
                    .buttonStyle(.plain)
                }
            }

            Spacer(minLength: 0)

            Text(acc.autoShutdownEnabled ? (rt.phase.isRunning ? "空闲已计时 \(Formatters.shortMinutes(rt.idleSeconds))" : "启动后自动守护防烧点") : "已关闭，手动控制关机")
                .font(ColabTypography.subheadline)
                .foregroundStyle(.secondary)
                .lineLimit(1)
        }
        .padding(16)
        .frame(height: 174)
        .bentoCard()
    }

    // Bento 7: Queue Strategy & Schedule Summary
    private func bentoStrategyCard(acc: AccountProfile) -> some View {
        let targets = PoliteQueuePolicy.sanitizedTargets(selected: acc.queueTargets, for: acc.tier)
        let headline = acc.enablePoliteQueue ? PoliteQueuePolicy.targetsSummary(targets) : acc.preferredAccelerator.shortChipLabel
        return VStack(alignment: .leading, spacing: 10) {
            TileHeader(
                icon: "clock.arrow.circlepath",
                iconTint: ColabTheme.amber,
                title: "抢卡与预约",
                badgeText: acc.enablePoliteQueue ? "排队已开" : "单次启动",
                badgeTint: acc.enablePoliteQueue ? ColabTheme.amber : nil
            )

            Text(headline)
                .font(.system(size: 20, weight: .semibold, design: .rounded))
                .lineLimit(1)
                .minimumScaleFactor(0.75)

            HStack(spacing: 6) {
                if acc.enablePoliteQueue && acc.queueFallbackEnabled {
                    CardBadge(text: "\(acc.queueFallbackAfterRounds)轮保底 \(acc.queueFallbackTarget.shortChipLabel)")
                }
                if acc.scheduledStartEnabled {
                    CardBadge(
                        text: "预约 \(String(format: "%02d:%02d", acc.scheduledStartHour, acc.scheduledStartMinute))",
                        tint: ColabTheme.emerald
                    )
                }
                if !acc.scheduledStartEnabled && !(acc.enablePoliteQueue && acc.queueFallbackEnabled) {
                    Text("未设保底或定时预约")
                        .font(ColabTypography.subheadline)
                        .foregroundStyle(.tertiary)
                }
            }

            Spacer(minLength: 0)

            Button {
                withAnimation(.snappy(duration: 0.16)) { store.selectedMainSection = .compute }
            } label: {
                HStack {
                    Text("配置顺位、降级与预约")
                        .font(ColabTypography.subheadlineMedium)
                    Spacer()
                    Image(systemName: "chevron.right")
                        .font(.system(size: 11, weight: .semibold))
                }
                .foregroundStyle(.primary.opacity(0.85))
                .padding(.horizontal, 10)
                .padding(.vertical, 6)
                .background(Color.primary.opacity(0.06), in: RoundedRectangle(cornerRadius: 8, style: .continuous))
            }
            .buttonStyle(.plain)
        }
        .padding(16)
        .frame(height: 174)
        .bentoCard()
    }

    // Bento 8: Quick Integrations & Actions
    private func bentoIntegrationsQuickCard(acc: AccountProfile, rt: AccountRuntimeState) -> some View {
        let syncSt = integrationSyncStatus(for: acc, rt: rt)
        let badgeLabel = syncSt.allSynced ? "已同步" : (rt.sshTunnelActive ? "隧道在线" : "快捷入口")
        let badgeTint: Color? = syncSt.allSynced ? ColabTheme.emerald : (rt.sshTunnelActive ? ColabTheme.sky : nil)

        return VStack(alignment: .leading, spacing: 10) {
            TileHeader(icon: "puzzlepiece.extension", iconTint: ColabTheme.sky, title: "接入与快捷", badgeText: badgeLabel, badgeTint: badgeTint)

            VStack(spacing: 7) {
                HStack(spacing: 7) {
                    QuickActionButton(
                        title: syncSt.allSynced ? "已同步" : (rt.publicURL.isEmpty ? "配置接入" : "同步工具"),
                        icon: syncSt.allSynced ? "checkmark.circle.fill" : (rt.publicURL.isEmpty ? "slider.horizontal.3" : "arrow.triangle.2.circlepath"),
                        disabled: false,
                        active: syncSt.allSynced
                    ) {
                        if syncSt.hasTargets && !rt.publicURL.isEmpty {
                            store.syncAllEnabledClients(accountId: acc.id, silent: false)
                        } else {
                            withAnimation(.snappy(duration: 0.16)) { store.selectedMainSection = .integrations }
                        }
                    }

                    if acc.enableLLMModule {
                        QuickActionButton(
                            title: "Claude",
                            icon: "terminal",
                            disabled: !rt.phase.isRunning || rt.publicURL.isEmpty
                        ) {
                            store.launchClaudeTerminal(accountId: acc.id)
                        }
                    } else {
                        QuickActionButton(
                            title: acc.workloadKind.webUIButtonTitle,
                            icon: "safari",
                            disabled: !rt.phase.isRunning
                        ) {
                            store.openWorkloadWebUI(accountId: acc.id)
                        }
                    }
                }

                HStack(spacing: 7) {
                    QuickActionButton(
                        title: rt.sshTunnelActive ? "断隧道" : "SSH 隧道",
                        icon: rt.sshTunnelActive ? "lock.fill" : "lock.open",
                        disabled: (!rt.phase.isRunning && !rt.sshTunnelActive) || rt.isFreeRuntimeRestricted(for: acc),
                        active: rt.sshTunnelActive
                    ) {
                        store.toggleSSHTunnel(for: acc.id)
                    }

                    QuickActionButton(
                        title: "浏览器",
                        icon: "safari",
                        disabled: !rt.phase.isRunning
                    ) {
                        store.openColabInBrowser(accountId: acc.id)
                    }
                }
            }

            Spacer(minLength: 0)
        }
        .padding(16)
        .frame(height: 174)
        .bentoCard()
    }

    // MARK: - Bottom Mole-Style Live Sessions & Accounts Table
    private var liveSessionsAndAccountsTable: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(spacing: 10) {
                HStack(spacing: 10) {
                    Text("账号与活跃会话 (\(store.settings.accounts.count))")
                        .font(ColabTypography.sectionTitle)
                        .lineLimit(1)

                    if store.totalManagedRunningSessionCount >= 2 {
                        Button {
                            Task { await store.stopAllManagedSessions() }
                        } label: {
                            Label("全部关机止损", systemImage: "power")
                                .font(ColabTypography.subheadlineMedium)
                                .foregroundStyle(ColabTheme.coral)
                        }
                        .buttonStyle(.bordered)
                        .controlSize(.small)
                    }
                }
                .frame(minWidth: 220, maxWidth: .infinity, alignment: .leading)

                Text("硬件与形态")
                    .font(ColabTypography.subheadlineMedium)
                    .foregroundStyle(.tertiary)
                    .lineLimit(1)
                    .frame(width: colHardwareW, alignment: .leading)
                Text("显存占用")
                    .font(ColabTypography.subheadlineMedium)
                    .foregroundStyle(.tertiary)
                    .lineLimit(1)
                    .frame(width: colVRAMW, alignment: .leading)
                Text("CU 余额")
                    .font(ColabTypography.subheadlineMedium)
                    .foregroundStyle(.tertiary)
                    .lineLimit(1)
                    .frame(width: colCUW, alignment: .trailing)
                Text("消耗速率")
                    .font(ColabTypography.subheadlineMedium)
                    .foregroundStyle(.tertiary)
                    .lineLimit(1)
                    .frame(width: colRateW, alignment: .trailing)
                Text("快捷操作")
                    .font(ColabTypography.subheadlineMedium)
                    .foregroundStyle(.tertiary)
                    .lineLimit(1)
                    .frame(width: colActionW, alignment: .trailing)
            }
            .padding(.horizontal, 6)
            .padding(.bottom, 2)

            Divider().opacity(0.35)

            ForEach(Array(store.settings.accounts.enumerated()), id: \.element.id) { idx, acc in
                if idx > 0 { Divider().opacity(0.22) }
                let st = store.state(for: acc.id)
                let isSelected = acc.id == selectedAccount?.id
                accountTableRow(acc: acc, st: st, isSelected: isSelected)

                // Surface any non-primary / orphan running sessions directly below the account
                let extras = st.sessions.filter { $0.isOrphan || $0.name != acc.sessionName }
                ForEach(extras) { sess in
                    HStack(spacing: 10) {
                        HStack(spacing: 8) {
                            Image(systemName: "exclamationmark.triangle.fill")
                                .font(.system(size: 12))
                                .foregroundStyle(ColabTheme.amber)
                            Text(sess.isOrphan ? "外部孤儿会话 [?] · 请防忘关计费" : "附加会话：\(sess.name)")
                                .font(ColabTypography.subheadlineMedium)
                                .foregroundStyle(ColabTheme.amber)
                                .lineLimit(1)
                                .truncationMode(.tail)
                        }
                        .frame(minWidth: 220, maxWidth: .infinity, alignment: .leading)

                        Text("\(sess.hardware) · \(sess.shape)")
                            .font(ColabTypography.subheadline)
                            .foregroundStyle(.secondary)
                            .lineLimit(1)
                            .frame(width: colHardwareW, alignment: .leading)
                        Spacer().frame(width: colVRAMW)
                        Spacer().frame(width: colCUW)
                        Spacer().frame(width: colRateW)
                        HStack(spacing: 8) {
                            if !sess.isOrphan {
                                Button("停止") {
                                    Task { await store.stopAccountSession(acc.id, specificSession: sess.name) }
                                }
                                .buttonStyle(.bordered)
                                .controlSize(.small)
                            }
                        }
                        .frame(width: colActionW, alignment: .trailing)
                    }
                    .padding(.vertical, 5)
                    .padding(.leading, 22)
                    .padding(.trailing, 6)
                }
            }
        }
        .padding(18)
        .bentoCard(cornerRadius: 16)
    }

    private func accountTableRow(acc: AccountProfile, st: AccountRuntimeState, isSelected: Bool) -> some View {
        HStack(spacing: 10) {
            Button {
                store.settings.selectedAccountId = acc.id
            } label: {
                HStack(spacing: 10) {
                    Circle()
                        .fill(st.phase.isRunning ? ColabTheme.emerald : (st.phase.isQueuing ? ColabTheme.amber : ColabTheme.color(for: acc.colorTag)))
                        .frame(width: 9, height: 9)
                        .fixedSize()
                    VStack(alignment: .leading, spacing: 2) {
                        HStack(spacing: 6) {
                            Text(acc.displayName)
                                .font(ColabTypography.bodySemibold)
                                .foregroundStyle(.primary)
                                .lineLimit(1)
                                .truncationMode(.tail)
                                .layoutPriority(1)
                            CardBadge(text: acc.tier.shortBadge)
                                .fixedSize(horizontal: true, vertical: false)
                            if isSelected {
                                CardBadge(text: "焦点", tint: ColabTheme.sky)
                                    .fixedSize(horizontal: true, vertical: false)
                            }
                        }
                        Text(st.phase.isRunning ? "会话 \(acc.sessionName) · \(st.shortModelName)" : (acc.email.isEmpty ? "默认官方 CLI 账号" : acc.email))
                            .font(ColabTypography.subheadline)
                            .foregroundStyle(.secondary)
                            .lineLimit(1)
                            .truncationMode(.tail)
                    }
                }
                .frame(minWidth: 220, maxWidth: .infinity, alignment: .leading)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)

            Text(st.phase.isRunning ? "\(st.shortGPUName) · \(acc.highMem ? "High-RAM" : "Std")" : "已关闭")
                .font(ColabTypography.body)
                .foregroundStyle(st.phase.isRunning ? .primary : .tertiary)
                .frame(width: colHardwareW, alignment: .leading)
                .lineLimit(1)
                .truncationMode(.tail)

            // Mole-style inline bar + value
            HStack(spacing: 8) {
                if st.vramTotalMB > 0 {
                    HorizonBar(progress: st.vramRatio, tint: st.vramRatio >= 0.94 ? ColabTheme.amber : ColabTheme.emerald, height: 6)
                        .frame(width: 56)
                    Text(String(format: "%.1fG", Double(st.vramUsedMB) / 1024.0))
                        .font(ColabTypography.subheadline)
                        .monospacedDigit()
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                } else {
                    Text("—")
                        .font(ColabTypography.subheadline)
                        .foregroundStyle(.tertiary)
                }
            }
            .frame(width: colVRAMW, alignment: .leading)

            Text(Formatters.cu(st.cuBalance))
                .font(.system(size: 13.5, weight: .semibold, design: .rounded))
                .monospacedDigit()
                .lineLimit(1)
                .frame(width: colCUW, alignment: .trailing)

            let rateText: String = {
                if st.cuRatePerHour > 0 {
                    return String(format: "%.2f/h", st.cuRatePerHour)
                }
                return st.lastUsageFetchAt == nil ? "—" : "0.00/h"
            }()
            let offlineBilling = !st.phase.isRunning && st.cuRatePerHour > 0
            Text(rateText)
                .font(offlineBilling ? ColabTypography.subheadlineMedium : ColabTypography.subheadline)
                .monospacedDigit()
                .foregroundStyle(offlineBilling ? ColabTheme.amber : .secondary)
                .lineLimit(1)
                .frame(width: colRateW, alignment: .trailing)

            HStack(spacing: 8) {
                if st.phase.isRunning {
                    Button {
                        store.openColabInBrowser(accountId: acc.id)
                    } label: {
                        Image(systemName: "safari").font(.system(size: 13.5))
                    }
                    .buttonStyle(.borderless)
                    .help("浏览器打开")

                    Button {
                        store.openRemoteSSHShell(accountId: acc.id)
                    } label: {
                        Image(systemName: "terminal").font(.system(size: 13.5))
                    }
                    .buttonStyle(.borderless)
                    .help("打开 SSH 终端")

                    Button("关机") {
                        Task { await store.stopAccountSession(acc.id) }
                    }
                    .buttonStyle(.bordered)
                    .tint(ColabTheme.coral)
                    .controlSize(.small)
                } else if st.phase.isQueuing {
                    Button("取消排队") {
                        store.cancelQueue(for: acc.id)
                    }
                    .buttonStyle(.bordered)
                    .controlSize(.small)
                } else {
                    Button("启动") {
                        Task { await store.startAccountSession(acc.id) }
                    }
                    .buttonStyle(.bordered)
                    .controlSize(.small)
                    .disabled(!store.cliInstalled)
                }
            }
            .frame(width: colActionW, alignment: .trailing)
        }
        .padding(.vertical, 6)
        .padding(.horizontal, 6)
        .background(isSelected ? Color.primary.opacity(0.04) : Color.clear, in: RoundedRectangle(cornerRadius: 9, style: .continuous))
    }

    private var emptyWorkspaceHero: some View {
        VStack(spacing: 16) {
            ColabAppIconView(size: 64, cornerRadius: 16)
            Text("尚未绑定 Google Colab 账号")
                .font(ColabTypography.pageTitle)
            Text("完成一键环境体检与官方 OAuth2 授权，即可在这里实时监控点数与云端 GPU。")
                .font(ColabTypography.body)
                .foregroundStyle(.secondary)
            HStack(spacing: 12) {
                Button("打开开箱引导向导") {
                    store.reopenOnboarding()
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.large)

                Button("直接添加账号") {
                    showingAddAccountSheet = true
                }
                .buttonStyle(.bordered)
                .controlSize(.large)
            }
        }
        .frame(maxWidth: .infinity)
        .padding(44)
        .bentoCard(cornerRadius: 16)
    }

    // MARK: - Tab 2: Compute & Queue Workbench (Full HIG Typography)
    @ViewBuilder
    private var computeAndQueuePage: some View {
        VStack(alignment: .leading, spacing: 16) {
            pageHeader(title: "算力与抢卡", subtitle: "选好目标显卡顺位、保底降级与清晨错峰预约，让排队更省心且不触发官方风控。")

            if let acc = selectedAccount {
                let rt = store.state(for: acc.id)
                let locked = rt.phase.isRunning || rt.phase.isBusy || rt.phase.isQueuing
                let primaryTargets = PoliteQueuePolicy.sanitizedTargets(selected: acc.queueTargets, for: acc.tier)

                sectionCard(title: "目标显卡与缺货排队", trailing: acc.tier.displayName) {
                    HStack {
                        VStack(alignment: .leading, spacing: 3) {
                            Text("目标显卡缺货时自动排队")
                                .font(ColabTypography.bodySemibold)
                            Text("遇到官方暂无库存（503）自动按合规节奏重试，不必反复手动点击启动")
                                .font(ColabTypography.subheadline)
                                .foregroundStyle(.secondary)
                        }
                        Spacer()
                        Toggle("", isOn: binding(for: acc, \.enablePoliteQueue))
                            .toggleStyle(.switch)
                            .tint(ColabTheme.amber)
                            .labelsHidden()
                            .disabled(locked)
                    }

                    Divider().opacity(0.35)

                    if acc.enablePoliteQueue {
                        HStack {
                            Text("点击卡片勾选目标显卡，用 ↑ ↓ 调整尝试顺位")
                                .font(ColabTypography.subheadline)
                                .foregroundStyle(.secondary)
                            Spacer()
                            CardBadge(text: "顺位 \(PoliteQueuePolicy.targetsSummary(primaryTargets))", tint: ColabTheme.amber)
                        }

                        let allowed = acc.tier.queueableAccelerators
                        let cols = [GridItem(.flexible(), spacing: 12), GridItem(.flexible(), spacing: 12)]
                        LazyVGrid(columns: cols, spacing: 12) {
                            ForEach(allowed) { opt in
                                hardwareTargetCard(
                                    option: opt,
                                    rankIndex: primaryTargets.firstIndex(of: opt),
                                    totalSelected: primaryTargets.count,
                                    locked: locked,
                                    accountId: acc.id
                                )
                            }
                        }

                        if acc.tier.supportsHighMem {
                            Toggle("申请时同时请求高内存形态（High-RAM）", isOn: binding(for: acc, \.highMem))
                                .font(ColabTypography.body)
                                .foregroundStyle(.secondary)
                                .toggleStyle(.checkbox)
                                .disabled(locked)
                                .padding(.top, 4)
                        }
                    } else {
                        HStack {
                            VStack(alignment: .leading, spacing: 3) {
                                Text("单次启动默认显卡").font(ColabTypography.bodyMedium)
                                Text("未开启排队时，点击启动后直接按此选项尝试分配一次")
                                    .font(ColabTypography.subheadline)
                                    .foregroundStyle(.secondary)
                            }
                            Spacer()
                            Picker("", selection: binding(for: acc, \.preferredAccelerator)) {
                                ForEach(acc.tier.allowedAccelerators) { opt in
                                    Text(opt.label).tag(opt)
                                }
                            }
                            .labelsHidden()
                            .frame(width: 260)
                            .disabled(locked)
                        }
                    }
                }

                if acc.enablePoliteQueue && acc.tier.queueableAccelerators.count >= 2 {
                    let eligibleFallbacks = PoliteQueuePolicy.eligibleFallbackTargets(excluding: primaryTargets, for: acc.tier)
                    sectionCard(title: "两阶段自动降级（先冲高配，再保底）") {
                        HStack {
                            VStack(alignment: .leading, spacing: 3) {
                                Text("前几轮专注抢主力卡，仍缺货时自动加入保底卡")
                                    .font(ColabTypography.bodySemibold)
                                Text(
                                    eligibleFallbacks.isEmpty
                                        ? "当前订阅下所有可用硬件已全部加入主力顺位，如需独立保底请先取消勾选部分主力卡"
                                        : "既不错过稀缺旗舰卡，又避免苦等 40 分钟一无所获"
                                )
                                .font(ColabTypography.subheadline)
                                .foregroundStyle(.secondary)
                            }
                            Spacer()
                            Toggle("", isOn: binding(for: acc, \.queueFallbackEnabled))
                                .toggleStyle(.switch)
                                .tint(ColabTheme.amber)
                                .labelsHidden()
                                .disabled(locked || eligibleFallbacks.isEmpty)
                        }

                        if acc.queueFallbackEnabled && !eligibleFallbacks.isEmpty {
                            Divider().opacity(0.35)
                            HStack(spacing: 10) {
                                Text("前").font(ColabTypography.body)
                                Picker("", selection: binding(for: acc, \.queueFallbackAfterRounds)) {
                                    ForEach(1..<PoliteQueuePolicy.maxAttempts, id: \.self) { r in
                                        Text("\(r) 轮").tag(r)
                                    }
                                }
                                .labelsHidden()
                                .frame(width: 82)
                                .disabled(locked)

                                Text("仅尝试主力卡，第 \(acc.queueFallbackAfterRounds + 1) 轮起加入保底")
                                    .font(ColabTypography.body)
                                    .foregroundStyle(.secondary)

                                Picker("", selection: binding(for: acc, \.queueFallbackTarget)) {
                                    ForEach(eligibleFallbacks) { opt in
                                        Text("\(opt.shortChipLabel) (\(opt.roleTag))").tag(opt)
                                    }
                                }
                                .labelsHidden()
                                .frame(width: 170)
                                .disabled(locked)
                                Spacer()
                            }
                        }
                    }
                }

                sectionCard(title: "错峰定时预约（推荐清晨 06:00–08:00 低峰）") {
                    HStack {
                        VStack(alignment: .leading, spacing: 3) {
                            Text("按指定时间自动启动申请").font(ColabTypography.bodySemibold)
                            Text("避开欧美工作高峰，到点自动唤醒并按上方策略拿卡（错过超过 10 分钟自动过期不误启）")
                                .font(ColabTypography.subheadline)
                                .foregroundStyle(.secondary)
                        }
                        Spacer()
                        Toggle("", isOn: binding(for: acc, \.scheduledStartEnabled))
                            .toggleStyle(.switch)
                            .tint(ColabTheme.emerald)
                            .labelsHidden()
                    }

                    if acc.scheduledStartEnabled {
                        Divider().opacity(0.35)

                        HStack(spacing: 12) {
                            Text("预约时间").font(ColabTypography.bodyMedium)

                            Picker("", selection: binding(for: acc, \.scheduledStartHour)) {
                                ForEach(0..<24, id: \.self) { h in
                                    Text(String(format: "%02d 时", h)).tag(h)
                                }
                            }
                            .labelsHidden()
                            .frame(width: 84)

                            Picker("", selection: binding(for: acc, \.scheduledStartMinute)) {
                                ForEach([0, 15, 30, 45], id: \.self) { m in
                                    Text(String(format: "%02d 分", m)).tag(m)
                                }
                            }
                            .labelsHidden()
                            .frame(width: 84)

                            Spacer()

                            Picker("", selection: binding(for: acc, \.scheduledStartRepeatDaily)) {
                                Text("仅一次").tag(false)
                                Text("每天重复").tag(true)
                            }
                            .pickerStyle(.segmented)
                            .labelsHidden()
                            .frame(width: 170)
                        }

                        HStack(spacing: 8) {
                            Text("低峰快捷").font(ColabTypography.subheadline).foregroundStyle(.secondary)
                            ForEach([(6, 30, "06:30"), (7, 0, "07:00"), (7, 30, "07:30")], id: \.2) { h, m, label in
                                let active = acc.scheduledStartHour == h && acc.scheduledStartMinute == m
                                Button {
                                    var copy = acc
                                    copy.scheduledStartHour = h
                                    copy.scheduledStartMinute = m
                                    store.updateAccount(copy)
                                } label: {
                                    Text(label)
                                        .font(.system(size: 12, weight: active ? .semibold : .regular))
                                        .monospacedDigit()
                                        .fixedSize(horizontal: true, vertical: false)
                                        .padding(.horizontal, 10)
                                        .padding(.vertical, 5)
                                        .background(
                                            active ? ColabTheme.emerald.opacity(0.20) : Color.primary.opacity(0.06),
                                            in: RoundedRectangle(cornerRadius: 7, style: .continuous)
                                        )
                                        .foregroundStyle(active ? .primary : .secondary)
                                }
                                .buttonStyle(.plain)
                            }
                            Spacer()
                            let nextDate = acc.scheduledNextFireAt ?? PoliteQueuePolicy.nextScheduledFireDate(hour: acc.scheduledStartHour, minute: acc.scheduledStartMinute)
                            HStack(spacing: 5) {
                                Image(systemName: "alarm").font(.system(size: 12)).foregroundStyle(ColabTheme.emerald)
                                Text("下次：\(nextDate.formatted(date: .abbreviated, time: .shortened))")
                                    .font(ColabTypography.subheadline)
                                    .monospacedDigit()
                                    .foregroundStyle(.secondary)
                            }
                        }
                    }
                }

                // Local Allocation Hit-Rate Heatmap
                let legalInsightKeys = acc.tier.queueableAccelerators.map { AllocationInsights.normalizeHardwareKey($0.shortChipLabel) }
                let defaultHW = primaryTargets.first?.shortChipLabel ?? (acc.tier.queueableAccelerators.first?.shortChipLabel ?? "G4")
                let focusHW: String = {
                    if !selectedInsightHW.isEmpty,
                       legalInsightKeys.contains(AllocationInsights.normalizeHardwareKey(selectedInsightHW)) {
                        return selectedInsightHW
                    }
                    return defaultHW
                }()
                let slots = store.insights.sixSlotHeatmap(for: focusHW)
                let recText = store.insights.recommendedWindowSummary(for: focusHW)

                sectionCard(title: "本机时段命中率参考（纯本地统计 · 绝不上传）") {
                    HStack(spacing: 8) {
                        ScrollView(.horizontal, showsIndicators: false) {
                            HStack(spacing: 6) {
                                ForEach(acc.tier.queueableAccelerators) { opt in
                                    let key = opt.shortChipLabel
                                    let isSel = AllocationInsights.normalizeHardwareKey(focusHW) == AllocationInsights.normalizeHardwareKey(key)
                                    Button {
                                        selectedInsightHW = key
                                    } label: {
                                        Text(key)
                                            .font(.system(size: 12, weight: isSel ? .semibold : .regular))
                                            .fixedSize(horizontal: true, vertical: false)
                                            .padding(.horizontal, 10)
                                            .padding(.vertical, 4.5)
                                            .background(
                                                isSel ? ColabTheme.sky.opacity(0.20) : Color.primary.opacity(0.06),
                                                in: RoundedRectangle(cornerRadius: 7, style: .continuous)
                                            )
                                            .foregroundStyle(isSel ? .primary : .secondary)
                                    }
                                    .buttonStyle(.plain)
                                }
                            }
                        }
                        Spacer(minLength: 8)
                        Text("共 \(store.insights.totalAttempts(for: focusHW)) 次记录")
                            .font(ColabTypography.subheadline)
                            .monospacedDigit()
                            .foregroundStyle(.tertiary)
                            .fixedSize(horizontal: true, vertical: false)
                    }

                    HStack(alignment: .bottom, spacing: 10) {
                        ForEach(slots) { slot in
                            VStack(spacing: 6) {
                                Text(slot.rate.map { "\(Int(($0 * 100).rounded()))%" } ?? "—")
                                    .font(ColabTypography.footnoteMedium)
                                    .monospacedDigit()
                                    .foregroundStyle(slot.attempts > 0 ? .primary : .tertiary)

                                ZStack(alignment: .bottom) {
                                    RoundedRectangle(cornerRadius: 6, style: .continuous)
                                        .fill(Color.primary.opacity(0.065))
                                        .frame(height: 52)
                                    if let rate = slot.rate {
                                        RoundedRectangle(cornerRadius: 6, style: .continuous)
                                            .fill(rate >= 0.5 ? ColabTheme.emerald.opacity(0.84) : ColabTheme.amber.opacity(0.78))
                                            .frame(height: max(7, CGFloat(rate) * 52))
                                    }
                                }

                                Text(slot.label)
                                    .font(ColabTypography.footnote)
                                    .monospacedDigit()
                                    .foregroundStyle(.secondary)
                            }
                            .frame(maxWidth: .infinity)
                        }
                    }
                    .padding(.top, 4)

                    HStack(spacing: 6) {
                        Image(systemName: "sparkles").font(.system(size: 13)).foregroundStyle(ColabTheme.sky)
                        Text(recText ?? "每次申请会在本机静默记录成功/缺货，累积 3 次后自动为你推荐 \(focusHW) 黄金时段。")
                            .font(ColabTypography.subheadline)
                            .foregroundStyle(.secondary)
                    }
                }

                HStack(spacing: 8) {
                    Image(systemName: "shield.lefthalf.filled").font(.system(size: 14)).foregroundStyle(ColabTheme.emerald)
                    Text("防风控护栏始终开启：低频随机间隔重试（上限 5 轮 / 40 分钟），遇到限流（429）或超额（412）立即自动熔断并冷却 15 分钟。")
                        .font(ColabTypography.subheadline)
                        .foregroundStyle(.secondary)
                }
                .padding(.horizontal, 4)
            }
        }
    }

    private func hardwareTargetCard(
        option: AcceleratorOption,
        rankIndex: Int?,
        totalSelected: Int,
        locked: Bool,
        accountId: UUID
    ) -> some View {
        let isSelected = rankIndex != nil
        return HStack(spacing: 10) {
            Button {
                store.toggleQueueTarget(option, for: accountId)
            } label: {
                HStack(spacing: 10) {
                    ZStack {
                        RoundedRectangle(cornerRadius: 7, style: .continuous)
                            .fill(isSelected ? ColabTheme.amber.opacity(0.24) : Color.primary.opacity(0.06))
                            .frame(width: 28, height: 28)
                        if let idx = rankIndex {
                            Text("#\(idx + 1)")
                                .font(.system(size: 12.5, weight: .bold, design: .rounded))
                                .monospacedDigit()
                                .foregroundStyle(ColabTheme.amber)
                        } else {
                            Image(systemName: "plus")
                                .font(.system(size: 12, weight: .medium))
                                .foregroundStyle(.tertiary)
                        }
                    }

                    VStack(alignment: .leading, spacing: 3) {
                        HStack(spacing: 6) {
                            Text(option.shortChipLabel)
                                .font(ColabTypography.bodySemibold)
                                .foregroundStyle(isSelected ? .primary : .secondary)
                            if !option.roleTag.isEmpty {
                                Text(option.roleTag)
                                    .font(ColabTypography.footnoteMedium)
                                    .foregroundStyle(.secondary)
                                    .padding(.horizontal, 6)
                                    .padding(.vertical, 2)
                                    .background(Color.primary.opacity(0.065), in: RoundedRectangle(cornerRadius: 5))
                            }
                        }
                        Text(option.specSubtitle)
                            .font(ColabTypography.subheadline)
                            .foregroundStyle(.tertiary)
                            .lineLimit(1)
                    }
                    Spacer(minLength: 0)
                }
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .disabled(locked)

            if let idx = rankIndex, totalSelected >= 2 {
                VStack(spacing: 4) {
                    Button {
                        store.moveQueueTarget(option, direction: -1, for: accountId)
                    } label: {
                        Image(systemName: "chevron.up").font(.system(size: 11, weight: .semibold)).frame(width: 22, height: 16)
                    }
                    .buttonStyle(.plain)
                    .foregroundStyle(idx == 0 ? .quaternary : .secondary)
                    .disabled(locked || idx == 0)

                    Button {
                        store.moveQueueTarget(option, direction: 1, for: accountId)
                    } label: {
                        Image(systemName: "chevron.down").font(.system(size: 11, weight: .semibold)).frame(width: 22, height: 16)
                    }
                    .buttonStyle(.plain)
                    .foregroundStyle(idx == totalSelected - 1 ? .quaternary : .secondary)
                    .disabled(locked || idx == totalSelected - 1)
                }
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 10)
        .background(
            isSelected ? ColabTheme.amber.opacity(0.09) : Color.primary.opacity(0.032),
            in: RoundedRectangle(cornerRadius: 11, style: .continuous)
        )
        .overlay(
            RoundedRectangle(cornerRadius: 11, style: .continuous)
                .strokeBorder(isSelected ? ColabTheme.amber.opacity(0.48) : Color.primary.opacity(0.08), lineWidth: 0.8)
        )
    }

    // MARK: - Tab 3: Integrations Hub (Full HIG Typography)
    @ViewBuilder
    private var integrationsPage: some View {
        VStack(alignment: .leading, spacing: 16) {
            HStack(alignment: .top) {
                pageHeader(title: "接入工具", subtitle: "自动检测本机 AI 编程客户端，支持无感安全写入或双协议一键复制。")
                Spacer()
                Button {
                    store.refreshClientDetection()
                    store.showToast("已重新扫描本机已安装的客户端")
                } label: {
                    Label("重新检测", systemImage: "arrow.clockwise")
                }
                .buttonStyle(.bordered)
                .controlSize(.regular)
            }

            if let acc = selectedAccount {
                let rt = store.state(for: acc.id)
                let det = store.clientDetection
                let hasEndpoint = !rt.publicURL.isEmpty

                sectionCard(title: "原生安全同步（按账号独立隔离 · 写前 0600 备份 · 绝不改官方默认）") {
                    let orderedClients: [(key: String, installed: Bool)] = [
                        ("ccswitch", det.ccSwitchInstalled),
                        ("opencode", det.openCodeInstalled),
                        ("codex", det.codexInstalled),
                        ("claude", det.claudeCodeInstalled)
                    ].sorted { ($0.installed ? 0 : 1) < ($1.installed ? 0 : 1) }

                    ForEach(Array(orderedClients.enumerated()), id: \.element.key) { idx, item in
                        if idx > 0 { Divider().opacity(0.35) }
                        switch item.key {
                        case "ccswitch":
                            nativeClientRow(
                                title: "CC Switch",
                                installed: det.ccSwitchInstalled,
                                synced: rt.ccSwitchSynced,
                                subtitle: "写入独立待机渠道（WAL 安全事务 + 备份），不改动当前激活的官方订阅",
                                toggleBinding: binding(for: acc, \.autoSyncCCSwitch),
                                canSyncNow: det.ccSwitchInstalled && hasEndpoint,
                                onSyncNow: { store.syncToCCSwitch(accountId: acc.id, silent: false) }
                            )
                            if acc.autoSyncCCSwitch {
                                HStack {
                                    Text("渠道显示名称").font(ColabTypography.body).foregroundStyle(.secondary)
                                    Spacer()
                                    TextField("Colab Tools", text: binding(for: acc, \.ccSwitchProviderName))
                                        .textFieldStyle(.roundedBorder)
                                        .frame(width: 280)
                                }
                                .padding(.leading, 4)
                            }

                        case "opencode":
                            let ocIds = ClientIntegrations.identifiers(for: acc.id)
                            nativeClientRow(
                                title: "OpenCode",
                                installed: det.openCodeInstalled,
                                synced: rt.openCodeSynced,
                                subtitle: "按账号独立合并至 opencode.json（不存明文 Key，引用环境变量 \(ocIds.envKey.prefix(28))…）",
                                toggleBinding: binding(for: acc, \.autoSyncOpenCode),
                                canSyncNow: hasEndpoint,
                                onSyncNow: { store.syncToOpenCode(accountId: acc.id, silent: false) }
                            )

                        case "codex":
                            nativeClientRow(
                                title: "Codex CLI",
                                installed: det.codexInstalled,
                                synced: rt.codexSynced,
                                subtitle: "按账号独立托管区块写入 ~/.codex/config.toml（仅 env_key，不改动官方默认渠道）",
                                toggleBinding: binding(for: acc, \.autoSyncCodex),
                                canSyncNow: hasEndpoint,
                                onSyncNow: { store.syncToCodex(accountId: acc.id, silent: false) }
                            )

                        default:
                            HStack(alignment: .center, spacing: 12) {
                                VStack(alignment: .leading, spacing: 3) {
                                    HStack(spacing: 8) {
                                        Text("Claude Code").font(ColabTypography.bodySemibold)
                                        CardBadge(
                                            text: det.claudeCodeInstalled ? "已检测到" : "未安装",
                                            tint: det.claudeCodeInstalled ? ColabTheme.emerald : nil
                                        )
                                    }
                                    Text("通过一次性自清理脚本安全注入环境变量拉起终端，API Key 不留命令历史")
                                        .font(ColabTypography.subheadline)
                                        .foregroundStyle(.secondary)
                                        .fixedSize(horizontal: false, vertical: true)
                                }
                                Spacer()
                                Button("安全拉起终端") {
                                    store.launchClaudeTerminal(accountId: acc.id)
                                }
                                .buttonStyle(.bordered)
                                .controlSize(.regular)
                                .disabled(!rt.phase.isRunning || !hasEndpoint)
                            }
                        }
                    }
                }

                sectionCard(title: "通用接入（一键复制给其他客户端）") {
                    let ids = ClientIntegrations.identifiers(for: acc.id)
                    let liveURL: String? = {
                        if hasEndpoint { return rt.publicURL }
                        if rt.sshTunnelActive, let bp = rt.activeSSHTunnelPort {
                            return "http://127.0.0.1:\(String(bp))"
                        }
                        return nil
                    }()

                    Text("适用于 Cherry Studio · Cline · Roo Code · Cursor · Claude Code Router · ccm 等任意支持标准 API 的工具")
                        .font(ColabTypography.subheadline)
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)

                    HStack(spacing: 10) {
                        HStack(spacing: 4) {
                            ForEach(CopyProtocolKind.allCases) { proto in
                                let active = selectedCopyProtocol == proto
                                Button {
                                    selectedCopyProtocol = proto
                                } label: {
                                    Text(proto.rawValue)
                                        .font(.system(size: 12.5, weight: active ? .semibold : .regular))
                                        .padding(.horizontal, 12)
                                        .padding(.vertical, 5.5)
                                        .background(
                                            active ? Color.primary.opacity(0.14) : Color.clear,
                                            in: RoundedRectangle(cornerRadius: 7, style: .continuous)
                                        )
                                        .foregroundStyle(active ? .primary : .secondary)
                                }
                                .buttonStyle(.plain)
                            }
                        }
                        .padding(3)
                        .background(Color.primary.opacity(0.055), in: RoundedRectangle(cornerRadius: 9, style: .continuous))

                        Spacer()

                        CardBadge(
                            text: liveURL == nil ? "启动后自动填入实测地址" : "实时端点已就绪",
                            tint: liveURL == nil ? ColabTheme.amber : ColabTheme.emerald
                        )
                    }

                    let previewURL = liveURL ?? "https://your-colab-endpoint.trycloudflare.com"
                    let previewText = ClientIntegrations.exportEnvSnippet(
                        protocolKind: selectedCopyProtocol,
                        baseURL: previewURL,
                        modelAlias: rt.modelAlias,
                        apiKey: "",
                        envKey: ids.envKey,
                        includeRawSecret: false
                    )

                    Text(previewText)
                        .font(ColabTypography.mono)
                        .foregroundStyle(.primary.opacity(0.90))
                        .lineSpacing(4)
                        .fixedSize(horizontal: false, vertical: true)
                        .textSelection(.enabled)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(12)
                        .background(Color.primary.opacity(0.048), in: RoundedRectangle(cornerRadius: 9, style: .continuous))

                    HStack(spacing: 10) {
                        Button {
                            store.copyClientSnippet(accountId: acc.id, protocolKind: selectedCopyProtocol, format: .env, includeRawSecret: false)
                        } label: {
                            Label("复制安全模板", systemImage: "terminal")
                        }
                        .buttonStyle(.borderedProminent)
                        .controlSize(.regular)

                        Button {
                            store.copyClientSnippet(accountId: acc.id, protocolKind: selectedCopyProtocol, format: .json, includeRawSecret: false)
                        } label: {
                            Label("复制 JSON", systemImage: "curlybraces")
                        }
                        .buttonStyle(.bordered)
                        .controlSize(.regular)

                        Button {
                            let urlToCopy = selectedCopyProtocol == .openAI
                                ? ClientIntegrations.openAIBaseURL(from: previewURL)
                                : previewURL
                            store.copyToClipboard(urlToCopy, label: "\(selectedCopyProtocol.rawValue) 地址")
                        } label: {
                            Label("仅复制地址", systemImage: "link")
                        }
                        .buttonStyle(.bordered)
                        .controlSize(.regular)

                        Spacer()

                        Button {
                            store.copyClientSnippet(accountId: acc.id, protocolKind: selectedCopyProtocol, format: .env, includeRawSecret: true)
                        } label: {
                            Label("复制含真实密钥", systemImage: "key")
                        }
                        .buttonStyle(.bordered)
                        .controlSize(.regular)
                        .disabled(liveURL == nil)
                        .help("仅在线时可用：将 Keychain 中的真实 API Key 写入剪贴板")
                    }
                }
            }
        }
    }

    private func nativeClientRow(
        title: String,
        installed: Bool,
        synced: Bool,
        subtitle: String,
        toggleBinding: Binding<Bool>,
        canSyncNow: Bool,
        onSyncNow: @escaping () -> Void
    ) -> some View {
        HStack(alignment: .center, spacing: 12) {
            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 8) {
                    Text(title).font(ColabTypography.bodySemibold)
                    CardBadge(text: installed ? "已检测到" : "未检测到", tint: installed ? ColabTheme.emerald : nil)
                    if synced {
                        CardBadge(text: "已同步", tint: ColabTheme.sky)
                    }
                }
                Text(subtitle)
                    .font(ColabTypography.subheadline)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
            Spacer(minLength: 10)
            Button("立即同步", action: onSyncNow)
                .buttonStyle(.bordered)
                .controlSize(.regular)
                .disabled(!canSyncNow)

            Toggle("", isOn: toggleBinding)
                .toggleStyle(.switch)
                .tint(ColabTheme.emerald)
                .labelsHidden()
        }
    }

    // MARK: - Tab 4: Accounts
    @ViewBuilder
    private var accountsPage: some View {
        VStack(alignment: .leading, spacing: 16) {
            HStack(alignment: .top) {
                pageHeader(title: "账号管理", subtitle: "多订阅账号彼此钥匙串隔离，可并行监控点数与独立配置算力。")
                Spacer()
                Button {
                    showingAddAccountSheet = true
                } label: {
                    Label("添加账号", systemImage: "person.badge.plus")
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.regular)
            }

            sectionCard(title: "已绑定账号 (\(store.settings.accounts.count))") {
                ForEach(Array(store.settings.accounts.enumerated()), id: \.element.id) { idx, acc in
                    if idx > 0 { Divider().opacity(0.35) }
                    let st = store.state(for: acc.id)
                    let isSel = acc.id == selectedAccount?.id
                    HStack(spacing: 12) {
                        Circle().fill(ColabTheme.color(for: acc.colorTag)).frame(width: 10, height: 10)
                        VStack(alignment: .leading, spacing: 3) {
                            HStack(spacing: 8) {
                                Text(acc.displayName).font(ColabTypography.bodySemibold)
                                CardBadge(text: acc.tier.shortBadge)
                                CardBadge(text: acc.authMode == .systemDefaultOAuth2 ? "默认 OAuth2" : "独立钥匙串")
                                if isSel { CardBadge(text: "当前焦点", tint: ColabTheme.sky) }
                            }
                            Text(acc.email.isEmpty ? "默认官方 CLI 账号" : acc.email)
                                .font(ColabTypography.subheadline)
                                .foregroundStyle(.secondary)
                        }
                        Spacer()
                        Text("\(Formatters.cu(st.cuBalance)) CU")
                            .font(.system(size: 14, weight: .semibold, design: .rounded))
                            .monospacedDigit()
                            .foregroundStyle(.secondary)
                        if !isSel {
                            Button("设为焦点") {
                                store.settings.selectedAccountId = acc.id
                            }
                            .buttonStyle(.bordered)
                            .controlSize(.small)
                        }
                        if store.settings.accounts.count > 1 || acc.authMode == .isolatedKeychainADC {
                            Button {
                                store.removeAccount(acc.id)
                            } label: {
                                Image(systemName: "trash").font(.system(size: 13)).foregroundStyle(ColabTheme.coral)
                            }
                            .buttonStyle(.borderless)
                            .help("移除此账号并清除对应钥匙串凭证")
                        }
                    }
                    .padding(.vertical, 3)
                }
            }

            if let acc = selectedAccount {
                sectionCard(title: "当前账号资料", trailing: acc.displayName) {
                    HStack {
                        Text("账号备注").font(ColabTypography.body).frame(width: 90, alignment: .leading)
                        TextField("账号名称", text: binding(for: acc, \.displayName))
                            .textFieldStyle(.roundedBorder)
                            .frame(maxWidth: 280)
                        Spacer()
                    }
                    HStack {
                        Text("订阅档位").font(ColabTypography.body).frame(width: 90, alignment: .leading)
                        Picker("", selection: binding(for: acc, \.tier)) {
                            ForEach(SubscriptionTier.allCases) { tier in
                                Text(tier.displayName).tag(tier)
                            }
                        }
                        .labelsHidden()
                        .frame(width: 210)
                        Button("管理订阅 / 购点…") {
                            store.openPayPage()
                        }
                        .buttonStyle(.bordered)
                        Spacer()
                    }
                    HStack(spacing: 12) {
                        Text("颜色标记").font(ColabTypography.body).frame(width: 90, alignment: .leading)
                        ForEach(AccountColorTag.allCases) { tag in
                            Button {
                                var copy = acc
                                copy.colorTag = tag
                                store.updateAccount(copy)
                            } label: {
                                Circle()
                                    .fill(ColabTheme.color(for: tag))
                                    .frame(width: 20, height: 20)
                                    .overlay {
                                        if acc.colorTag == tag {
                                            Circle().strokeBorder(Color.primary, lineWidth: 1.8).padding(-3)
                                        }
                                    }
                            }
                            .buttonStyle(.plain)
                        }
                        Spacer()
                    }
                }
            }
        }
    }

    // MARK: - Tab 5: Settings (General + Doctor + Advanced + About)
    @ViewBuilder
    private var settingsPage: some View {
        VStack(alignment: .leading, spacing: 16) {
            HStack(alignment: .center) {
                pageHeader(title: "设置", subtitle: "所有偏好修改即时生效，无需手动保存。")
                Spacer()
                Picker("", selection: $settingsSubTab) {
                    ForEach(SettingsSubTab.allCases) { tab in
                        Text(tab.rawValue).tag(tab)
                    }
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .frame(width: 360)
            }

            switch settingsSubTab {
            case .general:
                settingsGeneralBlock
            case .doctor:
                settingsDoctorBlock
            case .advanced:
                settingsAdvancedBlock
            case .about:
                settingsAboutBlock
            }
        }
    }

    @ViewBuilder
    private var settingsGeneralBlock: some View {
        sectionCard(title: "应用与菜单栏") {
            HStack {
                VStack(alignment: .leading, spacing: 3) {
                    HStack(spacing: 8) {
                        Text("开机自动启动").font(ColabTypography.bodySemibold)
                        if loginItemStatus == .requiresApproval {
                            CardBadge(text: "待系统批准", tint: ColabTheme.amber)
                        }
                    }
                    Text(
                        loginItemStatus == .requiresApproval
                            ? "已提交开机自启申请，请在 macOS「系统设置 → 登录项」中允许启用"
                            : "登录 macOS 后自动常驻顶部菜单栏"
                    )
                    .font(ColabTypography.subheadline)
                    .foregroundStyle(.secondary)
                }
                Spacer()
                if loginItemStatus == .requiresApproval {
                    Button("打开登录项设置") { SMAppService.openSystemSettingsLoginItems() }
                        .buttonStyle(.bordered)
                }
                Toggle("", isOn: Binding(
                    get: { loginItemStatus == .enabled || loginItemStatus == .requiresApproval },
                    set: { toggleLaunchAtLogin($0) }
                ))
                .toggleStyle(.switch)
                .labelsHidden()
            }

            Divider().opacity(0.35)

            HStack {
                VStack(alignment: .leading, spacing: 3) {
                    Text("菜单栏显示信息").font(ColabTypography.bodySemibold)
                    Text("在顶部状态栏实时查看显卡与剩余空闲时间")
                        .font(ColabTypography.subheadline)
                        .foregroundStyle(.secondary)
                }
                Spacer()
                Picker("", selection: $store.settings.menuBarMode) {
                    ForEach(MenuBarDisplayMode.allCases) { mode in
                        Text(mode.label).tag(mode)
                    }
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .frame(width: 260)
            }

            Divider().opacity(0.35)

            HStack {
                VStack(alignment: .leading, spacing: 3) {
                    Text("桌面通知提醒").font(ColabTypography.bodySemibold)
                    Text("排到卡、预约触发、空闲预警与自动关机时推送通知（计费止损警告始终保留）")
                        .font(ColabTypography.subheadline)
                        .foregroundStyle(.secondary)
                }
                Spacer()
                Toggle("", isOn: $store.settings.enableNotifications)
                    .toggleStyle(.switch)
                    .labelsHidden()
            }
        }

        if let acc = selectedAccount {
            sectionCard(title: "空闲自动关机（防止忘记关机浪费点数）", trailing: acc.displayName) {
                HStack {
                    VStack(alignment: .leading, spacing: 3) {
                        Text("开启空闲自动关机").font(ColabTypography.bodySemibold)
                        Text("确认长时间无任务运行后自动释放实例停止计费")
                            .font(ColabTypography.subheadline)
                            .foregroundStyle(.secondary)
                    }
                    Spacer()
                    Toggle("", isOn: binding(for: acc, \.autoShutdownEnabled))
                        .toggleStyle(.switch)
                        .tint(ColabTheme.emerald)
                        .labelsHidden()
                }

                if acc.autoShutdownEnabled {
                    Divider().opacity(0.35)
                    HStack(spacing: 10) {
                        Text("连续空闲多久关机").font(ColabTypography.body)
                        Spacer()
                        ForEach([15, 30, 40, 60], id: \.self) { m in
                            let active = acc.idleTimeoutMinutes == m
                            Button {
                                var copy = acc
                                copy.idleTimeoutMinutes = m
                                store.updateAccount(copy)
                            } label: {
                                Text("\(m) 分钟")
                                    .font(.system(size: 12.5, weight: active ? .semibold : .regular))
                                    .monospacedDigit()
                                    .padding(.horizontal, 10)
                                    .padding(.vertical, 5)
                                    .background(
                                        active ? ColabTheme.amber.opacity(0.22) : Color.primary.opacity(0.06),
                                        in: RoundedRectangle(cornerRadius: 7, style: .continuous)
                                    )
                                    .foregroundStyle(active ? .primary : .secondary)
                            }
                            .buttonStyle(.plain)
                        }
                        TextField("", value: binding(for: acc, \.idleTimeoutMinutes), format: .number.grouping(.never))
                            .textFieldStyle(.roundedBorder)
                            .frame(width: 60)
                            .multilineTextAlignment(.trailing)
                        Text("分").font(ColabTypography.subheadline).foregroundStyle(.secondary)
                    }

                    HStack {
                        Text("关机前提前提醒").font(ColabTypography.body)
                        Spacer()
                        Stepper(value: binding(for: acc, \.warnBeforeMinutes), in: 0...15) {
                            Text(acc.warnBeforeMinutes == 0 ? "不提前提醒" : "提前 \(acc.warnBeforeMinutes) 分钟")
                                .font(ColabTypography.body)
                                .monospacedDigit()
                        }
                    }

                    if !acc.enableLLMModule {
                        Divider().opacity(0.35)
                        HStack {
                            VStack(alignment: .leading, spacing: 3) {
                                Text("无应用信号时启用硬件双低载检测").font(ColabTypography.bodyMedium)
                                Text("仅在 GPU < 5% 且整机 CPU < 25% 同时满足时计时，避免下载权重或转码时误关")
                                    .font(ColabTypography.subheadline)
                                    .foregroundStyle(.secondary)
                            }
                            Spacer()
                            Toggle("", isOn: binding(for: acc, \.enableHardwareIdleFallback))
                                .toggleStyle(.switch)
                                .labelsHidden()
                        }
                    }
                }
            }
        }
    }

    @ViewBuilder
    private var settingsDoctorBlock: some View {
        let env = store.envReport
        sectionCard(title: "官方 CLI 环境体检与修复") {
            HStack {
                VStack(alignment: .leading, spacing: 3) {
                    Text("google-colab-cli 状态").font(ColabTypography.bodySemibold)
                    Text(
                        store.cliInstalled
                            ? "已安装\(env.colabVersion.map { " v\($0)" } ?? "") · \(env.colabPath ?? "")"
                            : (env.colabBrokenReason ?? "未检测到可执行文件")
                    )
                        .font(ColabTypography.subheadline)
                        .foregroundStyle(.secondary)
                }
                Spacer()
                CardBadge(text: store.cliInstalled ? "正常" : "未安装", tint: store.cliInstalled ? ColabTheme.emerald : ColabTheme.coral)
            }

            Divider().opacity(0.35)

            HStack {
                VStack(alignment: .leading, spacing: 3) {
                    Text("本机包管理工具").font(ColabTypography.bodySemibold)
                    Text(env.packageManagerSummary)
                        .font(ColabTypography.subheadline)
                        .foregroundStyle(.secondary)
                }
                Spacer()
                Button(store.isCheckingEnvironment ? "体检中…" : "重新体检") {
                    Task { await store.refreshEnvironmentCheck() }
                }
                .buttonStyle(.bordered)
                .disabled(store.isCheckingEnvironment)
            }

            Divider().opacity(0.35)

            HStack(spacing: 10) {
                Button {
                    store.reopenOnboarding()
                } label: {
                    Label("重新打开 3 步开箱安装与新手引导", systemImage: "wand.and.stars")
                }
                .buttonStyle(.borderedProminent)

                Button("复制官方安装命令") {
                    store.copyToClipboard(env.recommendedMethod.manualShellCommand, label: "CLI 安装命令")
                }
                .buttonStyle(.bordered)
                Spacer()
            }
        }
    }

    @ViewBuilder
    private var settingsAdvancedBlock: some View {
        if let acc = selectedAccount {
            let rt = store.state(for: acc.id)
            sectionCard(title: "工作负载、端口与远端脚本", trailing: acc.displayName) {
                HStack {
                    Text("负载模式").font(ColabTypography.body).frame(width: 96, alignment: .leading)
                    Picker("", selection: binding(for: acc, \.workloadKind)) {
                        ForEach(WorkloadKind.allCases) { k in
                            Text("\(k.displayName) (默认 :\(String(k.defaultRemotePort)))").tag(k)
                        }
                    }
                    .labelsHidden()
                    .frame(width: 260)
                    Spacer()
                }

                HStack {
                    Text("默认会话名").font(ColabTypography.body).frame(width: 96, alignment: .leading)
                    TextField("llm-coder", text: binding(for: acc, \.sessionName))
                        .textFieldStyle(.roundedBorder)
                        .frame(width: 220)
                    Spacer()
                }

                HStack(spacing: 12) {
                    Text("端口映射").font(ColabTypography.body).frame(width: 96, alignment: .leading)
                    Text("远端").font(ColabTypography.subheadline).foregroundStyle(.secondary)
                    TextField("18080", text: $remotePortDraft)
                        .textFieldStyle(.roundedBorder)
                        .frame(width: 84)
                        .multilineTextAlignment(.trailing)
                        .onSubmit { commitPortDraft(isRemote: true, account: acc) }

                    Text("本地 SSH 隧道").font(ColabTypography.subheadline).foregroundStyle(.secondary)
                    TextField("18080", text: $localPortDraft)
                        .textFieldStyle(.roundedBorder)
                        .frame(width: 84)
                        .multilineTextAlignment(.trailing)
                        .onSubmit { commitPortDraft(isRemote: false, account: acc) }

                    Text("回车生效 (1024–65535)").font(ColabTypography.footnote).foregroundStyle(.tertiary)
                    Spacer()
                }

                if rt.sshTunnelActive, let bound = rt.activeSSHTunnelPort {
                    Text("当前 SSH 隧道已绑定 127.0.0.1:\(String(bound))，修改本地端口需断开并重连隧道后生效。")
                        .font(ColabTypography.subheadline)
                        .foregroundStyle(ColabTheme.sky)
                }

                VStack(alignment: .leading, spacing: 6) {
                    Text(acc.workloadKind == .llm ? "Drive 远端 LLM 启动脚本路径" : "Drive 远端工作负载脚本（可选，留空仅分配算力）")
                        .font(ColabTypography.subheadline)
                        .foregroundStyle(.secondary)
                    TextField(
                        acc.workloadKind.defaultScriptHint.isEmpty ? "/content/drive/MyDrive/colab-tools/start.sh" : acc.workloadKind.defaultScriptHint,
                        text: binding(for: acc, \.driveScriptPath)
                    )
                    .textFieldStyle(.roundedBorder)
                    .font(ColabTypography.mono)
                }
            }

            sectionCard(title: "轮询节奏、维护与数据目录") {
                HStack {
                    VStack(alignment: .leading, spacing: 3) {
                        Text("后台自动同步间隔").font(ColabTypography.bodySemibold)
                        Text("多账号自动错峰串行查询，避免高频请求触发限流")
                            .font(ColabTypography.subheadline)
                            .foregroundStyle(.secondary)
                    }
                    Spacer()
                    Stepper(value: $store.settings.backgroundRefreshSeconds, in: 60...900, step: 60) {
                        Text("\(store.settings.backgroundRefreshSeconds / 60) 分钟")
                            .font(ColabTypography.body)
                            .monospacedDigit()
                    }
                }

                Divider().opacity(0.35)

                HStack(spacing: 10) {
                    Button("打开配置与备份目录") { NSWorkspace.shared.open(AppSettings.configDir) }
                        .buttonStyle(.bordered)

                    Button("清空本机时段统计") { store.clearAllocationInsights() }
                        .buttonStyle(.bordered)

                    if rt.lastError != nil {
                        Button("清除错误提示") { store.clearDisplayedError(for: acc.id) }
                            .buttonStyle(.bordered)
                    }

                    if let until = rt.queueCircuitBreakUntil, until > Date() {
                        let remMin = max(1, Int(ceil(until.timeIntervalSinceNow / 60.0)))
                        CardBadge(text: "防风控冷却剩余 \(remMin)m（不可跳过）", tint: ColabTheme.coral)
                    }
                    Spacer()
                }
            }
        }
    }

    @ViewBuilder
    private var settingsAboutBlock: some View {
        VStack(spacing: 14) {
            VStack(spacing: 10) {
                ColabAppIconView(size: 76, cornerRadius: 18)
                    .shadow(color: .black.opacity(0.18), radius: 8, y: 3)
                Text("Colab Tools").font(ColabTypography.pageTitle)
                Text("v0.3.6 · MIT Open Source").font(ColabTypography.subheadline).foregroundStyle(.secondary)
                Text("面向全体 Google AI / Gemini 与 Colab 订阅用户的原生 macOS 控制台与菜单栏伴侣。基于官方 google-colab-cli，支持一键环境体检安装、多订阅隔离、合规排队与错峰预约、全模态负载监控及多客户端生态接入。")
                    .font(ColabTypography.body)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
                    .frame(maxWidth: 540)
            }
            .frame(maxWidth: .infinity)
            .padding(24)
            .bentoCard(cornerRadius: 16)

            sectionCard(title: "开源仓库与作者") {
                aboutLinkRow(title: "GitHub 开源主页", subtitle: "github.com/LJMStark/Colab-Tools", url: "https://github.com/LJMStark/Colab-Tools")
                Divider().opacity(0.35)
                aboutLinkRow(title: "作者 X (Twitter)", subtitle: "x.com/starkljm", url: "https://x.com/starkljm")
                Divider().opacity(0.35)
                aboutLinkRow(title: "联系作者", subtitle: "X DM: @starkljm", url: "https://x.com/starkljm")
            }
        }
    }

    // MARK: - Shared Helpers

    private func pageHeader(title: String, subtitle: String) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(title).font(ColabTypography.pageTitle)
            Text(subtitle).font(ColabTypography.body).foregroundStyle(.secondary)
        }
    }

    private func sectionCard<Content: View>(title: String, trailing: String? = nil, @ViewBuilder content: () -> Content) -> some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Text(title).font(ColabTypography.sectionTitle)
                Spacer()
                if let trailing, !trailing.isEmpty {
                    Text(trailing).font(ColabTypography.subheadline).foregroundStyle(.tertiary)
                }
            }
            content()
        }
        .padding(18)
        .frame(maxWidth: .infinity, alignment: .leading)
        .bentoCard(cornerRadius: 14)
    }

    private func aboutLinkRow(title: String, subtitle: String, url: String) -> some View {
        HStack {
            VStack(alignment: .leading, spacing: 2) {
                Text(title).font(ColabTypography.bodyMedium)
                Text(subtitle).font(ColabTypography.subheadline).foregroundStyle(.secondary)
            }
            Spacer()
            if let u = URL(string: url) {
                Button("打开") { NSWorkspace.shared.open(u) }
                    .buttonStyle(.bordered)
            }
        }
    }

    private func integrationSyncStatus(for account: AccountProfile, rt: AccountRuntimeState) -> (hasTargets: Bool, allSynced: Bool, anySynced: Bool) {
        var flags: [Bool] = []
        if account.autoSyncCCSwitch && store.clientDetection.ccSwitchInstalled { flags.append(rt.ccSwitchSynced) }
        if account.autoSyncOpenCode { flags.append(rt.openCodeSynced) }
        if account.autoSyncCodex { flags.append(rt.codexSynced) }
        guard !flags.isEmpty else { return (false, false, false) }
        return (true, flags.allSatisfy { $0 }, flags.contains(true))
    }

    private func refreshLoginItemStatus() {
        if isPreviewMode { return }
        loginItemStatus = SMAppService.mainApp.status
    }

    private func syncPortDrafts() {
        guard let acc = selectedAccount else { return }
        remotePortDraft = String(acc.remoteServicePort)
        localPortDraft = String(acc.localSSHTunnelPort)
    }

    private func commitPortDraft(isRemote: Bool, account: AccountProfile) {
        let raw = (isRemote ? remotePortDraft : localPortDraft).trimmingCharacters(in: .whitespacesAndNewlines)
        guard let val = Int(raw), (1024...65535).contains(val) else {
            store.showToast("端口范围须为 1024–65535，已保留原设置")
            syncPortDrafts()
            return
        }
        var copy = account
        if isRemote { copy.remoteServicePort = val } else { copy.localSSHTunnelPort = val }
        store.updateAccount(copy)
        store.showToast(isRemote ? "已更新远端服务端口为 :\(String(val))" : "已更新本地隧道端口为 :\(String(val))")
    }

    private func toggleLaunchAtLogin(_ enabled: Bool) {
        if isPreviewMode {
            loginItemStatus = enabled ? .enabled : .notRegistered
            return
        }
        do {
            if enabled { try SMAppService.mainApp.register() } else { try SMAppService.mainApp.unregister() }
            loginItemStatus = SMAppService.mainApp.status
            switch loginItemStatus {
            case .enabled: store.showToast("已开启开机自动启动")
            case .requiresApproval: store.showToast("已请求开机自启，请在系统设置 → 登录项中批准")
            default: store.showToast("已关闭开机自动启动")
            }
        } catch {
            loginItemStatus = SMAppService.mainApp.status
            store.showToast("切换开机自启失败（请确认应用位于 /Applications 目录）")
        }
    }

    private func binding<Value>(for account: AccountProfile, _ keyPath: WritableKeyPath<AccountProfile, Value>) -> Binding<Value> {
        Binding(
            get: {
                store.settings.accounts.first(where: { $0.id == account.id })?[keyPath: keyPath] ?? account[keyPath: keyPath]
            },
            set: { newValue in
                guard var cur = store.settings.accounts.first(where: { $0.id == account.id }) else { return }
                cur[keyPath: keyPath] = newValue
                store.updateAccount(cur)
            }
        )
    }
}
