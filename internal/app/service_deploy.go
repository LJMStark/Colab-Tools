package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/LJMStark/Colab-Tools/internal/core"
	"github.com/LJMStark/Colab-Tools/scripts/templates"
	"github.com/google/uuid"
)

// Post-Allocation Dispatch, Interactive Drive Mount, Video/LLM Deploy & Logs

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
	if !s.refreshAccount(acc.ID, true) {
		usage, sessions, err := s.Engine.FetchUsageAndSessions(context.Background(), acc)
		if err == nil {
			var inFlight *core.HistoryRecord
			if s.History != nil {
				if rec, ok := s.History.InFlightByAccount()[acc.ID]; ok {
					cp := rec
					inFlight = &cp
				}
			}
			s.mu.Lock()
			ep, running, closedRec, nextInFlight := s.applyServerSnapshotLocked(acc.ID, acc, usage, sessions, inFlight, time.Now(), true, "")
			s.mu.Unlock()
			if closedRec != nil {
				s.recordHistory(*closedRec)
			}
			if nextInFlight != nil {
				s.upsertInFlight(*nextInFlight)
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

	if acc.EnableLLMModule {
		s.launchLLMDeployFlow(acc)
		return
	}

	if acc.DeployRecipe == core.RecipeNone && strings.TrimSpace(acc.DriveScriptPath) != "" {
		svcCtx, cancelSvcCtx := s.serviceContext()
		mounted, reason := s.runInteractiveDriveMountWithPrompt(svcCtx, acc, "", "", false)
		cancelSvcCtx()
		if mounted {
			ok, _, errMsg := s.Engine.RunDriveStartScript(context.Background(), acc)
			if ok {
				_ = s.refreshAccount(acc.ID, true)
			} else if errMsg != "" {
				s.ShowToast("远端启动脚本提示：" + errMsg)
			}
		} else if reason != "" {
			s.ShowToast(reason)
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
	} else if acc.EnableLLMModule {
		go s.launchLLMDeployFlow(acc)
	} else {
		s.ShowToast("机器已在运行中")
	}
}

func (s *Service) bringMainWindowToFront() {
	s.mu.RLock()
	openFn := s.OnOpenMainWin
	onboarding := s.ShowingOnboarding
	s.mu.RUnlock()
	if openFn != nil && !onboarding {
		openFn(SectionHome, false)
	}
}

func driveAuthAccountLabel(acc core.AccountProfile) string {
	name := strings.TrimSpace(acc.DisplayName)
	email := strings.TrimSpace(acc.Email)
	switch {
	case name != "" && email != "":
		return fmt.Sprintf("%s（%s）", name, email)
	case email != "":
		return email
	case name != "":
		return name
	default:
		return "当前 Google 账号"
	}
}

func (s *Service) needsInteractiveDriveOnStart(acc core.AccountProfile) bool {
	if acc.DeployRecipe.IsVideoComfy() || acc.EnableLLMModule {
		return acc.UseGoogleDrive
	}
	return acc.WorkloadKind != core.WorkloadGeneral && strings.TrimSpace(acc.DriveScriptPath) != ""
}

func (s *Service) clearDriveAuthPromptLocked(accountID string) {
	if mh, ok := s.driveMountCancels[accountID]; ok {
		if mh.cancel != nil {
			mh.cancel()
		}
		delete(s.driveMountCancels, accountID)
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

func (s *Service) driveAuthSecondaryLabel(acc core.AccountProfile, isRemount bool) string {
	if isRemount {
		return "取消挂载"
	}
	if acc.EnableLLMModule {
		return "跳过挂载，用内置脚本启动"
	}
	if !acc.DeployRecipe.IsVideoComfy() {
		return "跳过挂载（不跑 Drive 脚本）"
	}
	return "先不挂，纯云端跑"
}

func (s *Service) buildDriveAuthPreparingPrompt(acc core.AccountProfile, startingMachine, isRemount bool) *core.UserActionPrompt {
	step1 := "第 1 步（进行中）：正在连接云端机器获取授权链接，就绪后会自动拉起浏览器…"
	if startingMachine {
		step1 = "第 1 步（进行中）：正在申请云端机器并准备授权链接（约 15 秒），就绪后会自动拉起浏览器…"
	}
	intro := "Google 每次新开机挂载云盘都需确认一次授权，请先别离开电脑："
	if isRemount {
		intro = "重新挂载 Google Drive 需确认一次授权，请先别离开电脑："
	} else if acc.EnableLLMModule {
		intro = "挂载 Google Drive 可复用已下载的模型缓存或自定义启动脚本（跳过也会自动用内置脚本启动）："
	} else if !acc.DeployRecipe.IsVideoComfy() {
		intro = "挂载 Google Drive 后才会自动执行云盘上的启动脚本，请先别离开电脑："
	}
	msg := fmt.Sprintf(
		"授权账号：%s\n%s\n%s\n第 2 步：在自动弹出的浏览器页面核对上方账号并点击「允许 / Allow」。\n第 3 步：网页提示授权完成后，回到这里点击「我已点完允许，继续」。",
		driveAuthAccountLabel(acc),
		intro,
		step1,
	)
	return &core.UserActionPrompt{
		ID:             uuid.New().String(),
		Kind:           core.PromptDriveAuth,
		AccountID:      acc.ID,
		Title:          "请稍留片刻：即将自动打开浏览器授权 Google Drive",
		Message:        msg,
		SecondaryLabel: s.driveAuthSecondaryLabel(acc, isRemount),
		CancelLabel:    "收起提醒",
	}
}

func (s *Service) presentDriveAuthPreparingPrompt(acc core.AccountProfile, startingMachine bool) {
	prompt := s.buildDriveAuthPreparingPrompt(acc, startingMachine, false)
	s.mu.Lock()
	if s.actionPrompt != nil && !(s.actionPrompt.Kind == core.PromptDriveAuth && s.actionPrompt.AccountID == acc.ID && s.driveAuthResultCh == nil) {
		s.mu.Unlock()
		return
	}
	s.actionPrompt = prompt
	s.mu.Unlock()
	s.bringMainWindowToFront()
	s.notifyUI()
}

func (s *Service) canOccupyDriveAuthPromptLocked(accountID string) bool {
	if s.actionPrompt == nil {
		return true
	}
	return s.actionPrompt.Kind == core.PromptDriveAuth && s.actionPrompt.AccountID == accountID
}

func (s *Service) runInteractiveDriveMountWithPrompt(
	parentCtx context.Context,
	acc core.AccountProfile,
	flowToken string,
	expectedEndpoint string,
	isRemount bool,
) (bool, string) {
	mountCtx, cancelMount := context.WithCancel(parentCtx)
	mountToken := uuid.New().String()
	prepPrompt := s.buildDriveAuthPreparingPrompt(acc, false, isRemount)
	var prevCancel context.CancelFunc
	shownPrep := false

	s.mu.Lock()
	if !isRemount && s.skipNextDriveMount[acc.ID] {
		delete(s.skipNextDriveMount, acc.ID)
		if s.actionPrompt != nil && s.actionPrompt.Kind == core.PromptDriveAuth && s.actionPrompt.AccountID == acc.ID && s.driveAuthResultCh == nil {
			s.actionPrompt = nil
		}
		s.mu.Unlock()
		cancelMount()
		s.notifyUI()
		return false, "已跳过 Google Drive，直接纯云端运行"
	}
	if prev, ok := s.driveMountCancels[acc.ID]; ok && prev.cancel != nil {
		prevCancel = prev.cancel
	}
	s.driveMountCancels[acc.ID] = deployWatchHandle{token: mountToken, cancel: cancelMount}
	if s.actionPrompt == nil || (s.actionPrompt.Kind == core.PromptDriveAuth && s.actionPrompt.AccountID == acc.ID && s.driveAuthResultCh == nil) {
		s.actionPrompt = prepPrompt
		shownPrep = true
	}
	s.mu.Unlock()

	if prevCancel != nil {
		prevCancel()
	}
	if shownPrep {
		s.bringMainWindowToFront()
		s.notifyUI()
	}

	defer func() {
		cancelMount()
		cleared := false
		s.mu.Lock()
		if cur, ok := s.driveMountCancels[acc.ID]; ok && cur.token == mountToken {
			delete(s.driveMountCancels, acc.ID)
			if s.actionPrompt != nil && s.actionPrompt.Kind == core.PromptDriveAuth && s.actionPrompt.AccountID == acc.ID {
				s.actionPrompt = nil
				cleared = true
				if ch := s.driveAuthResultCh; ch != nil {
					s.driveAuthResultCh = nil
					select {
					case ch <- false:
					default:
					}
				}
			}
		}
		s.mu.Unlock()
		if cleared {
			s.notifyUI()
		}
	}()

	title := "需要你授权 Google Drive（已自动打开浏览器）"
	secondaryLabel := s.driveAuthSecondaryLabel(acc, isRemount)
	cancelLabel := "取消"
	intro := "请按以下 3 步完成授权，完成后模型、工作流与新成片将自动连接 Google Drive："
	if isRemount {
		title = "重新授权 Google Drive（已自动打开浏览器）"
		cancelLabel = "关闭"
		intro = "请按以下 3 步重新挂载 Google Drive（挂载成功后如需让运行中的 ComfyUI 切换输出目录，请再点「重新部署」）："
	} else if acc.EnableLLMModule {
		intro = "请按以下 3 步完成授权以复用云盘缓存或自定义脚本（若跳过将直接以内置脚本纯云端启动）："
	} else if !acc.DeployRecipe.IsVideoComfy() {
		intro = "请按以下 3 步完成授权，完成后将继续运行云盘上的启动脚本（若跳过将不执行该脚本）："
	}

	mounted, reason := s.Engine.EnsureDriveMountedInteractive(mountCtx, acc, func(authURL string) bool {
		if strings.TrimSpace(authURL) == "" {
			return false
		}
		ch := make(chan bool, 1)
		var opener func(string)
		for {
			s.mu.Lock()
			if flowToken != "" && (!s.isDeployFlowOwnerLocked(acc.ID, flowToken, expectedEndpoint) || parentCtx.Err() != nil) {
				s.mu.Unlock()
				return false
			}
			curHandle, ownsMount := s.driveMountCancels[acc.ID]
			if !ownsMount || curHandle.token != mountToken || mountCtx.Err() != nil {
				s.mu.Unlock()
				return false
			}
			if !s.canOccupyDriveAuthPromptLocked(acc.ID) {
				s.mu.Unlock()
				select {
				case <-time.After(200 * time.Millisecond):
					continue
				case <-mountCtx.Done():
					return false
				case <-s.stopLoops:
					return false
				}
			}
			if oldCh := s.driveAuthResultCh; oldCh != nil {
				select {
				case oldCh <- false:
				default:
				}
			}
			s.driveAuthResultCh = ch
			msg := fmt.Sprintf(
				"授权账号：%s\n%s\n第 1 步：切换到刚自动打开的浏览器页面（如未弹出请点下方蓝色按钮）。\n第 2 步：确认页面登录的是上方 Google 账号，点击「允许 / Allow」。\n第 3 步：网页提示授权成功后，切回这里点击「我已点完允许，继续」。",
				driveAuthAccountLabel(acc),
				intro,
			)
			s.actionPrompt = &core.UserActionPrompt{
				ID:             uuid.New().String(),
				Kind:           core.PromptDriveAuth,
				AccountID:      acc.ID,
				Title:          title,
				Message:        msg,
				URL:            authURL,
				PrimaryLabel:   "我已点完允许，继续",
				SecondaryLabel: secondaryLabel,
				CancelLabel:    cancelLabel,
			}
			opener = s.URLOpener
			s.mu.Unlock()
			break
		}

		s.bringMainWindowToFront()
		s.notifyUI()
		if opener != nil {
			opener(authURL)
		}
		s.SendNotification("请到浏览器点击「允许」挂载 Google Drive", fmt.Sprintf("授权账号：%s。点完允许后回到 Colab Tools 点「我已点完允许，继续」", driveAuthAccountLabel(acc)), true)

		if acc.QueuePersistentMode && !isRemount {
			autoSkip := time.NewTimer(90 * time.Second)
			defer autoSkip.Stop()
			select {
			case proceed := <-ch:
				return proceed
			case <-autoSkip.C:
				s.ShowToast("持续守候 90 秒内未确认 Drive 授权，已自动切纯云端继续运行（请记得手动下载成片）")
				return false
			case <-mountCtx.Done():
				return false
			case <-s.stopLoops:
				return false
			}
		}
		select {
		case proceed := <-ch:
			return proceed
		case <-mountCtx.Done():
			return false
		case <-s.stopLoops:
			return false
		}
	})
	if !mounted {
		s.mu.Lock()
		skippedDuringPrep := s.skipNextDriveMount[acc.ID]
		delete(s.skipNextDriveMount, acc.ID)
		s.mu.Unlock()
		if skippedDuringPrep {
			return false, "已跳过 Google Drive，直接纯云端运行"
		}
	}
	return mounted, reason
}

func (s *Service) cancelDeployFlowLocked(accountID string) {
	if fh, ok := s.deployFlowCancels[accountID]; ok {
		if fh.cancel != nil {
			fh.cancel()
		}
		delete(s.deployFlowCancels, accountID)
	}
	s.clearDriveAuthPromptLocked(accountID)
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

func (s *Service) startDeployProgressWatcher(accountID string) {
	s.cancelDeployWatcher(accountID)
	watchToken := uuid.New().String()
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.deployWatchCancels[accountID] = deployWatchHandle{token: watchToken, cancel: cancel}
	s.mu.Unlock()

	go func() {
		defer s.clearDeployWatcherIfToken(accountID, watchToken)
		firstCheck := time.NewTimer(4 * time.Second)
		defer firstCheck.Stop()
		tk := time.NewTicker(12 * time.Second)
		defer tk.Stop()
		deadline := time.Now().Add(18 * time.Minute)
		pollOnce := func() bool {
			latestAcc, ok := s.accountByID(accountID)
			if !ok {
				return true
			}
			rt := s.StateFor(accountID)
			if !rt.Phase.IsRunning() || rt.ActiveEndpoint == "" {
				return true
			}
			s.fetchAndApplyTelemetry(latestAcc, rt.ActiveEndpoint)
			after := s.StateFor(accountID)
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

func (s *Service) launchVideoDeployFlow(acc core.AccountProfile) {
	envVars, err := core.BuildDeployEnv(acc)
	if err != nil {
		s.ShowToast(err.Error())
		return
	}

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
	cur.SessionIsVideoComfy = true
	s.Runtime[acc.ID] = cur
	s.mu.Unlock()
	defer s.clearDeployFlowIfToken(acc.ID, flowToken)
	s.notifyUI()

	if acc.UseGoogleDrive {
		s.ShowToast("正在挂载 Google Drive（复用模型并自动存成片）…")
		mounted, reason := s.runInteractiveDriveMountWithPrompt(flowCtx, acc, flowToken, expectedEndpoint, false)
		s.mu.Lock()
		stillValid := s.isDeployFlowOwnerLocked(acc.ID, flowToken, expectedEndpoint) && flowCtx.Err() == nil
		if !stillValid {
			s.mu.Unlock()
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
		s.mu.Unlock()
		return
	}
	if st, ok := s.Runtime[acc.ID]; ok {
		st.WorkloadMetricValue = "8%"
		st.WorkloadMetricUnit = "推送脚本"
		r8 := 0.08
		st.WorkloadMetricRatio = &r8
		st.WorkloadDetail = fmt.Sprintf("正在向云端推送 %s 自动安装脚本…", recipeLabelForSession(acc, false))
		s.Runtime[acc.ID] = st
	}
	s.mu.Unlock()
	s.notifyUI()

	if err := s.Engine.StartBuiltinDeployScript(flowCtx, acc, templates.ComfyUIDeployScript, envVars); err != nil {
		s.handleDeployPushFailure(acc, flowToken, expectedEndpoint, flowCtx, err)
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
	cur.WorkloadDetail = fmt.Sprintf("已启动云端部署 %s，正在安装环境…", recipeLabelForSession(acc, false))
	cur.AutoOpenedEndpoint = ""
	s.Runtime[acc.ID] = cur
	s.mu.Unlock()
	s.clearDeployFlowIfToken(acc.ID, flowToken)
	s.notifyUI()
	s.ShowToast("已把部署脚本送到云端，可直接在主屏看到实时进度或点「查看日志」")

	s.startDeployProgressWatcher(acc.ID)
}

// launchLLMDeployFlow starts an OpenAI/Anthropic-compatible llama.cpp proxy:
// prefers a user's custom Drive script when present, otherwise pushes the built-in generic LLMProxyScript.
func (s *Service) launchLLMDeployFlow(acc core.AccountProfile) {
	s.mu.Lock()
	cur := s.Runtime[acc.ID]
	if !cur.Phase.IsRunning() {
		s.mu.Unlock()
		return
	}
	if cur.DeployRunning {
		s.mu.Unlock()
		s.ShowToast("大模型服务正在部署中，可点「查看日志」跟踪实时进度")
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
	cur.WorkloadMetricUnit = "大模型代理"
	r5 := 0.05
	cur.WorkloadMetricRatio = &r5
	cur.WorkloadDetail = "正在准备云端大模型代理环境…"
	cur.AutoOpenedEndpoint = ""
	s.Runtime[acc.ID] = cur
	s.mu.Unlock()
	defer s.clearDeployFlowIfToken(acc.ID, flowToken)
	s.notifyUI()

	mounted := false
	if acc.UseGoogleDrive {
		var reason string
		mounted, reason = s.runInteractiveDriveMountWithPrompt(flowCtx, acc, flowToken, expectedEndpoint, false)
		s.mu.Lock()
		stillValid := s.isDeployFlowOwnerLocked(acc.ID, flowToken, expectedEndpoint) && flowCtx.Err() == nil
		if !stillValid {
			s.mu.Unlock()
			return
		}
		if st, ok := s.Runtime[acc.ID]; ok {
			st.DriveMountedKnown = true
			st.DriveActuallyMounted = mounted
			s.Runtime[acc.ID] = st
		}
		s.mu.Unlock()
		s.notifyUI()
		if !mounted && reason != "" {
			s.ShowToast(reason)
		}
	}

	// If user placed a custom start_server.sh on Drive, honor it first.
	customScript := strings.TrimSpace(acc.DriveScriptPath)
	if mounted && customScript != "" && s.Engine.RemoteFileExists(flowCtx, acc, customScript) {
		ok, _, errMsg := s.Engine.RunDriveStartScript(flowCtx, acc)
		s.mu.Lock()
		if !s.isDeployFlowOwnerLocked(acc.ID, flowToken, expectedEndpoint) {
			s.mu.Unlock()
			return
		}
		if st, exists := s.Runtime[acc.ID]; exists {
			st.DeployPreLaunch = false
			st.DeployRunning = false
			st.DeployStartingUntil = time.Time{}
			if !ok {
				st.DeployFailed = true
				st.DeployError = errMsg
			}
			s.Runtime[acc.ID] = st
		}
		s.mu.Unlock()
		s.clearDeployFlowIfToken(acc.ID, flowToken)
		if ok {
			if key := s.Engine.FetchRemoteAPIKeySafely(context.Background(), acc); key != "" {
				_ = s.Vault.SaveLLMAPIKey(acc.ID, key)
			}
			_ = s.refreshAccount(acc.ID, true)
			s.SyncAllEnabledClients(acc.ID, true)
		} else if errMsg != "" {
			s.ShowToast("远端启动脚本提示：" + errMsg)
		}
		s.notifyUI()
		return
	}

	// Otherwise launch built-in generic LLM proxy script (zero manual Drive upload required).
	envVars := core.BuildLLMDeployEnv(acc)
	if err := s.Engine.StartBuiltinDeployScript(flowCtx, acc, templates.LLMProxyScript, envVars); err != nil {
		s.handleDeployPushFailure(acc, flowToken, expectedEndpoint, flowCtx, err)
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
	cur.WorkloadMetricUnit = "拉取引擎"
	r10 := 0.10
	cur.WorkloadMetricRatio = &r10
	cur.WorkloadDetail = "已推送内置大模型代理脚本，就绪后自动同步客户端…"
	s.Runtime[acc.ID] = cur
	s.mu.Unlock()
	s.clearDeployFlowIfToken(acc.ID, flowToken)
	s.notifyUI()
	s.ShowToast("已启动内置大模型代理部署，可查看主屏进度或日志")

	s.startDeployProgressWatcher(acc.ID)
}

func (s *Service) handleDeployPushFailure(acc core.AccountProfile, flowToken, expectedEndpoint string, flowCtx context.Context, err error) {
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
}

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
	mounted, reason := s.runInteractiveDriveMountWithPrompt(svcCtx, acc, "", "", true)
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
