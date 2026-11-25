package core_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/LJMStark/Colab-Tools/internal/core"
)

func TestInputValidatorBlocksShellInjection(t *testing.T) {
	if !core.IsValidSessionName("llm-coder_01.prod") {
		t.Fatal("valid session name rejected")
	}
	for _, bad := range []string{"foo; rm -rf /", "$(whoami)", "../etc/passwd", ""} {
		if core.IsValidSessionName(bad) {
			t.Fatalf("expected invalid session name: %q", bad)
		}
	}
	if !core.IsValidEndpointURL("https://demo-node.trycloudflare.com") {
		t.Fatal("valid URL rejected")
	}
	for _, bad := range []string{"javascript:alert(1)", "https://evil.com;rm -rf /", "file:///etc/passwd"} {
		if core.IsValidEndpointURL(bad) {
			t.Fatalf("expected invalid URL: %q", bad)
		}
	}
}

func TestSubscriptionTierAndOfflineHoursEstimation(t *testing.T) {
	if core.SuggestedTier(1000) != core.TierAIUltra {
		t.Fatal("expected AI Ultra for 1000 CU")
	}
	if core.SuggestedTier(200) != core.TierColabProPlus {
		t.Fatal("expected Pro+ for 200 CU")
	}
	if core.SuggestedTier(0) != core.TierFree {
		t.Fatal("expected Free for 0 CU")
	}

	acc := core.NewDefaultAccountProfile("Ultra", "u@example.com", core.TierAIUltra, core.AuthSystemDefaultOAuth2)
	acc.PreferredAccelerator = core.AccAutoBest
	rt := core.NewAccountRuntimeState()
	rt.CUBalance = 2362.2

	// Offline auto preset uses L4 baseline (2.33 CU/h -> ~1014h), never CPU 0.1 CU/h (23622h)
	hrs, basis, ok := rt.EstimatedHoursRemaining(acc)
	if !ok || basis != "L4" || int(hrs+0.5) != 1014 {
		t.Fatalf("unexpected offline estimate: hrs=%.1f basis=%s ok=%v", hrs, basis, ok)
	}

	// Queue targeting G4 first uses G4 (7.0 CU/h -> ~337h)
	acc.EnablePoliteQueue = true
	acc.QueueTargets = []core.AcceleratorOption{core.AccG4, core.AccH100}
	hrs, basis, ok = rt.EstimatedHoursRemaining(acc)
	if !ok || basis != "G4" || int(hrs+0.5) != 337 {
		t.Fatalf("unexpected queue estimate: hrs=%.1f basis=%s ok=%v", hrs, basis, ok)
	}

	// Free tier without paid balance is restricted from SSH/WebUI
	freeAcc := core.NewDefaultAccountProfile("Free", "f@example.com", core.TierFree, core.AuthSystemDefaultOAuth2)
	freeRT := core.NewAccountRuntimeState()
	if !freeRT.IsFreeRuntimeRestricted(freeAcc) {
		t.Fatal("expected free tier without CU balance to be restricted")
	}
}

func TestPoliteQueueEqualJitterFallbackAndScheduleGrace(t *testing.T) {
	now := time.Date(2026, 11, 25, 7, 0, 0, 0, time.Local)

	// Equal Jitter bounds for round 1 (cap 180s -> [90, 180])
	dMin, ok := core.NextQueueDelay(1, now, now, 0.0)
	if !ok || dMin != 90*time.Second {
		t.Fatalf("expected 90s min delay, got %v %v", dMin, ok)
	}
	dMax, ok := core.NextQueueDelay(1, now, now, 1.0)
	if !ok || dMax != 180*time.Second {
		t.Fatalf("expected 180s max delay, got %v %v", dMax, ok)
	}
	if _, ok := core.NextQueueDelay(core.QueueMaxAttempts, now, now, 0.5); ok {
		t.Fatal("expected nil after max attempts")
	}
	if _, ok := core.NextQueueDelay(1, now.Add(-39*time.Minute), now, 1.0); ok {
		t.Fatal("expected nil when exceeding 40m total wait")
	}

	// Two-stage fallback
	r2 := core.QueueTargetsForRound(2, []core.AcceleratorOption{core.AccG4, core.AccH100}, true, 2, core.AccL4, core.TierAIUltra)
	if len(r2) != 2 {
		t.Fatalf("round 2 must not append fallback yet: %v", r2)
	}
	r3 := core.QueueTargetsForRound(3, []core.AcceleratorOption{core.AccG4, core.AccH100}, true, 2, core.AccL4, core.TierAIUltra)
	if len(r3) != 3 || r3[2] != core.AccL4 {
		t.Fatalf("round 3 must append L4 fallback: %v", r3)
	}

	// Circuit breaker classification
	if core.ClassifyProvisionFailure(1, false, "", "HTTP 503 Service Unavailable", "G4").Kind != core.OutcomeCapacity503 {
		t.Fatal("503 should allow queue")
	}
	if core.ClassifyProvisionFailure(1, false, "", "HTTP 429 Too Many Requests", "G4").Kind != core.OutcomeCircuitBroken {
		t.Fatal("429 must circuit break")
	}
	if core.ClassifyProvisionFailure(1, false, "", "HTTP 412 Too many assignments", "G4").Kind != core.OutcomeCircuitBroken {
		t.Fatal("412 must circuit break")
	}
	if core.ClassifyProvisionFailure(1, true, "", "", "G4").Kind != core.OutcomeCircuitBroken {
		t.Fatal("timeout must circuit break")
	}

	// Schedule grace window & missed one-shot expiry
	fireAt := now
	decOK := core.EvaluateSchedule(fireAt, false, 7, 0, fireAt.Add(5*time.Minute))
	if decOK.Kind != core.ScheduleTriggerNow || decOK.NextFireAt != nil {
		t.Fatalf("expected triggerNow within 10m grace, got %+v", decOK)
	}
	decExpired := core.EvaluateSchedule(fireAt, false, 7, 0, fireAt.Add(15*time.Minute))
	if decExpired.Kind != core.ScheduleMissedExpired || decExpired.NextFireAt != nil {
		t.Fatalf("expected missedExpired past 10m grace, got %+v", decExpired)
	}
	decDaily := core.EvaluateSchedule(fireAt, true, 7, 0, fireAt.Add(15*time.Minute))
	if decDaily.Kind != core.ScheduleMissedExpired || decDaily.NextFireAt == nil || !decDaily.NextFireAt.After(fireAt) {
		t.Fatalf("expected daily rollover next day, got %+v", decDaily)
	}
}

