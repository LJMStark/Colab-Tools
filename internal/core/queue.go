package core

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
)

const (
	QueueMaxAttempts                  = 5
	QueueMaxTotalWait                 = 40 * time.Minute
	QueueMinProvisionBudget           = 15 * time.Second
	QueueScheduledGraceWindow         = 10 * time.Minute
	QueueCircuitBreakerCooldown       = 15 * time.Minute
	QueueNonLLMPostAllocateReminder   = 10 * time.Minute
)

var QueueRoundBaseDelays = []time.Duration{
	3 * time.Minute,
	6 * time.Minute,
	9 * time.Minute,
	12 * time.Minute,
}

type ProvisionOutcomeKind string

const (
	OutcomeAllocated          ProvisionOutcomeKind = "allocated"
	OutcomeCapacity503        ProvisionOutcomeKind = "capacity_503"
	OutcomeBudgetExhausted    ProvisionOutcomeKind = "budget_exhausted"
	OutcomeUnsupportedTier400 ProvisionOutcomeKind = "unsupported_400"
	OutcomeCircuitBroken      ProvisionOutcomeKind = "circuit_broken"
)

type ProvisionOutcome struct {
	Kind     ProvisionOutcomeKind
	Hardware string
	Code     string
	Message  string
}

type ScheduleDecisionKind string

const (
	ScheduleWaiting       ScheduleDecisionKind = "waiting"
	ScheduleTriggerNow    ScheduleDecisionKind = "trigger_now"
	ScheduleMissedExpired ScheduleDecisionKind = "missed_expired"
)

type ScheduleDecision struct {
	Kind       ScheduleDecisionKind
	NextFireAt *time.Time
}

// SanitizedQueueTargets filters and deduplicates against legal subscription tier while preserving user priority order.
func SanitizedQueueTargets(selected []AcceleratorOption, tier SubscriptionTier) []AcceleratorOption {
	allowed := tier.QueueableAccelerators()
	seen := make(map[AcceleratorOption]bool, len(selected))
	ordered := make([]AcceleratorOption, 0, len(selected))
	for _, opt := range selected {
		if slices.Contains(allowed, opt) && !seen[opt] {
			seen[opt] = true
			ordered = append(ordered, opt)
		}
	}
	if len(ordered) > 0 {
		return ordered
	}
	if len(allowed) > 0 {
		return []AcceleratorOption{allowed[0]}
	}
	return []AcceleratorOption{AccT4}
}

// QueueTargetsForRound implements two-stage auto-fallback:
// rounds 1..fallbackAfterRounds chase primary scarce cards exclusively; round fallbackAfterRounds+1 appends fallback card.
func QueueTargetsForRound(
	attempt int,
	primaryTargets []AcceleratorOption,
	fallbackEnabled bool,
	fallbackAfterRounds int,
	fallbackTarget AcceleratorOption,
	tier SubscriptionTier,
) []AcceleratorOption {
	base := SanitizedQueueTargets(primaryTargets, tier)
	threshold := clampInt(fallbackAfterRounds, 1, QueueMaxAttempts-1)
	if fallbackEnabled && attempt > threshold {
		if slices.Contains(tier.QueueableAccelerators(), fallbackTarget) && !slices.Contains(base, fallbackTarget) {
			base = append(base, fallbackTarget)
		}
	}
	return base
}

func EligibleFallbackTargets(excluding []AcceleratorOption, tier SubscriptionTier) []AcceleratorOption {
	primary := SanitizedQueueTargets(excluding, tier)
	var out []AcceleratorOption
	for _, opt := range tier.QueueableAccelerators() {
		if !slices.Contains(primary, opt) {
			out = append(out, opt)
		}
	}
	return out
}

func DefaultFallbackTarget(excluding []AcceleratorOption, tier SubscriptionTier) AcceleratorOption {
	candidates := EligibleFallbackTargets(excluding, tier)
	if slices.Contains(candidates, AccL4) {
		return AccL4
	}
	if len(candidates) > 0 {
		return candidates[0]
	}
	q := tier.QueueableAccelerators()
	if len(q) > 0 {
		return q[len(q)-1]
	}
	return AccT4
}

func NextScheduledFireDate(hour, minute int, now time.Time) time.Time {
	h := clampInt(hour, 0, 23)
	m := clampInt(minute, 0, 59)
	loc := now.Location()
	today := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, loc)
	if today.After(now) {
		return today
	}
	tomorrow := now.AddDate(0, 0, 1)
	return time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), h, m, 0, 0, loc)
}

