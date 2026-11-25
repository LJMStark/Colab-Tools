import AppKit
import ColabToolsCore
import Combine
import Darwin
import Foundation
import SwiftUI

@MainActor
public final class WorkspaceStore: ObservableObject {
    @Published public var settings: AppSettings {
        didSet {
            if persistSettingsChanges { settings.save() }
            onStatusBarNeedsUpdate?()
        }
    }
    @Published public var runtimeByAccount: [UUID: AccountRuntimeState] = [:]
    @Published public var selectedViewMode: WorkspaceViewMode = .singleAccount
    @Published public var selectedSettingsSection: SettingsSection = .general
    @Published public var showingAddAccount: Bool = false {
        didSet {
            onPopoverPinChanged?(showingAddAccount)
        }
    }
    @Published public var cliInstalled: Bool = true
    @Published public var clientDetection: ClientDetectionSnapshot = ClientDetectionSnapshot()
    @Published public var insights: AllocationInsights = AllocationInsights()
    @Published public var toastMessage: String? = nil
    @Published public var activeQueueAccountId: UUID? = nil
    @Published public var queueTickVersion: Int = 0

    public enum WorkspaceViewMode: Equatable {
        case overview
        case singleAccount
    }

    public enum SettingsSection: String, CaseIterable, Identifiable, Hashable {
        case general = "通用"
        case compute = "算力与抢卡"
        case integrations = "接入工具"
        case accounts = "账号管理"
        case advanced = "高级"
        case about = "关于"

        public var id: String { rawValue }

        public var iconName: String {
            switch self {
            case .general: return "gearshape"
            case .compute: return "cpu"
            case .integrations: return "puzzlepiece.extension"
            case .accounts: return "person.2"
            case .advanced: return "slider.horizontal.3"
            case .about: return "info.circle"
            }
        }
    }

    public enum SnippetFormat {
        case env
        case json
    }

    public var onStatusBarNeedsUpdate: (() -> Void)?
    public var onPopoverPinChanged: ((Bool) -> Void)?
    public var onOpenSettingsWindow: ((SettingsSection) -> Void)?

    private var tickTimer: Timer?
    private var slotTimer: Timer?
    private var backgroundTimer: Timer?
    private var sshTunnelProcesses: [UUID: Process] = [:]
    private var sshTunnelADCFiles: [UUID: URL] = [:]
    private var activeTasks: [UUID: Task<Void, Never>] = [:]
    private var queueTasks: [UUID: Task<Void, Never>] = [:]
    private var queueTokens: [UUID: UUID] = [:]
    private var activeQueueToken: UUID? = nil
    private var postAllocateReminderTasks: [UUID: Task<Void, Never>] = [:]
    private var reminderBoundEndpoint: [UUID: String] = [:]
    private var refreshingAccounts: Set<UUID> = []
    private var toastTask: Task<Void, Never>?

    private var persistSettingsChanges: Bool = true

    public init(skipAutoBootstrap: Bool = false) {
        self.persistSettingsChanges = !skipAutoBootstrap
        if skipAutoBootstrap {
            self.settings = AppSettings()
            return
        }
        KeychainVault.cleanupStaleEphemeralFiles()
        self.settings = AppSettings.load()
        self.insights = AllocationInsights.load()
        self.clientDetection = ClientIntegrations.detectInstalledTools()
        let now = Date()
        for acc in settings.accounts {
            var st = AccountRuntimeState()
            if let until = settings.circuitBreakUntilByAccount[acc.id.uuidString], until > now {
                st.queueCircuitBreakUntil = until
            }
            runtimeByAccount[acc.id] = st
        }
        if settings.selectedAccountId == nil, let first = settings.accounts.first {
            settings.selectedAccountId = first.id
        }
        startTimers()
        Task {
            await bootstrapInitialAccounts()
        }
    }

    public func refreshClientDetection() {
        clientDetection = ClientIntegrations.detectInstalledTools()
    }

    private func sendNotice(title: String, body: String, criticalBillingAlert: Bool = false) {
        if criticalBillingAlert || settings.enableNotifications {
            ColabCLIEngine.sendNotification(title: title, body: body)
        }
    }

    private func recordAllocationInsight(hardware: String, succeeded: Bool) {
        insights.record(hardware: hardware, succeeded: succeeded)
        if persistSettingsChanges {
            insights.save()
        }
    }

    public func clearAllocationInsights() {
        insights = AllocationInsights()
        if persistSettingsChanges {
            insights.save()
        }
        showToast("已清空本机时段统计记录")
    }

    public func cleanup() {
        tickTimer?.invalidate()
        slotTimer?.invalidate()
        backgroundTimer?.invalidate()
        tickTimer = nil
        slotTimer = nil
        backgroundTimer = nil
        for (_, t) in activeTasks { t.cancel() }
        activeTasks.removeAll()
        for (_, t) in queueTasks { t.cancel() }
        queueTasks.removeAll()
        queueTokens.removeAll()
        for (_, t) in postAllocateReminderTasks { t.cancel() }
        postAllocateReminderTasks.removeAll()
        reminderBoundEndpoint.removeAll()
        activeQueueAccountId = nil
        activeQueueToken = nil
        for id in Array(sshTunnelProcesses.keys) {
            stopSSHTunnel(for: id)
        }
    }

    // MARK: - Unified Navigation Helpers

    public func openAddAccountFlow() {
        withAnimation(.snappy(duration: 0.18)) {
            showingAddAccount = true
        }
    }

    public func openSettings(section: SettingsSection = .general) {
        selectedSettingsSection = section
        refreshClientDetection()
        onOpenSettingsWindow?(section)
    }

    // MARK: - Computed Workspace Aggregates

    public var isMultiAccount: Bool {
        settings.accounts.count >= 2
    }

    public var selectedAccount: AccountProfile? {
        if let id = settings.selectedAccountId,
           let match = settings.accounts.first(where: { $0.id == id }) {
            return match
        }
        return settings.accounts.first
    }

    public func state(for accountId: UUID) -> AccountRuntimeState {
        runtimeByAccount[accountId] ?? AccountRuntimeState()
    }

    public var totalBalanceCU: Double {
        settings.accounts.reduce(0) { $0 + state(for: $1.id).cuBalance }
    }

    public var totalHourlyRateCU: Double {
        settings.accounts.reduce(0) { $0 + state(for: $1.id).cuRatePerHour }
    }

    public var runningAccounts: [(AccountProfile, AccountRuntimeState)] {
        settings.accounts.compactMap { acc in
            let st = state(for: acc.id)
            return st.phase.isRunning ? (acc, st) : nil
        }
    }

    public func watchdogStatus(for account: AccountProfile) -> WatchdogAction {
        let st = state(for: account.id)
        let allowFallback = account.enableHardwareIdleFallback || st.signalSource == .statusFile || st.signalSource == .comfyQueue
        return IdleWatchdogEvaluator.evaluate(
            isRunning: st.phase.isRunning,
            enableLLMModule: account.enableLLMModule,
            activitySignal: st.activitySignal,
            autoShutdownEnabled: account.autoShutdownEnabled,
            idleSeconds: st.idleSeconds,
            timeoutMinutes: account.idleTimeoutMinutes,
            warnBeforeMinutes: account.warnBeforeMinutes,
            alreadyWarned: st.warnedForCurrentIdle,
            allowHardwareFallback: allowFallback
        )
    }

    // MARK: - Bootstrap & Timers

    private func bootstrapInitialAccounts() async {
        cliInstalled = await ColabCLIEngine.isColabCLIInstalled()
        guard cliInstalled else {
            onStatusBarNeedsUpdate?()
            return
        }

        if settings.accounts.isEmpty {
            let probe = AccountProfile(
                displayName: "默认账号",
                email: "",
                colorTag: .amber,
                tier: .colabPro,
                authMode: .systemDefaultOAuth2,
                enableLLMModule: false,
                autoShutdownEnabled: false
            )
            if let who = await ColabCLIEngine.whoami(profile: probe) {
                let res = await ColabCLIEngine.fetchUsageAndSessions(profile: probe)
                let balance = res.usage?.balance ?? 0
                let suggestedTier = SubscriptionTier.suggested(forBalance: balance)
                var detected = probe
                detected.email = who.email
                detected.displayName = who.email.split(separator: "@").first.map(String.init) ?? "主账号"
                detected.tier = suggestedTier
                detected.preferredAccelerator = suggestedTier.defaultAccelerator
                if let existingSession = res.sessions?.first(where: { !$0.isOrphan }) {
                    detected.sessionName = InputValidator.sanitizeSessionName(existingSession.name)
                }
                settings.accounts.append(detected)
                settings.selectedAccountId = detected.id
                runtimeByAccount[detected.id] = AccountRuntimeState()
                await refreshAccount(detected.id, force: true, bypassCooldown: true, silent: true)
            }
        } else {
            await refreshAllAccounts(force: true, silent: true)
        }
        onStatusBarNeedsUpdate?()
    }

    private func startTimers() {
        tickTimer = Timer.scheduledTimer(withTimeInterval: 1.0, repeats: true) { [weak self] _ in
            Task { @MainActor [weak self] in
                self?.handleSecondTick()
            }
        }
        slotTimer = Timer.scheduledTimer(withTimeInterval: 15.0, repeats: true) { [weak self] _ in
            Task { @MainActor [weak self] in
                await self?.pollAllRunningSlots()
            }
        }
        backgroundTimer = Timer.scheduledTimer(withTimeInterval: 60.0, repeats: true) { [weak self] _ in
            Task { @MainActor [weak self] in
                await self?.refreshAllAccounts(force: false, silent: true)
                await self?.pollRunningMultimodalTelemetry()
            }
        }
    }

