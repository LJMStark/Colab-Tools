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
	rt1.RAMUsedMB = 18432
	rt1.RAMTotalMB = 85504
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

	if svc.History != nil {
		e1 := now.Add(-2 * time.Hour)
		s1 := e1.Add(-96 * time.Minute)
		svc.History.AddRecord(core.HistoryRecord{
			Kind:           core.HistoryKindSession,
			AccountID:      acc1.ID,
			AccountName:    acc1.DisplayName,
			AccountColor:   acc1.ColorTag,
			SessionName:    acc1.SessionName,
			StartedAt:      s1,
			EndedAt:        &e1,
			DurationSec:    96 * 60,
			Hardware:       "NVIDIA A100-SXM4-80GB",
			HighMem:        true,
			RecipeLabel:    "H3 Flash 极速版",
			ConsumedCU:     14.2,
			BillingBasis:   core.BillingMeasured,
			ShutdownReason: core.ShutdownAutoIdle,
			Detail:         "队列清空且持续空闲后自动关机止损",
		})

		e2 := now.Add(-10 * time.Hour)
		s2 := e2.Add(-110 * time.Minute)
		svc.History.AddRecord(core.HistoryRecord{
			Kind:           core.HistoryKindSession,
			AccountID:      acc1.ID,
			AccountName:    acc1.DisplayName,
			AccountColor:   acc1.ColorTag,
			SessionName:    acc1.SessionName,
			StartedAt:      s2,
			EndedAt:        &e2,
			DurationSec:    110 * 60,
			Hardware:       "NVIDIA RTX PRO 6000 (G4)",
			HighMem:        true,
			RecipeLabel:    "H3 原版 (31B)",
			ConsumedCU:     21.6,
			BillingBasis:   core.BillingEstimated,
			ShutdownReason: core.ShutdownManual,
			Detail:         "手动停止会话释放算力",
		})

		e3 := now.Add(-26 * time.Hour)
		s3 := e3.Add(-118 * time.Minute)
		svc.History.AddRecord(core.HistoryRecord{
			Kind:            core.HistoryKindSession,
			AccountID:       acc1.ID,
			AccountName:     acc1.DisplayName,
			AccountColor:    acc1.ColorTag,
			SessionName:     acc1.SessionName,
			StartedAt:       s3,
			EndedAt:         &e3,
			DurationSec:     118 * 60,
			Hardware:        "NVIDIA L4",
			RecipeLabel:     "H3 原版 → 轻量 GGUF",
			ConsumedCU:      4.6,
			BillingBasis:    core.BillingMeasured,
			ShutdownReason:  core.ShutdownAutoIdle,
			FallbackUsed:    true,
			ModelDowngraded: true,
			Detail:          "大卡 2 轮无货，L4 保底并自动切换轻量",
		})

		svc.History.AddRecord(core.HistoryRecord{
			Kind:           core.HistoryKindAllocation,
			AccountID:      acc1.ID,
			AccountName:    acc1.DisplayName,
			AccountColor:   acc1.ColorTag,
			SessionName:    acc1.SessionName,
			StartedAt:      now.Add(-30 * time.Hour),
			DurationSec:    35 * 60,
			TargetsSummary: "G4 → A100",
			Rounds:         5,
			AllocOutcome:   core.AllocTagShortage,
			Detail:         "G4 / A100 暂无库存 (503)，已停止排队",
		})

		svc.History.AddRecord(core.HistoryRecord{
			Kind:           core.HistoryKindAllocation,
			AccountID:      acc2.ID,
			AccountName:    acc2.DisplayName,
			AccountColor:   acc2.ColorTag,
			SessionName:    acc2.SessionName,
			StartedAt:      now.Add(-36 * time.Hour),
			DurationSec:    185,
			Hardware:       "NVIDIA L4",
			TargetsSummary: "L4 → T4",
			Rounds:         2,
			AllocOutcome:   core.AllocTagSuccess,
			Detail:         "第 2 轮成功分配 NVIDIA L4",
		})
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
	// 1. Offline Standby 3x2 Bento (single account, zero bottom table, zero duplicates)
	cleanIdleSvc := makeDemoService(app.SectionHome, false, true)
	cleanIdleUI := uiapp.NewMainUI(cleanIdleSvc)
	cleanIdleUI.ForceTheme = "dark"
	cleanIdleTester := ui.NewTester(cleanIdleUI.View, 1160, 760)
	for _, want := range []string{
		"CU 余额与速率", "实时负载", "当前任务", "空闲自动关机", "挑卡与守候",
		"调整策略与进阶选项", "›", "点击开机", "账单 ›", "H3 Flash 极速版",
	} {
		if !cleanIdleTester.HasText(want) {
			t.Fatalf("missing %q on standby 3x2 dashboard: %q", want, cleanIdleTester.Texts())
		}
	}
	for _, unwanted := range []string{"算力续航", "本机通道", "更多玩法", "账号与活跃会话 (1)"} {
		if cleanIdleTester.HasText(unwanted) {
			t.Fatalf("unexpected redundant element %q on single-account standby dashboard: %q", unwanted, cleanIdleTester.Texts())
		}
	}

	// Cooldown clear + task switching + simplified options overlay
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
	if !idleTester.HasText("解除等待") {
		t.Fatalf("missing cooldown clear button: %q", idleTester.Texts())
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
	if err := idleTester.Click("调整策略与进阶选项"); err != nil {
		t.Fatalf("click open overlay options failed: %v", err)
	}
	for _, want := range []string{
		"任务玩法", "其他进阶玩法：", "自定义 HF / 网盘模型", "通用 ComfyUI", "纯开机",
		"智能挑卡", "省点模式", "高级自定义", "成片自动存进 Google Drive",
	} {
		if !idleTester.HasText(want) {
			t.Fatalf("missing %q in simplified options overlay: %q", want, idleTester.Texts())
		}
	}
	for _, unwanted := range []string{
		"跑完自动关机省点（全应用统一设置）",
		"同时生成 cloudflared 跨设备临时链接（可选）",
		"开启 sol-attn 稀疏加速（推荐）",
		"去 Drive 看成片",
		"装好后自动通过本机加密通道打开 ComfyUI（推荐）",
		"首次下载完 21.5GB 底座后，顺手存一份回 Drive",
	} {
		if idleTester.HasText(unwanted) {
			t.Fatalf("unexpected removed option %q in overlay: %q", unwanted, idleTester.Texts())
		}
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
	if idleTester.HasText("目标显卡缺货时自动排队重试") {
		t.Fatalf("removed EnablePoliteQueue toggle should not appear in custom GPU strategy: %q", idleTester.Texts())
	}
	if !idleTester.HasText("定时预约适合明天低峰自动开，持续守候适合现在挂着等卡（错过超 10 分钟自动过期不误启）") {
		t.Fatalf("missing mutual-exclusion hint next to scheduled start: %q", idleTester.Texts())
	}
	if err := idleTester.Click("智能挑卡"); err != nil {
		t.Fatalf("click 智能挑卡 failed: %v", err)
	}
	if acc, _ := idleSvc.SelectedAccount(); acc.GPUStrategy != core.GPUStrategyAutoSmart {
		t.Fatalf("expected AutoSmart GPU strategy, got %q", acc.GPUStrategy)
	}
	if !idleTester.HasText("大卡 2 轮无货时，允许保底开 L4 并自动切轻量 GGUF") {
		t.Fatalf("missing 31B cross-model fallback toggle in auto_smart options: %q", idleTester.Texts())
	}
	if err := idleTester.Click("大卡 2 轮无货时，允许保底开 L4 并自动切轻量 GGUF"); err != nil {
		t.Fatalf("click 31B cross-model fallback toggle failed: %v", err)
	}
	if acc, _ := idleSvc.SelectedAccount(); !acc.AllowCrossModelFallback {
		t.Fatalf("expected AllowCrossModelFallback enabled after click")
	}
	if err := idleTester.Click("完成"); err != nil {
		t.Fatalf("click 完成 failed: %v", err)
	}
	if !idleTester.HasText("2轮保底 L4") {
		t.Fatalf("expected 2轮保底 L4 badge on Bento strategy card: %q", idleTester.Texts())
	}

	// 1b. 31B shortage error banner offers one-click "改用 L4 轻量版" and "预约 07:00 抢卡"
	shortSvc := makeDemoService(app.SectionHome, false, true)
	shortAccID := shortSvc.Settings.SelectedAccountID
	shortAcc := shortSvc.Settings.Accounts[0]
	shortAcc.DeployRecipe = core.RecipeH3Video
	shortAcc.H3Variant = core.H3VariantOriginal
	shortSvc.Settings.Accounts[0] = shortAcc
	shortRT := shortSvc.Runtime[shortAccID]
	shortRT.LastErrorTitle = "大卡暂时无货"
	shortRT.LastError = "A100/G4 当前暂无库存 (503)"
	shortRT.LastAllocHadShortage = true
	shortSvc.Runtime[shortAccID] = shortRT
	shortUI := uiapp.NewMainUI(shortSvc)
	shortUI.ForceTheme = "dark"
	shortTester := ui.NewTester(shortUI.View, 1160, 760)
	if !shortTester.HasText("改用 L4 轻量版") || !shortTester.HasText("预约 07:00 抢卡") {
		t.Fatalf("missing 31B shortage fallback recovery actions: %q", shortTester.Texts())
	}

	// 2. Running Live 3x2 Bento Console + Multi-Account Table + Confirm-Stop Modal + 4-tab navigation
	svc := makeDemoService(app.SectionHome, false, false)
	mui := uiapp.NewMainUI(svc)
	mui.ForceTheme = "dark"
	tt := ui.NewTester(mui.View, 1160, 760)

	for _, want := range []string{
		"2374.8", "Personal", "G4 → A100", "2轮保底 L4", "开 ComfyUI", "后台",
		"CU 余额与速率", "实时负载", "当前任务", "空闲自动关机", "挑卡与守候",
		"调整策略与进阶选项", "点此关机", "账单 ›", "账号与活跃会话 (3)",
	} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q on running 3x2 dashboard: %q", want, tt.Texts())
		}
	}
	for _, unwanted := range []string{"算力续航", "本机通道", "更多玩法"} {
		if tt.HasText(unwanted) {
			t.Fatalf("unexpected removed element %q on running dashboard: %q", unwanted, tt.Texts())
		}
	}
	if err := tt.Click("40m"); err != nil {
		t.Fatalf("click 40m failed: %v", err)
	}
	if acc, _ := svc.SelectedAccount(); acc.IdleTimeoutMinutes != 40 || acc.QueueDrainMinutes != 40 {
		t.Fatalf("expected 40m after click, got idle=%d drain=%d", acc.IdleTimeoutMinutes, acc.QueueDrainMinutes)
	}
	if err := tt.Click("点此关机"); err != nil {
		t.Fatalf("click 点此关机 failed: %v", err)
	}
	if p := svc.CurrentActionPrompt(); p == nil || p.Kind != core.PromptConfirmStop {
		t.Fatalf("expected PromptConfirmStop modal after clicking 点此关机, got %+v", p)
	}
	if !tt.HasText("确认关机止损") || !tt.HasText("继续运行") {
		t.Fatalf("missing confirm-stop modal buttons: %q", tt.Texts())
	}
	if err := tt.Click("继续运行"); err != nil {
		t.Fatalf("click 继续运行 failed: %v", err)
	}
	if svc.CurrentActionPrompt() != nil {
		t.Fatalf("expected PromptConfirmStop dismissed after clicking 继续运行")
	}

	if err := tt.Click("记录"); err != nil {
		t.Fatalf("click tab 记录 failed: %v", err)
	}
	for _, want := range []string{"累计消耗 CU", "看门狗止损", "算力账单", "抢卡记录", "导出 CSV"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q on history tab: %q", want, tt.Texts())
		}
	}
	for _, filterBtn := range []string{"算力账单", "抢卡记录", "近 30 天", "近 7 天", "全部"} {
		if err := tt.Click(filterBtn); err != nil {
			t.Fatalf("click history filter %q failed: %v", filterBtn, err)
		}
	}
	if err := tt.Click("账号"); err != nil {
		t.Fatalf("click tab 账号 failed: %v", err)
	}
	if tt.HasText("导入终端已登账号") {
		t.Fatalf("导入终端已登账号 must be collapsed inside + 绑定新账号 panel initially: %q", tt.Texts())
	}
	if err := tt.Click("+ 绑定新账号"); err != nil {
		t.Fatalf("click + 绑定新账号 failed: %v", err)
	}
	if !tt.HasText("导入终端已登账号") {
		t.Fatalf("expected 导入终端已登账号 inside expanded + 绑定新账号 panel: %q", tt.Texts())
	}

	if err := tt.Click("设置"); err != nil {
		t.Fatalf("click tab 设置 failed: %v", err)
	}
	if err := tt.Click("展开高级参数 ▾"); err != nil {
		t.Fatalf("click 展开高级参数 ▾ failed: %v", err)
	}
	if !tt.HasText("Drive 成片目录") || !tt.HasText("Drive 模型目录") || !tt.HasText("换机自动恢复自定义节点快照") {
		t.Fatalf("expected Drive & snapshot settings in advanced section: %q", tt.Texts())
	}
	if err := tt.Click("换机自动恢复自定义节点快照"); err != nil {
		t.Fatalf("click 换机自动恢复自定义节点快照 failed: %v", err)
	}
	if acc, ok := svc.SelectedAccount(); !ok || !acc.RestoreCustomNodesSnapshot {
		t.Fatalf("expected RestoreCustomNodesSnapshot=true after click, got %+v", acc.RestoreCustomNodesSnapshot)
	}
	for _, unwanted := range []string{
		"解除冷静等待 / 清空报错",
		"非 LLM 场景开启 GPU(<5%) + CPU(<25%) 双低载兜底关机（下载/转码防误杀）",
	} {
		if tt.HasText(unwanted) {
			t.Fatalf("unexpected removed settings control %q: %q", unwanted, tt.Texts())
		}
	}
	if !tt.HasText("远端端口") || !tt.HasText("本地通道端口") {
		t.Fatalf("expected port inputs when DriveScriptPath is non-empty: %q", tt.Texts())
	}
	if acc, ok := svc.SelectedAccount(); ok {
		acc.DriveScriptPath = ""
		svc.UpdateAccount(acc)
		tt.Frame()
	}
	if tt.HasText("远端端口") || tt.HasText("本地通道端口") {
		t.Fatalf("port inputs must hide when DriveScriptPath is empty: %q", tt.Texts())
	}

	if err := tt.Click("主页"); err != nil {
		t.Fatalf("click tab 主页 failed: %v", err)
	}

	// 3. Onboarding wizard renders cleanly
	obSvc := makeDemoService(app.SectionHome, true, false)
	obUI := uiapp.NewMainUI(obSvc)
	obUI.ForceTheme = "dark"
	obTester := ui.NewTester(obUI.View, 1160, 720)
	if !obTester.HasText("欢迎使用 Colab Tools") || !obTester.HasText("一键安装 google-colab-cli") {
		t.Fatalf("missing onboarding elements: %q", obTester.Texts())
	}

	// 4. Popover companion HUD renders cleanly (idle hides 0% cards; running shows 续命 only when AutoShutdownEnabled; zero SSH/Browser/大屏)
	idlePop := uiapp.NewPopoverUI(cleanIdleSvc)
	idlePop.ForceTheme = "dark"
	idlePopTester := ui.NewTester(idlePop.View, 420, 560)
	for _, want := range []string{"Colab Tools", "已关机 · 0.00 CU/h", "一键开机", "主控制台", "记录", "设置", "退出"} {
		if !idlePopTester.HasText(want) {
			t.Fatalf("missing %q on idle popover: %q", want, idlePopTester.Texts())
		}
	}
	for _, unwanted := range []string{"SSH 隧道", "断隧道", "浏览器", "大屏", "显存", "GPU", "续命"} {
		if idlePopTester.HasText(unwanted) {
			t.Fatalf("unexpected element %q on idle popover: %q", unwanted, idlePopTester.Texts())
		}
	}

	pop := uiapp.NewPopoverUI(svc)
	pop.ForceTheme = "dark"
	popTester := ui.NewTester(pop.View, 420, 620)
	for _, want := range []string{"Colab Tools", "2374.8", "⏻ 关机止损", "显存", "GPU", "续命", "记录"} {
		if !popTester.HasText(want) {
			t.Fatalf("missing %q on running popover: %q", want, popTester.Texts())
		}
	}
	for _, unwanted := range []string{"SSH 隧道", "断隧道", "浏览器", "大屏"} {
		if popTester.HasText(unwanted) {
			t.Fatalf("unexpected removed button %q on running popover: %q", unwanted, popTester.Texts())
		}
	}
	popNoAutoSvc := makeDemoService(app.SectionHome, false, false)
	popNoAutoAcc := popNoAutoSvc.Settings.Accounts[0]
	popNoAutoAcc.AutoShutdownEnabled = false
	popNoAutoSvc.UpdateAccount(popNoAutoAcc)
	popNoAuto := uiapp.NewPopoverUI(popNoAutoSvc)
	popNoAuto.ForceTheme = "dark"
	popNoAutoTester := ui.NewTester(popNoAuto.View, 420, 620)
	if popNoAutoTester.HasText("续命") {
		t.Fatalf("续命 must be hidden when AutoShutdownEnabled=false: %q", popNoAutoTester.Texts())
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
	for _, want := range []string{
		"云端后台与日志", "Colab 网页", ">_ 云端终端", "Drive 目录",
		"Drive 标准目录", "端口 :8188: 未监听",
	} {
		if !logTester.HasText(want) {
			t.Fatalf("missing deploy log overlay content %q: %q", want, logTester.Texts())
		}
	}

	// 6. Persistent queue toggle in options drawer (single 8h toggle, zero 4h/12h buttons), ComfyOpening disabled feedback, and persistent queuing labels (never 7/5)
	pqSvc := makeDemoService(app.SectionHome, false, true)
	pqAcc := pqSvc.Settings.Accounts[0]
	pqAcc.AutoShutdownEnabled = false
	pqSvc.Settings.Accounts[0] = pqAcc
	pqUI := uiapp.NewMainUI(pqSvc)
	pqUI.ForceTheme = "dark"
	pqTester := ui.NewTester(pqUI.View, 1160, 760)
	if err := pqTester.Click("调整策略与进阶选项"); err != nil {
		t.Fatalf("click 调整策略与进阶选项 failed: %v", err)
	}
	if !pqTester.HasText("抢不到就持续守候（低频挂机，最长 8 小时）") {
		t.Fatalf("missing persistent queue switch in options drawer: %q", pqTester.Texts())
	}
	if err := pqTester.Click("抢不到就持续守候（低频挂机，最长 8 小时）"); err != nil {
		t.Fatalf("click persistent queue switch failed: %v", err)
	}
	if gotAcc, _ := pqSvc.SelectedAccount(); !gotAcc.QueuePersistentMode || !gotAcc.AutoShutdownEnabled || gotAcc.QueuePersistentMaxHours != 8 {
		t.Fatalf("expected persistent mode + auto-shutdown + default 8h enabled, got persistent=%v autoShutdown=%v maxH=%d",
			gotAcc.QueuePersistentMode, gotAcc.AutoShutdownEnabled, gotAcc.QueuePersistentMaxHours)
	}
	for _, unwanted := range []string{"4 小时", "8 小时（推荐）", "12 小时"} {
		if pqTester.HasText(unwanted) {
			t.Fatalf("unexpected redundant persistent duration button %q: %q", unwanted, pqTester.Texts())
		}
	}
	if err := pqTester.Click("完成"); err != nil {
		t.Fatalf("click 完成 failed: %v", err)
	}
	if !pqTester.HasText("守候 8h") {
		t.Fatalf("expected 守候 8h badge on strategy card: %q", pqTester.Texts())
	}

	// Standard queue banner quick switch to persistent queue
	stdQSvc := makeDemoService(app.SectionHome, false, true)
	stdQAcc := stdQSvc.Settings.Accounts[0]
	stdQAcc.QueuePersistentMode = false
	stdQSvc.Settings.Accounts[0] = stdQAcc
	stdQRT := stdQSvc.Runtime[stdQAcc.ID]
	stdQRT.Phase = core.InstancePhase{
		Kind:           core.PhaseQueuing,
		QueueAttempt:   2,
		QueueMax:       5,
		TargetsSummary: "G4 → A100",
	}
	stdQSvc.Runtime[stdQAcc.ID] = stdQRT
	stdQUI := uiapp.NewMainUI(stdQSvc)
	stdQUI.ForceTheme = "dark"
	stdQTester := ui.NewTester(stdQUI.View, 1160, 760)
	if !stdQTester.HasText("切持续守候") {
		t.Fatalf("missing quick 切持续守候 button on standard queue banner: %q", stdQTester.Texts())
	}
	if err := stdQTester.Click("切持续守候"); err != nil {
		t.Fatalf("click 切持续守候 failed: %v", err)
	}
	if gotAcc, _ := stdQSvc.SelectedAccount(); !gotAcc.QueuePersistentMode {
		t.Fatalf("expected QueuePersistentMode enabled after clicking 切持续守候")
	}

	// Persistent queuing state must display 守候 / 第 7 轮 and NEVER 7/5
	perQSvc := makeDemoService(app.SectionHome, false, true)
	perQAcc := perQSvc.Settings.Accounts[0]
	perQAcc.QueuePersistentMode = true
	perQAcc.QueuePersistentMaxHours = 8
	perQSvc.Settings.Accounts[0] = perQAcc
	perQRT := perQSvc.Runtime[perQAcc.ID]
	nextTry := time.Now().Add(9*time.Minute + 25*time.Second)
	perQRT.Phase = core.InstancePhase{
		Kind:            core.PhaseQueuing,
		QueueAttempt:    7,
		QueueMax:        5,
		QueuePersistent: true,
		QueueMaxHours:   8,
		NextRetryAt:     &nextTry,
		TargetsSummary:  "G4 → A100",
	}
	perQSvc.Runtime[perQAcc.ID] = perQRT
	perQUI := uiapp.NewMainUI(perQSvc)
	perQUI.ForceTheme = "dark"
	perQTester := ui.NewTester(perQUI.View, 1160, 760)
	if !perQTester.HasText("守候") || !perQTester.HasText("第 7 轮") || !perQTester.HasText("守候 第7轮") {
		t.Fatalf("missing persistent queue labels: %q", perQTester.Texts())
	}
	if perQTester.HasText("7/5") {
		t.Fatalf("persistent queue UI must never display 7/5: %q", perQTester.Texts())
	}

	// ComfyOpening=true shows 连接中… on both MainUI and PopoverUI
	openSvc := makeDemoService(app.SectionHome, false, false)
	openAccID := openSvc.Settings.SelectedAccountID
	openRT := openSvc.Runtime[openAccID]
	openRT.ComfyOpening = true
	openSvc.Runtime[openAccID] = openRT
	openUI := uiapp.NewMainUI(openSvc)
	openUI.ForceTheme = "dark"
	openTester := ui.NewTester(openUI.View, 1160, 760)
	if !openTester.HasText("连接中…") {
		t.Fatalf("expected 连接中… on MainUI when ComfyOpening=true: %q", openTester.Texts())
	}

	// 7. Top Power Hero Strip: clear offline/online power indicator + HH:MM:SS stopwatch + machine/RAM + available time + Google reclaim banner
	offSvc := makeDemoService(app.SectionHome, false, true)
	offAccID := offSvc.Settings.SelectedAccountID
	offRT := offSvc.Runtime[offAccID]
	offRT.LastUnexpectedStopMsg = "刚才的 L4 在 20:48 被 Google 服务端回收停机（非本软件关机），点右侧可立即重开"
	offSvc.Runtime[offAccID] = offRT
	offUI := uiapp.NewMainUI(offSvc)
	offUI.ForceTheme = "dark"
	offTester := ui.NewTester(offUI.View, 1160, 820)
	if !offTester.HasText("已关机") || !offTester.HasText("点击开机") || !offTester.HasText("00:00:00") || !offTester.HasText("未分配机器") || !offTester.HasText("预计可用时间") || !offTester.HasText("上次云端机器已被 Google 自动回收停机") {
		t.Fatalf("missing offline power hero strip or reclaim banner elements: %q", offTester.Texts())
	}
	if offTester.HasText("待机就绪") || offTester.HasText("已配齐") {
		t.Fatalf("offline dashboard must not show misleading 待机就绪/已配齐 labels: %q", offTester.Texts())
	}

	runHeroSvc := makeDemoService(app.SectionHome, false, false)
	runHeroUI := uiapp.NewMainUI(runHeroSvc)
	runHeroUI.ForceTheme = "dark"
	runHeroTester := ui.NewTester(runHeroUI.View, 1160, 820)
	if !runHeroTester.HasText("已开机") || !runHeroTester.HasText("01:18:00") || !runHeroTester.HasText("本轮可用时间") || !runHeroTester.HasText("内存 18.0/84 GB") {
		t.Fatalf("missing running power hero strip stopwatch/RAM/available-time elements: %q", runHeroTester.Texts())
	}
	openPop := uiapp.NewPopoverUI(openSvc)
	openPop.ForceTheme = "dark"
	openPopTester := ui.NewTester(openPop.View, 420, 620)
	if !openPopTester.HasText("连接中…") {
		t.Fatalf("expected 连接中… on PopoverUI when ComfyOpening=true: %q", openPopTester.Texts())
	}

	// 7. Action prompt modal overlay (PromptDriveAuth): title, message, open_url & primary resolve callbacks
	promptSvc := makeDemoService(app.SectionHome, false, false)
	var openedURL string
	promptSvc.URLOpener = func(u string) {
		openedURL = u
	}
	promptSvc.PresentActionPrompt(&core.UserActionPrompt{
		ID:             "drive-auth-test",
		Kind:           core.PromptDriveAuth,
		AccountID:      promptSvc.Settings.SelectedAccountID,
		Title:          "需要你点一下「允许」连接 Google Drive",
		Message:        "已在浏览器中打开 Google 授权页。请在网页里点「允许」挂载云盘后，回来点击下方绿色/琥珀主按钮继续部署。",
		URL:            "https://accounts.google.com/o/oauth2/auth?mock=1",
		PrimaryLabel:   "我已点完允许，继续",
		SecondaryLabel: "这次先不挂，纯云端跑",
		CancelLabel:    "取消",
	})
	promptUI := uiapp.NewMainUI(promptSvc)
	promptUI.ForceTheme = "dark"
	promptTester := ui.NewTester(promptUI.View, 1160, 760)
	for _, want := range []string{
		"需要你点一下「允许」连接 Google Drive",
		"浏览器没弹出？点此重新打开授权页",
		"我已点完允许，继续",
		"这次先不挂，纯云端跑",
		"取消",
	} {
		if !promptTester.HasText(want) {
			t.Fatalf("missing action prompt modal text %q: %q", want, promptTester.Texts())
		}
	}
	if err := promptTester.Click("浏览器没弹出？点此重新打开授权页"); err != nil {
		t.Fatalf("click reopen url failed: %v", err)
	}
	if openedURL != "https://accounts.google.com/o/oauth2/auth?mock=1" || promptSvc.CurrentActionPrompt() == nil {
		t.Fatalf("expected open_url callback without closing prompt, got url=%q prompt=%v", openedURL, promptSvc.CurrentActionPrompt())
	}
	if err := promptTester.Click("我已点完允许，继续"); err != nil {
		t.Fatalf("click primary button failed: %v", err)
	}
	if promptSvc.CurrentActionPrompt() != nil {
		t.Fatalf("expected action prompt cleared after clicking primary button")
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

	// 5b. History & Consumption Ledger tab (dark 1160x760)
	histSvc := makeDemoService(app.SectionHistory, false, false)
	histUI := uiapp.NewMainUI(histSvc)
	histUI.PreviewMode = true
	histUI.ForceTheme = "dark"
	renderPNG(t, histUI.View, 1160, 760, filepath.Join(outDir, "preview-history.png"))

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

		histLight := uiapp.NewMainUI(makeDemoService(app.SectionHistory, false, false))
		histLight.PreviewMode = true
		histLight.ForceTheme = "light"
		renderPNG(t, histLight.View, 1160, 760, filepath.Join(qaDir, "qa-history-light.png"))

		shortSvc := makeDemoService(app.SectionHome, false, true)
		sAcc := shortSvc.Settings.Accounts[0]
		sAcc.DeployRecipe = core.RecipeH3Video
		sAcc.H3Variant = core.H3VariantOriginal
		shortSvc.Settings.Accounts[0] = sAcc
		sRT := shortSvc.Runtime[sAcc.ID]
		sRT.LastErrorTitle = "大卡暂时无货"
		sRT.LastError = "G4 / A100 当前暂无库存 (503)，已停止排队"
		sRT.LastAllocHadShortage = true
		shortSvc.Runtime[sAcc.ID] = sRT
		shortUI := uiapp.NewMainUI(shortSvc)
		shortUI.PreviewMode = true
		shortUI.ForceTheme = "dark"
		renderPNG(t, shortUI.View, 1160, 760, filepath.Join(qaDir, "qa-shortage-fallback-dark.png"))

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

		perQSvc := makeDemoService(app.SectionHome, false, true)
		pAcc := perQSvc.Settings.Accounts[0]
		pAcc.QueuePersistentMode = true
		pAcc.QueuePersistentMaxHours = 8
		perQSvc.Settings.Accounts[0] = pAcc
		pRT := perQSvc.Runtime[pAcc.ID]
		nextTry := time.Now().Add(9*time.Minute + 25*time.Second)
		pRT.Phase = core.InstancePhase{
			Kind:            core.PhaseQueuing,
			QueueAttempt:    7,
			QueueMax:        5,
			QueuePersistent: true,
			QueueMaxHours:   8,
			NextRetryAt:     &nextTry,
			TargetsSummary:  "G4 → A100",
		}
		perQSvc.Runtime[pAcc.ID] = pRT
		perQUI := uiapp.NewMainUI(perQSvc)
		perQUI.PreviewMode = true
		perQUI.ForceTheme = "dark"
		renderPNG(t, perQUI.View, 1160, 760, filepath.Join(qaDir, "qa-persistent-queue-dark.png"))

		promptSvc := makeDemoService(app.SectionHome, false, false)
		promptSvc.PresentActionPrompt(&core.UserActionPrompt{
			ID:             "qa-drive-auth",
			Kind:           core.PromptDriveAuth,
			AccountID:      promptSvc.Settings.SelectedAccountID,
			Title:          "需要你点一下「允许」连接 Google Drive",
			Message:        "已在浏览器中打开 Google 授权页。请在网页里点「允许」把 Google Drive 挂载进云端机器，完成后回来点下方按钮继续安装 ComfyUI。",
			URL:            "https://accounts.google.com/o/oauth2/auth?demo=1",
			PrimaryLabel:   "我已点完允许，继续",
			SecondaryLabel: "这次先不挂，纯云端跑",
			CancelLabel:    "取消",
		})
		promptUI := uiapp.NewMainUI(promptSvc)
		promptUI.PreviewMode = true
		promptUI.ForceTheme = "dark"
		renderPNG(t, promptUI.View, 1160, 760, filepath.Join(qaDir, "qa-drive-auth-modal-dark.png"))

		reclaimSvc := makeDemoService(app.SectionHome, false, true)
		rAccID := reclaimSvc.Settings.SelectedAccountID
		rRT := reclaimSvc.Runtime[rAccID]
		rRT.LastUnexpectedStopMsg = "刚才的 L4 在 20:48 被 Google 服务端回收停机（非本软件关机），点右侧可立即重开"
		reclaimSvc.Runtime[rAccID] = rRT
		reclaimUI := uiapp.NewMainUI(reclaimSvc)
		reclaimUI.PreviewMode = true
		reclaimUI.ForceTheme = "dark"
		renderPNG(t, reclaimUI.View, 1160, 860, filepath.Join(qaDir, "qa-power-strip-offline-dark.png"))

		runHeroQA := uiapp.NewMainUI(makeDemoService(app.SectionHome, false, false))
		runHeroQA.PreviewMode = true
		runHeroQA.ForceTheme = "dark"
		renderPNG(t, runHeroQA.View, 1160, 860, filepath.Join(qaDir, "qa-power-strip-running-dark.png"))

		// v0.3.16 key QA screenshots
		v0316OffUI := uiapp.NewMainUI(makeDemoService(app.SectionHome, false, true))
		v0316OffUI.PreviewMode = true
		v0316OffUI.ForceTheme = "dark"
		renderPNG(t, v0316OffUI.View, 1160, 760, filepath.Join(qaDir, "qa-v0316-offline-dark.png"))

		v0316RunSvc := makeDemoService(app.SectionHome, false, false)
		v0316RunUI := uiapp.NewMainUI(v0316RunSvc)
		v0316RunUI.PreviewMode = true
		v0316RunUI.ForceTheme = "dark"
		renderPNG(t, v0316RunUI.View, 1160, 760, filepath.Join(qaDir, "qa-v0316-running-dark.png"))

		v0316ConfirmSvc := makeDemoService(app.SectionHome, false, false)
		v0316ConfirmSvc.RequestConfirmStop(v0316ConfirmSvc.Settings.SelectedAccountID)
		v0316ConfirmUI := uiapp.NewMainUI(v0316ConfirmSvc)
		v0316ConfirmUI.PreviewMode = true
		v0316ConfirmUI.ForceTheme = "dark"
		renderPNG(t, v0316ConfirmUI.View, 1160, 760, filepath.Join(qaDir, "qa-v0316-confirm-stop.png"))

		v0316IdlePop := uiapp.NewPopoverUI(makeDemoService(app.SectionHome, false, true))
		v0316IdlePop.ForceTheme = "dark"
		renderPNG(t, v0316IdlePop.View, 420, 520, filepath.Join(qaDir, "qa-v0316-popover-idle.png"))
	}
}

func TestConcurrentBackgroundMutationsAndUIRenderNoRace(t *testing.T) {
	svc := makeDemoService(app.SectionHome, false, false)
	defer svc.Shutdown()
	m := uiapp.NewMainUI(svc)
	m.PreviewMode = true
	p := uiapp.NewPopoverUI(svc)

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		step := 1
		for {
			select {
			case <-stop:
				return
			default:
				svc.ShowToast("并发状态更新测试")
				svc.SetShowingOnboarding(step%2 == 0)
				svc.SetOnboardingStep((step % 3) + 1)
				svc.SetSection(app.MainSection(step % 4))
				svc.UpdateSettings(func(s *core.AppSettings) {
					if step%2 == 0 {
						s.ThemePreference = "dark"
					} else {
						s.ThemePreference = "light"
					}
				})
				step++
			}
		}
	}()

	for i := 0; i < 8; i++ {
		if img := ui.Render(m.View, 1160, 760, 1); img == nil {
			t.Fatal("expected non-nil MainUI render under concurrent mutations")
		}
		if img := ui.Render(p.View, 420, 560, 1); img == nil {
			t.Fatal("expected non-nil PopoverUI render under concurrent mutations")
		}
	}
	close(stop)
	<-done
}
