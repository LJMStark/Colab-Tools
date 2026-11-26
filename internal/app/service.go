package app

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/LJMStark/Colab-Tools/internal/colab"
	"github.com/LJMStark/Colab-Tools/internal/core"
	"github.com/LJMStark/Colab-Tools/internal/doctor"
	"github.com/LJMStark/Colab-Tools/internal/integrations"
	"github.com/LJMStark/Colab-Tools/internal/runner"
	"github.com/LJMStark/Colab-Tools/internal/vault"
	"github.com/LJMStark/Colab-Tools/scripts/templates"
	"github.com/egoist/mygo"
	"github.com/google/uuid"
)

const AppVersion = "0.3.13"

type MainSection int

const (
	SectionHome MainSection = iota
	SectionHistory
	SectionAccounts
	SectionSettings

	// SectionStatus aliases SectionHome so existing call sites stay clean and unified.
	SectionStatus = SectionHome
)

var MainSectionTitles = []string{"主页", "记录", "账号", "设置"}

type deployWatchHandle struct {
	token  string
	cancel context.CancelFunc
}

type queueHandle struct {
	token          string
	cancel         context.CancelFunc
	startedAt      time.Time
	targetsSummary string
	rounds         int
	allocLogged    bool
}

type Service struct {
	mu sync.RWMutex

	Settings        core.AppSettings
	Runtime         map[string]core.AccountRuntimeState
	Insights        core.AllocationInsights
	History         *core.HistoryStore
	EnvReport       doctor.Report
	ClientDetection integrations.DetectionSnapshot

	SelectedSection   MainSection
	ShowingOnboarding bool
	OnboardingStep    int

	CLIInstalled           bool
	IsCheckingEnv          bool
	IsInstallingCLI        bool
	IsCancellingCLIInstall bool
	IsImportingDefault     bool
	CLIInstallLogs         []string
	CLIInstallError        string

	ToastMessage string
	toastTimer   *time.Timer

	ActiveQueueAccountID string
	activeQueueToken     string
	queueCancels         map[string]queueHandle

	cliInstallCancel context.CancelFunc
	cliInstallToken  string

	refreshing         map[string]bool
	lastManualFetch    time.Time
	sshTunnels         map[string]*exec.Cmd
	sshADCPaths        map[string]string
	deployWatchCancels map[string]deployWatchHandle
	pendingOAuth       *colab.OAuthSessionTicket

	OSRunner *runner.OSRunner
	Engine   *colab.Engine
	Vault    *vault.Vault

	persistChanges bool
	configPath     string
	insightsPath   string
	stopLoops      chan struct{}
	stopOnce       sync.Once

	OnUIInvalidate  func()
	OnTrayUpdate    func()
	OnOpenMainWin   func(sec MainSection, onboarding bool)
	NotifySender    func(title, body string)
	ClipboardWriter func(text string)
	URLOpener       func(url string)
}

func NewService(skipBootstrap bool) *Service {
	r := runner.NewOSRunner()
	var v *vault.Vault
	var hist *core.HistoryStore
	if skipBootstrap {
		v = vault.New(vault.NewMemoryBackend())
		hist = core.NewMemoryHistoryStore()
	} else {
		v = vault.New(nil)
		hist = core.LoadHistoryStore(core.HistoryFilePath())
	}
	s := &Service{
		Settings:           core.DefaultAppSettings(),
		Runtime:            map[string]core.AccountRuntimeState{},
		Insights:           core.NewAllocationInsights(),
		History:            hist,
		CLIInstalled:       true,
		OnboardingStep:     1,
		queueCancels:       map[string]queueHandle{},
		refreshing:         map[string]bool{},
		sshTunnels:         map[string]*exec.Cmd{},
		sshADCPaths:        map[string]string{},
		deployWatchCancels: map[string]deployWatchHandle{},
		OSRunner:           r,
		Engine:             colab.NewEngine(r, v),
		Vault:              v,
		persistChanges:     !skipBootstrap,
		configPath:         core.DefaultConfigPath(),
		insightsPath:       core.DefaultInsightsPath(),
		stopLoops:          make(chan struct{}),
		ClipboardWriter: func(text string) {
			if mygo.App.IsReady() {
				mygo.Clipboard.WriteText(text)
			}
		},
		URLOpener: func(u string) {
			if mygo.App.IsReady() {
				_ = mygo.Shell.OpenExternal(u)
			}
		},
	}

	s.NotifySender = func(title, body string) {
		if skipBootstrap {
			return
		}
		if runtime.GOOS == "darwin" && mygo.App.IsReady() {
			mygo.App.Dock.Bounce(false)
		}
		var showErr error
		supported := mygo.App.IsReady() && mygo.NotificationsSupported()
		if supported {
			n := mygo.NewNotification(mygo.NotificationOptions{
				Title:  title,
				Body:   body,
				Silent: false,
			})
			n.OnClick(func() {
				s.mu.RLock()
				openFn := s.OnOpenMainWin
				s.mu.RUnlock()
				if openFn != nil {
					openFn(SectionHome, false)
				}
			})
			showErr = n.Show()
		}
		if runtime.GOOS == "darwin" && (!supported || showErr != nil) {
			go sendMacOSAppleScriptNotification(title, body)
		}
	}

	if !skipBootstrap {
		s.Settings = core.LoadAppSettings(s.configPath)
		s.Insights = core.LoadAllocationInsights(s.insightsPath)
		s.ClientDetection = integrations.DetectInstalledTools()
		for _, acc := range s.Settings.Accounts {
			rt := core.NewAccountRuntimeState()
			if until, ok := s.Settings.QueueCooldownsByAccount[acc.ID]; ok && until.After(time.Now()) {
				u := until
				rt.QueueCircuitBreakTill = &u
			}
			s.Runtime[acc.ID] = rt
		}
		if s.Settings.SelectedAccountID == "" && len(s.Settings.Accounts) > 0 {
			s.Settings.SelectedAccountID = s.Settings.Accounts[0].ID
		}
	}
	return s
}

func (s *Service) notifyUI() {
	if fn := s.OnUIInvalidate; fn != nil {
		fn()
	}
	if fn := s.OnTrayUpdate; fn != nil {
		fn()
	}
}

func (s *Service) saveSettingsLocked() {
	if s.persistChanges {
		_ = s.Settings.Save(s.configPath)
	}
}

func (s *Service) saveInsightsLocked() {
	if s.persistChanges {
		_ = s.Insights.Save(s.insightsPath)
	}
}

// StartBackgroundServices runs startup environment inspection, system account import,
// and background watchdog/schedule/polling loops.
func (s *Service) StartBackgroundServices() {
	// Only clean stale ephemeral ADC files after the process has acquired the single-instance lock.
	if s.persistChanges && s.Vault != nil {
		s.Vault.CleanupStaleEphemeralFiles()
	}
	go func() {
		s.RefreshEnvironmentCheck()
		s.mu.RLock()
		cliOK := s.CLIInstalled
		emptyAccounts := len(s.Settings.Accounts) == 0
		completed := s.Settings.HasCompletedOnboarding
		s.mu.RUnlock()

		if cliOK && emptyAccounts {
			if imported := s.TryImportSystemDefaultAccount(); imported {
				s.mu.Lock()
				s.Settings.HasCompletedOnboarding = true
				s.saveSettingsLocked()
				s.mu.Unlock()
				completed = true
			}
		}

		if !cliOK || (!completed && len(s.AccountsSnapshot()) == 0) {
			s.mu.Lock()
			s.ShowingOnboarding = true
			s.OnboardingStep = 1
			s.mu.Unlock()
			s.notifyUI()
			if s.OnOpenMainWin != nil {
				s.OnOpenMainWin(SectionStatus, true)
			}
		} else {
			s.RefreshAllAccounts(true, true)
		}
	}()

	// 1-second tick: watchdog idle counter + off-peak schedule trigger + queue countdown repaint
	go func() {
		tk := time.NewTicker(1 * time.Second)
		defer tk.Stop()
		for {
			select {
			case <-s.stopLoops:
				return
			case now := <-tk.C:
				s.handleOneSecondTick(now)
			}
		}
	}()

	// Periodic staggered background refresh
	go func() {
		for {
			s.mu.RLock()
			interval := time.Duration(max(60, s.Settings.BackgroundRefreshSeconds)) * time.Second
			s.mu.RUnlock()
			select {
			case <-s.stopLoops:
				return
			case <-time.After(interval):
				s.RefreshAllAccounts(false, true)
			}
		}
	}()

	// Fast 20s LLM /slots probe when running
	go func() {
		tk := time.NewTicker(20 * time.Second)
		defer tk.Stop()
		for {
			select {
			case <-s.stopLoops:
				return
			case <-tk.C:
				s.pollRunningLLMSlots()
			}
		}
	}()
}

func (s *Service) Shutdown() {
	s.stopOnce.Do(func() {
		close(s.stopLoops)
	})
	s.mu.Lock()
	for _, h := range s.queueCancels {
		if h.cancel != nil {
			h.cancel()
		}
	}
	for id, wh := range s.deployWatchCancels {
		if wh.cancel != nil {
			wh.cancel()
		}
		delete(s.deployWatchCancels, id)
	}
	if s.cliInstallCancel != nil {
		s.cliInstallCancel()
	}
	ids := make([]string, 0, len(s.sshTunnels))
	for id := range s.sshTunnels {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	for _, id := range ids {
		s.StopSSHTunnel(id)
	}
}

func (s *Service) ShowToast(msg string) {
	s.mu.Lock()
	s.ToastMessage = msg
	if s.toastTimer != nil {
		s.toastTimer.Stop()
	}
	s.toastTimer = time.AfterFunc(3500*time.Millisecond, func() {
		s.mu.Lock()
		if s.ToastMessage == msg {
			s.ToastMessage = ""
		}
		s.mu.Unlock()
		s.notifyUI()
	})
	s.mu.Unlock()
	s.notifyUI()
}

func (s *Service) CopyToClipboard(text, label string) {
	if s.ClipboardWriter != nil {
		s.ClipboardWriter(text)
	}
	if label != "" {
		s.ShowToast("已复制：" + label)
	}
}

func (s *Service) SendNotification(title, body string, critical bool) {
	s.mu.RLock()
	enabled := s.Settings.EnableNotifications
	sender := s.NotifySender
	s.mu.RUnlock()
	if !critical && !enabled {
		return
	}
	if sender != nil {
		sender(title, body)
	}
}

// SendTestNotification triggers a critical desktop alert so users can verify popup and sound delivery.
func (s *Service) SendTestNotification() {
	s.SendNotification(
		"Colab Tools 提醒测试",
		"通知正常！抢到卡、装好 ComfyUI 或没抢到时都会这样弹窗响铃通知你。",
		true,
	)
	s.ShowToast("已发送测试通知，请留意屏幕右上角弹窗与提示音")
}

func sanitizeNotificationText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return strings.TrimSpace(s)
}

func sendMacOSAppleScriptNotification(title, body string) {
	script := "on run argv\ndisplay notification (item 2 of argv) with title (item 1 of argv) sound name \"Glass\"\nend run"
	cmd := exec.Command("osascript", "-e", script, "--", sanitizeNotificationText(title), sanitizeNotificationText(body))
	_ = cmd.Run()
}

// TrayStatusSnapshot summarizes live account phases for the menu bar tray title and tooltip.
type TrayStatusSnapshot struct {
	StatusBarMode  string
	RunningCount   int
	TotalBalance   float64
	Queuing        bool
	QueueAttempt   int
	QueueMax       int
	TargetsSummary string
	Deploying      bool
	Starting       bool
	HardwareLabel  string
	FocusHasError  bool
	FocusErrorText string
}

// TrayLiveStatus returns a lock-safe point-in-time snapshot of all accounts for tray updates.
func (s *Service) TrayLiveStatus() TrayStatusSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	st := TrayStatusSnapshot{
		StatusBarMode: string(s.Settings.StatusBarMode),
	}
	selectedID := s.Settings.SelectedAccountID
	if selectedID == "" && len(s.Settings.Accounts) > 0 {
		selectedID = s.Settings.Accounts[0].ID
	}

	for _, acc := range s.Settings.Accounts {
		rt := s.Runtime[acc.ID]
		st.TotalBalance += rt.CUBalance
		if rt.Phase.IsRunning() {
			st.RunningCount++
		}
		if !st.Queuing && rt.Phase.IsQueuing() {
			st.Queuing = true
			st.QueueAttempt = max(1, rt.Phase.QueueAttempt)
			st.QueueMax = max(1, rt.Phase.QueueMax)
			st.TargetsSummary = rt.Phase.TargetsSummary
		}
		hwLabel := ""
		if rt.GPUName != "" || rt.ActiveHardware != "" {
			hwLabel = rt.ShortGPUName()
		}
		if !st.Deploying && rt.DeployRunning {
			st.Deploying = true
			if hwLabel != "" {
				st.HardwareLabel = hwLabel
			}
		}
		if !st.Starting && rt.Phase.Kind == core.PhaseStarting {
			st.Starting = true
			if st.HardwareLabel == "" && hwLabel != "" {
				st.HardwareLabel = hwLabel
			}
		}
		if acc.ID == selectedID && !rt.Phase.IsRunning() && rt.LastError != "" {
			st.FocusHasError = true
			st.FocusErrorText = rt.LastErrorTitle
			if st.FocusErrorText == "" {
				st.FocusErrorText = rt.LastError
			}
		}
	}
	return st
}

// Snapshot helpers for thread-safe UI reads

func (s *Service) AccountsSnapshot() []core.AccountProfile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]core.AccountProfile, len(s.Settings.Accounts))
	copy(out, s.Settings.Accounts)
	return out
}

func (s *Service) SelectedAccount() (core.AccountProfile, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, acc := range s.Settings.Accounts {
		if acc.ID == s.Settings.SelectedAccountID {
			return acc, true
		}
	}
	if len(s.Settings.Accounts) > 0 {
		return s.Settings.Accounts[0], true
	}
	return core.AccountProfile{}, false
}

func (s *Service) StateFor(accountID string) core.AccountRuntimeState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if rt, ok := s.Runtime[accountID]; ok {
		return rt
	}
	return core.NewAccountRuntimeState()
}