    private func handleSecondTick() {
        var needsStatusUpdate = false
        var anyQueueOrCooldown = false
        let now = Date()
        let cal = Calendar.current
        let todayStamp = PoliteQueuePolicy.dayStamp(for: now, calendar: cal)

        for idx in settings.accounts.indices {
            let acc = settings.accounts[idx]
            guard var st = runtimeByAccount[acc.id] else { continue }
            if let until = st.queueCircuitBreakUntil {
                if until <= now {
                    st.queueCircuitBreakUntil = nil
                    runtimeByAccount[acc.id] = st
                    if settings.circuitBreakUntilByAccount.removeValue(forKey: acc.id.uuidString) != nil {
                        // Persisted cleanup handled automatically by settings.didSet
                    }
                    anyQueueOrCooldown = true
                } else {
                    anyQueueOrCooldown = true
                }
            }

            // Off-peak scheduled start evaluation using exact persisted fire timestamp + 10-minute grace window.
            // Acquires queue mutex / busy state synchronously BEFORE consuming the schedule so two accounts
            // scheduled in the same minute never silently drop a one-shot schedule.
            if acc.scheduledStartEnabled {
                let fireAt = acc.scheduledNextFireAt ?? PoliteQueuePolicy.nextScheduledFireDate(
                    hour: acc.scheduledStartHour,
                    minute: acc.scheduledStartMinute,
                    now: now,
                    calendar: cal
                )
                let decision = PoliteQueuePolicy.evaluateSchedule(
                    fireAt: fireAt,
                    repeatDaily: acc.scheduledStartRepeatDaily,
                    hour: acc.scheduledStartHour,
                    minute: acc.scheduledStartMinute,
                    now: now,
                    calendar: cal
                )
                switch decision {
                case .waiting:
                    if acc.scheduledNextFireAt == nil {
                        var copy = acc
                        copy.scheduledNextFireAt = fireAt
                        settings.accounts[idx] = copy
                    }
                case .missedExpired(let nextFireAt):
                    var copy = acc
                    if copy.scheduledStartRepeatDaily {
                        copy.scheduledNextFireAt = nextFireAt
                    } else {
                        copy.scheduledStartEnabled = false
                        copy.scheduledNextFireAt = nil
                    }
                    settings.accounts[idx] = copy
                    let tStr = String(format: "%02d:%02d", acc.scheduledStartHour, acc.scheduledStartMinute)
                    sendNotice(
                        title: "Colab Tools 预约已过期 · \(acc.displayName)",
                        body: "已错过 \(tStr) 预约窗口（超过 10 分钟），为防意外计费未自动启动。"
                    )
                case .triggerNow(let nextFireAt):
                    if case .offline = st.phase, activeCircuitBreakerUntil(for: acc.id, now: now) == nil {
                        let timeLabel = String(format: "%02d:%02d", acc.scheduledStartHour, acc.scheduledStartMinute)
                        if acc.enablePoliteQueue {
                            // Synchronously claim the single-account queue mutex BEFORE consuming the schedule
                            if startPoliteQueueSession(acc.id, silentIfBlocked: true) {
                                var copy = acc
                                copy.lastScheduledFireDay = todayStamp
                                if copy.scheduledStartRepeatDaily {
                                    copy.scheduledNextFireAt = nextFireAt
                                } else {
                                    copy.scheduledStartEnabled = false
                                    copy.scheduledNextFireAt = nil
                                }
                                settings.accounts[idx] = copy
                                sendNotice(
                                    title: "Colab Tools 错峰预约已启动 · \(acc.displayName)",
                                    body: "已按预约时间 \(timeLabel) 自动进入合规排队。"
                                )
                            } else {
                                // Another account currently holds the queue mutex: keep schedule pending inside the grace window
                                // and surface a clear status note without silently dropping the schedule.
                                if st.lastError == nil {
                                    st.lastError = "预约 \(timeLabel) 等待另一账号排队释放后自动尝试（10 分钟宽限内）"
                                    runtimeByAccount[acc.id] = st
                                }
                            }
                        } else {
                            // Synchronously mark starting generation & consume schedule before kicking off async run
                            var copy = acc
                            copy.lastScheduledFireDay = todayStamp
                            if copy.scheduledStartRepeatDaily {
                                copy.scheduledNextFireAt = nextFireAt
                            } else {
                                copy.scheduledStartEnabled = false
                                copy.scheduledNextFireAt = nil
                            }
                            settings.accounts[idx] = copy
                            sendNotice(
                                title: "Colab Tools 错峰预约已启动 · \(acc.displayName)",
                                body: "已到达预约时间 \(timeLabel)，正在自动申请目标算力。"
                            )
                            let accId = acc.id
                            Task { await self.runSingleStartSession(accId) }
                        }
                    }
                }
            }

            if st.phase.isQueuing {
                anyQueueOrCooldown = true
                needsStatusUpdate = true
                continue
            }
            guard st.phase.isRunning else { continue }
            needsStatusUpdate = true

            if st.sessionUptimeSeconds > 0 {
                st.sessionUptimeSeconds += 1
            }

            let allowFallback = acc.enableHardwareIdleFallback || st.signalSource == .statusFile || st.signalSource == .comfyQueue
            let maxAge = (allowFallback && !acc.enableLLMModule)
                ? IdleWatchdogEvaluator.hardwareSignalMaxAgeSeconds
                : IdleWatchdogEvaluator.defaultSignalMaxAgeSeconds

            // Only advance idleSeconds when we hold a fresh verified `.idle` signal
            switch st.activitySignal {
            case .busy(let observedAt):
                if now.timeIntervalSince(observedAt) <= maxAge {
                    st.lastActiveAt = now
                    st.idleSeconds = 0
                    st.warnedForCurrentIdle = false
                } else {
                    st.activitySignal = .unknown
                }
            case .idle(let observedAt):
                if now.timeIntervalSince(observedAt) <= maxAge {
                    st.idleSeconds = max(0, Int(now.timeIntervalSince(st.lastActiveAt)))
                } else {
                    st.activitySignal = .unknown
                }
            case .unknown:
                break
            }

            let action = IdleWatchdogEvaluator.evaluate(
                isRunning: st.phase.isRunning,
                enableLLMModule: acc.enableLLMModule,
                activitySignal: st.activitySignal,
                autoShutdownEnabled: acc.autoShutdownEnabled,
                idleSeconds: st.idleSeconds,
                timeoutMinutes: acc.idleTimeoutMinutes,
                warnBeforeMinutes: acc.warnBeforeMinutes,
                alreadyWarned: st.warnedForCurrentIdle,
                now: now,
                allowHardwareFallback: allowFallback
            )

            switch action {
            case .none, .pausedNoSignal:
                break
            case .warn(let leftMin):
                st.warnedForCurrentIdle = true
                sendNotice(
                    title: "Colab Tools 空闲预警 · \(acc.displayName)",
                    body: "已确认连续空闲，约 \(leftMin) 分钟后自动释放 GPU（点击菜单栏可一键续命）"
                )
            case .shutdown:
                st.warnedForCurrentIdle = false
                runtimeByAccount[acc.id] = st
                sendNotice(
                    title: "Colab Tools 自动关机 · \(acc.displayName)",
                    body: "连续空闲满 \(acc.idleTimeoutMinutes) 分钟，正在执行 colab stop 停止计费。"
                )
                Task {
                    await self.stopAccountSession(acc.id, reason: "\(acc.displayName) 空闲满 \(acc.idleTimeoutMinutes)m 自动关机")
                }
                continue
            }
            runtimeByAccount[acc.id] = st
        }
        if anyQueueOrCooldown {
            queueTickVersion &+= 1
        }
        if needsStatusUpdate {
            onStatusBarNeedsUpdate?()
        }
    }

    // MARK: - Generation-Guarded Incremental Refresh (No Stale Overwrites)

    private func isStillValid(accountId: UUID, expectedGeneration: Int) -> Bool {
        guard settings.accounts.contains(where: { $0.id == accountId }),
              let cur = runtimeByAccount[accountId] else {
            return false
        }
        return cur.generation == expectedGeneration
    }

    public func refreshAllAccounts(force: Bool, silent: Bool = false) async {
        for (idx, acc) in settings.accounts.enumerated() {
            if idx > 0 {
                try? await Task.sleep(nanoseconds: 1_200_000_000)
            }
            await refreshAccount(acc.id, force: force, bypassCooldown: false, silent: silent)
        }
    }

    public func refreshAccount(_ accountId: UUID, force: Bool, bypassCooldown: Bool = false, silent: Bool = false) async {
        guard let acc = settings.accounts.first(where: { $0.id == accountId }) else { return }
        guard !refreshingAccounts.contains(accountId) else { return }
        let initial = state(for: accountId)
        guard !initial.phase.isBusy else { return }

        if !bypassCooldown {
            let minCooldown: Double = force ? 5.0 : Double(settings.backgroundRefreshSeconds)
            if !IdleWatchdogEvaluator.canRefresh(lastFetch: initial.lastUsageFetchAt, minCooldownSeconds: minCooldown) {
                if !silent { showToast("刷新冷却中，请稍后再试") }
                return
            }
        }

        let startGen = initial.generation
        refreshingAccounts.insert(accountId)
        defer {
            refreshingAccounts.remove(accountId)
            onStatusBarNeedsUpdate?()
        }

        let res = await ColabCLIEngine.fetchUsageAndSessions(profile: acc)
        guard isStillValid(accountId: accountId, expectedGeneration: startGen) else { return }

        // Strictly preserve previous state when query fails
        guard res.ok, let usage = res.usage, let sessions = res.sessions else {
            var cur = state(for: accountId)
            cur.lastUsageFetchAt = Date()
            cur.lastError = res.error ?? "官方 CLI 查询失败（已保留上次状态）"
            runtimeByAccount[accountId] = cur
            if !silent { showToast("\(acc.displayName) 查询失败，已保留上次状态") }
            return
        }

        // Incremental merge of usage & sessions
        var cur = state(for: accountId)
        cur.lastUsageFetchAt = Date()
        cur.lastError = nil
        cur.cuBalance = usage.balance
        cur.cuRatePerHour = usage.ratePerHour
        cur.activeAssignments = usage.activeAssignments
        cur.sessions = sessions

        // Strictly match the configured primary sessionName (never masquerade another session as primary)
        if let primary = sessions.first(where: { $0.name == acc.sessionName && !$0.isOrphan }) {
            // If a polite queue is actively managing startup for this account, do NOT hijack or cancel it
            // (so mounting Drive, startup scripts, and post-allocate reminders are never skipped).
            if cur.phase.isQueuing {
                runtimeByAccount[accountId] = cur
                return
            }
            let wasOffline = !cur.phase.isRunning
            let endpointChanged = !cur.activeEndpoint.isEmpty && cur.activeEndpoint != primary.endpoint
            cur.phase = .running
            cur.activeEndpoint = primary.endpoint
            cur.activeHardware = primary.hardware
            if wasOffline || endpointChanged {
                // Invalidate any prior session's credential-bearing API URL and client sync badges when session/endpoint changes
                cur.publicURL = ""
                cur.llmURLBoundEndpoint = ""
                cur.displayUIURL = ""
                cur.ccSwitchSynced = false
                cur.openCodeSynced = false
                cur.codexSynced = false
                cur.lastActiveAt = Date()
                cur.idleSeconds = 0
                cur.warnedForCurrentIdle = false
                cur.activitySignal = .unknown
                cur.signalSource = .none
            }
            runtimeByAccount[accountId] = cur

            if let remote = await ColabCLIEngine.fetchRemoteTelemetry(profile: acc) {
                guard isStillValid(accountId: accountId, expectedGeneration: startGen) else { return }
                applyRemoteTelemetry(remote, to: accountId, account: acc)

                let latest = state(for: accountId)
                // Ensure API key is cached in Keychain via safe `colab download` (never via `colab exec` history)
                if acc.enableLLMModule && latest.serverHealthy && KeychainVault.loadAPIKey(for: acc.id) == nil {
                    if let key = await ColabCLIEngine.fetchRemoteAPIKeySafely(profile: acc) {
                        KeychainVault.saveAPIKey(key, for: acc.id)
                    }
                }
            }
            if acc.enableLLMModule {
                await pollSlots(for: accountId)
            }
            if !silent { showToast("\(acc.displayName) 状态已刷新") }
        } else {
            if !cur.phase.isQueuing {
                cur.phase = .offline
            }
            cur.activeEndpoint = ""
            cur.activeHardware = ""
            cur.gpuName = ""
            cur.vramUsedMB = 0
            cur.vramTotalMB = 0
            cur.gpuUtilPercent = 0
            cur.cpuUtilPercent = 0
            cur.cudaProcessCount = 0
            cur.publicURL = ""
            cur.llmURLBoundEndpoint = ""
            cur.displayUIURL = ""
            cur.ccSwitchSynced = false
            cur.openCodeSynced = false
            cur.codexSynced = false
            cur.serverHealthy = false
            cur.activitySignal = .unknown
            cur.signalSource = .none
            cur.promptTokensUsed = 0
            cur.queueRunning = 0
            cur.queuePending = 0
            cur.detectedServicePort = nil
            cur.sessionUptimeSeconds = 0
            cur.idleSeconds = 0
            runtimeByAccount[accountId] = cur
            if !silent { showToast("\(acc.displayName) 主会话未运行") }
        }
    }

