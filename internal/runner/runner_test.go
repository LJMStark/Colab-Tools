package runner_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/LJMStark/Colab-Tools/internal/doctor"
	"github.com/LJMStark/Colab-Tools/internal/runner"
	"github.com/LJMStark/Colab-Tools/internal/vault"
)

func TestStripCRAndDoctorMethodSelection(t *testing.T) {
	if got := runner.StripCR("a\r\nb\r\n"); got != "a\nb\n" {
		t.Fatalf("unexpected crlf strip: %q", got)
	}
	if doctor.ParseVersionString("Version: 0.8.0\r\n") != "0.8.0" {
		t.Fatal("failed parsing official colab version output")
	}
	if doctor.ParseVersionString("No such option: --version\n") != "" {
		t.Fatal("error output must not parse as version")
	}
	if doctor.ChooseInstallMethod("/opt/homebrew/bin/uv", "", "") != doctor.MethodUVTool {
		t.Fatal("expected uvTool when uv is present")
	}
	if doctor.ChooseInstallMethod("", "/opt/homebrew/bin/pipx", "") != doctor.MethodPipx {
		t.Fatal("expected pipx when only pipx is present")
	}
	if doctor.ChooseInstallMethod("", "", "/opt/homebrew/bin/brew") != doctor.MethodHomebrewThenUV {
		t.Fatal("expected brew_uv when only Homebrew is present")
	}
	if doctor.ChooseInstallMethod("", "", "") != doctor.MethodOfficialUVScript {
		t.Fatal("expected official_uv_script fallback")
	}
}

func TestProcessGroupCancellationAndHeavyPipeDrain(t *testing.T) {
	r := runner.NewOSRunner()

	// Cancel streaming shell with background child holding stdout open
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(250 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	res := r.RunStreamingShell(ctx, "sleep 30 & echo child_started; wait", 20*time.Second, func(string) {})
	elapsed := time.Since(start)
	if !res.Canceled || elapsed > 4*time.Second {
		t.Fatalf("expected prompt process-group cancel (<4s), got canceled=%v elapsed=%v", res.Canceled, elapsed)
	}

	// Timeout also kills entire process group cleanly
	start2 := time.Now()
	res2 := r.RunStreamingShell(context.Background(), "sleep 30 & echo running; wait", 400*time.Millisecond, func(string) {})
	if !res2.TimedOut || time.Since(start2) > 4*time.Second {
		t.Fatalf("expected prompt timeout kill, got timedOut=%v elapsed=%v", res2.TimedOut, time.Since(start2))
	}
}

func TestVaultMemoryBackendAndCredentialNormalization(t *testing.T) {
	mem := vault.NewMemoryBackend()
	v := vault.New(mem)
	if _, err := vault.NormalizeADCCredentials(`{"client_id":"only"}`); err == nil {
		t.Fatal("expected missing refresh_token error")
	}
	raw := `{"client_id":"cid","refresh_token":"rtok","client_secret":"sec"}`
	if err := v.SaveADCCredentials("acc-1", raw); err != nil {
		t.Fatalf("save adc failed: %v", err)
	}
	loaded, err := v.LoadADCCredentials("acc-1")
	if err != nil || !strings.Contains(loaded, `"authorized_user"`) {
		t.Fatalf("expected normalized authorized_user, got %s %v", loaded, err)
	}
	if err := v.SaveLLMAPIKey("acc-1", "sk-demo-key"); err != nil {
		t.Fatal(err)
	}
	if k, err := v.LoadLLMAPIKey("acc-1"); err != nil || k != "sk-demo-key" {
		t.Fatalf("unexpected api key: %q %v", k, err)
	}

	// Legacy v0.3.6 Keychain key migration (uppercase UUID account:<UUID> and apikey:<UUID>)
	legacyID := "a1b2c3d4-0000-0000-0000-000000000001"
	_ = mem.Set("account:"+strings.ToUpper(legacyID), []byte(`{"client_id":"legacy-cid","refresh_token":"legacy-rt"}`))
	_ = mem.Set("apikey:"+strings.ToUpper(legacyID), []byte("sk-legacy-migrated"))
	gotADC, err := v.LoadADCCredentials(legacyID)
	if err != nil || !strings.Contains(gotADC, "legacy-rt") {
		t.Fatalf("legacy ADC credential must migrate transparently: %q err=%v", gotADC, err)
	}
	gotKey, err := v.LoadLLMAPIKey(legacyID)
	if err != nil || gotKey != "sk-legacy-migrated" {
		t.Fatalf("legacy API key must migrate transparently: %q err=%v", gotKey, err)
	}
}

func TestStreamingShellHandlesOver600KiBSingleLine(t *testing.T) {
	r := runner.NewOSRunner()
	var lines []string
	// Emit a >600KiB single line followed by a normal marker line; must neither deadlock nor drop the trailing line
	res := r.RunStreamingShell(context.Background(), `python3 -c 'print("A"*650000); print("AFTER_HUGE_LINE")'`, 10*time.Second, func(line string) {
		lines = append(lines, line)
	})
	if res.ExitCode != 0 {
		t.Fatalf("expected ExitCode 0 for huge single-line stream, got %+v", res)
	}
	foundTrunc := false
	foundAfter := false
	for _, ln := range lines {
		if strings.HasSuffix(ln, "…[truncated]") {
			foundTrunc = true
		}
		if ln == "AFTER_HUGE_LINE" {
			foundAfter = true
		}
	}
	if !foundTrunc || !foundAfter {
		t.Fatalf("expected truncated huge line and trailing marker, got foundTrunc=%v foundAfter=%v count=%d", foundTrunc, foundAfter, len(lines))
	}
}