func EvaluateSchedule(fireAt time.Time, repeatDaily bool, hour, minute int, now time.Time) ScheduleDecision {
	if now.Before(fireAt) {
		return ScheduleDecision{Kind: ScheduleWaiting}
	}
	var nextDaily *time.Time
	if repeatDaily {
		anchor := now
		if plusMin := fireAt.Add(time.Minute); plusMin.After(anchor) {
			anchor = plusMin
		}
		nf := NextScheduledFireDate(hour, minute, anchor)
		nextDaily = &nf
	}
	if now.Sub(fireAt) <= QueueScheduledGraceWindow {
		return ScheduleDecision{Kind: ScheduleTriggerNow, NextFireAt: nextDaily}
	}
	return ScheduleDecision{Kind: ScheduleMissedExpired, NextFireAt: nextDaily}
}

// NextQueueDelay computes AWS/Google Equal Jitter backoff delay, returning (0, false) when caps are exhausted.
func NextQueueDelay(completedAttempts int, startedAt, now time.Time, unitRandom float64) (time.Duration, bool) {
	if completedAttempts < 1 || completedAttempts >= QueueMaxAttempts {
		return 0, false
	}
	idx := completedAttempts - 1
	if idx >= len(QueueRoundBaseDelays) {
		idx = len(QueueRoundBaseDelays) - 1
	}
	capSec := QueueRoundBaseDelays[idx].Seconds()
	r := math.Min(1.0, math.Max(0.0, unitRandom))
	delaySec := math.Round((capSec / 2.0) + r*(capSec/2.0))
	elapsed := now.Sub(startedAt).Seconds()
	if elapsed < 0 {
		elapsed = 0
	}
	if elapsed+delaySec > QueueMaxTotalWait.Seconds() {
		return 0, false
	}
	return time.Duration(delaySec) * time.Second, true
}

func InterTargetDelay(unitRandom float64) time.Duration {
	r := math.Min(1.0, math.Max(0.0, unitRandom))
	return time.Duration(math.Round(12.0+r*8.0)) * time.Second
}

// ClassifyProvisionFailure classifies a failed `colab new` run.
// ONLY explicit HTTP 503 capacity shortage allows polite queueing; 412/429/401/403/timeout/unknown circuit-break immediately.
func ClassifyProvisionFailure(exitCode int, timedOut bool, stdout, stderr, targetDisplay string) ProvisionOutcome {
	if timedOut {
		return ProvisionOutcome{
			Kind:    OutcomeCircuitBroken,
			Code:    "TIMEOUT",
			Message: fmt.Sprintf("申请 %s 超时，结果不确定，已熔断停止以防重复计费", targetDisplay),
		}
	}
	combined := strings.ToLower(stderr + "\n" + stdout)
	if strings.Contains(combined, "abusive") || strings.Contains(combined, "blocked from accessing") {
		return ProvisionOutcome{
			Kind:    OutcomeCircuitBroken,
			Code:    "ABUSE_BLOCK",
			Message: "检测到官方风控/访问限制提示，已立即熔断停止排队",
		}
	}
	if strings.Contains(combined, "429") || strings.Contains(combined, "too many requests") || strings.Contains(combined, "rate limit") {
		return ProvisionOutcome{
			Kind:    OutcomeCircuitBroken,
			Code:    "429",
			Message: "触发官方限流 (429)，已熔断停止并进入 15 分钟冷却",
		}
	}
	if strings.Contains(combined, "412") || strings.Contains(combined, "precondition failed") || strings.Contains(combined, "too many assignments") {
		return ProvisionOutcome{
			Kind:    OutcomeCircuitBroken,
			Code:    "412",
			Message: "活跃会话或配额已达上限 (412)，已熔断停止，请先关闭已有会话",
		}
	}
	if strings.Contains(combined, "401") || strings.Contains(combined, "403") || strings.Contains(combined, "unauthorized") || strings.Contains(combined, "forbidden") {
		return ProvisionOutcome{
			Kind:    OutcomeCircuitBroken,
			Code:    "401/403",
			Message: "认证失效或无权限 (401/403)，已熔断停止，请检查账号授权",
		}
	}
	if strings.Contains(combined, "503") || strings.Contains(combined, "service unavailable") {
		return ProvisionOutcome{
			Kind:    OutcomeCapacity503,
			Message: fmt.Sprintf("%s 当前无可用库存 (503)", targetDisplay),
		}
	}
	if strings.Contains(combined, "400") || strings.Contains(combined, "bad request") {
		return ProvisionOutcome{
			Kind:    OutcomeUnsupportedTier400,
			Message: fmt.Sprintf("当前订阅不支持 %s (400)", targetDisplay),
		}
	}
	return ProvisionOutcome{
		Kind:    OutcomeCircuitBroken,
		Code:    fmt.Sprintf("ERR_%d", exitCode),
		Message: fmt.Sprintf("%s 申请异常（退出码 %d），已熔断停止", targetDisplay, exitCode),
	}
}

func QueueTargetsSummary(targets []AcceleratorOption) string {
	parts := make([]string, 0, len(targets))
	for _, t := range targets {
		parts = append(parts, t.ShortChipLabel())
	}
	return strings.Join(parts, " → ")
}