    /// Applies universal GPU/VRAM/CPU telemetry + multimodal status.json / ComfyUI queue signals
    /// and updates the anti-false-shutdown ActivitySignal with strict safety rules:
    /// 1) Live ComfyUI busy ALWAYS blocks shutdown (never overridden by stale status.json `busy:false`).
    /// 2) `status.json` `busy` is honored ONLY when its remote mtime age <= `statusFileMaxAgeSeconds` (5m).
    /// 3) Hardware dual low-load requires BOTH valid non-nil GPU & CPU readings on non-TPU hardware.
    /// 4) Missing readings or TPU hardware safely downgrade to `.unknown` (never mistaken for 0% idle).
    private func applyRemoteTelemetry(_ remote: RemoteTelemetry, to accountId: UUID, account acc: AccountProfile) {
        var latest = state(for: accountId)
        guard latest.phase.isRunning else { return }
        let now = Date()

        if !remote.gpuName.isEmpty { latest.gpuName = remote.gpuName }
        latest.vramUsedMB = remote.vramUsedMB
        latest.vramTotalMB = remote.vramTotalMB
        if let g = remote.gpuUtil { latest.gpuUtilPercent = g }
        if let c = remote.cpuUtil { latest.cpuUtilPercent = c }
        latest.cudaProcessCount = remote.cudaProcessCount
        // Bind trusted LLM API URL to current activeEndpoint only (never overwritten by status.json `ui_url`)
        if !remote.publicURL.isEmpty && !latest.activeEndpoint.isEmpty {
            latest.publicURL = remote.publicURL
            latest.llmURLBoundEndpoint = latest.activeEndpoint
        }
        if !remote.displayUIURL.isEmpty {
            latest.displayUIURL = remote.displayUIURL
        }
        if !remote.modelFile.isEmpty { latest.modelFile = remote.modelFile }
        if !remote.modelAlias.isEmpty { latest.modelAlias = remote.modelAlias }
        latest.contextLimit = remote.contextLimit
        latest.kvQuant = remote.kvQuant
        latest.serverHealthy = remote.healthy
        latest.sessionUptimeSeconds = remote.uptimeSeconds
        latest.detectedServicePort = remote.detectedWebPort ?? latest.detectedServicePort
        if let g = remote.gpuUtil {
            latest.appendTelemetrySample(gpuUtil: g, vramRatio: latest.vramRatio)
        }

        if let payload = remote.statusPayload {
            if let pk = payload.parsedKind { latest.detectedWorkloadKind = pk }
            if let nm = payload.name, !nm.isEmpty { latest.workloadName = nm }
            latest.workloadMetricLabel = payload.metricLabel ?? ""
            latest.workloadMetricValue = payload.metricValue ?? ""
            latest.workloadMetricUnit = payload.metricUnit ?? ""
            latest.workloadMetricRatio = payload.metricRatio
            latest.workloadDetail = payload.detail ?? ""
            if let r = payload.queueRunning { latest.queueRunning = max(0, r) }
            if let p = payload.queuePending { latest.queuePending = max(0, p) }
        }

        if let cq = remote.comfyQueue {
            latest.queueRunning = cq.running
            latest.queuePending = cq.pending
            if latest.workloadName.isEmpty { latest.workloadName = "ComfyUI" }
        }

        let statusFileIsFresh: Bool = {
            guard let age = remote.statusAgeSeconds else { return false }
            return age >= 0 && age <= IdleWatchdogEvaluator.statusFileMaxAgeSeconds
        }()

        // Update ActivitySignal for non-LLM workloads (or LLM when /slots is unavailable)
        let shouldDriveNonLLMSignal = !acc.enableLLMModule || latest.publicURL.isEmpty
        if shouldDriveNonLLMSignal {
            let prevSignal = latest.activitySignal

            // 1. Live ComfyUI busy signal ALWAYS overrides everything to prevent false shutdown
            if let cq = remote.comfyQueue, cq.isBusy {
                latest.signalSource = .comfyQueue
                latest.activitySignal = .busy(observedAt: now)
                latest.lastActiveAt = now
                latest.idleSeconds = 0
                latest.warnedForCurrentIdle = false
            } else if statusFileIsFresh, let payloadBusy = remote.statusPayload?.isWorkloadBusy {
                // 2. Fresh status.json explicit signal
                latest.signalSource = .statusFile
                if payloadBusy {
                    latest.activitySignal = .busy(observedAt: now)
                    latest.lastActiveAt = now
                    latest.idleSeconds = 0
                    latest.warnedForCurrentIdle = false
                } else {
                    if case .unknown = prevSignal { latest.lastActiveAt = now; latest.idleSeconds = 0 }
                    latest.activitySignal = .idle(observedAt: now)
                }
            } else if remote.comfyQueue != nil {
                // 3. Live ComfyUI explicitly idle (running==0 && pending==0)
                latest.signalSource = .comfyQueue
                if case .unknown = prevSignal { latest.lastActiveAt = now; latest.idleSeconds = 0 }
                latest.activitySignal = .idle(observedAt: now)
            } else if acc.enableHardwareIdleFallback {
                // 4. Hardware dual low-load fallback (requires non-nil GPU & CPU readings on non-TPU hardware)
                let hwSig = IdleWatchdogEvaluator.evaluateHardwareActivity(
                    gpuUtilPercent: remote.gpuUtil,
                    cpuUtilPercent: remote.cpuUtil,
                    isTPU: latest.isTPUHardware,
                    now: now
                )
                switch hwSig {
                case .busy:
                    latest.signalSource = .hardwareDualLowLoad
                    latest.activitySignal = hwSig
                    latest.lastActiveAt = now
                    latest.idleSeconds = 0
                    latest.warnedForCurrentIdle = false
                case .idle:
                    latest.signalSource = .hardwareDualLowLoad
                    if case .unknown = prevSignal { latest.lastActiveAt = now; latest.idleSeconds = 0 }
                    latest.activitySignal = hwSig
                case .unknown:
                    // Missing telemetry or TPU runtime -> pause watchdog cleanly and reset continuous idle window
                    latest.signalSource = .none
                    latest.activitySignal = .unknown
                    latest.idleSeconds = 0
                }
            }
        }

        runtimeByAccount[accountId] = latest
    }

    /// Polite 60s hardware/status probe for running non-LLM accounts so dual-low-load watchdog stays fresh
    /// without hitting `colab usage` / `colab sessions` more often than the configured background interval.
    public func pollRunningMultimodalTelemetry() async {
        for acc in settings.accounts {
            let snap = state(for: acc.id)
            guard snap.phase.isRunning, !acc.enableLLMModule else { continue }
            guard !refreshingAccounts.contains(acc.id) else { continue }
            let startGen = snap.generation
            if let remote = await ColabCLIEngine.fetchRemoteTelemetry(profile: acc) {
                guard isStillValid(accountId: acc.id, expectedGeneration: startGen) else { continue }
                applyRemoteTelemetry(remote, to: acc.id, account: acc)
            }
        }
    }

    public func pollAllRunningSlots() async {
        for acc in settings.accounts {
            let st = state(for: acc.id)
            if st.phase.isRunning && acc.enableLLMModule && !st.publicURL.isEmpty {
                await pollSlots(for: acc.id)
            }
        }
    }

    public func pollSlots(for accountId: UUID) async {
        guard settings.accounts.contains(where: { $0.id == accountId }) else { return }
        let snap = state(for: accountId)
        // Strictly send Keychain LLM API key ONLY to a publicURL bound to the current active session endpoint
        guard snap.phase.isRunning,
              !snap.publicURL.isEmpty,
              !snap.activeEndpoint.isEmpty,
              snap.llmURLBoundEndpoint == snap.activeEndpoint else { return }
        let startGen = snap.generation
        let boundEndpoint = snap.activeEndpoint
        let apiKey = KeychainVault.loadAPIKey(for: accountId) ?? ""

        let slots = await ColabCLIEngine.fetchSlots(baseURL: snap.publicURL, apiKey: apiKey)
        guard isStillValid(accountId: accountId, expectedGeneration: startGen) else { return }

        var cur = state(for: accountId)
        guard cur.phase.isRunning, cur.activeEndpoint == boundEndpoint else { return }

        if let slots {
            let now = Date()
            let prevTask = cur.lastTaskID
            let agg = IdleWatchdogEvaluator.evaluateLLMSlots(slots, now: now)

            cur.signalSource = .llmSlots
            if agg.maxPromptTokens > 0 || !slots.isEmpty { cur.promptTokensUsed = agg.maxPromptTokens }
            if agg.maxContext > 0 { cur.contextLimit = agg.maxContext }
            if agg.latestTaskID >= 0 { cur.lastTaskID = agg.latestTaskID }

            switch agg.signal {
            case .busy:
                cur.activitySignal = agg.signal
                cur.lastActiveAt = now
                cur.idleSeconds = 0
                cur.warnedForCurrentIdle = false
            case .idle:
                if prevTask >= 0 && agg.latestTaskID >= 0 && agg.latestTaskID != prevTask {
                    cur.lastActiveAt = now
                    cur.idleSeconds = 0
                    cur.warnedForCurrentIdle = false
                } else if case .unknown = cur.activitySignal {
                    cur.lastActiveAt = now
                    cur.idleSeconds = 0
                }
                cur.activitySignal = agg.signal
            case .unknown:
                cur.activitySignal = .unknown
                cur.idleSeconds = 0
            }
            cur.serverHealthy = true
        } else {
            // Transient fetch failure -> downgrade to `.unknown` so watchdog pauses safely
            cur.activitySignal = .unknown
        }
        runtimeByAccount[accountId] = cur
        onStatusBarNeedsUpdate?()
    }

    // MARK: - Lifecycle Actions (Identity-Checked & Verified)

    /// Verifies that the mutable system-default OAuth2 login still matches the configured profile email before destructive ops.
    private func verifySystemDefaultIdentityIfNeeded(for acc: AccountProfile) async -> Bool {
        guard acc.authMode == .systemDefaultOAuth2, !acc.email.isEmpty else { return true }
        guard let who = await ColabCLIEngine.whoami(profile: acc) else { return false }
        if who.email.lowercased() != acc.email.lowercased() {
            var cur = state(for: acc.id)
            cur.lastError = "外部默认 colab CLI 已切换为 \(who.email)，与本档案 (\(acc.email)) 不符，已中止操作"
            runtimeByAccount[acc.id] = cur
            showToast("检测到默认 CLI 账号变更，已中止操作")
            return false
        }
        return true
    }

    public func toggleQueueTarget(_ option: AcceleratorOption, for accountId: UUID) {
        guard var acc = settings.accounts.first(where: { $0.id == accountId }) else { return }
        guard acc.tier.queueableAccelerators.contains(option) else { return }
        var list = PoliteQueuePolicy.sanitizedTargets(selected: acc.queueTargets, for: acc.tier)
        if let idx = list.firstIndex(of: option) {
            if list.count <= 1 {
                showToast("至少保留 1 个排队目标")
                return
            }
            list.remove(at: idx)
        } else {
            list.append(option)
        }
        acc.queueTargets = PoliteQueuePolicy.sanitizedTargets(selected: list, for: acc.tier)
        updateAccount(acc)
    }

    public func moveQueueTarget(_ option: AcceleratorOption, direction: Int, for accountId: UUID) {
        guard var acc = settings.accounts.first(where: { $0.id == accountId }) else { return }
        var list = PoliteQueuePolicy.sanitizedTargets(selected: acc.queueTargets, for: acc.tier)
        guard let idx = list.firstIndex(of: option) else { return }
        let targetIdx = idx + direction
        guard targetIdx >= 0, targetIdx < list.count else { return }
        list.swapAt(idx, targetIdx)
        acc.queueTargets = list
        updateAccount(acc)
    }

    /// Clears only the transient UI error banner; NEVER bypasses the mandatory 15-minute server-side anti-abuse cooldown.
    public func clearDisplayedError(for accountId: UUID) {
        if var st = runtimeByAccount[accountId] {
            st.lastError = nil
            if case .error = st.phase {
                st.phase = .offline
            }
            runtimeByAccount[accountId] = st
            showToast("已清除界面错误提示")
        }
    }

    private func activeCircuitBreakerUntil(for accountId: UUID, now: Date = Date()) -> Date? {
        let key = accountId.uuidString
        let persisted = settings.circuitBreakUntilByAccount[key]
        let mem = state(for: accountId).queueCircuitBreakUntil
        let latest = [persisted, mem].compactMap { $0 }.max()
        if let latest, latest > now {
            return latest
        }
        return nil
    }

    private func recordCircuitBreaker(for accountId: UUID) {
        let until = Date().addingTimeInterval(PoliteQueuePolicy.circuitBreakerCooldownSeconds)
        var st = state(for: accountId)
        st.queueCircuitBreakUntil = until
        runtimeByAccount[accountId] = st
        settings.circuitBreakUntilByAccount[accountId.uuidString] = until
    }

