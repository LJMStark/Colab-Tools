package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/LJMStark/Colab-Tools/internal/colab"
	"github.com/LJMStark/Colab-Tools/internal/core"
	"github.com/google/uuid"
)

// History / Single-Store Crash Recovery, Telemetry, Stop Verification, Watchdog & Action Prompts

func (s *Service) recordHistory(rec core.HistoryRecord) bool {
	if s.History != nil {
		if err := s.History.AddRecord(rec); err != nil {
			s.ShowToast("保存账单记录失败：" + err.Error())
			return false
		}
	}
	return true
}

func (s *Service) upsertInFlight(rec core.HistoryRecord) {
	if s.History != nil {
		_ = s.History.UpsertInFlight(rec)
	}
}

func (s *Service) flushInFlightSessions() {
	if s.History == nil {
		return
	}
	now := time.Now()
	s.mu.RLock()
	var recs []core.HistoryRecord
	for _, acc := range s.Settings.Accounts {
		rt := s.Runtime[acc.ID]
		if rec := buildInFlightRecordLocked(acc, rt, now); rec != nil {
			recs = append(recs, *rec)
		}
	}
	s.mu.RUnlock()
	for _, rec := range recs {
		_ = s.History.UpsertInFlight(rec)
	}
}

func mapShutdownReason(reason string) core.ShutdownReason {
	switch strings.TrimSpace(reason) {
	case "空闲自动关机", "临时盘宽限到期自动关机":
		return core.ShutdownAutoIdle
	case "全部关机止损":
		return core.ShutdownBatchStop
	default:
		return core.ShutdownManual
	}
}

func recipeLabelForSession(acc core.AccountProfile, downgraded bool) string {
	if acc.DeployRecipe == core.RecipeH3Video {
		if downgraded {
			return core.H3VariantFlashGGUF.DisplayName() + "（自动降级）"
		}
		return acc.H3Variant.DisplayName()
	}
	return acc.DeployRecipe.DisplayName()
}

func deterministicSessionRecordID(accountID, endpoint string, startedAt time.Time) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%d", accountID, endpoint, startedAt.UTC().Unix())))
	return "sess-" + hex.EncodeToString(sum[:8])
}

func computeSessionBilling(
	tier core.SubscriptionTier,
	hw string,
	durSec int,
	estCU float64,
	measuredRate float64,
	startBalance float64,
	startBalanceValid bool,
	adopted bool,
	maxAssignments int,
	allowMeasured bool,
	usage *colab.UsageSnapshot,
) (core.BillingBasis, float64) {
	// Never zero out billing purely from an optional Free note when measured CU burn actually occurred.
	hasMeasuredDelta := allowMeasured && usage != nil && maxAssignments <= 1 && !adopted && startBalanceValid && (startBalance-usage.Balance) > 0.01
	if tier == core.TierFree && measuredRate <= 0 && estCU <= 0 && !hasMeasuredDelta {
		return core.BillingFree, 0
	}
	est := estCU
	if est <= 0 && durSec > 0 {
		rate := measuredRate
		if rate <= 0 {
			rate = core.EstimatedHourlyCU(hw)
		}
		est = rate * float64(durSec) / 3600.0
	}
	if allowMeasured && usage != nil && maxAssignments <= 1 && !adopted && startBalanceValid {
		delta := startBalance - usage.Balance
		if delta > 0 && (est <= 0.05 || (delta >= est*0.3 && delta <= est*2.5)) {
			return core.BillingMeasured, delta
		}
	}
	return core.BillingEstimated, est
}

func buildInFlightRecordLocked(acc core.AccountProfile, rt core.AccountRuntimeState, now time.Time) *core.HistoryRecord {
	if rt.ActiveEndpoint == "" || (!rt.Phase.IsRunning() && rt.Phase.Kind != core.PhaseStopping) {
		return nil
	}
	hw := core.NormalizeHardwareKey(rt.ActiveHardware)
	if hw == "" {
		hw = core.NormalizeHardwareKey(rt.GPUName)
	}
	if hw == "" {
		hw = strings.TrimSpace(rt.ActiveHardware)
	}
	startedAt := rt.SessionStartedAt
	if startedAt.IsZero() {
		startedAt = now.Add(-time.Duration(max(0, rt.SessionUptimeSec)) * time.Second)
	}
	lastSample := rt.SessionLastSampleAt
	if lastSample.IsZero() {
		lastSample = now
	}
	estCU := rt.SessionEstimatedCU
	if now.After(lastSample) {
		rate := rt.SessionMeasuredSingleRate
		if rate <= 0 {
			rate = core.EstimatedHourlyCU(hw)
		}
		estCU += rate * now.Sub(lastSample).Seconds() / 3600.0
	}
	sampleAt := now
	effAcc := core.SessionEffectiveProfile(acc, rt)
	var gracePtr *time.Time
	if rt.WatchdogDeferredForTempOutputs && !rt.TempGuardGraceUntil.IsZero() {
		g := rt.TempGuardGraceUntil
		gracePtr = &g
	}
	return &core.HistoryRecord{
		ID:                    deterministicSessionRecordID(acc.ID, rt.ActiveEndpoint, startedAt),
		Kind:                  core.HistoryKindSession,
		InFlight:              true,
		AccountID:             acc.ID,
		AccountName:           acc.DisplayName,
		AccountColor:          acc.ColorTag,
		SessionName:           acc.SessionName,
		Endpoint:              rt.ActiveEndpoint,
		StartedAt:             startedAt,
		Hardware:              hw,
		HighMem:               core.EffectiveUseHighMem(effAcc) && !rt.SessionUsedStandardRAM,
		RecipeLabel:           recipeLabelForSession(effAcc, rt.SessionModelDowngraded),
		ConsumedCU:            estCU,
		FallbackUsed:          rt.SessionFallbackUsed,
		ModelDowngraded:       rt.SessionModelDowngraded,
		StartBalance:          rt.SessionStartBalance,
		StartBalanceValid:     rt.SessionStartBalanceValid,
		Adopted:               rt.SessionAdopted,
		MaxAssignments:        rt.SessionMaxAssignments,
		MeasuredSingleRate:    rt.SessionMeasuredSingleRate,
		LastSampleAt:          &sampleAt,
		LastUptimeSec:         rt.SessionUptimeSec,
		UsedStandardRAM:       rt.SessionUsedStandardRAM,
		SessionRecipe:         effAcc.DeployRecipe,
		SessionH3Variant:      effAcc.H3Variant,
		SessionWorkloadKind:   effAcc.WorkloadKind,
		SessionEnableLLM:      effAcc.EnableLLMModule,
		SessionIdleTimeoutMin: rt.EffectiveSessionIdleTimeout(acc),
		TempGuardGraceUntil:   gracePtr,
	}
}

func buildClosedSessionRecordLocked(
	acc core.AccountProfile,
	rt *core.AccountRuntimeState,
	prevEndpoint string,
	prevHardware string,
	prevGPUName string,
	prevUptime int,
	usage *colab.UsageSnapshot,
	reason core.ShutdownReason,
	now time.Time,
) core.HistoryRecord {
	hw := core.NormalizeHardwareKey(prevHardware)
	if hw == "" {
		hw = core.NormalizeHardwareKey(prevGPUName)
	}
	if hw == "" {
		hw = strings.TrimSpace(prevHardware)
		if hw == "" {
			hw = strings.TrimSpace(prevGPUName)
		}
	}
	if !rt.SessionLastSampleAt.IsZero() && now.After(rt.SessionLastSampleAt) {
		dt := now.Sub(rt.SessionLastSampleAt).Seconds()
		rate := rt.SessionMeasuredSingleRate
		if rate <= 0 {
			rate = core.EstimatedHourlyCU(hw)
		}
		rt.SessionEstimatedCU += rate * dt / 3600.0
	}

	startedAt := rt.SessionStartedAt
	if startedAt.IsZero() {
		startedAt = now.Add(-time.Duration(max(0, prevUptime)) * time.Second)
	}
	durSec := int(now.Sub(startedAt).Seconds())
	if prevUptime > durSec {
		durSec = prevUptime
	}
	if durSec < 0 {
		durSec = 0
	}

	if reason == "" {
		if durSec >= acc.MaxSessionHours()*3600-300 {
			reason = core.ShutdownCapReached
		} else {
			reason = core.ShutdownExternalOrLost
		}
	}

	effAcc := core.SessionEffectiveProfile(acc, *rt)
	billingTier := effAcc.Tier
	if !effAcc.IsFreeOnly() && billingTier == core.TierFree {
		billingTier = core.TierAIPro
	}
	basis, consumed := computeSessionBilling(
		billingTier, hw, durSec,
		rt.SessionEstimatedCU, rt.SessionMeasuredSingleRate,
		rt.SessionStartBalance, rt.SessionStartBalanceValid,
		rt.SessionAdopted, rt.SessionMaxAssignments,
		true, usage,
	)

	endedAt := now
	rec := core.HistoryRecord{
		ID:              deterministicSessionRecordID(acc.ID, prevEndpoint, startedAt),
		Kind:            core.HistoryKindSession,
		AccountID:       acc.ID,
		AccountName:     acc.DisplayName,
		AccountColor:    acc.ColorTag,
		SessionName:     acc.SessionName,
		Endpoint:        prevEndpoint,
		StartedAt:       startedAt,
		EndedAt:         &endedAt,
		DurationSec:     durSec,
		Hardware:        hw,
		HighMem:         core.EffectiveUseHighMem(effAcc) && !rt.SessionUsedStandardRAM,
		RecipeLabel:     recipeLabelForSession(effAcc, rt.SessionModelDowngraded),
		ConsumedCU:      consumed,
		BillingBasis:    basis,
		ShutdownReason:  reason,
		FallbackUsed:    rt.SessionFallbackUsed,
		ModelDowngraded: rt.SessionModelDowngraded,
	}

	rt.SessionStartedAt = time.Time{}
	rt.SessionStartBalance = 0
	rt.SessionStartBalanceValid = false
	rt.SessionAdopted = false
	rt.SessionMaxAssignments = 0
	rt.SessionMeasuredSingleRate = 0
	rt.SessionEstimatedCU = 0
	rt.SessionLastSampleAt = time.Time{}
	rt.SessionFallbackUsed = false
	rt.SessionModelDowngraded = false
	rt.SessionUsedStandardRAM = false
	return rec
}