// InsightsSnapshot returns a lock-safe point-in-time view of allocation heatmap data for the UI.
func (s *Service) InsightsSnapshot(hardware string) (total int, slots []core.TimeSlotHitRate, recommended string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Insights.TotalAttempts(hardware),
		s.Insights.SixSlotHeatmap(hardware),
		s.Insights.RecommendedWindowSummary(hardware)
}

func (s *Service) HistorySnapshot(filter core.HistoryFilter) ([]core.HistoryRecord, core.HistorySummary) {
	if s.History == nil {
		return []core.HistoryRecord{}, core.HistorySummary{}
	}
	return s.History.Query(filter, time.Now())
}

func (s *Service) ExportHistoryCSV(filter core.HistoryFilter) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return "", fmt.Errorf("无法定位用户主目录")
	}
	targetDir := filepath.Join(home, "Downloads")
	if info, statErr := os.Stat(targetDir); statErr != nil || !info.IsDir() {
		targetDir = home
	}
	store := s.History
	if store == nil {
		store = core.NewMemoryHistoryStore()
	}
	now := time.Now()
	csvContent := store.ExportCSV(filter, now)
	outPath := filepath.Join(targetDir, fmt.Sprintf("colab-tools-history-%s.csv", now.Format("20060102-150405")))
	if err := os.WriteFile(outPath, []byte(csvContent), 0o600); err != nil {
		return "", err
	}
	s.ShowToast("已导出账单至 " + outPath)
	return outPath, nil
}

func (s *Service) SwitchToLightGGUFAndStart(accountID string) {
	acc, ok := s.accountByID(accountID)
	if !ok {
		return
	}
	acc.H3Variant = core.H3VariantFlashGGUF
	s.UpdateAccount(acc)
	s.ClearAccountError(accountID)
	s.DeployNow(accountID)
}

func (s *Service) QuickScheduleMorningStart(accountID string, hour, minute int) {
	acc, ok := s.accountByID(accountID)
	if !ok {
		return
	}
	nf := core.NextScheduledFireDate(hour, minute, time.Now())
	acc.ScheduledStartEnabled = true
	acc.ScheduledStartHour = nf.Hour()
	acc.ScheduledStartMinute = nf.Minute()
	acc.ScheduledNextFireAt = &nf
	s.UpdateAccount(acc)
	s.ClearAccountError(accountID)
	s.ShowToast(fmt.Sprintf("已预约每天 %02d:%02d 自动抢卡", acc.ScheduledStartHour, acc.ScheduledStartMinute))
}

func (s *Service) SelectAccount(accountID string) {
	s.mu.Lock()
	s.Settings.SelectedAccountID = accountID
	s.saveSettingsLocked()
	s.mu.Unlock()
	s.notifyUI()
}

func (s *Service) UpdateAccount(updated core.AccountProfile) {
	updated.Sanitize()
	s.mu.Lock()
	for i := range s.Settings.Accounts {
		if s.Settings.Accounts[i].ID == updated.ID {
			s.Settings.Accounts[i] = updated
			break
		}
	}
	s.saveSettingsLocked()
	s.mu.Unlock()
	s.notifyUI()
}

func (s *Service) UpdateSettings(fn func(*core.AppSettings)) {
	s.mu.Lock()
	fn(&s.Settings)
	s.saveSettingsLocked()
	s.mu.Unlock()
	s.notifyUI()
}

func (s *Service) RemoveAccount(accountID string) {
	s.CancelQueue(accountID)
	s.cancelDeployWatcher(accountID)
	s.StopSSHTunnel(accountID)
	_ = s.Vault.DeleteADCCredentials(accountID)
	_ = s.Vault.DeleteLLMAPIKey(accountID)

	s.mu.Lock()
	filtered := make([]core.AccountProfile, 0, len(s.Settings.Accounts))
	for _, acc := range s.Settings.Accounts {
		if acc.ID != accountID {
			filtered = append(filtered, acc)
		}
	}
	s.Settings.Accounts = filtered
	delete(s.Runtime, accountID)
	delete(s.Settings.QueueCooldownsByAccount, accountID)
	if s.Settings.SelectedAccountID == accountID {
		if len(filtered) > 0 {
			s.Settings.SelectedAccountID = filtered[0].ID
		} else {
			s.Settings.SelectedAccountID = ""
		}
	}
	s.saveSettingsLocked()
	s.mu.Unlock()
	s.ShowToast("已移除账号并清理对应系统凭据")
}

func (s *Service) TotalManagedRunningSessionCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	total := 0
	for _, acc := range s.Settings.Accounts {
		rt := s.Runtime[acc.ID]
		names := map[string]bool{}
		for _, sess := range rt.Sessions {
			if !sess.IsOrphan {
				names[sess.Name] = true
			}
		}
		if rt.Phase.IsRunning() {
			names[acc.SessionName] = true
		}
		total += len(names)
	}
	return total
}

func (s *Service) TotalOrphanSessionCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	total := 0
	for _, acc := range s.Settings.Accounts {
		for _, sess := range s.Runtime[acc.ID].Sessions {
			if sess.IsOrphan {
				total++
			}
		}
	}
	return total
}

func (s *Service) RunningAccountCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for _, acc := range s.Settings.Accounts {
		if s.Runtime[acc.ID].Phase.IsRunning() {
			n++
		}
	}
	return n
}

func (s *Service) TotalBalanceAndRate() (float64, float64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var bal, rate float64
	for _, acc := range s.Settings.Accounts {
		rt := s.Runtime[acc.ID]
		bal += rt.CUBalance
		rate += rt.CURatePerHour
	}
	return bal, rate
}

func (s *Service) WatchdogStatusFor(acc core.AccountProfile) core.WatchdogAction {
	rt := s.StateFor(acc.ID)
	allowFallback := acc.EnableHardwareIdle || rt.SignalSource == core.SignalSourceStatusFile || rt.SignalSource == core.SignalSourceComfyQueue
	return core.EvaluateWatchdog(core.WatchdogInput{
		IsRunning:             rt.Phase.IsRunning(),
		EnableLLMModule:       acc.EnableLLMModule,
		ActivitySignal:        rt.ActivitySignal,
		AutoShutdownEnabled:   acc.AutoShutdownEnabled,
		IdleSeconds:           rt.IdleSeconds,
		TimeoutMinutes:        core.EffectiveIdleTimeoutMinutes(acc, rt),
		WarnBeforeMinutes:     acc.WarnBeforeMinutes,
		AlreadyWarned:         rt.WarnedForCurrentIdle,
		Now:                   time.Now(),
		AllowHardwareFallback: allowFallback,
	})
}

func (s *Service) ResetIdleTimer(accountID string) {
	s.mu.Lock()
	if rt, ok := s.Runtime[accountID]; ok {
		rt.IdleSeconds = 0
		rt.LastActiveAt = time.Now()
		rt.WarnedForCurrentIdle = false
		s.Runtime[accountID] = rt
	}
	s.mu.Unlock()
	s.ShowToast("已续命，空闲计时器清零")
}

// Navigation & Environment Doctor

func (s *Service) SetSection(sec MainSection) {
	s.mu.Lock()
	s.SelectedSection = sec
	s.ShowingOnboarding = false
	s.mu.Unlock()
	s.notifyUI()
}

func (s *Service) ReopenOnboarding() {
	s.mu.Lock()
	s.ShowingOnboarding = true
	s.OnboardingStep = 1
	s.mu.Unlock()
	s.notifyUI()
	go s.RefreshEnvironmentCheck()
}

func (s *Service) CompleteOnboarding() {
	s.mu.Lock()
	s.Settings.HasCompletedOnboarding = true
	s.ShowingOnboarding = false
	s.SelectedSection = SectionStatus
	s.saveSettingsLocked()
	s.mu.Unlock()
	s.notifyUI()
}

func (s *Service) RefreshClientDetection() {
	snap := integrations.DetectInstalledTools()
	s.mu.Lock()
	s.ClientDetection = snap
	s.mu.Unlock()
	s.notifyUI()
}

func (s *Service) RefreshEnvironmentCheck() {
	s.mu.Lock()
	if s.IsCheckingEnv {
		s.mu.Unlock()
		return
	}
	s.IsCheckingEnv = true
	s.mu.Unlock()
	s.notifyUI()

	rep := doctor.Inspect(context.Background(), s.OSRunner)

	s.mu.Lock()
	s.EnvReport = rep
	s.CLIInstalled = rep.ColabInstalled
	s.IsCheckingEnv = false
	s.mu.Unlock()
	s.notifyUI()
}

func (s *Service) InstallColabCLIAutomatically() {
	s.mu.Lock()
	if s.IsInstallingCLI || s.IsCancellingCLIInstall {
		s.mu.Unlock()
		return
	}
	method := s.EnvReport.RecommendedMethod
	if method.IsManualOnly() {
		s.mu.Unlock()
		s.CopyToClipboard(method.ManualShellCommand(), "WSL2 安装命令")
		return
	}
	token := uuid.New().String()
	ctx, cancel := context.WithCancel(context.Background())
	s.cliInstallToken = token
	s.cliInstallCancel = cancel
	s.IsInstallingCLI = true
	s.IsCancellingCLIInstall = false
	s.CLIInstallError = ""
	s.CLIInstallLogs = []string{"正在准备安装：" + method.DisplayTitle() + "…"}
	s.mu.Unlock()
	s.notifyUI()

	go func() {
		res := doctor.InstallColabCLI(ctx, s.OSRunner, method, 240*time.Second, func(line string) {
			s.mu.Lock()
			if s.cliInstallToken != token {
				s.mu.Unlock()
				return
			}
			s.CLIInstallLogs = append(s.CLIInstallLogs, line)
			if len(s.CLIInstallLogs) > 300 {
				s.CLIInstallLogs = s.CLIInstallLogs[len(s.CLIInstallLogs)-300:]
			}
			s.mu.Unlock()
			s.notifyUI()
		})

		s.mu.Lock()
		if s.cliInstallToken != token {
			s.mu.Unlock()
			return
		}
		s.IsInstallingCLI = false
		s.IsCancellingCLIInstall = false
		s.cliInstallCancel = nil
		s.cliInstallToken = ""
		s.EnvReport = res.Report
		s.CLIInstalled = res.Report.ColabInstalled
		emptyAcc := len(s.Settings.Accounts) == 0
		if res.Succeeded {
			s.CLIInstallError = ""
		} else {
			s.CLIInstallError = res.Message
		}
		s.mu.Unlock()

		s.ShowToast(res.Message)
		if res.Succeeded && emptyAcc {
			_ = s.TryImportSystemDefaultAccount()
		}
	}()
}

func (s *Service) CancelCLIInstall() {
	s.mu.Lock()
	if !s.IsInstallingCLI || s.IsCancellingCLIInstall {
		s.mu.Unlock()
		return
	}
	s.IsCancellingCLIInstall = true
	s.CLIInstallLogs = append(s.CLIInstallLogs, "[正在终止安装进程组，请稍候…]")
	cancel := s.cliInstallCancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.notifyUI()
}

// TryImportSystemDefaultAccount is serialized and re-deduplicated before append to prevent duplicate configs on double-click.
func (s *Service) TryImportSystemDefaultAccount() bool {
	s.mu.Lock()
	if !s.CLIInstalled || s.IsImportingDefault {
		s.mu.Unlock()
		return false
	}
	s.IsImportingDefault = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.IsImportingDefault = false
		s.mu.Unlock()
		s.notifyUI()
	}()

	probe := core.NewDefaultAccountProfile("默认账号", "", core.TierColabPro, core.AuthSystemDefaultOAuth2)
	who, ok := s.Engine.Whoami(context.Background(), probe)
	if !ok {
		return false
	}
	normEmail := strings.ToLower(strings.TrimSpace(who.Email))

	s.mu.Lock()
	for _, existing := range s.Settings.Accounts {
		if strings.ToLower(strings.TrimSpace(existing.Email)) == normEmail {
			s.Settings.SelectedAccountID = existing.ID
			s.saveSettingsLocked()
			s.mu.Unlock()
			s.RefreshAccount(existing.ID, true, true)
			return true
		}
	}
	s.mu.Unlock()

	usage, sessions, _ := s.Engine.FetchUsageAndSessions(context.Background(), probe)
	balance := 0.0
	if usage != nil {
		balance = usage.Balance
	}
	tier := core.SuggestedTier(balance)
	name := strings.Split(who.Email, "@")[0]
	if name == "" {
		name = "主账号"
	}
	detected := core.NewDefaultAccountProfile(name, who.Email, tier, core.AuthSystemDefaultOAuth2)
	for _, sess := range sessions {
		if !sess.IsOrphan {
			detected.SessionName = core.SanitizeSessionName(sess.Name)
			break
		}
	}

	s.mu.Lock()
	// Second deduplication check right before append
	for _, existing := range s.Settings.Accounts {
		if strings.ToLower(strings.TrimSpace(existing.Email)) == normEmail ||
			(existing.AuthMode == core.AuthSystemDefaultOAuth2 && existing.Email == "") {
			s.Settings.SelectedAccountID = existing.ID
			s.saveSettingsLocked()
			s.mu.Unlock()
			s.RefreshAccount(existing.ID, true, true)
			return true
		}
	}
	s.Settings.Accounts = append(s.Settings.Accounts, detected)
	s.Settings.SelectedAccountID = detected.ID
	s.Runtime[detected.ID] = core.NewAccountRuntimeState()
	s.saveSettingsLocked()
	s.mu.Unlock()

	s.RefreshAccount(detected.ID, true, true)
	s.ShowToast("已导入默认账号：" + detected.DisplayName)
	return true
}

// Official OAuth2 Multi-Account Binding

func (s *Service) BeginOAuthFlow() (string, error) {
	ticket, err := s.Engine.BeginOfficialOAuthFlow(context.Background())
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.pendingOAuth = ticket
	s.mu.Unlock()
	if s.URLOpener != nil {
		s.URLOpener(ticket.AuthURL)
	}
	return ticket.AuthURL, nil
}

