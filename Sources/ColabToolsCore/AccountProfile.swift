import Foundation

public enum AccountAuthMode: String, Codable, Equatable, Sendable {
    /// Uses the default official `colab --auth=oauth2` keychain entry and `~/.config/colab-cli/sessions.json`.
    case systemDefaultOAuth2 = "system_oauth2"
    /// Uses an isolated credential stored in macOS Keychain + `--auth=adc` + isolated `--config` sessions file.
    case isolatedKeychainADC = "isolated_adc"
}

public enum MenuBarDisplayMode: String, CaseIterable, Codable, Identifiable, Sendable {
    case iconOnly = "iconOnly"
    case compact = "compact"
    case detailed = "detailed"

    public var id: String { rawValue }

    public var label: String {
        switch self {
        case .iconOnly: return "仅图标"
        case .compact: return "紧凑 (推荐)"
        case .detailed: return "详细"
        }
    }
}

public enum AccountColorTag: String, CaseIterable, Codable, Identifiable, Sendable {
    case amber = "#F59E0B"
    case slate = "#64748B"
    case emerald = "#10B981"
    case indigo = "#6366F1"
    case rose = "#F43F5E"
    case cyan = "#06B6D4"

    public var id: String { rawValue }

    public var label: String {
        switch self {
        case .amber: return "琥珀"
        case .slate: return "石板"
        case .emerald: return "翠绿"
        case .indigo: return "靛蓝"
        case .rose: return "绯红"
        case .cyan: return "青蓝"
        }
    }
}

/// Input sanitization helpers preventing shell injection or invalid CLI arguments.
public enum InputValidator {
    private static let sessionRegex = try! NSRegularExpression(pattern: "^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$")
    private static let aliasRegex = try! NSRegularExpression(pattern: "^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$")

    public static func isValidSessionName(_ value: String) -> Bool {
        let range = NSRange(value.startIndex..., in: value)
        return sessionRegex.firstMatch(in: value, range: range) != nil
    }

    public static func sanitizeSessionName(_ value: String, fallback: String = "colab-session") -> String {
        let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
        return isValidSessionName(trimmed) ? trimmed : fallback
    }

    public static func isValidModelAlias(_ value: String) -> Bool {
        let range = NSRange(value.startIndex..., in: value)
        return aliasRegex.firstMatch(in: value, range: range) != nil
    }

    /// Accepts only strict `https://` or local `http://127.0.0.1:<port>` URLs with zero whitespace or shell metacharacters.
    public static func isValidEndpointURL(_ value: String) -> Bool {
        guard !value.isEmpty, value.count <= 256 else { return false }
        let forbidden = CharacterSet.whitespacesAndNewlines.union(CharacterSet(charactersIn: "'\"`$\\;|&<>()"))
        guard value.rangeOfCharacter(from: forbidden) == nil else { return false }
        guard let url = URL(string: value), let scheme = url.scheme?.lowercased(), let host = url.host?.lowercased() else {
            return false
        }
        if scheme == "https" {
            return !host.isEmpty
        }
        if scheme == "http" {
            return host == "127.0.0.1" || host == "localhost"
        }
        return false
    }
}

public struct AccountProfile: Codable, Equatable, Identifiable, Sendable {
    public var id: UUID
    public var displayName: String
    public var email: String
    public var colorTag: AccountColorTag
    public var tier: SubscriptionTier
    public var authMode: AccountAuthMode

    // Runtime & session preferences
    public var sessionName: String
    public var preferredAccelerator: AcceleratorOption
    public var highMem: Bool
    public var workloadKind: WorkloadKind
    public var enableHardwareIdleFallback: Bool

    /// Backwards-compatible computed bridge for legacy LLM toggle sites.
    public var enableLLMModule: Bool {
        get { workloadKind == .llm }
        set {
            if newValue {
                workloadKind = .llm
                if remoteServicePort == 8888 || remoteServicePort == 8188 || remoteServicePort == 7860 {
                    remoteServicePort = WorkloadKind.llm.defaultRemotePort
                }
            } else if workloadKind == .llm {
                workloadKind = .general
            }
        }
    }