func buildClosedRecordFromInFlight(
	entry core.HistoryRecord,
	acc core.AccountProfile,
	usage *colab.UsageSnapshot,
	now time.Time,
) core.HistoryRecord {
	var endedAt time.Time
	if entry.LastSampleAt != nil {
		endedAt = *entry.LastSampleAt
	}
	if endedAt.IsZero() || endedAt.Before(entry.StartedAt) {
		endedAt = now
	}
	startedAt := entry.StartedAt
	if startedAt.IsZero() {
		startedAt = endedAt.Add(-time.Duration(max(0, entry.LastUptimeSec)) * time.Second)
	}
	durSec := int(endedAt.Sub(startedAt).Seconds())
	if entry.LastUptimeSec > durSec {
		durSec = entry.LastUptimeSec
	}
	if durSec < 0 {
		durSec = 0
	}
	basis, consumed := computeSessionBilling(
		acc.Tier, entry.Hardware, durSec,
		entry.ConsumedCU, entry.MeasuredSingleRate,
		entry.StartBalance, entry.StartBalanceValid,
		entry.Adopted, entry.MaxAssignments,
		false, usage,
	)
	reason := core.ShutdownExternalOrLost
	if durSec >= acc.Tier.MaxSessionHours()*3600-300 {
		reason = core.ShutdownCapReached
	}
	id := entry.ID
	if id == "" {
		id = deterministicSessionRecordID(acc.ID, entry.Endpoint, startedAt)
	}
	return core.HistoryRecord{
		ID:              id,
		Kind:            core.HistoryKindSession,
		AccountID:       acc.ID,
		AccountName:     acc.DisplayName,
		AccountColor:    acc.ColorTag,
		SessionName:     entry.SessionName,
		Endpoint:        entry.Endpoint,
		StartedAt:       startedAt,
		EndedAt:         &endedAt,
		DurationSec:     durSec,
		Hardware:        entry.Hardware,
		HighMem:         entry.HighMem,
		RecipeLabel:     entry.RecipeLabel,
		ConsumedCU:      consumed,
		BillingBasis:    basis,
		ShutdownReason:  reason,
		FallbackUsed:    entry.FallbackUsed,
		ModelDowngraded: entry.ModelDowngraded,
		Detail:          "重启后由运行记录自动补记",
	}
}

func (s *Service) applyServerSnapshotLocked(
	accountID string,
	acc core.AccountProfile,
	usage *colab.UsageSnapshot,
	sessions []core.ColabSessionItem,
	inFlight *core.HistoryRecord,
	now time.Time,
	settleBusy bool,
	shutdownHint core.ShutdownReason,
) (primaryEndpoint string, isRunning bool, closedRec *core.HistoryRecord, nextInFlight *core.HistoryRecord) {
	rt := s.Runtime[accountID]
	if rt.ContextLimit == 0 {
		rt = core.NewAccountRuntimeState()
	}
	if rt.Phase.Kind == core.PhaseStopping && !settleBusy {
		return rt.ActiveEndpoint, false, nil, nil
	}

	prevEndpoint := rt.ActiveEndpoint
	prevHardware := rt.ActiveHardware
	prevGPUName := rt.GPUName
	prevBalance := rt.CUBalance
	prevBalanceValid := rt.LastUsageFetchAt != nil
	prevUptime := rt.SessionUptimeSec

	rt.LastError = ""
	if usage != nil {
		rt.CUBalance = usage.Balance
		rt.CURatePerHour = usage.RatePerHour
		rt.ActiveAssignments = usage.ActiveAssignments
	}
	rt.Sessions = sessions
	rt.LastUsageFetchAt = &now

	var primary *core.ColabSessionItem
	for i := range sessions {
		if !sessions[i].IsOrphan && sessions[i].Name == acc.SessionName {
			primary = &sessions[i]
			break
		}
	}

	if primary != nil {
		if prevEndpoint == primary.Endpoint {
			if usage != nil {
				if usage.ActiveAssignments > rt.SessionMaxAssignments {
					rt.SessionMaxAssignments = usage.ActiveAssignments
				}
				if usage.ActiveAssignments <= 1 && usage.RatePerHour > 0 {
					rt.SessionMeasuredSingleRate = usage.RatePerHour
				}
				if !rt.SessionLastSampleAt.IsZero() && now.After(rt.SessionLastSampleAt) {
					dt := now.Sub(rt.SessionLastSampleAt).Seconds()
					rate := rt.SessionMeasuredSingleRate
					if rate <= 0 {
						rate = core.EstimatedHourlyCU(primary.Hardware)
					}
					rt.SessionEstimatedCU += rate * dt / 3600.0
				}
				rt.SessionLastSampleAt = now
			}
		} else {
			if prevEndpoint != "" {
				rec := buildClosedSessionRecordLocked(acc, &rt, prevEndpoint, prevHardware, prevGPUName, prevUptime, usage, core.ShutdownExternalOrLost, now)
				closedRec = &rec
			}
			if inFlight != nil && inFlight.Endpoint == primary.Endpoint {
				// Seamlessly resume billing & uptime after app restart/crash from HistoryStore InFlight record
				rt.SessionStartedAt = inFlight.StartedAt
				if rt.SessionStartedAt.IsZero() {
					rt.SessionStartedAt = now.Add(-time.Duration(max(0, inFlight.LastUptimeSec)) * time.Second)
				}
				rt.SessionUptimeSec = max(inFlight.LastUptimeSec, int(now.Sub(rt.SessionStartedAt).Seconds()))
				rt.IdleSeconds = 0
				rt.WarnedForCurrentIdle = false
				rt.SessionStartBalance = inFlight.StartBalance
				rt.SessionStartBalanceValid = inFlight.StartBalanceValid
				rt.SessionAdopted = inFlight.Adopted
				rt.SessionMaxAssignments = inFlight.MaxAssignments
				rt.SessionMeasuredSingleRate = inFlight.MeasuredSingleRate
				rt.SessionEstimatedCU = inFlight.ConsumedCU
				rt.SessionFallbackUsed = inFlight.FallbackUsed
				rt.SessionModelDowngraded = inFlight.ModelDowngraded
				rt.SessionUsedStandardRAM = inFlight.UsedStandardRAM
				if inFlight.LastSampleAt != nil && !inFlight.LastSampleAt.IsZero() && now.After(*inFlight.LastSampleAt) {
					rate := rt.SessionMeasuredSingleRate
					if rate <= 0 {
						rate = core.EstimatedHourlyCU(primary.Hardware)
					}
					rt.SessionEstimatedCU += rate * now.Sub(*inFlight.LastSampleAt).Seconds() / 3600.0
				}
				rt.SessionLastSampleAt = now
				if usage != nil {
					if usage.ActiveAssignments > rt.SessionMaxAssignments {
						rt.SessionMaxAssignments = usage.ActiveAssignments
					}
					if usage.ActiveAssignments <= 1 && usage.RatePerHour > 0 {
						rt.SessionMeasuredSingleRate = usage.RatePerHour
					}
				}
			} else {
				if inFlight != nil && closedRec == nil {
					rec := buildClosedRecordFromInFlight(*inFlight, acc, usage, now)
					closedRec = &rec
				}
				isFreshAlloc := rt.Phase.Kind == core.PhaseStarting || rt.Phase.Kind == core.PhaseQueuing
				rt.SessionUptimeSec = 0
				rt.IdleSeconds = 0
				rt.WarnedForCurrentIdle = false
				rt.SessionStartedAt = now
				rt.SessionLastSampleAt = now
				rt.SessionEstimatedCU = 0
				rt.SessionAdopted = !isFreshAlloc
				rt.SessionFallbackUsed = false
				rt.SessionModelDowngraded = false
				rt.SessionUsedStandardRAM = false
				if isFreshAlloc && prevBalanceValid {
					rt.SessionStartBalance = prevBalance
					rt.SessionStartBalanceValid = true
				} else {
					rt.SessionStartBalance = 0
					rt.SessionStartBalanceValid = false
				}
				if usage != nil {
					rt.SessionMaxAssignments = usage.ActiveAssignments
					if usage.ActiveAssignments <= 1 && usage.RatePerHour > 0 {
						rt.SessionMeasuredSingleRate = usage.RatePerHour
					} else {
						rt.SessionMeasuredSingleRate = 0
					}
				} else {
					rt.SessionMaxAssignments = 0
					rt.SessionMeasuredSingleRate = 0
				}
			}

			s.cancelDeployFlowLocked(accountID)
			s.clearStaleEndpointPromptsLocked(accountID, primary.Endpoint)
			rt.PublicURL = ""
			rt.LLMURLBoundEndpoint = ""
			rt.DisplayUIURL = ""
			rt.ServerHealthy = false
			rt.DeployRunning = false
			rt.DeployPreLaunch = false
			rt.DeployFailed = false
			rt.DeployError = ""
			rt.DeployStartingUntil = time.Time{}
			rt.DeployNotifiedFailEndpoint = ""
			rt.DeployLogTail = ""
			rt.DeployLogLoading = false
			rt.RenderedVideoCount = 0
			rt.SessionNewVideoCount = 0
			rt.SessionOutputs = nil
			rt.DownloadingOutput = false
			rt.SessionLocked = true
			if inFlight != nil && inFlight.Endpoint == primary.Endpoint && inFlight.SessionRecipe != "" {
				rt.SessionRecipe = inFlight.SessionRecipe
				rt.SessionH3Variant = inFlight.SessionH3Variant
				rt.SessionWorkloadKind = inFlight.SessionWorkloadKind
				rt.SessionEnableLLM = inFlight.SessionEnableLLM
				rt.SessionIdleTimeoutMin = inFlight.SessionIdleTimeoutMin
				if rt.SessionIdleTimeoutMin < 5 {
					rt.SessionIdleTimeoutMin = core.EffectiveIdleTimeoutMinutes(acc)
				}
				rt.SessionIsVideoComfy = !rt.SessionEnableLLM && rt.SessionRecipe.IsVideoComfy()
				if inFlight.TempGuardGraceUntil != nil && !inFlight.TempGuardGraceUntil.IsZero() {
					rt.WatchdogDeferredForTempOutputs = true
					rt.TempGuardGraceUntil = *inFlight.TempGuardGraceUntil
				} else {
					rt.WatchdogDeferredForTempOutputs = false
					rt.TempGuardGraceUntil = time.Time{}
				}
			} else {
				rt.SessionIsVideoComfy = !acc.EnableLLMModule && acc.DeployRecipe.IsVideoComfy()
				rt.SessionRecipe = acc.DeployRecipe
				rt.SessionH3Variant = acc.H3Variant
				rt.SessionWorkloadKind = acc.WorkloadKind
				rt.SessionEnableLLM = acc.EnableLLMModule
				rt.SessionIdleTimeoutMin = core.EffectiveIdleTimeoutMinutes(acc)
				rt.WatchdogDeferredForTempOutputs = false
				rt.TempGuardGraceUntil = time.Time{}
			}
			rt.AutoOpenedEndpoint = ""
			rt.DriveMountedKnown = false
			rt.DriveActuallyMounted = false
			rt.EffectiveOutputDir = ""
			rt.TempOutputVideos = 0
			rt.TempOutputScanKnown = false
		}
		if settleBusy || !rt.Phase.IsBusy() {
			rt.Phase = core.InstancePhase{Kind: core.PhaseRunning}
		}
		rt.LastUnexpectedStopMsg = ""
		rt.QueueStartedAt = time.Time{}
		rt.ActiveEndpoint = primary.Endpoint
		rt.ActiveHardware = primary.Hardware
		s.Runtime[accountID] = rt
		nextInFlight = buildInFlightRecordLocked(acc, rt, now)
		return primary.Endpoint, true, closedRec, nextInFlight
	}

	if prevEndpoint != "" {
		rec := buildClosedSessionRecordLocked(acc, &rt, prevEndpoint, prevHardware, prevGPUName, prevUptime, usage, shutdownHint, now)
		closedRec = &rec
		if shutdownHint == "" {
			hwLbl := strings.TrimSpace(rec.Hardware)
			if hwLbl == "" {
				hwLbl = "云端显卡"
			}
			if rec.ShutdownReason == core.ShutdownCapReached {
				rt.LastUnexpectedStopMsg = fmt.Sprintf("刚才的 %s 在 %s 达到单次运行时长上限自动停机，可在「记录」查看账单", hwLbl, now.Format("15:04"))
			} else {
				rt.LastUnexpectedStopMsg = fmt.Sprintf("刚才的 %s 在 %s 被 Google 服务端回收停机（非本软件关机），点右侧可立即重开", hwLbl, now.Format("15:04"))
			}
		} else {
			rt.LastUnexpectedStopMsg = ""
		}
	} else if inFlight != nil {
		rec := buildClosedRecordFromInFlight(*inFlight, acc, usage, now)
		closedRec = &rec
	}

	if settleBusy || (!rt.Phase.IsBusy() && !rt.Phase.IsQueuing()) {
		rt.Phase = core.InstancePhase{Kind: core.PhaseOffline}
	}
	rt.ActiveEndpoint = ""
	rt.ActiveHardware = ""
	rt.GPUName = ""
	rt.VRAMUsedMB = 0
	rt.VRAMTotalMB = 0
	rt.RAMUsedMB = 0
	rt.RAMTotalMB = 0
	rt.GPUUtilPercent = 0
	rt.SessionUptimeSec = 0
	s.cancelDeployFlowLocked(accountID)
	s.clearStaleEndpointPromptsLocked(accountID, "")
	rt.PublicURL = ""
	rt.LLMURLBoundEndpoint = ""
	rt.DisplayUIURL = ""
	rt.ServerHealthy = false
	rt.DeployRunning = false
	rt.DeployPreLaunch = false
	rt.DeployFailed = false
	rt.DeployError = ""
	rt.DeployStartingUntil = time.Time{}
	rt.DeployNotifiedFailEndpoint = ""
	rt.DeployLogTail = ""
	rt.DeployLogLoading = false
	rt.RenderedVideoCount = 0
	rt.SessionNewVideoCount = 0
	rt.SessionOutputs = nil
	rt.DownloadingOutput = false
	rt.SessionIsVideoComfy = false
	rt.SessionLocked = false
	rt.SessionRecipe = ""
	rt.SessionH3Variant = ""
	rt.SessionWorkloadKind = ""
	rt.SessionEnableLLM = false
	rt.SessionIdleTimeoutMin = 0
	rt.AutoOpenedEndpoint = ""
	rt.DriveMountedKnown = false
	rt.DriveActuallyMounted = false
	rt.EffectiveOutputDir = ""
	rt.TempOutputVideos = 0
	rt.TempOutputScanKnown = false
	rt.WatchdogDeferredForTempOutputs = false
	rt.TempGuardGraceUntil = time.Time{}
	rt.QueueRunning = 0
	rt.QueuePending = 0
	rt.IdleSeconds = 0
	rt.WarnedForCurrentIdle = false
	rt.ActivitySignal = core.ActivitySignal{Kind: core.SignalUnknown}
	rt.SignalSource = core.SignalSourceNone
	s.Runtime[accountID] = rt
	return "", false, closedRec, nil
}