func (s *Service) CompleteOAuthFlow(displayName string, tier core.SubscriptionTier, authCode string) error {
	s.mu.RLock()
	ticket := s.pendingOAuth
	s.mu.RUnlock()
	if ticket == nil {
		return fmt.Errorf("请先点击「打开 Google 授权登录」获取授权码")
	}
	credJSON, err := s.Engine.CompleteOfficialOAuthFlow(context.Background(), ticket, authCode)
	if err != nil {
		return err
	}

	acc := core.NewDefaultAccountProfile(strings.TrimSpace(displayName), "", tier, core.AuthIsolatedVaultADC)
	if err := s.Vault.SaveADCCredentials(acc.ID, credJSON); err != nil {
		return err
	}
	if who, ok := s.Engine.Whoami(context.Background(), acc); ok {
		acc.Email = who.Email
		if strings.TrimSpace(displayName) == "" {
			acc.DisplayName = strings.Split(who.Email, "@")[0]
		}
	}
	// Suggest accurate tier if user kept default
	if usage, _, uErr := s.Engine.FetchUsageAndSessions(context.Background(), acc); uErr == nil && usage != nil && tier == core.TierColabPro {
		acc.Tier = core.SuggestedTier(usage.Balance)
		acc.PreferredAccelerator = acc.Tier.DefaultAccelerator()
		acc.Sanitize()
	}

	s.mu.Lock()
	s.pendingOAuth = nil
	s.Settings.Accounts = append(s.Settings.Accounts, acc)
	s.Settings.SelectedAccountID = acc.ID
	s.Runtime[acc.ID] = core.NewAccountRuntimeState()
	s.saveSettingsLocked()
	s.mu.Unlock()

	go s.RefreshAccount(acc.ID, true, true)
	s.ShowToast("账号已安全绑定：" + acc.DisplayName)
	return nil
}

// Refresh & Telemetry

func (s *Service) RefreshAllAccounts(force, silent bool) {
	s.mu.Lock()
	if !s.CLIInstalled {
		s.mu.Unlock()
		return
	}
	if !force && time.Since(s.lastManualFetch) < time.Duration(s.Settings.ManualRefreshDebounceSec)*time.Second {
		s.mu.Unlock()
		return
	}
	s.lastManualFetch = time.Now()
	accounts := make([]core.AccountProfile, len(s.Settings.Accounts))
	copy(accounts, s.Settings.Accounts)
	s.mu.Unlock()

	for i, acc := range accounts {
		if i > 0 {
			time.Sleep(1500 * time.Millisecond)
		}
		s.RefreshAccount(acc.ID, force, silent)
	}
	if !silent {
		s.ShowToast("已刷新全部账号状态")
	}
}

func (s *Service) RefreshAccount(accountID string, force, silent bool) {
	_ = s.refreshAccount(accountID, force, silent, false)
}

func (s *Service) refreshAccount(accountID string, force, silent, settleBusy bool) bool {
	s.mu.Lock()
	if s.refreshing[accountID] {
		s.mu.Unlock()
		return false
	}
	var acc core.AccountProfile
	found := false
	for _, a := range s.Settings.Accounts {
		if a.ID == accountID {
			acc = a
			found = true
			break
		}
	}
	if !found {
		s.mu.Unlock()
		return false
	}
	s.refreshing[accountID] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.refreshing, accountID)
		s.mu.Unlock()
		s.notifyUI()
	}()

	usage, sessions, err := s.Engine.FetchUsageAndSessions(context.Background(), acc)
	now := time.Now()

	s.mu.Lock()
	if err != nil {
		rt := s.Runtime[accountID]
		if rt.ContextLimit == 0 {
			rt = core.NewAccountRuntimeState()
		}
		rt.LastError = err.Error()
		s.Runtime[accountID] = rt
		s.mu.Unlock()
		return false
	}
	primaryEndpoint, isRunning, closedRec := s.applyServerSnapshotLocked(accountID, acc, usage, sessions, now, settleBusy, "")
	s.mu.Unlock()
	if closedRec != nil {
		s.recordHistory(*closedRec)
	}
	s.notifyUI()

	if isRunning {
		s.fetchAndApplyTelemetry(acc, primaryEndpoint)
	}
	return true
}

func (s *Service) recordHistory(rec core.HistoryRecord) {
	if s.History != nil {
		s.History.AddRecord(rec)
	}
}

func mapShutdownReason(reason string) core.ShutdownReason {
	switch strings.TrimSpace(reason) {
	case "空闲自动关机":
		return core.ShutdownAutoIdle
	case "全部关机止损":
		return core.ShutdownBatchStop
	default:
		return core.ShutdownManual
	}
}

func recipeLabelForSession(acc core.AccountProfile, downgraded bool) string {
	if acc.DeployRecipe == core.RecipeH3Video {
		if downgraded {
			return core.H3VariantFlashGGUF.DisplayName() + "（自动降级）"
		}
		return acc.H3Variant.DisplayName()
	}
	return acc.DeployRecipe.DisplayName()
}

func buildClosedSessionRecordLocked(
	acc core.AccountProfile,
	rt *core.AccountRuntimeState,
	prevEndpoint string,
	prevHardware string,
	prevGPUName string,
	prevUptime int,
	usage *colab.UsageSnapshot,
	reason core.ShutdownReason,
	now time.Time,
) core.HistoryRecord {
	hw := core.NormalizeHardwareKey(prevHardware)
	if hw == "" {
		hw = core.NormalizeHardwareKey(prevGPUName)
	}
	if hw == "" {
		hw = strings.TrimSpace(prevHardware)
		if hw == "" {
			hw = strings.TrimSpace(prevGPUName)
		}
	}
	if !rt.SessionLastSampleAt.IsZero() && now.After(rt.SessionLastSampleAt) {
		dt := now.Sub(rt.SessionLastSampleAt).Seconds()
		rate := rt.SessionMeasuredSingleRate
		if rate <= 0 {
			rate = core.EstimatedHourlyCU(hw)
		}
		rt.SessionEstimatedCU += rate * dt / 3600.0
	}

	startedAt := rt.SessionStartedAt
	if startedAt.IsZero() {
		startedAt = now.Add(-time.Duration(max(0, prevUptime)) * time.Second)
	}
	durSec := int(now.Sub(startedAt).Seconds())
	if prevUptime > durSec {
		durSec = prevUptime
	}
	if durSec < 0 {
		durSec = 0
	}

	if reason == "" {
		if durSec >= acc.Tier.MaxSessionHours()*3600-300 {
			reason = core.ShutdownCapReached
		} else {
			reason = core.ShutdownExternalOrLost
		}
	}

	var basis core.BillingBasis
	var consumed float64
	if acc.Tier == core.TierFree {
		basis = core.BillingFree
		consumed = 0
	} else {
		est := rt.SessionEstimatedCU
		if est <= 0 && durSec > 0 {
			rate := rt.SessionMeasuredSingleRate
			if rate <= 0 {
				rate = core.EstimatedHourlyCU(hw)
			}
			est = rate * float64(durSec) / 3600.0
		}
		delta := 0.0
		if usage != nil && rt.SessionStartBalanceValid {
			delta = rt.SessionStartBalance - usage.Balance
		}
		if usage != nil &&
			rt.SessionMaxAssignments <= 1 &&
			!rt.SessionAdopted &&
			rt.SessionStartBalanceValid &&
			delta > 0 &&
			(est <= 0.05 || (delta >= est*0.3 && delta <= est*2.5)) {
			basis = core.BillingMeasured
			consumed = delta
		} else {
			basis = core.BillingEstimated
			consumed = est
		}
	}

	endedAt := now
	rec := core.HistoryRecord{
		Kind:            core.HistoryKindSession,
		AccountID:       acc.ID,
		AccountName:     acc.DisplayName,
		AccountColor:    acc.ColorTag,
		SessionName:     acc.SessionName,
		Endpoint:        prevEndpoint,
		StartedAt:       startedAt,
		EndedAt:         &endedAt,
		DurationSec:     durSec,
		Hardware:        hw,
		HighMem:         core.EffectiveUseHighMem(acc) && !rt.SessionUsedStandardRAM,
		RecipeLabel:     recipeLabelForSession(acc, rt.SessionModelDowngraded),
		ConsumedCU:      consumed,
		BillingBasis:    basis,
		ShutdownReason:  reason,
		FallbackUsed:    rt.SessionFallbackUsed,
		ModelDowngraded: rt.SessionModelDowngraded,
	}

	rt.SessionStartedAt = time.Time{}
	rt.SessionStartBalance = 0
	rt.SessionStartBalanceValid = false
	rt.SessionAdopted = false
	rt.SessionMaxAssignments = 0
	rt.SessionMeasuredSingleRate = 0
	rt.SessionEstimatedCU = 0
	rt.SessionLastSampleAt = time.Time{}
	rt.SessionFallbackUsed = false
	rt.SessionModelDowngraded = false
	rt.SessionUsedStandardRAM = false
	return rec
}

func (s *Service) applyServerSnapshotLocked(
	accountID string,
	acc core.AccountProfile,
	usage *colab.UsageSnapshot,
	sessions []core.ColabSessionItem,
	now time.Time,
	settleBusy bool,
	shutdownHint core.ShutdownReason,
) (primaryEndpoint string, isRunning bool, closedRec *core.HistoryRecord) {
	rt := s.Runtime[accountID]
	if rt.ContextLimit == 0 {
		rt = core.NewAccountRuntimeState()
	}
	// Do not let concurrent background refresh race with StopAccountSession verification
	if rt.Phase.Kind == core.PhaseStopping && !settleBusy {
		return rt.ActiveEndpoint, false, nil
	}

	prevEndpoint := rt.ActiveEndpoint
	prevHardware := rt.ActiveHardware
	prevGPUName := rt.GPUName
	prevBalance := rt.CUBalance
	prevBalanceValid := rt.LastUsageFetchAt != nil
	prevUptime := rt.SessionUptimeSec

	rt.LastError = ""
	if usage != nil {
		rt.CUBalance = usage.Balance
		rt.CURatePerHour = usage.RatePerHour
		rt.ActiveAssignments = usage.ActiveAssignments
	}
	rt.Sessions = sessions
	rt.LastUsageFetchAt = &now

	var primary *core.ColabSessionItem
	for i := range sessions {
		if !sessions[i].IsOrphan && sessions[i].Name == acc.SessionName {
			primary = &sessions[i]
			break
		}
	}

	if primary != nil {
		if prevEndpoint == primary.Endpoint {
			if usage != nil {
				if usage.ActiveAssignments > rt.SessionMaxAssignments {
					rt.SessionMaxAssignments = usage.ActiveAssignments
				}
				if usage.ActiveAssignments <= 1 && usage.RatePerHour > 0 {
					rt.SessionMeasuredSingleRate = usage.RatePerHour
				}
				if !rt.SessionLastSampleAt.IsZero() && now.After(rt.SessionLastSampleAt) {
					dt := now.Sub(rt.SessionLastSampleAt).Seconds()
					rate := rt.SessionMeasuredSingleRate
					if rate <= 0 {
						rate = core.EstimatedHourlyCU(primary.Hardware)
					}
					rt.SessionEstimatedCU += rate * dt / 3600.0
				}
				rt.SessionLastSampleAt = now
			}
		} else {
			if prevEndpoint != "" {
				rec := buildClosedSessionRecordLocked(acc, &rt, prevEndpoint, prevHardware, prevGPUName, prevUptime, usage, core.ShutdownExternalOrLost, now)
				closedRec = &rec
			}
			isFreshAlloc := rt.Phase.Kind == core.PhaseStarting || rt.Phase.Kind == core.PhaseQueuing
			rt.SessionUptimeSec = 0
			rt.IdleSeconds = 0
			rt.WarnedForCurrentIdle = false
			rt.SessionStartedAt = now
			rt.SessionLastSampleAt = now
			rt.SessionEstimatedCU = 0
			rt.SessionAdopted = !isFreshAlloc
			rt.SessionFallbackUsed = false
			rt.SessionModelDowngraded = false
			rt.SessionUsedStandardRAM = false
			if isFreshAlloc && prevBalanceValid {
				rt.SessionStartBalance = prevBalance
				rt.SessionStartBalanceValid = true
			} else {
				rt.SessionStartBalance = 0
				rt.SessionStartBalanceValid = false
			}
			if usage != nil {
				rt.SessionMaxAssignments = usage.ActiveAssignments
				if usage.ActiveAssignments <= 1 && usage.RatePerHour > 0 {
					rt.SessionMeasuredSingleRate = usage.RatePerHour
				} else {
					rt.SessionMeasuredSingleRate = 0
				}
			} else {
				rt.SessionMaxAssignments = 0
				rt.SessionMeasuredSingleRate = 0
			}

			// Endpoint changed -> invalidate bound LLM URL & video deploy/auto-open state
			rt.PublicURL = ""
			rt.LLMURLBoundEndpoint = ""
			rt.DisplayUIURL = ""
			rt.ServerHealthy = false
			rt.DeployRunning = false
			rt.DeployFailed = false
			rt.DeployError = ""
			rt.DeployStartingUntil = time.Time{}
			rt.DeployNotifiedFailEndpoint = ""
			rt.DeployLogTail = ""
			rt.DeployLogLoading = false
			rt.RenderedVideoCount = 0
			rt.SawQueueBusyThisRun = false
			rt.AutoOpenedEndpoint = ""
		}
		if settleBusy || !rt.Phase.IsBusy() {
			rt.Phase = core.InstancePhase{Kind: core.PhaseRunning}
		}
		rt.ActiveEndpoint = primary.Endpoint
		rt.ActiveHardware = primary.Hardware
		s.Runtime[accountID] = rt
		return primary.Endpoint, true, closedRec
	}

	if prevEndpoint != "" {
		rec := buildClosedSessionRecordLocked(acc, &rt, prevEndpoint, prevHardware, prevGPUName, prevUptime, usage, shutdownHint, now)
		closedRec = &rec
	}

	if settleBusy || (!rt.Phase.IsBusy() && !rt.Phase.IsQueuing()) {
		rt.Phase = core.InstancePhase{Kind: core.PhaseOffline}
	}
	rt.ActiveEndpoint = ""
	rt.ActiveHardware = ""
	rt.GPUName = ""
	rt.VRAMUsedMB = 0
	rt.VRAMTotalMB = 0
	rt.GPUUtilPercent = 0
	rt.SessionUptimeSec = 0
	rt.PublicURL = ""
	rt.LLMURLBoundEndpoint = ""
	rt.DisplayUIURL = ""
	rt.ServerHealthy = false
	rt.DeployRunning = false
	rt.DeployFailed = false
	rt.DeployError = ""
	rt.DeployStartingUntil = time.Time{}
	rt.DeployNotifiedFailEndpoint = ""
	rt.DeployLogTail = ""
	rt.DeployLogLoading = false
	rt.RenderedVideoCount = 0
	rt.SawQueueBusyThisRun = false
	rt.AutoOpenedEndpoint = ""
	rt.QueueRunning = 0
	rt.QueuePending = 0
	rt.IdleSeconds = 0
	rt.WarnedForCurrentIdle = false
	rt.ActivitySignal = core.ActivitySignal{Kind: core.SignalUnknown}
	rt.SignalSource = core.SignalSourceNone
	s.Runtime[accountID] = rt
	return "", false, closedRec
}