    // Polite anti-abuse queue preferences (single-layer, minute-scale Equal Jitter, max 5 rounds / 40m)
    public var enablePoliteQueue: Bool
    public var queueTargets: [AcceleratorOption]
    // Two-stage auto-fallback: stick to primary cards for N rounds, then append fallback card
    public var queueFallbackEnabled: Bool
    public var queueFallbackAfterRounds: Int
    public var queueFallbackTarget: AcceleratorOption

    // Off-peak scheduled start (100% compliant single-shot / polite queue trigger at off-peak hours)
    public var scheduledStartEnabled: Bool
    public var scheduledStartHour: Int
    public var scheduledStartMinute: Int
    public var scheduledStartRepeatDaily: Bool
    public var scheduledNextFireAt: Date?
    public var lastScheduledFireDay: String

    // Idle shutdown watchdog settings (customizable per account)
    public var autoShutdownEnabled: Bool
    public var idleTimeoutMinutes: Int
    public var warnBeforeMinutes: Int

    // Workload script, service port & multi-client auto-sync integrations
    public var driveScriptPath: String
    public var autoSyncCCSwitch: Bool
    public var autoSyncOpenCode: Bool
    public var autoSyncCodex: Bool
    public var ccSwitchProviderId: String
    public var ccSwitchProviderName: String
    public var localEnvPath: String
    public var localSSHTunnelPort: Int
    public var remoteServicePort: Int

    /// Alias preserved for source compatibility with older references.
    public var remoteLLMPort: Int {
        get { remoteServicePort }
        set { remoteServicePort = newValue }
    }

    private enum CodingKeys: String, CodingKey {
        case id, displayName, email, colorTag, tier, authMode
        case sessionName, preferredAccelerator, highMem
        case workloadKind, enableLLMModule, enableHardwareIdleFallback
        case enablePoliteQueue, queueTargets
        case queueFallbackEnabled, queueFallbackAfterRounds, queueFallbackTarget
        case scheduledStartEnabled, scheduledStartHour, scheduledStartMinute, scheduledStartRepeatDaily, scheduledNextFireAt, lastScheduledFireDay
        case autoShutdownEnabled, idleTimeoutMinutes, warnBeforeMinutes
        case driveScriptPath, autoSyncCCSwitch, autoSyncOpenCode, autoSyncCodex, ccSwitchProviderId, ccSwitchProviderName
        case localEnvPath, localSSHTunnelPort, remoteServicePort, remoteLLMPort
    }

