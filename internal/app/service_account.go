package app

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/LJMStark/Colab-Tools/internal/colab"
	"github.com/LJMStark/Colab-Tools/internal/core"
	"github.com/LJMStark/Colab-Tools/internal/doctor"
	"github.com/LJMStark/Colab-Tools/internal/integrations"
	"github.com/google/uuid"
)

// Navigation, Environment Doctor, Account Import & OAuth

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
	s.SelectedSection = SectionHome
	snap := s.cloneSettingsLocked()
	s.mu.Unlock()
	s.saveSettingsSnapshot(snap)
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

// inferInitialTierNote picks a non-authoritative display tier note when the user did not manually pick one.
// Hardware eligibility always comes from server-reported eligibleGpus/eligibleTpus when present.
func inferInitialTierNote(usage *colab.UsageSnapshot) core.SubscriptionTier {
	if usage == nil {
		return core.TierAIPro
	}
	if len(usage.EligibleAccelerators) > 0 {
		hasHigh := slices.Contains(usage.EligibleAccelerators, core.AccG4) || slices.Contains(usage.EligibleAccelerators, core.AccH100)
		hasPaid := hasHigh || slices.Contains(usage.EligibleAccelerators, core.AccA100) || slices.Contains(usage.EligibleAccelerators, core.AccL4)
		if hasHigh {
			if usage.Balance > 700 {
				return core.TierAIUltra
			}
			return core.TierColabProPlus
		}
		if hasPaid {
			return core.TierAIPro
		}
		if usage.Balance <= 0.01 {
			return core.TierFree
		}
		return core.TierAIPro
	}
	if usage.Balance <= 0.01 {
		return core.TierFree
	}
	return core.TierAIPro
}

func seedInitialRuntimeFromUsage(usage *colab.UsageSnapshot, sessions []core.ColabSessionItem, now time.Time) core.AccountRuntimeState {
	rt := core.NewAccountRuntimeState()
	if usage != nil {
		rt.CUBalance = usage.Balance
		rt.CURatePerHour = usage.RatePerHour
		rt.ActiveAssignments = usage.ActiveAssignments
		rt.Sessions = slices.Clone(sessions)
		ts := now
		rt.LastUsageFetchAt = &ts
	}
	return rt
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
			snap := s.cloneSettingsLocked()
			s.mu.Unlock()
			s.saveSettingsSnapshot(snap)
			s.RefreshAccount(existing.ID, true, true)
			return true
		}
	}
	s.mu.Unlock()

	usage, sessions, sessErr := s.Engine.FetchUsageAndSessions(context.Background(), probe)
	if sessErr != nil {
		sessions = nil
	}
	tier := inferInitialTierNote(usage)
	name := strings.Split(who.Email, "@")[0]
	if name == "" {
		name = "主账号"
	}
	detected := core.NewDefaultAccountProfile(name, who.Email, tier, core.AuthSystemDefaultOAuth2)
	detected.TierUserConfirmed = false
	if usage != nil && usage.EligibleKnown {
		detected.EligibleAccelerators = slices.Clone(usage.EligibleAccelerators)
		if detected.HasHighTierGPU() {
			detected.PreferredAccelerator = core.AccAutoBest
		} else if detected.IsFreeOnly() {
			detected.PreferredAccelerator = core.AccT4
		} else {
			detected.PreferredAccelerator = core.AccAutoPro
		}
		detected.HighMem = detected.SupportsHighMem()
		detected.Sanitize()
	}
	for _, sess := range sessions {
		if !sess.IsOrphan {
			detected.SessionName = core.SanitizeSessionName(sess.Name)
			break
		}
	}

	s.mu.Lock()
	for _, existing := range s.Settings.Accounts {
		if strings.ToLower(strings.TrimSpace(existing.Email)) == normEmail ||
			(existing.AuthMode == core.AuthSystemDefaultOAuth2 && existing.Email == "") {
			s.Settings.SelectedAccountID = existing.ID
			snap := s.cloneSettingsLocked()
			s.mu.Unlock()
			s.saveSettingsSnapshot(snap)
			s.RefreshAccount(existing.ID, true, true)
			return true
		}
	}
	s.Settings.Accounts = append(s.Settings.Accounts, detected)
	s.Settings.SelectedAccountID = detected.ID
	s.Runtime[detected.ID] = seedInitialRuntimeFromUsage(usage, sessions, time.Now())
	snap := s.cloneSettingsLocked()
	s.mu.Unlock()
	s.saveSettingsSnapshot(snap)

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

	userPickedTier := tier != ""
	initTier := tier
	if initTier == "" {
		initTier = core.TierAIPro
	}
	acc := core.NewDefaultAccountProfile(strings.TrimSpace(displayName), "", initTier, core.AuthIsolatedVaultADC)
	acc.TierUserConfirmed = userPickedTier
	if err := s.Vault.SaveADCCredentials(acc.ID, credJSON); err != nil {
		return err
	}
	if who, ok := s.Engine.Whoami(context.Background(), acc); ok {
		acc.Email = who.Email
		if strings.TrimSpace(displayName) == "" {
			acc.DisplayName = strings.Split(who.Email, "@")[0]
		}
	}
	var initUsage *colab.UsageSnapshot
	var initSessions []core.ColabSessionItem
	if usage, sessions, uErr := s.Engine.FetchUsageAndSessions(context.Background(), acc); uErr == nil && usage != nil {
		initUsage = usage
		initSessions = sessions
		if usage.EligibleKnown {
			acc.EligibleAccelerators = slices.Clone(usage.EligibleAccelerators)
			if acc.HasHighTierGPU() {
				acc.PreferredAccelerator = core.AccAutoBest
			} else if acc.IsFreeOnly() {
				acc.PreferredAccelerator = core.AccT4
			} else {
				acc.PreferredAccelerator = core.AccAutoPro
			}
			acc.HighMem = acc.SupportsHighMem()
		}
		if !userPickedTier {
			acc.Tier = inferInitialTierNote(usage)
		}
		acc.Sanitize()
	}

	s.mu.Lock()
	s.pendingOAuth = nil
	s.Settings.Accounts = append(s.Settings.Accounts, acc)
	s.Settings.SelectedAccountID = acc.ID
	s.Runtime[acc.ID] = seedInitialRuntimeFromUsage(initUsage, initSessions, time.Now())
	snap := s.cloneSettingsLocked()
	s.mu.Unlock()
	s.saveSettingsSnapshot(snap)

	go s.RefreshAccount(acc.ID, true, true)
	s.ShowToast("账号已安全绑定：" + acc.DisplayName)
	return nil
}