func (s *Service) fetchAndApplyTelemetry(acc core.AccountProfile, expectedEndpoint string) {
	tel := s.Engine.FetchRemoteTelemetry(context.Background(), acc)
	if tel == nil {
		return
	}
	now := time.Now()
	s.mu.Lock()
	rt := s.Runtime[acc.ID]
	if rt.ActiveEndpoint != expectedEndpoint {
		s.mu.Unlock()
		return
	}
	if tel.GPUName != "" {
		rt.GPUName = tel.GPUName
	}
	rt.VRAMUsedMB = tel.VRAMUsedMB
	rt.VRAMTotalMB = tel.VRAMTotalMB
	if tel.GPUUtil != nil {
		rt.GPUUtilPercent = *tel.GPUUtil
		rt.AppendTelemetrySample(*tel.GPUUtil, rt.VRAMRatio())
	}
	if tel.CPUUtil != nil {
		rt.CPUUtilPercent = *tel.CPUUtil
	}
	rt.CUDAProcessCount = tel.CUDAProcessCount
	if tel.UptimeSeconds > 0 {
		rt.SessionUptimeSec = tel.UptimeSeconds
	}
	if tel.PublicURL != "" {
		rt.PublicURL = tel.PublicURL
		rt.LLMURLBoundEndpoint = expectedEndpoint
	}
	if tel.DisplayUIURL != "" {
		rt.DisplayUIURL = tel.DisplayUIURL
	}
	rt.ServerHealthy = tel.Healthy
	if tel.ModelFile != "" {
		rt.ModelFile = tel.ModelFile
	}
	if tel.ModelAlias != "" {
		rt.ModelAlias = tel.ModelAlias
	}
	if tel.ContextLimit > 0 {
		rt.ContextLimit = tel.ContextLimit
	}
	if tel.KVQuant != "" {
		rt.KVQuant = tel.KVQuant
	}
	rt.DetectedServicePort = tel.DetectedWebPort
	inStartingGrace := !rt.DeployStartingUntil.IsZero() && now.Before(rt.DeployStartingUntil)
	deployFailed := core.IsDeployFailedStatus(tel.StatusPayload) && !tel.DeployAlive && !tel.Healthy

	if tel.DeployAlive {
		rt.DeployRunning = true
		rt.DeployFailed = false
		rt.DeployError = ""
		rt.DeployStartingUntil = time.Time{}
	} else if deployFailed {
		rt.DeployRunning = false
		rt.DeployFailed = true
		rt.DeployStartingUntil = time.Time{}
	} else if tel.Healthy {
		rt.DeployRunning = false
		rt.DeployFailed = false
		rt.DeployError = ""
		rt.DeployStartingUntil = time.Time{}
	} else if inStartingGrace {
		rt.DeployRunning = true
	} else {
		rt.DeployRunning = false
	}
	rt.RenderedVideoCount = tel.RenderedVideoCount

	// Evaluate non-LLM signals (active deploy -> status.json with explicit busy -> ComfyUI /queue -> dual low-load fallback)
	statusFresh := tel.StatusPayload != nil && tel.StatusAgeSeconds != nil && *tel.StatusAgeSeconds <= core.StatusFileMaxAgeSec
	shouldNotifyDeployFail := false
	deployFailDetail := ""
	if deployFailed {
		detail := strings.TrimSpace(tel.StatusPayload.Detail)
		if detail == "" {
			detail = "云端部署中断，请点「查看日志」排查原因"
		}
		rt.WorkloadName = tel.StatusPayload.Name
		rt.WorkloadMetricLabel = tel.StatusPayload.MetricLabel
		rt.WorkloadMetricValue = tel.StatusPayload.MetricValue
		rt.WorkloadMetricUnit = tel.StatusPayload.MetricUnit
		zero := 0.0
		rt.WorkloadMetricRatio = &zero
		rt.WorkloadDetail = detail
		rt.DeployError = detail
		if rt.DeployNotifiedFailEndpoint != expectedEndpoint {
			rt.DeployNotifiedFailEndpoint = expectedEndpoint
			shouldNotifyDeployFail = true
			deployFailDetail = detail
		}
	} else if statusFresh {
		if k, ok := tel.StatusPayload.ParsedKind(); ok {
			rt.DetectedWorkloadKind = &k
		}
		rt.WorkloadName = tel.StatusPayload.Name
		rt.WorkloadMetricLabel = tel.StatusPayload.MetricLabel
		rt.WorkloadMetricValue = tel.StatusPayload.MetricValue
		rt.WorkloadMetricUnit = tel.StatusPayload.MetricUnit
		rt.WorkloadMetricRatio = tel.StatusPayload.MetricRatio
		rt.WorkloadDetail = tel.StatusPayload.Detail
		// Clear stale cloudflared URL when a fresh status file reports no public UI URL
		if tel.StatusPayload.UIURL == "" {
			rt.DisplayUIURL = ""
		}
	}
	if tel.ComfyQueue != nil {
		rt.QueueRunning = tel.ComfyQueue.Running
		rt.QueuePending = tel.ComfyQueue.Pending
		if tel.ComfyQueue.IsBusy() {
			rt.SawQueueBusyThisRun = true
		}
	}

	if !acc.EnableLLMModule {
		handled := false
		if rt.DeployRunning {
			handled = true
			rt.SignalSource = core.SignalSourceStatusFile
			rt.ActivitySignal = core.ActivitySignal{Kind: core.SignalBusy, ObservedAt: now}
			rt.IdleSeconds = 0
			rt.WarnedForCurrentIdle = false
		}
		if !handled && statusFresh {
			if busy, ok := tel.StatusPayload.IsWorkloadBusy(); ok {
				handled = true
				rt.SignalSource = core.SignalSourceStatusFile
				if busy {
					rt.ActivitySignal = core.ActivitySignal{Kind: core.SignalBusy, ObservedAt: now}
					rt.IdleSeconds = 0
					rt.WarnedForCurrentIdle = false
				} else {
					rt.ActivitySignal = core.ActivitySignal{Kind: core.SignalIdle, ObservedAt: now}
				}
			}
		}
		if !handled && tel.ComfyQueue != nil {
			handled = true
			rt.SignalSource = core.SignalSourceComfyQueue
			if tel.ComfyQueue.IsBusy() {
				rt.ActivitySignal = core.ActivitySignal{Kind: core.SignalBusy, ObservedAt: now}
				rt.IdleSeconds = 0
				rt.WarnedForCurrentIdle = false
			} else {
				rt.ActivitySignal = core.ActivitySignal{Kind: core.SignalIdle, ObservedAt: now}
			}
		}
		if !handled && acc.EnableHardwareIdle {
			sig := core.EvaluateHardwareActivity(tel.GPUUtil, tel.CPUUtil, rt.IsTPUHardware(), now)
			rt.ActivitySignal = sig
			if sig.Kind != core.SignalUnknown {
				rt.SignalSource = core.SignalSourceHardwareDualLowLoad
				if sig.IsBusy() {
					rt.IdleSeconds = 0
					rt.WarnedForCurrentIdle = false
				}
			}
		}
	}

	shouldAutoOpenComfy := acc.DeployRecipe.IsVideoComfy() &&
		acc.AutoOpenComfyUI &&
		tel.ComfyQueue != nil &&
		!rt.DeployRunning &&
		rt.AutoOpenedEndpoint != expectedEndpoint
	if shouldAutoOpenComfy {
		rt.AutoOpenedEndpoint = expectedEndpoint
	}

	s.Runtime[acc.ID] = rt
	s.mu.Unlock()
	s.notifyUI()

	if shouldNotifyDeployFail {
		s.cancelDeployWatcher(acc.ID)
		s.SendNotification("云端部署中断", fmt.Sprintf("%s 部署未完成：%s", acc.DisplayName, deployFailDetail), true)
	}
	if shouldAutoOpenComfy {
		s.cancelDeployWatcher(acc.ID)
		go func() {
			variantLabel := acc.H3Variant.DisplayName()
			if acc.DeployRecipe == core.RecipeComfyUIGeneral {
				variantLabel = acc.DeployRecipe.DisplayName()
			}
			s.SendNotification("ComfyUI 已装好，正在打开", fmt.Sprintf("%s 的 %s 已启动，正在通过本机加密通道打开画布", acc.DisplayName, variantLabel), true)
			s.OpenComfyUI(acc.ID)
		}()
	}
}

func (s *Service) pollRunningLLMSlots() {
	accounts := s.AccountsSnapshot()
	for _, acc := range accounts {
		if !acc.EnableLLMModule {
			continue
		}
		rt := s.StateFor(acc.ID)
		if !rt.Phase.IsRunning() || rt.PublicURL == "" || rt.LLMURLBoundEndpoint != rt.ActiveEndpoint {
			continue
		}
		apiKey, _ := s.Vault.LoadLLMAPIKey(acc.ID)
		slots, ok := colab.FetchSlots(context.Background(), rt.PublicURL, apiKey)
		if !ok {
			// Mark activity unknown on probe failure so monitoring outage time is never counted as idle.
			s.mu.Lock()
			cur := s.Runtime[acc.ID]
			if cur.ActiveEndpoint == rt.ActiveEndpoint {
				cur.ServerHealthy = false
				cur.ActivitySignal = core.ActivitySignal{Kind: core.SignalUnknown}
				s.Runtime[acc.ID] = cur
			}
			s.mu.Unlock()
			s.notifyUI()
			continue
		}
		now := time.Now()
		eval := core.EvaluateLLMSlots(slots, now)

		s.mu.Lock()
		cur := s.Runtime[acc.ID]
		if cur.ActiveEndpoint == rt.ActiveEndpoint {
			cur.ServerHealthy = true
			cur.PromptTokens = eval.MaxPromptTokens
			if eval.MaxContext > 0 {
				cur.ContextLimit = eval.MaxContext
			}
			taskProgressed := eval.LatestTaskID >= 0 && cur.LastTaskID >= 0 && eval.LatestTaskID != cur.LastTaskID
			if eval.LatestTaskID >= 0 {
				cur.LastTaskID = eval.LatestTaskID
			}
			cur.ActivitySignal = eval.Signal
			cur.SignalSource = core.SignalSourceLLMSlots
			if eval.Signal.IsBusy() || taskProgressed {
				cur.IdleSeconds = 0
				cur.WarnedForCurrentIdle = false
			}
			s.Runtime[acc.ID] = cur
		}
		s.mu.Unlock()
		s.notifyUI()
	}
}

// Start / Polite Queue / Stop

func (s *Service) StartAccountSession(accountID string) {
	s.mu.Lock()
	var acc core.AccountProfile
	found := false
	for _, a := range s.Settings.Accounts {
		if a.ID == accountID {
			acc = a
			found = true
			break
		}
	}
	if !found || !s.CLIInstalled {
		s.mu.Unlock()
		return
	}
	rt := s.Runtime[accountID]
	if rt.Phase.IsBusy() || rt.Phase.IsRunning() || rt.Phase.IsQueuing() {
		s.mu.Unlock()
		return
	}
	if rt.QueueCircuitBreakTill != nil && rt.QueueCircuitBreakTill.After(time.Now()) {
		remMin := max(1, int(time.Until(*rt.QueueCircuitBreakTill).Minutes())+1)
		s.mu.Unlock()
		s.ShowToast(fmt.Sprintf("正在冷静等待中（约 %d 分钟），如已切换显卡可点横幅右侧「解除等待」", remMin))
		return
	}
	// Clear stale error banner when starting a fresh attempt
	rt.LastError = ""
	rt.LastErrorTitle = ""
	rt.LastErrorAction = ""
	rt.LastErrorRaw = ""
	rt.LastAllocHadShortage = false

	if core.EffectiveAutoQueue(acc) {
		if s.ActiveQueueAccountID != "" && s.ActiveQueueAccountID != accountID {
			s.mu.Unlock()
			s.ShowToast("已有其他账号在排队拿卡中（单通道防风控）")
			return
		}
		token := uuid.New().String()
		ctx, cancel := context.WithCancel(context.Background())
		targets := core.EffectiveQueueTargets(acc)
		targetsSummary := core.QueueTargetsSummary(targets)
		now := time.Now()
		s.ActiveQueueAccountID = accountID
		s.activeQueueToken = token
		s.queueCancels[accountID] = queueHandle{
			token:          token,
			cancel:         cancel,
			startedAt:      now,
			targetsSummary: targetsSummary,
			rounds:         1,
		}
		rt.Phase = core.InstancePhase{
			Kind:           core.PhaseQueuing,
			QueueAttempt:   1,
			QueueMax:       core.QueueMaxAttempts,
			TargetsSummary: targetsSummary,
		}
		s.Runtime[accountID] = rt
		s.mu.Unlock()
		s.notifyUI()
		go s.runPoliteQueue(ctx, acc, token)
		return
	}

	rt.Phase = core.InstancePhase{Kind: core.PhaseStarting, Detail: "申请云端显卡…"}
	s.Runtime[accountID] = rt
	s.mu.Unlock()
	s.notifyUI()

	go func() {
		startedAt := time.Now()
		targetsSummary := core.QueueTargetsSummary(core.EffectiveQueueTargets(acc))
		res := s.Engine.ProvisionSession(context.Background(), acc, nil, nil, nil, func(msg string) {
			s.mu.Lock()
			cur := s.Runtime[accountID]
			cur.Phase = core.InstancePhase{Kind: core.PhaseStarting, Detail: msg}
			s.Runtime[accountID] = cur
			s.mu.Unlock()
			s.notifyUI()
		})
		s.recordProbeInsights(res.ProbeRecords)
		endedAt := time.Now()
		durSec := max(0, int(endedAt.Sub(startedAt).Seconds()))
		if res.Outcome.Kind == core.OutcomeAllocated {
			detail := "直连开机成功"
			if res.UsedStandardRAM {
				detail = "直连开机成功（高内存暂缺，已自动切标准内存）"
			}
			s.recordHistory(core.HistoryRecord{
				Kind:           core.HistoryKindAllocation,
				AccountID:      acc.ID,
				AccountName:    acc.DisplayName,
				AccountColor:   acc.ColorTag,
				SessionName:    acc.SessionName,
				StartedAt:      startedAt,
				EndedAt:        &endedAt,
				DurationSec:    durSec,
				Hardware:       res.Outcome.Hardware,
				HighMem:        core.EffectiveUseHighMem(acc) && !res.UsedStandardRAM,
				RecipeLabel:    recipeLabelForSession(acc, false),
				TargetsSummary: targetsSummary,
				Rounds:         1,
				AllocOutcome:   core.AllocTagSuccess,
				Detail:         detail,
			})
			s.handlePostAllocation(acc, res.Outcome.Hardware, false, res.UsedStandardRAM)
		} else {
			tag := mapAllocOutcomeTag(res.Outcome)
			s.recordHistory(core.HistoryRecord{
				Kind:           core.HistoryKindAllocation,
				AccountID:      acc.ID,
				AccountName:    acc.DisplayName,
				AccountColor:   acc.ColorTag,
				SessionName:    acc.SessionName,
				StartedAt:      startedAt,
				EndedAt:        &endedAt,
				DurationSec:    durSec,
				Hardware:       res.Outcome.Hardware,
				HighMem:        core.EffectiveUseHighMem(acc),
				RecipeLabel:    recipeLabelForSession(acc, false),
				TargetsSummary: targetsSummary,
				Rounds:         1,
				AllocOutcome:   tag,
				Detail:         res.Outcome.Message,
			})
			s.handleProvisionFailure(acc, res.Outcome)
		}
	}()
}