    public init(
        id: UUID = UUID(),
        displayName: String = "主账号",
        email: String = "",
        colorTag: AccountColorTag = .amber,
        tier: SubscriptionTier = .colabPro,
        authMode: AccountAuthMode = .systemDefaultOAuth2,
        sessionName: String = "llm-coder",
        preferredAccelerator: AcceleratorOption = .autoPro,
        highMem: Bool = false,
        workloadKind: WorkloadKind? = nil,
        enableLLMModule: Bool = false,
        enableHardwareIdleFallback: Bool = true,
        enablePoliteQueue: Bool = false,
        queueTargets: [AcceleratorOption]? = nil,
        queueFallbackEnabled: Bool = false,
        queueFallbackAfterRounds: Int = 2,
        queueFallbackTarget: AcceleratorOption? = nil,
        scheduledStartEnabled: Bool = false,
        scheduledStartHour: Int = 7,
        scheduledStartMinute: Int = 0,
        scheduledStartRepeatDaily: Bool = false,
        scheduledNextFireAt: Date? = nil,
        lastScheduledFireDay: String = "",
        autoShutdownEnabled: Bool = false,
        idleTimeoutMinutes: Int = 40,
        warnBeforeMinutes: Int = 2,
        driveScriptPath: String = "/content/drive/MyDrive/colab-llm/scripts/start_server.sh",
        autoSyncCCSwitch: Bool = true,
        autoSyncOpenCode: Bool = false,
        autoSyncCodex: Bool = false,
        ccSwitchProviderId: String = "colab-qwen38-uncensored",
        ccSwitchProviderName: String = "Colab Qwen3.8-27B (无审查)",
        localEnvPath: String = "",
        localSSHTunnelPort: Int? = nil,
        remoteServicePort: Int? = nil,
        remoteLLMPort: Int? = nil
    ) {
        let resolvedKind = workloadKind ?? (enableLLMModule ? .llm : .general)
        self.id = id
        self.displayName = displayName
        self.email = email
        self.colorTag = colorTag
        self.tier = tier
        self.authMode = authMode
        self.sessionName = InputValidator.sanitizeSessionName(sessionName)
        self.preferredAccelerator = preferredAccelerator
        self.highMem = highMem
        self.workloadKind = resolvedKind
        self.enableHardwareIdleFallback = enableHardwareIdleFallback
        self.enablePoliteQueue = enablePoliteQueue
        let defaultQueue = Array(tier.queueableAccelerators.prefix(2))
        let sanitizedQueue = PoliteQueuePolicy.sanitizedTargets(selected: queueTargets ?? defaultQueue, for: tier)
        self.queueTargets = sanitizedQueue
        self.queueFallbackEnabled = queueFallbackEnabled
        self.queueFallbackAfterRounds = max(1, min(PoliteQueuePolicy.maxAttempts - 1, queueFallbackAfterRounds))
        let eligibleFallbacks = PoliteQueuePolicy.eligibleFallbackTargets(excluding: sanitizedQueue, for: tier)
        if let explicitFB = queueFallbackTarget, eligibleFallbacks.contains(explicitFB) {
            self.queueFallbackTarget = explicitFB
        } else {
            self.queueFallbackTarget = PoliteQueuePolicy.defaultFallbackTarget(excluding: sanitizedQueue, for: tier)
        }
        self.scheduledStartEnabled = scheduledStartEnabled
        self.scheduledStartHour = max(0, min(23, scheduledStartHour))
        self.scheduledStartMinute = max(0, min(59, scheduledStartMinute))
        self.scheduledStartRepeatDaily = scheduledStartRepeatDaily
        self.scheduledNextFireAt = scheduledStartEnabled
            ? (scheduledNextFireAt ?? PoliteQueuePolicy.nextScheduledFireDate(hour: self.scheduledStartHour, minute: self.scheduledStartMinute))
            : nil
        self.lastScheduledFireDay = lastScheduledFireDay
        self.autoShutdownEnabled = autoShutdownEnabled
        self.idleTimeoutMinutes = max(5, min(720, idleTimeoutMinutes))
        self.warnBeforeMinutes = max(0, min(15, warnBeforeMinutes))
        self.driveScriptPath = driveScriptPath
        self.autoSyncCCSwitch = autoSyncCCSwitch
        self.autoSyncOpenCode = autoSyncOpenCode
        self.autoSyncCodex = autoSyncCodex
        self.ccSwitchProviderId = ccSwitchProviderId
        self.ccSwitchProviderName = ccSwitchProviderName
        self.localEnvPath = localEnvPath
        let resolvedRemote = remoteServicePort ?? remoteLLMPort ?? resolvedKind.defaultRemotePort
        self.remoteServicePort = max(1024, min(65535, resolvedRemote))
        let resolvedLocal = localSSHTunnelPort ?? resolvedKind.defaultLocalTunnelPort
        self.localSSHTunnelPort = max(1024, min(65535, resolvedLocal))
    }

