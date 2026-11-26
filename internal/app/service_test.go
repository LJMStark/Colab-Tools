package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LJMStark/Colab-Tools/internal/colab"
	"github.com/LJMStark/Colab-Tools/internal/core"
	"github.com/LJMStark/Colab-Tools/internal/runner"
)

type fakeRunner struct {
	mu          sync.Mutex
	onShell     func(cmd string) runner.Result
	onShellOpts func(cmd string, opts runner.RunOptions) runner.Result
	onRun       func(name string, args []string) runner.Result
	commands    []string
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

func (f *fakeRunner) RunShell(_ context.Context, shellCmd string, opts runner.RunOptions) runner.Result {
	f.mu.Lock()
	f.commands = append(f.commands, shellCmd)
	fnOpts := f.onShellOpts
	fn := f.onShell
	f.mu.Unlock()
	if fnOpts != nil {
		return fnOpts(shellCmd, opts)
	}
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
	svc.handlePostAllocation(acc, "L4", false, false)
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

func TestFailureNotificationsTestNotificationAndTrayLiveStatus(t *testing.T) {
	svc := NewService(true)
	svc.Settings.EnableNotifications = false // critical alerts & test notifications must still fire
	acc := core.NewDefaultAccountProfile("VideoUser", "video@example.com", core.TierAIUltra, core.AuthSystemDefaultOAuth2)
	svc.Settings.Accounts = []core.AccountProfile{acc}
	svc.Settings.SelectedAccountID = acc.ID
	svc.Runtime[acc.ID] = core.NewAccountRuntimeState()

	fr := &fakeRunner{}
	fr.onShell = func(cmd string) runner.Result {
		if strings.Contains(cmd, "usage") {
			return runner.Result{ExitCode: 0, Stdout: "Current balance: 100.00 compute units\nUsage rate: 2.33/hr\nActive assignments: 1\n"}
		}
		if strings.Contains(cmd, "sessions") {
			return runner.Result{ExitCode: 0, Stdout: "[" + acc.SessionName + "] m-s-pure1 | Hardware: L4 | Shape: Standard | Variant: GPU\n"}
		}
		return runner.Result{ExitCode: 0}
	}
	svc.Engine = colab.NewEngine(fr, svc.Vault)

	type sentNotice struct {
		title string
		body  string
	}
	var mu sync.Mutex
	var notices []sentNotice
	svc.NotifySender = func(title, body string) {
		mu.Lock()
		notices = append(notices, sentNotice{title: title, body: body})
		mu.Unlock()
	}

	// 1. User cancellation stays silent; all failure endings notify critically
	svc.handleProvisionFailure(acc, core.ProvisionOutcome{Kind: core.OutcomeTransientError, Code: "CANCELLED", Message: "已取消"})
	mu.Lock()
	if len(notices) != 0 {
		t.Fatalf("cancelled outcome must not notify, got %+v", notices)
	}
	mu.Unlock()

	failureCases := []core.ProvisionOutcome{
		{Kind: core.OutcomeBudgetExhausted, Code: "BUDGET", Message: "已达 5 轮 / 40 分钟合规排队上限，暂未拿到目标卡"},
		{Kind: core.OutcomeTransientError, Code: "TIMEOUT", Message: "本次申请超时未响应"},
		{Kind: core.OutcomePrecondition412, Code: "412", Message: "已有运行中机器，请先关机再试"},
		{Kind: core.OutcomeAuthError, Code: "401", Message: "授权已过期，请重新登录"},
		{Kind: core.OutcomeRateLimited, Code: "429", Message: "请求偏频繁，先冷静等一下"},
	}
	for _, fc := range failureCases {
		svc.handleProvisionFailure(acc, fc)
	}
	mu.Lock()
	if len(notices) != len(failureCases) {
		t.Fatalf("expected %d failure notifications, got %d: %+v", len(failureCases), len(notices), notices)
	}
	for i, fc := range failureCases {
		if notices[i].title == "" || notices[i].body != fc.Message {
			t.Fatalf("unexpected notification[%d]: got %+v want body %q", i, notices[i], fc.Message)
		}
	}
	mu.Unlock()

	// 2. TrayLiveStatus reflects focus error when idle, queuing, deploying, and running
	stErr := svc.TrayLiveStatus()
	if !stErr.FocusHasError || stErr.Queuing || stErr.Deploying || stErr.RunningCount != 0 {
		t.Fatalf("expected FocusHasError in tray status, got %+v", stErr)
	}

	svc.mu.Lock()
	rt := svc.Runtime[acc.ID]
	rt.Phase = core.InstancePhase{Kind: core.PhaseQueuing, QueueAttempt: 2, QueueMax: 5, TargetsSummary: "G4 → A100 → L4"}
	svc.Runtime[acc.ID] = rt
	svc.mu.Unlock()
	stQueue := svc.TrayLiveStatus()
	if !stQueue.Queuing || stQueue.QueueAttempt != 2 || stQueue.QueueMax != 5 || stQueue.TargetsSummary != "G4 → A100 → L4" {
		t.Fatalf("expected queuing tray status, got %+v", stQueue)
	}

	svc.mu.Lock()
	rt = svc.Runtime[acc.ID]
	rt.Phase = core.InstancePhase{Kind: core.PhaseRunning}
	rt.ActiveHardware = "G4"
	rt.DeployRunning = true
	svc.Runtime[acc.ID] = rt
	svc.mu.Unlock()
	stDeploy := svc.TrayLiveStatus()
	if !stDeploy.Deploying || stDeploy.HardwareLabel != "G4" || stDeploy.RunningCount != 1 {
		t.Fatalf("expected deploying G4 tray status, got %+v", stDeploy)
	}

	// 3. SendTestNotification triggers NotifySender & sets toast
	mu.Lock()
	notices = nil
	mu.Unlock()
	svc.SendTestNotification()
	mu.Lock()
	if len(notices) != 1 || notices[0].title != "Colab Tools 提醒测试" || !strings.Contains(notices[0].body, "通知正常") {
		t.Fatalf("unexpected test notification: %+v", notices)
	}
	mu.Unlock()
	svc.mu.RLock()
	toast := svc.ToastMessage
	svc.mu.RUnlock()
	if !strings.Contains(toast, "已发送测试通知") {
		t.Fatalf("expected test notification toast, got %q", toast)
	}

	// 4. handlePostAllocation sends critical notifications with accurate wording for pure-boot vs auto-deploy
	pureAcc := acc
	pureAcc.DeployRecipe = core.RecipeNone
	pureAcc.EnableLLMModule = false
	pureAcc.WorkloadKind = core.WorkloadGeneral
	pureAcc.DriveScriptPath = ""
	mu.Lock()
	notices = nil
	mu.Unlock()
	svc.handlePostAllocation(pureAcc, "L4", false, false)
	mu.Lock()
	if len(notices) != 1 || notices[0].title != "抢到 L4 了！" || !strings.Contains(notices[0].body, "已开机就绪") {
		t.Fatalf("unexpected pure-boot post-alloc notice: %+v", notices)
	}
	mu.Unlock()
}

func TestDeployTruthfulnessAndOpenComfyUIGuards(t *testing.T) {
	t.Parallel()
	telStdout := strings.Join([]string{
		"NV=NVIDIA L4, 0, 23034, 0",
		"CUDA_PROCS=0",
		"CPU=1",
		"UPTIME=3720",
		"STATUS_AGE=10",
		`STATUS_JSON={"kind":"video","name":"H3 原版 (31B)","port":8188,"metric_label":"部署中断","metric_value":"失败","metric_unit":"请检查","metric_ratio":0.0,"busy":false,"detail":"安装必需节点失败"}`,
		"HEALTH=",
		"DEPLOY_ALIVE=0",
		"OUT_VIDEOS=0",
	}, "\n")
	fr := &fakeRunner{
		onRun: func(name string, args []string) runner.Result {
			joined := strings.Join(args, " ")
			if strings.Contains(joined, "exec") {
				return runner.Result{ExitCode: 0, Stdout: telStdout}
			}
			return runner.Result{ExitCode: 0}
		},
		onShellOpts: func(cmd string, opts runner.RunOptions) runner.Result {
			if strings.Contains(cmd, "exec") {
				if strings.Contains(opts.Stdin, "DEPLOY_LAUNCHED") {
					return runner.Result{ExitCode: 0, Stdout: "DEPLOY_LAUNCHED=1\n"}
				}
				return runner.Result{ExitCode: 0, Stdout: telStdout}
			}
			return runner.Result{ExitCode: 0}
		},
	}
	svc := NewService(true)
	defer svc.Shutdown()
	svc.Engine = colab.NewEngine(fr, svc.Vault)
	acc := core.NewDefaultAccountProfile("VideoUser", "video@example.com", core.TierAIUltra, core.AuthSystemDefaultOAuth2)
	acc.DeployRecipe = core.RecipeH3Video
	acc.H3Variant = core.H3VariantOriginal
	acc.UseGoogleDrive = false
	svc.Settings.Accounts = []core.AccountProfile{acc}
	svc.Settings.SelectedAccountID = acc.ID

	var mu sync.Mutex
	var notices []string
	svc.NotifySender = func(title, body string) {
		mu.Lock()
		notices = append(notices, title+":"+body)
		mu.Unlock()
	}

	// 1. Bare running L4 machine (uninstalled) -> OpenComfyUI automatically kicks off deploy instead of just hinting
	rt := core.NewAccountRuntimeState()
	rt.Phase = core.InstancePhase{Kind: core.PhaseRunning}
	rt.ActiveEndpoint = "ep-l4-bare"
	rt.ActiveHardware = "L4"
	rt.GPUName = "NVIDIA L4"
	rt.VRAMTotalMB = 23034
	rt.ServerHealthy = false
	rt.DeployRunning = false
	svc.Runtime[acc.ID] = rt

	svc.OpenComfyUI(acc.ID)
	svc.mu.RLock()
	bareToast := svc.ToastMessage
	svc.mu.RUnlock()
	if !strings.Contains(bareToast, "自动安装 ComfyUI") {
		t.Fatalf("expected bare-machine OpenComfyUI to auto-start install, got %q", bareToast)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !svc.StateFor(acc.ID).DeployRunning {
		time.Sleep(15 * time.Millisecond)
	}
	if !svc.StateFor(acc.ID).DeployRunning {
		t.Fatalf("expected bare-machine OpenComfyUI to set DeployRunning=true, got %+v", svc.StateFor(acc.ID))
	}
	svc.cancelDeployWatcher(acc.ID)
	if hasWarn, warn := rt.HasVRAMWarningForRecipe(acc); !hasWarn || !strings.Contains(warn, "轻量 GGUF") {
		t.Fatalf("expected L4+31B VRAM warning, got (%v, %q)", hasWarn, warn)
	}

	// 2. Actively deploying -> OpenComfyUI reports live percentage instead of opening tunnel
	svc.mu.Lock()
	rt = svc.Runtime[acc.ID]
	rt.DeployRunning = true
	rt.WorkloadMetricValue = "35%"
	rt.WorkloadMetricUnit = "装环境"
	svc.Runtime[acc.ID] = rt
	svc.mu.Unlock()
	svc.OpenComfyUI(acc.ID)
	svc.mu.RLock()
	depToast := svc.ToastMessage
	svc.mu.RUnlock()
	if !strings.Contains(depToast, "正在自动部署中") || !strings.Contains(depToast, "35%") {
		t.Fatalf("expected deploying OpenComfyUI progress hint, got %q", depToast)
	}

	// 3. Remote status.json reports deploy failure -> ServerHealthy MUST remain false, DeployFailed becomes true, notified once
	svc.fetchAndApplyTelemetry(acc, "ep-l4-bare")
	after := svc.StateFor(acc.ID)
	if after.ServerHealthy {
		t.Fatalf("STATUS_JSON with deploy failure must NOT mark ServerHealthy=true")
	}
	if !after.DeployFailed || after.DeployRunning || !strings.Contains(after.DeployError, "安装必需节点失败") {
		t.Fatalf("expected DeployFailed with detail, got %+v", after)
	}
	// Polling a second time must not duplicate the failure notification
	svc.fetchAndApplyTelemetry(acc, "ep-l4-bare")
	mu.Lock()
	if len(notices) != 1 || !strings.Contains(notices[0], "云端部署中断") {
		t.Fatalf("expected exactly 1 deploy-failure notification, got %+v", notices)
	}
	mu.Unlock()

	svc.OpenComfyUI(acc.ID)
	svc.mu.RLock()
	failToast := svc.ToastMessage
	svc.mu.RUnlock()
	if !strings.Contains(failToast, "部署中断") {
		t.Fatalf("expected deploy-failed OpenComfyUI hint, got %q", failToast)
	}
}

func TestHistoryLedgerLifecycle_SessionAndAllocationAndHybridCU(t *testing.T) {
	svc := NewService(true)
	svc.NotifySender = func(_, _ string) {}
	acc := core.NewDefaultAccountProfile("LedgerUser", "ledger@example.com", core.TierAIUltra, core.AuthSystemDefaultOAuth2)
	acc.DeployRecipe = core.RecipeNone
	acc.WorkloadKind = core.WorkloadGeneral
	acc.EnableLLMModule = false
	acc.DriveScriptPath = ""
	svc.Settings.Accounts = []core.AccountProfile{acc}
	svc.Settings.SelectedAccountID = acc.ID
	svc.Runtime[acc.ID] = core.NewAccountRuntimeState()

	// 1. Pre-boot usage refresh gives valid baseline balance (100.00 CU)
	t0 := time.Now().Add(-2 * time.Hour)
	svc.mu.Lock()
	_, _, rec0 := svc.applyServerSnapshotLocked(acc.ID, acc, &colab.UsageSnapshot{
		Balance: 100.0, RatePerHour: 0, ActiveAssignments: 0,
	}, nil, t0, false, "")
	svc.mu.Unlock()
	if rec0 != nil {
		t.Fatalf("offline initial snapshot must not emit closed record, got %+v", rec0)
	}

	// 2. Boot transition (PhaseStarting -> PhaseRunning) opens session ledger, clears uptime/idle
	svc.mu.Lock()
	rt := svc.Runtime[acc.ID]
	rt.Phase = core.InstancePhase{Kind: core.PhaseStarting}
	rt.SessionUptimeSec = 999
	rt.IdleSeconds = 123
	svc.Runtime[acc.ID] = rt
	tBoot := t0.Add(2 * time.Minute)
	_, running, rec1 := svc.applyServerSnapshotLocked(acc.ID, acc, &colab.UsageSnapshot{
		Balance: 100.0, RatePerHour: 2.33, ActiveAssignments: 1,
	}, []core.ColabSessionItem{{Name: acc.SessionName, Endpoint: "ep-1", Hardware: "L4"}}, tBoot, true, "")
	svc.mu.Unlock()
	if !running || rec1 != nil {
		t.Fatalf("expected running without closed record, running=%v rec=%+v", running, rec1)
	}
	gotRT := svc.StateFor(acc.ID)
	if gotRT.SessionUptimeSec != 0 || gotRT.IdleSeconds != 0 || !gotRT.SessionStartBalanceValid || gotRT.SessionStartBalance != 100.0 || gotRT.SessionAdopted {
		t.Fatalf("unexpected opened ledger state: %+v", gotRT)
	}

	// 3. Mid-run sample 30m later accumulates estimated CU
	tMid := tBoot.Add(30 * time.Minute)
	svc.mu.Lock()
	_, _, _ = svc.applyServerSnapshotLocked(acc.ID, acc, &colab.UsageSnapshot{
		Balance: 98.83, RatePerHour: 2.33, ActiveAssignments: 1,
	}, []core.ColabSessionItem{{Name: acc.SessionName, Endpoint: "ep-1", Hardware: "L4"}}, tMid, false, "")
	svc.mu.Unlock()

	// 4. StopAccountSession (auto_idle) settles measured CU and prevents duplicate closed record
	fr := &fakeRunner{}
	fr.onShell = func(cmd string) runner.Result {
		if strings.Contains(cmd, "usage") {
			return runner.Result{ExitCode: 0, Stdout: "Current balance: 97.67 compute units\nUsage rate: 0.00/hr\nActive assignments: 0\n"}
		}
		if strings.Contains(cmd, "sessions") {
			return runner.Result{ExitCode: 0, Stdout: "No active sessions.\n"}
		}
		return runner.Result{ExitCode: 0}
	}
	svc.Engine = colab.NewEngine(fr, svc.Vault)
	svc.StopAccountSession(acc.ID, "", "空闲自动关机")

	recs, summary := svc.HistorySnapshot(core.HistoryFilter{Kind: core.HistoryKindSession})
	if len(recs) != 1 {
		t.Fatalf("expected exactly 1 closed session record, got %d: %+v", len(recs), recs)
	}
	if recs[0].ShutdownReason != core.ShutdownAutoIdle || recs[0].BillingBasis != core.BillingMeasured {
		t.Fatalf("expected auto_idle + measured billing, got reason=%v basis=%v", recs[0].ShutdownReason, recs[0].BillingBasis)
	}
	if recs[0].ConsumedCU < 2.32 || recs[0].ConsumedCU > 2.34 {
		t.Fatalf("expected measured delta ~2.33 CU, got %.4f", recs[0].ConsumedCU)
	}
	if summary.WatchdogStops != 1 || summary.MeasuredSessions != 1 || summary.TopHardware != "L4" {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	if rtAfter := svc.StateFor(acc.ID); rtAfter.SessionUptimeSec != 0 || rtAfter.ActiveEndpoint != "" {
		t.Fatalf("expected cleared uptime & endpoint after stop, got %+v", rtAfter)
	}

	// Second offline poll must NOT duplicate the closed session record
	svc.mu.Lock()
	_, _, recDup := svc.applyServerSnapshotLocked(acc.ID, acc, &colab.UsageSnapshot{
		Balance: 97.67, RatePerHour: 0, ActiveAssignments: 0,
	}, nil, time.Now(), false, "")
	svc.mu.Unlock()
	if recDup != nil {
		t.Fatalf("expected no duplicate session record on subsequent offline poll, got %+v", recDup)
	}

	// 5. Concurrent multi-assignment session falls back to BillingEstimated & external drop near 24h logs ShutdownCapReached
	svc.mu.Lock()
	rt2 := svc.Runtime[acc.ID]
	rt2.Phase = core.InstancePhase{Kind: core.PhaseStarting}
	svc.Runtime[acc.ID] = rt2
	t2Start := time.Now().Add(-24 * time.Hour)
	_, _, _ = svc.applyServerSnapshotLocked(acc.ID, acc, &colab.UsageSnapshot{
		Balance: 97.67, RatePerHour: 7.0, ActiveAssignments: 2,
	}, []core.ColabSessionItem{{Name: acc.SessionName, Endpoint: "ep-2", Hardware: "G4"}}, t2Start, true, "")
	rt2 = svc.Runtime[acc.ID]
	rt2.SessionUptimeSec = 24*3600 - 100
	svc.Runtime[acc.ID] = rt2
	_, _, recCap := svc.applyServerSnapshotLocked(acc.ID, acc, &colab.UsageSnapshot{
		Balance: 50.0, RatePerHour: 0, ActiveAssignments: 0,
	}, nil, time.Now(), false, "")
	svc.mu.Unlock()
	if recCap == nil || recCap.BillingBasis != core.BillingEstimated || recCap.ShutdownReason != core.ShutdownCapReached {
		t.Fatalf("expected estimated billing + cap_reached on multi-assignment 24h disconnect, got %+v", recCap)
	}

	// 6. Allocation records: direct shortage sets LastAllocHadShortage, CancelQueue logs cancelled once
	accDirect := acc
	accDirect.GPUStrategy = core.GPUStrategyCustom
	accDirect.EnablePoliteQueue = false
	accDirect.QueueTargets = []core.AcceleratorOption{core.AccG4}
	svc.UpdateAccount(accDirect)

	frShortage := &fakeRunner{}
	frShortage.onShell = func(cmd string) runner.Result {
		if strings.Contains(cmd, "--gpu") {
			return runner.Result{ExitCode: 1, Stderr: "503 Service Unavailable"}
		}
		return runner.Result{ExitCode: 0}
	}
	svc.Engine = colab.NewEngine(frShortage, svc.Vault)
	svc.StartAccountSession(acc.ID)
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		allocs, _ := svc.HistorySnapshot(core.HistoryFilter{Kind: core.HistoryKindAllocation})
		if len(allocs) >= 1 && svc.StateFor(acc.ID).Phase.Kind == core.PhaseOffline {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	allocs, _ := svc.HistorySnapshot(core.HistoryFilter{Kind: core.HistoryKindAllocation})
	if len(allocs) != 1 || allocs[0].AllocOutcome != core.AllocTagShortage {
		t.Fatalf("expected 1 shortage allocation record, got %+v", allocs)
	}
	if !svc.StateFor(acc.ID).LastAllocHadShortage {
		t.Fatalf("expected LastAllocHadShortage=true after 503 failure")
	}

	// Queue + CancelQueue logs exactly 1 AllocTagCancelled
	accQueue := acc
	accQueue.GPUStrategy = core.GPUStrategyAutoSmart
	svc.UpdateAccount(accQueue)
	frBlock := &fakeRunner{}
	blockCh := make(chan struct{})
	frBlock.onShell = func(cmd string) runner.Result {
		if strings.Contains(cmd, "--gpu") {
			<-blockCh
			return runner.Result{ExitCode: 1, Stderr: "503 Service Unavailable"}
		}
		if strings.Contains(cmd, "usage") {
			return runner.Result{ExitCode: 0, Stdout: "Current balance: 50.0 compute units\nUsage rate: 0.00/hr\nActive assignments: 0\n"}
		}
		return runner.Result{ExitCode: 0, Stdout: "No active sessions.\n"}
	}
	svc.Engine = colab.NewEngine(frBlock, svc.Vault)
	svc.StartAccountSession(acc.ID)
	time.Sleep(50 * time.Millisecond)
	svc.CancelQueue(acc.ID)
	close(blockCh)
	time.Sleep(80 * time.Millisecond)

	allocs, _ = svc.HistorySnapshot(core.HistoryFilter{Kind: core.HistoryKindAllocation})
	cancelCount := 0
	for _, r := range allocs {
		if r.AllocOutcome == core.AllocTagCancelled {
			cancelCount++
		}
	}
	if cancelCount != 1 {
		t.Fatalf("expected exactly 1 cancelled allocation record, got %d (%+v)", cancelCount, allocs)
	}

	// 7. ExportHistoryCSV writes BOM CSV to ~/Downloads (or $HOME fallback) and shows toast
	tmpHome := t.TempDir()
	downloads := filepath.Join(tmpHome, "Downloads")
	if err := os.MkdirAll(downloads, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", tmpHome)
	outPath, err := svc.ExportHistoryCSV(core.HistoryFilter{})
	if err != nil {
		t.Fatalf("ExportHistoryCSV failed: %v", err)
	}
	if !strings.HasPrefix(outPath, downloads) {
		t.Fatalf("expected CSV in Downloads, got %q", outPath)
	}
	rawCSV, err := os.ReadFile(outPath)
	if err != nil || !strings.HasPrefix(string(rawCSV), "\uFEFF") {
		t.Fatalf("expected UTF-8 BOM CSV file, err=%v", err)
	}
}

func TestCrossModelFallbackDoesNotMutateAccountAndQuickActions(t *testing.T) {
	svc := NewService(true)
	acc := core.NewDefaultAccountProfile("H3_31B", "h3@example.com", core.TierAIUltra, core.AuthSystemDefaultOAuth2)
	acc.DeployRecipe = core.RecipeH3Video
	acc.H3Variant = core.H3VariantOriginal
	acc.AllowCrossModelFallback = true
	acc.UseGoogleDrive = false
	svc.Settings.Accounts = []core.AccountProfile{acc}
	svc.Settings.SelectedAccountID = acc.ID
	svc.Runtime[acc.ID] = core.NewAccountRuntimeState()

	var mu sync.Mutex
	var notices []string
	var deployedH3Variant string
	svc.NotifySender = func(title, body string) {
		mu.Lock()
		notices = append(notices, title+"|"+body)
		mu.Unlock()
	}

	fr := &fakeRunner{}
	fr.onShellOpts = func(cmd string, opts runner.RunOptions) runner.Result {
		switch {
		case strings.Contains(cmd, "usage"):
			return runner.Result{ExitCode: 0, Stdout: "Current balance: 800.0 compute units\nUsage rate: 2.33/hr\nActive assignments: 1\n"}
		case strings.Contains(cmd, "sessions"):
			return runner.Result{ExitCode: 0, Stdout: "[" + acc.SessionName + "] m-s-l4fb | Hardware: L4 | Shape: Standard | Variant: GPU\n"}
		case strings.Contains(cmd, "exec"):
			if strings.Contains(opts.Stdin, "DEPLOY_LAUNCHED") {
				re := regexp.MustCompile(`extra = json\.loads\(base64\.b64decode\("([^"]+)"\)\.decode\("utf-8"\)\)`)
				if m := re.FindStringSubmatch(opts.Stdin); len(m) == 2 {
					if rawEnv, err := base64.StdEncoding.DecodeString(m[1]); err == nil {
						var envMap map[string]string
						if json.Unmarshal(rawEnv, &envMap) == nil {
							mu.Lock()
							deployedH3Variant = envMap["H3_VARIANT"]
							mu.Unlock()
						}
					}
				}
				return runner.Result{ExitCode: 0, Stdout: "DEPLOY_LAUNCHED=1\n"}
			}
			return runner.Result{ExitCode: 0, Stdout: "NV=NVIDIA L4, 0, 23034, 0\nHEALTH=\nDEPLOY_ALIVE=0\n"}
		default:
			return runner.Result{ExitCode: 0}
		}
	}
	svc.Engine = colab.NewEngine(fr, svc.Vault)

	svc.handlePostAllocation(acc, "L4", true, false)
	defer svc.Shutdown()

	// Original account config MUST still be H3VariantOriginal (31B)
	persisted, ok := svc.accountByID(acc.ID)
	if !ok || persisted.H3Variant != core.H3VariantOriginal {
		t.Fatalf("cross-model fallback must NOT mutate account H3Variant, got %+v", persisted.H3Variant)
	}
	rt := svc.StateFor(acc.ID)
	if !rt.SessionModelDowngraded || !rt.SessionFallbackUsed {
		t.Fatalf("expected SessionModelDowngraded=true and SessionFallbackUsed=true, got %+v", rt)
	}
	if !strings.Contains(rt.WorkloadDetail, "H3 Flash 轻量 GGUF") {
		t.Fatalf("expected temporary deploy detail to mention H3 Flash 轻量 GGUF, got %q", rt.WorkloadDetail)
	}
	mu.Lock()
	if deployedH3Variant != string(core.H3VariantFlashGGUF) {
		mu.Unlock()
		t.Fatalf("expected deployed H3_VARIANT=%q, got %q", core.H3VariantFlashGGUF, deployedH3Variant)
	}
	mu.Unlock()
	mu.Lock()
	if len(notices) == 0 || !strings.Contains(notices[0], "抢到保底 L4（已自动切轻量 GGUF）") {
		t.Fatalf("expected cross-model downgrade notification title, got %+v", notices)
	}
	mu.Unlock()

	// QuickScheduleMorningStart sets schedule & clears error & shows toast
	svc.mu.Lock()
	rtErr := svc.Runtime[acc.ID]
	rtErr.LastError = "没抢到"
	svc.Runtime[acc.ID] = rtErr
	svc.mu.Unlock()
	svc.QuickScheduleMorningStart(acc.ID, 6, 30)
	afterSched, _ := svc.accountByID(acc.ID)
	if !afterSched.ScheduledStartEnabled || afterSched.ScheduledStartHour != 6 || afterSched.ScheduledStartMinute != 30 || afterSched.ScheduledNextFireAt == nil {
		t.Fatalf("QuickScheduleMorningStart did not update schedule properly: %+v", afterSched)
	}
	if svc.StateFor(acc.ID).LastError != "" || !strings.Contains(svc.ToastMessage, "06:30") {
		t.Fatalf("expected cleared error and schedule toast, err=%q toast=%q", svc.StateFor(acc.ID).LastError, svc.ToastMessage)
	}

	// SwitchToLightGGUFAndStart updates H3Variant to flash_gguf, clears error, and triggers DeployNow
	svc.mu.Lock()
	rtOff := core.NewAccountRuntimeState()
	rtOff.LastError = "缺货"
	svc.Runtime[acc.ID] = rtOff
	svc.mu.Unlock()
	svc.SwitchToLightGGUFAndStart(acc.ID)
	afterGGUF, _ := svc.accountByID(acc.ID)
	if afterGGUF.H3Variant != core.H3VariantFlashGGUF {
		t.Fatalf("expected SwitchToLightGGUFAndStart to switch H3Variant to flash_gguf, got %v", afterGGUF.H3Variant)
	}
	if svc.StateFor(acc.ID).LastError != "" {
		t.Fatalf("expected SwitchToLightGGUFAndStart to clear error, got %q", svc.StateFor(acc.ID).LastError)
	}
	svc.CancelQueue(acc.ID)
}

type fakeInteractiveColabRunner struct {
	mu       sync.Mutex
	colabExe string
	mounted  bool
	cfURL    string
}

func (f *fakeInteractiveColabRunner) Run(_ context.Context, _ string, _ []string, _ runner.RunOptions) runner.Result {
	return runner.Result{}
}

func (f *fakeInteractiveColabRunner) RunShell(_ context.Context, shellCmd string, opts runner.RunOptions) runner.Result {
	f.mu.Lock()
	mounted := f.mounted
	cfURL := f.cfURL
	f.mu.Unlock()
	if strings.Contains(opts.Stdin, "DRIVE_MOUNTED=1") || strings.Contains(shellCmd, "DRIVE_MOUNTED=1") {
		if mounted {
			return runner.Result{ExitCode: 0, Stdout: "DRIVE_MOUNTED=1\n"}
		}
		return runner.Result{ExitCode: 0, Stdout: "DRIVE_MOUNTED=0\n"}
	}
	if strings.Contains(opts.Stdin, "CLOUDFLARED_URL=") || strings.Contains(shellCmd, "CLOUDFLARED_URL=") {
		if cfURL != "" {
			return runner.Result{ExitCode: 0, Stdout: "CLOUDFLARED_URL=" + cfURL + "\n"}
		}
		return runner.Result{ExitCode: 1, Stderr: "cloudflared unavailable"}
	}
	if strings.Contains(shellCmd, "'url'") || strings.Contains(shellCmd, " url ") {
		return runner.Result{ExitCode: 0, Stdout: "https://colab.research.google.com/drive/demo\n"}
	}
	return runner.Result{ExitCode: 0, Stdout: "DEPLOY_LAUNCHED=1\n"}
}

func (f *fakeInteractiveColabRunner) RunStreamingShell(_ context.Context, _ string, _ time.Duration, _ func(string)) runner.Result {
	return runner.Result{}
}

func (f *fakeInteractiveColabRunner) LookPath(name string) string {
	if name == "colab" {
		return f.colabExe
	}
	return ""
}

func TestActionPromptLifecycle_PersistentAutoShutdown_AndComfyFallback(t *testing.T) {
	// 1. ActionPrompt lifecycle for Drive Auth (open_url keeps modal, primary confirms, secondary skips)
	tmpDir := t.TempDir()
	fakeColab := filepath.Join(tmpDir, "colab")
	script := `#!/bin/sh
echo "REQUIRED: Google Drive Authorization needed"
echo "https://accounts.google.com/o/oauth2/auth?client_id=drive123"
echo "Press Enter to continue"
read line
exit 0
`
	if err := os.WriteFile(fakeColab, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	fr := &fakeInteractiveColabRunner{colabExe: fakeColab}

	svc := NewService(true)
	defer svc.Shutdown()
	svc.NotifySender = func(_, _ string) {}
	svc.Engine = colab.NewEngine(fr, svc.Vault)

	var openedMu sync.Mutex
	var openedURLs []string
	svc.URLOpener = func(u string) {
		openedMu.Lock()
		openedURLs = append(openedURLs, u)
		openedMu.Unlock()
	}

	acc := core.NewDefaultAccountProfile("DriveUser", "drive@example.com", core.TierAIUltra, core.AuthSystemDefaultOAuth2)
	acc.UseGoogleDrive = true
	acc.DeployRecipe = core.RecipeH3Video
	svc.Settings.Accounts = []core.AccountProfile{acc}
	svc.Settings.SelectedAccountID = acc.ID
	rt := core.NewAccountRuntimeState()
	rt.Phase = core.InstancePhase{Kind: core.PhaseRunning}
	rt.ActiveEndpoint = "ep-drive"
	svc.Runtime[acc.ID] = rt

	doneDeploy := make(chan struct{})
	go func() {
		defer close(doneDeploy)
		svc.launchVideoDeployFlow(acc)
	}()

	var prompt *core.UserActionPrompt
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if prompt = svc.CurrentActionPrompt(); prompt != nil {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if prompt == nil || prompt.Kind != core.PromptDriveAuth || !strings.Contains(prompt.URL, "accounts.google.com") {
		t.Fatalf("expected PromptDriveAuth to pop up, got %+v", prompt)
	}

	// open_url re-opens URL without dismissing modal
	svc.ResolveActionPrompt("open_url")
	if svc.CurrentActionPrompt() == nil {
		t.Fatal("open_url must not dismiss PromptDriveAuth modal")
	}
	openedMu.Lock()
	if len(openedURLs) < 2 {
		t.Fatalf("expected URLOpener called on popup and open_url, got %+v", openedURLs)
	}
	openedMu.Unlock()

	// primary proceeds and finishes mount
	fr.mu.Lock()
	fr.mounted = true
	fr.mu.Unlock()
	svc.ResolveActionPrompt("primary")
	if svc.CurrentActionPrompt() != nil {
		t.Fatal("primary must dismiss PromptDriveAuth modal")
	}
	<-doneDeploy
	svc.cancelDeployWatcher(acc.ID)

	// Skip branch ("secondary")
	fr.mu.Lock()
	fr.mounted = false
	fr.mu.Unlock()
	svc.mu.Lock()
	rt = svc.Runtime[acc.ID]
	rt.DeployRunning = false
	svc.Runtime[acc.ID] = rt
	svc.mu.Unlock()

	doneSkip := make(chan struct{})
	go func() {
		defer close(doneSkip)
		svc.launchVideoDeployFlow(acc)
	}()
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && svc.CurrentActionPrompt() == nil {
		time.Sleep(15 * time.Millisecond)
	}
	if svc.CurrentActionPrompt() == nil {
		t.Fatal("expected second PromptDriveAuth before skip")
	}
	svc.ResolveActionPrompt("secondary")
	<-doneSkip
	svc.cancelDeployWatcher(acc.ID)

	// 2. Persistent mode auto-enables AutoShutdownEnabled & populates PhaseQueuing
	pAcc := core.NewDefaultAccountProfile("NightGuard", "ng@example.com", core.TierAIUltra, core.AuthSystemDefaultOAuth2)
	pAcc.AutoShutdownEnabled = false
	pAcc.QueuePersistentMode = true
	pAcc.QueuePersistentMaxHours = 8
	svc.Settings.Accounts = append(svc.Settings.Accounts, pAcc)
	svc.Runtime[pAcc.ID] = core.NewAccountRuntimeState()

	blockQueue := make(chan struct{})
	qRunner := &fakeRunner{}
	qRunner.onShell = func(cmd string) runner.Result {
		if strings.Contains(cmd, "--gpu") {
			<-blockQueue
			return runner.Result{ExitCode: 1, Stderr: "503 Service Unavailable"}
		}
		if strings.Contains(cmd, "usage") {
			return runner.Result{ExitCode: 0, Stdout: "Current balance: 100.0 compute units\nUsage rate: 0.00/hr\nActive assignments: 0\n"}
		}
		return runner.Result{ExitCode: 0, Stdout: "No active sessions.\n"}
	}
	svc.Engine = colab.NewEngine(qRunner, svc.Vault)
	svc.StartAccountSession(pAcc.ID)

	gotAcc, _ := svc.accountByID(pAcc.ID)
	if !gotAcc.AutoShutdownEnabled {
		t.Fatal("QueuePersistentMode must automatically enable AutoShutdownEnabled on start")
	}
	qRT := svc.StateFor(pAcc.ID)
	if !qRT.Phase.IsQueuing() || !qRT.Phase.QueuePersistent || qRT.Phase.QueueMaxHours != 8 {
		t.Fatalf("expected persistent PhaseQueuing metadata, got %+v", qRT.Phase)
	}
	tray := svc.TrayLiveStatus()
	if !tray.Queuing || !tray.QueuePersistent || tray.QueueMaxHours != 8 {
		t.Fatalf("expected persistent queue fields in TrayLiveStatus, got %+v", tray)
	}
	svc.CancelQueue(pAcc.ID)
	close(blockQueue)

	// 3. OpenComfyUI: SSH 2 failures -> auto cloudflared fallback; all fail -> PromptComfyHelp
	svcComfy := NewService(true)
	defer svcComfy.Shutdown()
	svcComfy.NotifySender = func(_, _ string) {}
	svcComfy.URLOpener = func(u string) {
		openedMu.Lock()
		openedURLs = append(openedURLs, u)
		openedMu.Unlock()
	}
	svcComfy.comfyTunnelWaitTimeout = 60 * time.Millisecond
	svcComfy.comfyRetryPause = 10 * time.Millisecond
	svcComfy.sshStarter = func(_ *runner.OSRunner, _ core.AccountProfile, _ int, _ int, _ string) (*exec.Cmd, *tunnelStderrBuffer, error) {
		buf := &tunnelStderrBuffer{}
		_, _ = buf.Write([]byte("Already-active SSH session (HTTP 429)"))
		cmd := exec.Command("sh", "-c", "exit 1")
		_ = cmd.Start()
		return cmd, buf, nil
	}

	fr.mu.Lock()
	fr.cfURL = "https://auto-rescue-comfy.trycloudflare.com"
	fr.mu.Unlock()
	svcComfy.Engine = colab.NewEngine(fr, svcComfy.Vault)
	svcComfy.Settings.Accounts = []core.AccountProfile{acc}
	svcComfy.Settings.SelectedAccountID = acc.ID

	openedMu.Lock()
	openedURLs = nil
	openedMu.Unlock()
	svcComfy.mu.Lock()
	rt = core.NewAccountRuntimeState()
	rt.Phase = core.InstancePhase{Kind: core.PhaseRunning}
	rt.ActiveEndpoint = "ep-ssh-fail"
	rt.ServerHealthy = true
	rt.DeployRunning = false
	rt.DeployFailed = false
	rt.SSHTunnelActive = false
	rt.ActiveSSHTunnelPort = nil
	svcComfy.Runtime[acc.ID] = rt
	svcComfy.mu.Unlock()

	svcComfy.OpenComfyUI(acc.ID)
	openedMu.Lock()
	hasCF := len(openedURLs) > 0 && openedURLs[len(openedURLs)-1] == "https://auto-rescue-comfy.trycloudflare.com"
	openedMu.Unlock()
	if !hasCF {
		t.Fatalf("expected OpenComfyUI to fall back to cloudflared URL, got openedURLs=%+v", openedURLs)
	}
	if svcComfy.StateFor(acc.ID).ComfyOpening {
		t.Fatal("ComfyOpening must reset to false after OpenComfyUI finishes")
	}

	// All channels fail -> PromptComfyHelp with plain-Chinese guidance (never 8188 jargon)
	fr.mu.Lock()
	fr.cfURL = ""
	fr.mu.Unlock()
	svcComfy.OpenComfyUI(acc.ID)
	helpPrompt := svcComfy.CurrentActionPrompt()
	if helpPrompt == nil || helpPrompt.Kind != core.PromptComfyHelp {
		t.Fatalf("expected PromptComfyHelp when both SSH and cloudflared fail, got %+v", helpPrompt)
	}
	if strings.Contains(helpPrompt.Message, "8188") {
		t.Fatalf("PromptComfyHelp must avoid technical 8188 port jargon, got %q", helpPrompt.Message)
	}
	svcComfy.ResolveActionPrompt("cancel")
	if svcComfy.CurrentActionPrompt() != nil {
		t.Fatal("cancel must dismiss PromptComfyHelp")
	}
}
