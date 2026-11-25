import Foundation

public enum WatchdogAction: Equatable, Sendable {
    case none
    case pausedNoSignal
    case warn(remainingMinutes: Int)
    case shutdown
}

/// Pure, deterministic evaluator for per-account custom idle shutdown and polite API cooldowns.
/// Strictly requires a fresh, verified `.idle` signal from the workload before advancing or triggering shutdown,
/// ensuring active notebooks, training jobs, or transient network glitches are never killed by mistake.
public enum IdleWatchdogEvaluator {
    /// Maximum staleness of fast `/slots` observations before treating activity as `.unknown`.
    public static let defaultSignalMaxAgeSeconds: TimeInterval = 90
    /// Maximum staleness for polite 60s remote hardware/status probes (`GPU+CPU` dual low-load / `status.json` / ComfyUI `/queue`).
    public static let hardwareSignalMaxAgeSeconds: TimeInterval = 210
    /// Maximum remote file mtime age for `/tmp/colab-tools-status.json` before treating its `busy` flag as stale.
    public static let statusFileMaxAgeSeconds: Int = 300
    /// Dual low-load thresholds: BOTH GPU < 5% AND host CPU < 25% must hold to count as idle
    /// (prevents false shutdowns during 20GB model downloads or FFmpeg CPU video transcoding).
    public static let hardwareIdleMaxGPUPercent: Int = 5
    public static let hardwareIdleMaxCPUPercent: Int = 25

    /// Pure dual-low-load hardware activity classifier for non-LLM GPU workloads without an app-level signal.
    /// Missing readings (`nil`) or TPU hardware NEVER map to 0 / idle — they safely return `.unknown`.
    public static func evaluateHardwareActivity(
        gpuUtilPercent: Int?,
        cpuUtilPercent: Int?,
        isTPU: Bool = false,
        now: Date = Date()
    ) -> ActivitySignal {
        guard !isTPU, let gpu = gpuUtilPercent, let cpu = cpuUtilPercent else {
            return .unknown
        }
        if gpu < hardwareIdleMaxGPUPercent && cpu < hardwareIdleMaxCPUPercent {
            return .idle(observedAt: now)
        }
        return .busy(observedAt: now)
    }

    /// Aggregates all slots from llama-server `/slots` JSON array.
    /// - Any slot explicitly processing -> `.busy`
    /// - All slots explicitly non-processing (`false`) -> `.idle`
    /// - Empty array or any slot missing `is_processing` -> `.unknown` (never assume idle)
    public static func evaluateLLMSlots(
        _ slots: [SlotInfo],
        now: Date = Date()
    ) -> (signal: ActivitySignal, maxPromptTokens: Int, maxContext: Int, latestTaskID: Int) {
        guard !slots.isEmpty else {
            return (.unknown, 0, 0, -1)
        }
        var anyBusy = false
        var allExplicitIdle = true
        var maxPrompt = 0
        var maxCtx = 0
        var latestTask = -1
        for slot in slots {
            if let p = slot.n_prompt_tokens { maxPrompt = max(maxPrompt, p) }
            if let c = slot.n_ctx { maxCtx = max(maxCtx, c) }
            if let t = slot.id_task { latestTask = max(latestTask, t) }
            switch slot.is_processing {
            case .some(true):
                anyBusy = true
                allExplicitIdle = false
            case .some(false):
                break
            case .none:
                allExplicitIdle = false
            }
        }
        let signal: ActivitySignal = anyBusy ? .busy(observedAt: now) : (allExplicitIdle ? .idle(observedAt: now) : .unknown)
        return (signal, maxPrompt, maxCtx, latestTask)
    }

    public static func evaluate(
        isRunning: Bool,
        enableLLMModule: Bool,
        activitySignal: ActivitySignal,
        autoShutdownEnabled: Bool,
        idleSeconds: Int,
        timeoutMinutes: Int,
        warnBeforeMinutes: Int,
        alreadyWarned: Bool,
        now: Date = Date(),
        maxSignalAgeSeconds: TimeInterval = defaultSignalMaxAgeSeconds,
        allowHardwareFallback: Bool = false
    ) -> WatchdogAction {
        guard isRunning, autoShutdownEnabled else { return .none }

        // Require either LLM telemetry or explicit multimodal/hardware fallback signal before counting toward shutdown.
        guard enableLLMModule || allowHardwareFallback else { return .pausedNoSignal }

        let effectiveMaxAge = allowHardwareFallback && !enableLLMModule
            ? max(maxSignalAgeSeconds, hardwareSignalMaxAgeSeconds)
            : maxSignalAgeSeconds

        switch activitySignal {
        case .unknown:
            return .pausedNoSignal
        case .busy(let observedAt):
            // If busy observation itself went stale, downgrade to unknown (never assume idle)
            if now.timeIntervalSince(observedAt) > effectiveMaxAge {
                return .pausedNoSignal
            }
            return .none
        case .idle(let observedAt):
            // Require recent valid idle sampling before counting toward shutdown
            if now.timeIntervalSince(observedAt) > effectiveMaxAge {
                return .pausedNoSignal
            }
        }

        let limitSec = max(5, timeoutMinutes) * 60
        let warnSec = max(0, limitSec - max(0, warnBeforeMinutes) * 60)

        if idleSeconds >= limitSec {
            return .shutdown
        }
        if warnBeforeMinutes > 0 && !alreadyWarned && idleSeconds >= warnSec {
            let leftMin = max(1, (limitSec - idleSeconds + 59) / 60)
            return .warn(remainingMinutes: leftMin)
        }
        return .none
    }

    public static func canRefresh(lastFetch: Date?, now: Date = Date(), minCooldownSeconds: Double) -> Bool {
        guard let lastFetch else { return true }
        return now.timeIntervalSince(lastFetch) >= minCooldownSeconds
    }
}
