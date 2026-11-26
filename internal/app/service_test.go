package app

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LJMStark/Colab-Tools/internal/colab"
	"github.com/LJMStark/Colab-Tools/internal/core"
	"github.com/LJMStark/Colab-Tools/internal/runner"
)

type fakeRunner struct {
	mu       sync.Mutex
	onShell  func(cmd string) runner.Result
	onRun    func(name string, args []string) runner.Result
	commands []string
}

func (f *fakeRunner) Run(_ context.Context, name string, args []string, _ runner.RunOptions) runner.Result {
	f.mu.Lock()
	f.commands = append(f.commands, name+" "+strings.Join(args, " "))
	fn := f.onRun
	f.mu.Unlock()
	if fn != nil {
		return fn(name, args)
	}
	return runner.Result{ExitCode: 0}
}

func (f *fakeRunner) RunShell(_ context.Context, shellCmd string, _ runner.RunOptions) runner.Result {
	f.mu.Lock()
	f.commands = append(f.commands, shellCmd)
	fn := f.onShell
	f.mu.Unlock()
	if fn != nil {
		return fn(shellCmd)
	}
	return runner.Result{ExitCode: 0}
}

func (f *fakeRunner) RunStreamingShell(_ context.Context, _ string, _ time.Duration, _ func(string)) runner.Result {
	return runner.Result{ExitCode: 0}
}

func (f *fakeRunner) LookPath(_ string) string { return "/usr/local/bin/colab" }

func TestPhaseSettlementPostAllocAndStopVerification(t *testing.T) {
	svc := NewService(true)
	acc := core.NewDefaultAccountProfile("Primary", "primary@example.com", core.TierAIUltra, core.AuthSystemDefaultOAuth2)
	acc.DeployRecipe = core.RecipeNone
	acc.EnableLLMModule = false
	acc.DriveScriptPath = ""
	svc.Settings.Accounts = []core.AccountProfile{acc}
	svc.Settings.SelectedAccountID = acc.ID
	rt := core.NewAccountRuntimeState()
	rt.Phase = core.InstancePhase{Kind: core.PhaseStarting, Detail: "申请中"}
	svc.Runtime[acc.ID] = rt

	fr := &fakeRunner{}
	serverAlive := true
	verifyFail := false
	fr.onShell = func(cmd string) runner.Result {
		switch {
		case strings.Contains(cmd, "usage"):
			if verifyFail {
				return runner.Result{ExitCode: 1, Stderr: "network timeout"}
			}
			return runner.Result{ExitCode: 0, Stdout: "Current balance: 100.00 compute units\nUsage rate: 2.33/hr\nActive assignments: 1\n"}
		case strings.Contains(cmd, "sessions"):
			if verifyFail {
				return runner.Result{ExitCode: 1, Stderr: "network timeout"}
			}
			if serverAlive {
				return runner.Result{ExitCode: 0, Stdout: "[" + acc.SessionName + "] m-s-abc123 | Hardware: L4 | Shape: Standard | Variant: GPU\n"}
			}
			return runner.Result{ExitCode: 0, Stdout: "No active sessions.\n"}
		case strings.Contains(cmd, "rm") || strings.Contains(cmd, "stop"):
			serverAlive = false
			return runner.Result{ExitCode: 0}
		default:
			return runner.Result{ExitCode: 0}
		}
	}
	svc.Engine = colab.NewEngine(fr, svc.Vault)

	// 1. handlePostAllocation must settle PhaseStarting -> PhaseRunning
	svc.handlePostAllocation(acc, "L4")
	got := svc.StateFor(acc.ID)
	if !got.Phase.IsRunning() || got.ActiveEndpoint != "m-s-abc123" {
		t.Fatalf("expected PhaseStarting to settle into PhaseRunning, got phase=%+v ep=%q", got.Phase, got.ActiveEndpoint)
	}

	// 2. StopAccountSession when post-stop verification fails must NOT stay stuck in PhaseStopping or claim confirmed stop
	verifyFail = true
	var lastToast string
	svc.NotifySender = func(_, _ string) {}
	svc.StopAccountSession(acc.ID, "", "")
	got = svc.StateFor(acc.ID)
	svc.mu.RLock()
	lastToast = svc.ToastMessage
	svc.mu.RUnlock()
	if got.Phase.Kind == core.PhaseStopping {
		t.Fatalf("must not remain stuck in PhaseStopping when verification fails: %+v", got.Phase)
	}
	if !strings.Contains(lastToast, "未能") {
		t.Fatalf("expected unverified warning toast, got %q", lastToast)
	}

	// 3. StopAccountSession when post-stop verification succeeds must settle cleanly to PhaseOffline
	verifyFail = false
	svc.StopAccountSession(acc.ID, "", "")
	got = svc.StateFor(acc.ID)
	if got.Phase.Kind != core.PhaseOffline {
		t.Fatalf("expected PhaseOffline after verified stop, got %+v", got.Phase)
	}
}