// Refresh & Account Polling

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

func (s *Service) RefreshAccount(accountID string, _, _ bool) {
	_ = s.refreshAccount(accountID, false)
}

func (s *Service) refreshAccount(accountID string, settleBusy bool) bool {
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
	firstFetch := false
	rtInit := s.Runtime[accountID]
	if rtInit.ContextLimit == 0 {
		rtInit = core.NewAccountRuntimeState()
	}
	if rtInit.LastUsageFetchAt == nil {
		firstFetch = true
	}
	rtInit.UsageFetching = true
	s.Runtime[accountID] = rtInit
	s.mu.Unlock()
	if firstFetch {
		s.notifyUI()
	}
	defer func() {
		s.mu.Lock()
		delete(s.refreshing, accountID)
		if cur, ok := s.Runtime[accountID]; ok {
			cur.UsageFetching = false
			s.Runtime[accountID] = cur
		}
		s.mu.Unlock()
		s.notifyUI()
	}()

	usage, sessions, err := s.Engine.FetchUsageAndSessions(context.Background(), acc)
	now := time.Now()

	var inFlight *core.HistoryRecord
	if s.History != nil {
		if rec, ok := s.History.InFlightByAccount()[accountID]; ok {
			cp := rec
			inFlight = &cp
		}
	}

	var updatedSnap *core.AppSettings
	s.mu.Lock()
	if err != nil {
		rt := s.Runtime[accountID]
		if rt.ContextLimit == 0 {
			rt = core.NewAccountRuntimeState()
		}
		rt.UsageFetching = false
		rt.LastError = err.Error()
		s.Runtime[accountID] = rt
		s.mu.Unlock()
		return false
	}
	if usage != nil && usage.EligibleKnown {
		for i := range s.Settings.Accounts {
			if s.Settings.Accounts[i].ID == accountID {
				if !slices.Equal(s.Settings.Accounts[i].EligibleAccelerators, usage.EligibleAccelerators) {
					s.Settings.Accounts[i].EligibleAccelerators = slices.Clone(usage.EligibleAccelerators)
					// Preserve user's explicit HighMem and legacy/confirmed Tier preferences;
					// only refresh hardware eligibility and re-sanitize legal targets.
					s.Settings.Accounts[i].Sanitize()
					acc = s.Settings.Accounts[i]
					snap := s.cloneSettingsLocked()
					updatedSnap = &snap
				}
				break
			}
		}
	}
	primaryEndpoint, isRunning, closedRec, nextInFlight := s.applyServerSnapshotLocked(accountID, acc, usage, sessions, inFlight, now, settleBusy, "")
	s.mu.Unlock()
	if updatedSnap != nil {
		s.saveSettingsSnapshot(*updatedSnap)
	}

	if closedRec != nil {
		s.recordHistory(*closedRec)
		if closedRec.ShutdownReason == core.ShutdownExternalOrLost || closedRec.ShutdownReason == core.ShutdownCapReached {
			hwLbl := strings.TrimSpace(closedRec.Hardware)
			if hwLbl == "" {
				hwLbl = "云端机器"
			}
			s.SendNotification("云端机器已被 Google 回收停机", fmt.Sprintf("%s 的 %s 已停止运行（非本软件关机），可在「记录」查看详情", acc.DisplayName, hwLbl), true)
		}
	}
	if nextInFlight != nil {
		s.upsertInFlight(*nextInFlight)
	}
	s.notifyUI()

	if isRunning {
		s.fetchAndApplyTelemetry(acc, primaryEndpoint)
	}
	return true
}