func (s *Service) clearStaleEndpointPromptsLocked(accountID, currentEndpoint string) {
	if s.actionPrompt == nil || s.actionPrompt.AccountID != accountID {
		return
	}
	switch s.actionPrompt.Kind {
	case core.PromptConfirmStop, core.PromptTempOutputGuard, core.PromptSwitchModeWhileRunning:
		if currentEndpoint == "" || (s.actionPrompt.ExpectedEndpoint != "" && s.actionPrompt.ExpectedEndpoint != currentEndpoint) {
			s.actionPrompt = nil
		}
	}
}

func (s *Service) fetchAndApplyTelemetry(acc core.AccountProfile, expectedEndpoint string) {
	liveRT := s.StateFor(acc.ID)
	probeProfile := core.SessionEffectiveProfile(acc, liveRT)
	tel := s.Engine.FetchRemoteTelemetry(context.Background(), probeProfile)
	if tel == nil {
		return
	}
	now := time.Now()
	s.mu.Lock()
	rt := s.Runtime[acc.ID]
	if rt.ActiveEndpoint != expectedEndpoint {
		s.mu.Unlock()
		return
	}
	if tel.GPUName != "" {
		rt.GPUName = tel.GPUName
	}
	rt.VRAMUsedMB = tel.VRAMUsedMB
	rt.VRAMTotalMB = tel.VRAMTotalMB
	if tel.RAMTotalMB > 0 {
		rt.RAMUsedMB = tel.RAMUsedMB
		rt.RAMTotalMB = tel.RAMTotalMB
	}
	if tel.GPUUtil != nil {
		rt.GPUUtilPercent = *tel.GPUUtil
		rt.AppendTelemetrySample(*tel.GPUUtil, rt.VRAMRatio())
	}
	if tel.CPUUtil != nil {
		rt.CPUUtilPercent = *tel.CPUUtil
	}
	rt.CUDAProcessCount = tel.CUDAProcessCount
	if tel.UptimeSeconds > 0 {
		rt.SessionUptimeSec = tel.UptimeSeconds
	}
	if tel.PublicURL != "" {
		rt.PublicURL = tel.PublicURL
		rt.LLMURLBoundEndpoint = expectedEndpoint
	}
	if tel.DisplayUIURL != "" && !strings.Contains(tel.DisplayUIURL, "trycloudflare.com") {
		rt.DisplayUIURL = tel.DisplayUIURL
	}
	rt.ServerHealthy = tel.Healthy
	if tel.ModelFile != "" {
		rt.ModelFile = tel.ModelFile
	}
	if tel.ModelAlias != "" {
		rt.ModelAlias = tel.ModelAlias
	}
	if tel.ContextLimit > 0 {
		rt.ContextLimit = tel.ContextLimit
	}
	if tel.KVQuant != "" {
		rt.KVQuant = tel.KVQuant
	}
	rt.DetectedServicePort = tel.DetectedWebPort
	inStartingGrace := !rt.DeployStartingUntil.IsZero() && now.Before(rt.DeployStartingUntil)
	deployFailed := core.IsDeployFailedStatus(tel.StatusPayload) && !tel.DeployAlive && !tel.Healthy

	if rt.DeployPreLaunch {
		rt.DeployRunning = true
	} else if tel.DeployAlive {
		rt.DeployRunning = true
		rt.DeployFailed = false
		rt.DeployError = ""
		rt.DeployStartingUntil = time.Time{}
	} else if deployFailed {
		rt.DeployRunning = false
		rt.DeployFailed = true
		rt.DeployStartingUntil = time.Time{}
	} else if tel.Healthy {
		rt.DeployRunning = false
		rt.DeployFailed = false
		rt.DeployError = ""
		rt.DeployStartingUntil = time.Time{}
	} else if inStartingGrace {
		rt.DeployRunning = true
	} else {
		rt.DeployRunning = false
	}
	rt.RenderedVideoCount = tel.RenderedVideoCount
	if tel.SessionNewVideos != nil {
		rt.SessionNewVideoCount = *tel.SessionNewVideos
		rt.SessionOutputs = tel.SessionOutputs
	}
	if tel.TempOutputVideos != nil {
		rt.TempOutputScanKnown = true
		rt.TempOutputVideos = *tel.TempOutputVideos
		if *tel.TempOutputVideos > 0 {
			rt.SessionIsVideoComfy = true
		} else if *tel.TempOutputVideos == 0 {
			rt.WatchdogDeferredForTempOutputs = false
			rt.TempGuardGraceUntil = time.Time{}
		}
	}
	if tel.DriveMounted != nil {
		rt.DriveMountedKnown = true
		rt.DriveActuallyMounted = *tel.DriveMounted
	}
	if tel.EffectiveOutputDir != "" {
		rt.EffectiveOutputDir = tel.EffectiveOutputDir
	}

	statusFresh := tel.StatusPayload != nil && tel.StatusAgeSeconds != nil && *tel.StatusAgeSeconds <= core.StatusFileMaxAgeSec
	shouldNotifyDeployFail := false
	deployFailDetail := ""
	if !rt.DeployPreLaunch && deployFailed {
		detail := strings.TrimSpace(tel.StatusPayload.Detail)
		if detail == "" {
			detail = "云端部署中断，请点「查看日志」排查原因"
		}
		rt.WorkloadName = tel.StatusPayload.Name
		rt.WorkloadMetricLabel = tel.StatusPayload.MetricLabel
		rt.WorkloadMetricValue = tel.StatusPayload.MetricValue
		rt.WorkloadMetricUnit = tel.StatusPayload.MetricUnit
		zero := 0.0
		rt.WorkloadMetricRatio = &zero
		rt.WorkloadDetail = detail
		rt.DeployError = detail
		if rt.DeployNotifiedFailEndpoint != expectedEndpoint {
			rt.DeployNotifiedFailEndpoint = expectedEndpoint
			shouldNotifyDeployFail = true
			deployFailDetail = detail
		}
	} else if !rt.DeployPreLaunch && statusFresh {
		if k, ok := tel.StatusPayload.ParsedKind(); ok {
			rt.DetectedWorkloadKind = &k
		}
		rt.WorkloadName = tel.StatusPayload.Name
		rt.WorkloadMetricLabel = tel.StatusPayload.MetricLabel
		rt.WorkloadMetricValue = tel.StatusPayload.MetricValue
		rt.WorkloadMetricUnit = tel.StatusPayload.MetricUnit
		rt.WorkloadMetricRatio = tel.StatusPayload.MetricRatio
		rt.WorkloadDetail = tel.StatusPayload.Detail
		if tel.StatusPayload.UIURL == "" {
			rt.DisplayUIURL = ""
		}
	}
	if tel.ComfyQueue != nil {
		rt.QueueRunning = tel.ComfyQueue.Running
		rt.QueuePending = tel.ComfyQueue.Pending
		rt.SessionIsVideoComfy = true
	}

	sessionRecipe, sessionVariant, _, sessionEnableLLM := rt.EffectiveSessionState(acc)
	if !sessionEnableLLM {
		handled := false
		if rt.DeployRunning {
			handled = true
			rt.SignalSource = core.SignalSourceStatusFile
			rt.ActivitySignal = core.ActivitySignal{Kind: core.SignalBusy, ObservedAt: now}
			rt.IdleSeconds = 0
			rt.WarnedForCurrentIdle = false
			rt.WatchdogDeferredForTempOutputs = false
			rt.TempGuardGraceUntil = time.Time{}
		}
		if !handled && statusFresh {
			if busy, ok := tel.StatusPayload.IsWorkloadBusy(); ok {
				handled = true
				rt.SignalSource = core.SignalSourceStatusFile
				if busy {
					rt.ActivitySignal = core.ActivitySignal{Kind: core.SignalBusy, ObservedAt: now}
					rt.IdleSeconds = 0
					rt.WarnedForCurrentIdle = false
					rt.WatchdogDeferredForTempOutputs = false
					rt.TempGuardGraceUntil = time.Time{}
				} else {
					rt.ActivitySignal = core.ActivitySignal{Kind: core.SignalIdle, ObservedAt: now}
				}
			}
		}
		if !handled && tel.ComfyQueue != nil {
			handled = true
			rt.SignalSource = core.SignalSourceComfyQueue
			if tel.ComfyQueue.IsBusy() {
				rt.ActivitySignal = core.ActivitySignal{Kind: core.SignalBusy, ObservedAt: now}
				rt.IdleSeconds = 0
				rt.WarnedForCurrentIdle = false
				rt.WatchdogDeferredForTempOutputs = false
				rt.TempGuardGraceUntil = time.Time{}
			} else {
				rt.ActivitySignal = core.ActivitySignal{Kind: core.SignalIdle, ObservedAt: now}
			}
		}
		if !handled {
			sig := core.EvaluateHardwareActivity(tel.GPUUtil, tel.CPUUtil, rt.IsTPUHardware(), now)
			rt.ActivitySignal = sig
			if sig.Kind != core.SignalUnknown {
				rt.SignalSource = core.SignalSourceHardwareDualLowLoad
				if sig.IsBusy() {
					rt.IdleSeconds = 0
					rt.WarnedForCurrentIdle = false
					rt.WatchdogDeferredForTempOutputs = false
					rt.TempGuardGraceUntil = time.Time{}
				}
			}
		}
	}

	shouldAutoOpenComfy := sessionRecipe.IsVideoComfy() &&
		!sessionEnableLLM &&
		tel.ComfyQueue != nil &&
		!rt.DeployRunning &&
		!rt.DeployPreLaunch &&
		rt.AutoOpenedEndpoint != expectedEndpoint
	shouldSyncLLMReady := sessionEnableLLM &&
		tel.Healthy &&
		rt.PublicURL != "" &&
		!rt.DeployRunning &&
		!rt.DeployPreLaunch &&
		rt.AutoOpenedEndpoint != expectedEndpoint
	if shouldAutoOpenComfy || shouldSyncLLMReady {
		rt.AutoOpenedEndpoint = expectedEndpoint
	}

	s.Runtime[acc.ID] = rt
	s.mu.Unlock()
	s.notifyUI()

	if shouldNotifyDeployFail {
		s.cancelDeployWatcher(acc.ID)
		s.SendNotification("云端部署中断", fmt.Sprintf("%s 部署未完成：%s", acc.DisplayName, deployFailDetail), true)
	}
	if shouldAutoOpenComfy {
		s.cancelDeployWatcher(acc.ID)
		go func() {
			variantLabel := sessionVariant.DisplayName()
			if sessionRecipe == core.RecipeComfyUIGeneral {
				variantLabel = sessionRecipe.DisplayName()
			}
			s.SendNotification("ComfyUI 已装好，正在打开", fmt.Sprintf("%s 的 %s 已启动，正在通过本机加密通道打开画布", acc.DisplayName, variantLabel), true)
			s.OpenComfyUI(acc.ID)
		}()
	}
	if shouldSyncLLMReady {
		s.cancelDeployWatcher(acc.ID)
		go func() {
			if key := s.Engine.FetchRemoteAPIKeySafely(context.Background(), acc); key != "" {
				_ = s.Vault.SaveLLMAPIKey(acc.ID, key)
			}
			s.SyncAllEnabledClients(acc.ID, true)
			s.SendNotification("大模型代理已就绪", fmt.Sprintf("%s 的 %s 已上线，可直接同步工具或拉起终端", acc.DisplayName, rt.ModelAlias), true)
		}()
	}
}

