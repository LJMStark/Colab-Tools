package core_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	acc.GPUStrategy = core.GPUStrategyCustom
	acc.EnablePoliteQueue = false
	acc.PreferredAccelerator = core.AccAutoBest
	rt := core.NewAccountRuntimeState()
	rt.CUBalance = 2362.2

	// Offline custom single-launch auto preset uses L4 baseline (2.33 CU/h -> ~1014h), never CPU 0.1 CU/h (23622h)
	hrs, basis, ok := rt.EstimatedHoursRemaining(acc)
	if !ok || basis != "L4" || int(hrs+0.5) != 1014 {
		t.Fatalf("unexpected offline estimate: hrs=%.1f basis=%s ok=%v", hrs, basis, ok)
	}

	// Custom queue targeting G4 first uses G4 (7.0 CU/h -> ~337h)
	acc.EnablePoliteQueue = true
	acc.QueueTargets = []core.AcceleratorOption{core.AccG4, core.AccH100}
	hrs, basis, ok = rt.EstimatedHoursRemaining(acc)
	if !ok || basis != "G4" || int(hrs+0.5) != 337 {
		t.Fatalf("unexpected queue estimate: hrs=%.1f basis=%s ok=%v", hrs, basis, ok)
	}

	// Smart strategy auto-adapts GPU priority per scenario:
	// H3 Flash / ComfyUI / LLM / None -> primary [G4, A100] with L4 fallback from round 3;
	// H3 Original -> G4 -> A100 -> H100 (no fallback by default); GGUF -> L4 -> G4 -> A100 (no fallback)
	acc.GPUStrategy = core.GPUStrategyAutoSmart
	acc.DeployRecipe = core.RecipeH3Video
	acc.H3Variant = core.H3VariantFlash
	if got := core.EffectiveQueueTargets(acc); len(got) != 2 || got[0] != core.AccG4 || got[1] != core.AccA100 {
		t.Fatalf("expected H3 Flash smart primary order G4->A100, got %v", got)
	}
	if fbOn, fbAfter, fbTarget := core.EffectiveFallback(acc); !fbOn || fbAfter != 2 || fbTarget != core.AccL4 {
		t.Fatalf("expected H3 Flash Ultra fallback (true, 2, L4), got (%v, %d, %v)", fbOn, fbAfter, fbTarget)
	}
	acc.DeployRecipe = core.RecipeComfyUIGeneral
	if got := core.EffectiveQueueTargets(acc); len(got) != 2 || got[0] != core.AccG4 || got[1] != core.AccA100 {
		t.Fatalf("expected General ComfyUI smart primary order G4->A100, got %v", got)
	}
	acc.DeployRecipe = core.RecipeLLMProxy
	if got := core.EffectiveQueueTargets(acc); len(got) != 2 || got[0] != core.AccG4 || got[1] != core.AccA100 {
		t.Fatalf("expected LLM Proxy smart primary order G4->A100, got %v", got)
	}
	acc.DeployRecipe = core.RecipeNone
	if got := core.EffectiveQueueTargets(acc); len(got) != 2 || got[0] != core.AccG4 || got[1] != core.AccA100 {
		t.Fatalf("expected Pure Compute smart primary order G4->A100, got %v", got)
	}
	acc.DeployRecipe = core.RecipeH3Video
	acc.H3Variant = core.H3VariantOriginal
	if got := core.EffectiveQueueTargets(acc); len(got) != 3 || got[0] != core.AccG4 || got[1] != core.AccA100 || got[2] != core.AccH100 {
		t.Fatalf("expected H3 Original smart order G4->A100->H100, got %v", got)
	}
	if fbEnabled, _, _ := core.EffectiveFallback(acc); fbEnabled {
		t.Fatal("expected EffectiveFallback to stay disabled for H3 Original under auto_smart by default")
	}
	acc.H3Variant = core.H3VariantFlashGGUF
	if got := core.EffectiveQueueTargets(acc); len(got) != 3 || got[0] != core.AccL4 || got[1] != core.AccG4 || got[2] != core.AccA100 {
		t.Fatalf("expected H3 GGUF smart order L4->G4->A100, got %v", got)
	}
	if fbEnabled, _, _ := core.EffectiveFallback(acc); fbEnabled {
		t.Fatal("expected EffectiveFallback to stay disabled for H3 GGUF under auto_smart")
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
	r6 := core.QueueTargetsForRound(6, []core.AcceleratorOption{core.AccG4, core.AccH100}, true, 2, core.AccL4, core.TierAIUltra)
	if len(r6) != 3 || r6[2] != core.AccL4 {
		t.Fatalf("round 6+ in persistent mode must keep L4 fallback active: %v", r6)
	}

	// Real google-colab-cli 0.8.x error classification (never misclassify single-card rejection or timeout as 15m rate-limit)
	if core.ClassifyProvisionFailure(1, false, "", "HTTP 503 Service Unavailable", "G4").Kind != core.OutcomeCapacity503 {
		t.Fatal("503 should allow queue/fallback")
	}
	if core.ClassifyProvisionFailure(1, false, "", "requests.exceptions.ReadTimeout: Read timed out", "G4").Kind != core.OutcomeCapacity503 {
		t.Fatal("ReadTimeout should be treated as retryable capacity/network issue")
	}
	if core.ClassifyProvisionFailure(1, false, "", "[colab] Backend rejected accelerator 'G4'. You may not have quota or entitlement for this accelerator on your account.", "G4").Kind != core.OutcomeUnsupportedTier400 {
		t.Fatal("google-colab-cli 0.8.0 Backend rejected accelerator must classify as single-card unsupported 400")
	}
	if core.ClassifyProvisionFailure(1, false, "", "[colab] Allocation refused (precondition failed). This can mean too many active sessions, or a temporary usage or capacity limit.", "G4").Kind != core.OutcomePrecondition412 {
		t.Fatal("412 Allocation refused must classify as OutcomePrecondition412 for active-session inspection")
	}
	if core.ClassifyProvisionFailure(1, false, "", "HTTP 401 Unauthorized", "G4").Kind != core.OutcomeAuthError {
		t.Fatal("401 must classify as OutcomeAuthError without 15m lock")
	}
	if core.ClassifyProvisionFailure(1, true, "", "", "G4").Kind != core.OutcomeTransientError {
		t.Fatal("command timeout must classify as transient error without 15m lock")
	}
	if core.ClassifyProvisionFailure(1, false, "", "HTTP 429 Too Many Requests", "G4").Kind != core.OutcomeRateLimited {
		t.Fatal("429 must trigger rate-limit cooldown")
	}
	if core.ClassifyProvisionFailure(1, false, "", "Your account is blocked from accessing Colab due to abusive usage", "G4").Kind != core.OutcomeRateLimited {
		t.Fatal("abuse warning must trigger rate-limit cooldown")
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

func TestH3DeployEnvValidationAndSanitization(t *testing.T) {
	acc := core.NewDefaultAccountProfile("Video", "v@example.com", core.TierAIUltra, core.AuthSystemDefaultOAuth2)
	acc.DeployRecipe = core.RecipeH3Video
	acc.H3Variant = core.H3VariantOriginal
	env, err := core.BuildDeployEnv(acc)
	if err != nil {
		t.Fatalf("unexpected error for original variant: %v", err)
	}
	if env["H3_VARIANT"] != "original" || env["USE_GOOGLE_DRIVE"] != "1" || env["ENABLE_CLOUDFLARED"] != "0" || env["RESTORE_CUSTOM_NODES_SNAPSHOT"] != "0" {
		t.Fatalf("unexpected deploy env: %+v", env)
	}
	acc.RestoreCustomNodesSnapshot = true
	if envSnap, err := core.BuildDeployEnv(acc); err != nil || envSnap["RESTORE_CUSTOM_NODES_SNAPSHOT"] != "1" {
		t.Fatalf("expected RESTORE_CUSTOM_NODES_SNAPSHOT=1 when enabled, got %+v err=%v", envSnap, err)
	}
	acc.RestoreCustomNodesSnapshot = false

	// Custom variant rejects command-injection repo / traversal paths
	acc.H3Variant = core.H3VariantCustom
	acc.H3DriveOnly = false
	acc.H3CustomHFRepo = "bad; rm -rf /"
	acc.H3CustomHFFiles = "../evil.safetensors"
	if _, err := core.BuildDeployEnv(acc); err == nil {
		t.Fatal("expected invalid custom HF repo/files to be rejected")
	}

	// Valid custom repo & Drive-only mode both pass cleanly
	acc.H3CustomHFRepo = "VideoRebirthAI/hyperflow-h3-flash"
	acc.H3CustomHFFiles = "fl2va_gguf/model.gguf, ref2va_gguf/model2.gguf"
	env2, err := core.BuildDeployEnv(acc)
	if err != nil || env2["H3_CUSTOM_HF_REPO"] != "VideoRebirthAI/hyperflow-h3-flash" {
		t.Fatalf("expected valid custom HF config, got %+v err=%v", env2, err)
	}

	acc.H3DriveOnly = true
	acc.H3CustomHFRepo = ""
	acc.H3CustomHFFiles = ""
	if env3, err := core.BuildDeployEnv(acc); err != nil || env3["H3_DRIVE_ONLY"] != "1" {
		t.Fatalf("expected drive-only custom mode to pass, got %+v err=%v", env3, err)
	}

	// Switching back to a built-in H3 preset must NOT carry over custom H3DriveOnly=1
	acc.H3Variant = core.H3VariantFlash
	if env4, err := core.BuildDeployEnv(acc); err != nil || env4["H3_DRIVE_ONLY"] != "0" {
		t.Fatalf("built-in H3 preset must force H3_DRIVE_ONLY=0, got %+v err=%v", env4, err)
	}

	// EffectiveIdleTimeoutMinutes stays consistent between UI display and watchdog execution
	rt := core.NewAccountRuntimeState()
	acc.IdleTimeoutMinutes = 40
	acc.QueueDrainMinutes = 10
	acc.StopAfterQueueDrained = true
	acc.EnableLLMModule = false
	if got := core.EffectiveIdleTimeoutMinutes(acc, rt); got != 40 {
		t.Fatalf("before queue busy, expected 40m, got %d", got)
	}
	rt.SawQueueBusyThisRun = true
	if got := core.EffectiveIdleTimeoutMinutes(acc, rt); got != 10 {
		t.Fatalf("after queue drained, expected 10m, got %d", got)
	}

	// Drive subpath sanitizer blocks traversal & shell metacharacters
	if got := core.SanitizeDriveSubpath("/content/drive/MyDrive/../../etc/passwd", "/content/drive/MyDrive/ColabTools/outputs"); got != "/content/drive/MyDrive/ColabTools/outputs" {
		t.Fatalf("traversal must fall back cleanly, got %q", got)
	}
}

func TestVRAMProtectionCrossModelFallbackAndEcoSaver(t *testing.T) {
	// Pro / AI Pro / PAYG VRAM protection under GPUStrategyAutoSmart
	for _, tier := range []core.SubscriptionTier{core.TierAIPro, core.TierColabPro, core.TierPayAsYouGo} {
		acc := core.NewDefaultAccountProfile("Pro", "p@example.com", tier, core.AuthSystemDefaultOAuth2)
		acc.GPUStrategy = core.GPUStrategyAutoSmart
		acc.DeployRecipe = core.RecipeH3Video

		// 31B Original: strictly [A100], no L4/T4; fallback disabled by default
		acc.H3Variant = core.H3VariantOriginal
		acc.AllowCrossModelFallback = false
		if got := core.EffectiveQueueTargets(acc); len(got) != 1 || got[0] != core.AccA100 {
			t.Fatalf("tier %s 31B must only queue [A100], got %v", tier, got)
		}
		if fbOn, after, target := core.EffectiveFallback(acc); fbOn || after != 2 || target != core.AccL4 {
			t.Fatalf("tier %s 31B default fallback = (%v,%d,%v), want (false,2,L4)", tier, fbOn, after, target)
		}

		// 31B Original with AllowCrossModelFallback=true: primary [A100], round 3+ adds L4
		acc.AllowCrossModelFallback = true
		if fbOn, after, target := core.EffectiveFallback(acc); !fbOn || after != 2 || target != core.AccL4 {
			t.Fatalf("tier %s 31B cross-model fallback = (%v,%d,%v), want (true,2,L4)", tier, fbOn, after, target)
		}
		r3 := core.QueueTargetsForRound(3, core.EffectiveQueueTargets(acc), true, 2, core.AccL4, tier)
		if len(r3) != 2 || r3[0] != core.AccA100 || r3[1] != core.AccL4 {
			t.Fatalf("tier %s 31B round 3 expected [A100, L4], got %v", tier, r3)
		}

		// H3 Flash: [A100, L4], never T4
		acc.H3Variant = core.H3VariantFlash
		if got := core.EffectiveQueueTargets(acc); len(got) != 2 || got[0] != core.AccA100 || got[1] != core.AccL4 {
			t.Fatalf("tier %s H3 Flash expected [A100, L4], got %v", tier, got)
		}
		if fbOn, _, _ := core.EffectiveFallback(acc); fbOn {
			t.Fatalf("tier %s H3 Flash must not fall back to T4", tier)
		}

		// H3 GGUF: [L4, A100], no T4 fallback
		acc.H3Variant = core.H3VariantFlashGGUF
		if got := core.EffectiveQueueTargets(acc); len(got) != 2 || got[0] != core.AccL4 || got[1] != core.AccA100 {
			t.Fatalf("tier %s GGUF expected [L4, A100], got %v", tier, got)
		}
		if fbOn, _, _ := core.EffectiveFallback(acc); fbOn {
			t.Fatalf("tier %s GGUF must disable fallback", tier)
		}

		// General ComfyUI / LLM Proxy / Pure Compute: [A100, L4] with T4 fallback
		for _, recipe := range []core.DeployRecipe{core.RecipeComfyUIGeneral, core.RecipeLLMProxy, core.RecipeNone} {
			acc.DeployRecipe = recipe
			if got := core.EffectiveQueueTargets(acc); len(got) != 2 || got[0] != core.AccA100 || got[1] != core.AccL4 {
				t.Fatalf("tier %s recipe %s expected [A100, L4], got %v", tier, recipe, got)
			}
			if fbOn, after, target := core.EffectiveFallback(acc); !fbOn || after != 2 || target != core.AccT4 {
				t.Fatalf("tier %s recipe %s expected fallback (true,2,T4), got (%v,%d,%v)", tier, recipe, fbOn, after, target)
			}
		}
	}

	// Free tier under AutoSmart
	freeAcc := core.NewDefaultAccountProfile("Free", "f@example.com", core.TierFree, core.AuthSystemDefaultOAuth2)
	freeAcc.GPUStrategy = core.GPUStrategyAutoSmart
	if got := core.EffectiveQueueTargets(freeAcc); len(got) != 1 || got[0] != core.AccT4 {
		t.Fatalf("free tier expected [T4], got %v", got)
	}
	if fbOn, _, target := core.EffectiveFallback(freeAcc); fbOn || target != core.AccT4 {
		t.Fatalf("free tier fallback expected (false, _, T4), got (%v, %v)", fbOn, target)
	}

	// EcoSaver VRAM protection
	ultraEco := core.NewDefaultAccountProfile("UltraEco", "ue@example.com", core.TierAIUltra, core.AuthSystemDefaultOAuth2)
	ultraEco.GPUStrategy = core.GPUStrategyEcoSaver
	ultraEco.DeployRecipe = core.RecipeH3Video
	ultraEco.H3Variant = core.H3VariantOriginal
	if got := core.EffectiveQueueTargets(ultraEco); len(got) != 2 || got[0] != core.AccA100 || got[1] != core.AccG4 {
		t.Fatalf("Ultra eco 31B expected [A100, G4], got %v", got)
	}
	proEco := core.NewDefaultAccountProfile("ProEco", "pe@example.com", core.TierColabPro, core.AuthSystemDefaultOAuth2)
	proEco.GPUStrategy = core.GPUStrategyEcoSaver
	proEco.DeployRecipe = core.RecipeH3Video
	proEco.H3Variant = core.H3VariantOriginal
	if got := core.EffectiveQueueTargets(proEco); len(got) != 1 || got[0] != core.AccA100 {
		t.Fatalf("Pro eco 31B expected [A100], got %v", got)
	}
	proEco.H3Variant = core.H3VariantFlash
	if got := core.EffectiveQueueTargets(proEco); len(got) != 1 || got[0] != core.AccL4 {
		t.Fatalf("Pro eco Flash expected [L4], got %v", got)
	}
	proEco.H3Variant = core.H3VariantFlashGGUF
	if got := core.EffectiveQueueTargets(proEco); len(got) != 1 || got[0] != core.AccL4 {
		t.Fatalf("Pro eco GGUF expected [L4], got %v", got)
	}
	proEco.DeployRecipe = core.RecipeComfyUIGeneral
	if got := core.EffectiveQueueTargets(proEco); len(got) != 2 || got[0] != core.AccL4 || got[1] != core.AccT4 {
		t.Fatalf("Pro eco ComfyUI expected [L4, T4], got %v", got)
	}
	freeAcc.GPUStrategy = core.GPUStrategyEcoSaver
	if got := core.EffectiveQueueTargets(freeAcc); len(got) != 1 || got[0] != core.AccT4 {
		t.Fatalf("Free eco expected [T4], got %v", got)
	}

	// DefaultFallbackTarget must never select H100/A100 when L4 is already in primary list
	if got := core.DefaultFallbackTarget([]core.AcceleratorOption{core.AccL4, core.AccG4, core.AccA100}, core.TierAIUltra); got != core.AccT4 {
		t.Fatalf("DefaultFallbackTarget with L4 present on Ultra must return T4, got %v", got)
	}
	if got := core.DefaultFallbackTarget([]core.AcceleratorOption{core.AccL4, core.AccT4}, core.TierColabPro); got != core.AccT4 {
		t.Fatalf("DefaultFallbackTarget with L4+T4 present on Pro must return T4 (never A100), got %v", got)
	}
	if got := core.DefaultFallbackTarget([]core.AcceleratorOption{core.AccG4, core.AccA100}, core.TierAIUltra); got != core.AccL4 {
		t.Fatalf("DefaultFallbackTarget with G4+A100 on Ultra must return L4, got %v", got)
	}

	// IsSmallVRAMHardware & ShouldCrossModelDowngrade
	if !core.IsSmallVRAMHardware("NVIDIA L4") || !core.IsSmallVRAMHardware("Tesla T4") || core.IsSmallVRAMHardware("NVIDIA A100") {
		t.Fatal("IsSmallVRAMHardware classification mismatch")
	}
	acc31B := core.NewDefaultAccountProfile("31B", "31b@example.com", core.TierAIUltra, core.AuthSystemDefaultOAuth2)
	acc31B.DeployRecipe = core.RecipeH3Video
	acc31B.H3Variant = core.H3VariantOriginal
	acc31B.AllowCrossModelFallback = false
	if core.ShouldCrossModelDowngrade(acc31B, "L4") {
		t.Fatal("ShouldCrossModelDowngrade must be false when AllowCrossModelFallback=false")
	}
	acc31B.AllowCrossModelFallback = true
	if !core.ShouldCrossModelDowngrade(acc31B, "L4") || !core.ShouldCrossModelDowngrade(acc31B, "T4") {
		t.Fatal("ShouldCrossModelDowngrade must be true on L4/T4 when AllowCrossModelFallback=true")
	}
	if core.ShouldCrossModelDowngrade(acc31B, "A100") || core.ShouldCrossModelDowngrade(acc31B, "G4") {
		t.Fatal("ShouldCrossModelDowngrade must be false on A100/G4")
	}
	acc31B.EnableLLMModule = true
	if core.ShouldCrossModelDowngrade(acc31B, "L4") {
		t.Fatal("ShouldCrossModelDowngrade must be false when EnableLLMModule=true")
	}
}

func TestNextQueueDelayForModeAndPersistentHours(t *testing.T) {
	now := time.Date(2026, 11, 26, 2, 0, 0, 0, time.Local)

	// EffectivePersistentHours normalizes invalid/default to 8 and clamps to 1..24
	acc := core.NewDefaultAccountProfile("Persist", "p@example.com", core.TierAIUltra, core.AuthSystemDefaultOAuth2)
	if acc.QueuePersistentMode {
		t.Fatal("QueuePersistentMode must default to false")
	}
	if got := core.EffectivePersistentHours(acc); got != 8 {
		t.Fatalf("expected default persistent hours 8, got %d", got)
	}
	acc.QueuePersistentMaxHours = -3
	if got := core.EffectivePersistentHours(acc); got != 8 {
		t.Fatalf("expected <=0 persistent hours to normalize to 8, got %d", got)
	}
	acc.QueuePersistentMaxHours = 99
	if got := core.EffectivePersistentHours(acc); got != 24 {
		t.Fatalf("expected >24 persistent hours to clamp to 24, got %d", got)
	}
	acc.QueuePersistentMaxHours = 12
	if got := core.EffectivePersistentHours(acc); got != 12 {
		t.Fatalf("expected 12 persistent hours, got %d", got)
	}

	// Standard mode (!persistent) stays capped at 5 rounds / 40 minutes
	dStd, ok := core.NextQueueDelayForMode(1, now, now, 0.5, false, 8)
	if !ok || dStd != 135*time.Second {
		t.Fatalf("standard mode round 1 expected 135s, got (%v, %v)", dStd, ok)
	}
	if _, ok := core.NextQueueDelayForMode(core.QueueMaxAttempts, now, now, 0.5, false, 8); ok {
		t.Fatal("standard mode must stop at QueueMaxAttempts")
	}
	if _, ok := core.NextQueueDelayForMode(1, now.Add(-39*time.Minute), now, 1.0, false, 8); ok {
		t.Fatal("standard mode must stop past 40m")
	}

	// Persistent mode: rounds 1..3 keep 3m/6m/9m Equal Jitter, rounds 4+ strictly stay in 8~12m (480s..720s)
	d1Min, ok := core.NextQueueDelayForMode(1, now, now, 0.0, true, 8)
	if !ok || d1Min != 90*time.Second {
		t.Fatalf("persistent round 1 min expected 90s, got (%v, %v)", d1Min, ok)
	}
	d3Max, ok := core.NextQueueDelayForMode(3, now, now, 1.0, true, 8)
	if !ok || d3Max != 540*time.Second {
		t.Fatalf("persistent round 3 max expected 540s, got (%v, %v)", d3Max, ok)
	}
	for _, attempt := range []int{4, 5, 6, 15} {
		dMin, okMin := core.NextQueueDelayForMode(attempt, now, now, 0.0, true, 8)
		dMax, okMax := core.NextQueueDelayForMode(attempt, now, now, 1.0, true, 8)
		dMid, okMid := core.NextQueueDelayForMode(attempt, now, now, 0.5, true, 8)
		if !okMin || dMin != 480*time.Second {
			t.Fatalf("attempt %d min expected 480s (8m), got (%v, %v)", attempt, dMin, okMin)
		}
		if !okMax || dMax != 720*time.Second {
			t.Fatalf("attempt %d max expected 720s (12m), got (%v, %v)", attempt, dMax, okMax)
		}
		if !okMid || dMid != 600*time.Second {
			t.Fatalf("attempt %d mid expected 600s (10m), got (%v, %v)", attempt, dMid, okMid)
		}
	}

	// Persistent 8h cap & wake-from-sleep resilience (checks total elapsed, stops cleanly past cap)
	if _, ok := core.NextQueueDelayForMode(10, now.Add(-7*time.Hour-55*time.Minute), now, 1.0, true, 8); ok {
		t.Fatal("persistent mode must stop when elapsed + delay exceeds 8h")
	}
	if d, ok := core.NextQueueDelayForMode(10, now.Add(-7*time.Hour-40*time.Minute), now, 0.0, true, 8); !ok || d != 480*time.Second {
		t.Fatalf("persistent mode within 8h expected 480s, got (%v, %v)", d, ok)
	}
}

func TestSettingsSchemaVersionAndActiveLedgerPersistence(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	// Legacy config without schemaVersion loads cleanly and upgrades to CurrentConfigSchemaVersion on save
	legacyJSON := `{"accounts":[],"backgroundRefreshSeconds":180,"manualRefreshDebounceSeconds":5,"enableNotifications":true,"themePreference":"dark"}`
	if err := os.WriteFile(cfgPath, []byte(legacyJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded := core.LoadAppSettings(cfgPath)
	if loaded.SchemaVersion != core.CurrentConfigSchemaVersion {
		t.Fatalf("expected upgraded schemaVersion=%d, got %d", core.CurrentConfigSchemaVersion, loaded.SchemaVersion)
	}
	if err := loaded.Save(cfgPath); err != nil {
		t.Fatalf("save settings failed: %v", err)
	}
	rawSaved, err := os.ReadFile(cfgPath)
	if err != nil || !strings.Contains(string(rawSaved), `"schemaVersion": 1`) {
		t.Fatalf("expected persisted schemaVersion=1, got %q err=%v", string(rawSaved), err)
	}

	// ActiveSessionLedger round-trip and corrupt fallback
	ledgerPath := filepath.Join(dir, "active_sessions.json")
	started := time.Now().Add(-25 * time.Minute).UTC().Truncate(time.Second)
	entries := map[string]core.ActiveSessionLedgerEntry{
		"acc-1": {
			AccountID:          "acc-1",
			AccountName:        "Ultra",
			SessionName:        "colab-tools",
			Endpoint:           "m-s-ledger-1",
			Hardware:           "A100",
			StartedAt:          started,
			StartBalance:       400.0,
			StartBalanceValid:  true,
			MeasuredSingleRate: 12.0,
			EstimatedCU:        5.0,
			LastSampleAt:       started.Add(25 * time.Minute),
			LastUptimeSec:      1500,
		},
	}
	if err := core.SaveActiveLedger(ledgerPath, entries); err != nil {
		t.Fatalf("SaveActiveLedger failed: %v", err)
	}
	got := core.LoadActiveLedger(ledgerPath)
	if len(got) != 1 || got["acc-1"].Endpoint != "m-s-ledger-1" || got["acc-1"].EstimatedCU != 5.0 {
		t.Fatalf("unexpected loaded active ledger: %+v", got)
	}

	// Corrupt ledger is backed up and returns empty map safely
	if err := os.WriteFile(ledgerPath, []byte("{broken json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if gotCorrupt := core.LoadActiveLedger(ledgerPath); len(gotCorrupt) != 0 {
		t.Fatalf("expected empty map on corrupt ledger, got %+v", gotCorrupt)
	}
}