    // Backwards-compatible decoding migrating legacy configs seamlessly
    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(UUID.self, forKey: .id)
        displayName = try c.decode(String.self, forKey: .displayName)
        email = try c.decode(String.self, forKey: .email)
        colorTag = (try? c.decodeIfPresent(AccountColorTag.self, forKey: .colorTag)) ?? .amber
        tier = (try? c.decodeIfPresent(SubscriptionTier.self, forKey: .tier)) ?? .colabPro
        authMode = (try? c.decodeIfPresent(AccountAuthMode.self, forKey: .authMode)) ?? .systemDefaultOAuth2
        sessionName = InputValidator.sanitizeSessionName(try c.decode(String.self, forKey: .sessionName))
        preferredAccelerator = (try? c.decodeIfPresent(AcceleratorOption.self, forKey: .preferredAccelerator)) ?? tier.defaultAccelerator
        highMem = try c.decode(Bool.self, forKey: .highMem)
        if let decodedKind = try? c.decodeIfPresent(WorkloadKind.self, forKey: .workloadKind) {
            workloadKind = decodedKind
        } else {
            let legacyLLM = (try? c.decodeIfPresent(Bool.self, forKey: .enableLLMModule)) ?? false
            workloadKind = legacyLLM ? .llm : .general
        }
        // Preserve legacy safe paused behavior when upgrading old config.json lacking `enableHardwareIdleFallback`
        enableHardwareIdleFallback = (try? c.decodeIfPresent(Bool.self, forKey: .enableHardwareIdleFallback)) ?? false
        enablePoliteQueue = (try? c.decodeIfPresent(Bool.self, forKey: .enablePoliteQueue)) ?? false
        let decodedQueueRaw = (try? c.decodeIfPresent([String].self, forKey: .queueTargets))?.compactMap(AcceleratorOption.init(rawValue:))
        queueTargets = PoliteQueuePolicy.sanitizedTargets(
            selected: decodedQueueRaw ?? Array(tier.queueableAccelerators.prefix(2)),
            for: tier
        )
        queueFallbackEnabled = (try? c.decodeIfPresent(Bool.self, forKey: .queueFallbackEnabled)) ?? false
        queueFallbackAfterRounds = max(1, min(PoliteQueuePolicy.maxAttempts - 1, (try? c.decodeIfPresent(Int.self, forKey: .queueFallbackAfterRounds)) ?? 2))
        let decodedFallback = (try? c.decodeIfPresent(String.self, forKey: .queueFallbackTarget)).flatMap(AcceleratorOption.init(rawValue:))
        let eligibleFB = PoliteQueuePolicy.eligibleFallbackTargets(excluding: queueTargets, for: tier)
        if let decodedFallback, eligibleFB.contains(decodedFallback) {
            queueFallbackTarget = decodedFallback
        } else {
            queueFallbackTarget = PoliteQueuePolicy.defaultFallbackTarget(excluding: queueTargets, for: tier)
        }
        scheduledStartEnabled = (try? c.decodeIfPresent(Bool.self, forKey: .scheduledStartEnabled)) ?? false
        scheduledStartHour = max(0, min(23, (try? c.decodeIfPresent(Int.self, forKey: .scheduledStartHour)) ?? 7))
        scheduledStartMinute = max(0, min(59, (try? c.decodeIfPresent(Int.self, forKey: .scheduledStartMinute)) ?? 0))
        scheduledStartRepeatDaily = (try? c.decodeIfPresent(Bool.self, forKey: .scheduledStartRepeatDaily)) ?? false
        let decodedNextFire = try? c.decodeIfPresent(Date.self, forKey: .scheduledNextFireAt)
        scheduledNextFireAt = scheduledStartEnabled
            ? (decodedNextFire ?? PoliteQueuePolicy.nextScheduledFireDate(hour: scheduledStartHour, minute: scheduledStartMinute))
            : nil
        lastScheduledFireDay = (try? c.decodeIfPresent(String.self, forKey: .lastScheduledFireDay)) ?? ""
        autoShutdownEnabled = try c.decode(Bool.self, forKey: .autoShutdownEnabled)
        idleTimeoutMinutes = max(5, min(720, try c.decode(Int.self, forKey: .idleTimeoutMinutes)))
        warnBeforeMinutes = max(0, min(15, try c.decode(Int.self, forKey: .warnBeforeMinutes)))
        driveScriptPath = try c.decode(String.self, forKey: .driveScriptPath)
        autoSyncCCSwitch = try c.decode(Bool.self, forKey: .autoSyncCCSwitch)
        autoSyncOpenCode = try c.decodeIfPresent(Bool.self, forKey: .autoSyncOpenCode) ?? false
        autoSyncCodex = try c.decodeIfPresent(Bool.self, forKey: .autoSyncCodex) ?? false
        ccSwitchProviderId = try c.decode(String.self, forKey: .ccSwitchProviderId)
        ccSwitchProviderName = try c.decode(String.self, forKey: .ccSwitchProviderName)
        localEnvPath = try c.decode(String.self, forKey: .localEnvPath)
        let decodedRemote = try c.decodeIfPresent(Int.self, forKey: .remoteServicePort)
            ?? c.decodeIfPresent(Int.self, forKey: .remoteLLMPort)
            ?? workloadKind.defaultRemotePort
        remoteServicePort = max(1024, min(65535, decodedRemote))
        let decodedLocal = try c.decodeIfPresent(Int.self, forKey: .localSSHTunnelPort) ?? workloadKind.defaultLocalTunnelPort
        localSSHTunnelPort = max(1024, min(65535, decodedLocal))
    }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(id, forKey: .id)
        try c.encode(displayName, forKey: .displayName)
        try c.encode(email, forKey: .email)
        try c.encode(colorTag, forKey: .colorTag)
        try c.encode(tier, forKey: .tier)
        try c.encode(authMode, forKey: .authMode)
        try c.encode(sessionName, forKey: .sessionName)
        try c.encode(preferredAccelerator, forKey: .preferredAccelerator)
        try c.encode(highMem, forKey: .highMem)
        try c.encode(workloadKind, forKey: .workloadKind)
        try c.encode(workloadKind == .llm, forKey: .enableLLMModule)
        try c.encode(enableHardwareIdleFallback, forKey: .enableHardwareIdleFallback)
        try c.encode(enablePoliteQueue, forKey: .enablePoliteQueue)
        try c.encode(queueTargets, forKey: .queueTargets)
        try c.encode(queueFallbackEnabled, forKey: .queueFallbackEnabled)
        try c.encode(queueFallbackAfterRounds, forKey: .queueFallbackAfterRounds)
        try c.encode(queueFallbackTarget, forKey: .queueFallbackTarget)
        try c.encode(scheduledStartEnabled, forKey: .scheduledStartEnabled)
        try c.encode(scheduledStartHour, forKey: .scheduledStartHour)
        try c.encode(scheduledStartMinute, forKey: .scheduledStartMinute)
        try c.encode(scheduledStartRepeatDaily, forKey: .scheduledStartRepeatDaily)
        try c.encodeIfPresent(scheduledNextFireAt, forKey: .scheduledNextFireAt)
        try c.encode(lastScheduledFireDay, forKey: .lastScheduledFireDay)
        try c.encode(autoShutdownEnabled, forKey: .autoShutdownEnabled)
        try c.encode(idleTimeoutMinutes, forKey: .idleTimeoutMinutes)
        try c.encode(warnBeforeMinutes, forKey: .warnBeforeMinutes)
        try c.encode(driveScriptPath, forKey: .driveScriptPath)
        try c.encode(autoSyncCCSwitch, forKey: .autoSyncCCSwitch)
        try c.encode(autoSyncOpenCode, forKey: .autoSyncOpenCode)
        try c.encode(autoSyncCodex, forKey: .autoSyncCodex)
        try c.encode(ccSwitchProviderId, forKey: .ccSwitchProviderId)
        try c.encode(ccSwitchProviderName, forKey: .ccSwitchProviderName)
        try c.encode(localEnvPath, forKey: .localEnvPath)
        try c.encode(localSSHTunnelPort, forKey: .localSSHTunnelPort)
        try c.encode(remoteServicePort, forKey: .remoteServicePort)
        try c.encode(remoteServicePort, forKey: .remoteLLMPort)
    }

    public var isolatedConfigFilePath: String? {
        guard authMode == .isolatedKeychainADC else { return nil }
        let dir = AppSettings.configDir.appendingPathComponent("profiles/\(id.uuidString)", isDirectory: true)
        return dir.appendingPathComponent("sessions.json").path
    }
}