func (s *Service) pollRunningLLMSlots() {
	accounts := s.AccountsSnapshot()
	for _, acc := range accounts {
		rt := s.StateFor(acc.ID)
		if !rt.EffectiveSessionEnableLLM(acc) {
			continue
		}
		if !rt.Phase.IsRunning() || rt.PublicURL == "" || rt.LLMURLBoundEndpoint != rt.ActiveEndpoint {
			continue
		}
		apiKey, _ := s.Vault.LoadLLMAPIKey(acc.ID)
		slots, ok := colab.FetchSlots(context.Background(), rt.PublicURL, apiKey)
		if !ok {
			s.mu.Lock()
			cur := s.Runtime[acc.ID]
			if cur.ActiveEndpoint == rt.ActiveEndpoint {
				cur.ServerHealthy = false
				cur.ActivitySignal = core.ActivitySignal{Kind: core.SignalUnknown}
				s.Runtime[acc.ID] = cur
			}
			s.mu.Unlock()
			s.notifyUI()
			continue
		}
		now := time.Now()
		eval := core.EvaluateLLMSlots(slots, now)

		s.mu.Lock()
		cur := s.Runtime[acc.ID]
		if cur.ActiveEndpoint == rt.ActiveEndpoint {
			cur.ServerHealthy = true
			cur.PromptTokens = eval.MaxPromptTokens
			if eval.MaxContext > 0 {
				cur.ContextLimit = eval.MaxContext
			}
			taskProgressed := eval.LatestTaskID >= 0 && cur.LastTaskID >= 0 && eval.LatestTaskID != cur.LastTaskID
			if eval.LatestTaskID >= 0 {
				cur.LastTaskID = eval.LatestTaskID
			}
			cur.ActivitySignal = eval.Signal
			cur.SignalSource = core.SignalSourceLLMSlots
			if eval.Signal.IsBusy() || taskProgressed {
				cur.IdleSeconds = 0
				cur.WarnedForCurrentIdle = false
			}
			s.Runtime[acc.ID] = cur
		}
		s.mu.Unlock()
		s.notifyUI()
	}
}

