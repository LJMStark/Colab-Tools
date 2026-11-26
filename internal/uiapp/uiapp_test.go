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

func makeDemoService(section app.MainSection, onboarding, idleHome bool) *app.Service {
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
		ColabVersion:      "0.8.0",
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

	if idleHome {
		acc := core.NewDefaultAccountProfile("Personal", "personal@example.com", core.TierAIUltra, core.AuthSystemDefaultOAuth2)
		acc.GPUStrategy = core.GPUStrategyAutoSmart
		acc.DeployRecipe = core.RecipeH3Video
		acc.H3Variant = core.H3VariantFlash
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
	acc1.GPUStrategy = core.GPUStrategyAutoSmart
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
	acc1.QueueTargets = []core.AcceleratorOption{core.AccG4, core.AccA100}
	acc1.QueueFallbackEnabled = true
	acc1.QueueFallbackAfterRounds = 2
	acc1.QueueFallbackTarget = core.AccL4
	acc1.ScheduledStartEnabled = true
	acc1.ScheduledStartHour = 7
	acc1.ScheduledStartMinute = 0
	nf := core.NextScheduledFireDate(7, 0, now)
	acc1.ScheduledNextFireAt = &nf
	acc1.AutoSyncCCSwitch = true
	acc1.CCSwitchName = "Colab Qwen3.8-27B"
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

	for i := 0; i < 8; i++ {
		svc.Insights.Record("G4", i < 7, time.Date(2026, 11, 25, 6, 30, 0, 0, time.Local))
	}
	for i := 0; i < 6; i++ {
		svc.Insights.Record("G4", i < 3, time.Date(2026, 11, 25, 2, 0, 0, 0, time.Local))
		svc.Insights.Record("G4", i < 3, time.Date(2026, 11, 25, 10, 0, 0, 0, time.Local))
		svc.Insights.Record("G4", i < 2, time.Date(2026, 11, 25, 21, 0, 0, 0, time.Local))
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
	// 1. Offline Standby 4x2 Bento: zero-config task switching + centered Overlay options + cooldown clear
	idleSvc := makeDemoService(app.SectionHome, false, true)
	accID := idleSvc.Settings.SelectedAccountID
	until := time.Now().Add(12 * time.Minute)
	rt := idleSvc.Runtime[accID]
	rt.QueueCircuitBreakTill = &until
	rt.LastErrorTitle = "Google 提示请求偏频繁，先歇一会儿保护账号"
	rt.LastError = "Google 提示请求偏频繁 (429)"
	rt.LastErrorRaw = "HTTP 429 Too Many Requests"
	idleSvc.Runtime[accID] = rt

	idleUI := uiapp.NewMainUI(idleSvc)
	idleUI.ForceTheme = "dark"
	idleTester := ui.NewTester(idleUI.View, 1160, 760)
	if !idleTester.HasText("当前任务") || !idleTester.HasText("H3 Flash 极速版") || !idleTester.HasText("一键开机并运行") || !idleTester.HasText("解除等待") {
		t.Fatalf("missing standby bento or cooldown clear controls: %q", idleTester.Texts())
	}
	if err := idleTester.Click("解除等待"); err != nil {
		t.Fatalf("click 解除等待 failed: %v", err)
	}
	if got := idleSvc.StateFor(accID); got.QueueCircuitBreakTill != nil {
		t.Fatalf("expected cooldown cleared after click")
	}
	if err := idleTester.Click("H3 原版 (31B)"); err != nil {
		t.Fatalf("click H3 原版 failed: %v", err)
	}
	if acc, _ := idleSvc.SelectedAccount(); acc.H3Variant != core.H3VariantOriginal {
		t.Fatalf("expected H3 original variant, got %q", acc.H3Variant)
	}
	if err := idleTester.Click("调整显卡与选项"); err != nil {
		t.Fatalf("click open overlay options failed: %v", err)
	}
	if !idleTester.HasText("智能挑卡") || !idleTester.HasText("省点模式") || !idleTester.HasText("高级自定义") {
		t.Fatalf("missing simplified 3-mode GPU picker in overlay: %q", idleTester.Texts())
	}
	if err := idleTester.Click("省点模式"); err != nil {
		t.Fatalf("click 省点模式 failed: %v", err)
	}
	if acc, _ := idleSvc.SelectedAccount(); acc.GPUStrategy != core.GPUStrategyEcoSaver {
		t.Fatalf("expected EcoSaver GPU strategy, got %q", acc.GPUStrategy)
	}
	if err := idleTester.Click("高级自定义"); err != nil {
		t.Fatalf("click 高级自定义 failed: %v", err)
	}
	if acc, _ := idleSvc.SelectedAccount(); acc.GPUStrategy != core.GPUStrategyCustom {
		t.Fatalf("expected Custom GPU strategy, got %q", acc.GPUStrategy)
	}
	if err := idleTester.Click("智能挑卡"); err != nil {
		t.Fatalf("click 智能挑卡 failed: %v", err)
	}
	if acc, _ := idleSvc.SelectedAccount(); acc.GPUStrategy != core.GPUStrategyAutoSmart {
		t.Fatalf("expected AutoSmart GPU strategy, got %q", acc.GPUStrategy)
	}
	if err := idleTester.Click("完成"); err != nil {
		t.Fatalf("click 完成 failed: %v", err)
	}

	// 2. Running Live Bento Console + 3-tab navigation (主页 / 账号 / 设置)
	svc := makeDemoService(app.SectionHome, false, false)
	mui := uiapp.NewMainUI(svc)
	mui.ForceTheme = "dark"
	tt := ui.NewTester(mui.View, 1160, 760)

	if !tt.HasText("2374.8") || !tt.HasText("Personal") || !tt.HasText("G4 → A100 → L4") || !tt.HasText("开 ComfyUI") {
		t.Fatalf("missing primary live dashboard text: %q", tt.Texts())
	}
	if err := tt.Click("40m"); err != nil {
		t.Fatalf("click 40m failed: %v", err)
	}
	if acc, _ := svc.SelectedAccount(); acc.IdleTimeoutMinutes != 40 || acc.QueueDrainMinutes != 40 {
		t.Fatalf("expected 40m after click, got idle=%d drain=%d", acc.IdleTimeoutMinutes, acc.QueueDrainMinutes)
	}
	for _, tab := range []string{"账号", "设置", "主页"} {
		if err := tt.Click(tab); err != nil {
			t.Fatalf("click tab %s failed: %v", tab, err)
		}
	}

	// 3. Onboarding wizard renders cleanly
	obSvc := makeDemoService(app.SectionHome, true, false)
	obUI := uiapp.NewMainUI(obSvc)
	obUI.ForceTheme = "dark"
	obTester := ui.NewTester(obUI.View, 1160, 720)
	if !obTester.HasText("欢迎使用 Colab Tools") || !obTester.HasText("一键安装 google-colab-cli") {
		t.Fatalf("missing onboarding elements: %q", obTester.Texts())
	}

	// 4. Popover companion HUD renders cleanly
	pop := uiapp.NewPopoverUI(svc)
	pop.ForceTheme = "dark"
	popTester := ui.NewTester(pop.View, 420, 620)
	if !popTester.HasText("Colab Tools") || !popTester.HasText("2374.8") {
		t.Fatalf("missing popover elements: %q", popTester.Texts())
	}

	// 5. Bare running L4 machine with H3 Original (31B): must show truthful "未部署", VRAM mismatch alert, "立即部署" & deploy log overlay — NEVER fake "启动中"
	bareSvc := makeDemoService(app.SectionHome, false, false)
	bareAcc := bareSvc.Settings.Accounts[0]
	bareAcc.H3Variant = core.H3VariantOriginal
	bareSvc.Settings.Accounts[0] = bareAcc
	bareRT := bareSvc.Runtime[bareAcc.ID]
	bareRT.ActiveHardware = "L4"
	bareRT.GPUName = "NVIDIA L4"
	bareRT.VRAMUsedMB = 0
	bareRT.VRAMTotalMB = 23034
	bareRT.GPUUtilPercent = 0
	bareRT.ServerHealthy = false
	bareRT.DeployRunning = false
	bareRT.DeployFailed = false
	bareRT.QueueRunning = 0
	bareRT.QueuePending = 0
	bareRT.RenderedVideoCount = 0
	bareRT.WorkloadDetail = ""
	bareRT.DeployLogTail = "=== 云端实时状态 ===\n部署进程: 未运行\n端口 :8188: 未监听"
	bareSvc.Runtime[bareAcc.ID] = bareRT

	bareUI := uiapp.NewMainUI(bareSvc)
	bareUI.ForceTheme = "dark"
	bareTester := ui.NewTester(bareUI.View, 1160, 760)
	if bareTester.HasText("启动中") {
		t.Fatalf("bare running machine must never show misleading 启动中: %q", bareTester.Texts())
	}
	if !bareTester.HasText("未部署") || !bareTester.HasText("立即部署") || !bareTester.HasText("查看日志") || !bareTester.HasText("改用轻量 GGUF") {
		t.Fatalf("missing truthful bare-machine deploy controls: %q", bareTester.Texts())
	}
	if err := bareTester.Click("改用轻量 GGUF"); err != nil {
		t.Fatalf("click 改用轻量 GGUF failed: %v", err)
	}
	if gotAcc, _ := bareSvc.SelectedAccount(); gotAcc.H3Variant != core.H3VariantFlashGGUF {
		t.Fatalf("expected switched to FlashGGUF, got %q", gotAcc.H3Variant)
	}
	bareUI.ShowDeployLog = true
	logTester := ui.NewTester(bareUI.View, 1160, 760)
	if !logTester.HasText("云端部署日志与状态核查") || !logTester.HasText("端口 :8188: 未监听") {
		t.Fatalf("missing deploy log overlay content: %q", logTester.Texts())
	}
}

// TestExportOfficialShowcasePreviews exports all Retina @2x previews when COLABTOOLS_EXPORT_DIR is set.
func TestExportOfficialShowcasePreviews(t *testing.T) {
	outDir := os.Getenv("COLABTOOLS_EXPORT_DIR")
	if outDir == "" {
		t.Skip("set COLABTOOLS_EXPORT_DIR to export official @2x screenshots")
	}

	// 1. Live running status console (dark 4x2 Bento)
	runSvc := makeDemoService(app.SectionHome, false, false)
	runUI := uiapp.NewMainUI(runSvc)
	runUI.PreviewMode = true
	runUI.ForceTheme = "dark"
	renderPNG(t, runUI.View, 1160, 760, filepath.Join(outDir, "preview-main-status.png"))

	// 2. Clean standby 4x2 Bento dashboard (dark, standard 1160x760)
	idleSvc := makeDemoService(app.SectionHome, false, true)
	idleUI := uiapp.NewMainUI(idleSvc)
	idleUI.PreviewMode = true
	idleUI.ForceTheme = "dark"
	renderPNG(t, idleUI.View, 1160, 760, filepath.Join(outDir, "preview-deploy-h3.png"))

	// 3. Centered Overlay options inspector over 4x2 Bento
	customSvc := makeDemoService(app.SectionHome, false, true)
	cAcc := customSvc.Settings.Accounts[0]
	cAcc.GPUStrategy = core.GPUStrategyCustom
	cAcc.QueueTargets = []core.AcceleratorOption{core.AccG4, core.AccA100}
	cAcc.QueueFallbackEnabled = true
	cAcc.QueueFallbackAfterRounds = 2
	cAcc.QueueFallbackTarget = core.AccL4
	cAcc.ScheduledStartEnabled = true
	cAcc.ScheduledStartHour = 7
	cAcc.ScheduledStartMinute = 0
	nf := core.NextScheduledFireDate(7, 0, time.Now())
	cAcc.ScheduledNextFireAt = &nf
	customSvc.Settings.Accounts[0] = cAcc
	customUI := uiapp.NewMainUI(customSvc)
	customUI.PreviewMode = true
	customUI.ForceTheme = "dark"
	customUI.ShowHomeOptions = true
	renderPNG(t, customUI.View, 1160, 760, filepath.Join(outDir, "preview-settings-queue.png"))

	// 4. LLM Proxy mode with centered Overlay integrations inspector (1160x760)
	llmSvc := makeDemoService(app.SectionHome, false, true)
	llmSvc.ApplyDeployRecipe(llmSvc.Settings.Accounts[0].ID, core.RecipeLLMProxy)
	llmUI := uiapp.NewMainUI(llmSvc)
	llmUI.PreviewMode = true
	llmUI.ForceTheme = "dark"
	llmUI.ShowHomeOptions = true
	renderPNG(t, llmUI.View, 1160, 760, filepath.Join(outDir, "preview-settings-integrations.png"))

	// 5. Onboarding wizard
	obSvc := makeDemoService(app.SectionHome, true, false)
	obUI := uiapp.NewMainUI(obSvc)
	obUI.PreviewMode = true
	obUI.ForceTheme = "dark"
	renderPNG(t, obUI.View, 1160, 720, filepath.Join(outDir, "preview-onboarding.png"))

	// 6. Popover single + multi-account
	singleSvc := makeDemoService(app.SectionHome, false, false)
	singleSvc.Settings.Accounts = singleSvc.Settings.Accounts[:1]
	popSingle := uiapp.NewPopoverUI(singleSvc)
	popSingle.ForceTheme = "dark"
	renderPNG(t, popSingle.View, 420, 620, filepath.Join(outDir, "preview-dashboard.png"))

	multiSvc := makeDemoService(app.SectionHome, false, false)
	popMulti := uiapp.NewPopoverUI(multiSvc)
	popMulti.ForceTheme = "dark"
	popMulti.OverviewMode = true
	renderPNG(t, popMulti.View, 420, 540, filepath.Join(outDir, "preview-multi-account.png"))

	if qaDir := os.Getenv("COLABTOOLS_EXPORT_QA"); qaDir != "" {
		idleLight := uiapp.NewMainUI(makeDemoService(app.SectionHome, false, true))
		idleLight.PreviewMode = true
		idleLight.ForceTheme = "light"
		renderPNG(t, idleLight.View, 1160, 760, filepath.Join(qaDir, "qa-home-idle-light.png"))

		setLight := uiapp.NewMainUI(makeDemoService(app.SectionSettings, false, true))
		setLight.PreviewMode = true
		setLight.ForceTheme = "light"
		renderPNG(t, setLight.View, 1160, 760, filepath.Join(qaDir, "qa-settings-light.png"))

		bareSvc := makeDemoService(app.SectionHome, false, false)
		bAcc := bareSvc.Settings.Accounts[0]
		bAcc.H3Variant = core.H3VariantOriginal
		bareSvc.Settings.Accounts[0] = bAcc
		bRT := bareSvc.Runtime[bAcc.ID]
		bRT.ActiveHardware = "L4"
		bRT.GPUName = "NVIDIA L4"
		bRT.VRAMUsedMB = 0
		bRT.VRAMTotalMB = 23034
		bRT.GPUUtilPercent = 0
		bRT.CURatePerHour = 2.33
		bRT.ServerHealthy = false
		bRT.DeployRunning = false
		bRT.QueueRunning = 0
		bRT.QueuePending = 0
		bRT.RenderedVideoCount = 0
		bRT.WorkloadDetail = ""
		bRT.DeployLogTail = "=== 云端实时状态 ===\n部署进程: 未运行\n端口 :8188: 未监听\nComfyUI 目录: 尚未安装\nGoogle Drive: 未挂载\n\n云端机器上目前没有任何部署记录。\n这台机器是刚开机或之前启动的裸机，尚未安装 ComfyUI，点击「立即部署」即可开始自动安装。"
		bareSvc.Runtime[bAcc.ID] = bRT

		bareUI := uiapp.NewMainUI(bareSvc)
		bareUI.PreviewMode = true
		bareUI.ForceTheme = "dark"
		renderPNG(t, bareUI.View, 1160, 760, filepath.Join(qaDir, "qa-bare-l4-dark.png"))

		bareUI.ShowDeployLog = true
		renderPNG(t, bareUI.View, 1160, 760, filepath.Join(qaDir, "qa-deploy-log-overlay.png"))
	}
}
