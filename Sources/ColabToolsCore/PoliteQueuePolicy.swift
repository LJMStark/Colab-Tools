import Foundation

/// Outcome of a single polite `colab new` provisioning round.
public enum ProvisionOutcome: Equatable, Sendable {
    case allocated(hardware: String)
    case capacityUnavailable503(message: String)
    case budgetExhausted(message: String)
    case unsupportedTier400(message: String)
    case circuitBroken(code: String, message: String)
}

public enum ScheduleDecision: Equatable, Sendable {
    case waiting
    case triggerNow(nextFireAt: Date?)
    case missedExpired(nextFireAt: Date?)
}

/// Best-practice anti-abuse queue policy derived from AWS/Google Equal Jitter backoff
/// and real-world `google-colab-cli` 503/412/429 rate-limit behavior.
///
/// Key safeguards:
/// - Single-layer retries only (`COLAB_CLI_ASSIGN_RETRIES=0` enforced at CLI layer).
/// - Minute-scale Equal Jitter backoff (`3m -> 6m -> 9m -> 12m` ceilings; randomized `[base/2, base]`).
/// - Strict attempt ceiling (5 rounds) and elapsed time ceiling (40 minutes).
/// - Immediate circuit-breaker on 412, 429, 401/403, abuse warnings, timeouts, or unknown errors,
///   followed by a mandatory 15-minute cooldown before queueing can restart.
public struct PoliteQueuePolicy: Sendable {
    public static let maxAttempts: Int = 5
    public static let maxTotalWaitSeconds: TimeInterval = 40 * 60
    public static let minProvisionBudgetSeconds: TimeInterval = 15
    public static let scheduledGraceWindowSeconds: TimeInterval = 10 * 60
    public static let circuitBreakerCooldownSeconds: TimeInterval = 15 * 60
    public static let nonLLMPostAllocateReminderSeconds: TimeInterval = 10 * 60

    /// Exponential base ceilings (in seconds) between round 1->2, 2->3, 3->4, 4->5.
    public static let roundBaseDelaysSeconds: [TimeInterval] = [180, 360, 540, 720]

    /// Filters and deduplicates against the account's legal subscription tier while PRESERVING user custom priority order.
    public static func sanitizedTargets(
        selected: [AcceleratorOption],
        for tier: SubscriptionTier
    ) -> [AcceleratorOption] {
        let allowedSet = Set(tier.queueableAccelerators)
        var seen: Set<AcceleratorOption> = []
        var ordered: [AcceleratorOption] = []
        for opt in selected where allowedSet.contains(opt) && !seen.contains(opt) {
            seen.insert(opt)
            ordered.append(opt)
        }
        if !ordered.isEmpty { return ordered }
        if let first = tier.queueableAccelerators.first { return [first] }
        return [.t4]
    }

    /// Two-stage queue strategy:
    /// - Rounds `1...fallbackAfterRounds` try ONLY the user's primary priority targets.
    /// - Starting at round `fallbackAfterRounds + 1`, if `fallbackEnabled` is true, appends the legal fallback card.
    public static func targetsForRound(
        attempt: Int,
        primaryTargets: [AcceleratorOption],
        fallbackEnabled: Bool,
        fallbackAfterRounds: Int,
        fallbackTarget: AcceleratorOption,
        tier: SubscriptionTier
    ) -> [AcceleratorOption] {
        var base = sanitizedTargets(selected: primaryTargets, for: tier)
        let threshold = max(1, min(maxAttempts - 1, fallbackAfterRounds))
        if fallbackEnabled && attempt > threshold {
            let allowed = Set(tier.queueableAccelerators)
            if allowed.contains(fallbackTarget) && !base.contains(fallbackTarget) {
                base.append(fallbackTarget)
            }
        }
        return base
    }

    /// Computes the next wall-clock fire date for off-peak scheduled start (today if in the future, else tomorrow).
    public static func nextScheduledFireDate(
        hour: Int,
        minute: Int,
        now: Date = Date(),
        calendar: Calendar = .current
    ) -> Date {
        let h = max(0, min(23, hour))
        let m = max(0, min(59, minute))
        var comps = calendar.dateComponents([.year, .month, .day], from: now)
        comps.hour = h
        comps.minute = m
        comps.second = 0
        if let todayTarget = calendar.date(from: comps), todayTarget > now {
            return todayTarget
        }
        let tomorrow = calendar.date(byAdding: .day, value: 1, to: now) ?? now.addingTimeInterval(86400)
        var tComps = calendar.dateComponents([.year, .month, .day], from: tomorrow)
        tComps.hour = h
        tComps.minute = m
        tComps.second = 0
        return calendar.date(from: tComps) ?? now.addingTimeInterval(86400)
    }

    /// Returns legal fallback cards that are NOT already in the user's primary target list.
    public static func eligibleFallbackTargets(
        excluding primaryTargets: [AcceleratorOption],
        for tier: SubscriptionTier
    ) -> [AcceleratorOption] {
        let primarySet = Set(sanitizedTargets(selected: primaryTargets, for: tier))
        return tier.queueableAccelerators.filter { !primarySet.contains($0) }
    }

    public static func defaultFallbackTarget(
        excluding primaryTargets: [AcceleratorOption],
        for tier: SubscriptionTier
    ) -> AcceleratorOption {
        let candidates = eligibleFallbackTargets(excluding: primaryTargets, for: tier)
        if candidates.contains(.l4) { return .l4 }
        if let first = candidates.first { return first }
        return tier.queueableAccelerators.last ?? .t4
    }