public struct AppSettings: Codable, Equatable, Sendable {
    public var accounts: [AccountProfile] = []
    public var selectedAccountId: UUID? = nil
    public var menuBarMode: MenuBarDisplayMode = .compact
    /// Minimum background refresh interval in seconds (default 180s to remain polite to official endpoints).
    public var backgroundRefreshSeconds: Int = 180
    public var lowBalanceWarningCU: Double = 20.0
    public var enableNotifications: Bool = true
    /// Persisted anti-abuse circuit-breaker cooldown deadlines keyed by account identity (survives app restarts).
    public var circuitBreakUntilByAccount: [String: Date] = [:]

    public static let configDir = FileManager.default.homeDirectoryForCurrentUser
        .appendingPathComponent(".config/colab-tools", isDirectory: true)
    public static let configFile = configDir.appendingPathComponent("config.json")

    public init() {}

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        accounts = try c.decodeIfPresent([AccountProfile].self, forKey: .accounts) ?? []
        selectedAccountId = try c.decodeIfPresent(UUID.self, forKey: .selectedAccountId)
        menuBarMode = try c.decodeIfPresent(MenuBarDisplayMode.self, forKey: .menuBarMode) ?? .compact
        backgroundRefreshSeconds = try c.decodeIfPresent(Int.self, forKey: .backgroundRefreshSeconds) ?? 180
        lowBalanceWarningCU = try c.decodeIfPresent(Double.self, forKey: .lowBalanceWarningCU) ?? 20.0
        enableNotifications = try c.decodeIfPresent(Bool.self, forKey: .enableNotifications) ?? true
        circuitBreakUntilByAccount = try c.decodeIfPresent([String: Date].self, forKey: .circuitBreakUntilByAccount) ?? [:]
    }

    public static func load() -> AppSettings {
        if let data = try? Data(contentsOf: configFile),
           var loaded = try? JSONDecoder().decode(AppSettings.self, from: data) {
            loaded.backgroundRefreshSeconds = max(60, min(900, loaded.backgroundRefreshSeconds))
            let now = Date()
            loaded.circuitBreakUntilByAccount = loaded.circuitBreakUntilByAccount.filter { $0.value > now }
            for i in loaded.accounts.indices {
                loaded.accounts[i].idleTimeoutMinutes = max(5, min(720, loaded.accounts[i].idleTimeoutMinutes))
            }
            return loaded
        }
        return AppSettings()
    }

    public func save() {
        try? FileManager.default.createDirectory(at: Self.configDir, withIntermediateDirectories: true)
        let enc = JSONEncoder()
        enc.outputFormatting = [.prettyPrinted, .sortedKeys]
        if let data = try? enc.encode(self) {
            try? data.write(to: Self.configFile, options: .atomic)
        }
    }
}

