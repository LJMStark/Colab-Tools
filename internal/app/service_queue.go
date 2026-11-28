package app

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/LJMStark/Colab-Tools/internal/core"
	"github.com/google/uuid"
)

// Polite Queue, Allocation & Error Recovery

func (s *Service) StartAccountSession(accountID string) {
	s.startAccountSession(accountID, true)
}

func (s *Service) startAccountSession(accountID string, interactive bool) {
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
	if s.ActiveQueueAccountID != "" && s.ActiveQueueAccountID != accountID {
		s.mu.Unlock()
		s.ShowToast("已有其他账号在排队拿卡中（单通道防风控）")
		return
	}
	delete(s.skipNextDriveMount, accountID)

	// 开启持续守候时自动补开空闲自动关机，防止半夜抢到后无人看管烧光点数
	var persistSnap *core.AppSettings
	if acc.QueuePersistentMode && !acc.AutoShutdownEnabled {
		acc.AutoShutdownEnabled = true
		if acc.IdleTimeoutMinutes < 5 {
			acc.IdleTimeoutMinutes = 40
		}
		s.Settings.Accounts[accIdx] = acc
		snap := s.cloneSettingsLocked()
		persistSnap = &snap
	}

	rt.LastError = ""
	rt.LastErrorTitle = ""
	rt.LastErrorAction = ""
	rt.LastErrorRaw = ""
	rt.LastAllocHadShortage = false

	showPreDrivePrompt := interactive && s.needsInteractiveDriveOnStart(acc) && !acc.QueuePersistentMode
	warnPersistentDrive := interactive && acc.QueuePersistentMode && s.needsInteractiveDriveOnStart(acc)

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

	if persistSnap != nil {
		s.saveSettingsSnapshot(*persistSnap)
	}
	s.notifyUI()
	if showPreDrivePrompt {
		s.presentDriveAuthPreparingPrompt(acc, true)
	} else if warnPersistentDrive {
		s.ShowToast("持续守候已启动：抢到卡后需在 90 秒内点浏览器「允许」挂网盘，若不在电脑前将自动切纯云端运行")
	}
	go s.runPoliteQueue(ctx, acc, token)
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
						go s.refreshAccount(acc.ID, true)
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

		for v := range res.UnsupportedValues {
			pruned[v] = true
		}

		if res.Outcome.Kind == core.OutcomeAllocated {
			if !owned || ctx.Err() != nil {
				// Queue was cancelled while `colab new` was in flight: sync live instance so billing is visible, but skip auto-deploy.
				_ = s.refreshAccount(acc.ID, true)
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
	delete(s.skipNextDriveMount, accountID)
	s.clearDriveAuthPromptLocked(accountID)
	s.mu.Unlock()

	if cancelRec != nil {
		s.recordHistory(*cancelRec)
	}
	if h.cancel != nil {
		h.cancel()
	}
	s.ShowToast("已取消排队")
	if inFlight {
		go s.refreshAccount(accountID, true)
	}
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
	var persistSnap *core.AppSettings
	if outcome.Kind == core.OutcomeRateLimited {
		until := time.Now().Add(core.QueueCircuitBreakerCooldown)
		rt.QueueCircuitBreakTill = &until
		if s.Settings.QueueCooldownsByAccount == nil {
			s.Settings.QueueCooldownsByAccount = map[string]time.Time{}
		}
		s.Settings.QueueCooldownsByAccount[acc.ID] = until
		snap := s.cloneSettingsLocked()
		persistSnap = &snap
	}
	s.Runtime[acc.ID] = rt
	delete(s.skipNextDriveMount, acc.ID)
	s.clearDriveAuthPromptLocked(acc.ID)
	s.mu.Unlock()
	if persistSnap != nil {
		s.saveSettingsSnapshot(*persistSnap)
	}
	s.notifyUI()

	if outcome.Kind == core.OutcomePrecondition412 {
		go s.refreshAccount(acc.ID, true)
	}

	s.ShowToast(outcome.Message)
	notifyBody := outcome.Message
	if strings.TrimSpace(notifyBody) == "" {
		notifyBody = "请打开主控制台查看详细原因"
	}
	s.SendNotification(notifyTitle, notifyBody, true)
}

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
	snap := s.cloneSettingsLocked()
	s.mu.Unlock()
	s.saveSettingsSnapshot(snap)
	s.notifyUI()
	s.ShowToast("已解除等待状态，现在可以直接重试")
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
