package uiapp_test

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/LJMStark/Colab-Tools/internal/app"
	"github.com/LJMStark/Colab-Tools/internal/core"
	"github.com/LJMStark/Colab-Tools/internal/doctor"
	"github.com/LJMStark/Colab-Tools/internal/integrations"
	"github.com/LJMStark/Colab-Tools/internal/uiapp"
	"github.com/egoist/mygo/ui"
)

func makeDemoService(section app.MainSection, onboarding, narrowIdle bool) *app.Service {
	svc := app.NewService(true)
	svc.SelectedSection = section
	svc.ShowingOnboarding = onboarding
	now := time.Now()

	if onboarding {
		svc.CLIInstalled = false
		svc.EnvReport = doctor.Report{
			ColabInstalled:    false,
			UVPath:            "/opt/homebrew/bin/uv",
			BrewPath:          "/opt/homebrew/bin/brew",
			Python3Path:       "/usr/bin/python3",
			RecommendedMethod: doctor.MethodUVTool,
			CheckedAt:         now,
		}
		return svc
	}

	svc.CLIInstalled = true
	svc.EnvReport = doctor.Report{
		ColabInstalled:    true,
		ColabPath:         "~/.local/bin/colab",
		ColabVersion:      "0.8.2",
		UVPath:            "/opt/homebrew/bin/uv",
		BrewPath:          "/opt/homebrew/bin/brew",
		RecommendedMethod: doctor.MethodUVTool,
		CheckedAt:         now,
	}
	svc.ClientDetection = integrations.DetectionSnapshot{
		CCSwitchInstalled:   true,
		OpenCodeInstalled:   true,
		CodexInstalled:      true,
		ClaudeCodeInstalled: true,
	}

	if narrowIdle {
		acc := core.NewDefaultAccountProfile("demolongusername9876", "demolongusername9876@example.com", core.TierAIUltra, core.AuthSystemDefaultOAuth2)
		acc.DeployRecipe = core.RecipeLLMProxy
		acc.WorkloadKind = core.WorkloadLLM
		acc.EnableLLMModule = true
		svc.Settings.Accounts = []core.AccountProfile{acc}
		svc.Settings.SelectedAccountID = acc.ID
		rt := core.NewAccountRuntimeState()
		rt.CUBalance = 2362.2
		rt.LastUsageFetchAt = &now
		svc.Runtime[acc.ID] = rt
		return svc
	}

	acc1 := core.NewDefaultAccountProfile("Personal", "personal@example.com", core.TierAIUltra, core.AuthSystemDefaultOAuth2)
	acc1.SessionName = "h3-studio"
	acc1.HighMem = true
	acc1.DeployRecipe = core.RecipeH3Video
	acc1.H3Variant = core.H3VariantFlash
	acc1.WorkloadKind = core.WorkloadVideo
	acc1.EnableLLMModule = false
	acc1.UseGoogleDrive = true
	acc1.CacheBaseToDrive = true
	acc1.AutoOpenComfyUI = true
	acc1.StopAfterQueueDrained = true
	acc1.QueueDrainMinutes = 10
	acc1.IdleTimeoutMinutes = 10
	acc1.EnablePoliteQueue = true
	acc1.QueueTargets = []core.AcceleratorOption{core.AccG4, core.AccH100}
	acc1.QueueFallbackEnabled = true
	acc1.QueueFallbackAfterRounds = 2
	acc1.QueueFallbackTarget = core.AccL4
	acc1.ScheduledStartEnabled = true
	acc1.ScheduledStartHour = 7
	acc1.ScheduledStartMinute = 0
	nf := core.NextScheduledFireDate(7, 0, now)
	acc1.ScheduledNextFireAt = &nf
	acc1.AutoSyncCCSwitch = true
	acc1.CCSwitchName = "Colab Qwen3.8-27B (无审查)"
	acc1.AutoSyncOpenCode = true

	rt1 := core.NewAccountRuntimeState()
	rt1.Phase = core.InstancePhase{Kind: core.PhaseRunning}
	rt1.CUBalance = 2374.8
	rt1.CURatePerHour = 8.88
	rt1.ActiveAssignments = 1
	rt1.GPUName = "NVIDIA A100-SXM4-80GB"
	rt1.VRAMUsedMB = 46899
	rt1.VRAMTotalMB = 81920
	rt1.GPUUtilPercent = 86
	rt1.GPUUtilHistory = []float64{42, 68, 84, 92, 88, 76, 85, 91, 89, 82, 88, 86, 84}
	rt1.SessionUptimeSec = 1*3600 + 18*60
	rt1.WorkloadName = "H3 Flash 极速版"
	rt1.WorkloadDetail = "H3 Flash 极速版 · 成片直存 Drive"
	ratio := 0.67
	rt1.WorkloadMetricRatio = &ratio
	rt1.QueueRunning = 1
	rt1.QueuePending = 3
	rt1.RenderedVideoCount = 6
	rt1.ServerHealthy = true
	rt1.SSHTunnelActive = true
	port := 8188
	rt1.ActiveSSHTunnelPort = &port
	rt1.DetectedServicePort = &port
	rt1.PublicURL = "https://demo-colab-node.trycloudflare.com"
	rt1.IdleSeconds = 0
	rt1.ActivitySignal = core.ActivitySignal{Kind: core.SignalBusy, ObservedAt: now}
	rt1.SignalSource = core.SignalSourceComfyQueue
	rt1.CCSwitchSynced = true
	rt1.OpenCodeSynced = true
	rt1.LastUsageFetchAt = &now

	acc2 := core.NewDefaultAccountProfile("Work", "work@example.com", core.TierColabProPlus, core.AuthIsolatedVaultADC)
	acc2.SessionName = "llm-coder"
	acc2.DeployRecipe = core.RecipeLLMProxy
	acc2.WorkloadKind = core.WorkloadLLM
	acc2.EnableLLMModule = true
	acc2.ColorTag = core.ColorEmerald
	rt2 := core.NewAccountRuntimeState()
	rt2.Phase = core.InstancePhase{Kind: core.PhaseRunning}
	rt2.CUBalance = 584.2
	rt2.CURatePerHour = 2.33
	rt2.GPUName = "NVIDIA L4"
	rt2.VRAMUsedMB = 21913
	rt2.VRAMTotalMB = 23034
	rt2.ModelFile = "Qwen3.8-27B-Q4_K_XL.gguf"
	rt2.LastUsageFetchAt = &now

	acc3 := core.NewDefaultAccountProfile("Lab", "lab@example.org", core.TierFree, core.AuthIsolatedVaultADC)
	acc3.ColorTag = core.ColorCyan
	rt3 := core.NewAccountRuntimeState()
	rt3.CUBalance = 0
	rt3.LastUsageFetchAt = &now

	svc.Settings.Accounts = []core.AccountProfile{acc1, acc2, acc3}
	svc.Settings.SelectedAccountID = acc1.ID
	svc.Runtime[acc1.ID] = rt1
	svc.Runtime[acc2.ID] = rt2
	svc.Runtime[acc3.ID] = rt3

	// Seed rich local allocation heatmap samples for preview
	for i := 0; i < 8; i++ {
		svc.Insights.Record("G4", i < 7, time.Date(2026, 11, 25, 6, 30, 0, 0, time.Local))
	}
	for i := 0; i < 6; i++ {
		svc.Insights.Record("G4", i < 3, time.Date(2026, 11, 25, 2, 0, 0, 0, time.Local))
		svc.Insights.Record("G4", i < 3, time.Date(2026, 11, 25, 10, 0, 0, 0, time.Local))
		svc.Insights.Record("G4", i < 2, time.Date(2026, 11, 25, 21, 0, 0, 0, time.Local))
	}
	for i := 0; i < 5; i++ {
		svc.Insights.Record("G4", i == 0, time.Date(2026, 11, 25, 14, 0, 0, 0, time.Local))
		svc.Insights.Record("G4", i == 0, time.Date(2026, 11, 25, 18, 0, 0, 0, time.Local))
	}
	return svc
}