    public func cancelQueue(for accountId: UUID, silent: Bool = false) {
        let st = state(for: accountId)
        let wasInFlight = st.queueInFlightProvisioning
        let existingToken = queueTokens[accountId]
        queueTasks.removeValue(forKey: accountId)?.cancel()

        if wasInFlight, let acc = settings.accounts.first(where: { $0.id == accountId }) {
            // An allocation POST was already in flight! Keep the mutex locked and verify server state
            // so a newly allocated billing runtime is NEVER hidden as "offline".
            var verifying = st
            verifying.generation += 1
            let verifyGen = verifying.generation
            verifying.queueInFlightProvisioning = false
            verifying.phase = .starting("取消中 · 核实服务端…")
            runtimeByAccount[accountId] = verifying
            onStatusBarNeedsUpdate?()
            if !silent { showToast("正在取消并核实服务端是否已分配…") }

            Task { @MainActor [weak self] in
                guard let self else { return }
                defer {
                    if self.queueTokens[accountId] == existingToken {
                        self.queueTokens.removeValue(forKey: accountId)
                    }
                    if self.activeQueueToken == existingToken {
                        self.activeQueueToken = nil
                        self.activeQueueAccountId = nil
                    }
                    self.onStatusBarNeedsUpdate?()
                }
                let verify = await ColabCLIEngine.fetchUsageAndSessions(profile: acc)
                guard self.isStillValid(accountId: accountId, expectedGeneration: verifyGen) else { return }
                var cur = self.state(for: accountId)
                if verify.ok, let usage = verify.usage, let sessions = verify.sessions {
                    cur.cuBalance = usage.balance
                    cur.cuRatePerHour = usage.ratePerHour
                    cur.activeAssignments = usage.activeAssignments
                    cur.sessions = sessions
                    if let primary = sessions.first(where: { $0.name == acc.sessionName && !$0.isOrphan }) {
                        cur.phase = .running
                        cur.activeEndpoint = primary.endpoint
                        cur.activeHardware = primary.hardware
                        cur.lastError = "取消前服务端已完成分配，实例正在计费，请按需关机止损！"
                        self.runtimeByAccount[accountId] = cur
                        let rate = usage.ratePerHour > 0 ? usage.ratePerHour : AcceleratorOption.estimatedHourlyCU(forHardware: primary.hardware)
                        ColabCLIEngine.sendNotification(
                            title: "Colab Tools 计费提醒 · \(acc.displayName)",
                            body: "取消前 \(primary.hardware) 已成功分配（约 \(String(format: "%.1f", rate)) CU/hr），如不使用请立即关机！"
                        )
                        if !acc.enableLLMModule {
                            self.scheduleNonLLMPostAllocateReminder(for: acc, endpoint: primary.endpoint, hardwareLabel: primary.hardware, hourlyRate: rate)
                        }
                        self.showToast("取消前已分配 \(primary.hardware)，请注意关机止损")
                        return
                    }
                    cur.phase = .offline
                    cur.lastError = nil
                    self.runtimeByAccount[accountId] = cur
                    if !silent { self.showToast("已确认取消，未产生计费实例") }
                } else {
                    cur.phase = .error("取消时无法核实服务端状态，请手动刷新确认有无计费实例")
                    cur.lastError = verify.error ?? "服务端核验失败"
                    self.runtimeByAccount[accountId] = cur
                    self.showToast("无法核实服务端分配状态，请刷新检查")
                }
            }
            return
        }

        if queueTokens[accountId] == existingToken {
            queueTokens.removeValue(forKey: accountId)
        }
        if activeQueueToken == existingToken || activeQueueAccountId == accountId {
            activeQueueToken = nil
            activeQueueAccountId = nil
        }
        var next = st
        next.queueInFlightProvisioning = false
        if next.phase.isQueuing || next.phase.isBusy {
            next.generation += 1
            next.phase = .offline
            runtimeByAccount[accountId] = next
        }
        onStatusBarNeedsUpdate?()
        if !silent { showToast("已取消算力排队") }
    }

    private func cancelPostAllocateReminder(for accountId: UUID) {
        postAllocateReminderTasks.removeValue(forKey: accountId)?.cancel()
        reminderBoundEndpoint.removeValue(forKey: accountId)
        var st = state(for: accountId)
        if st.nonLLMReminderPending {
            st.nonLLMReminderPending = false
            runtimeByAccount[accountId] = st
        }
    }

    private func scheduleNonLLMPostAllocateReminder(for acc: AccountProfile, endpoint: String, hardwareLabel: String, hourlyRate: Double) {
        cancelPostAllocateReminder(for: acc.id)
        var st = state(for: acc.id)
        st.nonLLMReminderPending = true
        runtimeByAccount[acc.id] = st

        let accountId = acc.id
        let displayName = acc.displayName
        reminderBoundEndpoint[accountId] = endpoint
        postAllocateReminderTasks[accountId] = Task { @MainActor [weak self] in
            let waitNs = UInt64(PoliteQueuePolicy.nonLLMPostAllocateReminderSeconds * 1_000_000_000)
            try? await Task.sleep(nanoseconds: waitNs)
            guard !Task.isCancelled, let self else { return }
            let expectedEp = self.reminderBoundEndpoint[accountId]
            var cur = self.state(for: accountId)
            cur.nonLLMReminderPending = false
            self.runtimeByAccount[accountId] = cur
            self.postAllocateReminderTasks.removeValue(forKey: accountId)
            self.reminderBoundEndpoint.removeValue(forKey: accountId)
            guard cur.phase.isRunning else { return }
            if let expectedEp, !expectedEp.isEmpty, !cur.activeEndpoint.isEmpty, cur.activeEndpoint != expectedEp {
                return
            }
            let rate = cur.cuRatePerHour > 0 ? cur.cuRatePerHour : hourlyRate
            ColabCLIEngine.sendNotification(
                title: "Colab Tools 防遗忘提醒 · \(displayName)",
                body: "\(hardwareLabel)（约 \(String(format: "%.1f", rate)) CU/hr）排队分配已满 10 分钟，若暂未使用请及时关机止损。"
            )
        }
    }

    public func startAccountSession(_ accountId: UUID) async {
        guard let acc = settings.accounts.first(where: { $0.id == accountId }) else { return }
        if acc.enablePoliteQueue {
            startPoliteQueueSession(accountId)
            return
        }
        await runSingleStartSession(accountId)
    }

    private func runSingleStartSession(_ accountId: UUID) async {
        guard let acc = settings.accounts.first(where: { $0.id == accountId }) else { return }
        var st = state(for: accountId)
        guard !st.phase.isBusy && !st.phase.isQueuing && !st.phase.isRunning else { return }

        // Enforce persisted 15-minute anti-abuse cooldown across ALL `colab new` entry points
        if let until = activeCircuitBreakerUntil(for: accountId) {
            let remMin = max(1, Int(ceil(until.timeIntervalSinceNow / 60.0)))
            showToast("处于防风控硬熔断冷却期（剩余约 \(remMin) 分钟）")
            return
        }

        // Claim busy state SYNCHRONOUSLY before the very first `await` to eliminate double-click races
        st.generation += 1
        let runGen = st.generation
        st.phase = .starting("核验身份…")
        st.lastError = nil
        runtimeByAccount[accountId] = st
        onStatusBarNeedsUpdate?()

        guard await verifySystemDefaultIdentityIfNeeded(for: acc) else {
            if isStillValid(accountId: accountId, expectedGeneration: runGen) {
                var cur = state(for: accountId)
                if cur.phase.isBusy { cur.phase = .offline }
                runtimeByAccount[accountId] = cur
                onStatusBarNeedsUpdate?()
            }
            return
        }
        guard isStillValid(accountId: accountId, expectedGeneration: runGen) else { return }

        var preSt = state(for: accountId)
        preSt.phase = .starting("核验现有会话…")
        runtimeByAccount[accountId] = preSt
        onStatusBarNeedsUpdate?()

        // Strictly require pre-flight query success before calling `colab new`
        let check = await ColabCLIEngine.fetchUsageAndSessions(profile: acc)
        guard isStillValid(accountId: accountId, expectedGeneration: runGen) else { return }
        guard check.ok, let usage = check.usage, let sessions = check.sessions else {
            var cur = state(for: accountId)
            cur.phase = .error("启动前核验会话失败，已中止以防重复计费")
            cur.lastError = check.error
            runtimeByAccount[accountId] = cur
            showToast("无法核验现有会话，已中止启动")
            onStatusBarNeedsUpdate?()
            return
        }

        var cur = state(for: accountId)
        cur.cuBalance = usage.balance
        cur.sessions = sessions
        runtimeByAccount[accountId] = cur

        let alreadyExists = sessions.contains(where: { $0.name == acc.sessionName && !$0.isOrphan })
        var allocatedHW: String? = nil
        if !alreadyExists {
            let roundRes = await ColabCLIEngine.provisionSession(profile: acc) { [weak self] step in
                Task { @MainActor [weak self] in
                    guard let self, self.isStillValid(accountId: accountId, expectedGeneration: runGen) else { return }
                    var c = self.state(for: accountId)
                    c.phase = .starting(step)
                    self.runtimeByAccount[accountId] = c
                    self.onStatusBarNeedsUpdate?()
                }
            }
            guard isStillValid(accountId: accountId, expectedGeneration: runGen) else { return }
            for rec in roundRes.probeRecords {
                recordAllocationInsight(hardware: rec.hardware, succeeded: rec.succeeded)
            }
            switch roundRes.outcome {
            case .allocated(let hw):
                allocatedHW = hw
            case .capacityUnavailable503(let msg), .budgetExhausted(let msg):
                var errState = state(for: accountId)
                errState.phase = .error(msg)
                errState.lastError = msg
                runtimeByAccount[accountId] = errState
                showToast(msg)
                onStatusBarNeedsUpdate?()
                return
            case .unsupportedTier400(let msg):
                var errState = state(for: accountId)
                errState.phase = .error(msg)
                errState.lastError = msg
                runtimeByAccount[accountId] = errState
                showToast(msg)
                onStatusBarNeedsUpdate?()
                return
            case .circuitBroken(let code, let msg):
                var errState = state(for: accountId)
                errState.phase = .error(msg)
                errState.lastError = msg
                runtimeByAccount[accountId] = errState
                if code == "429" || code == "412" || code == "ABUSE_BLOCK" || code == "401/403" {
                    recordCircuitBreaker(for: accountId)
                }
                showToast(msg)
                onStatusBarNeedsUpdate?()
                return
            }
        }

        await finalizeStartedSession(
            accountId: accountId,
            runGen: runGen,
            allocatedHardware: allocatedHW,
            fromQueue: false
        )
    }

