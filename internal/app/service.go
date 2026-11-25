package app

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net"
	"os/exec"
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

const AppVersion = "0.3.8"

type MainSection int

const (
	SectionStatus MainSection = iota
	SectionDeploy
	SectionCompute
	SectionIntegrations
	SectionAccounts
	SectionSettings
)

var MainSectionTitles = []string{"状态", "部署", "抢卡", "接入", "账号", "设置"}

type deployWatchHandle struct {
	token  string
	cancel context.CancelFunc
}

type queueHandle struct {
	token  string
	cancel context.CancelFunc
}

type Service struct {
	mu sync.RWMutex

	Settings        core.AppSettings
	Runtime         map[string]core.AccountRuntimeState
	Insights        core.AllocationInsights
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
	if skipBootstrap {
		v = vault.New(vault.NewMemoryBackend())
	} else {
		v = vault.New(nil)
	}
	s := &Service{
		Settings:       core.DefaultAppSettings(),
		Runtime:        map[string]core.AccountRuntimeState{},
		Insights:       core.NewAllocationInsights(),
		CLIInstalled:   true,
		OnboardingStep: 1,
		queueCancels:       map[string]queueHandle{},
		refreshing:         map[string]bool{},
		sshTunnels:         map[string]*exec.Cmd{},
		sshADCPaths:        map[string]string{},
		deployWatchCancels: map[string]deployWatchHandle{},
		OSRunner:       r,
		Engine:         colab.NewEngine(r, v),
		Vault:          v,
		persistChanges: !skipBootstrap,
		configPath:     core.DefaultConfigPath(),
		insightsPath:   core.DefaultInsightsPath(),
		stopLoops:      make(chan struct{}),
		NotifySender: func(title, body string) {
			if mygo.App.IsReady() && mygo.NotificationsSupported() {
				mygo.NewNotification(mygo.NotificationOptions{Title: title, Body: body}).Show()
			}
		},
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
	primaryEndpoint, isRunning := s.applyServerSnapshotLocked(accountID, acc, usage, sessions, now, settleBusy)
	s.mu.Unlock()
	s.notifyUI()

	if isRunning {
		s.fetchAndApplyTelemetry(acc, primaryEndpoint)
	}
	return true
}

func (s *Service) applyServerSnapshotLocked(
	accountID string,
	acc core.AccountProfile,
	usage *colab.UsageSnapshot,
	sessions []core.ColabSessionItem,
	now time.Time,
	settleBusy bool,
) (primaryEndpoint string, isRunning bool) {
	rt := s.Runtime[accountID]
	if rt.ContextLimit == 0 {
		rt = core.NewAccountRuntimeState()
	}
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
		if settleBusy || !rt.Phase.IsBusy() {
			rt.Phase = core.InstancePhase{Kind: core.PhaseRunning}
		}
		if rt.ActiveEndpoint != primary.Endpoint {
			// Endpoint changed -> invalidate bound LLM URL & video deploy/auto-open state
			rt.PublicURL = ""
			rt.LLMURLBoundEndpoint = ""
			rt.DisplayUIURL = ""
			rt.ServerHealthy = false
			rt.DeployRunning = false
			rt.RenderedVideoCount = 0
			rt.SawQueueBusyThisRun = false
			rt.AutoOpenedEndpoint = ""
		}
		rt.ActiveEndpoint = primary.Endpoint
		rt.ActiveHardware = primary.Hardware
		s.Runtime[accountID] = rt
		return primary.Endpoint, true
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
	rt.PublicURL = ""
	rt.LLMURLBoundEndpoint = ""
	rt.DisplayUIURL = ""
	rt.ServerHealthy = false
	rt.DeployRunning = false
	rt.RenderedVideoCount = 0
	rt.SawQueueBusyThisRun = false
	rt.AutoOpenedEndpoint = ""
	rt.QueueRunning = 0
	rt.QueuePending = 0
	rt.IdleSeconds = 0
	rt.ActivitySignal = core.ActivitySignal{Kind: core.SignalUnknown}
	rt.SignalSource = core.SignalSourceNone
	s.Runtime[accountID] = rt
	return "", false
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
	rt.DeployRunning = tel.DeployAlive
	rt.RenderedVideoCount = tel.RenderedVideoCount

	// Evaluate non-LLM signals (active deploy -> status.json with explicit busy -> ComfyUI /queue -> dual low-load fallback)
	statusFresh := tel.StatusPayload != nil && tel.StatusAgeSeconds != nil && *tel.StatusAgeSeconds <= core.StatusFileMaxAgeSec
	if statusFresh {
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
		if tel.DeployAlive {
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
		!tel.DeployAlive &&
		rt.AutoOpenedEndpoint != expectedEndpoint
	if shouldAutoOpenComfy {
		rt.AutoOpenedEndpoint = expectedEndpoint
	}

	s.Runtime[acc.ID] = rt
	s.mu.Unlock()
	s.notifyUI()

	if shouldAutoOpenComfy {
		s.cancelDeployWatcher(acc.ID)
		go func() {
			s.SendNotification("ComfyUI 已准备好", fmt.Sprintf("%s 的 %s 已启动，正在通过本机加密通道打开画布", acc.DisplayName, acc.H3Variant.DisplayName()), false)
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
		s.ShowToast(fmt.Sprintf("防风控冷却中，请约 %d 分钟后再申请", remMin))
		return
	}
	if acc.EnablePoliteQueue {
		if s.ActiveQueueAccountID != "" && s.ActiveQueueAccountID != accountID {
			s.mu.Unlock()
			s.ShowToast("已有其他账号在合规排队中（全局单通道防风控）")
			return
		}
		token := uuid.New().String()
		ctx, cancel := context.WithCancel(context.Background())
		s.ActiveQueueAccountID = accountID
		s.activeQueueToken = token
		s.queueCancels[accountID] = queueHandle{token: token, cancel: cancel}
		targets := core.SanitizedQueueTargets(acc.QueueTargets, acc.Tier)
		rt.Phase = core.InstancePhase{
			Kind:           core.PhaseQueuing,
			QueueAttempt:   1,
			QueueMax:       core.QueueMaxAttempts,
			TargetsSummary: core.QueueTargetsSummary(targets),
		}
		s.Runtime[accountID] = rt
		s.mu.Unlock()
		s.notifyUI()
		go s.runPoliteQueue(ctx, acc, token)
		return
	}

	rt.Phase = core.InstancePhase{Kind: core.PhaseStarting, Detail: "申请云端实例…"}
	s.Runtime[accountID] = rt
	s.mu.Unlock()
	s.notifyUI()

	go func() {
		res := s.Engine.ProvisionSession(context.Background(), acc, nil, nil, nil, func(msg string) {
			s.mu.Lock()
			cur := s.Runtime[accountID]
			cur.Phase = core.InstancePhase{Kind: core.PhaseStarting, Detail: msg}
			s.Runtime[accountID] = cur
			s.mu.Unlock()
			s.notifyUI()
		})
		s.recordProbeInsights(res.ProbeRecords)
		if res.Outcome.Kind == core.OutcomeAllocated {
			s.handlePostAllocation(acc, res.Outcome.Hardware)
		} else {
			s.handleProvisionFailure(acc, res.Outcome)
		}
	}()
}

func (s *Service) isQueueTokenOwnerLocked(accountID, token string) bool {
	h, ok := s.queueCancels[accountID]
	return ok && h.token == token
}

func (s *Service) runPoliteQueue(ctx context.Context, acc core.AccountProfile, token string) {
	startedAt := time.Now()
	deadline := startedAt.Add(core.QueueMaxTotalWait)
	pruned := map[string]bool{}

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
			return
		}
		// Pre-flight check: verify session not already running on server
		if _, sessions, err := s.Engine.FetchUsageAndSessions(ctx, acc); err == nil {
			for _, sess := range sessions {
				if !sess.IsOrphan && sess.Name == acc.SessionName {
					s.handlePostAllocation(acc, sess.Hardware)
					return
				}
			}
		}

		roundAccs := core.QueueTargetsForRound(attempt, acc.QueueTargets, acc.QueueFallbackEnabled, acc.QueueFallbackAfterRounds, acc.QueueFallbackTarget, acc.Tier)
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
				s.handleProvisionFailure(acc, core.ProvisionOutcome{Kind: core.OutcomeCircuitBroken, Code: "NO_SUPPORTED_TARGET", Message: "所选显卡均不被当前订阅支持，排队已停止"})
			}
			return
		}

		s.mu.Lock()
		if !s.isQueueTokenOwnerLocked(acc.ID, token) {
			s.mu.Unlock()
			return
		}
		cur := s.Runtime[acc.ID]
		cur.QueueInFlight = true
		cur.Phase = core.InstancePhase{
			Kind:           core.PhaseQueuing,
			QueueAttempt:   attempt,
			QueueMax:       core.QueueMaxAttempts,
			TargetsSummary: core.QueueTargetsSummary(roundAccs),
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
			s.handlePostAllocation(acc, res.Outcome.Hardware)
			return
		}
		if !owned || ctx.Err() != nil {
			return
		}
		if res.Outcome.Kind == core.OutcomeCircuitBroken || res.Outcome.Kind == core.OutcomeBudgetExhausted {
			s.handleProvisionFailure(acc, res.Outcome)
			return
		}

		delay, ok := core.NextQueueDelay(attempt, startedAt, time.Now(), rand.Float64())
		if !ok {
			s.handleProvisionFailure(acc, core.ProvisionOutcome{Kind: core.OutcomeBudgetExhausted, Message: "已达 5 轮 / 40 分钟合规排队上限，暂未拿到目标卡"})
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
			TargetsSummary: core.QueueTargetsSummary(roundAccs),
		}
		s.Runtime[acc.ID] = c
		s.mu.Unlock()
		s.notifyUI()

		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return
		}
	}
}