func TestIdleTickOnlyCountsFreshIdleEvidenceAndInsightsSnapshotIsRaceFree(t *testing.T) {
	svc := NewService(true)
	acc := core.NewDefaultAccountProfile("Watchdog", "wd@example.com", core.TierColabPro, core.AuthSystemDefaultOAuth2)
	acc.AutoShutdownEnabled = true
	acc.IdleTimeoutMinutes = 30
	svc.Settings.Accounts = []core.AccountProfile{acc}

	now := time.Now()
	rt := core.NewAccountRuntimeState()
	rt.Phase = core.InstancePhase{Kind: core.PhaseRunning}
	rt.ActivitySignal = core.ActivitySignal{Kind: core.SignalIdle, ObservedAt: now.Add(-10 * time.Minute)} // stale
	svc.Runtime[acc.ID] = rt

	svc.handleOneSecondTick(now)
	if svc.StateFor(acc.ID).IdleSeconds != 0 {
		t.Fatalf("stale idle signal must NOT increment IdleSeconds, got %d", svc.StateFor(acc.ID).IdleSeconds)
	}

	// Fresh idle signal SHOULD increment IdleSeconds
	svc.mu.Lock()
	cur := svc.Runtime[acc.ID]
	cur.ActivitySignal = core.ActivitySignal{Kind: core.SignalIdle, ObservedAt: now.Add(-10 * time.Second)}
	svc.Runtime[acc.ID] = cur
	svc.mu.Unlock()
	svc.handleOneSecondTick(now)
	if svc.StateFor(acc.ID).IdleSeconds != 1 {
		t.Fatalf("fresh idle signal must increment IdleSeconds to 1, got %d", svc.StateFor(acc.ID).IdleSeconds)
	}

	// Concurrent Insights writes + InsightsSnapshot reads must be race-free under -race
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			svc.recordProbeInsights([]colab.ProbeAttemptRecord{{Hardware: "G4", Succeeded: i%2 == 0}})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			_, _, _ = svc.InsightsSnapshot("G4")
		}
	}()
	wg.Wait()
}

