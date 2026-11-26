package colab_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LJMStark/Colab-Tools/internal/colab"
	"github.com/LJMStark/Colab-Tools/internal/core"
	"github.com/LJMStark/Colab-Tools/internal/runner"
	"github.com/LJMStark/Colab-Tools/internal/vault"
)

type mockRunner struct {
	mu       sync.Mutex
	onShell  func(cmd string) runner.Result
	commands []string
}

func (m *mockRunner) Run(_ context.Context, name string, args []string, _ runner.RunOptions) runner.Result {
	cmd := name + " " + strings.Join(args, " ")
	m.mu.Lock()
	m.commands = append(m.commands, cmd)
	fn := m.onShell
	m.mu.Unlock()
	if fn != nil {
		return fn(cmd)
	}
	return runner.Result{}
}

func (m *mockRunner) RunShell(_ context.Context, shellCmd string, _ runner.RunOptions) runner.Result {
	m.mu.Lock()
	m.commands = append(m.commands, shellCmd)
	fn := m.onShell
	m.mu.Unlock()
	if fn != nil {
		return fn(shellCmd)
	}
	return runner.Result{}
}

func (m *mockRunner) RunStreamingShell(_ context.Context, _ string, _ time.Duration, _ func(string)) runner.Result {
	return runner.Result{}
}

func (m *mockRunner) LookPath(_ string) string { return "/usr/local/bin/colab" }

func (m *mockRunner) recordedCommands() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]string, len(m.commands))
	copy(cp, m.commands)
	return cp
}

func TestStrictParsersHandleNamedSessionsAndRejectPartialUsage(t *testing.T) {
	// Partial usage must return false instead of silent zero
	if _, ok := colab.ParseUsage("Current balance: 120.5 compute units\n"); ok {
		t.Fatal("partial usage output must be rejected")
	}
	u, ok := colab.ParseUsage("Current balance: 2374.8 compute units\r\nUsage rate: 2.33/hr\r\nActive assignments: 1\r\n")
	if !ok || u.Balance != 2374.8 || u.RatePerHour != 2.33 || u.ActiveAssignments != 1 {
		t.Fatalf("unexpected parsed usage: %+v %v", u, ok)
	}

	// Sessions parser preserves session literally named `colab` and ignores log lines
	out := `[colab] Pruned 1 stale session
[colab] m-s-abc123 | Hardware: L4 | Shape: Standard | Variant: GPU
[?] m-s-orphan99 | Hardware: A100 | Shape: High-RAM | Variant: GPU
`
	sessions := colab.ParseSessions(out)
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}
	if sessions[0].Name != "colab" || sessions[0].IsOrphan {
		t.Fatalf("unexpected first session: %+v", sessions[0])
	}
	if sessions[1].Name != "?" || !sessions[1].IsOrphan || sessions[1].Hardware != "A100" {
		t.Fatalf("unexpected orphan session: %+v", sessions[1])
	}

	who, ok := colab.ParseWhoami("Auth provider: oauth2\r\nEmail: demo@example.com\r\nExpires in: 55m\r\n")
	if !ok || who.Email != "demo@example.com" {
		t.Fatalf("unexpected whoami: %+v %v", who, ok)
	}
}