func (s *Service) CancelQueue(accountID string) {
	s.mu.Lock()
	h := s.queueCancels[accountID]
	inFlight := s.Runtime[accountID].QueueInFlight
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
	s.mu.Lock()
	rt := s.Runtime[acc.ID]
	rt.Phase = core.InstancePhase{Kind: core.PhaseOffline}
	rt.LastError = outcome.Message
	if outcome.Kind == core.OutcomeCircuitBroken && outcome.Code != "CANCELLED" {
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

	s.ShowToast(outcome.Message)
	if outcome.Kind == core.OutcomeCircuitBroken && outcome.Code != "CANCELLED" {
		s.SendNotification("Colab Tools 防风控熔断", outcome.Message, true)
	}
}

func (s *Service) handlePostAllocation(acc core.AccountProfile, hardware string) {
	estRate := core.EstimatedHourlyCU(hardware)
	s.SendNotification("Colab 实例已就绪", fmt.Sprintf("%s 已分配 %s（约 %.2f CU/h）", acc.DisplayName, hardware, estRate), false)
	// Always settle transitional PhaseStarting / PhaseQueuing via authoritative server fetch
	if !s.refreshAccount(acc.ID, true, true, true) {
		usage, sessions, err := s.Engine.FetchUsageAndSessions(context.Background(), acc)
		if err == nil {
			s.mu.Lock()
			ep, running := s.applyServerSnapshotLocked(acc.ID, acc, usage, sessions, time.Now(), true)
			s.mu.Unlock()
			s.notifyUI()
			if running {
				s.fetchAndApplyTelemetry(acc, ep)
			}
		} else {
			s.mu.Lock()
			rt := s.Runtime[acc.ID]
			if rt.Phase.Kind == core.PhaseStarting {
				rt.Phase = core.InstancePhase{Kind: core.PhaseRunning}
				rt.ActiveHardware = hardware
				s.Runtime[acc.ID] = rt
			}
			s.mu.Unlock()
			s.notifyUI()
		}
	}

	if acc.DeployRecipe.IsVideoComfy() {
		s.launchVideoDeployFlow(acc)
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

// ApplyDeployRecipe updates an account's one-click deploy preset and aligns ports/workload flags cleanly.
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
	case core.RecipeLLMProxy:
		acc.WorkloadKind = core.WorkloadLLM
		acc.EnableLLMModule = true
		acc.RemoteServicePort = 18080
		acc.LocalSSHTunnelPort = 18080
	default:
		acc.WorkloadKind = core.WorkloadGeneral
		acc.EnableLLMModule = false
	}
	s.UpdateAccount(acc)
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

	if acc.UseGoogleDrive {
		s.ShowToast("正在挂载 Google Drive（复用模型并自动存成片）…")
		if !s.Engine.EnsureDriveMounted(context.Background(), acc) {
			s.ShowToast("Google Drive 暂未完成授权，先直接云端下载试跑（成片请记得保存）")
		}
	}

	scriptBody := templates.H3VideoScript
	if acc.DeployRecipe == core.RecipeComfyUIGeneral {
		scriptBody = templates.ComfyUIVideoScript
	}
	if err := s.Engine.StartBuiltinDeployScript(context.Background(), acc, scriptBody, envVars); err != nil {
		s.ShowToast("送出部署脚本失败：" + err.Error())
		return
	}

	s.mu.Lock()
	cur := s.Runtime[acc.ID]
	cur.DeployRunning = true
	cur.WorkloadMetricLabel = "正在部署"
	cur.WorkloadMetricValue = "10%"
	cur.WorkloadMetricUnit = "准备中"
	ratio := 0.10
	cur.WorkloadMetricRatio = &ratio
	cur.WorkloadDetail = fmt.Sprintf("正在云端自动部署 %s…", acc.H3Variant.DisplayName())
	cur.AutoOpenedEndpoint = ""
	s.Runtime[acc.ID] = cur
	s.mu.Unlock()
	s.notifyUI()
	s.ShowToast("已自动把部署脚本送到云端，大屏将实时显示拷贝与加载进度")

	// Start a bounded 12s progress watcher (up to 18 minutes) scoped by unique token so old watchers never cancel newer deploys
	s.cancelDeployWatcher(acc.ID)
	watchToken := uuid.New().String()
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.deployWatchCancels[acc.ID] = deployWatchHandle{token: watchToken, cancel: cancel}
	s.mu.Unlock()

	go func() {
		defer s.clearDeployWatcherIfToken(acc.ID, watchToken)
		tk := time.NewTicker(12 * time.Second)
		defer tk.Stop()
		deadline := time.Now().Add(18 * time.Minute)
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.stopLoops:
				return
			case <-tk.C:
				if time.Now().After(deadline) {
					return
				}
				latestAcc, ok := s.accountByID(acc.ID)
				if !ok {
					return
				}
				rt := s.StateFor(acc.ID)
				if !rt.Phase.IsRunning() || rt.ActiveEndpoint == "" {
					return
				}
				s.fetchAndApplyTelemetry(latestAcc, rt.ActiveEndpoint)
				after := s.StateFor(acc.ID)
				if !after.DeployRunning && after.ServerHealthy {
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

	s.mu.Lock()
	ep, isRunning := s.applyServerSnapshotLocked(accountID, acc, usage, sessions, time.Now(), true)
	s.mu.Unlock()
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
		s.ShowToast(fmt.Sprintf("本机通道 127.0.0.1:%d 暂未连通，请确认 ComfyUI 已就绪后再试", port))
		return
	}
	if s.URLOpener != nil {
		s.URLOpener("http://" + addr)
	}
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
