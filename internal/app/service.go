package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
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
	"github.com/egoist/mygo"
)

const AppVersion = "0.3.25"

var errSSHTunnelStarting = errors.New("本机加密通道正在建立中，请稍候")

type MainSection int

const (
	SectionHome MainSection = iota
	SectionHistory
	SectionAccounts
	SectionSettings
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
	driveMountCancels      map[string]deployWatchHandle
	skipNextDriveMount     map[string]bool
	pendingOAuth           *colab.OAuthSessionTicket
	actionPrompt           *core.UserActionPrompt
	pendingQuitCallback    func()
	driveAuthResultCh      chan bool
	comfyTunnelWaitTimeout  time.Duration
	comfyRetryPause         time.Duration
	tempOutputGraceDuration time.Duration
	sshStarter              func(r *runner.OSRunner, profile core.AccountProfile, localPort, remotePort int, adcPath string) (*exec.Cmd, *tunnelStderrBuffer, error)
	terminalOpener          func(shellCmd string) error
	revealFileFn            func(localPath string)
	downloadBaseDir         string

	OSRunner *runner.OSRunner
	Engine   *colab.Engine
	Vault    *vault.Vault

	persistChanges bool
	saveMu         sync.Mutex
	configPath     string
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
		_ = hist.ImportLegacyActiveSessions(filepath.Join(core.ConfigDir(), "active_sessions.json"))
	}
	s := &Service{
		Settings:           core.DefaultAppSettings(),
		Runtime:            map[string]core.AccountRuntimeState{},
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
		driveMountCancels:  map[string]deployWatchHandle{},
		skipNextDriveMount: map[string]bool{},
		sshStarter:         startSSHTunnelProcess,
		terminalOpener:     openTerminalCommand,
		revealFileFn: func(localPath string) {
			if !skipBootstrap && runtime.GOOS == "darwin" && strings.TrimSpace(localPath) != "" {
				_ = exec.Command("open", "-R", localPath).Start()
			}
		},
		OSRunner:           r,
		Engine:             colab.NewEngine(r, v),
		Vault:              v,
		persistChanges:     !skipBootstrap,
		configPath:         core.DefaultConfigPath(),
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

func (s *Service) cloneSettingsLocked() core.AppSettings {
	cp := s.Settings
	cp.Accounts = make([]core.AccountProfile, len(s.Settings.Accounts))
	for i, acc := range s.Settings.Accounts {
		acc.QueueTargets = slices.Clone(acc.QueueTargets)
		acc.EligibleAccelerators = slices.Clone(acc.EligibleAccelerators)
		cp.Accounts[i] = acc
	}
	if s.Settings.QueueCooldownsByAccount != nil {
		cp.QueueCooldownsByAccount = make(map[string]time.Time, len(s.Settings.QueueCooldownsByAccount))
		for k, v := range s.Settings.QueueCooldownsByAccount {
			cp.QueueCooldownsByAccount[k] = v
		}
	}
	return cp
}

func (s *Service) saveSettingsSnapshot(_ core.AppSettings) {
	if !s.persistChanges {
		return
	}
	// Serialize disk writes and always persist the latest in-memory snapshot under saveMu
	// so slower background goroutines can never overwrite newer user edits with stale state.
	s.saveMu.Lock()
	s.mu.RLock()
	latest := s.cloneSettingsLocked()
	s.mu.RUnlock()
	err := latest.Save(s.configPath)
	s.saveMu.Unlock()
	if err != nil {
		log.Printf("[colab-tools] save config failed: %v", err)
		s.ShowToast("保存配置文件失败：" + err.Error())
	}
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

// StartBackgroundServices runs startup environment inspection, system account import,
// and background watchdog/polling loops.
func (s *Service) StartBackgroundServices() {
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
				snap := s.cloneSettingsLocked()
				s.mu.Unlock()
				s.saveSettingsSnapshot(snap)
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
				s.OnOpenMainWin(SectionHome, true)
			}
		} else {
			s.RefreshAllAccounts(true, true)
		}
	}()

	// 1-second tick: watchdog idle counter + stopwatch/queue countdown repaint
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
	s.flushInFlightSessions()
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
	for i, acc := range s.Settings.Accounts {
		acc.QueueTargets = slices.Clone(acc.QueueTargets)
		acc.EligibleAccelerators = slices.Clone(acc.EligibleAccelerators)
		out[i] = acc
	}
	return out
}

