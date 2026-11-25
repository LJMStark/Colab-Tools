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
    /// Maximum staleness of the last `/slots` observation before treating activity as `.unknown`.
    public static let defaultSignalMaxAgeSeconds: TimeInterval = 90

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
        maxSignalAgeSeconds: TimeInterval = defaultSignalMaxAgeSeconds
    ) -> WatchdogAction {
        guard isRunning, autoShutdownEnabled else { return .none }

        // Without LLM /slots telemetry, we cannot reliably verify notebook/training idle state.
        guard enableLLMModule else { return .pausedNoSignal }

        switch activitySignal {
        case .unknown:
            return .pausedNoSignal
        case .busy(let observedAt):
            // If busy observation itself went stale, downgrade to unknown (never assume idle)
            if now.timeIntervalSince(observedAt) > maxSignalAgeSeconds {
                return .pausedNoSignal
            }
            return .none
        case .idle(let observedAt):
            // Require recent valid idle sampling before counting toward shutdown
            if now.timeIntervalSince(observedAt) > maxSignalAgeSeconds {
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