    @discardableResult
    public func startPoliteQueueSession(_ accountId: UUID, silentIfBlocked: Bool = false) -> Bool {
        guard let acc = settings.accounts.first(where: { $0.id == accountId }) else { return false }
        let st = state(for: accountId)
        guard !st.phase.isBusy && !st.phase.isRunning && !st.phase.isQueuing else { return false }

        // Global single-account mutex to strictly prevent multi-account polling / abuse
        if let activeId = activeQueueAccountId, activeId != accountId {
            if !silentIfBlocked {
                let otherName = settings.accounts.first(where: { $0.id == activeId })?.displayName ?? "另一账号"
                showToast("「\(otherName)」正在排队，防风控限制同时仅允许单账号排队")
            }
            return false
        }

        // Enforce persisted circuit-breaker cooldown after 412 / 429 / 403 / abuse signal
        if let until = activeCircuitBreakerUntil(for: accountId) {
            if !silentIfBlocked {
                let remMin = max(1, Int(ceil(until.timeIntervalSinceNow / 60.0)))
                showToast("触发防风控硬熔断，冷却剩余约 \(remMin) 分钟后再试")
            }
            return false
        }

        let initialTargets = PoliteQueuePolicy.targetsForRound(
            attempt: 1,
            primaryTargets: acc.queueTargets,
            fallbackEnabled: acc.queueFallbackEnabled,
            fallbackAfterRounds: acc.queueFallbackAfterRounds,
            fallbackTarget: acc.queueFallbackTarget,
            tier: acc.tier
        )
        let initialSummary = PoliteQueuePolicy.targetsSummary(initialTargets)

        let myToken = UUID()
        var nextSt = st
        nextSt.generation += 1
        let runGen = nextSt.generation
        nextSt.lastError = nil
        nextSt.queueInFlightProvisioning = false
        nextSt.phase = .queuing(attempt: 1, maxAttempts: PoliteQueuePolicy.maxAttempts, nextRetryAt: nil, targetsSummary: initialSummary)
        runtimeByAccount[accountId] = nextSt
        activeQueueAccountId = accountId
        activeQueueToken = myToken
        queueTokens[accountId] = myToken
        onStatusBarNeedsUpdate?()

        let startedAt = Date()
        let absoluteDeadline = startedAt.addingTimeInterval(PoliteQueuePolicy.maxTotalWaitSeconds)

        queueTasks[accountId] = Task { @MainActor [weak self] in
            guard let self else { return }
            var permanentlyUnsupportedValues: Set<String> = []
            defer {
                // Only the active token holder may clear the task entry or release the global queue mutex
                if self.queueTokens[accountId] == myToken {
                    self.queueTokens.removeValue(forKey: accountId)
                    self.queueTasks.removeValue(forKey: accountId)
                }
                if self.activeQueueToken == myToken {
                    self.activeQueueToken = nil
                    self.activeQueueAccountId = nil
                }
                if self.isStillValid(accountId: accountId, expectedGeneration: runGen) {
                    var c = self.state(for: accountId)
                    c.queueInFlightProvisioning = false
                    self.runtimeByAccount[accountId] = c
                }
                self.onStatusBarNeedsUpdate?()
            }

            for attempt in 1...PoliteQueuePolicy.maxAttempts {
                guard !Task.isCancelled, self.isStillValid(accountId: accountId, expectedGeneration: runGen) else { return }
                guard let latestAcc = self.settings.accounts.first(where: { $0.id == accountId }) else { return }

                // Compute two-stage targets for this round (primary targets for early rounds, append fallback later)
                let roundOptions = PoliteQueuePolicy.targetsForRound(
                    attempt: attempt,
                    primaryTargets: latestAcc.queueTargets,
                    fallbackEnabled: latestAcc.queueFallbackEnabled,
                    fallbackAfterRounds: latestAcc.queueFallbackAfterRounds,
                    fallbackTarget: latestAcc.queueFallbackTarget,
                    tier: latestAcc.tier
                )
                let roundTargets = roundOptions.flatMap(\.probeTargets).filter { target in
                    guard let val = target.value?.uppercased() else { return true }
                    return !permanentlyUnsupportedValues.contains(val)
                }
                let fallbackActiveThisRound = latestAcc.queueFallbackEnabled
                    && attempt > max(1, min(PoliteQueuePolicy.maxAttempts - 1, latestAcc.queueFallbackAfterRounds))
                    && roundOptions.contains(latestAcc.queueFallbackTarget)
                    && !PoliteQueuePolicy.sanitizedTargets(selected: latestAcc.queueTargets, for: latestAcc.tier).contains(latestAcc.queueFallbackTarget)
                let baseSummary = roundTargets.map(\.display).joined(separator: " → ")
                let summary = fallbackActiveThisRound ? "\(baseSummary) (含保底)" : baseSummary

                guard !roundTargets.isEmpty else {
                    var noLeft = self.state(for: accountId)
                    noLeft.phase = .error("所选算力均不受当前订阅支持，已停止排队")
                    self.runtimeByAccount[accountId] = noLeft
                    return
                }

                guard Date() < absoluteDeadline else {
                    var done = self.state(for: accountId)
                    done.phase = .offline
                    done.lastError = "已达 40 分钟合规排队上限，\(summary) 仍暂无库存，已自动停止"
                    self.runtimeByAccount[accountId] = done
                    return
                }

                // Re-verify default OAuth2 CLI identity before EVERY round so switching `colab login` never provisions on another account
                guard await self.verifySystemDefaultIdentityIfNeeded(for: latestAcc) else {
                    if self.isStillValid(accountId: accountId, expectedGeneration: runGen) {
                        var c = self.state(for: accountId)
                        if c.phase.isQueuing { c.phase = .offline }
                        self.runtimeByAccount[accountId] = c
                    }
                    return
                }
                guard !Task.isCancelled, self.isStillValid(accountId: accountId, expectedGeneration: runGen) else { return }

                // Pre-flight read-only check before every attempt to avoid duplicate assignments
                var checking = self.state(for: accountId)
                checking.phase = .queuing(attempt: attempt, maxAttempts: PoliteQueuePolicy.maxAttempts, nextRetryAt: nil, targetsSummary: summary)
                self.runtimeByAccount[accountId] = checking
                self.onStatusBarNeedsUpdate?()

                let check = await ColabCLIEngine.fetchUsageAndSessions(profile: latestAcc)
                guard !Task.isCancelled, self.isStillValid(accountId: accountId, expectedGeneration: runGen) else { return }
                guard check.ok, let usage = check.usage, let sessions = check.sessions else {
                    var errSt = self.state(for: accountId)
                    errSt.phase = .error("排队前核验会话失败，已中止以防重复计费")
                    errSt.lastError = check.error
                    self.runtimeByAccount[accountId] = errSt
                    self.showToast("无法核验现有会话，排队已中止")
                    return
                }

                var updated = self.state(for: accountId)
                updated.cuBalance = usage.balance
                updated.sessions = sessions
                self.runtimeByAccount[accountId] = updated

                if sessions.contains(where: { $0.name == latestAcc.sessionName && !$0.isOrphan }) {
                    await self.finalizeStartedSession(
                        accountId: accountId,
                        runGen: runGen,
                        allocatedHardware: nil,
                        fromQueue: true
                    )
                    return
                }

                let roundRes = await ColabCLIEngine.provisionSession(
                    profile: latestAcc,
                    overrideTargets: roundTargets,
                    deadline: absoluteDeadline,
                    interTargetDelaySeconds: { PoliteQueuePolicy.interTargetDelaySeconds() },
                    onBeforeAttempt: { [weak self] _ in
                        Task { @MainActor [weak self] in
                            guard let self, self.isStillValid(accountId: accountId, expectedGeneration: runGen) else { return }
                            var f = self.state(for: accountId)
                            f.queueInFlightProvisioning = true
                            self.runtimeByAccount[accountId] = f
                        }
                    }
                ) { _ in }

                if self.isStillValid(accountId: accountId, expectedGeneration: runGen) {
                    var f = self.state(for: accountId)
                    f.queueInFlightProvisioning = false
                    self.runtimeByAccount[accountId] = f
                }

                guard !Task.isCancelled, self.isStillValid(accountId: accountId, expectedGeneration: runGen) else { return }

                // Permanently prune any targets that returned HTTP 400 so we never re-probe unsupported tiers in later rounds
                if !roundRes.unsupportedTargetValues.isEmpty {
                    permanentlyUnsupportedValues.formUnion(roundRes.unsupportedTargetValues)
                }

                // Record only verified per-target probe observations (allocated or explicit 503)
                for rec in roundRes.probeRecords {
                    self.recordAllocationInsight(hardware: rec.hardware, succeeded: rec.succeeded)
                }

                switch roundRes.outcome {
                case .allocated(let hw):
                    await self.finalizeStartedSession(
                        accountId: accountId,
                        runGen: runGen,
                        allocatedHardware: hw,
                        fromQueue: true
                    )
                    return

                case .circuitBroken(let code, let msg):
                    if code == "CANCELLED" { return }
                    var broken = self.state(for: accountId)
                    broken.phase = .error(msg)
                    broken.lastError = msg
                    self.runtimeByAccount[accountId] = broken
                    self.recordCircuitBreaker(for: accountId)
                    self.sendNotice(
                        title: "Colab Tools 排队熔断 · \(latestAcc.displayName)",
                        body: msg,
                        criticalBillingAlert: true
                    )
                    self.showToast(msg)
                    return

                case .unsupportedTier400(let msg):
                    var unsupported = self.state(for: accountId)
                    unsupported.phase = .error(msg)
                    unsupported.lastError = msg
                    self.runtimeByAccount[accountId] = unsupported
                    self.showToast(msg)
                    return

                case .budgetExhausted(let msg):
                    var done = self.state(for: accountId)
                    done.phase = .offline
                    done.lastError = "\(msg)（上限 40 分钟），\(summary) 仍暂无库存，已自动停止"
                    self.runtimeByAccount[accountId] = done
                    self.sendNotice(
                        title: "Colab Tools 排队结束 · \(latestAcc.displayName)",
                        body: "\(summary) 暂未释放库存，已按防风控 40 分钟上限自动停止排队。"
                    )
                    self.showToast("已达安全排队时长上限，自动停止")
                    return

                case .capacityUnavailable503(let msg):
                    let nextRoundOptions = PoliteQueuePolicy.targetsForRound(
                        attempt: attempt + 1,
                        primaryTargets: latestAcc.queueTargets,
                        fallbackEnabled: latestAcc.queueFallbackEnabled,
                        fallbackAfterRounds: latestAcc.queueFallbackAfterRounds,
                        fallbackTarget: latestAcc.queueFallbackTarget,
                        tier: latestAcc.tier
                    )
                    let nextLegalTargets = nextRoundOptions.flatMap(\.probeTargets).filter { target in
                        guard let val = target.value?.uppercased() else { return true }
                        return !permanentlyUnsupportedValues.contains(val)
                    }
                    if nextLegalTargets.isEmpty {
                        var noLeft = self.state(for: accountId)
                        noLeft.phase = .error("所选算力均不受当前订阅支持，已停止排队")
                        noLeft.lastError = msg
                        self.runtimeByAccount[accountId] = noLeft
                        return
                    }
                    guard let delay = PoliteQueuePolicy.nextDelay(
                        completedAttempts: attempt,
                        startedAt: startedAt,
                        now: Date()
                    ) else {
                        var done = self.state(for: accountId)
                        done.phase = .offline
                        done.lastError = "已完成 \(attempt) 轮合规排队（上限 40 分钟），\(summary) 仍暂无库存，已自动停止"
                        self.runtimeByAccount[accountId] = done
                        self.sendNotice(
                            title: "Colab Tools 排队结束 · \(latestAcc.displayName)",
                            body: "\(summary) 暂未释放库存，已按防风控上限自动停止排队。"
                        )
                        self.showToast("已达安全排队上限，自动停止")
                        return
                    }

                    let nextAt = Date().addingTimeInterval(delay)
                    var waiting = self.state(for: accountId)
                    waiting.lastError = msg
                    waiting.phase = .queuing(
                        attempt: attempt,
                        maxAttempts: PoliteQueuePolicy.maxAttempts,
                        nextRetryAt: nextAt,
                        targetsSummary: summary
                    )
                    self.runtimeByAccount[accountId] = waiting
                    self.onStatusBarNeedsUpdate?()

                    try? await Task.sleep(nanoseconds: UInt64(delay * 1_000_000_000))
                }
            }
        }
        return true
    }

