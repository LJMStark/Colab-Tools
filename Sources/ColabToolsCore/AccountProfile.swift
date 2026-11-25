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
    public var enableLLMModule: Bool

    // Idle shutdown watchdog settings (customizable per account)
    public var autoShutdownEnabled: Bool
    public var idleTimeoutMinutes: Int
    public var warnBeforeMinutes: Int

    // Optional LLM & CC Switch integration
    public var driveScriptPath: String
    public var autoSyncCCSwitch: Bool
    public var ccSwitchProviderId: String
    public var ccSwitchProviderName: String
    public var localEnvPath: String
    public var localSSHTunnelPort: Int
    public var remoteLLMPort: Int

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
        enableLLMModule: Bool = false,
        autoShutdownEnabled: Bool = false,
        idleTimeoutMinutes: Int = 40,
        warnBeforeMinutes: Int = 2,
        driveScriptPath: String = "/content/drive/MyDrive/colab-llm/scripts/start_server.sh",
        autoSyncCCSwitch: Bool = true,
        ccSwitchProviderId: String = "colab-qwen38-uncensored",
        ccSwitchProviderName: String = "Colab Qwen3.8-27B (无审查)",
        localEnvPath: String = "",
        localSSHTunnelPort: Int = 18080,
        remoteLLMPort: Int = 18080
    ) {
        self.id = id
        self.displayName = displayName
        self.email = email
        self.colorTag = colorTag
        self.tier = tier
        self.authMode = authMode
        self.sessionName = InputValidator.sanitizeSessionName(sessionName)
        self.preferredAccelerator = preferredAccelerator
        self.highMem = highMem
        self.enableLLMModule = enableLLMModule
        self.autoShutdownEnabled = autoShutdownEnabled
        self.idleTimeoutMinutes = max(5, min(720, idleTimeoutMinutes))
        self.warnBeforeMinutes = max(0, min(15, warnBeforeMinutes))
        self.driveScriptPath = driveScriptPath
        self.autoSyncCCSwitch = autoSyncCCSwitch
        self.ccSwitchProviderId = ccSwitchProviderId
        self.ccSwitchProviderName = ccSwitchProviderName
        self.localEnvPath = localEnvPath
        self.localSSHTunnelPort = max(1024, min(65535, localSSHTunnelPort))
        self.remoteLLMPort = max(1024, min(65535, remoteLLMPort))
    }

    // Backwards-compatible decoding when older config.json lacks `remoteLLMPort`
    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(UUID.self, forKey: .id)
        displayName = try c.decode(String.self, forKey: .displayName)
        email = try c.decode(String.self, forKey: .email)
        colorTag = try c.decode(AccountColorTag.self, forKey: .colorTag)
        tier = try c.decode(SubscriptionTier.self, forKey: .tier)
        authMode = try c.decode(AccountAuthMode.self, forKey: .authMode)
        sessionName = InputValidator.sanitizeSessionName(try c.decode(String.self, forKey: .sessionName))
        preferredAccelerator = try c.decode(AcceleratorOption.self, forKey: .preferredAccelerator)
        highMem = try c.decode(Bool.self, forKey: .highMem)
        enableLLMModule = try c.decode(Bool.self, forKey: .enableLLMModule)
        autoShutdownEnabled = try c.decode(Bool.self, forKey: .autoShutdownEnabled)
        idleTimeoutMinutes = max(5, min(720, try c.decode(Int.self, forKey: .idleTimeoutMinutes)))
        warnBeforeMinutes = max(0, min(15, try c.decode(Int.self, forKey: .warnBeforeMinutes)))
        driveScriptPath = try c.decode(String.self, forKey: .driveScriptPath)
        autoSyncCCSwitch = try c.decode(Bool.self, forKey: .autoSyncCCSwitch)
        ccSwitchProviderId = try c.decode(String.self, forKey: .ccSwitchProviderId)
        ccSwitchProviderName = try c.decode(String.self, forKey: .ccSwitchProviderName)
        localEnvPath = try c.decode(String.self, forKey: .localEnvPath)
        localSSHTunnelPort = try c.decodeIfPresent(Int.self, forKey: .localSSHTunnelPort) ?? 18080
        remoteLLMPort = try c.decodeIfPresent(Int.self, forKey: .remoteLLMPort) ?? 18080
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

    public static let configDir = FileManager.default.homeDirectoryForCurrentUser
        .appendingPathComponent(".config/colab-tools", isDirectory: true)
    public static let configFile = configDir.appendingPathComponent("config.json")

    public init() {}

    public static func load() -> AppSettings {
        if let data = try? Data(contentsOf: configFile),
           var loaded = try? JSONDecoder().decode(AppSettings.self, from: data) {
            loaded.backgroundRefreshSeconds = max(60, min(900, loaded.backgroundRefreshSeconds))
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
    case running
    case stopping
    case error(String)

    public var isRunning: Bool {
        if case .running = self { return true }
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
    public var sessionUptimeSeconds: Int = 0

    // Optional LLM server details
    public var modelFile: String = ""
    public var modelAlias: String = "qwen3.8-27b-uncensored"
    public var contextLimit: Int = 262144
    public var kvQuant: String = "q4_0"
    public var publicURL: String = ""
    public var serverHealthy: Bool = false

    // Live /slots activity (three-state)
    public var activitySignal: ActivitySignal = .unknown
    public var lastTaskID: Int = -1
    public var promptTokensUsed: Int = 0

    // Idle watchdog tracking
    public var lastActiveAt: Date = Date()
    public var idleSeconds: Int = 0
    public var warnedForCurrentIdle: Bool = false

    // Bookkeeping
    public var lastUsageFetchAt: Date? = nil
    public var ccSwitchSynced: Bool = false
    public var sshTunnelActive: Bool = false
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