func mapAllocOutcomeTag(outcome core.ProvisionOutcome) core.AllocationOutcomeTag {
	if outcome.Code == "CANCELLED" {
		return core.AllocTagCancelled
	}
	switch outcome.Kind {
	case core.OutcomeAllocated:
		return core.AllocTagSuccess
	case core.OutcomeCapacity503, core.OutcomeBudgetExhausted:
		return core.AllocTagShortage
	case core.OutcomeRateLimited:
		return core.AllocTagRateLimited
	default:
		return core.AllocTagError
	}
}

func isHardwareFromOption(hardware string, opt core.AcceleratorOption) bool {
	hwNorm := core.NormalizeHardwareKey(hardware)
	hwRaw := strings.ToUpper(strings.TrimSpace(hardware))
	for _, pt := range opt.ProbeTargets() {
		ptNorm := core.NormalizeHardwareKey(pt.Display)
		if hwNorm != "" && ptNorm != "" && hwNorm == ptNorm {
			return true
		}
		if hwRaw != "" && (strings.EqualFold(hwRaw, pt.Display) || (pt.Value != "" && strings.EqualFold(hwRaw, pt.Value))) {
			return true
		}
	}
	return false
}

func isFallbackAllocation(hardware string, attempt int, primaryTargets []core.AcceleratorOption, fbEnabled bool, fbAfter int, fbTarget core.AcceleratorOption) bool {
	if !fbEnabled || attempt <= max(1, fbAfter) {
		return false
	}
	for _, pt := range primaryTargets {
		if pt == fbTarget || isHardwareFromOption(hardware, pt) {
			return false
		}
	}
	return isHardwareFromOption(hardware, fbTarget)
}

func (s *Service) isQueueTokenOwnerLocked(accountID, token string) bool {
	h, ok := s.queueCancels[accountID]
	return ok && h.token == token
}

func (s *Service) markQueueAllocLoggedLocked(accountID, token string) bool {
	h, ok := s.queueCancels[accountID]
	if !ok || h.token != token || h.allocLogged {
		return false
	}
	h.allocLogged = true
	s.queueCancels[accountID] = h
	return true
}

func (s *Service) runPoliteQueue(ctx context.Context, acc core.AccountProfile, token string) {
	startedAt := time.Now()
	deadline := startedAt.Add(core.QueueMaxTotalWait)
	pruned := map[string]bool{}
	roundsCompleted := 0
	lastTargetsSummary := core.QueueTargetsSummary(core.EffectiveQueueTargets(acc))

	logQueueOnce := func(hw string, usedFallback, usedStdRAM bool, tag core.AllocationOutcomeTag, detail string) {
		s.mu.Lock()
		canLog := s.markQueueAllocLoggedLocked(acc.ID, token)
		s.mu.Unlock()
		if !canLog {
			return
		}
		endedAt := time.Now()
		s.recordHistory(core.HistoryRecord{
			Kind:            core.HistoryKindAllocation,
			AccountID:       acc.ID,
			AccountName:     acc.DisplayName,
			AccountColor:    acc.ColorTag,
			SessionName:     acc.SessionName,
			StartedAt:       startedAt,
			EndedAt:         &endedAt,
			DurationSec:     max(0, int(endedAt.Sub(startedAt).Seconds())),
			Hardware:        hw,
			HighMem:         core.EffectiveUseHighMem(acc) && !usedStdRAM,
			RecipeLabel:     recipeLabelForSession(acc, usedFallback && core.ShouldCrossModelDowngrade(acc, hw)),
			FallbackUsed:    usedFallback,
			ModelDowngraded: usedFallback && core.ShouldCrossModelDowngrade(acc, hw),
			TargetsSummary:  lastTargetsSummary,
			Rounds:          max(1, roundsCompleted),
			AllocOutcome:    tag,
			Detail:          detail,
		})
	}

	defer func() {
		s.mu.Lock()
		if s.activeQueueToken == token {
			s.ActiveQueueAccountID = ""
			s.activeQueueToken = ""
		}
		if s.isQueueTokenOwnerLocked(acc.ID, token) {
			delete(s.queueCancels, acc.ID)
			cur := s.Runtime[acc.ID]
			cur.QueueInFlight = false
			s.Runtime[acc.ID] = cur
		}
		s.mu.Unlock()
		s.notifyUI()
	}()

	for attempt := 1; attempt <= core.QueueMaxAttempts; attempt++ {
		if ctx.Err() != nil {
			logQueueOnce("", false, false, core.AllocTagCancelled, "排队已取消")
			return
		}
		// Pre-flight check: verify session not already running on server
		if preUsage, sessions, err := s.Engine.FetchUsageAndSessions(ctx, acc); err == nil {
			for _, sess := range sessions {
				if !sess.IsOrphan && sess.Name == acc.SessionName {
					logQueueOnce(sess.Hardware, false, false, core.AllocTagSuccess, "检测到目标会话已在线")
					s.handlePostAllocation(acc, sess.Hardware, false, false)
					return
				}
			}
			if preUsage != nil {
				s.mu.Lock()
				if cur, ok := s.Runtime[acc.ID]; ok && !cur.Phase.IsRunning() {
					now := time.Now()
					cur.CUBalance = preUsage.Balance
					cur.CURatePerHour = preUsage.RatePerHour
					cur.ActiveAssignments = preUsage.ActiveAssignments
					cur.LastUsageFetchAt = &now
					s.Runtime[acc.ID] = cur
				}
				s.mu.Unlock()
			}
		}

		primaryTargets := core.EffectiveQueueTargets(acc)
		fbEnabled, fbAfter, fbTarget := core.EffectiveFallback(acc)
		roundAccs := core.QueueTargetsForRound(attempt, primaryTargets, fbEnabled, fbAfter, fbTarget, acc.Tier)
		lastTargetsSummary = core.QueueTargetsSummary(roundAccs)
		roundsCompleted = attempt
		var probeTargets []core.ProbeTarget
		for _, a := range roundAccs {
			for _, pt := range a.ProbeTargets() {
				if pt.Value == "" || !pruned[strings.ToUpper(pt.Value)] {
					probeTargets = append(probeTargets, pt)
				}
			}
		}
		if len(probeTargets) == 0 {
			s.mu.RLock()
			owned := s.isQueueTokenOwnerLocked(acc.ID, token)
			s.mu.RUnlock()
			if owned {
				outcome := core.ProvisionOutcome{
					Kind:    core.OutcomeUnsupportedTier400,
					Code:    "NO_SUPPORTED_TARGET",
					Message: "当前顺位里的显卡均暂无配额，可切换到「省点模式 (L4)」或「智能挑卡」再试",
				}
				logQueueOnce("", false, false, core.AllocTagError, outcome.Message)
				s.handleProvisionFailure(acc, outcome)
			}
			return
		}

		s.mu.Lock()
		if !s.isQueueTokenOwnerLocked(acc.ID, token) {
			s.mu.Unlock()
			return
		}
		h := s.queueCancels[acc.ID]
		h.rounds = attempt
		h.targetsSummary = lastTargetsSummary
		s.queueCancels[acc.ID] = h
		cur := s.Runtime[acc.ID]
		cur.QueueInFlight = true
		cur.Phase = core.InstancePhase{
			Kind:           core.PhaseQueuing,
			QueueAttempt:   attempt,
			QueueMax:       core.QueueMaxAttempts,
			TargetsSummary: lastTargetsSummary,
		}
		s.Runtime[acc.ID] = cur
		s.mu.Unlock()
		s.notifyUI()

		res := s.Engine.ProvisionSession(ctx, acc, probeTargets, &deadline, func() time.Duration {
			return core.InterTargetDelay(rand.Float64())
		}, func(msg string) {
			s.mu.Lock()
			if s.isQueueTokenOwnerLocked(acc.ID, token) {
				c := s.Runtime[acc.ID]
				c.Phase.Detail = msg
				s.Runtime[acc.ID] = c
			}
			s.mu.Unlock()
			s.notifyUI()
		})

		s.mu.Lock()
		owned := s.isQueueTokenOwnerLocked(acc.ID, token)
		if owned {
			c := s.Runtime[acc.ID]
			c.QueueInFlight = false
			s.Runtime[acc.ID] = c
		}
		s.mu.Unlock()

		s.recordProbeInsights(res.ProbeRecords)
		for v := range res.UnsupportedValues {
			pruned[v] = true
		}

		if res.Outcome.Kind == core.OutcomeAllocated {
			usedFallback := isFallbackAllocation(res.Outcome.Hardware, attempt, primaryTargets, fbEnabled, fbAfter, fbTarget)
			detail := fmt.Sprintf("第 %d 轮抢到 %s", attempt, res.Outcome.Hardware)
			if usedFallback {
				detail += "（保底显卡）"
			}
			if res.UsedStandardRAM {
				detail += "（标准内存）"
			}
			logQueueOnce(res.Outcome.Hardware, usedFallback, res.UsedStandardRAM, core.AllocTagSuccess, detail)
			s.handlePostAllocation(acc, res.Outcome.Hardware, usedFallback, res.UsedStandardRAM)
			return
		}
		if !owned || ctx.Err() != nil {
			logQueueOnce("", false, false, core.AllocTagCancelled, "排队已取消")
			return
		}
		if res.Outcome.Kind != core.OutcomeCapacity503 {
			logQueueOnce(res.Outcome.Hardware, false, false, mapAllocOutcomeTag(res.Outcome), res.Outcome.Message)
			s.handleProvisionFailure(acc, res.Outcome)
			return
		}

		delay, ok := core.NextQueueDelay(attempt, startedAt, time.Now(), rand.Float64())
		if !ok {
			outcome := core.ProvisionOutcome{Kind: core.OutcomeBudgetExhausted, Message: "已达 5 轮 / 40 分钟合规排队上限，暂未拿到目标卡"}
			logQueueOnce("", false, false, core.AllocTagShortage, outcome.Message)
			s.handleProvisionFailure(acc, outcome)
			return
		}
		nextAt := time.Now().Add(delay)
		s.mu.Lock()
		if !s.isQueueTokenOwnerLocked(acc.ID, token) {
			s.mu.Unlock()
			return
		}
		c := s.Runtime[acc.ID]
		c.Phase = core.InstancePhase{
			Kind:           core.PhaseQueuing,
			QueueAttempt:   attempt,
			QueueMax:       core.QueueMaxAttempts,
			NextRetryAt:    &nextAt,
			TargetsSummary: lastTargetsSummary,
		}
		s.Runtime[acc.ID] = c
		s.mu.Unlock()
		s.notifyUI()

		select {
		case <-time.After(delay):
		case <-ctx.Done():
			logQueueOnce("", false, false, core.AllocTagCancelled, "排队已取消")
			return
		}
	}
}

func (s *Service) CancelQueue(accountID string) {
	s.mu.Lock()
	h, hasHandle := s.queueCancels[accountID]
	inFlight := s.Runtime[accountID].QueueInFlight
	var cancelRec *core.HistoryRecord
	if hasHandle && !h.allocLogged {
		h.allocLogged = true
		var acc core.AccountProfile
		for _, a := range s.Settings.Accounts {
			if a.ID == accountID {
				acc = a
				break
			}
		}
		endedAt := time.Now()
		startedAt := h.startedAt
		if startedAt.IsZero() {
			startedAt = endedAt
		}
		targetsSummary := h.targetsSummary
		if targetsSummary == "" && acc.ID != "" {
			targetsSummary = core.QueueTargetsSummary(core.EffectiveQueueTargets(acc))
		}
		cancelRec = &core.HistoryRecord{
			Kind:           core.HistoryKindAllocation,
			AccountID:      accountID,
			AccountName:    acc.DisplayName,
			AccountColor:   acc.ColorTag,
			SessionName:    acc.SessionName,
			StartedAt:      startedAt,
			EndedAt:        &endedAt,
			DurationSec:    max(0, int(endedAt.Sub(startedAt).Seconds())),
			HighMem:        core.EffectiveUseHighMem(acc),
			RecipeLabel:    recipeLabelForSession(acc, false),
			TargetsSummary: targetsSummary,
			Rounds:         max(1, h.rounds),
			AllocOutcome:   core.AllocTagCancelled,
			Detail:         "用户手动取消排队",
		}
	}
	delete(s.queueCancels, accountID)
	if s.ActiveQueueAccountID == accountID {
		s.ActiveQueueAccountID = ""
		s.activeQueueToken = ""
	}
	rt := s.Runtime[accountID]
	rt.QueueInFlight = false
	if rt.Phase.IsQueuing() {
		rt.Phase = core.InstancePhase{Kind: core.PhaseOffline}
		s.Runtime[accountID] = rt
	}
	s.mu.Unlock()

	if cancelRec != nil {
		s.recordHistory(*cancelRec)
	}
	if h.cancel != nil {
		h.cancel()
	}
	s.ShowToast("已取消排队")
	if inFlight {
		// Re-verify server side so an in-flight allocation is never hidden from billing monitor
		go s.refreshAccount(accountID, true, true, true)
	}
}

