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
    /// Dual low-load thresholds: BOTH GPU < 5% AND host CPU < 25% must hold to count as idle
    /// (prevents false shutdowns during 20GB model downloads or FFmpeg CPU video transcoding).
    public static let hardwareIdleMaxGPUPercent: Int = 5
    public static let hardwareIdleMaxCPUPercent: Int = 25

    /// Pure dual-low-load hardware activity classifier for non-LLM workloads without an app-level signal.
    public static func evaluateHardwareActivity(
        gpuUtilPercent: Int,
        cpuUtilPercent: Int,
        now: Date = Date()
    ) -> ActivitySignal {
        if gpuUtilPercent < hardwareIdleMaxGPUPercent && cpuUtilPercent < hardwareIdleMaxCPUPercent {
            return .idle(observedAt: now)
        }
        return .busy(observedAt: now)
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