func TestProvisionSession_412CheckErrorContinuesQueue(t *testing.T) {
	mr := &mockRunner{}
	mr.onShell = func(cmd string) runner.Result {
		switch {
		case strings.Contains(cmd, "--gpu") && strings.Contains(cmd, "G4"):
			return runner.Result{ExitCode: 1, Stderr: "[colab] Allocation refused (precondition failed)"}
		case strings.Contains(cmd, "usage"):
			return runner.Result{ExitCode: 1, Stderr: "temporary network error"}
		case strings.Contains(cmd, "--gpu") && strings.Contains(cmd, "L4"):
			return runner.Result{ExitCode: 0, Stdout: "Created session.\n"}
		default:
			return runner.Result{ExitCode: 0}
		}
	}
	eng := colab.NewEngine(mr, vault.New(vault.NewMemoryBackend()))
	acc := core.NewDefaultAccountProfile("Test", "t@example.com", core.TierAIUltra, core.AuthSystemDefaultOAuth2)
	acc.HighMem = false
	acc.GPUStrategy = core.GPUStrategyCustom
	acc.QueueTargets = []core.AcceleratorOption{core.AccG4, core.AccL4}

	var progress []string
	res := eng.ProvisionSession(context.Background(), acc, nil, nil, nil, func(s string) {
		progress = append(progress, s)
	})
	if res.Outcome.Kind != core.OutcomeAllocated || res.Outcome.Hardware != "L4" {
		t.Fatalf("expected 412 with usage checkErr to continue to L4 and allocate, got %+v", res.Outcome)
	}
	if len(res.ProbeRecords) != 2 || res.ProbeRecords[0].Succeeded || !res.ProbeRecords[1].Succeeded {
		t.Fatalf("unexpected probe records: %+v", res.ProbeRecords)
	}

	// Only when checkErr == nil && len(activeSessions) > 0 should 412 abort early
	mr2 := &mockRunner{}
	mr2.onShell = func(cmd string) runner.Result {
		switch {
		case strings.Contains(cmd, "--gpu") && strings.Contains(cmd, "G4"):
			return runner.Result{ExitCode: 1, Stderr: "[colab] 412 precondition failed"}
		case strings.Contains(cmd, "usage"):
			return runner.Result{ExitCode: 0, Stdout: "Current balance: 100.0 compute units\nUsage rate: 2.33/hr\nActive assignments: 1\n"}
		case strings.Contains(cmd, "sessions"):
			return runner.Result{ExitCode: 0, Stdout: "[other] m-s-old | Hardware: L4 | Shape: Standard | Variant: GPU\n"}
		default:
			return runner.Result{ExitCode: 0}
		}
	}
	eng2 := colab.NewEngine(mr2, vault.New(vault.NewMemoryBackend()))
	res2 := eng2.ProvisionSession(context.Background(), acc, nil, nil, nil, nil)
	if res2.Outcome.Kind != core.OutcomePrecondition412 {
		t.Fatalf("expected 412 with active session to abort early, got %+v", res2.Outcome)
	}
}

func TestProvisionSession_TimeoutDedupAndContinue(t *testing.T) {
	acc := core.NewDefaultAccountProfile("Test", "t@example.com", core.TierAIUltra, core.AuthSystemDefaultOAuth2)
	acc.HighMem = false
	acc.GPUStrategy = core.GPUStrategyCustom
	acc.QueueTargets = []core.AcceleratorOption{core.AccG4, core.AccL4}

	// Case 1: TIMEOUT on G4, FetchUsageAndSessions finds session already running -> return OutcomeAllocated immediately, never try L4
	mr1 := &mockRunner{}
	mr1.onShell = func(cmd string) runner.Result {
		switch {
		case strings.Contains(cmd, "--gpu") && strings.Contains(cmd, "G4"):
			return runner.Result{ExitCode: -1, TimedOut: true}
		case strings.Contains(cmd, "usage"):
			return runner.Result{ExitCode: 0, Stdout: "Current balance: 100.0 compute units\nUsage rate: 7.00/hr\nActive assignments: 1\n"}
		case strings.Contains(cmd, "sessions"):
			return runner.Result{ExitCode: 0, Stdout: "[" + acc.SessionName + "] m-s-g4ok | Hardware: G4 | Shape: Standard | Variant: GPU\n"}
		default:
			return runner.Result{ExitCode: 0}
		}
	}
	eng1 := colab.NewEngine(mr1, vault.New(vault.NewMemoryBackend()))
	res1 := eng1.ProvisionSession(context.Background(), acc, nil, nil, nil, nil)
	if res1.Outcome.Kind != core.OutcomeAllocated || res1.Outcome.Hardware != "G4" {
		t.Fatalf("expected already-online session after timeout to return Allocated G4, got %+v", res1.Outcome)
	}
	for _, c := range mr1.recordedCommands() {
		if strings.Contains(c, "--gpu") && strings.Contains(c, "L4") {
			t.Fatalf("must not attempt L4 after timeout verified G4 is online: %v", mr1.recordedCommands())
		}
	}

	// Case 2: TIMEOUT on G4, checkErr == nil and confirmed NOT online -> continue and allocate L4
	mr2 := &mockRunner{}
	mr2.onShell = func(cmd string) runner.Result {
		switch {
		case strings.Contains(cmd, "--gpu") && strings.Contains(cmd, "G4"):
			return runner.Result{ExitCode: -1, TimedOut: true}
		case strings.Contains(cmd, "usage"):
			return runner.Result{ExitCode: 0, Stdout: "Current balance: 100.0 compute units\nUsage rate: 0.00/hr\nActive assignments: 0\n"}
		case strings.Contains(cmd, "sessions"):
			return runner.Result{ExitCode: 0, Stdout: "No active sessions.\n"}
		case strings.Contains(cmd, "--gpu") && strings.Contains(cmd, "L4"):
			return runner.Result{ExitCode: 0, Stdout: "Created session.\n"}
		default:
			return runner.Result{ExitCode: 0}
		}
	}
	eng2 := colab.NewEngine(mr2, vault.New(vault.NewMemoryBackend()))
	res2 := eng2.ProvisionSession(context.Background(), acc, nil, nil, nil, nil)
	if res2.Outcome.Kind != core.OutcomeAllocated || res2.Outcome.Hardware != "L4" {
		t.Fatalf("expected confirmed-offline timeout on G4 to continue and allocate L4, got %+v", res2.Outcome)
	}

	// Case 3: TIMEOUT on G4, verification check fails -> conservatively abort with TIMEOUT
	mr3 := &mockRunner{}
	mr3.onShell = func(cmd string) runner.Result {
		switch {
		case strings.Contains(cmd, "--gpu") && strings.Contains(cmd, "G4"):
			return runner.Result{ExitCode: -1, TimedOut: true}
		case strings.Contains(cmd, "usage"):
			return runner.Result{ExitCode: 1, Stderr: "network unreachable"}
		default:
			return runner.Result{ExitCode: 0}
		}
	}
	eng3 := colab.NewEngine(mr3, vault.New(vault.NewMemoryBackend()))
	res3 := eng3.ProvisionSession(context.Background(), acc, nil, nil, nil, nil)
	if res3.Outcome.Kind != core.OutcomeTransientError || res3.Outcome.Code != "TIMEOUT" {
		t.Fatalf("expected conservative TIMEOUT when verification fails, got %+v", res3.Outcome)
	}
}