func (s *Service) recordProbeInsights(records []colab.ProbeAttemptRecord) {
	if len(records) == 0 {
		return
	}
	now := time.Now()
	s.mu.Lock()
	for _, rec := range records {
		s.Insights.Record(rec.Hardware, rec.Succeeded, now)
	}
	s.saveInsightsLocked()
	s.mu.Unlock()
}

func (s *Service) handleProvisionFailure(acc core.AccountProfile, outcome core.ProvisionOutcome) {
	if outcome.Code == "CANCELLED" {
		return
	}
	title := "本次申请未成功，可直接重试"
	notifyTitle := "开机未成功"
	action := "retry"
	switch outcome.Kind {
	case core.OutcomeRateLimited:
		title = "Google 提示请求偏频繁，先冷静等一会儿"
		notifyTitle = "请求偏频繁，先冷静等一下"
		action = "clear_cooldown"
	case core.OutcomePrecondition412:
		title = "已有运行中机器，达到同时开机上限"
		notifyTitle = "开机未成功：已有旧机在运行"
		action = "stop_existing"
	case core.OutcomeAuthError:
		title = "Google 账号需要重新授权"
		notifyTitle = "开机未成功：Google 授权已过期"
		action = "reauth"
	case core.OutcomeUnsupportedTier400:
		title = "当前账号暂不支持所选显卡"
		notifyTitle = "开机未成功：暂不支持所选显卡"
		action = "retry"
	case core.OutcomeBudgetExhausted:
		title = "没抢到卡，排队已结束"
		notifyTitle = "没抢到卡，排队已结束"
		action = "retry"
	case core.OutcomeCapacity503:
		title = "热门显卡暂时没货"
		notifyTitle = "没抢到卡，热门显卡暂时没货"
		action = "retry"
	}

	s.mu.Lock()
	rt := s.Runtime[acc.ID]
	rt.Phase = core.InstancePhase{Kind: core.PhaseOffline}
	rt.LastError = outcome.Message
	rt.LastErrorTitle = title
	rt.LastErrorAction = action
	rt.LastErrorRaw = outcome.RawDetail
	rt.LastAllocHadShortage = outcome.Kind == core.OutcomeCapacity503 || outcome.Kind == core.OutcomeBudgetExhausted
	// ONLY genuine rate-limit / abuse warnings trigger the 15-minute cooldown lock.
	if outcome.Kind == core.OutcomeRateLimited {
		until := time.Now().Add(core.QueueCircuitBreakerCooldown)
		rt.QueueCircuitBreakTill = &until
		if s.Settings.QueueCooldownsByAccount == nil {
			s.Settings.QueueCooldownsByAccount = map[string]time.Time{}
		}
		s.Settings.QueueCooldownsByAccount[acc.ID] = until
		s.saveSettingsLocked()
	}
	s.Runtime[acc.ID] = rt
	s.mu.Unlock()
	s.notifyUI()

	if outcome.Kind == core.OutcomePrecondition412 {
		// Refresh live sessions so any running machine appears immediately for one-click stop
		go s.refreshAccount(acc.ID, true, true, true)
	}

	s.ShowToast(outcome.Message)
	notifyBody := outcome.Message
	if strings.TrimSpace(notifyBody) == "" {
		notifyBody = "请打开主控制台查看详细原因"
	}
	s.SendNotification(notifyTitle, notifyBody, true)
}

// ClearCooldown lets the user manually clear a cooldown lock or error banner once they have switched cards or fixed the cause.
func (s *Service) ClearCooldown(accountID string) {
	s.mu.Lock()
	rt := s.Runtime[accountID]
	rt.QueueCircuitBreakTill = nil
	rt.LastError = ""
	rt.LastErrorTitle = ""
	rt.LastErrorAction = ""
	rt.LastErrorRaw = ""
	rt.LastAllocHadShortage = false
	s.Runtime[accountID] = rt
	if s.Settings.QueueCooldownsByAccount != nil {
		delete(s.Settings.QueueCooldownsByAccount, accountID)
	}
	s.saveSettingsLocked()
	s.mu.Unlock()
	s.notifyUI()
	s.ShowToast("已解除等待状态，现在可以直接重试")
}

// ClearAccountError dismisses the visible error notice on the home screen.
func (s *Service) ClearAccountError(accountID string) {
	s.mu.Lock()
	rt := s.Runtime[accountID]
	rt.LastError = ""
	rt.LastErrorTitle = ""
	rt.LastErrorAction = ""
	rt.LastErrorRaw = ""
	rt.LastAllocHadShortage = false
	s.Runtime[accountID] = rt
	s.mu.Unlock()
	s.notifyUI()
}

// ResetAccountToRecommendedDefaults restores one-click beginner-friendly defaults without removing the account or credentials.
func (s *Service) ResetAccountToRecommendedDefaults(accountID string) {
	acc, ok := s.accountByID(accountID)
	if !ok {
		return
	}
	fresh := core.NewDefaultAccountProfile(acc.DisplayName, acc.Email, acc.Tier, acc.AuthMode)
	fresh.ID = acc.ID
	fresh.ColorTag = acc.ColorTag
	fresh.SessionName = acc.SessionName
	fresh.CCSwitchProviderID = acc.CCSwitchProviderID
	fresh.CCSwitchName = acc.CCSwitchName
	s.UpdateAccount(fresh)
	s.ClearCooldown(accountID)
	s.ShowToast("已恢复小白推荐默认配置（H3 Flash 极速版 · 智能挑卡 · 自动存片关机）")
}

func (s *Service) handlePostAllocation(acc core.AccountProfile, hardware string, usedFallback, usedStandardRAM bool) {
	estRate := core.EstimatedHourlyCU(hardware)
	hwTitle := strings.TrimSpace(hardware)
	if hwTitle == "" {
		hwTitle = "云端显卡"
	}
	downgraded := core.ShouldCrossModelDowngrade(acc, hardware)
	willAutoDeploy := acc.DeployRecipe.IsVideoComfy() ||
		acc.EnableLLMModule ||
		(acc.WorkloadKind != core.WorkloadGeneral && strings.TrimSpace(acc.DriveScriptPath) != "")
	notifyTitle := fmt.Sprintf("抢到 %s 了！", hwTitle)
	notifyBody := fmt.Sprintf("%s 已开机就绪（约 %.2f CU/h），可以开始使用", acc.DisplayName, estRate)
	if willAutoDeploy {
		notifyBody = fmt.Sprintf("%s 已开机（约 %.2f CU/h），正在自动装好环境，完成后会自动打开", acc.DisplayName, estRate)
	}
	if downgraded {
		notifyTitle = fmt.Sprintf("抢到保底 %s（已自动切轻量 GGUF）", hwTitle)
		notifyBody = fmt.Sprintf("%s 已开机（约 %.2f CU/h），已自动切轻量 GGUF 部署，完成后会自动打开", acc.DisplayName, estRate)
	} else if usedFallback {
		notifyBody += "（保底显卡）"
	}
	s.SendNotification(notifyTitle, notifyBody, true)
	// Always settle transitional PhaseStarting / PhaseQueuing via authoritative server fetch
	if !s.refreshAccount(acc.ID, true, true, true) {
		usage, sessions, err := s.Engine.FetchUsageAndSessions(context.Background(), acc)
		if err == nil {
			s.mu.Lock()
			ep, running, closedRec := s.applyServerSnapshotLocked(acc.ID, acc, usage, sessions, time.Now(), true, "")
			s.mu.Unlock()
			if closedRec != nil {
				s.recordHistory(*closedRec)
			}
			s.notifyUI()
			if running {
				s.fetchAndApplyTelemetry(acc, ep)
			}
		} else {
			s.mu.Lock()
			rt := s.Runtime[acc.ID]
			if rt.Phase.Kind == core.PhaseStarting || rt.Phase.Kind == core.PhaseQueuing {
				rt.Phase = core.InstancePhase{Kind: core.PhaseRunning}
				rt.ActiveHardware = hardware
				s.Runtime[acc.ID] = rt
			}
			s.mu.Unlock()
			s.notifyUI()
		}
	}

	s.mu.Lock()
	if cur, ok := s.Runtime[acc.ID]; ok {
		cur.LastAllocHadShortage = false
		if cur.Phase.IsRunning() {
			cur.SessionFallbackUsed = usedFallback
			cur.SessionModelDowngraded = downgraded
			cur.SessionUsedStandardRAM = usedStandardRAM
		}
		s.Runtime[acc.ID] = cur
	}
	s.mu.Unlock()
	s.notifyUI()

	if acc.DeployRecipe.IsVideoComfy() {
		deployAcc := acc
		if downgraded {
			deployAcc.H3Variant = core.H3VariantFlashGGUF
		}
		s.launchVideoDeployFlow(deployAcc)
		return
	}

	if acc.EnableLLMModule || (acc.WorkloadKind != core.WorkloadGeneral && strings.TrimSpace(acc.DriveScriptPath) != "") {
		if s.Engine.EnsureDriveMounted(context.Background(), acc) {
			ok, _, errMsg := s.Engine.RunDriveStartScript(context.Background(), acc)
			if ok {
				if acc.EnableLLMModule {
					if key := s.Engine.FetchRemoteAPIKeySafely(context.Background(), acc); key != "" {
						_ = s.Vault.SaveLLMAPIKey(acc.ID, key)
					}
				}
				_ = s.refreshAccount(acc.ID, true, true, true)
				if acc.EnableLLMModule {
					s.SyncAllEnabledClients(acc.ID, true)
				}
			} else if errMsg != "" {
				s.ShowToast("远端启动脚本提示：" + errMsg)
			}
		}
	}
}

// ApplyDeployRecipe updates an account's one-click deploy preset and aligns ports/workload/idle defaults cleanly.
func (s *Service) ApplyDeployRecipe(accountID string, recipe core.DeployRecipe) {
	acc, ok := s.accountByID(accountID)
	if !ok {
		return
	}
	acc.DeployRecipe = recipe
	switch recipe {
	case core.RecipeH3Video, core.RecipeComfyUIGeneral:
		acc.WorkloadKind = core.WorkloadVideo
		acc.EnableLLMModule = false
		acc.RemoteServicePort = 8188
		acc.LocalSSHTunnelPort = 8188
		if acc.QueueDrainMinutes < 5 {
			acc.QueueDrainMinutes = 10
		}
		if acc.IdleTimeoutMinutes == 40 {
			acc.IdleTimeoutMinutes = acc.QueueDrainMinutes
		}
	case core.RecipeLLMProxy:
		acc.WorkloadKind = core.WorkloadLLM
		acc.EnableLLMModule = true
		acc.RemoteServicePort = 18080
		acc.LocalSSHTunnelPort = 18080
		if acc.IdleTimeoutMinutes <= 15 {
			acc.IdleTimeoutMinutes = 40
		}
	default:
		acc.WorkloadKind = core.WorkloadGeneral
		acc.EnableLLMModule = false
	}
	s.UpdateAccount(acc)
	s.ClearAccountError(accountID)
}

// DeployNow starts a machine (and deploys after allocation) if offline, or immediately pushes the built-in deploy script if online.
func (s *Service) DeployNow(accountID string) {
	acc, ok := s.accountByID(accountID)
	if !ok {
		return
	}
	if acc.DeployRecipe == core.RecipeH3Video {
		if _, err := core.BuildDeployEnv(acc); err != nil {
			s.ShowToast(err.Error())
			return
		}
	}
	rt := s.StateFor(accountID)
	if !rt.Phase.IsRunning() {
		s.StartAccountSession(accountID)
		return
	}
	if acc.DeployRecipe.IsVideoComfy() {
		go s.launchVideoDeployFlow(acc)
	} else {
		s.ShowToast("机器已在运行中")
	}
}

func (s *Service) cancelDeployWatcher(accountID string) {
	s.mu.Lock()
	if wh, ok := s.deployWatchCancels[accountID]; ok {
		if wh.cancel != nil {
			wh.cancel()
		}
		delete(s.deployWatchCancels, accountID)
	}
	s.mu.Unlock()
}

func (s *Service) clearDeployWatcherIfToken(accountID, token string) {
	s.mu.Lock()
	if wh, ok := s.deployWatchCancels[accountID]; ok && wh.token == token {
		if wh.cancel != nil {
			wh.cancel()
		}
		delete(s.deployWatchCancels, accountID)
	}
	s.mu.Unlock()
}