func renderPNG(t *testing.T, view func(*ui.Context), width, height int, outPath string) {
	t.Helper()
	img := ui.Render(view, width, height, 2.0)
	if img == nil {
		t.Fatalf("ui.Render returned nil for %s", outPath)
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(outPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func TestMainConsoleInteractionsAndAllTabs(t *testing.T) {
	svc := makeDemoService(app.SectionStatus, false, false)
	mui := uiapp.NewMainUI(svc)
	mui.ForceTheme = "dark"
	tt := ui.NewTester(mui.View, 1160, 760)

	if !tt.HasText("2374.8") || !tt.HasText("Personal") || !tt.HasText("G4 → H100") || !tt.HasText("开 ComfyUI") {
		t.Fatalf("missing primary dashboard text: %q", tt.Texts())
	}

	// Switch video drain/watchdog pill to 40m
	if err := tt.Click("40m"); err != nil {
		t.Fatalf("click 40m failed: %v", err)
	}
	if acc, _ := svc.SelectedAccount(); acc.IdleTimeoutMinutes != 40 || acc.QueueDrainMinutes != 40 {
		t.Fatalf("expected 40m after click, got idle=%d drain=%d", acc.IdleTimeoutMinutes, acc.QueueDrainMinutes)
	}

	// Navigate across all 6 capsule tabs and verify H3 Original / Flash toggle inside Deploy page
	for _, tab := range []string{"部署", "抢卡", "接入", "账号", "设置", "部署"} {
		if err := tt.Click(tab); err != nil {
			t.Fatalf("click tab %s failed: %v", tab, err)
		}
	}
	if !tt.HasText("H3 原版（31B 官方满血）") || !tt.HasText("H3 Flash 极速版（13.7B）") {
		t.Fatalf("missing H3 variant cards on deploy page: %q", tt.Texts())
	}
	if err := tt.Click("H3 原版（31B 官方满血）"); err != nil {
		t.Fatalf("click H3 original failed: %v", err)
	}
	if acc, _ := svc.SelectedAccount(); acc.H3Variant != core.H3VariantOriginal {
		t.Fatalf("expected H3 original variant, got %q", acc.H3Variant)
	}
	if err := tt.Click("状态"); err != nil {
		t.Fatalf("return to status tab failed: %v", err)
	}

	// Onboarding wizard renders cleanly
	obSvc := makeDemoService(app.SectionStatus, true, false)
	obUI := uiapp.NewMainUI(obSvc)
	obUI.ForceTheme = "dark"
	obTester := ui.NewTester(obUI.View, 1160, 720)
	if !obTester.HasText("欢迎使用 Colab Tools") || !obTester.HasText("一键安装 google-colab-cli") {
		t.Fatalf("missing onboarding elements: %q", obTester.Texts())
	}

	// Popover companion HUD renders cleanly
	pop := uiapp.NewPopoverUI(svc)
	pop.ForceTheme = "dark"
	popTester := ui.NewTester(pop.View, 420, 620)
	if !popTester.HasText("Colab Tools") || !popTester.HasText("2374.8") {
		t.Fatalf("missing popover elements: %q", popTester.Texts())
	}
}

// TestExportOfficialShowcasePreviews exports all Retina @2x previews when COLABTOOLS_EXPORT_DIR is set.
func TestExportOfficialShowcasePreviews(t *testing.T) {
	outDir := os.Getenv("COLABTOOLS_EXPORT_DIR")
	if outDir == "" {
		t.Skip("set COLABTOOLS_EXPORT_DIR to export official @2x screenshots")
	}

	exportMain := func(sec app.MainSection, onboarding, narrow bool, theme string, w, h int, filename string) {
		svc := makeDemoService(sec, onboarding, narrow)
		mui := uiapp.NewMainUI(svc)
		mui.PreviewMode = true
		mui.ForceTheme = theme
		renderPNG(t, mui.View, w, h, filepath.Join(outDir, filename))
	}

	exportMain(app.SectionStatus, false, false, "dark", 1160, 760, "preview-main-status.png")
	exportMain(app.SectionDeploy, false, false, "dark", 1160, 950, "preview-deploy-h3.png")
	exportMain(app.SectionCompute, false, false, "dark", 1160, 1230, "preview-settings-queue.png")
	exportMain(app.SectionIntegrations, false, false, "dark", 1160, 860, "preview-settings-integrations.png")
	exportMain(app.SectionStatus, true, false, "dark", 1160, 720, "preview-onboarding.png")

	// Popover single + multi-account
	singleSvc := makeDemoService(app.SectionStatus, false, false)
	singleSvc.Settings.Accounts = singleSvc.Settings.Accounts[:1]
	popSingle := uiapp.NewPopoverUI(singleSvc)
	popSingle.ForceTheme = "dark"
	renderPNG(t, popSingle.View, 420, 620, filepath.Join(outDir, "preview-dashboard.png"))

	multiSvc := makeDemoService(app.SectionStatus, false, false)
	popMulti := uiapp.NewPopoverUI(multiSvc)
	popMulti.ForceTheme = "dark"
	popMulti.OverviewMode = true
	renderPNG(t, popMulti.View, 420, 540, filepath.Join(outDir, "preview-multi-account.png"))

	if qaDir := os.Getenv("COLABTOOLS_EXPORT_QA"); qaDir != "" {
		exportQA := func(sec app.MainSection, onboarding, narrow bool, theme string, w, h int, filename string) {
			svc := makeDemoService(sec, onboarding, narrow)
			mui := uiapp.NewMainUI(svc)
			mui.PreviewMode = true
			mui.ForceTheme = theme
			renderPNG(t, mui.View, w, h, filepath.Join(qaDir, filename))
		}
		exportQA(app.SectionStatus, false, false, "light", 1160, 760, "qa-main-status-light.png")
		exportQA(app.SectionDeploy, false, false, "light", 1160, 950, "qa-deploy-h3-light.png")
		exportQA(app.SectionStatus, true, false, "light", 1160, 720, "qa-onboarding-light.png")
		exportQA(app.SectionSettings, false, false, "dark", 1160, 740, "qa-main-settings-dark.png")
		exportQA(app.SectionStatus, false, true, "dark", 980, 680, "qa-main-status-narrow-idle.png")
	}
}
