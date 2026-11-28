package colab_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	mu          sync.Mutex
	onShell     func(cmd string) runner.Result
	onShellOpts func(cmd string, opts runner.RunOptions) runner.Result
	commands    []string
}

func (m *mockRunner) Run(_ context.Context, name string, args []string, opts runner.RunOptions) runner.Result {
	cmd := name + " " + strings.Join(args, " ")
	m.mu.Lock()
	m.commands = append(m.commands, cmd)
	fn := m.onShell
	fnOpts := m.onShellOpts
	m.mu.Unlock()
	if fnOpts != nil {
		return fnOpts(cmd, opts)
	}
	if fn != nil {
		return fn(cmd)
	}
	return runner.Result{}
}

func (m *mockRunner) RunShell(_ context.Context, shellCmd string, opts runner.RunOptions) runner.Result {
	m.mu.Lock()
	m.commands = append(m.commands, shellCmd)
	fn := m.onShell
	fnOpts := m.onShellOpts
	m.mu.Unlock()
	if fnOpts != nil {
		return fnOpts(shellCmd, opts)
	}
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
	sessions, err := colab.ParseSessionsStrict(out)
	if err != nil || len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d err=%v", len(sessions), err)
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

type driveTestRunner struct {
	colabExe string
	mounted  bool
}

func (d *driveTestRunner) Run(_ context.Context, _ string, _ []string, _ runner.RunOptions) runner.Result {
	return runner.Result{}
}

func (d *driveTestRunner) RunShell(_ context.Context, shellCmd string, _ runner.RunOptions) runner.Result {
	if strings.Contains(shellCmd, "exec") {
		if d.mounted {
			return runner.Result{ExitCode: 0, Stdout: "DRIVE_MOUNTED=1\n"}
		}
		return runner.Result{ExitCode: 0, Stdout: "DRIVE_MOUNTED=0\n"}
	}
	return runner.Result{}
}

func (d *driveTestRunner) RunStreamingShell(_ context.Context, _ string, _ time.Duration, _ func(string)) runner.Result {
	return runner.Result{}
}

func (d *driveTestRunner) LookPath(name string) string {
	if name == "colab" {
		return d.colabExe
	}
	return ""
}

func TestDriveMountInteractive(t *testing.T) {
	acc := core.NewDefaultAccountProfile("CF", "cf@example.com", core.TierAIUltra, core.AuthSystemDefaultOAuth2)

	// 1. EnsureDriveMountedInteractive triggers auth callback, writes Enter on proceed=true, and verifies mount
	tmpDir := t.TempDir()
	fakeColab := filepath.Join(tmpDir, "colab")
	script := `#!/bin/sh
echo "REQUIRED: Google Drive Authorization needed"
echo "https://accounts.google.com/o/oauth2/auth?client_id=test123"
echo "Press Enter to continue"
if (exec </dev/tty) 2>/dev/null; then
  echo "ERR_CONTROLLING_TTY_NOT_DETACHED" >&2
  exit 99
fi
if command -v python3 >/dev/null 2>&1; then
  python3 -c 'import os, sys; sys.exit(0 if os.getsid(0) == int(sys.argv[1]) else 98)' "$$" || exit 98
fi
IFS= read -r line || exit 97
[ -z "$line" ] || exit 96
exit 0
`
	if err := os.WriteFile(fakeColab, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	dr := &driveTestRunner{colabExe: fakeColab, mounted: false}
	engDrive := colab.NewEngine(dr, vault.New(vault.NewMemoryBackend()))

	var gotURL string
	ok, reason := engDrive.EnsureDriveMountedInteractive(context.Background(), acc, func(authURL string) bool {
		gotURL = authURL
		dr.mounted = true
		return true
	})
	if !ok || reason != "" || gotURL != "https://accounts.google.com/o/oauth2/auth?client_id=test123" {
		t.Fatalf("expected interactive drive mount success, got ok=%v reason=%q url=%q", ok, reason, gotURL)
	}

	// 3. EnsureDriveMountedInteractive skips cleanly without deadlocking even if CLI keeps writing output
	noisyColab := filepath.Join(tmpDir, "colab-noisy")
	noisyScript := `#!/bin/sh
echo "REQUIRED: Google Drive Authorization needed"
echo "https://accounts.google.com/o/oauth2/auth?client_id=noisy"
echo "Press Enter to continue"
trap 'i=0; while [ $i -lt 120 ]; do echo "teardown-noise-line-$i-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"; i=$((i+1)); done; exit 1' TERM
while :; do
  echo "waiting-noise-line-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
  sleep 0.05
done
`
	if err := os.WriteFile(noisyColab, []byte(noisyScript), 0o755); err != nil {
		t.Fatal(err)
	}
	dr.colabExe = noisyColab
	dr.mounted = false
	skipCtx, skipCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer skipCancel()
	ok2, reason2 := engDrive.EnsureDriveMountedInteractive(skipCtx, acc, func(authURL string) bool {
		return false
	})
	if ok2 || !strings.Contains(reason2, "跳过 Google Drive") {
		t.Fatalf("expected interactive drive mount skip without deadlock, got ok=%v reason=%q", ok2, reason2)
	}
}

func TestParseSessionsStrictFlushValidationAndTelemetryDriveState(t *testing.T) {
	// 1. ParseSessionsStrict accepts empty / known empty hints / valid lines, rejects unknown garbage
	if items, err := colab.ParseSessionsStrict(""); err != nil || len(items) != 0 {
		t.Fatalf("empty output must parse cleanly, got items=%v err=%v", items, err)
	}
	if items, err := colab.ParseSessionsStrict("No active sessions.\n"); err != nil || len(items) != 0 {
		t.Fatalf("known empty hint must parse cleanly, got items=%v err=%v", items, err)
	}
	if items, err := colab.ParseSessionsStrict("[colab-tools] ep-1 | Hardware: A100 | Shape: High-RAM | Variant: GPU\n"); err != nil || len(items) != 1 || items[0].Hardware != "A100" {
		t.Fatalf("valid session line failed: items=%+v err=%v", items, err)
	}
	for _, bad := range []string{
		"<html><body>502 Bad Gateway</body></html>",
		"[colab-tools] truncated without hardware field",
		"Unexpected CLI v2 JSON format: {\"sessions\":[]}",
		"Active sessions:\n[colab-tools] truncated without hardware",
		"[extra] ep-2 | Hardware: L4 | Shape: Standard | Variant: GPU\n[colab-tools] ep-1 | truncated",
	} {
		if _, err := colab.ParseSessionsStrict(bad); err == nil {
			t.Fatalf("expected ErrSessionParseFailed for %q, got nil", bad)
		}
	}

	// 2. FlushWorkspaceToDrive must reject FLUSH_SKIP=1 and FLUSH_NO_DRIVE=1 and accept FLUSH_OK
	acc := core.NewDefaultAccountProfile("Flush", "flush@example.com", core.TierAIUltra, core.AuthSystemDefaultOAuth2)
	for _, tc := range []struct {
		name    string
		stdout  string
		wantErr bool
	}{
		{"ok", "FLUSH_OK workflows=2 snapshot_nodes=1\n", false},
		{"substring_false_positive", "ERROR: NOT_FLUSH_OK\n", true},
		{"skip", "FLUSH_SKIP=1\n", true},
		{"no_drive", "FLUSH_NO_DRIVE=1\n", true},
		{"empty", "\n", true},
	} {
		mr := &mockRunner{
			onShell: func(_ string) runner.Result {
				return runner.Result{ExitCode: 0, Stdout: tc.stdout}
			},
		}
		eng := colab.NewEngine(mr, vault.New(vault.NewMemoryBackend()))
		err := eng.FlushWorkspaceToDrive(context.Background(), acc)
		if tc.wantErr && err == nil {
			t.Fatalf("case %s: expected error for stdout=%q", tc.name, tc.stdout)
		}
		if !tc.wantErr && err != nil {
			t.Fatalf("case %s: expected nil error, got %v", tc.name, err)
		}
	}

	// 3. FetchRemoteTelemetry parses DRIVE_MOUNTED, EFFECTIVE_OUT_DIR, TEMP_OUT_VIDEOS
	mrTel := &mockRunner{
		onShell: func(_ string) runner.Result {
			return runner.Result{
				ExitCode: 0,
				Stdout:   "NV=NVIDIA L4, 1200, 23034, 5\nDRIVE_MOUNTED=0\nTEMP_OUT_VIDEOS=4\nEFFECTIVE_OUT_DIR=/content/output\nOUT_VIDEOS=3\nHEALTH=ok\n",
			}
		},
	}
	engTel := colab.NewEngine(mrTel, vault.New(vault.NewMemoryBackend()))
	tel := engTel.FetchRemoteTelemetry(context.Background(), acc)
	if tel == nil || tel.DriveMounted == nil || *tel.DriveMounted != false || tel.TempOutputVideos == nil || *tel.TempOutputVideos != 4 || tel.EffectiveOutputDir != "/content/output" || tel.RenderedVideoCount != 3 {
		t.Fatalf("unexpected telemetry parsed: %+v", tel)
	}

	// 4. ParseSessionsStrict must NOT treat "10 active sessions" as empty
	if _, err := colab.ParseSessionsStrict("10 active sessions\n"); err == nil {
		t.Fatal("expected ParseSessionsStrict to reject malformed '10 active sessions' line instead of treating it as empty")
	}

	// 5. Execute the embedded Python telemetry scanner for real (verifies import os + recursive temp scan)
	var capturedPy string
	mrCap := &mockRunner{
		onShellOpts: func(_ string, opts runner.RunOptions) runner.Result {
			capturedPy = opts.Stdin
			return runner.Result{ExitCode: 0, Stdout: "HEALTH=ok\n"}
		},
	}
	colab.NewEngine(mrCap, vault.New(vault.NewMemoryBackend())).FetchRemoteTelemetry(context.Background(), acc)
	if !strings.Contains(capturedPy, "import os,") {
		t.Fatalf("embedded telemetry Python must explicitly import os")
	}
	if _, err := exec.LookPath("python3"); err == nil {
		tmpScan := t.TempDir()
		nested := filepath.Join(tmpScan, "scene_01")
		if err := os.MkdirAll(nested, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(nested, "clip.mp4"), []byte("mp4"), 0o600); err != nil {
			t.Fatal(err)
		}
		probeScript := strings.ReplaceAll(capturedPy, `"/content/output"`, fmt.Sprintf("%q", tmpScan))
		probeScript = strings.ReplaceAll(probeScript, `"/content/ComfyUI/output"`, fmt.Sprintf("%q", filepath.Join(tmpScan, "none")))
		cmd := exec.Command("python3", "-")
		cmd.Stdin = strings.NewReader(probeScript)
		outBytes, runErr := cmd.CombinedOutput()
		if runErr != nil {
			t.Fatalf("real python3 execution of embedded telemetry script failed: %v\noutput:\n%s", runErr, string(outBytes))
		}
		if !strings.Contains(string(outBytes), "TEMP_OUT_VIDEOS=1") {
			t.Fatalf("expected recursive temp video scan to detect nested clip.mp4, got:\n%s", string(outBytes))
		}
	}
}

func TestParseUsageEligibleAcceleratorsSanitizeErrorAndSessionOutputs(t *testing.T) {
	// 1. Real `--logtostderr usage` stderr payload with XSSI prefix and sensitive Authorization header
	stderr := strings.Join([]string{
		"2026-11-28 23:28:31,100 - colab_cli.client - DEBUG - Request Headers: {'Authorization': 'Bearer ya29.super-secret-token'}",
		"2026-11-28 23:28:31,351 - colab_cli.client - DEBUG - Response: 200 OK",
		"2026-11-28 23:28:31,351 - colab_cli.client - DEBUG - Response Body: )]}'",
		`{"currentBalance":200.0, "consumptionRateHourly":0, "assignmentsCount":0, "eligibleGpus":["A100", "L4", "T4"], "eligibleTpus":["V5E1"]}`,
	}, "\n")
	elig := colab.ParseUsageEligibleAccelerators(stderr)
	want := []core.AcceleratorOption{core.AccA100, core.AccL4, core.AccT4, core.AccTPUV5e1}
	if len(elig) != len(want) {
		t.Fatalf("expected %v, got %v", want, elig)
	}
	for i := range want {
		if elig[i] != want[i] {
			t.Fatalf("eligible[%d]: want %v, got %v", i, want[i], elig[i])
		}
	}

	// 2. Error sanitization must NEVER leak Authorization Bearer tokens or DEBUG request headers
	errStderr := strings.Join([]string{
		"2026-11-28 23:28:31,100 - colab_cli.client - DEBUG - Request Headers: {'Authorization': 'Bearer ya29.leak-test'}",
		"[colab] Warning: failed to fetch compute-unit info: 401 Unauthorized",
	}, "\n")
	sanitized := colab.SanitizeCLIErrorOutput(errStderr)
	if strings.Contains(sanitized, "ya29.leak-test") || strings.Contains(sanitized, "Authorization") {
		t.Fatalf("SanitizeCLIErrorOutput leaked sensitive header: %q", sanitized)
	}
	if !strings.Contains(sanitized, "401 Unauthorized") {
		t.Fatalf("expected user-facing error preserved, got %q", sanitized)
	}

	// 3. Fallback to plain `colab usage` when older CLI rejects `--logtostderr`
	acc := core.NewDefaultAccountProfile("Compat", "compat@example.com", core.TierAIPro, core.AuthSystemDefaultOAuth2)
	mrFallback := &mockRunner{
		onShell: func(cmd string) runner.Result {
			switch {
			case strings.Contains(cmd, "--logtostderr"):
				return runner.Result{ExitCode: 2, Stderr: "Error: No such option: --logtostderr"}
			case strings.Contains(cmd, "usage"):
				return runner.Result{ExitCode: 0, Stdout: "Current balance: 200.00 compute units\nUsage rate: 0.00/hr\nActive assignments: 0\n"}
			case strings.Contains(cmd, "sessions"):
				return runner.Result{ExitCode: 0, Stdout: "No active sessions.\n"}
			default:
				return runner.Result{ExitCode: 0}
			}
		},
	}
	usage, sessions, err := colab.NewEngine(mrFallback, vault.New(vault.NewMemoryBackend())).FetchUsageAndSessions(context.Background(), acc)
	if err != nil || usage == nil || usage.Balance != 200.0 || len(sessions) != 0 {
		t.Fatalf("expected fallback usage=200.0, got usage=%+v sessions=%+v err=%v", usage, sessions, err)
	}

	// 4. FetchRemoteTelemetry parses SESSION_NEW_VIDEOS, TEMP_OUT_VIDEOS=unknown (-1), & rejects invalid filenames/subfolders
	mrOut := &mockRunner{
		onShell: func(_ string) runner.Result {
			return runner.Result{
				ExitCode: 0,
				Stdout: strings.Join([]string{
					"HEALTH=ok",
					"EFFECTIVE_OUT_DIR=/content/output",
					"TEMP_OUT_VIDEOS=unknown",
					"OUT_VIDEOS=9",
					"SESSION_NEW_VIDEOS=2",
					`SESSION_OUTPUTS_JSON=[{"filename":"clip_002.mp4","subfolder":"","type":"output","savedToDrive":false},{"filename":"../evil.mp4","subfolder":"","type":"output","savedToDrive":false},{"filename":"bad_sub.mp4","subfolder":"../escape","type":"output","savedToDrive":false},{"filename":"clip_001.mp4","subfolder":"scene1","type":"output","savedToDrive":false}]`,
				}, "\n"),
			}
		},
	}
	tel := colab.NewEngine(mrOut, vault.New(vault.NewMemoryBackend())).FetchRemoteTelemetry(context.Background(), acc)
	if tel == nil || tel.SessionNewVideos == nil || *tel.SessionNewVideos != 2 {
		t.Fatalf("expected SessionNewVideos=2, got %+v", tel)
	}
	if tel.TempOutputVideos == nil || *tel.TempOutputVideos != -1 {
		t.Fatalf("expected TEMP_OUT_VIDEOS=unknown mapped to -1, got %+v", tel.TempOutputVideos)
	}
	if len(tel.SessionOutputs) != 2 || tel.SessionOutputs[0].Filename != "clip_002.mp4" || tel.SessionOutputs[1].Filename != "clip_001.mp4" {
		t.Fatalf("expected sanitized 2 outputs (excluding traversal filename & subfolder), got %+v", tel.SessionOutputs)
	}

	// 5. ParseUsageEligibleAcceleratorsWithKnown distinguishes absent field vs explicit empty array
	if _, known := colab.ParseUsageEligibleAcceleratorsWithKnown("DEBUG - Response: 200 OK\n"); known {
		t.Fatal("expected known=false when eligible fields absent")
	}
	if emptyElig, known := colab.ParseUsageEligibleAcceleratorsWithKnown("Response Body: )]}'\n{\"eligibleGpus\":[],\"eligibleTpus\":[]}"); !known || len(emptyElig) != 0 {
		t.Fatalf("expected known=true empty slice for explicit empty arrays, got known=%v elig=%v", known, emptyElig)
	}

	// 6. DownloadRemoteFile must never leak colab-runtime-proxy-token from stdout/stderr
	mrDL := &mockRunner{
		onShell: func(_ string) runner.Result {
			return runner.Result{
				ExitCode: 1,
				Stdout:   "Error downloading https://proxy.example/files?colab-runtime-proxy-token=SECRET_XYZ_999 (403 Forbidden)",
			}
		},
	}
	dlErr := colab.NewEngine(mrDL, vault.New(vault.NewMemoryBackend())).DownloadRemoteFile(context.Background(), acc, "/content/output/a.mp4", filepath.Join(t.TempDir(), "a.mp4"))
	if dlErr == nil || strings.Contains(dlErr.Error(), "SECRET_XYZ_999") || strings.Contains(dlErr.Error(), "proxy-token") {
		t.Fatalf("DownloadRemoteFile leaked proxy token or missed error: %v", dlErr)
	}
}
