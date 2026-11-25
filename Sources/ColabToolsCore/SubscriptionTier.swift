import Foundation

/// Official Google AI / Colab subscription tiers and their accelerator capabilities.
public enum SubscriptionTier: String, CaseIterable, Codable, Identifiable, Sendable {
    case aiUltra = "ai_ultra"
    case colabProPlus = "colab_pro_plus"
    case aiPro = "ai_pro"
    case colabPro = "colab_pro"
    case payAsYouGo = "pay_as_you_go"
    case free = "free"

    public var id: String { rawValue }

    public var displayName: String {
        switch self {
        case .aiUltra: return "Google AI Ultra"
        case .colabProPlus: return "Colab Pro+"
        case .aiPro: return "Google AI Pro"
        case .colabPro: return "Colab Pro"
        case .payAsYouGo: return "Pay As You Go"
        case .free: return "Free 免费版"
        }
    }

    public var shortBadge: String {
        switch self {
        case .aiUltra: return "AI Ultra"
        case .colabProPlus: return "Pro+"
        case .aiPro: return "AI Pro"
        case .colabPro: return "Pro"
        case .payAsYouGo: return "PAYG"
        case .free: return "Free"
        }
    }

    public var summaryNote: String {
        switch self {
        case .aiUltra: return "5x (1000 CU) / 20x (2000 CU) · G4 / H100 / A100 · 24h"
        case .colabProPlus: return "~600 CU/月 · 优先高阶 GPU + 后台运行 24h"
        case .aiPro: return "~100 CU/月 · A100 / L4 / T4 · 24h"
        case .colabPro: return "~100 CU/月 · A100 / L4 / T4 · 高内存形态"
        case .payAsYouGo: return "按量购点 · A100 / L4 / T4 · 灵活计费"
        case .free: return "无付费点数 · 仅 T4 / CPU · 最长 ~12h"
        }
    }

    /// Maximum typical single-session duration in hours (not guaranteed by Google).
    public var maxSessionHours: Int {
        self == .free ? 12 : 24
    }

    /// Whether `--high-mem` is supported on this tier.
    public var supportsHighMem: Bool {
        self != .free
    }

    /// Official accelerator choices appropriate for this subscription tier (prevents blind 400 errors).
    public var allowedAccelerators: [AcceleratorOption] {
        switch self {
        case .aiUltra, .colabProPlus:
            return [.autoBest, .g4, .h100, .a100, .l4, .t4, .tpuV6e1, .tpuV5e1, .cpu]
        case .aiPro, .colabPro, .payAsYouGo:
            return [.autoPro, .a100, .l4, .t4, .tpuV5e1, .cpu]
        case .free:
            return [.t4, .cpu]
        }
    }

    /// Individual hardware targets eligible for polite queuing under this subscription tier, ordered high-to-low.
    public var queueableAccelerators: [AcceleratorOption] {
        switch self {
        case .aiUltra, .colabProPlus:
            return [.g4, .h100, .a100, .l4, .t4, .tpuV6e1, .tpuV5e1]
        case .aiPro, .colabPro, .payAsYouGo:
            return [.a100, .l4, .t4, .tpuV5e1]
        case .free:
            return [.t4]
        }
    }

    public var defaultAccelerator: AcceleratorOption {
        switch self {
        case .aiUltra, .colabProPlus: return .autoBest
        case .aiPro, .colabPro, .payAsYouGo: return .autoPro
        case .free: return .t4
        }
    }

    /// Intelligent tier suggestion based on observed Compute Units balance from `colab usage`.
    public static func suggested(forBalance balance: Double) -> SubscriptionTier {
        if balance > 700 { return .aiUltra }
        if balance > 150 { return .colabProPlus }
        if balance > 0.01 { return .colabPro }
        return .free
    }
}