    /// Evaluates a persisted scheduled fire timestamp against `now`:
    /// - Before `fireAt`: `.waiting`.
    /// - Within `[fireAt, fireAt + 10m]`: `.triggerNow` (computes next day's slot if `repeatDaily`, else `nil`).
    /// - Past `fireAt + 10m` (e.g., Mac slept past the window): `.missedExpired` so one-shot schedules NEVER fire unexpectedly the next day!
    public static func evaluateSchedule(
        fireAt: Date,
        repeatDaily: Bool,
        hour: Int,
        minute: Int,
        now: Date = Date(),
        calendar: Calendar = .current
    ) -> ScheduleDecision {
        if now < fireAt {
            return .waiting
        }
        let nextIfDaily: Date? = repeatDaily
            ? nextScheduledFireDate(hour: hour, minute: minute, now: max(now, fireAt.addingTimeInterval(60)), calendar: calendar)
            : nil
        if now.timeIntervalSince(fireAt) <= scheduledGraceWindowSeconds {
            return .triggerNow(nextFireAt: nextIfDaily)
        }
        return .missedExpired(nextFireAt: nextIfDaily)
    }

    public static func dayStamp(for date: Date, calendar: Calendar = .current) -> String {
        let c = calendar.dateComponents([.year, .month, .day], from: date)
        return String(format: "%04d-%02d-%02d", c.year ?? 0, c.month ?? 0, c.day ?? 0)
    }

    /// Computes Equal Jitter delay before the next attempt, or `nil` when attempts or total elapsed cap are exhausted.
    /// - Parameters:
    ///   - completedAttempts: Number of rounds already attempted (1-based; 1 means round 1 just failed with 503).
    ///   - startedAt: Timestamp when the queue started.
    ///   - now: Current timestamp.
    ///   - unitRandom: Deterministic or random value in `[0.0, 1.0]`.
    public static func nextDelay(
        completedAttempts: Int,
        startedAt: Date,
        now: Date = Date(),
        unitRandom: Double = Double.random(in: 0...1)
    ) -> TimeInterval? {
        guard completedAttempts >= 1, completedAttempts < maxAttempts else { return nil }
        let idx = min(completedAttempts - 1, roundBaseDelaysSeconds.count - 1)
        let cap = roundBaseDelaysSeconds[idx]
        let clampedRand = min(1.0, max(0.0, unitRandom))
        // AWS/Google Equal Jitter: half deterministic baseline + half randomized spread
        let delay = (cap / 2.0) + (clampedRand * (cap / 2.0))
        let elapsed = max(0, now.timeIntervalSince(startedAt))
        if elapsed + delay > maxTotalWaitSeconds {
            return nil
        }
        return delay.rounded()
    }

    /// Smooth randomized pause (12–20s) between distinct target GPUs inside the same round to avoid burst traffic.
    public static func interTargetDelaySeconds(unitRandom: Double = Double.random(in: 0...1)) -> TimeInterval {
        let clamped = min(1.0, max(0.0, unitRandom))
        return (12.0 + clamped * 8.0).rounded()
    }

    /// Classifies a failed `colab new` invocation. ONLY explicit HTTP 503 capacity shortage allows polite queueing.
    public static func classifyFailure(
        exitCode: Int32,
        timedOut: Bool,
        stdout: String,
        stderr: String,
        targetDisplay: String
    ) -> ProvisionOutcome {
        if timedOut {
            return .circuitBroken(
                code: "TIMEOUT",
                message: "申请 \(targetDisplay) 超时，结果不确定，已熔断停止以防重复计费"
            )
        }
        let combined = (stderr + "\n" + stdout).lowercased()
        if combined.contains("abusive") || combined.contains("blocked from accessing") {
            return .circuitBroken(
                code: "ABUSE_BLOCK",
                message: "检测到官方风控/访问限制提示，已立即熔断停止排队"
            )
        }
        if combined.contains("429") || combined.contains("too many requests") || combined.contains("rate limit") {
            return .circuitBroken(
                code: "429",
                message: "触发官方限流 (429)，已熔断停止并进入 15 分钟冷却"
            )
        }
        if combined.contains("412") || combined.contains("precondition failed") || combined.contains("too many assignments") {
            return .circuitBroken(
                code: "412",
                message: "活跃会话或配额已达上限 (412)，已熔断停止，请先关闭已有会话"
            )
        }
        if combined.contains("401") || combined.contains("403") || combined.contains("unauthorized") || combined.contains("forbidden") {
            return .circuitBroken(
                code: "401/403",
                message: "认证失效或无权限 (401/403)，已熔断停止，请检查账号授权"
            )
        }
        if combined.contains("503") || combined.contains("service unavailable") {
            return .capacityUnavailable503(message: "\(targetDisplay) 当前无可用库存 (503)")
        }
        if combined.contains("400") || combined.contains("bad request") {
            return .unsupportedTier400(message: "当前订阅不支持 \(targetDisplay) (400)")
        }
        return .circuitBroken(
            code: "ERR_\(exitCode)",
            message: "\(targetDisplay) 申请异常（退出码 \(exitCode)），已熔断停止"
        )
    }

    public static func targetsSummary(_ targets: [AcceleratorOption]) -> String {
        targets.map(\.shortChipLabel).joined(separator: " → ")
    }
}
