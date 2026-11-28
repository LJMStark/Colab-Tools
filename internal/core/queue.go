package core

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
)

const (
	QueueMaxAttempts            = 5
	QueueMaxTotalWait           = 40 * time.Minute
	QueuePersistentDefaultHours = 8
	QueueMinProvisionBudget     = 15 * time.Second
	QueueCircuitBreakerCooldown = 15 * time.Minute
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
	OutcomePrecondition412    ProvisionOutcomeKind = "precondition_412"
	OutcomeAuthError          ProvisionOutcomeKind = "auth_error"
	OutcomeTransientError     ProvisionOutcomeKind = "transient_error"
	OutcomeRateLimited        ProvisionOutcomeKind = "rate_limited"
	// OutcomeCircuitBroken is kept as an alias for OutcomeRateLimited (true rate-limit / abuse block only).
	OutcomeCircuitBroken ProvisionOutcomeKind = OutcomeRateLimited
)

type ProvisionOutcome struct {
	Kind      ProvisionOutcomeKind
	Hardware  string
	Code      string
	Message   string
	RawDetail string
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
	if tier == TierFree {
		return AccT4
	}
	candidates := EligibleFallbackTargets(excluding, tier)
	if slices.Contains(candidates, AccL4) {
		return AccL4
	}
	if slices.Contains(candidates, AccT4) {
		return AccT4
	}
	// Never pick high-cost cards (H100/G4/A100) as a fallback when L4/T4 are already present or excluded.
	allowed := tier.QueueableAccelerators()
	if slices.Contains(allowed, AccT4) {
		return AccT4
	}
	if slices.Contains(allowed, AccL4) {
		return AccL4
	}
	return AccT4
}