func TestVideoQueueDrainFastAutoShutdownAndDeployRecipeSync(t *testing.T) {
	svc := NewService(true)
	acc := core.NewDefaultAccountProfile("H3User", "h3@example.com", core.TierAIUltra, core.AuthSystemDefaultOAuth2)
	acc.IdleTimeoutMinutes = 40
	acc.QueueDrainMinutes = 10
	acc.StopAfterQueueDrained = true
	acc.AutoShutdownEnabled = true
	svc.Settings.Accounts = []core.AccountProfile{acc}

	// Switching recipes keeps ports & workload kind aligned cleanly
	svc.ApplyDeployRecipe(acc.ID, core.RecipeLLMProxy)
	gotAcc, _ := svc.accountByID(acc.ID)
	if !gotAcc.EnableLLMModule || gotAcc.RemoteServicePort != 18080 {
		t.Fatalf("expected LLM proxy recipe alignment, got %+v", gotAcc)
	}
	svc.ApplyDeployRecipe(acc.ID, core.RecipeH3Video)
	gotAcc, _ = svc.accountByID(acc.ID)
	if gotAcc.EnableLLMModule || gotAcc.WorkloadKind != core.WorkloadVideo || gotAcc.RemoteServicePort != 8188 {
		t.Fatalf("expected H3 video recipe alignment, got %+v", gotAcc)
	}

	// Once video queue was busy and then drains to 0/0, 10m idle triggers fast shutdown even though general IdleTimeout is 40m
	var stopped atomicStopTracker
	fr := &fakeRunner{}
	fr.onShell = func(cmd string) runner.Result {
		if strings.Contains(cmd, "stop") || strings.Contains(cmd, "rm") {
			stopped.mark()
		}
		if strings.Contains(cmd, "usage") {
			return runner.Result{ExitCode: 0, Stdout: "Current balance: 100.00 compute units\nUsage rate: 0.00/hr\nActive assignments: 0\n"}
		}
		return runner.Result{ExitCode: 0, Stdout: "No active sessions.\n"}
	}
	svc.Engine = colab.NewEngine(fr, svc.Vault)

	now := time.Now()
	rt := core.NewAccountRuntimeState()
	rt.Phase = core.InstancePhase{Kind: core.PhaseRunning}
	rt.SawQueueBusyThisRun = true
	rt.QueueRunning = 0
	rt.QueuePending = 0
	rt.IdleSeconds = 10*60 - 1 // 1 second before 10m drain timeout
	rt.ActivitySignal = core.ActivitySignal{Kind: core.SignalIdle, ObservedAt: now.Add(-5 * time.Second)}
	rt.SignalSource = core.SignalSourceComfyQueue
	svc.Runtime[acc.ID] = rt

	svc.handleOneSecondTick(now)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !stopped.didStop() {
		time.Sleep(15 * time.Millisecond)
	}
	if !stopped.didStop() {
		t.Fatal("expected fast auto-shutdown after ComfyUI queue drained for QueueDrainMinutes")
	}
}

type atomicStopTracker struct {
	mu      sync.Mutex
	stopped bool
}

func (a *atomicStopTracker) mark() {
	a.mu.Lock()
	a.stopped = true
	a.mu.Unlock()
}

func (a *atomicStopTracker) didStop() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.stopped
}