// MARK: - Live Runtime Telemetry Models

public enum InstancePhase: Equatable, Sendable {
    case offline
    case starting(String)
    case queuing(attempt: Int, maxAttempts: Int, nextRetryAt: Date?, targetsSummary: String)
    case running
    case stopping
    case error(String)

    public var isRunning: Bool {
        if case .running = self { return true }
        return false
    }

    public var isQueuing: Bool {
        if case .queuing = self { return true }
        return false
    }

    public var isBusy: Bool {
        switch self {
        case .starting, .stopping: return true
        default: return false
        }
    }
}

/// Three-state workload activity observation to prevent false auto-shutdowns when activity cannot be verified.
public enum ActivitySignal: Equatable, Sendable {
    case unknown
    case busy(observedAt: Date)
    case idle(observedAt: Date)

    public var isBusy: Bool {
        if case .busy = self { return true }
        return false
    }

    public var observedAt: Date? {
        switch self {
        case .unknown: return nil
        case .busy(let d), .idle(let d): return d
        }
    }
}

public struct ColabSessionItem: Equatable, Identifiable, Sendable {
    public var id: String { endpoint.isEmpty ? name : endpoint }
    public let name: String
    public let endpoint: String
    public let hardware: String
    public let shape: String
    public let variant: String
    public let isOrphan: Bool

