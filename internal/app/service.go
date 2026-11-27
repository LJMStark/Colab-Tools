package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"slices"
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

const AppVersion = "0.3.20"

var errSSHTunnelStarting = errors.New("本机加密通道正在建立中，请稍候")

// tunnelStderrBuffer 线程安全地记录 SSH 隧道进程最后 4KB 的 stderr 输出，用于诊断 HTTP 429 / 连接故障。
type tunnelStderrBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (b *tunnelStderrBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	const maxBytes = 4096
	if len(p) >= maxBytes {
		b.buf = append(b.buf[:0], p[len(p)-maxBytes:]...)
		return len(p), nil
	}
	if len(b.buf)+len(p) > maxBytes {
		drop := len(b.buf) + len(p) - maxBytes
		b.buf = append([]byte(nil), b.buf[drop:]...)
	}
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (b *tunnelStderrBuffer) String() string {
	if b == nil {
		return ""
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.TrimSpace(runner.StripCR(string(b.buf)))
}

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
	token    string
	endpoint string
	cancel   context.CancelFunc
}

// ViewStateSnapshot provides a lock-safe snapshot of Service UI state for concurrent rendering.
type ViewStateSnapshot struct {
	SelectedSection        MainSection
	ShowingOnboarding      bool
	OnboardingStep         int
	ThemePreference        string
	StatusBarMode          core.StatusBarDisplayMode
	EnableNotifications    bool
	CLIInstalled           bool
	IsCheckingEnv          bool
	IsInstallingCLI        bool
	IsCancellingCLIInstall bool
	IsImportingDefault     bool
	CLIInstallLogs         []string
	CLIInstallError        string
	ToastMessage           string
	EnvReport              doctor.Report
	ClientDetection        integrations.DetectionSnapshot
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

	refreshing             map[string]bool
	stoppingSessions       map[string]bool
	sshStarting            map[string]bool
	lastManualFetch        time.Time
	sshTunnels             map[string]*exec.Cmd
	sshADCPaths            map[string]string
	sshStderrs             map[string]*tunnelStderrBuffer
	deployFlowCancels      map[string]deployWatchHandle
	deployWatchCancels     map[string]deployWatchHandle
	pendingOAuth           *colab.OAuthSessionTicket
	actionPrompt           *core.UserActionPrompt
	pendingQuitCallback    func()
	driveAuthResultCh      chan bool
	comfyTunnelWaitTimeout time.Duration
	comfyRetryPause        time.Duration
	sshStarter             func(r *runner.OSRunner, profile core.AccountProfile, localPort, remotePort int, adcPath string) (*exec.Cmd, *tunnelStderrBuffer, error)
	terminalOpener         func(shellCmd string) error

	OSRunner *runner.OSRunner
	Engine   *colab.Engine
	Vault    *vault.Vault

	persistChanges   bool
	configPath       string
	insightsPath     string
	activeLedgerPath string
	ledgerMu         sync.Mutex
	pendingLedger    map[string]core.ActiveSessionLedgerEntry
	stopLoops        chan struct{}
	stopOnce         sync.Once

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
		stoppingSessions:   map[string]bool{},
		sshStarting:        map[string]bool{},
		sshTunnels:         map[string]*exec.Cmd{},
		sshADCPaths:        map[string]string{},
		sshStderrs:         map[string]*tunnelStderrBuffer{},
		deployFlowCancels:  map[string]deployWatchHandle{},
		deployWatchCancels: map[string]deployWatchHandle{},
		sshStarter:         startSSHTunnelProcess,
		terminalOpener:     openTerminalCommand,
		OSRunner:           r,
		Engine:             colab.NewEngine(r, v),
		Vault:              v,
		persistChanges:     !skipBootstrap,
		configPath:         core.DefaultConfigPath(),
		insightsPath:       core.DefaultInsightsPath(),
		activeLedgerPath:   core.ActiveLedgerFilePath(),
		pendingLedger:      map[string]core.ActiveSessionLedgerEntry{},
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
		s.pendingLedger = core.LoadActiveLedger(s.activeLedgerPath)
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

	// Periodic 60s active session ledger persistence (outside Service mutex)
	go func() {
		tk := time.NewTicker(60 * time.Second)
		defer tk.Stop()
		for {
			select {
			case <-s.stopLoops:
				return
			case <-tk.C:
				s.PersistActiveLedger()
			}
		}
	}()
}

// PersistActiveLedger snapshots all currently running/stopping sessions and saves them atomically under ledgerMu.
func (s *Service) PersistActiveLedger() {
	s.ledgerMu.Lock()
	defer s.ledgerMu.Unlock()

	s.mu.RLock()
	if !s.persistChanges || s.activeLedgerPath == "" {
		s.mu.RUnlock()
		return
	}
	path := s.activeLedgerPath
	entries := make(map[string]core.ActiveSessionLedgerEntry, len(s.pendingLedger)+len(s.Settings.Accounts))
	for k, v := range s.pendingLedger {
		entries[k] = v
	}
	now := time.Now()
	for _, acc := range s.Settings.Accounts {
		rt := s.Runtime[acc.ID]
		if rt.ActiveEndpoint == "" || (!rt.Phase.IsRunning() && rt.Phase.Kind != core.PhaseStopping) {
			continue
		}
		hw := core.NormalizeHardwareKey(rt.ActiveHardware)
		if hw == "" {
			hw = core.NormalizeHardwareKey(rt.GPUName)
		}
		if hw == "" {
			hw = strings.TrimSpace(rt.ActiveHardware)
		}
		startedAt := rt.SessionStartedAt
		if startedAt.IsZero() {
			startedAt = now.Add(-time.Duration(max(0, rt.SessionUptimeSec)) * time.Second)
		}
		lastSample := rt.SessionLastSampleAt
		if lastSample.IsZero() {
			lastSample = now
		}
		estCU := rt.SessionEstimatedCU
		if now.After(lastSample) {
			rate := rt.SessionMeasuredSingleRate
			if rate <= 0 {
				rate = core.EstimatedHourlyCU(hw)
			}
			estCU += rate * now.Sub(lastSample).Seconds() / 3600.0
		}
		entries[acc.ID] = core.ActiveSessionLedgerEntry{
			AccountID:          acc.ID,
			AccountName:        acc.DisplayName,
			AccountColor:       acc.ColorTag,
			SessionName:        acc.SessionName,
			Endpoint:           rt.ActiveEndpoint,
			Hardware:           hw,
			HighMem:            core.EffectiveUseHighMem(acc) && !rt.SessionUsedStandardRAM,
			RecipeLabel:        recipeLabelForSession(acc, rt.SessionModelDowngraded),
			StartedAt:          startedAt,
			StartBalance:       rt.SessionStartBalance,
			StartBalanceValid:  rt.SessionStartBalanceValid,
			Adopted:            rt.SessionAdopted,
			MaxAssignments:     rt.SessionMaxAssignments,
			MeasuredSingleRate: rt.SessionMeasuredSingleRate,
			EstimatedCU:        estCU,
			LastSampleAt:       now,
			LastUptimeSec:      rt.SessionUptimeSec,
			FallbackUsed:       rt.SessionFallbackUsed,
			ModelDowngraded:    rt.SessionModelDowngraded,
			UsedStandardRAM:    rt.SessionUsedStandardRAM,
		}
	}
	s.mu.RUnlock()
	_ = core.SaveActiveLedger(path, entries)
}

func (s *Service) Shutdown() {
	s.PersistActiveLedger()
	s.stopOnce.Do(func() {
		close(s.stopLoops)
	})
	s.mu.Lock()
	for _, h := range s.queueCancels {
		if h.cancel != nil {
			h.cancel()
		}
	}
	for id, fh := range s.deployFlowCancels {
		if fh.cancel != nil {
			fh.cancel()
		}
		delete(s.deployFlowCancels, id)
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
	StatusBarMode   string
	RunningCount    int
	TotalBalance    float64
	Queuing         bool
	QueueAttempt    int
	QueueMax        int
	QueuePersistent bool
	QueueMaxHours   int
	TargetsSummary  string
	Deploying       bool
	Starting        bool
	HardwareLabel   string
	FocusHasError   bool
	FocusErrorText  string
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
			st.QueuePersistent = rt.Phase.QueuePersistent
			st.QueueMaxHours = rt.Phase.QueueMaxHours
			if rt.Phase.QueuePersistent {
				st.QueueMax = rt.Phase.QueueMax
				if st.QueueMaxHours <= 0 {
					st.QueueMaxHours = core.EffectivePersistentHours(acc)
				}
			} else {
				st.QueueMax = max(1, rt.Phase.QueueMax)
			}
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

func (s *Service) ViewState() ViewStateSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return ViewStateSnapshot{
		SelectedSection:        s.SelectedSection,
		ShowingOnboarding:      s.ShowingOnboarding,
		OnboardingStep:         s.OnboardingStep,
		ThemePreference:        s.Settings.ThemePreference,
		StatusBarMode:          s.Settings.StatusBarMode,
		EnableNotifications:    s.Settings.EnableNotifications,
		CLIInstalled:           s.CLIInstalled,
		IsCheckingEnv:          s.IsCheckingEnv,
		IsInstallingCLI:        s.IsInstallingCLI,
		IsCancellingCLIInstall: s.IsCancellingCLIInstall,
		IsImportingDefault:     s.IsImportingDefault,
		CLIInstallLogs:         slices.Clone(s.CLIInstallLogs),
		CLIInstallError:        s.CLIInstallError,
		ToastMessage:           s.ToastMessage,
		EnvReport:              s.EnvReport,
		ClientDetection:        s.ClientDetection,
	}
}

func (s *Service) SetShowingOnboarding(show bool) {
	s.mu.Lock()
	s.ShowingOnboarding = show
	s.mu.Unlock()
	s.notifyUI()
}

func (s *Service) SetOnboardingStep(step int) {
	s.mu.Lock()
	s.OnboardingStep = step
	s.mu.Unlock()
	s.notifyUI()
}

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
	// 系统托管以下底层技术参数为最优默认，避免冗余配置与用户误关
	updated.AutoOpenComfyUI = true
	updated.EnablePoliteQueue = true
	updated.EnableHardwareIdle = true
	updated.H3EnableSolAttn = true
	if updated.UseGoogleDrive {
		updated.CacheBaseToDrive = true
	}
	if updated.QueuePersistentMode {
		updated.QueuePersistentMaxHours = core.QueuePersistentDefaultHours
		if !updated.AutoShutdownEnabled {
			updated.AutoShutdownEnabled = true
		}
	}
	// 统一空闲关机单一字段，防止旧配置中 IdleTimeoutMinutes 与 QueueDrainMinutes 打架
	updated.QueueDrainMinutes = updated.IdleTimeoutMinutes
	if !updated.EnableLLMModule && updated.DeployRecipe.IsVideoComfy() {
		updated.StopAfterQueueDrained = true
	}
	s.mu.Lock()
	for i := range s.Settings.Accounts {
		if s.Settings.Accounts[i].ID == updated.ID {
			s.Settings.Accounts[i] = updated
			break
		}
	}
	if rt, ok := s.Runtime[updated.ID]; ok && rt.Phase.IsQueuing() {
		rt.Phase.QueuePersistent = updated.QueuePersistentMode
		rt.Phase.QueueMaxHours = core.EffectivePersistentHours(updated)
		if updated.QueuePersistentMode {
			rt.Phase.QueueMax = 0
		} else if rt.Phase.QueueMax <= 0 {
			rt.Phase.QueueMax = core.QueueMaxAttempts
		}
		s.Runtime[updated.ID] = rt
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
	s.cancelDeployFlow(accountID)
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
	delete(s.pendingLedger, accountID)
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
	s.PersistActiveLedger()
	s.ShowToast("已移除账号并清理对应系统凭据")
}

// managedSessionsForAccountLocked returns deduplicated managed (non-orphan) session names for an account.
func managedSessionsForAccountLocked(acc core.AccountProfile, rt core.AccountRuntimeState) []string {
	seen := map[string]bool{}
	var names []string
	if rt.Phase.IsRunning() || rt.Phase.Kind == core.PhaseStopping {
		seen[acc.SessionName] = true
		names = append(names, acc.SessionName)
	}
	for _, sess := range rt.Sessions {
		if !sess.IsOrphan && !seen[sess.Name] {
			seen[sess.Name] = true
			names = append(names, sess.Name)
		}
	}
	return names
}

func (s *Service) TotalManagedRunningSessionCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	total := 0
	for _, acc := range s.Settings.Accounts {
		total += len(managedSessionsForAccountLocked(acc, s.Runtime[acc.ID]))
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
		rt.WatchdogDeferredForTempOutputs = false
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
	var ledgerBackup *core.ActiveSessionLedgerEntry
	if prev, ok := s.pendingLedger[accountID]; ok {
		cp := prev
		ledgerBackup = &cp
	}
	primaryEndpoint, isRunning, closedRec := s.applyServerSnapshotLocked(accountID, acc, usage, sessions, now, settleBusy, "")
	s.mu.Unlock()
	if closedRec != nil {
		if !s.recordHistory(*closedRec) && ledgerBackup != nil && closedRec.Endpoint == ledgerBackup.Endpoint {
			// Restore pending ledger entry so failed history writes (e.g. corrupt read-only) do not drop billing data
			s.mu.Lock()
			if _, exists := s.pendingLedger[accountID]; !exists {
				s.pendingLedger[accountID] = *ledgerBackup
			}
			s.mu.Unlock()
		}
		if closedRec.ShutdownReason == core.ShutdownExternalOrLost || closedRec.ShutdownReason == core.ShutdownCapReached {
			hwLbl := strings.TrimSpace(closedRec.Hardware)
			if hwLbl == "" {
				hwLbl = "云端机器"
			}
			s.SendNotification("云端机器已被 Google 回收停机", fmt.Sprintf("%s 的 %s 已停止运行（非本软件关机），可在「记录」查看详情", acc.DisplayName, hwLbl), true)
		}
	}
	s.PersistActiveLedger()
	s.notifyUI()

	if isRunning {
		s.fetchAndApplyTelemetry(acc, primaryEndpoint)
	}
	return true
}

func (s *Service) recordHistory(rec core.HistoryRecord) bool {
	if s.History != nil {
		if err := s.History.AddRecord(rec); err != nil {
			s.ShowToast("保存账单记录失败：" + err.Error())
			return false
		}
	}
	return true
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

func deterministicSessionRecordID(accountID, endpoint string, startedAt time.Time) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%d", accountID, endpoint, startedAt.UTC().Unix())))
	return "sess-" + hex.EncodeToString(sum[:8])
}

func computeSessionBilling(
	tier core.SubscriptionTier,
	hw string,
	durSec int,
	estCU float64,
	measuredRate float64,
	startBalance float64,
	startBalanceValid bool,
	adopted bool,
	maxAssignments int,
	allowMeasured bool,
	usage *colab.UsageSnapshot,
) (core.BillingBasis, float64) {
	if tier == core.TierFree {
		return core.BillingFree, 0
	}
	est := estCU
	if est <= 0 && durSec > 0 {
		rate := measuredRate
		if rate <= 0 {
			rate = core.EstimatedHourlyCU(hw)
		}
		est = rate * float64(durSec) / 3600.0
	}
	if allowMeasured && usage != nil && maxAssignments <= 1 && !adopted && startBalanceValid {
		delta := startBalance - usage.Balance
		if delta > 0 && (est <= 0.05 || (delta >= est*0.3 && delta <= est*2.5)) {
			return core.BillingMeasured, delta
		}
	}
	return core.BillingEstimated, est
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

	basis, consumed := computeSessionBilling(
		acc.Tier, hw, durSec,
		rt.SessionEstimatedCU, rt.SessionMeasuredSingleRate,
		rt.SessionStartBalance, rt.SessionStartBalanceValid,
		rt.SessionAdopted, rt.SessionMaxAssignments,
		true, usage,
	)

	endedAt := now
	rec := core.HistoryRecord{
		ID:              deterministicSessionRecordID(acc.ID, prevEndpoint, startedAt),
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

func buildClosedRecordFromLedger(
	entry core.ActiveSessionLedgerEntry,
	acc core.AccountProfile,
	usage *colab.UsageSnapshot,
	now time.Time,
) core.HistoryRecord {
	endedAt := entry.LastSampleAt
	if endedAt.IsZero() || endedAt.Before(entry.StartedAt) {
		endedAt = now
	}
	startedAt := entry.StartedAt
	if startedAt.IsZero() {
		startedAt = endedAt.Add(-time.Duration(max(0, entry.LastUptimeSec)) * time.Second)
	}
	durSec := int(endedAt.Sub(startedAt).Seconds())
	if entry.LastUptimeSec > durSec {
		durSec = entry.LastUptimeSec
	}
	if durSec < 0 {
		durSec = 0
	}
	// Session ended during unobserved app downtime: never attribute post-restart balance delta as BillingMeasured
	basis, consumed := computeSessionBilling(
		acc.Tier, entry.Hardware, durSec,
		entry.EstimatedCU, entry.MeasuredSingleRate,
		entry.StartBalance, entry.StartBalanceValid,
		entry.Adopted, entry.MaxAssignments,
		false, usage,
	)
	reason := core.ShutdownExternalOrLost
	if durSec >= acc.Tier.MaxSessionHours()*3600-300 {
		reason = core.ShutdownCapReached
	}
	return core.HistoryRecord{
		ID:              deterministicSessionRecordID(acc.ID, entry.Endpoint, startedAt),
		Kind:            core.HistoryKindSession,
		AccountID:       acc.ID,
		AccountName:     acc.DisplayName,
		AccountColor:    acc.ColorTag,
		SessionName:     entry.SessionName,
		Endpoint:        entry.Endpoint,
		StartedAt:       startedAt,
		EndedAt:         &endedAt,
		DurationSec:     durSec,
		Hardware:        entry.Hardware,
		HighMem:         entry.HighMem,
		RecipeLabel:     entry.RecipeLabel,
		ConsumedCU:      consumed,
		BillingBasis:    basis,
		ShutdownReason:  reason,
		FallbackUsed:    entry.FallbackUsed,
		ModelDowngraded: entry.ModelDowngraded,
		Detail:          "重启后由活跃账本自动补记",
	}
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
			ledgerEntry, hasLedger := s.pendingLedger[accountID]
			if hasLedger {
				delete(s.pendingLedger, accountID)
			}
			if hasLedger && ledgerEntry.Endpoint == primary.Endpoint {
				// Seamlessly resume billing & uptime after app restart/crash
				rt.SessionStartedAt = ledgerEntry.StartedAt
				if rt.SessionStartedAt.IsZero() {
					rt.SessionStartedAt = now.Add(-time.Duration(max(0, ledgerEntry.LastUptimeSec)) * time.Second)
				}
				rt.SessionUptimeSec = max(ledgerEntry.LastUptimeSec, int(now.Sub(rt.SessionStartedAt).Seconds()))
				rt.IdleSeconds = 0
				rt.WarnedForCurrentIdle = false
				rt.SessionStartBalance = ledgerEntry.StartBalance
				rt.SessionStartBalanceValid = ledgerEntry.StartBalanceValid
				rt.SessionAdopted = ledgerEntry.Adopted
				rt.SessionMaxAssignments = ledgerEntry.MaxAssignments
				rt.SessionMeasuredSingleRate = ledgerEntry.MeasuredSingleRate
				rt.SessionEstimatedCU = ledgerEntry.EstimatedCU
				rt.SessionFallbackUsed = ledgerEntry.FallbackUsed
				rt.SessionModelDowngraded = ledgerEntry.ModelDowngraded
				rt.SessionUsedStandardRAM = ledgerEntry.UsedStandardRAM
				if !ledgerEntry.LastSampleAt.IsZero() && now.After(ledgerEntry.LastSampleAt) {
					rate := rt.SessionMeasuredSingleRate
					if rate <= 0 {
						rate = core.EstimatedHourlyCU(primary.Hardware)
					}
					rt.SessionEstimatedCU += rate * now.Sub(ledgerEntry.LastSampleAt).Seconds() / 3600.0
				}
				rt.SessionLastSampleAt = now
				if usage != nil {
					if usage.ActiveAssignments > rt.SessionMaxAssignments {
						rt.SessionMaxAssignments = usage.ActiveAssignments
					}
					if usage.ActiveAssignments <= 1 && usage.RatePerHour > 0 {
						rt.SessionMeasuredSingleRate = usage.RatePerHour
					}
				}
			} else {
				if hasLedger && closedRec == nil {
					rec := buildClosedRecordFromLedger(ledgerEntry, acc, usage, now)
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
			}

			// Endpoint changed -> invalidate bound LLM URL & video deploy/auto-open state
			s.cancelDeployFlowLocked(accountID)
			rt.PublicURL = ""
			rt.LLMURLBoundEndpoint = ""
			rt.DisplayUIURL = ""
			rt.ServerHealthy = false
			rt.DeployRunning = false
			rt.DeployPreLaunch = false
			rt.DeployFailed = false
			rt.DeployError = ""
			rt.DeployStartingUntil = time.Time{}
			rt.DeployNotifiedFailEndpoint = ""
			rt.DeployLogTail = ""
			rt.DeployLogLoading = false
			rt.RenderedVideoCount = 0
			rt.SawQueueBusyThisRun = false
			rt.AutoOpenedEndpoint = ""
			rt.DriveMountedKnown = false
			rt.DriveActuallyMounted = false
			rt.EffectiveOutputDir = ""
			rt.TempOutputVideos = 0
			rt.TempOutputScanKnown = false
			rt.WatchdogDeferredForTempOutputs = false
			rt.CloudflaredActive = false
		}
		if settleBusy || !rt.Phase.IsBusy() {
			rt.Phase = core.InstancePhase{Kind: core.PhaseRunning}
		}
		rt.LastUnexpectedStopMsg = ""
		rt.QueueStartedAt = time.Time{}
		rt.ActiveEndpoint = primary.Endpoint
		rt.ActiveHardware = primary.Hardware
		s.Runtime[accountID] = rt
		return primary.Endpoint, true, closedRec
	}

	if prevEndpoint != "" {
		delete(s.pendingLedger, accountID)
		rec := buildClosedSessionRecordLocked(acc, &rt, prevEndpoint, prevHardware, prevGPUName, prevUptime, usage, shutdownHint, now)
		closedRec = &rec
		if shutdownHint == "" {
			hwLbl := strings.TrimSpace(rec.Hardware)
			if hwLbl == "" {
				hwLbl = "云端显卡"
			}
			if rec.ShutdownReason == core.ShutdownCapReached {
				rt.LastUnexpectedStopMsg = fmt.Sprintf("刚才的 %s 在 %s 达到单次运行时长上限自动停机，可在「记录」查看账单", hwLbl, now.Format("15:04"))
			} else {
				rt.LastUnexpectedStopMsg = fmt.Sprintf("刚才的 %s 在 %s 被 Google 服务端回收停机（非本软件关机），点右侧可立即重开", hwLbl, now.Format("15:04"))
			}
		} else {
			rt.LastUnexpectedStopMsg = ""
		}
	} else if ledgerEntry, hasLedger := s.pendingLedger[accountID]; hasLedger {
		delete(s.pendingLedger, accountID)
		rec := buildClosedRecordFromLedger(ledgerEntry, acc, usage, now)
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
	rt.RAMUsedMB = 0
	rt.RAMTotalMB = 0
	rt.GPUUtilPercent = 0
	rt.SessionUptimeSec = 0
	s.cancelDeployFlowLocked(accountID)
	rt.PublicURL = ""
	rt.LLMURLBoundEndpoint = ""
	rt.DisplayUIURL = ""
	rt.ServerHealthy = false
	rt.DeployRunning = false
	rt.DeployPreLaunch = false
	rt.DeployFailed = false
	rt.DeployError = ""
	rt.DeployStartingUntil = time.Time{}
	rt.DeployNotifiedFailEndpoint = ""
	rt.DeployLogTail = ""
	rt.DeployLogLoading = false
	rt.RenderedVideoCount = 0
	rt.SawQueueBusyThisRun = false
	rt.AutoOpenedEndpoint = ""
	rt.DriveMountedKnown = false
	rt.DriveActuallyMounted = false
	rt.EffectiveOutputDir = ""
	rt.TempOutputVideos = 0
	rt.TempOutputScanKnown = false
	rt.WatchdogDeferredForTempOutputs = false
	rt.CloudflaredActive = false
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
	if tel.RAMTotalMB > 0 {
		rt.RAMUsedMB = tel.RAMUsedMB
		rt.RAMTotalMB = tel.RAMTotalMB
	}
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

	if rt.DeployPreLaunch {
		// Keep local pre-launch (Drive mount / script upload) alive even if a previous ComfyUI is still healthy on the VM.
		rt.DeployRunning = true
	} else if tel.DeployAlive {
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
	if tel.TempOutputVideos != nil {
		rt.TempOutputScanKnown = true
		rt.TempOutputVideos = *tel.TempOutputVideos
		if *tel.TempOutputVideos == 0 {
			rt.WatchdogDeferredForTempOutputs = false
		}
	}
	if tel.DriveMounted != nil {
		rt.DriveMountedKnown = true
		rt.DriveActuallyMounted = *tel.DriveMounted
	}
	if tel.EffectiveOutputDir != "" {
		rt.EffectiveOutputDir = tel.EffectiveOutputDir
	}
	rt.CloudflaredActive = tel.CloudflaredActive
	if !tel.CloudflaredActive && strings.Contains(rt.DisplayUIURL, "trycloudflare.com") {
		rt.DisplayUIURL = ""
	}

	// Evaluate non-LLM signals (active deploy -> status.json with explicit busy -> ComfyUI /queue -> dual low-load fallback)
	statusFresh := tel.StatusPayload != nil && tel.StatusAgeSeconds != nil && *tel.StatusAgeSeconds <= core.StatusFileMaxAgeSec
	shouldNotifyDeployFail := false
	deployFailDetail := ""
	if !rt.DeployPreLaunch && deployFailed {
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
	} else if !rt.DeployPreLaunch && statusFresh {
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
		!rt.DeployPreLaunch &&
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
	accIdx := -1
	for i, a := range s.Settings.Accounts {
		if a.ID == accountID {
			acc = a
			accIdx = i
			break
		}
	}
	if accIdx < 0 || !s.CLIInstalled {
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
	// 开启持续守候时自动补开空闲自动关机，防止半夜抢到后无人看管烧光点数
	if acc.QueuePersistentMode && !acc.AutoShutdownEnabled {
		acc.AutoShutdownEnabled = true
		if acc.IdleTimeoutMinutes < 5 {
			acc.IdleTimeoutMinutes = 40
		}
		s.Settings.Accounts[accIdx] = acc
		s.saveSettingsLocked()
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
		maxHours := core.EffectivePersistentHours(acc)
		queueMax := core.QueueMaxAttempts
		if acc.QueuePersistentMode {
			queueMax = 0
		}
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
			Kind:            core.PhaseQueuing,
			QueueAttempt:    1,
			QueueMax:        queueMax,
			QueuePersistent: acc.QueuePersistentMode,
			QueueMaxHours:   maxHours,
			TargetsSummary:  targetsSummary,
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

	for attempt := 1; ; attempt++ {
		if latest, ok := s.accountByID(acc.ID); ok {
			acc = latest
		}
		maxHours := core.EffectivePersistentHours(acc)
		if !acc.QueuePersistentMode && attempt > core.QueueMaxAttempts {
			return
		}
		deadline := startedAt.Add(core.QueueMaxTotalWait)
		queueMax := core.QueueMaxAttempts
		if acc.QueuePersistentMode {
			deadline = startedAt.Add(time.Duration(maxHours) * time.Hour)
			queueMax = 0
		}

		if ctx.Err() != nil {
			logQueueOnce("", false, false, core.AllocTagCancelled, "排队已取消")
			return
		}
		// Pre-flight check: verify session not already running on server
		if preUsage, sessions, err := s.Engine.FetchUsageAndSessions(ctx, acc); err == nil {
			for _, sess := range sessions {
				if !sess.IsOrphan && sess.Name == acc.SessionName {
					s.mu.RLock()
					stillOwned := s.isQueueTokenOwnerLocked(acc.ID, token)
					s.mu.RUnlock()
					if !stillOwned || ctx.Err() != nil {
						go s.refreshAccount(acc.ID, true, true, true)
						return
					}
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
		if cur.QueueStartedAt.IsZero() {
			cur.QueueStartedAt = startedAt
		}
		cur.LastUnexpectedStopMsg = ""
		cur.QueueInFlight = true
		cur.Phase = core.InstancePhase{
			Kind:            core.PhaseQueuing,
			QueueAttempt:    attempt,
			QueueMax:        queueMax,
			QueuePersistent: acc.QueuePersistentMode,
			QueueMaxHours:   maxHours,
			TargetsSummary:  lastTargetsSummary,
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
			if !owned || ctx.Err() != nil {
				// Queue was cancelled while `colab new` was in flight: sync live instance so billing is visible, but skip auto-deploy.
				_ = s.refreshAccount(acc.ID, true, true, true)
				hwLbl := strings.TrimSpace(res.Outcome.Hardware)
				if hwLbl == "" {
					hwLbl = "云端显卡"
				}
				warn := fmt.Sprintf("取消排队时云端恰好已完成 %s 分配，如无需使用请点击关机止损", hwLbl)
				s.ShowToast(warn)
				s.SendNotification("排队已取消，但实例已分配", warn, true)
				return
			}
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
		// 遇 429 / abusive / 401 / 403 / 真多机 412 立即熔断停手，绝不顶风继续
		if res.Outcome.Kind != core.OutcomeCapacity503 {
			logQueueOnce(res.Outcome.Hardware, false, false, mapAllocOutcomeTag(res.Outcome), res.Outcome.Message)
			s.handleProvisionFailure(acc, res.Outcome)
			return
		}

		// 若用户在排队期间点击了「切持续守候」，这里重新同步最新账户配置
		if latest, ok := s.accountByID(acc.ID); ok {
			acc = latest
			maxHours = core.EffectivePersistentHours(acc)
			if acc.QueuePersistentMode {
				queueMax = 0
			} else {
				queueMax = core.QueueMaxAttempts
			}
		}

		delay, ok := core.NextQueueDelayForMode(attempt, startedAt, time.Now(), rand.Float64(), acc.QueuePersistentMode, maxHours)
		if !ok {
			msg := "已达 5 轮 / 40 分钟合规排队上限，暂未拿到目标卡"
			if acc.QueuePersistentMode {
				msg = fmt.Sprintf("已达 %d 小时持续守候上限（共试 %d 轮），暂未拿到目标卡", maxHours, attempt)
			}
			outcome := core.ProvisionOutcome{Kind: core.OutcomeBudgetExhausted, Message: msg}
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
			Kind:            core.PhaseQueuing,
			QueueAttempt:    attempt,
			QueueMax:        queueMax,
			QueuePersistent: acc.QueuePersistentMode,
			QueueMaxHours:   maxHours,
			NextRetryAt:     &nextAt,
			TargetsSummary:  lastTargetsSummary,
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
func (s *Service) CurrentActionPrompt() *core.UserActionPrompt {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.actionPrompt == nil {
		return nil
	}
	cp := *s.actionPrompt
	return &cp
}

// PresentActionPrompt 展示一个用户行动弹窗并触发界面重绘；若替换正在等待的 Drive 授权弹窗会安全解除旧通道阻塞。
func (s *Service) PresentActionPrompt(prompt *core.UserActionPrompt) {
	s.mu.Lock()
	var oldDriveCh chan bool
	if s.actionPrompt != nil && s.actionPrompt.Kind == core.PromptDriveAuth && s.driveAuthResultCh != nil {
		if prompt == nil || prompt.Kind != core.PromptDriveAuth || prompt.ID != s.actionPrompt.ID {
			oldDriveCh = s.driveAuthResultCh
			s.driveAuthResultCh = nil
		}
	}
	if prompt == nil {
		s.actionPrompt = nil
	} else {
		cp := *prompt
		s.actionPrompt = &cp
	}
	s.mu.Unlock()
	if oldDriveCh != nil {
		select {
		case oldDriveCh <- false:
		default:
		}
	}
	s.notifyUI()
}

// ResolveActionPrompt 处理用户在行动弹窗上的点击操作（open_url / primary / secondary / cancel）。
func (s *Service) ResolveActionPrompt(action string) {
	s.ResolveActionPromptByID("", action)
}

// ResolveActionPromptByID 按弹窗 ID 精确匹配处理点击操作，防止弹窗被并发替换后误触发新弹窗按钮。
func (s *Service) ResolveActionPromptByID(promptID, action string) {
	s.mu.Lock()
	prompt := s.actionPrompt
	if prompt == nil {
		s.mu.Unlock()
		return
	}
	if promptID != "" && prompt.ID != promptID {
		s.mu.Unlock()
		return
	}
	promptCopy := *prompt
	opener := s.URLOpener

	switch promptCopy.Kind {
	case core.PromptDriveAuth:
		if action == "open_url" {
			s.mu.Unlock()
			if promptCopy.URL != "" && opener != nil {
				opener(promptCopy.URL)
			}
			return
		}
		ch := s.driveAuthResultCh
		s.actionPrompt = nil
		s.driveAuthResultCh = nil
		s.mu.Unlock()
		s.notifyUI()
		if ch != nil {
			proceed := action == "primary"
			select {
			case ch <- proceed:
			default:
			}
		}
	case core.PromptComfyHelp:
		if action == "open_url" {
			s.mu.Unlock()
			if promptCopy.URL != "" && opener != nil {
				opener(promptCopy.URL)
			} else if promptCopy.AccountID != "" {
				s.OpenColabInBrowser(promptCopy.AccountID)
			}
			return
		}
		s.actionPrompt = nil
		s.mu.Unlock()
		s.notifyUI()
		switch action {
		case "primary":
			if promptCopy.AccountID != "" {
				if promptCopy.PrimaryAction == "open_colab" {
					if promptCopy.URL != "" && opener != nil {
						opener(promptCopy.URL)
					} else {
						s.OpenColabInBrowser(promptCopy.AccountID)
					}
				} else {
					go s.OpenComfyUI(promptCopy.AccountID)
				}
			}
		case "secondary":
			if promptCopy.AccountID != "" {
				if promptCopy.SecondaryAction == "enable_cloudflared" {
					go s.OpenComfyUIViaCloudflared(promptCopy.AccountID)
				} else if acc, ok := s.accountByID(promptCopy.AccountID); ok {
					go s.launchVideoDeployFlow(acc)
				}
			}
		}
	case core.PromptConfirmStop:
		expectedEP := promptCopy.ExpectedEndpoint
		curEP := ""
		if rt, ok := s.Runtime[promptCopy.AccountID]; ok {
			curEP = rt.ActiveEndpoint
		}
		s.actionPrompt = nil
		s.mu.Unlock()
		s.notifyUI()
		if action == "primary" && promptCopy.AccountID != "" {
			if promptCopy.SessionName == "" && expectedEP != "" && curEP != "" && curEP != expectedEP {
				s.ShowToast("云端机器已切换为新实例，已取消本次过期停机确认")
				return
			}
			go s.StopAccountSession(promptCopy.AccountID, promptCopy.SessionName, "")
		}
	case core.PromptConfirmStopAll:
		s.actionPrompt = nil
		s.mu.Unlock()
		s.notifyUI()
		if action == "primary" {
			go s.StopAllManagedSessions()
		}
	case core.PromptConfirmRemoveAccount:
		s.actionPrompt = nil
		s.mu.Unlock()
		s.notifyUI()
		if promptCopy.AccountID != "" {
			switch action {
			case "primary":
				go func(accID string) {
					s.CancelQueue(accID)
					if !s.StopAccountManagedSessions(accID) {
						s.ShowToast("未能确认该账号云端机器已完全停机，为防计费失控已保留账号配置，请先在 Colab 网页确认")
						return
					}
					s.RemoveAccount(accID)
				}(promptCopy.AccountID)
			case "secondary":
				s.RemoveAccount(promptCopy.AccountID)
			}
		}
	case core.PromptConfirmQuit:
		quitFn := s.pendingQuitCallback
		s.actionPrompt = nil
		s.pendingQuitCallback = nil
		s.mu.Unlock()
		s.notifyUI()
		switch action {
		case "primary":
			go func() {
				if !s.StopAllManagedSessions() {
					s.ShowToast("仍有云端机器未能确认停机，已暂缓退出以保持监控")
					return
				}
				if quitFn != nil {
					quitFn()
				}
			}()
		case "secondary":
			if quitFn != nil {
				quitFn()
			}
		}
	case core.PromptTempOutputGuard:
		s.actionPrompt = nil
		s.mu.Unlock()
		s.notifyUI()
		if promptCopy.AccountID != "" {
			switch action {
			case "primary":
				s.ResetIdleTimer(promptCopy.AccountID)
				go s.OpenComfyUI(promptCopy.AccountID)
			case "secondary":
				go s.StopAccountSession(promptCopy.AccountID, "", "用户确认跳过临时盘护片关机")
			}
		}
	default:
		s.actionPrompt = nil
		s.mu.Unlock()
		s.notifyUI()
	}
}

// RequestConfirmStop 弹出防误触二次确认弹窗，仅在目标账号主会话处于运行态时触发。
func (s *Service) RequestConfirmStop(accountID string) {
	s.RequestConfirmStopSession(accountID, "")
}

// RequestConfirmStopSession 弹出防误触二次确认弹窗，支持主会话或附加非孤儿会话。
func (s *Service) RequestConfirmStopSession(accountID, specificSession string) {
	acc, ok := s.accountByID(accountID)
	if !ok {
		return
	}
	rt := s.StateFor(accountID)
	target := strings.TrimSpace(specificSession)
	if target == "" || target == acc.SessionName {
		if !rt.Phase.IsRunning() {
			return
		}
		s.PresentActionPrompt(&core.UserActionPrompt{
			ID:               uuid.New().String(),
			Kind:             core.PromptConfirmStop,
			AccountID:        accountID,
			SessionName:      "",
			ExpectedEndpoint: rt.ActiveEndpoint,
			Title:            fmt.Sprintf("确认关闭当前 %s 云端机器？", rt.ShortGPUName()),
			Message:          fmt.Sprintf("账号「%s」本次已运行 %s（当前约 %.2f CU/h）。确认关机后会立即释放云端显卡并停止计费。", acc.DisplayName, core.FormatStopwatch(rt.SessionUptimeSec), rt.CURatePerHour),
			PrimaryLabel:     "确认关机止损",
			CancelLabel:      "继续运行",
		})
		return
	}
	s.PresentActionPrompt(&core.UserActionPrompt{
		ID:           uuid.New().String(),
		Kind:         core.PromptConfirmStop,
		AccountID:    accountID,
		SessionName:  target,
		Title:        fmt.Sprintf("确认关闭附加会话「%s」？", target),
		Message:      fmt.Sprintf("将停止账号「%s」名下的附加云端会话 %s 并释放对应算力。", acc.DisplayName, target),
		PrimaryLabel: "确认停止会话",
		CancelLabel:  "取消",
	})
}

// RequestConfirmStopAll 弹出多账号全部关机止损二次确认。
func (s *Service) RequestConfirmStopAll() {
	n := s.TotalManagedRunningSessionCount()
	if n == 0 {
		if orphans := s.TotalOrphanSessionCount(); orphans > 0 {
			s.ShowToast(fmt.Sprintf("存在 %d 个外部孤儿会话 [?] 无法通过 CLI 关闭，请前往 Colab 网页释放", orphans))
		}
		return
	}
	_, totalRate := s.TotalBalanceAndRate()
	s.PresentActionPrompt(&core.UserActionPrompt{
		ID:           uuid.New().String(),
		Kind:         core.PromptConfirmStopAll,
		Title:        fmt.Sprintf("确认关闭全部 %d 个运行中的云端机器？", n),
		Message:      fmt.Sprintf("当前所有账号合计消耗约 %.2f CU/h。确认后会逐一同步工作流并停止全部受管云端会话。", totalRate),
		PrimaryLabel: "全部关机止损",
		CancelLabel:  "先不关",
	})
}

// RequestConfirmRemoveAccount 移除账号前核验是否有活跃、分配中、排队中或待恢复账本的云端会话，防止失去监控导致计费失控。
func (s *Service) RequestConfirmRemoveAccount(accountID string) {
	s.mu.RLock()
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
		s.mu.RUnlock()
		return
	}
	rt := s.Runtime[accountID]
	managedCount := len(managedSessionsForAccountLocked(acc, rt))
	_, hasPendingLedger := s.pendingLedger[accountID]
	s.mu.RUnlock()

	if managedCount == 0 && !rt.Phase.IsQueuing() && !rt.Phase.IsBusy() && !hasPendingLedger {
		s.RemoveAccount(accountID)
		return
	}
	msg := fmt.Sprintf("账号「%s」当前还有 %d 个运行中的云端会话（或正在启动/排队）。直接移除本地配置会导致失去监控与自动关机保护，建议先停机再移除。", acc.DisplayName, managedCount)
	if managedCount == 0 && hasPendingLedger {
		msg = fmt.Sprintf("账号「%s」上次退出前仍有未核验的云端计费记录，建议先停机核验后再移除，或确认仅移除本地配置。", acc.DisplayName)
	} else if managedCount == 0 {
		msg = fmt.Sprintf("账号「%s」当前正在申请或排队显卡。确认后将取消申请并移除该账号与本地凭据。", acc.DisplayName)
	}
	s.PresentActionPrompt(&core.UserActionPrompt{
		ID:             uuid.New().String(),
		Kind:           core.PromptConfirmRemoveAccount,
		AccountID:      accountID,
		Title:          fmt.Sprintf("账号「%s」尚有活跃任务，如何移除？", acc.DisplayName),
		Message:        msg,
		PrimaryLabel:   "先停机再移除",
		SecondaryLabel: "仅移除本地配置",
		CancelLabel:    "取消",
	})
}

// RequestConfirmQuit 退出 App 前若检测到运行中 GPU，弹窗让用户决定先停机还是保持云端运行。
func (s *Service) RequestConfirmQuit(onConfirmedQuit func()) {
	n := s.TotalManagedRunningSessionCount()
	if n == 0 {
		if onConfirmedQuit != nil {
			onConfirmedQuit()
		}
		return
	}
	_, totalRate := s.TotalBalanceAndRate()
	s.mu.Lock()
	s.pendingQuitCallback = onConfirmedQuit
	s.actionPrompt = &core.UserActionPrompt{
		ID:             uuid.New().String(),
		Kind:           core.PromptConfirmQuit,
		Title:          fmt.Sprintf("还有 %d 台云端显卡正在计费，确定退出？", n),
		Message:        fmt.Sprintf("当前消耗约 %.2f CU/h。退出桌面端后，本地空闲自动关机监控将停止（云端将依赖 Colab 官方闲置超时回收）。", totalRate),
		PrimaryLabel:   "先全部关机再退出",
		SecondaryLabel: "保持云端运行，仅退出",
		CancelLabel:    "留在后台继续监控",
	}
	s.mu.Unlock()
	s.notifyUI()
}

func (s *Service) ClearAccountError(accountID string) {
	s.mu.Lock()
	rt := s.Runtime[accountID]
	rt.LastError = ""
	rt.LastErrorTitle = ""
	rt.LastErrorAction = ""
	rt.LastErrorRaw = ""
	rt.LastAllocHadShortage = false
	rt.LastUnexpectedStopMsg = ""
	s.Runtime[accountID] = rt
	s.mu.Unlock()
	s.notifyUI()
}

func (s *Service) ClearUnexpectedStopNotice(accountID string) {
	s.mu.Lock()
	rt := s.Runtime[accountID]
	rt.LastUnexpectedStopMsg = ""
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
	prevLLM := acc.EnableLLMModule
	acc.DeployRecipe = recipe
	switch recipe {
	case core.RecipeH3Video, core.RecipeComfyUIGeneral:
		acc.WorkloadKind = core.WorkloadVideo
		acc.EnableLLMModule = false
		acc.RemoteServicePort = 8188
		acc.LocalSSHTunnelPort = 8188
		if prevLLM && acc.IdleTimeoutMinutes == 40 {
			acc.IdleTimeoutMinutes = 10
		}
		acc.StopAfterQueueDrained = true
		acc.QueueDrainMinutes = acc.IdleTimeoutMinutes
	case core.RecipeLLMProxy:
		acc.WorkloadKind = core.WorkloadLLM
		acc.EnableLLMModule = true
		acc.RemoteServicePort = 18080
		acc.LocalSSHTunnelPort = 18080
		if acc.IdleTimeoutMinutes <= 15 {
			acc.IdleTimeoutMinutes = 40
		}
		acc.QueueDrainMinutes = acc.IdleTimeoutMinutes
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

func (s *Service) cancelDeployFlowLocked(accountID string) {
	if fh, ok := s.deployFlowCancels[accountID]; ok {
		if fh.cancel != nil {
			fh.cancel()
		}
		delete(s.deployFlowCancels, accountID)
	}
	if s.actionPrompt != nil && s.actionPrompt.Kind == core.PromptDriveAuth && s.actionPrompt.AccountID == accountID {
		s.actionPrompt = nil
		if ch := s.driveAuthResultCh; ch != nil {
			s.driveAuthResultCh = nil
			select {
			case ch <- false:
			default:
			}
		}
	}
}

func (s *Service) cancelDeployFlow(accountID string) {
	s.mu.Lock()
	s.cancelDeployFlowLocked(accountID)
	if cur, ok := s.Runtime[accountID]; ok && cur.DeployPreLaunch {
		cur.DeployPreLaunch = false
		cur.DeployRunning = false
		cur.DeployStartingUntil = time.Time{}
		s.Runtime[accountID] = cur
	}
	s.mu.Unlock()
	s.notifyUI()
}

func (s *Service) clearDeployFlowIfToken(accountID, token string) {
	s.mu.Lock()
	if fh, ok := s.deployFlowCancels[accountID]; ok && fh.token == token {
		if fh.cancel != nil {
			fh.cancel()
		}
		delete(s.deployFlowCancels, accountID)
	}
	s.mu.Unlock()
}

func (s *Service) isDeployFlowOwnerLocked(accountID, token, expectedEndpoint string) bool {
	fh, ok := s.deployFlowCancels[accountID]
	if !ok || fh.token != token {
		return false
	}
	rt, exists := s.Runtime[accountID]
	if !exists || !rt.Phase.IsRunning() {
		return false
	}
	return rt.ActiveEndpoint == expectedEndpoint
}

func (s *Service) cancelDeployWatcher(accountID string) {
	s.cancelDeployFlow(accountID)
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

	// Atomically guard against duplicate concurrent launches & bind full lifecycle to current running endpoint
	s.mu.Lock()
	cur := s.Runtime[acc.ID]
	if !cur.Phase.IsRunning() {
		s.mu.Unlock()
		return
	}
	if cur.DeployRunning {
		s.mu.Unlock()
		s.ShowToast("云端正在自动部署中，可点「查看日志」跟踪实时进度")
		return
	}
	expectedEndpoint := cur.ActiveEndpoint
	flowToken := uuid.New().String()
	flowCtx, cancelFlow := s.serviceContext()
	s.cancelDeployFlowLocked(acc.ID)
	s.deployFlowCancels[acc.ID] = deployWatchHandle{
		token:    flowToken,
		endpoint: expectedEndpoint,
		cancel:   cancelFlow,
	}
	cur.DeployRunning = true
	cur.DeployPreLaunch = true
	cur.DeployFailed = false
	cur.DeployError = ""
	cur.DeployNotifiedFailEndpoint = ""
	cur.DeployStartingUntil = time.Now().Add(120 * time.Second)
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
	defer s.clearDeployFlowIfToken(acc.ID, flowToken)
	s.notifyUI()

	if acc.UseGoogleDrive {
		s.ShowToast("正在挂载 Google Drive（复用模型并自动存成片）…")
		mounted, reason := s.Engine.EnsureDriveMountedInteractive(flowCtx, acc, func(authURL string) bool {
			ch := make(chan bool, 1)
			s.mu.Lock()
			if !s.isDeployFlowOwnerLocked(acc.ID, flowToken, expectedEndpoint) || flowCtx.Err() != nil {
				s.mu.Unlock()
				return false
			}
			if oldCh := s.driveAuthResultCh; oldCh != nil {
				select {
				case oldCh <- false:
				default:
				}
			}
			s.driveAuthResultCh = ch
			s.actionPrompt = &core.UserActionPrompt{
				ID:             uuid.New().String(),
				Kind:           core.PromptDriveAuth,
				AccountID:      acc.ID,
				Title:          "需要你点一下『允许』连接 Google Drive",
				Message:        "已为你打开 Google Drive 授权网页，请在网页里点击「允许」，把云端模型和成片目录挂上。点完后回到这里点击下方绿色按钮继续。",
				URL:            authURL,
				PrimaryLabel:   "我已点完允许，继续",
				SecondaryLabel: "先不挂，纯云端跑",
				CancelLabel:    "取消",
			}
			opener := s.URLOpener
			s.mu.Unlock()
			s.notifyUI()
			if authURL != "" && opener != nil {
				opener(authURL)
			}
			s.SendNotification("需要你点一下「允许」", "请到浏览器点击允许挂载 Google Drive，点完后回来点「我已点完允许，继续」", true)

			select {
			case proceed := <-ch:
				return proceed
			case <-flowCtx.Done():
				return false
			case <-s.stopLoops:
				return false
			}
		})
		s.mu.Lock()
		stillValid := s.isDeployFlowOwnerLocked(acc.ID, flowToken, expectedEndpoint) && flowCtx.Err() == nil
		if !stillValid {
			if st, ok := s.Runtime[acc.ID]; ok && st.ActiveEndpoint == expectedEndpoint {
				st.DeployPreLaunch = false
				st.DeployRunning = false
				st.DeployStartingUntil = time.Time{}
				s.Runtime[acc.ID] = st
			}
			s.mu.Unlock()
			s.notifyUI()
			return
		}
		if !mounted {
			if reason == "" {
				reason = "Google Drive 暂未完成授权，先直接云端运行（成片请记得保存）"
			}
			if st, ok := s.Runtime[acc.ID]; ok {
				st.DriveMountedKnown = true
				st.DriveActuallyMounted = false
				st.EffectiveOutputDir = "/content/output"
				st.WorkloadDetail = reason
				s.Runtime[acc.ID] = st
			}
			s.mu.Unlock()
			s.notifyUI()
			s.ShowToast(reason)
		} else {
			if st, ok := s.Runtime[acc.ID]; ok {
				st.DriveMountedKnown = true
				st.DriveActuallyMounted = true
				st.WatchdogDeferredForTempOutputs = false
				if acc.DriveOutputDir != "" {
					st.EffectiveOutputDir = acc.DriveOutputDir
				}
				s.Runtime[acc.ID] = st
			}
			s.mu.Unlock()
			s.notifyUI()
		}
	}

	s.mu.Lock()
	if !s.isDeployFlowOwnerLocked(acc.ID, flowToken, expectedEndpoint) || flowCtx.Err() != nil {
		if st, ok := s.Runtime[acc.ID]; ok && st.ActiveEndpoint == expectedEndpoint {
			st.DeployPreLaunch = false
			st.DeployRunning = false
			st.DeployStartingUntil = time.Time{}
			s.Runtime[acc.ID] = st
		}
		s.mu.Unlock()
		s.notifyUI()
		return
	}
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
	if err := s.Engine.StartBuiltinDeployScript(flowCtx, acc, scriptBody, envVars); err != nil {
		s.mu.Lock()
		stillValid := s.isDeployFlowOwnerLocked(acc.ID, flowToken, expectedEndpoint) && flowCtx.Err() == nil
		if !stillValid {
			if st, ok := s.Runtime[acc.ID]; ok && st.ActiveEndpoint == expectedEndpoint {
				st.DeployPreLaunch = false
				st.DeployRunning = false
				st.DeployStartingUntil = time.Time{}
				s.Runtime[acc.ID] = st
			}
			s.mu.Unlock()
			s.notifyUI()
			return
		}
		errMsg := strings.TrimSpace(err.Error())
		if errMsg == "" {
			errMsg = "未能把部署脚本送到云端机器"
		}
		st := s.Runtime[acc.ID]
		st.DeployPreLaunch = false
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
	if !s.isDeployFlowOwnerLocked(acc.ID, flowToken, expectedEndpoint) {
		s.mu.Unlock()
		return
	}
	cur = s.Runtime[acc.ID]
	cur.DeployPreLaunch = false
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
	s.clearDeployFlowIfToken(acc.ID, flowToken)
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
	_ = s.stopAccountSessionVerified(accountID, specificSession, reason)
}

func (s *Service) stopAccountSessionVerified(accountID, specificSession, reason string) bool {
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
		return true
	}
	targetSession := specificSession
	if targetSession == "" {
		targetSession = acc.SessionName
	}
	stopKey := accountID + "|" + targetSession
	if s.stoppingSessions[stopKey] {
		s.mu.Unlock()
		s.ShowToast("正在停止该会话并核验中，请稍候…")
		return false
	}
	s.stoppingSessions[stopKey] = true
	prevPhase := s.Runtime[accountID].Phase
	if targetSession == acc.SessionName {
		rt := s.Runtime[accountID]
		rt.Phase = core.InstancePhase{Kind: core.PhaseStopping, Detail: "停止计费实例…"}
		s.Runtime[accountID] = rt
	}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.stoppingSessions, stopKey)
		s.mu.Unlock()
	}()
	s.notifyUI()

	flushFailed := false
	if targetSession == acc.SessionName {
		s.cancelDeployFlow(accountID)
		s.cancelDeployWatcher(accountID)
		if prevPhase.IsRunning() && acc.UseGoogleDrive && !acc.EnableLLMModule && acc.DeployRecipe.IsVideoComfy() {
			flushCtx, cancelFlush := context.WithTimeout(context.Background(), 25*time.Second)
			if err := s.Engine.FlushWorkspaceToDrive(flushCtx, acc); err != nil {
				flushFailed = true
			}
			cancelFlush()
		}
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
		return false
	}

	shutdownHint := core.ShutdownReason("")
	if targetSession == acc.SessionName {
		shutdownHint = mapShutdownReason(reason)
	}
	s.mu.Lock()
	var ledgerBackup *core.ActiveSessionLedgerEntry
	if prev, ok := s.pendingLedger[accountID]; ok {
		cp := prev
		ledgerBackup = &cp
	}
	ep, isRunning, closedRec := s.applyServerSnapshotLocked(accountID, acc, usage, sessions, time.Now(), true, shutdownHint)
	s.mu.Unlock()
	if closedRec != nil {
		if !s.recordHistory(*closedRec) && ledgerBackup != nil && closedRec.Endpoint == ledgerBackup.Endpoint {
			s.mu.Lock()
			if _, exists := s.pendingLedger[accountID]; !exists {
				s.pendingLedger[accountID] = *ledgerBackup
			}
			s.mu.Unlock()
		}
		if closedRec.ShutdownReason == core.ShutdownExternalOrLost || closedRec.ShutdownReason == core.ShutdownCapReached {
			hwLbl := strings.TrimSpace(closedRec.Hardware)
			if hwLbl == "" {
				hwLbl = "云端机器"
			}
			s.SendNotification("云端机器已被 Google 回收停机", fmt.Sprintf("%s 的 %s 已停止运行（非本软件关机），可在「记录」查看详情", acc.DisplayName, hwLbl), true)
		}
	}
	s.PersistActiveLedger()
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
	flushWarnSuffix := ""
	if flushFailed {
		flushWarnSuffix = "（注意：关机前工作流最终同步未完成，请核对 Drive）"
		s.SendNotification("工作流最终同步未完成", fmt.Sprintf("%s 已继续执行关机止损，但关机前最后一次工作流同步到 Drive 失败，请核对最新工作流是否已保存。", acc.DisplayName), true)
	}
	if stillAlive {
		msg := fmt.Sprintf("警告：%s 会话 %s 仍出现在服务端，请前往 Colab 网页确认停机！", acc.DisplayName, targetSession)
		s.ShowToast(msg + flushWarnSuffix)
		s.SendNotification("停机核验异常", msg, true)
		return false
	} else if reason != "" {
		s.ShowToast(fmt.Sprintf("%s：%s 已确认关机止损%s", reason, targetSession, flushWarnSuffix))
	} else {
		s.ShowToast(fmt.Sprintf("%s 已确认关机止损%s", targetSession, flushWarnSuffix))
	}
	return true
}

// StopAccountManagedSessions 仅停止指定账号下的主会话与附加非孤儿会话，绝不波及其他账号；返回是否全部确认离线。
func (s *Service) StopAccountManagedSessions(accountID string) bool {
	acc, ok := s.accountByID(accountID)
	if !ok {
		return true
	}
	s.mu.RLock()
	sessions := managedSessionsForAccountLocked(acc, s.Runtime[accountID])
	s.mu.RUnlock()

	// 若缓存为空（如刚触发 412 或重启尚未刷新），对系统默认 OAuth 账号先核验本机 CLI 登录邮箱以防外部切号误停
	if len(sessions) == 0 {
		if acc.AuthMode != core.AuthIsolatedVaultADC && strings.TrimSpace(acc.Email) != "" {
			if who, ok := s.Engine.Whoami(context.Background(), acc); ok && !strings.EqualFold(strings.TrimSpace(who.Email), strings.TrimSpace(acc.Email)) {
				s.ShowToast(fmt.Sprintf("本机默认 colab CLI 当前已切换为 %s（非 %s），已中止自动停机防止误停其他账号", who.Email, acc.Email))
				return false
			}
		}
		sessions = append(sessions, acc.SessionName)
	}
	allStopped := true
	for _, name := range sessions {
		if !s.stopAccountSessionVerified(accountID, name, "停止旧会话") {
			allStopped = false
		}
	}
	return allStopped
}

// StopAllManagedSessions stops primary + all extra non-orphan sessions across all accounts and reports orphans.
// Returns true only when all targeted managed sessions are confirmed stopped.
func (s *Service) StopAllManagedSessions() bool {
	type target struct {
		accID   string
		session string
	}
	s.mu.RLock()
	var targets []target
	for _, acc := range s.Settings.Accounts {
		for _, name := range managedSessionsForAccountLocked(acc, s.Runtime[acc.ID]) {
			targets = append(targets, target{acc.ID, name})
		}
	}
	s.mu.RUnlock()

	if len(targets) == 0 {
		if orphans := s.TotalOrphanSessionCount(); orphans > 0 {
			s.ShowToast(fmt.Sprintf("存在 %d 个外部孤儿会话 [?] 无法通过 CLI 关闭，请前往 Colab 网页释放", orphans))
		}
		return true
	}
	allVerified := true
	for _, t := range targets {
		if !s.stopAccountSessionVerified(t.accID, t.session, "全部关机止损") {
			allVerified = false
		}
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
	return allVerified && stillManaged == 0
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
	s.mu.RLock()
	opener := s.terminalOpener
	s.mu.RUnlock()
	if opener == nil {
		opener = openTerminalCommand
	}
	if err := opener(cmd); err != nil {
		s.ShowToast("拉起终端失败：" + err.Error())
	} else {
		s.ShowToast("已在独立终端安全拉起 Claude Code")
	}
}

// OpenCloudTerminal opens an interactive shell to the running Colab VM, reusing the isolated SSH ControlMaster socket when active to prevent HTTP 429.
func (s *Service) OpenCloudTerminal(accountID string) {
	acc, ok := s.accountByID(accountID)
	if !ok {
		return
	}
	rt := s.StateFor(accountID)
	if !rt.Phase.IsRunning() {
		s.ShowToast("云端机器尚未开机，请先点击开机")
		return
	}
	if rt.IsFreeRuntimeRestricted(acc) {
		s.ShowToast("免费版不支持 SSH 终端，请点「Colab 网页」进入云端后台")
		return
	}

	sockPath := ""
	reuseSock := false
	if rt.SSHTunnelActive {
		sockPath = sshControlSocketPath(acc.ID, acc.SessionName)
		reuseSock = canReuseSSHControlSocket(sockPath)
	}
	adcPath := ""
	if !reuseSock {
		switch acc.AuthMode {
		case core.AuthSystemDefaultOAuth2, "":
			// system default OAuth2 session
		case core.AuthIsolatedVaultADC:
			raw, err := s.Vault.LoadADCCredentials(acc.ID)
			if err != nil || strings.TrimSpace(raw) == "" {
				s.ShowToast("无法读取该隔离账号的凭据，已取消打开终端以防跨账号连接")
				return
			}
			p, err := s.Vault.WriteEphemeralFile("term-adc", "json", []byte(raw))
			if err != nil || p == "" {
				s.ShowToast("无法创建临时凭据文件，已取消打开终端")
				return
			}
			adcPath = p
		default:
			s.ShowToast("无法识别账号认证模式，已拒绝打开终端")
			return
		}
	}
	shellCmd, reused := buildCloudTerminalShellCmd(s.OSRunner, acc, sockPath, reuseSock, adcPath)
	if reused && adcPath != "" {
		vault.ShredAndRemove(adcPath)
	}

	s.mu.RLock()
	opener := s.terminalOpener
	s.mu.RUnlock()
	if opener == nil {
		opener = openTerminalCommand
	}
	if err := opener(shellCmd); err != nil {
		if !reused && adcPath != "" {
			vault.ShredAndRemove(adcPath)
		}
		s.ShowToast("打开云端终端失败：" + err.Error())
		return
	}
	if reused {
		s.ShowToast("已在独立终端打开云端后台（复用加密通道）")
	} else {
		s.ShowToast("已在独立终端连接云端后台")
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

func (s *Service) serviceContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		select {
		case <-s.stopLoops:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

func (s *Service) startSSHTunnelInternal(accountID string) (int, *tunnelStderrBuffer, error) {
	s.mu.Lock()
	if s.sshStarting[accountID] {
		s.mu.Unlock()
		return 0, nil, errSSHTunnelStarting
	}
	s.sshStarting[accountID] = true
	usedPorts := map[int]bool{}
	for id, rt := range s.Runtime {
		if id != accountID && rt.SSHTunnelActive && rt.ActiveSSHTunnelPort != nil {
			usedPorts[*rt.ActiveSSHTunnelPort] = true
		}
	}
	starter := s.sshStarter
	_, hadExisting := s.sshTunnels[accountID]
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.sshStarting, accountID)
		s.mu.Unlock()
	}()
	if hadExisting {
		s.StopSSHTunnel(accountID)
	}
	if starter == nil {
		starter = startSSHTunnelProcess
	}

	acc, ok := s.accountByID(accountID)
	if !ok {
		return 0, nil, fmt.Errorf("未找到账号")
	}
	rt := s.StateFor(accountID)
	if !rt.Phase.IsRunning() || rt.IsFreeRuntimeRestricted(acc) {
		return 0, nil, fmt.Errorf("当前实例未运行或受免费版配额限制")
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
	proc, errBuf, err := starter(s.OSRunner, acc, localPort, remotePort, adcPath)
	if err != nil {
		if adcPath != "" {
			vault.ShredAndRemove(adcPath)
		}
		return 0, errBuf, err
	}
	s.mu.Lock()
	s.sshTunnels[accountID] = proc
	if adcPath != "" {
		s.sshADCPaths[accountID] = adcPath
	}
	if errBuf != nil {
		s.sshStderrs[accountID] = errBuf
	}
	cur := s.Runtime[accountID]
	cur.SSHTunnelActive = true
	p := localPort
	cur.ActiveSSHTunnelPort = &p
	s.Runtime[accountID] = cur
	s.mu.Unlock()
	s.notifyUI()

	if proc != nil {
		go func() {
			_ = proc.Wait()
			s.stopSSHTunnelIfMatches(accountID, proc)
		}()
	}
	return localPort, errBuf, nil
}

func (s *Service) ToggleSSHTunnel(accountID string) {
	s.mu.RLock()
	_, active := s.sshTunnels[accountID]
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
	remotePort := acc.RemoteServicePort
	if rt.DetectedServicePort != nil {
		remotePort = *rt.DetectedServicePort
	}
	localPort, _, err := s.startSSHTunnelInternal(accountID)
	if err != nil {
		s.ShowToast("启动 SSH 隧道失败：" + err.Error())
		return
	}
	s.ShowToast(fmt.Sprintf("SSH 隧道已建立：127.0.0.1:%d → :%d", localPort, remotePort))
}

func (s *Service) stopSSHTunnelIfMatches(accountID string, proc *exec.Cmd) {
	s.mu.RLock()
	cur := s.sshTunnels[accountID]
	s.mu.RUnlock()
	if cur == proc {
		s.StopSSHTunnel(accountID)
	}
}

func probeLocalComfyHTTP(port int) bool {
	client := &http.Client{Timeout: 3 * time.Second}
	for _, path := range []string{"/queue", "/"} {
		resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d%s", port, path))
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 500 {
				return true
			}
		}
	}
	return false
}

// OpenComfyUI 一键直达 ComfyUI：自动补开机/补部署、防连点、最多 2 次 SSH 隧道重连（含残留清理与 HTTP 探活）、cloudflared 云端兜底、白话助手机制。
func (s *Service) OpenComfyUI(accountID string) {
	acc, ok := s.accountByID(accountID)
	if !ok {
		return
	}

	// 防并发连点
	s.mu.Lock()
	rt := s.Runtime[accountID]
	if rt.ComfyOpening {
		s.mu.Unlock()
		s.ShowToast("正在为你连接 ComfyUI，请稍等几秒…")
		return
	}
	rt.ComfyOpening = true
	s.Runtime[accountID] = rt
	waitTimeout := s.comfyTunnelWaitTimeout
	retryPause := s.comfyRetryPause
	s.mu.Unlock()
	s.notifyUI()

	if waitTimeout <= 0 {
		waitTimeout = 22 * time.Second
	}
	if retryPause <= 0 {
		retryPause = 4 * time.Second
	}

	defer func() {
		s.mu.Lock()
		if cur, exists := s.Runtime[accountID]; exists {
			cur.ComfyOpening = false
			s.Runtime[accountID] = cur
		}
		s.mu.Unlock()
		s.notifyUI()
	}()

	// 未开机时帮小白直接一键开机并部署
	if !rt.Phase.IsRunning() {
		s.DeployNow(accountID)
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
		s.ShowToast("上次云端部署中断，请点「查看日志」排查或点「重新部署」")
		return
	}
	// 裸机未安装 ComfyUI 时直接自动拉起部署，装好后自动打开
	if !rt.ServerHealthy {
		s.ShowToast("正在为你自动安装 ComfyUI，装好后会自动弹出浏览器")
		go s.launchVideoDeployFlow(acc)
		return
	}
	if rt.IsFreeRuntimeRestricted(acc) {
		if rt.DisplayUIURL != "" && s.URLOpener != nil {
			s.URLOpener(rt.DisplayUIURL)
			return
		}
		if acc.EnableCloudflared {
			s.OpenComfyUIViaCloudflared(accountID)
			return
		}
		svcCtx, cancelSvcCtx := s.serviceContext()
		defer cancelSvcCtx()
		colabURL := s.Engine.ResolveColabURL(svcCtx, acc, acc.SessionName)
		s.PresentActionPrompt(&core.UserActionPrompt{
			ID:              uuid.New().String(),
			Kind:            core.PromptComfyHelp,
			AccountID:       acc.ID,
			Title:           "免费版不支持本机 SSH 私连通道",
			Message:         "Google 免费版不开放本机 SSH 隧道，为避免默认暴露无密码公网地址，本软件不会自动开启 cloudflared。你可以打开 Colab 官方网页操作，或显式开启临时公网链接（无密码保护，用完请及时点击关闭）。",
			URL:             colabURL,
			PrimaryLabel:    "打开 Colab 网页",
			PrimaryAction:   "open_colab",
			SecondaryLabel:  "显式开启临时公网链接",
			SecondaryAction: "enable_cloudflared",
			CancelLabel:     "关闭",
		})
		s.ShowToast("免费版不支持 SSH 私连，请确认打开方式")
		return
	}

	// 若已有现成隧道且本地 HTTP 探活直接通，立刻打开
	if rt.SSHTunnelActive && rt.ActiveSSHTunnelPort != nil {
		existingPort := *rt.ActiveSSHTunnelPort
		if probeLocalComfyHTTP(existingPort) {
			if s.URLOpener != nil {
				s.URLOpener(fmt.Sprintf("http://127.0.0.1:%d", existingPort))
			}
			return
		}
		// 现有隧道已失活，清理后再走 2 次重连
		s.StopSSHTunnel(accountID)
	}

	var lastErrText string
	for attempt := 1; attempt <= 2; attempt++ {
		port, errBuf, startErr := s.startSSHTunnelInternal(accountID)
		if errors.Is(startErr, errSSHTunnelStarting) {
			s.ShowToast(startErr.Error())
			return
		}
		if startErr != nil {
			lastErrText = startErr.Error()
		} else {
			addr := fmt.Sprintf("127.0.0.1:%d", port)
			deadline := time.Now().Add(waitTimeout)
			for time.Now().Before(deadline) {
				s.mu.RLock()
				_, stillAlive := s.sshTunnels[accountID]
				s.mu.RUnlock()
				if !stillAlive {
					break
				}
				conn, dialErr := net.DialTimeout("tcp", addr, 350*time.Millisecond)
				if dialErr == nil {
					_ = conn.Close()
					if probeLocalComfyHTTP(port) {
						if s.URLOpener != nil {
							s.URLOpener("http://" + addr)
						}
						return
					}
				}
				time.Sleep(250 * time.Millisecond)
			}
			if errBuf != nil {
				if msg := errBuf.String(); msg != "" {
					lastErrText = msg
				}
			}
		}

		// 清理残留并准备重连
		cleanupStaleColabSSH(acc)
		s.StopSSHTunnel(accountID)

		if attempt == 1 {
			if strings.Contains(strings.ToLower(lastErrText), "already-active ssh session") || strings.Contains(lastErrText, "429") {
				s.ShowToast("检测到旧 SSH 连接占坑，正在自动清理并在 4 秒后重连…")
			} else {
				s.ShowToast("本机通道暂未连通，正在清理旧连接并自动重试…")
			}
			select {
			case <-time.After(retryPause):
			case <-s.stopLoops:
				return
			}
		}
	}

	// 仅在用户显式启用 cloudflared 开关时才自动降级公网隧道，默认绝不静默暴露无认证公网端口
	if acc.EnableCloudflared {
		s.OpenComfyUIViaCloudflared(accountID)
		return
	}

	// 本机 SSH 2 次未连通且未开启公网隧道：弹窗由用户显式选择重试、打开 Colab 网页或手动启用临时公网链接
	svcCtx, cancelSvcCtx := s.serviceContext()
	defer cancelSvcCtx()
	colabURL := s.Engine.ResolveColabURL(svcCtx, acc, acc.SessionName)
	reasonHint := "本机加密通道两次尝试均未连通。"
	if strings.Contains(strings.ToLower(lastErrText), "already-active ssh session") || strings.Contains(lastErrText, "429") {
		reasonHint = "Google 检测到云端还有一个没关干净的旧 SSH 连接（通常等十几秒会自动释放）。"
	}
	s.PresentActionPrompt(&core.UserActionPrompt{
		ID:              uuid.New().String(),
		Kind:            core.PromptComfyHelp,
		AccountID:       acc.ID,
		Title:           "本机加密通道暂未连通",
		Message:         reasonHint + "为保护安全，默认不会自动暴露无密码公网链接。你可以点击「重新连接」再试，或显式开启临时公网链接（无密码，使用后请在主页点击关闭）。",
		URL:             colabURL,
		PrimaryLabel:    "重新连接本机通道",
		SecondaryLabel:  "临时开启公网链接（无密码）",
		SecondaryAction: "enable_cloudflared",
		CancelLabel:     "关闭",
	})
	s.ShowToast("本机加密通道暂未连通，已弹出处理选项")
}

// OpenComfyUIViaCloudflared 在用户显式授权或开启开关后启动 cloudflared 临时公网预览链接。
func (s *Service) OpenComfyUIViaCloudflared(accountID string) {
	acc, ok := s.accountByID(accountID)
	if !ok {
		return
	}
	rt := s.StateFor(accountID)
	if !rt.Phase.IsRunning() {
		s.ShowToast("云端机器未运行")
		return
	}
	remotePort := acc.RemoteServicePort
	if rt.DetectedServicePort != nil {
		remotePort = *rt.DetectedServicePort
	}
	s.ShowToast("正在按你的确认开启云端临时公网链接（注意无访问密码）…")
	svcCtx, cancelSvcCtx := s.serviceContext()
	defer cancelSvcCtx()
	cfURL, err := s.Engine.EnsureCloudflaredFallbackURL(svcCtx, acc, remotePort)
	if err != nil || cfURL == "" {
		s.ShowToast("开启临时公网链接失败，请尝试重新连接本机通道或直接打开 Colab 网页")
		return
	}
	s.mu.Lock()
	if cur, exists := s.Runtime[accountID]; exists && cur.ActiveEndpoint == rt.ActiveEndpoint {
		cur.DisplayUIURL = cfURL
		cur.CloudflaredActive = true
		s.Runtime[accountID] = cur
	}
	s.mu.Unlock()
	s.notifyUI()
	if s.URLOpener != nil {
		s.URLOpener(cfURL)
	}
	s.ShowToast("已开启临时公网链接（无密码保护，用完请及时点击关闭）")
}

// StopCloudflaredTunnel 立即关闭云端已开启的 cloudflared 临时公网通道。
func (s *Service) StopCloudflaredTunnel(accountID string) {
	acc, ok := s.accountByID(accountID)
	if !ok {
		return
	}
	svcCtx, cancelSvcCtx := s.serviceContext()
	defer cancelSvcCtx()
	if err := s.Engine.StopCloudflaredTunnel(svcCtx, acc); err != nil {
		s.ShowToast("关闭公网通道失败：" + err.Error())
		return
	}
	s.mu.Lock()
	if cur, exists := s.Runtime[accountID]; exists {
		cur.CloudflaredActive = false
		if strings.Contains(cur.DisplayUIURL, "trycloudflare.com") {
			cur.DisplayUIURL = ""
		}
		s.Runtime[accountID] = cur
	}
	s.mu.Unlock()
	s.notifyUI()
	s.ShowToast("已关闭云端临时公网通道")
}

// RemountDriveNow 允许用户在降级临时盘状态下手动重新触发 Google Drive 授权挂载。
func (s *Service) RemountDriveNow(accountID string) {
	acc, ok := s.accountByID(accountID)
	if !ok {
		return
	}
	rt := s.StateFor(accountID)
	if !rt.Phase.IsRunning() {
		s.ShowToast("云端机器未运行")
		return
	}
	expectedEndpoint := rt.ActiveEndpoint
	s.ShowToast("正在重新挂载 Google Drive…")
	svcCtx, cancelSvcCtx := s.serviceContext()
	defer cancelSvcCtx()
	mounted, reason := s.Engine.EnsureDriveMountedInteractive(svcCtx, acc, func(authURL string) bool {
		ch := make(chan bool, 1)
		s.mu.Lock()
		if oldCh := s.driveAuthResultCh; oldCh != nil {
			select {
			case oldCh <- false:
			default:
			}
		}
		s.driveAuthResultCh = ch
		s.actionPrompt = &core.UserActionPrompt{
			ID:             uuid.New().String(),
			Kind:           core.PromptDriveAuth,
			AccountID:      acc.ID,
			Title:          "重新授权挂载 Google Drive",
			Message:        "已在浏览器打开 Google Drive 授权页面，请点「允许」后回到这里点击下方按钮继续挂载。",
			URL:            authURL,
			PrimaryLabel:   "我已点完允许，继续",
			SecondaryLabel: "取消挂载",
			CancelLabel:    "关闭",
		}
		opener := s.URLOpener
		s.mu.Unlock()
		s.notifyUI()
		if authURL != "" && opener != nil {
			opener(authURL)
		}
		select {
		case proceed := <-ch:
			return proceed
		case <-svcCtx.Done():
			return false
		case <-s.stopLoops:
			return false
		}
	})
	s.mu.Lock()
	hasTempVideos := false
	if cur, exists := s.Runtime[accountID]; exists && cur.ActiveEndpoint == expectedEndpoint {
		cur.DriveMountedKnown = true
		cur.DriveActuallyMounted = mounted
		hasTempVideos = cur.TempOutputVideos > 0 || (!cur.TempOutputScanKnown && cur.RenderedVideoCount > 0)
		s.Runtime[accountID] = cur
	}
	s.mu.Unlock()
	s.notifyUI()
	if mounted {
		if hasTempVideos {
			s.ShowToast("Google Drive 已挂载；当前运行中的 ComfyUI 既有成片仍在临时盘 /content/output，请先保存或重新部署切换目录")
		} else {
			s.ShowToast("Google Drive 已挂载；如需让运行中的 ComfyUI 切换输出目录到 Drive，请点击「重新部署」")
		}
	} else {
		if reason == "" {
			reason = "Google Drive 暂未完成挂载，当前仍使用云端临时盘"
		}
		s.ShowToast(reason)
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

func (s *Service) OpenDriveOutputs(accountID ...string) {
	if s.URLOpener == nil {
		return
	}
	var acc core.AccountProfile
	hasAcc := false
	if len(accountID) > 0 && strings.TrimSpace(accountID[0]) != "" {
		acc, hasAcc = s.accountByID(accountID[0])
	}
	if !hasAcc {
		acc, hasAcc = s.SelectedAccount()
	}
	queryTerm := "ColabTools"
	authEmail := ""
	if hasAcc {
		authEmail = strings.TrimSpace(acc.Email)
		dir := strings.TrimSpace(acc.DriveOutputDir)
		if dir != "" {
			cleaned := path.Clean(strings.ReplaceAll(dir, `\`, "/"))
			base := path.Base(cleaned)
			if base != "" && base != "." && base != "/" {
				// If user kept default .../ColabTools/outputs, search parent folder name for clearer hits when base is generic "outputs"
				if strings.EqualFold(base, "outputs") {
					parent := path.Base(path.Dir(cleaned))
					if parent != "" && parent != "." && parent != "/" && !strings.EqualFold(parent, "MyDrive") {
						queryTerm = parent
					} else {
						queryTerm = base
					}
				} else {
					queryTerm = base
				}
			}
		}
	}
	target := "https://drive.google.com/drive/search?q=" + url.QueryEscape(queryTerm)
	if authEmail != "" {
		target += "&authuser=" + url.QueryEscape(authEmail)
	}
	s.URLOpener(target)
}

func (s *Service) StopSSHTunnel(accountID string) {
	s.mu.Lock()
	var accSessionName string
	for _, a := range s.Settings.Accounts {
		if a.ID == accountID {
			accSessionName = a.SessionName
			break
		}
	}
	proc := s.sshTunnels[accountID]
	adc := s.sshADCPaths[accountID]
	delete(s.sshTunnels, accountID)
	delete(s.sshADCPaths, accountID)
	delete(s.sshStderrs, accountID)
	if cur, ok := s.Runtime[accountID]; ok {
		cur.SSHTunnelActive = false
		cur.ActiveSSHTunnelPort = nil
		s.Runtime[accountID] = cur
	}
	s.mu.Unlock()
	if proc != nil && proc.Process != nil {
		_ = proc.Process.Kill()
	}
	if accSessionName != "" {
		removeSSHControlSocket(accountID, accSessionName)
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
	acc, ok := s.accountByID(accountID)
	if !ok {
		return
	}
	rt := s.StateFor(accountID)
	if rt.DisplayUIURL != "" {
		if s.URLOpener != nil {
			s.URLOpener(rt.DisplayUIURL)
		}
		return
	}
	if rt.ActiveSSHTunnelPort != nil && rt.SSHTunnelActive {
		port := *rt.ActiveSSHTunnelPort
		addr := fmt.Sprintf("127.0.0.1:%d", port)
		if conn, err := net.DialTimeout("tcp", addr, 350*time.Millisecond); err == nil {
			_ = conn.Close()
			if s.URLOpener != nil {
				s.URLOpener("http://" + addr)
			}
			return
		}
		s.StopSSHTunnel(accountID)
	} else if rt.ActiveSSHTunnelPort != nil {
		if s.URLOpener != nil {
			s.URLOpener(fmt.Sprintf("http://127.0.0.1:%d", *rt.ActiveSSHTunnelPort))
		}
		return
	}
	if !rt.Phase.IsRunning() {
		s.ShowToast("请先开机后再打开 WebUI")
		return
	}
	if acc.DeployRecipe.IsVideoComfy() && !acc.EnableLLMModule {
		go s.OpenComfyUI(accountID)
		return
	}
	if acc.EnableLLMModule {
		s.ShowToast("大模型代理模式请直接使用「同步工具」或「Claude 终端」")
		return
	}
	if rt.IsFreeRuntimeRestricted(acc) {
		s.ShowToast("免费版不支持本机加密通道，请等待远端 WebUI 报告公网地址")
		return
	}

	s.mu.Lock()
	cur := s.Runtime[accountID]
	if cur.ComfyOpening {
		s.mu.Unlock()
		s.ShowToast("正在为你建立本机加密通道，请稍等几秒…")
		return
	}
	cur.ComfyOpening = true
	s.Runtime[accountID] = cur
	waitTimeout := s.comfyTunnelWaitTimeout
	s.mu.Unlock()
	s.notifyUI()
	if waitTimeout <= 0 {
		waitTimeout = 22 * time.Second
	}

	s.ShowToast("正在自动建立本机加密通道并打开 WebUI…")
	go func() {
		defer func() {
			s.mu.Lock()
			if st, exists := s.Runtime[accountID]; exists {
				st.ComfyOpening = false
				s.Runtime[accountID] = st
			}
			s.mu.Unlock()
			s.notifyUI()
		}()

		port, errBuf, startErr := s.startSSHTunnelInternal(accountID)
		if errors.Is(startErr, errSSHTunnelStarting) {
			s.ShowToast(startErr.Error())
			return
		}
		if startErr != nil {
			s.ShowToast("WebUI 本机通道启动失败：" + startErr.Error())
			return
		}
		addr := fmt.Sprintf("127.0.0.1:%d", port)
		deadline := time.Now().Add(waitTimeout)
		for time.Now().Before(deadline) {
			select {
			case <-s.stopLoops:
				return
			default:
			}
			s.mu.RLock()
			_, stillAlive := s.sshTunnels[accountID]
			s.mu.RUnlock()
			if !stillAlive {
				break
			}
			if conn, dialErr := net.DialTimeout("tcp", addr, 350*time.Millisecond); dialErr == nil {
				_ = conn.Close()
				if s.URLOpener != nil {
					s.URLOpener("http://" + addr)
				}
				return
			}
			time.Sleep(250 * time.Millisecond)
		}

		errText := ""
		if errBuf != nil {
			errText = errBuf.String()
		}
		cleanupStaleColabSSH(acc)
		s.StopSSHTunnel(accountID)
		if strings.Contains(strings.ToLower(errText), "already-active ssh session") || strings.Contains(errText, "429") {
			s.ShowToast("检测到旧 SSH 连接占坑，已清理旧连接，请稍等十几秒后再点一次「打开 WebUI」")
		} else {
			s.ShowToast("WebUI 本机通道暂未连通，请确认云端服务已在对应端口启动")
		}
	}()
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
	var toTempGuard []struct {
		id     string
		name   string
		videos int
	}
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

		if rt.Phase.IsQueuing() || rt.Phase.IsRunning() || rt.Phase.Kind == core.PhaseStarting {
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
				tempOutputsAtRisk := !acc.EnableLLMModule && acc.UseGoogleDrive && ((rt.TempOutputScanKnown && rt.TempOutputVideos > 0) ||
					(rt.DriveMountedKnown && !rt.DriveActuallyMounted && (rt.RenderedVideoCount > 0 || (rt.TempOutputScanKnown && rt.TempOutputVideos < 0))))
				if tempOutputsAtRisk {
					if !rt.WatchdogDeferredForTempOutputs {
						rt.WatchdogDeferredForTempOutputs = true
						vidCount := rt.TempOutputVideos
						if vidCount <= 0 {
							vidCount = rt.RenderedVideoCount
						}
						toTempGuard = append(toTempGuard, struct {
							id     string
							name   string
							videos int
						}{acc.ID, acc.DisplayName, vidCount})
					}
				} else {
					toShutdown = append(toShutdown, acc.ID)
				}
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
	for _, tg := range toTempGuard {
		msg := fmt.Sprintf("检测到 %s 的云端临时盘（未挂载 Drive）有 %d 条新成片，已暂缓自动关机以防清空视频，请先打开画布下载或确认关机。", tg.name, tg.videos)
		s.SendNotification("暂缓自动关机：临时盘有未保存成片", msg, true)
		s.PresentActionPrompt(&core.UserActionPrompt{
			ID:             uuid.New().String(),
			Kind:           core.PromptTempOutputGuard,
			AccountID:      tg.id,
			Title:          "已暂缓自动关机：临时盘有新成片",
			Message:        msg,
			PrimaryLabel:   "打开画布保存（重置计时）",
			SecondaryLabel: "已保存/放弃成片，立刻关机",
			CancelLabel:    "稍后处理",
		})
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