// Verified Stop & Batch Stop

func (s *Service) StopAccountSession(accountID, specificSession, reason string) {
	_ = s.stopAccountSessionVerified(accountID, specificSession, reason)
}

func (s *Service) stopAccountSessionVerified(accountID, specificSession, reason string) bool {
	s.mu.Lock()
	var acc core.AccountProfile
	found := false
	for _, a := range s.Settings.Accounts {
		if a.ID == accountID {
			acc = a
			found = true
			break
		}
	}
	if !found {
		s.mu.Unlock()
		return true
	}
	targetSession := specificSession
	if targetSession == "" {
		targetSession = acc.SessionName
	}
	stopKey := accountID + "|" + targetSession
	if s.stoppingSessions[stopKey] {
		s.mu.Unlock()
		s.ShowToast("正在停止该会话并核验中，请稍候…")
		return false
	}
	s.stoppingSessions[stopKey] = true
	prevRT := s.Runtime[accountID]
	prevPhase := prevRT.Phase
	targetEndpoint := ""
	if targetSession == acc.SessionName {
		targetEndpoint = strings.TrimSpace(prevRT.ActiveEndpoint)
	}
	if targetEndpoint == "" {
		for _, sess := range prevRT.Sessions {
			if !sess.IsOrphan && sess.Name == targetSession {
				targetEndpoint = strings.TrimSpace(sess.Endpoint)
				break
			}
		}
	}
	shouldFlushVideo := prevPhase.IsRunning() && acc.UseGoogleDrive &&
		(prevRT.SessionIsVideoComfy || (!acc.EnableLLMModule && acc.DeployRecipe.IsVideoComfy()))
	if targetSession == acc.SessionName {
		rt := prevRT
		rt.Phase = core.InstancePhase{Kind: core.PhaseStopping, Detail: "停止计费实例…"}
		s.Runtime[accountID] = rt
	}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.stoppingSessions, stopKey)
		s.mu.Unlock()
	}()
	s.notifyUI()

	flushFailed := false
	if targetSession == acc.SessionName {
		s.cancelDeployFlow(accountID)
		s.cancelDeployWatcher(accountID)
		if shouldFlushVideo {
			flushCtx, cancelFlush := context.WithTimeout(context.Background(), 25*time.Second)
			if err := s.Engine.FlushWorkspaceToDrive(flushCtx, acc); err != nil {
				flushFailed = true
			}
			cancelFlush()
		}
		s.StopSSHTunnel(accountID)
	}
	stopRes := s.Engine.StopSession(context.Background(), acc, targetSession)

	// Independent authoritative server-side verification
	usage, sessions, verifyErr := s.Engine.FetchUsageAndSessions(context.Background(), acc)
	if verifyErr != nil {
		s.mu.Lock()
		rt := s.Runtime[accountID]
		if targetSession == acc.SessionName && rt.Phase.Kind == core.PhaseStopping {
			if prevPhase.IsRunning() {
				rt.Phase = prevPhase
			} else {
				rt.Phase = core.InstancePhase{Kind: core.PhaseError, Detail: "停机后核验失败"}
			}
		}
		rt.LastError = verifyErr.Error()
		s.Runtime[accountID] = rt
		s.mu.Unlock()
		s.notifyUI()
		extra := ""
		if stopRes.ExitCode != 0 {
			extra = fmt.Sprintf("（停机命令返回码 %d）", stopRes.ExitCode)
		}
		msg := fmt.Sprintf("警告：%s 会话 %s 停机后未能从服务端核验%s，请前往 Colab 网页确认！", acc.DisplayName, targetSession, extra)
		s.ShowToast(msg)
		s.SendNotification("停机未能核验", msg, true)
		return false
	}

	shutdownHint := core.ShutdownReason("")
	if targetSession == acc.SessionName {
		shutdownHint = mapShutdownReason(reason)
	}
	var inFlight *core.HistoryRecord
	if s.History != nil {
		if rec, ok := s.History.InFlightByAccount()[accountID]; ok {
			cp := rec
			inFlight = &cp
		}
	}
	s.mu.Lock()
	ep, isRunning, closedRec, nextInFlight := s.applyServerSnapshotLocked(accountID, acc, usage, sessions, inFlight, time.Now(), true, shutdownHint)
	s.mu.Unlock()
	if closedRec != nil {
		s.recordHistory(*closedRec)
		if closedRec.ShutdownReason == core.ShutdownExternalOrLost || closedRec.ShutdownReason == core.ShutdownCapReached {
			hwLbl := strings.TrimSpace(closedRec.Hardware)
			if hwLbl == "" {
				hwLbl = "云端机器"
			}
			s.SendNotification("云端机器已被 Google 回收停机", fmt.Sprintf("%s 的 %s 已停止运行（非本软件关机），可在「记录」查看详情", acc.DisplayName, hwLbl), true)
		}
	}
	if nextInFlight != nil {
		s.upsertInFlight(*nextInFlight)
	}
	s.notifyUI()
	if isRunning {
		go s.fetchAndApplyTelemetry(acc, ep)
	}

	stillAlive := false
	stillOrphan := false
	for _, sess := range sessions {
		if !sess.IsOrphan && sess.Name == targetSession {
			stillAlive = true
			break
		}
		if targetEndpoint != "" && strings.TrimSpace(sess.Endpoint) == targetEndpoint {
			stillAlive = true
			stillOrphan = sess.IsOrphan
			break
		}
	}
	flushWarnSuffix := ""
	if flushFailed {
		flushWarnSuffix = "（注意：关机前工作流最终同步未完成，请核对 Drive）"
		s.SendNotification("工作流最终同步未完成", fmt.Sprintf("%s 已继续执行关机止损，但关机前最后一次工作流同步到 Drive 失败，请核对最新工作流是否已保存。", acc.DisplayName), true)
	}
	if stillAlive {
		msg := fmt.Sprintf("警告：%s 会话 %s 仍出现在服务端，请前往 Colab 网页确认停机！", acc.DisplayName, targetSession)
		if stillOrphan {
			msg = fmt.Sprintf("警告：%s 的原云端机器仍在计费运行（已被 Colab 转为网页托管），请立即前往 Colab 网页手动断开！", acc.DisplayName)
		}
		s.ShowToast(msg + flushWarnSuffix)
		s.SendNotification("停机核验异常", msg, true)
		return false
	} else if reason != "" {
		s.ShowToast(fmt.Sprintf("%s：%s 已确认关机止损%s", reason, targetSession, flushWarnSuffix))
	} else {
		s.ShowToast(fmt.Sprintf("%s 已确认关机止损%s", targetSession, flushWarnSuffix))
	}
	return true
}

func (s *Service) StopAccountManagedSessions(accountID string) bool {
	acc, ok := s.accountByID(accountID)
	if !ok {
		return true
	}
	s.mu.RLock()
	sessions := managedSessionsForAccountLocked(acc, s.Runtime[accountID])
	s.mu.RUnlock()

	if len(sessions) == 0 {
		if acc.AuthMode != core.AuthIsolatedVaultADC && strings.TrimSpace(acc.Email) != "" {
			if who, ok := s.Engine.Whoami(context.Background(), acc); ok && !strings.EqualFold(strings.TrimSpace(who.Email), strings.TrimSpace(acc.Email)) {
				s.ShowToast(fmt.Sprintf("本机默认 colab CLI 当前已切换为 %s（非 %s），已中止自动停机防止误停其他账号", who.Email, acc.Email))
				return false
			}
		}
		sessions = append(sessions, acc.SessionName)
	}
	allStopped := true
	for _, name := range sessions {
		if !s.stopAccountSessionVerified(accountID, name, "停止旧会话") {
			allStopped = false
		}
	}
	return allStopped
}

func (s *Service) StopAllManagedSessions() bool {
	type target struct {
		accID   string
		session string
	}
	s.mu.RLock()
	var targets []target
	for _, acc := range s.Settings.Accounts {
		for _, name := range managedSessionsForAccountLocked(acc, s.Runtime[acc.ID]) {
			targets = append(targets, target{acc.ID, name})
		}
	}
	s.mu.RUnlock()

	if len(targets) == 0 {
		if orphans := s.TotalOrphanSessionCount(); orphans > 0 {
			s.ShowToast(fmt.Sprintf("还有 %d 台在 Colab 网页开的机器无法直接关闭，请去 Colab 网页手动断开", orphans))
		}
		return true
	}
	allVerified := true
	for _, t := range targets {
		if !s.stopAccountSessionVerified(t.accID, t.session, "全部关机止损") {
			allVerified = false
		}
	}
	stillManaged := s.TotalManagedRunningSessionCount()
	orphans := s.TotalOrphanSessionCount()
	if stillManaged > 0 || orphans > 0 {
		var parts []string
		if stillManaged > 0 {
			parts = append(parts, fmt.Sprintf("%d 台机器未能确认关机", stillManaged))
		}
		if orphans > 0 {
			parts = append(parts, fmt.Sprintf("%d 台 Colab 网页开的机器需去网页关闭", orphans))
		}
		s.ShowToast("关机提示：" + strings.Join(parts, "，"))
	} else {
		s.ShowToast(fmt.Sprintf("已关闭全部 %d 台云端机器", len(targets)))
	}
	return allVerified && stillManaged == 0
}

// Idle Watchdog & 1s Tick