// EffectivePersistentHours 返回持续守候模式的有效最大守候时长（<=0 默认 8 小时，范围限制在 1..24 小时）。
func EffectivePersistentHours(acc AccountProfile) int {
	if acc.QueuePersistentMaxHours <= 0 {
		return QueuePersistentDefaultHours
	}
	return clampInt(acc.QueuePersistentMaxHours, 1, 24)
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

// NextQueueDelayForMode 根据标准模式或持续守候模式计算下一轮排队退避时间。
// 休眠唤醒后只检查实际总经过时长 elapsed，不补发积压请求。
func NextQueueDelayForMode(completedAttempts int, startedAt, now time.Time, unitRandom float64, persistent bool, maxHours int) (time.Duration, bool) {
	if !persistent {
		return NextQueueDelay(completedAttempts, startedAt, now, unitRandom)
	}
	if completedAttempts < 1 {
		return 0, false
	}
	r := math.Min(1.0, math.Max(0.0, unitRandom))
	var delaySec float64
	if completedAttempts <= 3 {
		capSec := QueueRoundBaseDelays[completedAttempts-1].Seconds()
		delaySec = math.Round((capSec / 2.0) + r*(capSec/2.0))
	} else {
		// 第 4 轮及之后：采用 8~12 分钟/轮（480s..720s）随机间隔
		delaySec = math.Round(480.0 + r*240.0)
	}
	maxWait := time.Duration(clampInt(maxHours, 1, 24)) * time.Hour
	elapsed := now.Sub(startedAt).Seconds()
	if elapsed < 0 {
		elapsed = 0
	}
	if elapsed+delaySec > maxWait.Seconds() {
		return 0, false
	}
	return time.Duration(delaySec) * time.Second, true
}

func InterTargetDelay(unitRandom float64) time.Duration {
	r := math.Min(1.0, math.Max(0.0, unitRandom))
	return time.Duration(math.Round(12.0+r*8.0)) * time.Second
}

// SanitizeRawErrorSnippet extracts one compact, readable diagnostic line from CLI output.
func SanitizeRawErrorSnippet(stdout, stderr string) string {
	pick := func(raw string) string {
		lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
		for i := len(lines) - 1; i >= 0; i-- {
			ln := strings.TrimSpace(lines[i])
			if ln == "" || strings.HasPrefix(ln, "Traceback ") || strings.HasPrefix(ln, "File \"") || strings.HasPrefix(ln, "[colab] Creating session") {
				continue
			}
			if len(ln) > 180 {
				ln = ln[:177] + "..."
			}
			return ln
		}
		return ""
	}
	if s := pick(stderr); s != "" {
		return s
	}
	return pick(stdout)
}

// ClassifyProvisionFailure classifies a failed `colab new` run against real google-colab-cli 0.8.x outputs.
// Only true 429 / abuse warnings trigger the 15-minute cooldown; single-card rejections (400 / Backend rejected),
// capacity limits (503/502/504/ReadTimeout), and single-card 412s allow trying the next card or actionable recovery.
func ClassifyProvisionFailure(exitCode int, timedOut bool, stdout, stderr, targetDisplay string) ProvisionOutcome {
	raw := SanitizeRawErrorSnippet(stdout, stderr)
	if timedOut {
		return ProvisionOutcome{
			Kind:      OutcomeTransientError,
			Hardware:  targetDisplay,
			Code:      "TIMEOUT",
			Message:   fmt.Sprintf("申请 %s 响应超时，请稍等几秒直接重试（若已开机可先点右上角刷新）", targetDisplay),
			RawDetail: raw,
		}
	}
	combined := strings.ToLower(stderr + "\n" + stdout)
	if strings.Contains(combined, "abusive") || strings.Contains(combined, "blocked from accessing") {
		return ProvisionOutcome{
			Kind:      OutcomeRateLimited,
			Hardware:  targetDisplay,
			Code:      "ABUSE_BLOCK",
			Message:   "Google 提示当前账号访问受限，为保护账号先暂停自动申请 15 分钟",
			RawDetail: raw,
		}
	}
	if strings.Contains(combined, "429") || strings.Contains(combined, "too many requests") || strings.Contains(combined, "rate limit") {
		return ProvisionOutcome{
			Kind:      OutcomeRateLimited,
			Hardware:  targetDisplay,
			Code:      "429",
			Message:   "Google 提示请求偏频繁 (429)，先冷静等待 15 分钟再试以保护账号",
			RawDetail: raw,
		}
	}
	if strings.Contains(combined, "401") || strings.Contains(combined, "403") || strings.Contains(combined, "unauthorized") || strings.Contains(combined, "forbidden") || strings.Contains(combined, "invalid_grant") || strings.Contains(combined, "reauthenticate") {
		return ProvisionOutcome{
			Kind:      OutcomeAuthError,
			Hardware:  targetDisplay,
			Code:      "AUTH",
			Message:   "Google 账号登录已过期或无权限，请去「账号」页重新授权后再开机",
			RawDetail: raw,
		}
	}
	if strings.Contains(combined, "412") || strings.Contains(combined, "precondition failed") || strings.Contains(combined, "too many assignments") || strings.Contains(combined, "allocation refused") {
		return ProvisionOutcome{
			Kind:      OutcomePrecondition412,
			Hardware:  targetDisplay,
			Code:      "412",
			Message:   fmt.Sprintf("Google 暂时拒绝了 %s 申请（可能已有运行中机器，或该卡暂时满载）", targetDisplay),
			RawDetail: raw,
		}
	}
	if strings.Contains(combined, "backend rejected accelerator") || strings.Contains(combined, "not have quota or entitlement") || strings.Contains(combined, "400") || strings.Contains(combined, "bad request") {
		return ProvisionOutcome{
			Kind:      OutcomeUnsupportedTier400,
			Hardware:  targetDisplay,
			Code:      "400",
			Message:   fmt.Sprintf("当前账号暂不支持或暂无 %s 配额，将自动改试下一张卡", targetDisplay),
			RawDetail: raw,
		}
	}
	if strings.Contains(combined, "503") || strings.Contains(combined, "502") || strings.Contains(combined, "504") || strings.Contains(combined, "service unavailable") || strings.Contains(combined, "bad gateway") || strings.Contains(combined, "gateway timeout") || strings.Contains(combined, "readtimeout") || strings.Contains(combined, "read timed out") || strings.Contains(combined, "insufficient capacity") || strings.Contains(combined, "cannot connect to gpu backend") {
		return ProvisionOutcome{
			Kind:      OutcomeCapacity503,
			Hardware:  targetDisplay,
			Code:      "503",
			Message:   fmt.Sprintf("%s 当前暂时没货，将自动改试下一张卡", targetDisplay),
			RawDetail: raw,
		}
	}
	return ProvisionOutcome{
		Kind:      OutcomeTransientError,
		Hardware:  targetDisplay,
		Code:      fmt.Sprintf("ERR_%d", exitCode),
		Message:   fmt.Sprintf("申请 %s 暂时失败（退出码 %d），检查网络或换张卡后可直接重试", targetDisplay, exitCode),
		RawDetail: raw,
	}
}

func QueueTargetsSummary(targets []AcceleratorOption) string {
	parts := make([]string, 0, len(targets))
	for _, t := range targets {
		parts = append(parts, t.ShortChipLabel())
	}
	return strings.Join(parts, " → ")
}