func (s *Service) SelectedAccount() (core.AccountProfile, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, acc := range s.Settings.Accounts {
		if acc.ID == s.Settings.SelectedAccountID {
			acc.QueueTargets = slices.Clone(acc.QueueTargets)
			acc.EligibleAccelerators = slices.Clone(acc.EligibleAccelerators)
			return acc, true
		}
	}
	if len(s.Settings.Accounts) > 0 {
		acc := s.Settings.Accounts[0]
		acc.QueueTargets = slices.Clone(acc.QueueTargets)
		acc.EligibleAccelerators = slices.Clone(acc.EligibleAccelerators)
		return acc, true
	}
	return core.AccountProfile{}, false
}

func (s *Service) StateFor(accountID string) core.AccountRuntimeState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if rt, ok := s.Runtime[accountID]; ok {
		rt.Sessions = slices.Clone(rt.Sessions)
		rt.SessionOutputs = slices.Clone(rt.SessionOutputs)
		rt.GPUUtilHistory = slices.Clone(rt.GPUUtilHistory)
		rt.VRAMHistory = slices.Clone(rt.VRAMHistory)
		return rt
	}
	return core.NewAccountRuntimeState()
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

func (s *Service) SelectAccount(accountID string) {
	s.mu.Lock()
	s.Settings.SelectedAccountID = accountID
	snap := s.cloneSettingsLocked()
	s.mu.Unlock()
	s.saveSettingsSnapshot(snap)
	s.notifyUI()
}

func (s *Service) UpdateAccount(updated core.AccountProfile) {
	s.updateAccountProfile(updated, true)
}

func (s *Service) updateAccountProfile(updated core.AccountProfile, syncRunningIdleTimeout bool) {
	updated.Sanitize()
	if updated.QueuePersistentMode && !updated.AutoShutdownEnabled {
		updated.AutoShutdownEnabled = true
	}
	s.mu.Lock()
	var prev core.AccountProfile
	foundPrev := false
	for i := range s.Settings.Accounts {
		if s.Settings.Accounts[i].ID == updated.ID {
			prev = s.Settings.Accounts[i]
			foundPrev = true
			s.Settings.Accounts[i] = updated
			break
		}
	}
	if rt, ok := s.Runtime[updated.ID]; ok {
		if rt.Phase.IsQueuing() {
			rt.Phase.QueuePersistent = updated.QueuePersistentMode
			rt.Phase.QueueMaxHours = core.EffectivePersistentHours(updated)
			if updated.QueuePersistentMode {
				rt.Phase.QueueMax = 0
			} else if rt.Phase.QueueMax <= 0 {
				rt.Phase.QueueMax = core.QueueMaxAttempts
			}
			s.Runtime[updated.ID] = rt
		} else if syncRunningIdleTimeout && rt.Phase.IsRunning() && rt.SessionLocked && foundPrev && updated.IdleTimeoutMinutes != prev.IdleTimeoutMinutes {
			rt.SessionIdleTimeoutMin = core.EffectiveIdleTimeoutMinutes(updated)
			s.Runtime[updated.ID] = rt
		}
	}
	snap := s.cloneSettingsLocked()
	s.mu.Unlock()
	s.saveSettingsSnapshot(snap)
	s.notifyUI()
}

func (s *Service) UpdateSettings(fn func(*core.AppSettings)) {
	s.mu.Lock()
	fn(&s.Settings)
	snap := s.cloneSettingsLocked()
	s.mu.Unlock()
	s.saveSettingsSnapshot(snap)
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
	delete(s.Settings.QueueCooldownsByAccount, accountID)
	if s.Settings.SelectedAccountID == accountID {
		if len(filtered) > 0 {
			s.Settings.SelectedAccountID = filtered[0].ID
		} else {
			s.Settings.SelectedAccountID = ""
		}
	}
	snap := s.cloneSettingsLocked()
	s.mu.Unlock()
	if s.History != nil {
		_ = s.History.DeleteInFlight(accountID, "")
	}
	s.saveSettingsSnapshot(snap)
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
