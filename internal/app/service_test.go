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