func (s *Service) launchVideoDeployFlow(acc core.AccountProfile) {
	envVars, err := core.BuildDeployEnv(acc)
	if err != nil {
		s.ShowToast(err.Error())
		return
	}

	// Atomically guard against duplicate concurrent launches & immediately show live deploy progress before Drive mount
	s.mu.Lock()
	cur := s.Runtime[acc.ID]
	if cur.DeployRunning {
		s.mu.Unlock()
		s.ShowToast("云端正在自动部署中，可点「查看日志」跟踪实时进度")
		return
	}
	cur.DeployRunning = true
	cur.DeployFailed = false
	cur.DeployError = ""
	cur.DeployNotifiedFailEndpoint = ""
	cur.DeployStartingUntil = time.Now().Add(95 * time.Second)
	cur.WorkloadMetricLabel = "正在部署"
	cur.WorkloadMetricValue = "5%"
	cur.WorkloadMetricUnit = "准备环境"
	initRatio := 0.05
	cur.WorkloadMetricRatio = &initRatio
	if acc.UseGoogleDrive {
		cur.WorkloadDetail = "正在检查并挂载 Google Drive…"
	} else {
		cur.WorkloadDetail = fmt.Sprintf("正在向云端发送 %s 部署脚本…", acc.H3Variant.DisplayName())
	}
	cur.AutoOpenedEndpoint = ""
	s.Runtime[acc.ID] = cur
	s.mu.Unlock()
	s.notifyUI()

	if acc.UseGoogleDrive {
		s.ShowToast("正在挂载 Google Drive（复用模型并自动存成片）…")
		if mounted, reason := s.Engine.EnsureDriveMountedWithReason(context.Background(), acc); !mounted {
			if reason == "" {
				reason = "Google Drive 暂未完成授权，先直接云端运行（成片请记得保存）"
			}
			s.mu.Lock()
			if st, ok := s.Runtime[acc.ID]; ok {
				st.WorkloadDetail = reason
				s.Runtime[acc.ID] = st
			}
			s.mu.Unlock()
			s.notifyUI()
			s.ShowToast(reason)
		}
	}

	s.mu.Lock()
	if st, ok := s.Runtime[acc.ID]; ok {
		st.WorkloadMetricValue = "8%"
		st.WorkloadMetricUnit = "推送脚本"
		r8 := 0.08
		st.WorkloadMetricRatio = &r8
		st.WorkloadDetail = fmt.Sprintf("正在向云端推送 %s 自动安装脚本…", acc.H3Variant.DisplayName())
		s.Runtime[acc.ID] = st
	}
	s.mu.Unlock()
	s.notifyUI()

	scriptBody := templates.H3VideoScript
	if acc.DeployRecipe == core.RecipeComfyUIGeneral {
		scriptBody = templates.ComfyUIVideoScript
	}
	if err := s.Engine.StartBuiltinDeployScript(context.Background(), acc, scriptBody, envVars); err != nil {
		errMsg := strings.TrimSpace(err.Error())
		if errMsg == "" {
			errMsg = "未能把部署脚本送到云端机器"
		}
		s.mu.Lock()
		st := s.Runtime[acc.ID]
		st.DeployRunning = false
		st.DeployFailed = true
		st.DeployError = errMsg
		st.DeployStartingUntil = time.Time{}
		st.WorkloadMetricLabel = "部署中断"
		st.WorkloadMetricValue = "失败"
		st.WorkloadMetricUnit = "未启动"
		zero := 0.0
		st.WorkloadMetricRatio = &zero
		st.WorkloadDetail = errMsg
		s.Runtime[acc.ID] = st
		s.mu.Unlock()
		s.notifyUI()
		s.ShowToast("送出部署脚本失败：" + errMsg)
		s.SendNotification("云端部署未能启动", fmt.Sprintf("%s 推送部署脚本失败：%s", acc.DisplayName, errMsg), true)
		return
	}

	s.mu.Lock()
	cur = s.Runtime[acc.ID]
	cur.DeployRunning = true
	cur.DeployFailed = false
	cur.DeployError = ""
	cur.DeployStartingUntil = time.Now().Add(20 * time.Second)
	cur.WorkloadMetricLabel = "正在部署"
	cur.WorkloadMetricValue = "10%"
	cur.WorkloadMetricUnit = "准备中"
	ratio := 0.10
	cur.WorkloadMetricRatio = &ratio
	cur.WorkloadDetail = fmt.Sprintf("已启动云端部署 %s，正在安装环境…", acc.H3Variant.DisplayName())
	cur.AutoOpenedEndpoint = ""
	s.Runtime[acc.ID] = cur
	s.mu.Unlock()
	s.notifyUI()
	s.ShowToast("已把部署脚本送到云端，可直接在主屏看到实时进度或点「查看日志」")

	// Start a bounded 12s progress watcher (up to 18 minutes) scoped by unique token so old watchers never cancel newer deploys
	s.cancelDeployWatcher(acc.ID)
	watchToken := uuid.New().String()
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.deployWatchCancels[acc.ID] = deployWatchHandle{token: watchToken, cancel: cancel}
	s.mu.Unlock()

	go func() {
		defer s.clearDeployWatcherIfToken(acc.ID, watchToken)
		firstCheck := time.NewTimer(4 * time.Second)
		defer firstCheck.Stop()
		tk := time.NewTicker(12 * time.Second)
		defer tk.Stop()
		deadline := time.Now().Add(18 * time.Minute)
		pollOnce := func() bool {
			latestAcc, ok := s.accountByID(acc.ID)
			if !ok {
				return true
			}
			rt := s.StateFor(acc.ID)
			if !rt.Phase.IsRunning() || rt.ActiveEndpoint == "" {
				return true
			}
			s.fetchAndApplyTelemetry(latestAcc, rt.ActiveEndpoint)
			after := s.StateFor(acc.ID)
			return !after.DeployRunning && (after.ServerHealthy || after.DeployFailed)
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.stopLoops:
				return
			case <-firstCheck.C:
				if pollOnce() {
					return
				}
			case <-tk.C:
				if time.Now().After(deadline) {
					return
				}
				if pollOnce() {
					return
				}
			}
		}
	}()
}

func (s *Service) StopAccountSession(accountID, specificSession, reason string) {
	s.mu.Lock()
	var acc core.AccountProfile
	found := false
	for _, a := range s.Settings.Accounts {
		if a.ID == accountID {
			acc = a
			found = true
			break
		}
	}
	if !found {
		s.mu.Unlock()
		return
	}
	targetSession := specificSession
	if targetSession == "" {
		targetSession = acc.SessionName
	}
	prevPhase := s.Runtime[accountID].Phase
	if targetSession == acc.SessionName {
		rt := s.Runtime[accountID]
		rt.Phase = core.InstancePhase{Kind: core.PhaseStopping, Detail: "停止计费实例…"}
		s.Runtime[accountID] = rt
	}
	s.mu.Unlock()
	s.notifyUI()

	if targetSession == acc.SessionName {
		s.cancelDeployWatcher(accountID)
		s.StopSSHTunnel(accountID)
	}
	stopRes := s.Engine.StopSession(context.Background(), acc, targetSession)

	// Independent authoritative server-side verification (never skipped by concurrent refreshing lock)
	usage, sessions, verifyErr := s.Engine.FetchUsageAndSessions(context.Background(), acc)
	if verifyErr != nil {
		s.mu.Lock()
		rt := s.Runtime[accountID]
		if targetSession == acc.SessionName && rt.Phase.Kind == core.PhaseStopping {
			if prevPhase.IsRunning() {
				rt.Phase = prevPhase
			} else {
				rt.Phase = core.InstancePhase{Kind: core.PhaseError, Detail: "停机后核验失败"}
			}
		}
		rt.LastError = verifyErr.Error()
		s.Runtime[accountID] = rt
		s.mu.Unlock()
		s.notifyUI()
		extra := ""
		if stopRes.ExitCode != 0 {
			extra = fmt.Sprintf("（停机命令返回码 %d）", stopRes.ExitCode)
		}
		msg := fmt.Sprintf("警告：%s 会话 %s 停机后未能从服务端核验%s，请前往 Colab 网页确认！", acc.DisplayName, targetSession, extra)
		s.ShowToast(msg)
		s.SendNotification("停机未能核验", msg, true)
		return
	}

	shutdownHint := core.ShutdownReason("")
	if targetSession == acc.SessionName {
		shutdownHint = mapShutdownReason(reason)
	}
	s.mu.Lock()
	ep, isRunning, closedRec := s.applyServerSnapshotLocked(accountID, acc, usage, sessions, time.Now(), true, shutdownHint)
	s.mu.Unlock()
	if closedRec != nil {
		s.recordHistory(*closedRec)
	}
	s.notifyUI()
	if isRunning {
		go s.fetchAndApplyTelemetry(acc, ep)
	}

	stillAlive := false
	for _, sess := range sessions {
		if !sess.IsOrphan && sess.Name == targetSession {
			stillAlive = true
			break
		}
	}
	if stillAlive {
		msg := fmt.Sprintf("警告：%s 会话 %s 仍出现在服务端，请前往 Colab 网页确认停机！", acc.DisplayName, targetSession)
		s.ShowToast(msg)
		s.SendNotification("停机核验异常", msg, true)
	} else if reason != "" {
		s.ShowToast(fmt.Sprintf("%s：%s 已确认关机止损", reason, targetSession))
	} else {
		s.ShowToast(fmt.Sprintf("%s 已确认关机止损", targetSession))
	}
}

// StopAllManagedSessions stops primary + all extra non-orphan sessions across all accounts and reports orphans.
func (s *Service) StopAllManagedSessions() {
	type target struct {
		accID   string
		session string
	}
	s.mu.RLock()
	var targets []target
	for _, acc := range s.Settings.Accounts {
		rt := s.Runtime[acc.ID]
		seen := map[string]bool{}
		if rt.Phase.IsRunning() {
			seen[acc.SessionName] = true
			targets = append(targets, target{acc.ID, acc.SessionName})
		}
		for _, sess := range rt.Sessions {
			if !sess.IsOrphan && !seen[sess.Name] {
				seen[sess.Name] = true
				targets = append(targets, target{acc.ID, sess.Name})
			}
		}
	}
	s.mu.RUnlock()

	if len(targets) == 0 {
		if orphans := s.TotalOrphanSessionCount(); orphans > 0 {
			s.ShowToast(fmt.Sprintf("存在 %d 个外部孤儿会话 [?] 无法通过 CLI 关闭，请前往 Colab 网页释放", orphans))
		}
		return
	}
	for _, t := range targets {
		s.StopAccountSession(t.accID, t.session, "全部关机止损")
	}
	stillManaged := s.TotalManagedRunningSessionCount()
	orphans := s.TotalOrphanSessionCount()
	if stillManaged > 0 || orphans > 0 {
		var parts []string
		if stillManaged > 0 {
			parts = append(parts, fmt.Sprintf("%d 个会话未能确认停止", stillManaged))
		}
		if orphans > 0 {
			parts = append(parts, fmt.Sprintf("%d 个外部孤儿会话 [?] 需前往网页关闭", orphans))
		}
		s.ShowToast("止损提示：" + strings.Join(parts, "，"))
	} else {
		s.ShowToast(fmt.Sprintf("已停止全部 %d 个受管会话", len(targets)))
	}
}

// Integrations, SSH Tunnel, Terminal & Browser Actions

func (s *Service) SyncAllEnabledClients(accountID string, silent bool) {
	acc, ok := s.accountByID(accountID)
	if !ok {
		return
	}
	rt := s.StateFor(accountID)
	if rt.PublicURL == "" {
		if !silent {
			s.ShowToast("实例尚未提供在线公网地址，暂无法同步")
		}
		return
	}
	apiKey, _ := s.Vault.LoadLLMAPIKey(accountID)
	ids := integrations.IdentifiersForAccount(accountID)
	var synced []string
	var errs []string

	if acc.AutoSyncCCSwitch && apiKey != "" {
		if _, err := integrations.SyncCCSwitch(acc.CCSwitchProviderID, acc.CCSwitchName, rt.PublicURL, apiKey, rt.ModelAlias, rt.ContextLimit, rt.ShortGPUName(), acc.DisplayName, "", ""); err != nil {
			errs = append(errs, "CC Switch: "+err.Error())
		} else {
			s.mu.Lock()
			c := s.Runtime[accountID]
			c.CCSwitchSynced = true
			s.Runtime[accountID] = c
			s.mu.Unlock()
			synced = append(synced, "CC Switch")
		}
	}
	if acc.AutoSyncOpenCode {
		if _, err := integrations.SyncOpenCode(rt.PublicURL, rt.ModelAlias, ids.OpenCodeProviderKey, ids.EnvKey, fmt.Sprintf("Colab (%s)", acc.DisplayName), "", ""); err != nil {
			errs = append(errs, "OpenCode: "+err.Error())
		} else {
			s.mu.Lock()
			c := s.Runtime[accountID]
			c.OpenCodeSynced = true
			s.Runtime[accountID] = c
			s.mu.Unlock()
			synced = append(synced, "OpenCode")
		}
	}
	if acc.AutoSyncCodex {
		if _, err := integrations.SyncCodexCLI(rt.PublicURL, rt.ModelAlias, ids.CodexTableKey, ids.EnvKey, fmt.Sprintf("Colab (%s)", acc.DisplayName), "", ""); err != nil {
			errs = append(errs, "Codex: "+err.Error())
		} else {
			s.mu.Lock()
			c := s.Runtime[accountID]
			c.CodexSynced = true
			s.Runtime[accountID] = c
			s.mu.Unlock()
			synced = append(synced, "Codex CLI")
		}
	}
	s.notifyUI()
	if !silent {
		if len(errs) > 0 {
			s.ShowToast("同步部分异常：" + strings.Join(errs, "；"))
		} else if len(synced) > 0 {
			s.ShowToast("已安全同步至：" + strings.Join(synced, " / "))
		} else {
			s.ShowToast("请先在「接入工具」中开启至少一个同步目标")
		}
	}
}

func (s *Service) LaunchClaudeTerminal(accountID string) {
	if _, ok := s.accountByID(accountID); !ok {
		return
	}
	rt := s.StateFor(accountID)
	if rt.PublicURL == "" {
		s.ShowToast("模型服务尚未在线")
		return
	}
	apiKey, _ := s.Vault.LoadLLMAPIKey(accountID)
	ids := integrations.IdentifiersForAccount(accountID)
	cmd := fmt.Sprintf("export ANTHROPIC_BASE_URL=%s ANTHROPIC_AUTH_TOKEN=%s ANTHROPIC_MODEL=%s %s=%s && claude",
		runner.ShellEscapePOSIX(rt.PublicURL),
		runner.ShellEscapePOSIX(apiKey),
		runner.ShellEscapePOSIX(rt.ModelAlias),
		ids.EnvKey,
		runner.ShellEscapePOSIX(apiKey),
	)
	if err := openTerminalCommand(cmd); err != nil {
		s.ShowToast("拉起终端失败：" + err.Error())
	} else {
		s.ShowToast("已在独立终端安全拉起 Claude Code")
	}
}