    public init(name: String, endpoint: String, hardware: String, shape: String, variant: String, isOrphan: Bool = false) {
        self.name = name
        self.endpoint = endpoint
        self.hardware = hardware
        self.shape = shape
        self.variant = variant
        self.isOrphan = isOrphan
    }
}

public struct AccountRuntimeState: Equatable, Sendable {
    public var generation: Int = 0
    public var phase: InstancePhase = .offline
    public var cuBalance: Double = 0
    public var cuRatePerHour: Double = 0
    public var activeAssignments: Int = 0
    public var sessions: [ColabSessionItem] = []

    // Primary managed session details (strictly matching `account.sessionName`)
    public var activeEndpoint: String = ""
    public var activeHardware: String = ""
    public var gpuName: String = ""
    public var vramUsedMB: Int = 0
    public var vramTotalMB: Int = 0
    public var gpuUtilPercent: Int = 0
    public var cpuUtilPercent: Int = 0
    public var cudaProcessCount: Int = 0
    public var sessionUptimeSeconds: Int = 0

    // Optional LLM / Multimodal workload details
    public var modelFile: String = ""
    public var modelAlias: String = "qwen3.8-27b-uncensored"
    public var contextLimit: Int = 262144
    public var kvQuant: String = "q4_0"
    /// Trusted LLM API origin verified from the current session's start script or `/tmp/public_url.txt`.
    /// NEVER populated from untrusted workload `status.json` `ui_url`.
    public var publicURL: String = ""
    /// Session endpoint to which `publicURL` (and its Keychain LLM API key transmission) is bound.
    public var llmURLBoundEndpoint: String = ""
    /// Separate display/WebUI URL reported by non-LLM workloads (`status.json` `ui_url`). Never carries LLM API keys.
    public var displayUIURL: String = ""
    public var serverHealthy: Bool = false

    // Multimodal workload protocol / auto-detected details
    public var detectedWorkloadKind: WorkloadKind? = nil
    public var workloadName: String = ""
    public var workloadMetricLabel: String = ""
    public var workloadMetricValue: String = ""
    public var workloadMetricUnit: String = ""
    public var workloadMetricRatio: Double? = nil
    public var workloadDetail: String = ""
    public var queueRunning: Int = 0
    public var queuePending: Int = 0
    public var detectedServicePort: Int? = nil
    public var queueInFlightProvisioning: Bool = false

    /// Returns true for free-tier runtimes without positive paid Compute Units balance,
    /// where Google Colab FAQ restricts remote SSH and standalone WebUI tunneling.
    public func isFreeRuntimeRestricted(for account: AccountProfile) -> Bool {
        account.tier == .free && cuBalance <= 0.01
    }

    public var isTPUHardware: Bool {
        let src = (gpuName.isEmpty ? activeHardware : gpuName).uppercased()
        return src.contains("TPU") || src.contains("V6E") || src.contains("V5E") || src.contains("V2-") || src.contains("V3-")
    }

