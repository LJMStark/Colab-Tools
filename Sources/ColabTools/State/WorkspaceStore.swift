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
    @Published public var selectedTab: PanelTab = .dashboard
    @Published public var showingAddAccount: Bool = false {
        didSet {
            onPopoverPinChanged?(showingAddAccount)
        }
    }
    @Published public var cliInstalled: Bool = true
    @Published public var toastMessage: String? = nil
    @Published public var activeQueueAccountId: UUID? = nil
    @Published public var queueTickVersion: Int = 0

    public enum WorkspaceViewMode: Equatable {
        case overview
        case singleAccount
    }

    public enum PanelTab: String, CaseIterable, Identifiable {
        case dashboard = "控制"
        case settings = "设置"
        case about = "关于"
        public var id: String { rawValue }
    }

    public var onStatusBarNeedsUpdate: (() -> Void)?
    public var onPopoverPinChanged: ((Bool) -> Void)?

    private var tickTimer: Timer?
    private var slotTimer: Timer?
    private var backgroundTimer: Timer?
    private var sshTunnelProcesses: [UUID: Process] = [:]
    private var sshTunnelADCFiles: [UUID: URL] = [:]
    private var activeTasks: [UUID: Task<Void, Never>] = [:]
    private var queueTasks: [UUID: Task<Void, Never>] = [:]
    private var postAllocateReminderTasks: [UUID: Task<Void, Never>] = [:]
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
        for acc in settings.accounts {
            runtimeByAccount[acc.id] = AccountRuntimeState()
        }
        if settings.selectedAccountId == nil, let first = settings.accounts.first {
            settings.selectedAccountId = first.id
        }
        startTimers()
        Task {
            await bootstrapInitialAccounts()
        }
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
        for (_, t) in postAllocateReminderTasks { t.cancel() }
        postAllocateReminderTasks.removeAll()
        activeQueueAccountId = nil
        for id in Array(sshTunnelProcesses.keys) {
            stopSSHTunnel(for: id)
        }
    }

    // MARK: - Unified Navigation Helpers (Fixes Settings -> Add Account conflict)

    public func openAddAccountFlow() {
        withAnimation(.snappy(duration: 0.18)) {
            selectedTab = .dashboard
            showingAddAccount = true
        }
    }

    public func switchTab(to tab: PanelTab) {
        withAnimation(.snappy(duration: 0.16)) {
            selectedTab = tab
            if tab != .dashboard {
                showingAddAccount = false
            }
        }
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
        return IdleWatchdogEvaluator.evaluate(
            isRunning: st.phase.isRunning,
            enableLLMModule: account.enableLLMModule,
            activitySignal: st.activitySignal,
            autoShutdownEnabled: account.autoShutdownEnabled,
            idleSeconds: st.idleSeconds,
            timeoutMinutes: account.idleTimeoutMinutes,
            warnBeforeMinutes: account.warnBeforeMinutes,
            alreadyWarned: st.warnedForCurrentIdle
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
            }
        }
    }

    private func handleSecondTick() {
        var needsStatusUpdate = false
        var anyQueueOrCooldown = false
        let now = Date()
        for acc in settings.accounts {
            guard var st = runtimeByAccount[acc.id] else { continue }
            if let until = st.queueCircuitBreakUntil {
                if until <= now {
                    st.queueCircuitBreakUntil = nil
                    runtimeByAccount[acc.id] = st
                    anyQueueOrCooldown = true
                } else {
                    anyQueueOrCooldown = true
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

            // Only advance idleSeconds when we hold a fresh verified `.idle` signal
            switch st.activitySignal {
            case .busy(let observedAt):
                if now.timeIntervalSince(observedAt) <= IdleWatchdogEvaluator.defaultSignalMaxAgeSeconds {
                    st.lastActiveAt = now
                    st.idleSeconds = 0
                    st.warnedForCurrentIdle = false
                } else {
                    st.activitySignal = .unknown
                }
            case .idle(let observedAt):
                if now.timeIntervalSince(observedAt) <= IdleWatchdogEvaluator.defaultSignalMaxAgeSeconds {
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
                now: now
            )

            switch action {
            case .none, .pausedNoSignal:
                break
            case .warn(let leftMin):
                st.warnedForCurrentIdle = true
                ColabCLIEngine.sendNotification(
                    title: "Colab Tools 空闲预警 · \(acc.displayName)",
                    body: "已确认连续空闲，约 \(leftMin) 分钟后自动释放 GPU（点击菜单栏可一键续命）"
                )
            case .shutdown:
                st.warnedForCurrentIdle = false
                runtimeByAccount[acc.id] = st
                ColabCLIEngine.sendNotification(
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
            let wasOffline = !cur.phase.isRunning
            if cur.phase.isQueuing {
                queueTasks.removeValue(forKey: accountId)?.cancel()
                if activeQueueAccountId == accountId { activeQueueAccountId = nil }
            }
            cur.phase = .running
            cur.activeEndpoint = primary.endpoint
            cur.activeHardware = primary.hardware
            if wasOffline {
                cur.lastActiveAt = Date()
                cur.idleSeconds = 0
                cur.warnedForCurrentIdle = false
                cur.activitySignal = .unknown
            }
            runtimeByAccount[accountId] = cur

            if let remote = await ColabCLIEngine.fetchRemoteTelemetry(profile: acc) {
                guard isStillValid(accountId: accountId, expectedGeneration: startGen) else { return }
                var latest = state(for: accountId)
                if !remote.gpuName.isEmpty { latest.gpuName = remote.gpuName }
                latest.vramUsedMB = remote.vramUsedMB
                latest.vramTotalMB = remote.vramTotalMB
                latest.gpuUtilPercent = remote.gpuUtil
                if !remote.publicURL.isEmpty { latest.publicURL = remote.publicURL }
                if !remote.modelFile.isEmpty { latest.modelFile = remote.modelFile }
                if !remote.modelAlias.isEmpty { latest.modelAlias = remote.modelAlias }
                latest.contextLimit = remote.contextLimit
                latest.kvQuant = remote.kvQuant
                latest.serverHealthy = remote.healthy
                latest.sessionUptimeSeconds = remote.uptimeSeconds
                latest.appendTelemetrySample(gpuUtil: remote.gpuUtil, vramRatio: latest.vramRatio)
                runtimeByAccount[accountId] = latest

                // Ensure API key is cached in Keychain via safe `colab download` (never via `colab exec` history)
                if acc.enableLLMModule && latest.serverHealthy && KeychainVault.loadAPIKey(for: acc.id) == nil {
                    if let key = await ColabCLIEngine.fetchRemoteAPIKeySafely(profile: acc) {
                        KeychainVault.saveAPIKey(key, for: acc.id)
                    }
                }
            }
            await pollSlots(for: accountId)
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
            cur.serverHealthy = false
            cur.activitySignal = .unknown
            cur.promptTokensUsed = 0
            cur.sessionUptimeSeconds = 0
            cur.idleSeconds = 0
            runtimeByAccount[accountId] = cur
            if !silent { showToast("\(acc.displayName) 主会话未运行") }
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
        guard snap.phase.isRunning, !snap.publicURL.isEmpty else { return }
        let startGen = snap.generation
        let apiKey = KeychainVault.loadAPIKey(for: accountId) ?? ""

        let slot = await ColabCLIEngine.fetchSlots(baseURL: snap.publicURL, apiKey: apiKey)
        guard isStillValid(accountId: accountId, expectedGeneration: startGen) else { return }

        var cur = state(for: accountId)
        guard cur.phase.isRunning else { return }

        if let slot {
            let now = Date()
            let prevTask = cur.lastTaskID
            let nextTask = slot.id_task ?? prevTask
            let processing = slot.is_processing ?? false

            cur.promptTokensUsed = slot.n_prompt_tokens ?? cur.promptTokensUsed
            if let ctx = slot.n_ctx, ctx > 0 { cur.contextLimit = ctx }

            if processing {
                cur.activitySignal = .busy(observedAt: now)
                cur.lastActiveAt = now
                cur.idleSeconds = 0
                cur.warnedForCurrentIdle = false
            } else {
                if prevTask >= 0 && nextTask != prevTask {
                    cur.lastActiveAt = now
                    cur.idleSeconds = 0
                    cur.warnedForCurrentIdle = false
                } else if case .unknown = cur.activitySignal {
                    // First confirmed idle observation starts the clock cleanly from now
                    cur.lastActiveAt = now
                    cur.idleSeconds = 0
                }
                cur.activitySignal = .idle(observedAt: now)
            }
            cur.lastTaskID = nextTask
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
        var set = Set(acc.queueTargets)
        if set.contains(option) {
            if set.count <= 1 {
                showToast("至少保留 1 个排队目标")
                return
            }
            set.remove(option)
        } else {
            set.insert(option)
        }
        acc.queueTargets = PoliteQueuePolicy.sanitizedTargets(selected: Array(set), for: acc.tier)
        updateAccount(acc)
    }

    public func cancelQueue(for accountId: UUID, silent: Bool = false) {
        queueTasks.removeValue(forKey: accountId)?.cancel()
        if activeQueueAccountId == accountId {
            activeQueueAccountId = nil
        }
        var st = state(for: accountId)
        if st.phase.isQueuing || st.phase.isBusy {
            st.generation += 1
            st.phase = .offline
            runtimeByAccount[accountId] = st
        }
        onStatusBarNeedsUpdate?()
        if !silent { showToast("已取消算力排队") }
    }

    private func cancelPostAllocateReminder(for accountId: UUID) {
        postAllocateReminderTasks.removeValue(forKey: accountId)?.cancel()
        var st = state(for: accountId)
        if st.nonLLMReminderPending {
            st.nonLLMReminderPending = false
            runtimeByAccount[accountId] = st
        }
    }

    private func scheduleNonLLMPostAllocateReminder(for acc: AccountProfile, hardwareLabel: String, hourlyRate: Double) {
        cancelPostAllocateReminder(for: acc.id)
        var st = state(for: acc.id)
        st.nonLLMReminderPending = true
        runtimeByAccount[acc.id] = st

        let accountId = acc.id
        let displayName = acc.displayName
        postAllocateReminderTasks[accountId] = Task { @MainActor [weak self] in
            let waitNs = UInt64(PoliteQueuePolicy.nonLLMPostAllocateReminderSeconds * 1_000_000_000)
            try? await Task.sleep(nanoseconds: waitNs)
            guard !Task.isCancelled, let self else { return }
            var cur = self.state(for: accountId)
            cur.nonLLMReminderPending = false
            self.runtimeByAccount[accountId] = cur
            self.postAllocateReminderTasks.removeValue(forKey: accountId)
            guard cur.phase.isRunning else { return }
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
        guard !st.phase.isBusy && !st.phase.isQueuing else { return }

        guard await verifySystemDefaultIdentityIfNeeded(for: acc) else { return }

        st.generation += 1
        let runGen = st.generation
        st.phase = .starting("核验现有会话…")
        st.lastError = nil
        runtimeByAccount[accountId] = st
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
            let outcome = await ColabCLIEngine.provisionSession(profile: acc) { [weak self] step in
                Task { @MainActor [weak self] in
                    guard let self, self.isStillValid(accountId: accountId, expectedGeneration: runGen) else { return }
                    var c = self.state(for: accountId)
                    c.phase = .starting(step)
                    self.runtimeByAccount[accountId] = c
                    self.onStatusBarNeedsUpdate?()
                }
            }
            guard isStillValid(accountId: accountId, expectedGeneration: runGen) else { return }
            switch outcome {
            case .allocated(let hw):
                allocatedHW = hw
            case .capacityUnavailable503(let msg), .unsupportedTier400(let msg):
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
                if code == "429" || code == "412" || code == "ABUSE_BLOCK" {
                    errState.queueCircuitBreakUntil = Date().addingTimeInterval(PoliteQueuePolicy.circuitBreakerCooldownSeconds)
                }
                runtimeByAccount[accountId] = errState
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

    public func startPoliteQueueSession(_ accountId: UUID) {
        guard let acc = settings.accounts.first(where: { $0.id == accountId }) else { return }
        let st = state(for: accountId)
        guard !st.phase.isBusy && !st.phase.isRunning && !st.phase.isQueuing else { return }

        // Global single-account mutex to strictly prevent multi-account polling / abuse
        if let activeId = activeQueueAccountId, activeId != accountId {
            let otherName = settings.accounts.first(where: { $0.id == activeId })?.displayName ?? "另一账号"
            showToast("「\(otherName)」正在排队，防风控限制同时仅允许单账号排队")
            return
        }

        // Enforce mandatory circuit-breaker cooldown after 412 / 429 / 403 / abuse signal
        if let until = st.queueCircuitBreakUntil, until > Date() {
            let remMin = max(1, Int(ceil(until.timeIntervalSinceNow / 60.0)))
            showToast("触发防风控硬熔断，冷却剩余约 \(remMin) 分钟后再试")
            return
        }

        let cleanTargets = PoliteQueuePolicy.sanitizedTargets(selected: acc.queueTargets, for: acc.tier)
        let summary = PoliteQueuePolicy.targetsSummary(cleanTargets)
        let probeTargets = cleanTargets.flatMap(\.probeTargets)

        var nextSt = st
        nextSt.generation += 1
        let runGen = nextSt.generation
        nextSt.lastError = nil
        nextSt.phase = .queuing(attempt: 1, maxAttempts: PoliteQueuePolicy.maxAttempts, nextRetryAt: nil, targetsSummary: summary)
        runtimeByAccount[accountId] = nextSt
        activeQueueAccountId = accountId
        onStatusBarNeedsUpdate?()

        let startedAt = Date()
        queueTasks[accountId] = Task { @MainActor [weak self] in
            guard let self else { return }
            defer {
                self.queueTasks.removeValue(forKey: accountId)
                if self.activeQueueAccountId == accountId {
                    self.activeQueueAccountId = nil
                }
                self.onStatusBarNeedsUpdate?()
            }

            guard await self.verifySystemDefaultIdentityIfNeeded(for: acc) else {
                var c = self.state(for: accountId)
                if c.phase.isQueuing { c.phase = .offline }
                self.runtimeByAccount[accountId] = c
                return
            }

            for attempt in 1...PoliteQueuePolicy.maxAttempts {
                guard !Task.isCancelled, self.isStillValid(accountId: accountId, expectedGeneration: runGen) else { return }
                guard let latestAcc = self.settings.accounts.first(where: { $0.id == accountId }) else { return }

                // Pre-flight read-only check before every attempt to avoid duplicate assignments or orphan contention
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

                let outcome = await ColabCLIEngine.provisionSession(
                    profile: latestAcc,
                    overrideTargets: probeTargets,
                    interTargetDelaySeconds: { PoliteQueuePolicy.interTargetDelaySeconds() }
                ) { _ in }

                guard !Task.isCancelled, self.isStillValid(accountId: accountId, expectedGeneration: runGen) else { return }

                switch outcome {
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
                    broken.queueCircuitBreakUntil = Date().addingTimeInterval(PoliteQueuePolicy.circuitBreakerCooldownSeconds)
                    self.runtimeByAccount[accountId] = broken
                    ColabCLIEngine.sendNotification(
                        title: "Colab Tools 排队熔断 · \(latestAcc.displayName)",
                        body: msg
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

                case .capacityUnavailable503(let msg):
                    guard let delay = PoliteQueuePolicy.nextDelay(
                        completedAttempts: attempt,
                        startedAt: startedAt,
                        now: Date()
                    ) else {
                        var done = self.state(for: accountId)
                        done.phase = .offline
                        done.lastError = "已完成 \(attempt) 轮合规排队（上限 40 分钟），\(summary) 仍暂无库存，已自动停止"
                        self.runtimeByAccount[accountId] = done
                        ColabCLIEngine.sendNotification(
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

        if acc.enableLLMModule {
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
            sState.phase = .starting("启动模型与公网隧道…")
            runtimeByAccount[accountId] = sState
            onStatusBarNeedsUpdate?()

            let startRes = await ColabCLIEngine.runDriveStartScript(profile: acc)
            guard isStillValid(accountId: accountId, expectedGeneration: runGen) else { return }
            guard startRes.ok else {
                var warnState = state(for: accountId)
                warnState.phase = .running
                warnState.lastError = "VM 已分配，但 LLM 启动脚本失败：\(startRes.error ?? "")"
                runtimeByAccount[accountId] = warnState
                showToast("LLM 启动失败，VM 仍在运行可手动关机")
                await refreshAccount(accountId, force: true, bypassCooldown: true, silent: true)
                return
            }

            var parsed = state(for: accountId)
            for line in startRes.stdout.components(separatedBy: .newlines) {
                if line.hasPrefix("PUBLIC_URL=") {
                    let u = String(line.dropFirst(11)).trimmingCharacters(in: .whitespaces)
                    if InputValidator.isValidEndpointURL(u) { parsed.publicURL = u }
                } else if line.hasPrefix("ALIAS=") {
                    let a = String(line.dropFirst(6)).trimmingCharacters(in: .whitespaces)
                    if InputValidator.isValidModelAlias(a) { parsed.modelAlias = a }
                } else if line.hasPrefix("GPU=") {
                    parsed.gpuName = String(line.dropFirst(4)).trimmingCharacters(in: .whitespaces)
                } else if line.hasPrefix("MODEL=") {
                    parsed.modelFile = String(line.dropFirst(6)).trimmingCharacters(in: .whitespaces)
                } else if line.hasPrefix("CTX="), let c = Int(line.dropFirst(4).trimmingCharacters(in: .whitespaces)) {
                    parsed.contextLimit = c
                }
            }
            parsed.serverHealthy = !parsed.publicURL.isEmpty
            runtimeByAccount[accountId] = parsed

            if let apiKey = await ColabCLIEngine.fetchRemoteAPIKeySafely(profile: acc) {
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
        runtimeByAccount[accountId] = doneState

        await refreshAccount(accountId, force: true, bypassCooldown: true, silent: true)

        let latest = state(for: accountId)
        if acc.enableLLMModule && acc.autoSyncCCSwitch && CCSwitchSync.isInstalled && !latest.publicURL.isEmpty {
            syncToCCSwitch(accountId: accountId, silent: true)
        }

        let rate = latest.cuRatePerHour > 0
            ? latest.cuRatePerHour
            : AcceleratorOption.estimatedHourlyCU(forHardware: latest.shortGPUName)
        let rateText = String(format: "%.1f CU/hr", rate)

        ColabCLIEngine.sendNotification(
            title: fromQueue ? "Colab Tools 排到卡啦 · \(acc.displayName)" : "Colab Tools 已就绪 · \(acc.displayName)",
            body: "\(latest.shortGPUName) 已上线（约 \(rateText)）"
        )
        showToast("\(acc.displayName) · \(latest.shortGPUName) 已上线")

        if fromQueue && !acc.enableLLMModule {
            scheduleNonLLMPostAllocateReminder(for: acc, hardwareLabel: latest.shortGPUName, hourlyRate: rate)
        }
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
                cur.serverHealthy = false
                cur.activitySignal = .unknown
                cur.promptTokensUsed = 0
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
        var normalized = updated
        normalized.sessionName = InputValidator.sanitizeSessionName(updated.sessionName, fallback: settings.accounts[idx].sessionName)
        if !normalized.tier.allowedAccelerators.contains(normalized.preferredAccelerator) {
            normalized.preferredAccelerator = normalized.tier.defaultAccelerator
        }
        if !normalized.tier.supportsHighMem {
            normalized.highMem = false
        }
        normalized.queueTargets = PoliteQueuePolicy.sanitizedTargets(selected: normalized.queueTargets, for: normalized.tier)
        normalized.idleTimeoutMinutes = max(5, min(720, normalized.idleTimeoutMinutes))
        settings.accounts[idx] = normalized
    }

    // MARK: - Integrations & Safe Launchers (Zero Shell Injection / Zero Key in osascript)

    public func syncToCCSwitch(accountId: UUID, silent: Bool = false) {
        guard let acc = settings.accounts.first(where: { $0.id == accountId }) else { return }
        let st = state(for: accountId)
        let apiKey = KeychainVault.loadAPIKey(for: accountId) ?? ""
        let providerId = acc.ccSwitchProviderId
        let providerName = acc.ccSwitchProviderName
        let baseURL = st.publicURL
        let modelAlias = st.modelAlias
        let ctx = st.contextLimit
        let gpuLabel = st.shortGPUName
        let accountLabel = acc.displayName

        Task { [weak self] in
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

            guard let self, var cur = self.runtimeByAccount[accountId] else { return }
            switch result {
            case .success(let msg):
                cur.ccSwitchSynced = true
                self.runtimeByAccount[accountId] = cur
                if !silent { self.showToast(msg) }
            case .failure(let error):
                cur.ccSwitchSynced = false
                self.runtimeByAccount[accountId] = cur
                if !silent { self.showToast(error.localizedDescription) }
            }
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

    public func openPayPage() {
        Task { _ = await ColabCLIEngine.openColabPayPage() }
    }

    /// Opens an interactive official `colab ssh` terminal supporting BOTH system OAuth2 and isolated Keychain ADC profiles.
    /// Uses an owner-only (`0700`) self-cleaning wrapper script so ADC credentials are bound properly and wiped on exit.
    public func openRemoteSSHShell(accountId: UUID, sessionName: String? = nil) {
        guard let acc = settings.accounts.first(where: { $0.id == accountId }) else { return }
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
        guard let acc = settings.accounts.first(where: { $0.id == accountId }) else { return }
        let st = state(for: accountId)
        let baseURL = st.sshTunnelActive ? "http://127.0.0.1:\(acc.localSSHTunnelPort)" : st.publicURL
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

        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: "/usr/bin/ssh")
        proc.arguments = [
            "-o", "StrictHostKeyChecking=no",
            "-o", "UserKnownHostsFile=/dev/null",
            "-o", "ServerAliveInterval=30",
            "-o", "ExitOnForwardFailure=yes",
            "-o", "ProxyCommand=\(proxyCmd)",
            "-N", "-L", "127.0.0.1:\(acc.localSSHTunnelPort):127.0.0.1:\(acc.remoteLLMPort)",
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
                    self.runtimeByAccount[accountId] = cur
                    self.showToast("SSH 隧道已连通：127.0.0.1:\(port)")
                } else if let proc, self.sshTunnelProcesses[accountId] === proc {
                    self.stopSSHTunnel(for: accountId)
                    self.showToast("SSH 隧道未能绑定本地端口 \(port)")
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