    private func finalizeStartedSession(
        accountId: UUID,
        runGen: Int,
        allocatedHardware: String?,
        fromQueue: Bool
    ) async {
        guard let acc = settings.accounts.first(where: { $0.id == accountId }) else { return }
        if let hw = allocatedHardware {
            var allocState = state(for: accountId)
            allocState.activeHardware = hw
            runtimeByAccount[accountId] = allocState
        }

        // Schedule 10-minute anti-forget reminder IMMEDIATELY upon confirmed non-LLM queue allocation
        // so even if Drive mounting or a startup script later fails, the user is still warned about billing.
        let initialHW = allocatedHardware ?? state(for: accountId).shortGPUName
        if fromQueue && !acc.enableLLMModule {
            let estRate = AcceleratorOption.estimatedHourlyCU(forHardware: initialHW)
            scheduleNonLLMPostAllocateReminder(for: acc, endpoint: state(for: accountId).activeEndpoint, hardwareLabel: initialHW, hourlyRate: estRate)
        }

        let trimmedScript = acc.driveScriptPath.trimmingCharacters(in: .whitespacesAndNewlines)
        let shouldRunCustomNonLLMScript = !acc.enableLLMModule
            && !trimmedScript.isEmpty
            && !trimmedScript.hasSuffix("/colab-llm/scripts/start_server.sh")

        if acc.enableLLMModule || shouldRunCustomNonLLMScript {
            var mState = state(for: accountId)
            mState.phase = .starting("挂载 Google Drive…")
            runtimeByAccount[accountId] = mState
            onStatusBarNeedsUpdate?()

            let mounted = await ColabCLIEngine.ensureDriveMounted(profile: acc)
            guard isStillValid(accountId: accountId, expectedGeneration: runGen) else { return }
            guard mounted else {
                var warnState = state(for: accountId)
                warnState.phase = .running
                warnState.lastError = "VM 已分配但 Drive 挂载失败（请注意及时关机止损）"
                runtimeByAccount[accountId] = warnState
                showToast("Drive 挂载失败，实例已分配，可点关机止损")
                await refreshAccount(accountId, force: true, bypassCooldown: true, silent: true)
                return
            }

            var sState = state(for: accountId)
            sState.phase = .starting(acc.enableLLMModule ? "启动模型与公网隧道…" : "启动 \(acc.workloadKind.displayName) 脚本…")
            runtimeByAccount[accountId] = sState
            onStatusBarNeedsUpdate?()

            let startRes = await ColabCLIEngine.runDriveStartScript(profile: acc)
            guard isStillValid(accountId: accountId, expectedGeneration: runGen) else { return }
            guard startRes.ok else {
                var warnState = state(for: accountId)
                warnState.phase = .running
                warnState.lastError = "VM 已分配，但启动脚本失败：\(startRes.error ?? "")"
                runtimeByAccount[accountId] = warnState
                showToast("远端启动失败，VM 仍在运行可手动关机")
                await refreshAccount(accountId, force: true, bypassCooldown: true, silent: true)
                return
            }

            var parsed = state(for: accountId)
            for line in startRes.stdout.components(separatedBy: .newlines) {
                if line.hasPrefix("PUBLIC_URL=") {
                    let u = String(line.dropFirst(11)).trimmingCharacters(in: .whitespaces)
                    if InputValidator.isValidEndpointURL(u) {
                        if acc.enableLLMModule {
                            parsed.publicURL = u
                            parsed.llmURLBoundEndpoint = parsed.activeEndpoint
                        } else {
                            parsed.displayUIURL = u
                        }
                    }
                } else if line.hasPrefix("ALIAS=") {
                    let a = String(line.dropFirst(6)).trimmingCharacters(in: .whitespaces)
                    if InputValidator.isValidModelAlias(a) { parsed.modelAlias = a }
                } else if line.hasPrefix("GPU=") {
                    parsed.gpuName = String(line.dropFirst(4)).trimmingCharacters(in: .whitespaces)
                } else if line.hasPrefix("MODEL=") {
                    parsed.modelFile = String(line.dropFirst(6)).trimmingCharacters(in: .whitespaces)
                } else if line.hasPrefix("WORKLOAD_NAME=") {
                    parsed.workloadName = String(line.dropFirst(14)).trimmingCharacters(in: .whitespaces)
                } else if line.hasPrefix("CTX="), let c = Int(line.dropFirst(4).trimmingCharacters(in: .whitespaces)) {
                    parsed.contextLimit = c
                }
            }
            parsed.serverHealthy = acc.enableLLMModule ? !parsed.publicURL.isEmpty : (!parsed.displayUIURL.isEmpty || true)
            runtimeByAccount[accountId] = parsed

            if acc.enableLLMModule, let apiKey = await ColabCLIEngine.fetchRemoteAPIKeySafely(profile: acc) {
                KeychainVault.saveAPIKey(apiKey, for: acc.id)
            }
        }

        var doneState = state(for: accountId)
        doneState.phase = .running
        doneState.lastError = nil
        doneState.lastActiveAt = Date()
        doneState.idleSeconds = 0
        doneState.warnedForCurrentIdle = false
        doneState.activitySignal = .unknown
        doneState.signalSource = .none
        runtimeByAccount[accountId] = doneState

        await refreshAccount(accountId, force: true, bypassCooldown: true, silent: true)

        let latest = state(for: accountId)
        if acc.enableLLMModule && !latest.publicURL.isEmpty {
            syncAllEnabledClients(accountId: accountId, silent: true)
        }

        let rate = latest.cuRatePerHour > 0
            ? latest.cuRatePerHour
            : AcceleratorOption.estimatedHourlyCU(forHardware: latest.shortGPUName)
        let rateText = String(format: "%.1f CU/hr", rate)

        // Update bound endpoint on pending non-LLM reminder now that refreshAccount populated activeEndpoint
        if fromQueue && !acc.enableLLMModule && !latest.activeEndpoint.isEmpty {
            reminderBoundEndpoint[accountId] = latest.activeEndpoint
        }

        let readyLabel = (acc.enableLLMModule || shouldRunCustomNonLLMScript) ? "已上线" : "实例已分配"
        sendNotice(
            title: fromQueue ? "Colab Tools 排到卡啦 · \(acc.displayName)" : "Colab Tools \(readyLabel) · \(acc.displayName)",
            body: "\(latest.shortGPUName) \(readyLabel)（约 \(rateText)）"
        )
        showToast("\(acc.displayName) · \(latest.shortGPUName) \(readyLabel)")
    }

    /// Strictly verifies server-side session removal after `colab stop` before claiming billing has stopped.
    public func stopAccountSession(_ accountId: UUID, specificSession: String? = nil, reason: String? = nil) async {
        guard let acc = settings.accounts.first(where: { $0.id == accountId }) else { return }
        cancelPostAllocateReminder(for: accountId)
        if state(for: accountId).phase.isQueuing {
            cancelQueue(for: accountId, silent: true)
        }
        guard await verifySystemDefaultIdentityIfNeeded(for: acc) else { return }

        let targetName = specificSession ?? acc.sessionName
        let stoppingPrimary = (targetName == acc.sessionName)
        let priorEndpoint = state(for: accountId).sessions.first(where: { $0.name == targetName })?.endpoint

        if stoppingPrimary {
            stopSSHTunnel(for: accountId)
            var st = state(for: accountId)
            st.generation += 1
            st.phase = .stopping
            runtimeByAccount[accountId] = st
            onStatusBarNeedsUpdate?()
        }

        let stopRes = await ColabCLIEngine.stopSession(profile: acc, sessionName: targetName)

        // Independent server-side verification bypassing UI cooldown
        let verify = await ColabCLIEngine.fetchUsageAndSessions(profile: acc)
        guard settings.accounts.contains(where: { $0.id == accountId }) else { return }

        var cur = state(for: accountId)
        cur.lastUsageFetchAt = Date()
        if let usage = verify.usage {
            cur.cuBalance = usage.balance
            cur.cuRatePerHour = usage.ratePerHour
            cur.activeAssignments = usage.activeAssignments
        }
        if let sessions = verify.sessions {
            cur.sessions = sessions
        }

        let stillPresentOnServer: Bool
        if let sessions = verify.sessions {
            stillPresentOnServer = sessions.contains(where: {
                $0.name == targetName || (priorEndpoint != nil && !priorEndpoint!.isEmpty && $0.endpoint == priorEndpoint)
            })
        } else {
            stillPresentOnServer = true // Verification failed -> never claim billing stopped
        }

        if stopRes.exitCode == 0 && verify.ok && !stillPresentOnServer {
            if stoppingPrimary {
                cur.phase = .offline
                cur.activeEndpoint = ""
                cur.activeHardware = ""
                cur.gpuName = ""
                cur.vramUsedMB = 0
                cur.vramTotalMB = 0
                cur.gpuUtilPercent = 0
                cur.cpuUtilPercent = 0
                cur.cudaProcessCount = 0
                cur.publicURL = ""
                cur.llmURLBoundEndpoint = ""
                cur.displayUIURL = ""
                cur.ccSwitchSynced = false
                cur.openCodeSynced = false
                cur.codexSynced = false
                cur.serverHealthy = false
                cur.activitySignal = .unknown
                cur.signalSource = .none
                cur.promptTokensUsed = 0
                cur.queueRunning = 0
                cur.queuePending = 0
                cur.sessionUptimeSeconds = 0
                cur.idleSeconds = 0
                cur.lastError = nil
            }
            runtimeByAccount[accountId] = cur
            showToast(reason ?? "已确认停止 \(targetName)（服务端已释放）")
        } else {
            // Stop failed or server still lists the assignment -> keep warning visible!
            if stoppingPrimary {
                cur.phase = .running
            }
            cur.lastError = "停止 \(targetName) 未能确认释放，实例可能仍在计费，请核查！"
            runtimeByAccount[accountId] = cur
            showToast("⚠️ 停机未确认释放，请检查服务端会话")
        }
        onStatusBarNeedsUpdate?()
    }

    public func resetIdleTimer(for accountId: UUID) {
        guard var st = runtimeByAccount[accountId] else { return }
        let mins = settings.accounts.first(where: { $0.id == accountId })?.idleTimeoutMinutes ?? 40
        st.lastActiveAt = Date()
        st.idleSeconds = 0
        st.warnedForCurrentIdle = false
        runtimeByAccount[accountId] = st
        showToast("已重置空闲计时（续命 \(mins) 分钟）")
        onStatusBarNeedsUpdate?()
    }

    // MARK: - Account Management

    public func addAccountProfile(_ profile: AccountProfile) async {
        settings.accounts.append(profile)
        settings.selectedAccountId = profile.id
        runtimeByAccount[profile.id] = AccountRuntimeState()
        selectedViewMode = .singleAccount
        await refreshAccount(profile.id, force: true, bypassCooldown: true, silent: true)
        showToast("已添加账号：\(profile.displayName)")
    }

    public func removeAccount(_ accountId: UUID) {
        activeTasks.removeValue(forKey: accountId)?.cancel()
        cancelQueue(for: accountId, silent: true)
        cancelPostAllocateReminder(for: accountId)
        stopSSHTunnel(for: accountId)
        if let acc = settings.accounts.first(where: { $0.id == accountId }) {
            if acc.authMode == .isolatedKeychainADC {
                KeychainVault.deleteCredential(for: accountId)
                if let cfg = acc.isolatedConfigFilePath {
                    let dir = URL(fileURLWithPath: cfg).deletingLastPathComponent()
                    try? FileManager.default.removeItem(at: dir)
                }
            } else {
                KeychainVault.deleteCredential(for: accountId)
            }
        }
        settings.accounts.removeAll(where: { $0.id == accountId })
        runtimeByAccount.removeValue(forKey: accountId)
        if settings.selectedAccountId == accountId {
            settings.selectedAccountId = settings.accounts.first?.id
        }
        if !isMultiAccount {
            selectedViewMode = .singleAccount
        }
        showToast("已移除账号配置")
        onStatusBarNeedsUpdate?()
    }

    public func updateAccount(_ updated: AccountProfile) {
        guard let idx = settings.accounts.firstIndex(where: { $0.id == updated.id }) else { return }
        let old = settings.accounts[idx]
        var normalized = updated
        normalized.sessionName = InputValidator.sanitizeSessionName(updated.sessionName, fallback: old.sessionName)
        if !normalized.tier.allowedAccelerators.contains(normalized.preferredAccelerator) {
            normalized.preferredAccelerator = normalized.tier.defaultAccelerator
        }
        if !normalized.tier.supportsHighMem {
            normalized.highMem = false
        }
        // When workloadKind changes and ports were still at the old workload defaults, migrate smoothly to new defaults
        if old.workloadKind != normalized.workloadKind {
            if normalized.remoteServicePort == old.workloadKind.defaultRemotePort {
                normalized.remoteServicePort = normalized.workloadKind.defaultRemotePort
            }
            if normalized.localSSHTunnelPort == old.workloadKind.defaultLocalTunnelPort {
                normalized.localSSHTunnelPort = normalized.workloadKind.defaultLocalTunnelPort
            }
        }
        normalized.remoteServicePort = max(1024, min(65535, normalized.remoteServicePort))
        normalized.localSSHTunnelPort = max(1024, min(65535, normalized.localSSHTunnelPort))
        normalized.queueTargets = PoliteQueuePolicy.sanitizedTargets(selected: normalized.queueTargets, for: normalized.tier)
        normalized.queueFallbackAfterRounds = max(1, min(PoliteQueuePolicy.maxAttempts - 1, normalized.queueFallbackAfterRounds))
        let eligibleFallbacks = PoliteQueuePolicy.eligibleFallbackTargets(excluding: normalized.queueTargets, for: normalized.tier)
        if eligibleFallbacks.isEmpty {
            normalized.queueFallbackEnabled = false
            normalized.queueFallbackTarget = normalized.tier.queueableAccelerators.last ?? .t4
        } else if !eligibleFallbacks.contains(normalized.queueFallbackTarget) {
            normalized.queueFallbackTarget = PoliteQueuePolicy.defaultFallbackTarget(excluding: normalized.queueTargets, for: normalized.tier)
        }
        normalized.scheduledStartHour = max(0, min(23, normalized.scheduledStartHour))
        normalized.scheduledStartMinute = max(0, min(59, normalized.scheduledStartMinute))
        if normalized.scheduledStartEnabled {
            let scheduleChanged = !old.scheduledStartEnabled
                || old.scheduledStartHour != normalized.scheduledStartHour
                || old.scheduledStartMinute != normalized.scheduledStartMinute
                || old.scheduledStartRepeatDaily != normalized.scheduledStartRepeatDaily
            if scheduleChanged || normalized.scheduledNextFireAt == nil {
                normalized.scheduledNextFireAt = PoliteQueuePolicy.nextScheduledFireDate(
                    hour: normalized.scheduledStartHour,
                    minute: normalized.scheduledStartMinute
                )
            }
        } else {
            normalized.scheduledNextFireAt = nil
        }
        normalized.idleTimeoutMinutes = max(5, min(720, normalized.idleTimeoutMinutes))
        settings.accounts[idx] = normalized
    }