func TestWatchdogDualLowLoadAndMultiSlotAggregation(t *testing.T) {
	now := time.Now()
	// Missing reading or TPU never maps to idle
	low := 2
	highCPU := 60
	if core.EvaluateHardwareActivity(&low, nil, false, now).Kind != core.SignalUnknown {
		t.Fatal("nil CPU must be unknown")
	}
	if core.EvaluateHardwareActivity(&low, &low, true, now).Kind != core.SignalUnknown {
		t.Fatal("TPU must be unknown")
	}
	// GPU < 5% but CPU = 60% (weight download / FFmpeg transcode) -> busy
	if core.EvaluateHardwareActivity(&low, &highCPU, false, now).Kind != core.SignalBusy {
		t.Fatal("high CPU during download must stay busy")
	}
	// Both low -> idle
	if core.EvaluateHardwareActivity(&low, &low, false, now).Kind != core.SignalIdle {
		t.Fatal("dual low load must be idle")
	}

	// Multi-slot LLM aggregation: slot 0 idle, slot 1 busy -> overall busy
	f, tr := false, true
	eval := core.EvaluateLLMSlots([]core.SlotInfo{
		{IsProcessing: &f},
		{IsProcessing: &tr},
	}, now)
	if eval.Signal.Kind != core.SignalBusy {
		t.Fatal("second busy slot must keep LLM busy")
	}

	// Never shut down without fresh verified idle signal
	stale := core.ActivitySignal{Kind: core.SignalIdle, ObservedAt: now.Add(-120 * time.Second)}
	act := core.EvaluateWatchdog(core.WatchdogInput{
		IsRunning:           true,
		EnableLLMModule:     true,
		ActivitySignal:      stale,
		AutoShutdownEnabled: true,
		IdleSeconds:         9999,
		TimeoutMinutes:      15,
		WarnBeforeMinutes:   2,
		Now:                 now,
	})
	if act.Kind != core.WatchdogPausedNoSignal {
		t.Fatalf("stale idle signal must pause watchdog, got %+v", act)
	}

	// Status JSON & ComfyUI queue parsing
	status, ok := core.ParseWorkloadStatusJSON(`{"kind":"video","busy":true,"queue_running":1,"port":8188}`)
	if !ok {
		t.Fatal("failed to parse workload status json")
	}
	if b, known := status.IsWorkloadBusy(); !known || !b {
		t.Fatal("expected busy workload")
	}
	cq, ok := core.ParseComfyQueueJSON(`{"queue_running":[["a"]],"queue_pending":[]}`)
	if !ok || !cq.IsBusy() || cq.Running != 1 {
		t.Fatal("expected busy ComfyUI queue")
	}
}