/// Supported official `colab new` accelerator targets (`--gpu` / `--tpu` / CPU).
public enum AcceleratorOption: String, CaseIterable, Codable, Identifiable, Sendable {
    case autoBest = "AUTO_BEST"
    case autoPro = "AUTO_PRO"
    case g4 = "G4"
    case h100 = "H100"
    case a100 = "A100"
    case l4 = "L4"
    case t4 = "T4"
    case tpuV6e1 = "V6E1"
    case tpuV5e1 = "V5E1"
    case cpu = "CPU"

    public var id: String { rawValue }

    public var label: String {
        switch self {
        case .autoBest: return "自动高配 (G4→H100→A100→L4)"
        case .autoPro: return "自动优选 (A100→L4→T4)"
        case .g4: return "GPU · G4 (RTX PRO 6000 96G)"
        case .h100: return "GPU · H100 (80G)"
        case .a100: return "GPU · A100 (40/80G)"
        case .l4: return "GPU · L4 (24G · 性价比主力)"
        case .t4: return "GPU · T4 (16G)"
        case .tpuV6e1: return "TPU · v6e-1"
        case .tpuV5e1: return "TPU · v5e-1"
        case .cpu: return "CPU 标准环境"
        }
    }

    public var shortChipLabel: String {
        switch self {
        case .autoBest: return "自动高配"
        case .autoPro: return "自动优选"
        case .g4: return "G4"
        case .h100: return "H100"
        case .a100: return "A100"
        case .l4: return "L4"
        case .t4: return "T4"
        case .tpuV6e1: return "v6e-1"
        case .tpuV5e1: return "v5e-1"
        case .cpu: return "CPU"
        }
    }

    public var isTPU: Bool {
        self == .tpuV6e1 || self == .tpuV5e1
    }

    public struct ProbeTarget: Equatable, Sendable {
        public let flag: String? // "--gpu", "--tpu", or nil for CPU
        public let value: String?
        public let display: String

        public init(flag: String?, value: String?, display: String) {
            self.flag = flag
            self.value = value
            self.display = display
        }
    }

    /// Graceful fallback sequence (single-shot per tier, zero aggressive retries).
    public var probeTargets: [ProbeTarget] {
        switch self {
        case .autoBest:
            return [
                .init(flag: "--gpu", value: "G4", display: "G4"),
                .init(flag: "--gpu", value: "H100", display: "H100"),
                .init(flag: "--gpu", value: "A100", display: "A100"),
                .init(flag: "--gpu", value: "L4", display: "L4")
            ]
        case .autoPro:
            return [
                .init(flag: "--gpu", value: "A100", display: "A100"),
                .init(flag: "--gpu", value: "L4", display: "L4"),
                .init(flag: "--gpu", value: "T4", display: "T4")
            ]
        case .g4: return [.init(flag: "--gpu", value: "G4", display: "G4")]
        case .h100: return [.init(flag: "--gpu", value: "H100", display: "H100")]
        case .a100: return [.init(flag: "--gpu", value: "A100", display: "A100")]
        case .l4: return [.init(flag: "--gpu", value: "L4", display: "L4")]
        case .t4: return [.init(flag: "--gpu", value: "T4", display: "T4")]
        case .tpuV6e1: return [.init(flag: "--tpu", value: "v6e1", display: "TPU v6e-1")]
        case .tpuV5e1: return [.init(flag: "--tpu", value: "v5e1", display: "TPU v5e-1")]
        case .cpu: return [.init(flag: nil, value: nil, display: "CPU")]
        }
    }

    public static func estimatedHourlyCU(forHardware raw: String) -> Double {
        let g = raw.uppercased()
        if g.contains("H100") { return 18.0 }
        if g.contains("A100") { return 12.0 }
        if g.contains("G4") || g.contains("6000") { return 7.0 }
        if g.contains("L4") { return 2.33 }
        if g.contains("T4") { return 1.3 }
        if g.contains("V6E") { return 6.0 }
        if g.contains("V5E") { return 3.0 }
        return 0.1
    }
}