    // MARK: - Integrations & Safe Launchers (Zero Shell Injection / Zero Key in osascript)

    public func syncAllEnabledClients(accountId: UUID, silent: Bool = false) {
        guard let acc = settings.accounts.first(where: { $0.id == accountId }) else { return }
        let st = state(for: accountId)
        guard !st.publicURL.isEmpty else {
            if !silent { showToast("请先启动 LLM 获取公网地址后再同步") }
            return
        }
        refreshClientDetection()
        let doCC = acc.autoSyncCCSwitch && clientDetection.ccSwitchInstalled
        let doOC = acc.autoSyncOpenCode
        let doCodex = acc.autoSyncCodex
        guard doCC || doOC || doCodex else {
            if !silent { showToast("尚未勾选要同步的工具，请到设置 → 接入工具开启") }
            return
        }

        Task { @MainActor [weak self] in
            guard let self else { return }
            var succeededNames: [String] = []
            var failedNotes: [String] = []

            if doCC {
                switch await self.performCCSwitchSync(accountId: accountId) {
                case .success: succeededNames.append("CC Switch")
                case .failure(let err): failedNotes.append("CC Switch：\(err.localizedDescription)")
                }
            }
            if doOC {
                switch await self.performOpenCodeSync(accountId: accountId) {
                case .success: succeededNames.append("OpenCode")
                case .failure(let err): failedNotes.append("OpenCode：\(err.localizedDescription)")
                }
            }
            if doCodex {
                switch await self.performCodexSync(accountId: accountId) {
                case .success: succeededNames.append("Codex")
                case .failure(let err): failedNotes.append("Codex：\(err.localizedDescription)")
                }
            }

            guard !silent else { return }
            if failedNotes.isEmpty {
                self.showToast("已同步至 \(succeededNames.joined(separator: "、"))")
            } else if succeededNames.isEmpty {
                self.showToast("同步失败：\(failedNotes.first ?? "")")
            } else {
                self.showToast("部分成功（\(succeededNames.joined(separator: "、"))）；失败：\(failedNotes.first ?? "")")
            }
        }
    }

    public func syncToOpenCode(accountId: UUID, silent: Bool = false) {
        Task { @MainActor [weak self] in
            guard let self else { return }
            let result = await self.performOpenCodeSync(accountId: accountId)
            guard !silent else { return }
            switch result {
            case .success(let msg): self.showToast(msg)
            case .failure(let err): self.showToast(err.localizedDescription)
            }
        }
    }

    public func syncToCodex(accountId: UUID, silent: Bool = false) {
        Task { @MainActor [weak self] in
            guard let self else { return }
            let result = await self.performCodexSync(accountId: accountId)
            guard !silent else { return }
            switch result {
            case .success(let msg): self.showToast(msg)
            case .failure(let err): self.showToast(err.localizedDescription)
            }
        }
    }

    private func performOpenCodeSync(accountId: UUID) async -> Result<String, Error> {
        guard let acc = settings.accounts.first(where: { $0.id == accountId }) else {
            return .failure(ClientSyncError.ioError("账号不存在"))
        }
        let st = state(for: accountId)
        let startGen = st.generation
        let baseURL = st.publicURL
        let modelAlias = st.modelAlias
        let ids = ClientIntegrations.identifiers(for: accountId)
        let displayName = acc.ccSwitchProviderName.isEmpty ? "Colab Tools · \(acc.displayName)" : acc.ccSwitchProviderName

        let result: Result<URL, Error> = await Task.detached(priority: .userInitiated) {
            Result {
                try ClientIntegrations.syncOpenCode(
                    baseURL: baseURL,
                    modelAlias: modelAlias,
                    providerKey: ids.openCodeProviderKey,
                    envKey: ids.envKey,
                    displayName: displayName
                )
            }
        }.value

        guard isStillValid(accountId: accountId, expectedGeneration: startGen),
              var cur = runtimeByAccount[accountId],
              cur.publicURL == baseURL else {
            return .failure(ClientSyncError.ioError("端点已变更，已丢弃过期写入结果"))
        }

        switch result {
        case .success:
            cur.openCodeSynced = true
            runtimeByAccount[accountId] = cur
            return .success("已安全写入 OpenCode（独立账号隔离 · 未存明文 Key）")
        case .failure(let error):
            cur.openCodeSynced = false
            runtimeByAccount[accountId] = cur
            return .failure(error)
        }
    }

    private func performCodexSync(accountId: UUID) async -> Result<String, Error> {
        guard let acc = settings.accounts.first(where: { $0.id == accountId }) else {
            return .failure(ClientSyncError.ioError("账号不存在"))
        }
        let st = state(for: accountId)
        let startGen = st.generation
        let baseURL = st.publicURL
        let modelAlias = st.modelAlias
        let ids = ClientIntegrations.identifiers(for: accountId)
        let displayName = acc.ccSwitchProviderName.isEmpty ? "Colab Tools · \(acc.displayName)" : acc.ccSwitchProviderName

        let result: Result<URL, Error> = await Task.detached(priority: .userInitiated) {
            Result {
                try ClientIntegrations.syncCodexCLI(
                    baseURL: baseURL,
                    modelAlias: modelAlias,
                    providerTableKey: ids.codexTableKey,
                    envKey: ids.envKey,
                    displayName: displayName
                )
            }
        }.value

        guard isStillValid(accountId: accountId, expectedGeneration: startGen),
              var cur = runtimeByAccount[accountId],
              cur.publicURL == baseURL else {
            return .failure(ClientSyncError.ioError("端点已变更，已丢弃过期写入结果"))
        }

        switch result {
        case .success:
            cur.codexSynced = true
            runtimeByAccount[accountId] = cur
            return .success("已安全写入 Codex CLI 独立区块（不改官方默认）")
        case .failure(let error):
            cur.codexSynced = false
            runtimeByAccount[accountId] = cur
            return .failure(error)
        }
    }

    public func copyClientSnippet(
        accountId: UUID,
        protocolKind: CopyProtocolKind,
        format: SnippetFormat,
        includeRawSecret: Bool = false
    ) {
        guard settings.accounts.contains(where: { $0.id == accountId }) else { return }
        let st = state(for: accountId)
        let ids = ClientIntegrations.identifiers(for: accountId)

        let liveEndpoint: String? = {
            if !st.publicURL.isEmpty { return st.publicURL }
            if st.sshTunnelActive, let boundPort = st.activeSSHTunnelPort {
                return "http://127.0.0.1:\(String(boundPort))"
            }
            return nil
        }()

        // NEVER inject a real Keychain secret into an offline/example placeholder URL
        let allowSecret = includeRawSecret && liveEndpoint != nil
        let effectiveURL = liveEndpoint ?? "https://your-colab-endpoint.trycloudflare.com"
        let apiKey = allowSecret ? (KeychainVault.loadAPIKey(for: accountId) ?? "") : ""
        let text: String
        switch format {
        case .env:
            text = ClientIntegrations.exportEnvSnippet(
                protocolKind: protocolKind,
                baseURL: effectiveURL,
                modelAlias: st.modelAlias,
                apiKey: apiKey,
                envKey: ids.envKey,
                includeRawSecret: allowSecret
            )
        case .json:
            text = ClientIntegrations.exportJSONSnippet(
                protocolKind: protocolKind,
                baseURL: effectiveURL,
                modelAlias: st.modelAlias,
                apiKey: apiKey,
                envKey: ids.envKey,
                includeRawSecret: allowSecret
            )
        }
        let label = allowSecret
            ? "\(protocolKind.rawValue) 含真实密钥的配置（请注意保密）"
            : "\(protocolKind.rawValue) 安全\(format == .env ? "环境变量" : "JSON")模板"
        copyToClipboard(text, label: label)
    }

    public func syncToCCSwitch(accountId: UUID, silent: Bool = false) {
        Task { @MainActor [weak self] in
            guard let self else { return }
            let result = await self.performCCSwitchSync(accountId: accountId)
            guard !silent else { return }
            switch result {
            case .success(let msg): self.showToast(msg)
            case .failure(let err): self.showToast(err.localizedDescription)
            }
        }
    }

    private func performCCSwitchSync(accountId: UUID) async -> Result<String, Error> {
        guard let acc = settings.accounts.first(where: { $0.id == accountId }) else {
            return .failure(ClientSyncError.ioError("账号不存在"))
        }
        let st = state(for: accountId)
        let startGen = st.generation
        let apiKey = KeychainVault.loadAPIKey(for: accountId) ?? ""
        let providerId = acc.ccSwitchProviderId
        let providerName = acc.ccSwitchProviderName
        let baseURL = st.publicURL
        let modelAlias = st.modelAlias
        let ctx = st.contextLimit
        let gpuLabel = st.shortGPUName
        let accountLabel = acc.displayName

        let result: Result<String, Error> = await Task.detached(priority: .userInitiated) {
            Result {
                try CCSwitchSync.syncProvider(
                    providerId: providerId,
                    providerName: providerName,
                    baseURL: baseURL,
                    apiKey: apiKey,
                    modelAlias: modelAlias,
                    contextLimit: ctx,
                    gpuLabel: gpuLabel,
                    accountLabel: accountLabel
                )
            }
        }.value

        guard isStillValid(accountId: accountId, expectedGeneration: startGen),
              var cur = runtimeByAccount[accountId],
              cur.publicURL == baseURL else {
            return .failure(ClientSyncError.ioError("端点已变更，已丢弃过期写入结果"))
        }
        switch result {
        case .success(let msg):
            cur.ccSwitchSynced = true
            runtimeByAccount[accountId] = cur
            return .success(msg)
        case .failure(let error):
            cur.ccSwitchSynced = false
            runtimeByAccount[accountId] = cur
            return .failure(error)
        }
    }

    public func openColabInBrowser(accountId: UUID, sessionName: String? = nil) {
        guard let acc = settings.accounts.first(where: { $0.id == accountId }) else { return }
        cancelPostAllocateReminder(for: accountId)
        Task {
            let r = await ColabCLIEngine.openColabWebURL(profile: acc, sessionName: sessionName)
            showToast(r.exitCode == 0 ? "已在浏览器打开 Colab 会话" : "打开网页会话失败")
        }
    }

    /// Opens the multimodal WebUI / audio preview page (public URL > active local SSH tunnel > Colab web fallback).
    /// Respects Google Colab FAQ limitations: free-tier runtimes without paid CU balance use official Notebook web UI.
    public func openWorkloadWebUI(accountId: UUID) {
        guard let acc = settings.accounts.first(where: { $0.id == accountId }) else { return }
        let st = state(for: accountId)
        cancelPostAllocateReminder(for: accountId)
        if st.isFreeRuntimeRestricted(for: acc) {
            showToast("免费且无付费 CU 运行时按官方规定请使用 Colab 网页 Notebook")
            openColabInBrowser(accountId: accountId)
            return
        }
        let targetUI = !st.displayUIURL.isEmpty ? st.displayUIURL : st.publicURL
        if !targetUI.isEmpty, InputValidator.isValidEndpointURL(targetUI), let url = URL(string: targetUI) {
            NSWorkspace.shared.open(url)
            showToast("已在浏览器打开 \(acc.workloadKind.webUIButtonTitle)")
            return
        }
        if st.sshTunnelActive, let boundPort = st.activeSSHTunnelPort, let url = URL(string: "http://127.0.0.1:\(String(boundPort))") {
            NSWorkspace.shared.open(url)
            showToast("已通过本地隧道打开 127.0.0.1:\(String(boundPort))")
            return
        }
        showToast("暂无公网地址，可先开端口隧道；正打开 Colab 网页")
        openColabInBrowser(accountId: accountId)
    }