func TestProvisionAutoSkips400AndEmpty412AndManualCooldownClear(t *testing.T) {
	svc := NewService(true)
	acc := core.NewDefaultAccountProfile("SmartUser", "smart@example.com", core.TierAIUltra, core.AuthSystemDefaultOAuth2)
	acc.GPUStrategy = core.GPUStrategyCustom
	acc.QueueTargets = []core.AcceleratorOption{core.AccG4, core.AccA100, core.AccL4}
	acc.DeployRecipe = core.RecipeNone
	svc.Settings.Accounts = []core.AccountProfile{acc}
	svc.Settings.SelectedAccountID = acc.ID
	svc.Runtime[acc.ID] = core.NewAccountRuntimeState()

	// Case 1: G4 hits google-colab-cli 0.8.0 "Allocation refused (precondition failed)" with 0 active sessions,
	// A100 hits "Backend rejected accelerator 'A100'" -> engine must auto-skip both and allocate L4 without any cooldown!
	fr := &fakeRunner{}
	fr.onShell = func(cmd string) runner.Result {
		switch {
		case strings.Contains(cmd, "--gpu") && strings.Contains(cmd, "G4"):
			return runner.Result{ExitCode: 1, Stderr: "[colab] Allocation refused (precondition failed). This can mean too many active sessions, or a temporary usage or capacity limit."}
		case strings.Contains(cmd, "--gpu") && strings.Contains(cmd, "A100"):
			return runner.Result{ExitCode: 1, Stderr: "[colab] Backend rejected accelerator 'A100'. You may not have quota or entitlement."}
		case strings.Contains(cmd, "--gpu") && strings.Contains(cmd, "L4"):
			return runner.Result{ExitCode: 0, Stdout: "Created session.\n"}
		case strings.Contains(cmd, "usage"):
			return runner.Result{ExitCode: 0, Stdout: "Current balance: 1000.00 compute units\nUsage rate: 0.00/hr\nActive assignments: 0\n"}
		case strings.Contains(cmd, "sessions"):
			return runner.Result{ExitCode: 0, Stdout: "No active sessions.\n"}
		default:
			return runner.Result{ExitCode: 0}
		}
	}
	svc.Engine = colab.NewEngine(fr, svc.Vault)

	res := svc.Engine.ProvisionSession(context.Background(), acc, nil, nil, nil, nil)
	if res.Outcome.Kind != core.OutcomeAllocated || res.Outcome.Hardware != "L4" {
		t.Fatalf("expected auto-fallback to L4 after empty-412 G4 and 400 A100, got %+v", res.Outcome)
	}
	if !res.UnsupportedValues["A100"] {
		t.Fatalf("expected A100 to be pruned as unsupported, got %+v", res.UnsupportedValues)
	}

	// Case 2: Non-rate-limit errors (timeout / 412 with active session / 401) must NEVER impose a 15-minute cooldown lock
	svc.NotifySender = func(_, _ string) {}
	svc.handleProvisionFailure(acc, core.ProvisionOutcome{Kind: core.OutcomeTransientError, Code: "TIMEOUT", Message: "超时"})
	if rt := svc.StateFor(acc.ID); rt.QueueCircuitBreakTill != nil {
		t.Fatalf("timeout must not set cooldown lock: %+v", rt.QueueCircuitBreakTill)
	}
	svc.handleProvisionFailure(acc, core.ProvisionOutcome{Kind: core.OutcomePrecondition412, Code: "412", Message: "已有运行中机器"})
	if rt := svc.StateFor(acc.ID); rt.QueueCircuitBreakTill != nil || rt.LastErrorAction != "stop_existing" {
		t.Fatalf("412 must offer stop_existing without 15m lock, got till=%v action=%q", rt.QueueCircuitBreakTill, rt.LastErrorAction)
	}

	// Case 3: True 429 triggers cooldown, and ClearCooldown clears it immediately
	svc.handleProvisionFailure(acc, core.ProvisionOutcome{Kind: core.OutcomeRateLimited, Code: "429", Message: "请求偏频繁"})
	if rt := svc.StateFor(acc.ID); rt.QueueCircuitBreakTill == nil || !rt.QueueCircuitBreakTill.After(time.Now()) {
		t.Fatalf("429 must trigger cooldown, got %+v", rt.QueueCircuitBreakTill)
	}
	svc.ClearCooldown(acc.ID)
	if rt := svc.StateFor(acc.ID); rt.QueueCircuitBreakTill != nil || rt.LastError != "" {
		t.Fatalf("ClearCooldown must clear lock & error banner, got till=%v err=%q", rt.QueueCircuitBreakTill, rt.LastError)
	}

	// Case 4: ResetAccountToRecommendedDefaults restores H3 Flash + Smart GPU picking cleanly
	svc.ResetAccountToRecommendedDefaults(acc.ID)
	resetAcc, _ := svc.accountByID(acc.ID)
	if resetAcc.GPUStrategy != core.GPUStrategyAutoSmart || resetAcc.DeployRecipe != core.RecipeH3Video || resetAcc.H3Variant != core.H3VariantFlash {
		t.Fatalf("expected clean beginner defaults after reset, got %+v", resetAcc)
	}
}