func (s *Service) tempOutputGrace() time.Duration {
	if s.tempOutputGraceDuration > 0 {
		return s.tempOutputGraceDuration
	}
	return 15 * time.Minute
}

func (s *Service) WatchdogStatusFor(acc core.AccountProfile) core.WatchdogAction {
	rt := s.StateFor(acc.ID)
	return core.EvaluateWatchdog(core.WatchdogInput{
		IsRunning:             rt.Phase.IsRunning(),
		EnableLLMModule:       rt.EffectiveSessionEnableLLM(acc),
		ActivitySignal:        rt.ActivitySignal,
		AutoShutdownEnabled:   acc.AutoShutdownEnabled,
		IdleSeconds:           rt.IdleSeconds,
		TimeoutMinutes:        rt.EffectiveSessionIdleTimeout(acc),
		WarnBeforeMinutes:     acc.WarnBeforeMinutes,
		AlreadyWarned:         rt.WarnedForCurrentIdle,
		Now:                   time.Now(),
		AllowHardwareFallback: true,
	})
}

func (s *Service) ResetIdleTimer(accountID string) {
	s.mu.Lock()
	if rt, ok := s.Runtime[accountID]; ok {
		rt.IdleSeconds = 0
		rt.LastActiveAt = time.Now()
		rt.WarnedForCurrentIdle = false
		rt.WatchdogDeferredForTempOutputs = false
		rt.TempGuardGraceUntil = time.Time{}
		s.Runtime[accountID] = rt
	}
	s.mu.Unlock()
	s.ShowToast("已续命，空闲计时器清零")
}

func (s *Service) handleOneSecondTick(now time.Time) {
	var toShutdown []string
	var toGraceExpiredShutdown []struct {
		id       string
		name     string
		endpoint string
	}
	var toTempGuard []struct {
		id              string
		name            string
		endpoint        string
		videos          int
		rate            float64
		driveDisabled   bool
		graceMinutes    int
		hasDownloadable bool
	}
	var toWarn []struct {
		name string
		rem  int
	}
	needsRepaint := false
	grace := s.tempOutputGrace()
	graceMins := max(1, int((grace+time.Second*59)/time.Minute))

	s.mu.Lock()
	for i := range s.Settings.Accounts {
		acc := &s.Settings.Accounts[i]
		rt := s.Runtime[acc.ID]

		if rt.Phase.IsQueuing() || rt.Phase.IsRunning() || rt.Phase.Kind == core.PhaseStarting {
			needsRepaint = true
		}

		if rt.Phase.IsRunning() {
			rt.SessionUptimeSec++
			sessionEnableLLM := rt.EffectiveSessionEnableLLM(*acc)
			maxAge := core.DefaultSignalMaxAge
			if !sessionEnableLLM {
				maxAge = core.HardwareSignalMaxAge
			}
			freshIdle := rt.ActivitySignal.Kind == core.SignalIdle &&
				!rt.ActivitySignal.ObservedAt.IsZero() &&
				now.Sub(rt.ActivitySignal.ObservedAt) <= maxAge
			if acc.AutoShutdownEnabled && freshIdle {
				rt.IdleSeconds++
			}
			effectiveTimeout := rt.EffectiveSessionIdleTimeout(*acc)
			act := core.EvaluateWatchdog(core.WatchdogInput{
				IsRunning:             true,
				EnableLLMModule:       sessionEnableLLM,
				ActivitySignal:        rt.ActivitySignal,
				AutoShutdownEnabled:   acc.AutoShutdownEnabled,
				IdleSeconds:           rt.IdleSeconds,
				TimeoutMinutes:        effectiveTimeout,
				WarnBeforeMinutes:     acc.WarnBeforeMinutes,
				AlreadyWarned:         rt.WarnedForCurrentIdle,
				Now:                   now,
				AllowHardwareFallback: true,
			})
			switch act.Kind {
			case core.WatchdogWarn:
				rt.WarnedForCurrentIdle = true
				toWarn = append(toWarn, struct {
					name string
					rem  int
				}{acc.DisplayName, act.RemainingMinutes})
			case core.WatchdogShutdown:
				isVideoSession := rt.SessionIsVideoComfy || !sessionEnableLLM
				var tempOutputsAtRisk bool
				if rt.TempOutputScanKnown {
					// Authoritative three-state decision: >0 (files exist) or <0 (scan unknown/truncated) protect; ==0 (verified empty) allows stop.
					tempOutputsAtRisk = rt.TempOutputVideos != 0
				} else {
					tempOutputsAtRisk = isVideoSession && rt.IsTempOutputActive(*acc) && (rt.SessionNewVideoCount > 0 || rt.RenderedVideoCount > 0)
				}
				if tempOutputsAtRisk {
					if !rt.WatchdogDeferredForTempOutputs {
						rt.WatchdogDeferredForTempOutputs = true
						rt.TempGuardGraceUntil = now.Add(grace)
						vidCount := rt.TempOutputVideos
						if vidCount <= 0 && rt.IsTempOutputActive(*acc) && rt.SessionNewVideoCount > 0 {
							vidCount = rt.SessionNewVideoCount
						}
						if vidCount <= 0 && rt.TempOutputVideos >= 0 {
							vidCount = rt.RenderedVideoCount
						}
						hasDL := len(rt.SessionOutputs) > 0 || rt.TempOutputVideos != 0
						toTempGuard = append(toTempGuard, struct {
							id              string
							name            string
							endpoint        string
							videos          int
							rate            float64
							driveDisabled   bool
							graceMinutes    int
							hasDownloadable bool
						}{acc.ID, acc.DisplayName, rt.ActiveEndpoint, vidCount, rt.CURatePerHour, !acc.UseGoogleDrive, graceMins, hasDL})
					} else if !rt.TempGuardGraceUntil.IsZero() && !now.Before(rt.TempGuardGraceUntil) {
						if rt.DownloadingOutput {
							// Keep the machine alive while user is actively downloading the temporary video.
							rt.TempGuardGraceUntil = now.Add(60 * time.Second)
						} else {
							// Grace period exhausted: enforce shutdown to protect user's compute units.
							rt.TempGuardGraceUntil = now.Add(60 * time.Second)
							toGraceExpiredShutdown = append(toGraceExpiredShutdown, struct {
								id       string
								name     string
								endpoint string
							}{acc.ID, acc.DisplayName, rt.ActiveEndpoint})
						}
					}
				} else {
					toShutdown = append(toShutdown, acc.ID)
				}
			}
			s.Runtime[acc.ID] = rt
		}
	}
	s.mu.Unlock()

	for _, w := range toWarn {
		s.SendNotification("即将空闲关机", fmt.Sprintf("%s 将在约 %d 分钟后自动关机止损，可在主控台点击「续命」", w.name, w.rem), true)
	}
	for _, tg := range toTempGuard {
		cause := "未挂载 Google Drive"
		if tg.driveDisabled {
			cause = "当前已关闭自动存网盘"
		}
		vidDesc := fmt.Sprintf("%d 条视频", tg.videos)
		if tg.videos <= 0 {
			vidDesc = "未确认数量的临时视频"
		}
		msg := fmt.Sprintf(
			"检测到 %s 的云电脑临时盘（%s）还有 %s，关机后会永久清空。\n当前机器仍在计费（约 %.2f CU/h），已为你宽限 %d 分钟。请尽快下回本机，超时无人处理将自动关机止损。",
			tg.name, cause, vidDesc, tg.rate, tg.graceMinutes,
		)
		s.SendNotification("暂缓自动关机：临时盘有未保存视频", msg, true)
		primaryLbl := "一键下回本机（重置计时）"
		if !tg.hasDownloadable {
			primaryLbl = "打开画布保存（重置计时）"
		}
		s.PresentActionPrompt(&core.UserActionPrompt{
			ID:                  uuid.New().String(),
			Kind:                core.PromptTempOutputGuard,
			AccountID:           tg.id,
			ExpectedEndpoint:    tg.endpoint,
			Title:               fmt.Sprintf("已宽限 %d 分钟：临时盘有未保存视频", tg.graceMinutes),
			Message:             msg,
			PrimaryLabel:        primaryLbl,
			SecondaryLabel:      "放弃视频，立刻关机",
			CancelLabel:         fmt.Sprintf("稍后处理（%d 分钟后关机）", tg.graceMinutes),
			HasDownloadableTemp: tg.hasDownloadable,
		})
	}
	for _, ge := range toGraceExpiredShutdown {
		item := ge
		go func() {
			rtNow := s.StateFor(item.id)
			if !rtNow.Phase.IsRunning() || (item.endpoint != "" && rtNow.ActiveEndpoint != "" && rtNow.ActiveEndpoint != item.endpoint) {
				return
			}
			if rtNow.DownloadingOutput {
				return
			}
			if s.stopAccountSessionVerified(item.id, "", "临时盘宽限到期自动关机") {
				s.SendNotification("宽限期已到，已自动关机止损", fmt.Sprintf("%s 的 %d 分钟临时盘宽限期已结束，已确认停机停止扣点", item.name, graceMins), true)
			} else {
				s.SendNotification("自动关机未能确认，请立即核查", fmt.Sprintf("%s 的临时盘宽限期已到，但未能确认云端停机，请打开软件检查", item.name), true)
			}
		}()
	}
	for _, id := range toShutdown {
		go s.StopAccountSession(id, "", "空闲自动关机")
	}
	if needsRepaint {
		s.notifyUI()
	}
}

// Action Prompts & Safety Confirmations

func (s *Service) CurrentActionPrompt() *core.UserActionPrompt {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.actionPrompt == nil {
		return nil
	}
	cp := *s.actionPrompt
	return &cp
}