    public func openPayPage() {
        Task { _ = await ColabCLIEngine.openColabPayPage() }
    }

    /// Opens an interactive official `colab ssh` terminal supporting BOTH system OAuth2 and isolated Keychain ADC profiles.
    /// Uses an owner-only (`0700`) self-cleaning wrapper script so ADC credentials are bound properly and wiped on exit.
    public func openRemoteSSHShell(accountId: UUID, sessionName: String? = nil) {
        guard let acc = settings.accounts.first(where: { $0.id == accountId }) else { return }
        let st = state(for: accountId)
        if st.isFreeRuntimeRestricted(for: acc) {
            showToast("免费且无付费 CU 余额的运行时受官方条款限制，请使用 Colab 网页打开")
            return
        }
        cancelPostAllocateReminder(for: accountId)
        let target = sessionName ?? acc.sessionName
        guard InputValidator.isValidSessionName(target) else {
            showToast("会话名不合法")
            return
        }

        let prefix = ColabCLIEngine.colabPrefixArgs(for: acc).map(ColabCLIEngine.shellEscape).joined(separator: " ")
        var scriptLines = [
            "#!/usr/bin/env bash",
            "set -u",
            "unset all_proxy ALL_PROXY",
            "export PATH=\"$HOME/.local/bin:/opt/homebrew/bin:/usr/local/bin:$PATH\""
        ]

        if acc.authMode == .isolatedKeychainADC {
            guard let credJSON = KeychainVault.loadCredentialJSON(for: acc.id),
                  let adcURL = try? KeychainVault.writeEphemeralFile(prefix: "sshadc", ext: "json", content: credJSON) else {
                showToast("读取该账号钥匙串凭证失败")
                return
            }
            scriptLines.append("ADC_FILE=\(ColabCLIEngine.shellEscape(adcURL.path))")
            scriptLines.append("trap 'rm -f \"$ADC_FILE\" \"$0\"' EXIT INT TERM")
            scriptLines.append("export GOOGLE_APPLICATION_CREDENTIALS=\"$ADC_FILE\"")
        } else {
            scriptLines.append("trap 'rm -f \"$0\"' EXIT INT TERM")
        }

        scriptLines.append("colab \(prefix) ssh -s \(ColabCLIEngine.shellEscape(target))")

        guard let launchScript = try? KeychainVault.writeEphemeralFile(
            prefix: "sshrun",
            ext: "sh",
            content: scriptLines.joined(separator: "\n") + "\n",
            executable: true
        ) else {
            showToast("创建临时终端启动脚本失败")
            return
        }

        openScriptInTerminal(launchScript)
        showToast("已在终端启动 colab ssh (\(acc.displayName))")
    }

    /// Launches Claude Code via an owner-only (`0700`) self-deleting wrapper script — API keys NEVER appear in `osascript` or Terminal history.
    public func launchClaudeTerminal(accountId: UUID) {
        guard settings.accounts.contains(where: { $0.id == accountId }) else { return }
        let st = state(for: accountId)
        let baseURL: String
        if st.sshTunnelActive, let boundPort = st.activeSSHTunnelPort {
            baseURL = "http://127.0.0.1:\(String(boundPort))"
        } else {
            baseURL = st.publicURL
        }
        let apiKey = KeychainVault.loadAPIKey(for: accountId) ?? ""

        guard InputValidator.isValidEndpointURL(baseURL),
              !apiKey.isEmpty,
              InputValidator.isValidModelAlias(st.modelAlias) else {
            showToast("缺少合法端点或 API Key，请先确认 LLM 已上线")
            return
        }

        let autoCompact = max(32768, st.contextLimit - 16384)
        let script = """
        #!/usr/bin/env bash
        set -euo pipefail
        rm -f "$0"
        unset ANTHROPIC_API_KEY
        export PATH="$HOME/.local/bin:/opt/homebrew/bin:/usr/local/bin:$PATH"
        export ANTHROPIC_BASE_URL=\(ColabCLIEngine.shellEscape(baseURL))
        export ANTHROPIC_AUTH_TOKEN=\(ColabCLIEngine.shellEscape(apiKey))
        export ANTHROPIC_MODEL=\(ColabCLIEngine.shellEscape(st.modelAlias))
        export ANTHROPIC_SMALL_FAST_MODEL=\(ColabCLIEngine.shellEscape(st.modelAlias))
        export CLAUDE_CODE_DISABLE_1M_CONTEXT=1
        export CLAUDE_CODE_MAX_CONTEXT_TOKENS=\(st.contextLimit)
        export CLAUDE_CODE_AUTO_COMPACT_WINDOW=\(autoCompact)
        export CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1
        exec claude --model \(ColabCLIEngine.shellEscape(st.modelAlias))
        """

        guard let launchScript = try? KeychainVault.writeEphemeralFile(
            prefix: "clauderun",
            ext: "sh",
            content: script,
            executable: true
        ) else {
            showToast("创建安全启动脚本失败")
            return
        }

        openScriptInTerminal(launchScript)
        showToast("已在终端安全拉起 Claude Code")
    }

    /// Starts or stops an official `colab ssh --proxy-mode` tunnel for BOTH default OAuth2 and isolated ADC accounts.
    /// Verifies actual TCP port bind (`ExitOnForwardFailure=yes` + socket probe) before marking active,
    /// and checks exact Process instance identity in terminationHandler to avoid clobbering reconnects.
    public func toggleSSHTunnel(for accountId: UUID) {
        guard let acc = settings.accounts.first(where: { $0.id == accountId }) else { return }
        let st = state(for: accountId)
        if st.sshTunnelActive || sshTunnelProcesses[accountId] != nil {
            stopSSHTunnel(for: accountId)
            showToast("已断开 SSH 隧道")
            return
        }
        guard st.phase.isRunning, InputValidator.isValidSessionName(acc.sessionName) else {
            showToast("请先启动有效的 GPU 会话")
            return
        }
        if st.isFreeRuntimeRestricted(for: acc) {
            showToast("免费且无付费 CU 余额的运行时不支持 SSH 端口隧道，请使用 Colab 网页打开")
            return
        }

        var extraEnv: [String: String] = [:]
        var adcFileURL: URL? = nil
        if acc.authMode == .isolatedKeychainADC {
            guard let credJSON = KeychainVault.loadCredentialJSON(for: acc.id),
                  let tmpURL = try? KeychainVault.writeEphemeralFile(prefix: "tunneladc", ext: "json", content: credJSON) else {
                showToast("读取该账号钥匙串凭证失败")
                return
            }
            adcFileURL = tmpURL
            extraEnv["GOOGLE_APPLICATION_CREDENTIALS"] = tmpURL.path
        }

        let prefix = ColabCLIEngine.colabPrefixArgs(for: acc).map(ColabCLIEngine.shellEscape).joined(separator: " ")
        let proxyCmd = "colab \(prefix) ssh --proxy-mode -s \(ColabCLIEngine.shellEscape(acc.sessionName))"

        let targetRemotePort = st.detectedServicePort ?? acc.remoteServicePort
        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: "/usr/bin/ssh")
        proc.arguments = [
            "-o", "StrictHostKeyChecking=no",
            "-o", "UserKnownHostsFile=/dev/null",
            "-o", "ServerAliveInterval=30",
            "-o", "ExitOnForwardFailure=yes",
            "-o", "ProxyCommand=\(proxyCmd)",
            "-N", "-L", "127.0.0.1:\(acc.localSSHTunnelPort):127.0.0.1:\(targetRemotePort)",
            "root@colab-runtime"
        ]
        proc.environment = ColabCLIEngine.baseEnvironment(extra: extraEnv)
        proc.terminationHandler = { [weak self, weak proc] _ in
            Task { @MainActor [weak self] in
                guard let self, let proc else { return }
                // Only clear state if this exact Process instance is still the registered tunnel
                if self.sshTunnelProcesses[accountId] === proc {
                    self.sshTunnelProcesses.removeValue(forKey: accountId)
                    if let f = self.sshTunnelADCFiles.removeValue(forKey: accountId) {
                        KeychainVault.shredAndRemove(f)
                    }
                    if var cur = self.runtimeByAccount[accountId] {
                        cur.sshTunnelActive = false
                        cur.activeSSHTunnelPort = nil
                        self.runtimeByAccount[accountId] = cur
                    }
                }
            }
        }

        do {
            try proc.run()
            sshTunnelProcesses[accountId] = proc
            if let adcFileURL { sshTunnelADCFiles[accountId] = adcFileURL }
            showToast("正在建立 SSH 隧道…")

            let port = acc.localSSHTunnelPort
            Task { [weak self, weak proc] in
                var bound = false
                for _ in 0..<16 {
                    try? await Task.sleep(nanoseconds: 500_000_000)
                    guard let self, let proc, proc.isRunning, self.sshTunnelProcesses[accountId] === proc else { break }
                    if Self.isLocalTCPPortOpen(port: port) {
                        bound = true
                        break
                    }
                }
                guard let self else { return }
                if bound, let proc, self.sshTunnelProcesses[accountId] === proc {
                    var cur = self.state(for: accountId)
                    cur.sshTunnelActive = true
                    cur.activeSSHTunnelPort = port
                    self.runtimeByAccount[accountId] = cur
                    self.showToast("SSH 隧道已连通：127.0.0.1:\(String(port))")
                } else if let proc, self.sshTunnelProcesses[accountId] === proc {
                    self.stopSSHTunnel(for: accountId)
                    self.showToast("SSH 隧道未能绑定本地端口 \(String(port))")
                }
            }
        } catch {
            if let adcFileURL { KeychainVault.shredAndRemove(adcFileURL) }
            showToast("SSH 隧道启动失败：\(error.localizedDescription)")
        }
    }

    public func stopSSHTunnel(for accountId: UUID) {
        if let proc = sshTunnelProcesses.removeValue(forKey: accountId) {
            if proc.isRunning {
                let pid = proc.processIdentifier
                proc.terminate()
                DispatchQueue.global().asyncAfter(deadline: .now() + 1.0) {
                    if proc.isRunning && pid > 0 { kill(pid, SIGKILL) }
                }
            }
        }
        if let adc = sshTunnelADCFiles.removeValue(forKey: accountId) {
            KeychainVault.shredAndRemove(adc)
        }
        if var st = runtimeByAccount[accountId] {
            st.sshTunnelActive = false
            st.activeSSHTunnelPort = nil
            runtimeByAccount[accountId] = st
        }
    }

    private nonisolated static func isLocalTCPPortOpen(port: Int) -> Bool {
        let fd = socket(AF_INET, SOCK_STREAM, 0)
        guard fd >= 0 else { return false }
        defer { close(fd) }
        var addr = sockaddr_in()
        addr.sin_len = UInt8(MemoryLayout<sockaddr_in>.size)
        addr.sin_family = sa_family_t(AF_INET)
        addr.sin_port = in_port_t(UInt16(port).bigEndian)
        inet_pton(AF_INET, "127.0.0.1", &addr.sin_addr)
        let rc = withUnsafePointer(to: &addr) { ptr in
            ptr.withMemoryRebound(to: sockaddr.self, capacity: 1) { sptr in
                connect(fd, sptr, socklen_t(MemoryLayout<sockaddr_in>.size))
            }
        }
        return rc == 0
    }

    public func copyToClipboard(_ text: String, label: String) {
        guard !text.isEmpty else {
            showToast("暂无可复制的\(label)")
            return
        }
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(text, forType: .string)
        showToast("已复制\(label)")
    }

    public func showToast(_ message: String) {
        toastTask?.cancel()
        toastMessage = message
        toastTask = Task { [weak self] in
            try? await Task.sleep(nanoseconds: 3_000_000_000)
            if !Task.isCancelled {
                self?.toastMessage = nil
            }
        }
    }

    private func openScriptInTerminal(_ scriptURL: URL) {
        let cmd = "bash \(ColabCLIEngine.shellEscape(scriptURL.path))"
        let escaped = cmd.replacingOccurrences(of: "\\", with: "\\\\").replacingOccurrences(of: "\"", with: "\\\"")
        let script = """
        tell application "Terminal"
            activate
            do script "\(escaped)"
        end tell
        """
        Task.detached {
            _ = await ColabCLIEngine.runProcess("/usr/bin/osascript", arguments: ["-e", script], timeoutSeconds: 8)
        }
    }
}