    // Live activity signal (three-state) & micro-chart telemetry history
    public var activitySignal: ActivitySignal = .unknown
    public var signalSource: ActivitySignalSource = .none
    public var lastTaskID: Int = -1
    public var promptTokensUsed: Int = 0
    public var gpuUtilHistory: [Double] = []
    public var vramHistory: [Double] = []

    public mutating func appendTelemetrySample(gpuUtil: Int, vramRatio: Double) {
        gpuUtilHistory.append(min(100, max(0, Double(gpuUtil))))
        if gpuUtilHistory.count > 24 { gpuUtilHistory.removeFirst(gpuUtilHistory.count - 24) }
        vramHistory.append(min(1.0, max(0.0, vramRatio)) * 100.0)
        if vramHistory.count > 24 { vramHistory.removeFirst(vramHistory.count - 24) }
    }

    // Idle watchdog tracking
    public var lastActiveAt: Date = Date()
    public var idleSeconds: Int = 0
    public var warnedForCurrentIdle: Bool = false

    // Bookkeeping
    public var lastUsageFetchAt: Date? = nil
    public var ccSwitchSynced: Bool = false
    public var openCodeSynced: Bool = false
    public var codexSynced: Bool = false
    public var sshTunnelActive: Bool = false
    /// Verified actual bound local port of the active SSH tunnel process (immune to in-flight settings edits).
    public var activeSSHTunnelPort: Int? = nil
    public var queueCircuitBreakUntil: Date? = nil
    public var nonLLMReminderPending: Bool = false
    public var lastError: String? = nil

    public init() {}

    public var isProcessing: Bool {
        activitySignal.isBusy
    }

    public var vramRatio: Double {
        guard vramTotalMB > 0 else { return 0 }
        return min(1.0, max(0.0, Double(vramUsedMB) / Double(vramTotalMB)))
    }

    public var contextRatio: Double {
        guard contextLimit > 0 else { return 0 }
        return min(1.0, max(0.0, Double(promptTokensUsed) / Double(contextLimit)))
    }

    public var shortGPUName: String {
        let src = gpuName.isEmpty ? activeHardware : gpuName
        if src.localizedCaseInsensitiveContains("H100") { return "H100" }
        if src.localizedCaseInsensitiveContains("A100") { return "A100" }
        if src.localizedCaseInsensitiveContains("6000") || src.localizedCaseInsensitiveContains("G4") { return "G4" }
        if src.localizedCaseInsensitiveContains("L4") { return "L4" }
        if src.localizedCaseInsensitiveContains("T4") { return "T4" }
        if src.localizedCaseInsensitiveContains("V6E") { return "TPU v6e" }
        if src.localizedCaseInsensitiveContains("V5E") { return "TPU v5e" }
        if src.localizedCaseInsensitiveContains("CPU") { return "CPU" }
        return src.isEmpty ? "Runtime" : src
    }

    public var shortModelName: String {
        if modelFile.isEmpty { return modelAlias }
        return modelFile
            .replacingOccurrences(of: ".gguf", with: "")
            .replacingOccurrences(of: "Huihui-", with: "")
    }

    public func estimatedHoursRemaining(for tier: SubscriptionTier) -> Double? {
        guard cuBalance > 0 else { return nil }
        let rate = cuRatePerHour > 0
            ? cuRatePerHour
            : AcceleratorOption.estimatedHourlyCU(forHardware: gpuName.isEmpty ? activeHardware : gpuName)
        guard rate > 0 else { return nil }
        return cuBalance / rate
    }

    public func remainingCapSeconds(for tier: SubscriptionTier) -> Int {
        max(0, tier.maxSessionHours * 3600 - sessionUptimeSeconds)
    }

    public func remainingIdleSeconds(timeoutMinutes: Int) -> Int {
        max(0, max(5, timeoutMinutes) * 60 - idleSeconds)
    }
}

public struct SlotInfo: Decodable, Sendable {
    public let id: Int?
    public let n_ctx: Int?
    public let is_processing: Bool?
    public let id_task: Int?
    public let n_prompt_tokens: Int?
}