func pickAvailableLocalPort(preferred int, used map[int]bool) int {
	isFree := func(p int) bool {
		if p < 1024 || p > 65535 || used[p] {
			return false
		}
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err != nil {
			return false
		}
		_ = ln.Close()
		return true
	}
	if isFree(preferred) {
		return preferred
	}
	for offset := 1; offset <= 32; offset++ {
		if isFree(preferred + offset) {
			return preferred + offset
		}
	}
	if ln, err := net.Listen("tcp", "127.0.0.1:0"); err == nil {
		addr := ln.Addr().(*net.TCPAddr)
		_ = ln.Close()
		return addr.Port
	}
	return preferred
}

func (s *Service) ToggleSSHTunnel(accountID string) {
	s.mu.RLock()
	_, active := s.sshTunnels[accountID]
	usedPorts := map[int]bool{}
	for id, rt := range s.Runtime {
		if id != accountID && rt.SSHTunnelActive && rt.ActiveSSHTunnelPort != nil {
			usedPorts[*rt.ActiveSSHTunnelPort] = true
		}
	}
	s.mu.RUnlock()
	if active {
		s.StopSSHTunnel(accountID)
		s.ShowToast("本机加密通道已断开")
		return
	}
	acc, ok := s.accountByID(accountID)
	if !ok {
		return
	}
	rt := s.StateFor(accountID)
	if !rt.Phase.IsRunning() || rt.IsFreeRuntimeRestricted(acc) {
		s.ShowToast("当前实例未运行或受免费版配额限制")
		return
	}
	remotePort := acc.RemoteServicePort
	if rt.DetectedServicePort != nil {
		remotePort = *rt.DetectedServicePort
	}
	localPort := pickAvailableLocalPort(acc.LocalSSHTunnelPort, usedPorts)
	adcPath := ""
	if acc.AuthMode == core.AuthIsolatedVaultADC {
		if raw, err := s.Vault.LoadADCCredentials(acc.ID); err == nil {
			if p, err := s.Vault.WriteEphemeralFile("ssh-adc", "json", []byte(raw)); err == nil {
				adcPath = p
			}
		}
	}
	proc, err := startSSHTunnelProcess(s.OSRunner, acc, localPort, remotePort, adcPath)
	if err != nil {
		if adcPath != "" {
			vault.ShredAndRemove(adcPath)
		}
		s.ShowToast("启动 SSH 隧道失败：" + err.Error())
		return
	}
	s.mu.Lock()
	s.sshTunnels[accountID] = proc
	if adcPath != "" {
		s.sshADCPaths[accountID] = adcPath
	}
	cur := s.Runtime[accountID]
	cur.SSHTunnelActive = true
	p := localPort
	cur.ActiveSSHTunnelPort = &p
	s.Runtime[accountID] = cur
	s.mu.Unlock()
	s.notifyUI()
	s.ShowToast(fmt.Sprintf("SSH 隧道已建立：127.0.0.1:%d → :%d", localPort, remotePort))

	go func() {
		_ = proc.Wait()
		s.stopSSHTunnelIfMatches(accountID, proc)
	}()
}

func (s *Service) stopSSHTunnelIfMatches(accountID string, proc *exec.Cmd) {
	s.mu.RLock()
	cur := s.sshTunnels[accountID]
	s.mu.RUnlock()
	if cur == proc {
		s.StopSSHTunnel(accountID)
	}
}

// OpenComfyUI ensures a local encrypted SSH tunnel to ComfyUI (:8188) is live, waits briefly for localhost port readiness, and opens the default browser.
func (s *Service) OpenComfyUI(accountID string) {
	acc, ok := s.accountByID(accountID)
	if !ok {
		return
	}
	rt := s.StateFor(accountID)
	if !rt.Phase.IsRunning() {
		s.ShowToast("请先开机并部署后再打开 ComfyUI")
		return
	}
	if rt.DeployRunning {
		step := strings.TrimSpace(rt.WorkloadMetricValue + " " + rt.WorkloadMetricUnit)
		if step != "" {
			s.ShowToast(fmt.Sprintf("ComfyUI 正在自动部署中（%s），装好后会自动打开", step))
		} else {
			s.ShowToast("ComfyUI 正在自动部署中，可点「查看日志」看进度")
		}
		return
	}
	if rt.DeployFailed {
		s.ShowToast("上次云端部署中断，请先点「查看日志」排查或点「重新部署」")
		return
	}
	if !rt.ServerHealthy {
		s.ShowToast("云端机器尚未安装 ComfyUI，请先点「立即部署」")
		return
	}
	if rt.IsFreeRuntimeRestricted(acc) {
		if rt.DisplayUIURL != "" && s.URLOpener != nil {
			s.URLOpener(rt.DisplayUIURL)
			return
		}
		s.ShowToast("免费版不支持本机加密通道，请打开「cloudflared 临时链接」或直接点 Colab 网页")
		return
	}
	if !rt.SSHTunnelActive {
		s.ToggleSSHTunnel(accountID)
	}
	rt = s.StateFor(accountID)
	if !rt.SSHTunnelActive || rt.ActiveSSHTunnelPort == nil {
		s.ShowToast("本机加密通道未能启动，请检查本机 OpenSSH 或重试")
		return
	}
	port := *rt.ActiveSSHTunnelPort
	// Verify BOTH that this account's own tunnel process stays alive AND its bound localhost port accepts TCP connections
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(8 * time.Second)
	connected := false
	for time.Now().Before(deadline) {
		s.mu.RLock()
		_, stillAlive := s.sshTunnels[accountID]
		s.mu.RUnlock()
		if !stillAlive {
			break
		}
		conn, err := net.DialTimeout("tcp", addr, 350*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			connected = true
			break
		}
		time.Sleep(350 * time.Millisecond)
	}
	if !connected {
		s.ShowToast(fmt.Sprintf("本机通道 127.0.0.1:%d 暂未连通，请点「查看日志」核对 8188 端口", port))
		return
	}
	if s.URLOpener != nil {
		s.URLOpener("http://" + addr)
	}
}

// RefreshDeployLog fetches recent remote deploy/ComfyUI logs asynchronously into runtime state for the log viewer overlay.
func (s *Service) RefreshDeployLog(accountID string) {
	acc, ok := s.accountByID(accountID)
	if !ok {
		return
	}
	rt := s.StateFor(accountID)
	if !rt.Phase.IsRunning() {
		s.mu.Lock()
		cur := s.Runtime[accountID]
		cur.DeployLogLoading = false
		cur.DeployLogTail = "当前机器未运行。开机并部署后可在这里实时查看云端安装进度与日志。"
		s.Runtime[accountID] = cur
		s.mu.Unlock()
		s.notifyUI()
		return
	}
	s.mu.Lock()
	cur := s.Runtime[accountID]
	if cur.DeployLogLoading {
		s.mu.Unlock()
		return
	}
	cur.DeployLogLoading = true
	s.Runtime[accountID] = cur
	s.mu.Unlock()
	s.notifyUI()

	go func() {
		text, err := s.Engine.FetchDeployLogTail(context.Background(), acc, 80)
		s.mu.Lock()
		st := s.Runtime[accountID]
		st.DeployLogLoading = false
		if err != nil {
			st.DeployLogTail = "拉取云端日志失败：" + err.Error()
		} else {
			st.DeployLogTail = text
		}
		s.Runtime[accountID] = st
		s.mu.Unlock()
		s.notifyUI()
	}()
}

// RefreshAccountTelemetryNow immediately triggers a fresh remote telemetry poll for a running account.
func (s *Service) RefreshAccountTelemetryNow(accountID string) {
	acc, ok := s.accountByID(accountID)
	if !ok {
		return
	}
	rt := s.StateFor(accountID)
	if !rt.Phase.IsRunning() || rt.ActiveEndpoint == "" {
		go s.RefreshAllAccounts(true, false)
		return
	}
	go func() {
		s.fetchAndApplyTelemetry(acc, rt.ActiveEndpoint)
		s.ShowToast("已刷新云端实时状态")
	}()
}

func (s *Service) OpenDriveOutputs() {
	if s.URLOpener != nil {
		s.URLOpener("https://drive.google.com/drive/search?q=ColabTools")
	}
}

func (s *Service) StopSSHTunnel(accountID string) {
	s.mu.Lock()
	proc := s.sshTunnels[accountID]
	adc := s.sshADCPaths[accountID]
	delete(s.sshTunnels, accountID)
	delete(s.sshADCPaths, accountID)
	if cur, ok := s.Runtime[accountID]; ok {
		cur.SSHTunnelActive = false
		cur.ActiveSSHTunnelPort = nil
		s.Runtime[accountID] = cur
	}
	s.mu.Unlock()
	if proc != nil && proc.Process != nil {
		_ = proc.Process.Kill()
	}
	if adc != "" {
		vault.ShredAndRemove(adc)
	}
	s.notifyUI()
}

func (s *Service) OpenColabInBrowser(accountID string) {
	acc, ok := s.accountByID(accountID)
	if !ok {
		return
	}
	go func() {
		if u := s.Engine.ResolveColabURL(context.Background(), acc, acc.SessionName); u != "" && s.URLOpener != nil {
			s.URLOpener(u)
		} else {
			s.ShowToast("未能获取 Colab 网页链接")
		}
	}()
}

func (s *Service) OpenWorkloadWebUI(accountID string) {
	rt := s.StateFor(accountID)
	if rt.DisplayUIURL != "" && s.URLOpener != nil {
		s.URLOpener(rt.DisplayUIURL)
		return
	}
	if rt.ActiveSSHTunnelPort != nil && s.URLOpener != nil {
		s.URLOpener(fmt.Sprintf("http://127.0.0.1:%d", *rt.ActiveSSHTunnelPort))
		return
	}
	s.ShowToast("请先建立 SSH 隧道或等待远端 WebUI 报告地址")
}

func (s *Service) OpenColabPayPage() {
	if s.URLOpener != nil {
		s.URLOpener("https://colab.research.google.com/signup")
	}
}

func (s *Service) accountByID(id string) (core.AccountProfile, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, a := range s.Settings.Accounts {
		if a.ID == id {
			return a, true
		}
	}
	return core.AccountProfile{}, false
}

// 1-second loop: idle watchdog & off-peak scheduled starts
func (s *Service) handleOneSecondTick(now time.Time) {
	var toShutdown []string
	var toWarn []struct {
		name string
		rem  int
	}
	var toScheduleStart []string
	needsRepaint := false

	s.mu.Lock()
	for i := range s.Settings.Accounts {
		acc := &s.Settings.Accounts[i]
		rt := s.Runtime[acc.ID]

		if rt.Phase.IsQueuing() {
			needsRepaint = true
		}

		// Idle watchdog progression (only accumulate while fresh idle evidence is observed)
		if rt.Phase.IsRunning() {
			rt.SessionUptimeSec++
			maxAge := core.DefaultSignalMaxAge
			if !acc.EnableLLMModule {
				maxAge = core.HardwareSignalMaxAge
			}
			freshIdle := rt.ActivitySignal.Kind == core.SignalIdle &&
				!rt.ActivitySignal.ObservedAt.IsZero() &&
				now.Sub(rt.ActivitySignal.ObservedAt) <= maxAge
			if acc.AutoShutdownEnabled && freshIdle {
				rt.IdleSeconds++
			}
			allowFallback := acc.EnableHardwareIdle || rt.SignalSource == core.SignalSourceStatusFile || rt.SignalSource == core.SignalSourceComfyQueue
			effectiveTimeout := core.EffectiveIdleTimeoutMinutes(*acc, rt)
			act := core.EvaluateWatchdog(core.WatchdogInput{
				IsRunning:             true,
				EnableLLMModule:       acc.EnableLLMModule,
				ActivitySignal:        rt.ActivitySignal,
				AutoShutdownEnabled:   acc.AutoShutdownEnabled,
				IdleSeconds:           rt.IdleSeconds,
				TimeoutMinutes:        effectiveTimeout,
				WarnBeforeMinutes:     acc.WarnBeforeMinutes,
				AlreadyWarned:         rt.WarnedForCurrentIdle,
				Now:                   now,
				AllowHardwareFallback: allowFallback,
			})
			switch act.Kind {
			case core.WatchdogWarn:
				rt.WarnedForCurrentIdle = true
				toWarn = append(toWarn, struct {
					name string
					rem  int
				}{acc.DisplayName, act.RemainingMinutes})
			case core.WatchdogShutdown:
				toShutdown = append(toShutdown, acc.ID)
			}
			s.Runtime[acc.ID] = rt
		}

		// Off-peak scheduled start evaluation
		if acc.ScheduledStartEnabled && acc.ScheduledNextFireAt != nil {
			dec := core.EvaluateSchedule(*acc.ScheduledNextFireAt, acc.ScheduledStartRepeatDaily, acc.ScheduledStartHour, acc.ScheduledStartMinute, now)
			switch dec.Kind {
			case core.ScheduleTriggerNow:
				acc.ScheduledNextFireAt = dec.NextFireAt
				if !acc.ScheduledStartRepeatDaily {
					acc.ScheduledStartEnabled = false
				}
				s.saveSettingsLocked()
				if !rt.Phase.IsRunning() && !rt.Phase.IsQueuing() && !rt.Phase.IsBusy() {
					toScheduleStart = append(toScheduleStart, acc.ID)
				}
			case core.ScheduleMissedExpired:
				acc.ScheduledNextFireAt = dec.NextFireAt
				if !acc.ScheduledStartRepeatDaily {
					acc.ScheduledStartEnabled = false
				}
				s.saveSettingsLocked()
			}
		}
	}
	s.mu.Unlock()

	for _, w := range toWarn {
		s.SendNotification("即将空闲关机", fmt.Sprintf("%s 将在约 %d 分钟后自动关机止损，可在主控台点击「续命」", w.name, w.rem), true)
	}
	for _, id := range toShutdown {
		go s.StopAccountSession(id, "", "空闲自动关机")
	}
	for _, id := range toScheduleStart {
		s.SendNotification("定时错峰预约触发", "已按预约时间自动发起算力申请", false)
		go s.StartAccountSession(id)
	}
	if needsRepaint {
		s.notifyUI()
	}
}