func TestAllocationInsightsAndLegacyOnboardingMigration(t *testing.T) {
	ins := core.NewAllocationInsights()
	morning := time.Date(2026, 11, 25, 6, 30, 0, 0, time.Local)
	evening := time.Date(2026, 11, 25, 21, 0, 0, 0, time.Local)
	ins.Record("NVIDIA RTX PRO 6000", true, morning)
	ins.Record("G4", true, morning)
	ins.Record("G4", true, morning)
	ins.Record("G4", false, evening)

	slots := ins.SixSlotHeatmap("G4")
	if slots[1].Attempts != 3 || slots[1].Successes != 3 {
		t.Fatalf("unexpected 04-08 slot: %+v", slots[1])
	}
	if sum := ins.RecommendedWindowSummary("G4"); sum == "" {
		t.Fatal("expected golden hour summary")
	}

	// Corrupt counts clamp safely
	ins.BucketsByHardware["G4"]["7"] = core.AllocationBucket{Attempts: -5, Successes: 999}
	ins.BucketsByHardware["G4"]["99"] = core.AllocationBucket{Attempts: 10, Successes: 5}
	ins.Sanitize()
	if _, exists := ins.BucketsByHardware["G4"]["99"]; exists {
		t.Fatal("invalid hour 99 must be stripped")
	}

	// Legacy config migration skips onboarding for existing account users, shows for fresh users
	tmp := t.TempDir()
	withAcc := filepath.Join(tmp, "existing.json")
	_ = os.WriteFile(withAcc, []byte(`{"accounts":[{"id":"11111111-1111-1111-1111-111111111111","displayName":"Old","tier":"colab_pro","queueFallbackTarget":"FUTURE_B200"}]}`), 0o600)
	s1 := core.LoadAppSettings(withAcc)
	if !s1.HasCompletedOnboarding || len(s1.Accounts) != 1 || s1.Accounts[0].QueueFallbackTarget == "FUTURE_B200" {
		t.Fatalf("unexpected legacy load: %+v", s1)
	}

	emptyCfg := filepath.Join(tmp, "empty.json")
	_ = os.WriteFile(emptyCfg, []byte(`{}`), 0o600)
	s2 := core.LoadAppSettings(emptyCfg)
	if s2.HasCompletedOnboarding {
		t.Fatal("fresh empty config must show onboarding")
	}

	// Swift JSONEncoder numeric reference-date migration, isolated_adc identity preservation, cooldown carryover, & corrupt backup
	futureAppleSec := time.Now().Add(12 * time.Minute).Sub(time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)).Seconds()
	swiftCfg := filepath.Join(tmp, "swift-036.json")
	swiftPayload := fmt.Sprintf(`{
		"accounts": [{
			"id": "22222222-2222-2222-2222-222222222222",
			"displayName": "Work",
			"authMode": "isolated_adc",
			"tier": "colab_pro_plus",
			"scheduledStartEnabled": true,
			"scheduledNextFireAt": %.3f
		}],
		"circuitBreakUntilByAccount": {
			"22222222-2222-2222-2222-222222222222": %.3f
		}
	}`, futureAppleSec, futureAppleSec)
	_ = os.WriteFile(swiftCfg, []byte(swiftPayload), 0o600)
	s3 := core.LoadAppSettings(swiftCfg)
	if len(s3.Accounts) != 1 {
		t.Fatalf("Swift 0.3.6 config failed to load: %+v", s3)
	}
	if s3.Accounts[0].AuthMode != core.AuthIsolatedVaultADC {
		t.Fatalf("legacy isolated_adc must migrate to %q, got %q", core.AuthIsolatedVaultADC, s3.Accounts[0].AuthMode)
	}
	if s3.Accounts[0].ScheduledNextFireAt == nil || !s3.Accounts[0].ScheduledNextFireAt.After(time.Now()) {
		t.Fatalf("Swift reference-date scheduledNextFireAt must decode into future time, got %+v", s3.Accounts[0].ScheduledNextFireAt)
	}
	if cd, ok := s3.QueueCooldownsByAccount["22222222-2222-2222-2222-222222222222"]; !ok || !cd.After(time.Now()) {
		t.Fatalf("legacy circuitBreakUntilByAccount cooldown must survive upgrade: %+v", s3.QueueCooldownsByAccount)
	}

	// Unknown authMode must NEVER silently downgrade to system default OAuth2
	unknownAcc := core.NewDefaultAccountProfile("Future", "future@example.com", core.TierColabPro, core.AccountAuthMode("future_enterprise_sso"))
	unknownAcc.Sanitize()
	if unknownAcc.AuthMode == core.AuthSystemDefaultOAuth2 {
		t.Fatal("unknown authMode must not downgrade to system default oauth2")
	}

	// Corrupt existing config must be preserved as a .corrupt.<ts> backup
	corruptCfg := filepath.Join(tmp, "corrupt.json")
	_ = os.WriteFile(corruptCfg, []byte(`{"accounts":[broken`), 0o600)
	_ = core.LoadAppSettings(corruptCfg)
	matches, _ := filepath.Glob(corruptCfg + ".corrupt.*")
	if len(matches) == 0 {
		t.Fatal("expected corrupt config backup file to be written")
	}
}