func TestProvisionSession_HighMem503FallbackToStandardRAM(t *testing.T) {
	acc := core.NewDefaultAccountProfile("Test", "t@example.com", core.TierAIUltra, core.AuthSystemDefaultOAuth2)
	acc.HighMem = true
	acc.GPUStrategy = core.GPUStrategyCustom
	acc.QueueTargets = []core.AcceleratorOption{core.AccG4, core.AccL4}

	// Case 1: G4 --high-mem returns 503 -> retries G4 without --high-mem and succeeds with UsedStandardRAM=true
	mr1 := &mockRunner{}
	mr1.onShell = func(cmd string) runner.Result {
		if strings.Contains(cmd, "--gpu") && strings.Contains(cmd, "G4") {
			if strings.Contains(cmd, "--high-mem") {
				return runner.Result{ExitCode: 1, Stderr: "503 Service Unavailable"}
			}
			return runner.Result{ExitCode: 0, Stdout: "Created session.\n"}
		}
		return runner.Result{ExitCode: 0}
	}
	eng1 := colab.NewEngine(mr1, vault.New(vault.NewMemoryBackend()))
	res1 := eng1.ProvisionSession(context.Background(), acc, nil, nil, nil, nil)
	if res1.Outcome.Kind != core.OutcomeAllocated || res1.Outcome.Hardware != "G4" || !res1.UsedStandardRAM {
		t.Fatalf("expected G4 standard-RAM fallback allocation with UsedStandardRAM=true, got %+v", res1)
	}

	// Case 2: G4 fails both --high-mem and standard RAM with 503 -> continues to L4 and allocates L4 --high-mem
	mr2 := &mockRunner{}
	mr2.onShell = func(cmd string) runner.Result {
		if strings.Contains(cmd, "--gpu") && strings.Contains(cmd, "G4") {
			return runner.Result{ExitCode: 1, Stderr: "503 Service Unavailable"}
		}
		if strings.Contains(cmd, "--gpu") && strings.Contains(cmd, "L4") {
			return runner.Result{ExitCode: 0, Stdout: "Created session.\n"}
		}
		return runner.Result{ExitCode: 0}
	}
	eng2 := colab.NewEngine(mr2, vault.New(vault.NewMemoryBackend()))
	res2 := eng2.ProvisionSession(context.Background(), acc, nil, nil, nil, nil)
	if res2.Outcome.Kind != core.OutcomeAllocated || res2.Outcome.Hardware != "L4" || res2.UsedStandardRAM {
		t.Fatalf("expected L4 --high-mem allocation after G4 standard fallback 503, got %+v", res2)
	}
}