func (s *Service) PresentActionPrompt(prompt *core.UserActionPrompt) {
	s.mu.Lock()
	var oldDriveCh chan bool
	if s.actionPrompt != nil && s.actionPrompt.Kind == core.PromptDriveAuth && s.driveAuthResultCh != nil {
		if prompt == nil || prompt.Kind != core.PromptDriveAuth || prompt.ID != s.actionPrompt.ID {
			oldDriveCh = s.driveAuthResultCh
			s.driveAuthResultCh = nil
		}
	}
	if prompt == nil {
		s.actionPrompt = nil
	} else {
		cp := *prompt
		s.actionPrompt = &cp
	}
	s.mu.Unlock()
	if oldDriveCh != nil {
		select {
		case oldDriveCh <- false:
		default:
		}
	}
	s.notifyUI()
}

func (s *Service) ResolveActionPrompt(action string) {
	s.ResolveActionPromptByID("", action)
}

func (s *Service) ResolveActionPromptByID(promptID, action string) {
	s.mu.Lock()
	prompt := s.actionPrompt
	if prompt == nil {
		s.mu.Unlock()
		return
	}
	if promptID != "" && prompt.ID != promptID {
		s.mu.Unlock()
		return
	}
	promptCopy := *prompt
	opener := s.URLOpener

	switch promptCopy.Kind {
	case core.PromptDriveAuth:
		if action == "open_url" {
			s.mu.Unlock()
			if promptCopy.URL != "" && opener != nil {
				opener(promptCopy.URL)
			}
			return
		}
		ch := s.driveAuthResultCh
		s.actionPrompt = nil
		s.driveAuthResultCh = nil
		var prepCancel context.CancelFunc
		if ch == nil && action == "secondary" && promptCopy.AccountID != "" {
			s.skipNextDriveMount[promptCopy.AccountID] = true
			if mh, ok := s.driveMountCancels[promptCopy.AccountID]; ok && mh.cancel != nil {
				prepCancel = mh.cancel
				delete(s.driveMountCancels, promptCopy.AccountID)
			}
		}
		s.mu.Unlock()
		s.notifyUI()
		if prepCancel != nil {
			prepCancel()
		}
		if ch != nil {
			proceed := action == "primary"
			select {
			case ch <- proceed:
			default:
			}
		} else if action == "secondary" {
			s.ShowToast("已切换为纯云端运行，本次不再等待 Google Drive 授权")
		}
	case core.PromptComfyHelp:
		if action == "open_url" {
			s.mu.Unlock()
			if promptCopy.URL != "" && opener != nil {
				opener(promptCopy.URL)
			} else if promptCopy.AccountID != "" {
				s.OpenColabInBrowser(promptCopy.AccountID)
			}
			return
		}
		s.actionPrompt = nil
		s.mu.Unlock()
		s.notifyUI()
		switch action {
		case "primary":
			if promptCopy.AccountID != "" {
				if promptCopy.PrimaryAction == "open_colab" {
					if promptCopy.URL != "" && opener != nil {
						opener(promptCopy.URL)
					} else {
						s.OpenColabInBrowser(promptCopy.AccountID)
					}
				} else {
					go s.OpenComfyUI(promptCopy.AccountID)
				}
			}
		case "secondary":
			if promptCopy.AccountID != "" {
				if acc, ok := s.accountByID(promptCopy.AccountID); ok {
					go s.launchVideoDeployFlow(acc)
				}
			}
		}
	case core.PromptConfirmStop:
		expectedEP := promptCopy.ExpectedEndpoint
		curRT := s.Runtime[promptCopy.AccountID]
		curEP := curRT.ActiveEndpoint
		isRunning := curRT.Phase.IsRunning()
		s.actionPrompt = nil
		s.mu.Unlock()
		s.notifyUI()
		if promptCopy.AccountID != "" {
			stale := promptCopy.SessionName == "" && (!isRunning || (expectedEP != "" && curEP != "" && curEP != expectedEP))
			switch action {
			case "primary":
				if stale {
					s.ShowToast("云端机器已切换或已关机，已取消本次过期停机确认")
					return
				}
				go s.StopAccountSession(promptCopy.AccountID, promptCopy.SessionName, "")
			case "secondary":
				if stale {
					s.ShowToast("云端机器已停机或已切换，无法执行过期保存操作")
					return
				}
				if promptCopy.HasDownloadableTemp {
					go s.DownloadAllTempVideos(promptCopy.AccountID)
				} else {
					go s.OpenComfyUI(promptCopy.AccountID)
				}
			}
		}
	case core.PromptConfirmStopAll:
		s.actionPrompt = nil
		s.mu.Unlock()
		s.notifyUI()
		if action == "primary" {
			go s.StopAllManagedSessions()
		}
	case core.PromptConfirmRemoveAccount:
		s.actionPrompt = nil
		s.mu.Unlock()
		s.notifyUI()
		if promptCopy.AccountID != "" {
			switch action {
			case "primary":
				go func(accID string) {
					s.CancelQueue(accID)
					if !s.StopAccountManagedSessions(accID) {
						s.ShowToast("未能确认该账号云端机器已完全停机，为防计费失控已保留账号配置，请先在 Colab 网页确认")
						return
					}
					s.RemoveAccount(accID)
				}(promptCopy.AccountID)
			case "secondary":
				s.RemoveAccount(promptCopy.AccountID)
			}
		}
	case core.PromptConfirmQuit:
		quitFn := s.pendingQuitCallback
		s.actionPrompt = nil
		s.pendingQuitCallback = nil
		s.mu.Unlock()
		s.notifyUI()
		switch action {
		case "primary":
			go func() {
				if !s.StopAllManagedSessions() {
					s.ShowToast("仍有云端机器未能确认停机，已暂缓退出以保持监控")
					return
				}
				if quitFn != nil {
					quitFn()
				}
			}()
		case "secondary":
			if quitFn != nil {
				quitFn()
			}
		}
	case core.PromptTempOutputGuard:
		curRT := s.Runtime[promptCopy.AccountID]
		stale := !curRT.Phase.IsRunning() || (promptCopy.ExpectedEndpoint != "" && curRT.ActiveEndpoint != "" && curRT.ActiveEndpoint != promptCopy.ExpectedEndpoint)
		s.actionPrompt = nil
		s.mu.Unlock()
		s.notifyUI()
		if promptCopy.AccountID != "" && (action == "primary" || action == "secondary") {
			if stale {
				s.ShowToast("原云端机器已停机或已切换，已忽略过期护片操作")
				return
			}
			switch action {
			case "primary":
				s.ResetIdleTimer(promptCopy.AccountID)
				if promptCopy.HasDownloadableTemp {
					go s.DownloadAllTempVideos(promptCopy.AccountID)
				} else {
					go s.OpenComfyUI(promptCopy.AccountID)
				}
			case "secondary":
				go s.StopAccountSession(promptCopy.AccountID, "", "用户确认跳过临时盘护片关机")
			}
		}
	case core.PromptSwitchModeWhileRunning:
		curRT := s.Runtime[promptCopy.AccountID]
		stale := !curRT.Phase.IsRunning() || (promptCopy.ExpectedEndpoint != "" && curRT.ActiveEndpoint != "" && curRT.ActiveEndpoint != promptCopy.ExpectedEndpoint)
		s.actionPrompt = nil
		s.mu.Unlock()
		s.notifyUI()
		if promptCopy.AccountID != "" {
			switch action {
			case "primary":
				s.applyNextBootPresetOnly(promptCopy.AccountID, promptCopy.TargetRecipe, promptCopy.TargetH3Variant, true)
			case "secondary":
				if stale {
					s.ShowToast("云端机器已停机或已切换，已取消过期原地重装")
					return
				}
				go s.redeploySwitchedMode(promptCopy.AccountID, promptCopy.TargetRecipe, promptCopy.TargetH3Variant, promptCopy.ExpectedEndpoint)
			}
		}
	case core.PromptWebStopGuide:
		s.actionPrompt = nil
		s.mu.Unlock()
		s.notifyUI()
		if action == "primary" && promptCopy.AccountID != "" {
			s.OpenColabWebHome(promptCopy.AccountID)
		}
	default:
		s.actionPrompt = nil
		s.mu.Unlock()
		s.notifyUI()
	}
}

func (s *Service) RequestConfirmStop(accountID string) {
	s.RequestConfirmStopSession(accountID, "")
}

func sessionStopRiskSummary(acc core.AccountProfile, rt core.AccountRuntimeState) (riskLine string, hasRisk bool, hasDownloadable bool) {
	var parts []string
	if rt.QueueRunning > 0 {
		parts = append(parts, fmt.Sprintf("%d 条正在生成", rt.QueueRunning))
	}
	if rt.QueuePending > 0 {
		parts = append(parts, fmt.Sprintf("%d 条排队等待", rt.QueuePending))
	}
	tempCnt := rt.TempOutputVideos
	if tempCnt <= 0 && rt.IsTempOutputActive(acc) && rt.SessionNewVideoCount > 0 {
		tempCnt = rt.SessionNewVideoCount
	}
	if tempCnt > 0 {
		parts = append(parts, fmt.Sprintf("%d 条视频仅存在云电脑上（关机后永久清空）", tempCnt))
	} else if rt.TempOutputScanKnown && rt.TempOutputVideos < 0 {
		parts = append(parts, "云电脑临时盘存在未确认成片（关机后永久清空）")
	}
	if len(parts) == 0 {
		return "", false, false
	}
	return "⚠ " + strings.Join(parts, "，") + "。", true, len(rt.SessionOutputs) > 0
}

func (s *Service) aggregateManagedStopRisks() string {
	accounts := s.AccountsSnapshot()
	totalRun, totalPend, totalTemp := 0, 0, 0
	unknownTemp := false
	for _, acc := range accounts {
		rt := s.StateFor(acc.ID)
		if !rt.Phase.IsRunning() {
			continue
		}
		totalRun += rt.QueueRunning
		totalPend += rt.QueuePending
		if rt.TempOutputVideos > 0 {
			totalTemp += rt.TempOutputVideos
		} else if rt.IsTempOutputActive(acc) && rt.SessionNewVideoCount > 0 {
			totalTemp += rt.SessionNewVideoCount
		} else if rt.TempOutputScanKnown && rt.TempOutputVideos < 0 {
			unknownTemp = true
		}
	}
	var parts []string
	if totalRun > 0 {
		parts = append(parts, fmt.Sprintf("%d 条正在生成", totalRun))
	}
	if totalPend > 0 {
		parts = append(parts, fmt.Sprintf("%d 条排队中", totalPend))
	}
	if totalTemp > 0 {
		parts = append(parts, fmt.Sprintf("%d 条视频仅存在云电脑临时盘（关机将清空）", totalTemp))
	} else if unknownTemp {
		parts = append(parts, "云电脑临时盘存在未确认视频（关机将清空）")
	}
	if len(parts) == 0 {
		return ""
	}
	return "\n\n⚠ 当前活跃风险：" + strings.Join(parts, "，") + "。"
}

func (s *Service) RequestConfirmStopSession(accountID, specificSession string) {
	acc, ok := s.accountByID(accountID)
	if !ok {
		return
	}
	rt := s.StateFor(accountID)
	target := strings.TrimSpace(specificSession)
	if target == "" || target == acc.SessionName {
		if !rt.Phase.IsRunning() {
			return
		}
		msg := fmt.Sprintf("账号「%s」本次已运行 %s（当前约 %.2f CU/h）。确认关机后会立即释放云端显卡并停止计费。", acc.DisplayName, core.FormatStopwatch(rt.SessionUptimeSec), rt.CURatePerHour)
		primaryLbl := "确认关机止损"
		secondaryLbl := ""
		riskLine, hasRisk, hasDownloadable := sessionStopRiskSummary(acc, rt)
		if hasRisk {
			msg = riskLine + "\n\n" + msg
			primaryLbl = "放弃并立刻关机"
			if hasDownloadable {
				secondaryLbl = "先下回本机"
			} else if !rt.EffectiveSessionEnableLLM(acc) {
				secondaryLbl = "先打开画布保存"
			}
		}
		s.PresentActionPrompt(&core.UserActionPrompt{
			ID:                  uuid.New().String(),
			Kind:                core.PromptConfirmStop,
			AccountID:           accountID,
			SessionName:         "",
			ExpectedEndpoint:    rt.ActiveEndpoint,
			Title:               fmt.Sprintf("确认关闭当前 %s 云端机器？", rt.ShortGPUName()),
			Message:             msg,
			PrimaryLabel:        primaryLbl,
			SecondaryLabel:      secondaryLbl,
			CancelLabel:         "继续运行",
			HasDownloadableTemp: hasDownloadable,
		})
		return
	}
	s.PresentActionPrompt(&core.UserActionPrompt{
		ID:           uuid.New().String(),
		Kind:         core.PromptConfirmStop,
		AccountID:    accountID,
		SessionName:  target,
		Title:        fmt.Sprintf("确认关闭机器「%s」？", target),
		Message:      fmt.Sprintf("将停止账号「%s」下这台名为 %s 的云端机器并停止扣点。", acc.DisplayName, target),
		PrimaryLabel: "确认关机",
		CancelLabel:  "取消",
	})
}

func (s *Service) RequestWebStopGuide(accountID string, sess core.ColabSessionItem, rateHint string) {
	acc, ok := s.accountByID(accountID)
	if !ok {
		return
	}
	s.mu.RLock()
	driveAuthWaiting := s.actionPrompt != nil && s.actionPrompt.Kind == core.PromptDriveAuth && s.driveAuthResultCh != nil
	s.mu.RUnlock()
	if driveAuthWaiting {
		s.ShowToast("请先完成当前的 Google Drive 授权，再查看手动关机指引")
		return
	}
	hw := strings.TrimSpace(sess.Hardware)
	if hw == "" {
		hw = "云端 GPU"
	}
	spec := hw
	if sh := strings.TrimSpace(sess.Shape); sh != "" {
		spec = fmt.Sprintf("%s · %s", hw, sh)
	}
	machineInfo := spec
	if rh := strings.TrimSpace(rateHint); rh != "" {
		machineInfo = fmt.Sprintf("%s（%s）", spec, rh)
	}
	msg := fmt.Sprintf(
		"当前机器：%s · 账号：%s\n这台机器不是由当前软件启动的，无法在这里直接关机，请按以下 3 步到 Colab 网页关闭：\n第 1 步：点击下方「打开 Colab 网页」，先核对页面右上角登录的是上方账号，再打开任意一个笔记本。\n第 2 步：点击顶部菜单「代码执行程序 → 管理会话」（英文界面为 Runtime → Manage sessions）。\n第 3 步：在列表中核对笔记本名称与显卡型号（%s），确认无误后点击右侧「终止 / TERMINATE」即可停止扣点。",
		machineInfo,
		driveAuthAccountLabel(acc),
		hw,
	)
	s.PresentActionPrompt(&core.UserActionPrompt{
		ID:           uuid.New().String(),
		Kind:         core.PromptWebStopGuide,
		AccountID:    accountID,
		Title:        fmt.Sprintf("这台 %s 需到 Colab 网页手动关闭", hw),
		Message:      msg,
		PrimaryLabel: "打开 Colab 网页 ↗",
		CancelLabel:  "我知道了",
	})
}

func (s *Service) RequestConfirmStopAll() {
	n := s.TotalManagedRunningSessionCount()
	if n == 0 {
		if orphans := s.TotalOrphanSessionCount(); orphans > 0 {
			s.ShowToast(fmt.Sprintf("还有 %d 台在 Colab 网页开的机器无法直接关闭，请去 Colab 网页手动断开", orphans))
		}
		return
	}
	_, totalRate := s.TotalBalanceAndRate()
	riskSuffix := s.aggregateManagedStopRisks()
	s.PresentActionPrompt(&core.UserActionPrompt{
		ID:           uuid.New().String(),
		Kind:         core.PromptConfirmStopAll,
		Title:        fmt.Sprintf("确认关闭全部 %d 台运行中的云端机器？", n),
		Message:      fmt.Sprintf("当前所有账号合计消耗约 %.2f CU/h。确认后会先保存工作流，再逐一关闭云端机器。", totalRate) + riskSuffix,
		PrimaryLabel: "全部关机止损",
		CancelLabel:  "先不关",
	})
}

func (s *Service) RequestConfirmRemoveAccount(accountID string) {
	hasPendingInFlight := false
	if s.History != nil {
		_, hasPendingInFlight = s.History.InFlightByAccount()[accountID]
	}

	s.mu.RLock()
	var acc core.AccountProfile
	found := false
	for _, a := range s.Settings.Accounts {
		if a.ID == accountID {
			acc = a
			found = true
			break
		}
	}
	if !found {
		s.mu.RUnlock()
		return
	}
	rt := s.Runtime[accountID]
	managedCount := len(managedSessionsForAccountLocked(acc, rt))
	s.mu.RUnlock()

	if managedCount == 0 && !rt.Phase.IsQueuing() && !rt.Phase.IsBusy() && !hasPendingInFlight {
		s.RemoveAccount(accountID)
		return
	}
	msg := fmt.Sprintf("账号「%s」当前还有 %d 个运行中的云端会话（或正在启动/排队）。直接移除本地配置会导致失去监控与自动关机保护，建议先停机再移除。", acc.DisplayName, managedCount)
	if managedCount == 0 && hasPendingInFlight {
		msg = fmt.Sprintf("账号「%s」上次退出前仍有未核验的云端计费记录，建议先停机核验后再移除，或确认仅移除本地配置。", acc.DisplayName)
	} else if managedCount == 0 {
		msg = fmt.Sprintf("账号「%s」当前正在申请或排队显卡。确认后将取消申请并移除该账号与本地凭据。", acc.DisplayName)
	}
	s.PresentActionPrompt(&core.UserActionPrompt{
		ID:             uuid.New().String(),
		Kind:           core.PromptConfirmRemoveAccount,
		AccountID:      accountID,
		Title:          fmt.Sprintf("账号「%s」尚有活跃任务，如何移除？", acc.DisplayName),
		Message:        msg,
		PrimaryLabel:   "先停机再移除",
		SecondaryLabel: "仅移除本地配置",
		CancelLabel:    "取消",
	})
}

func (s *Service) RequestConfirmQuit(onConfirmedQuit func()) {
	n := s.TotalManagedRunningSessionCount()
	if n == 0 {
		if onConfirmedQuit != nil {
			onConfirmedQuit()
		}
		return
	}
	_, totalRate := s.TotalBalanceAndRate()
	riskSuffix := s.aggregateManagedStopRisks()
	s.mu.Lock()
	s.pendingQuitCallback = onConfirmedQuit
	s.actionPrompt = &core.UserActionPrompt{
		ID:             uuid.New().String(),
		Kind:           core.PromptConfirmQuit,
		Title:          fmt.Sprintf("还有 %d 台云端显卡正在计费，确定退出？", n),
		Message:        fmt.Sprintf("当前消耗约 %.2f CU/h。退出桌面端后，本地空闲自动关机监控将停止（云端将依赖 Colab 官方闲置超时回收）。", totalRate) + riskSuffix,
		PrimaryLabel:   "先全部关机再退出",
		SecondaryLabel: "保持云端运行，仅退出",
		CancelLabel:    "留在后台继续监控",
	}
	s.mu.Unlock()
	s.notifyUI()
}
