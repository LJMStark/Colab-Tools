package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/LJMStark/Colab-Tools/internal/core"
	"github.com/LJMStark/Colab-Tools/internal/integrations"
	"github.com/LJMStark/Colab-Tools/internal/runner"
	"github.com/LJMStark/Colab-Tools/internal/vault"
	"github.com/google/uuid"
)

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
	apiKey, keyErr := s.Vault.LoadLLMAPIKey(accountID)
	ids := integrations.IdentifiersForAccount(accountID)
	var synced []string
	var errs []string

	if acc.AutoSyncCCSwitch {
		if keyErr != nil || strings.TrimSpace(apiKey) == "" {
			errs = append(errs, "CC Switch: 未能从凭据库读取 API 密钥")
		} else if _, err := integrations.SyncCCSwitch(acc.CCSwitchProviderID, acc.CCSwitchName, rt.PublicURL, apiKey, rt.ModelAlias, rt.ContextLimit, rt.ShortGPUName(), acc.DisplayName, "", ""); err != nil {
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
	apiKey, err := s.Vault.LoadLLMAPIKey(accountID)
	if err != nil || strings.TrimSpace(apiKey) == "" {
		s.ShowToast("未能从系统凭据库读取 API 密钥，请先确认大模型服务已完成部署后再试")
		return
	}
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
		raw, err := s.Vault.LoadADCCredentials(acc.ID)
		if err != nil || strings.TrimSpace(raw) == "" {
			return 0, nil, fmt.Errorf("无法读取该账号的隔离凭据（请去「账号」页重新授权）")
		}
		p, err := s.Vault.WriteEphemeralFile("ssh-adc", "json", []byte(raw))
		if err != nil || p == "" {
			return 0, nil, fmt.Errorf("无法创建临时凭据文件，已取消建立通道")
		}
		adcPath = p
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

func (s *Service) stopSSHTunnelIfMatches(accountID string, proc *exec.Cmd) {
	s.mu.Lock()
	if s.sshTunnels[accountID] != proc {
		s.mu.Unlock()
		return
	}
	var accSessionName string
	for _, a := range s.Settings.Accounts {
		if a.ID == accountID {
			accSessionName = a.SessionName
			break
		}
	}
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
	if proc != nil {
		runner.KillProcessGroup(proc)
	}
	if accSessionName != "" {
		removeSSHControlSocket(accountID, accSessionName)
	}
	if adc != "" {
		vault.ShredAndRemove(adc)
	}
	s.notifyUI()
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
	if proc != nil {
		runner.KillProcessGroup(proc)
	}
	if accSessionName != "" {
		removeSSHControlSocket(accountID, accSessionName)
	}
	if adc != "" {
		vault.ShredAndRemove(adc)
	}
	s.notifyUI()
}

func probeLocalComfyHTTP(port int) bool {
	client := &http.Client{Timeout: 3 * time.Second}
	for _, p := range []string{"/queue", "/"} {
		resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d%s", port, p))
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 500 {
				return true
			}
		}
	}
	return false
}

// establishLocalSSHTunnel unifies SSH tunnel launch, readiness probing, cleanup, and retries across ComfyUI and general WebUIs.
func (s *Service) establishLocalSSHTunnel(
	accountID string,
	maxAttempts int,
	waitTimeout time.Duration,
	retryPause time.Duration,
	requireHTTP bool,
) (int, string, error) {
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	var lastErrText string
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		port, errBuf, startErr := s.startSSHTunnelInternal(accountID)
		if errors.Is(startErr, errSSHTunnelStarting) {
			return 0, startErr.Error(), startErr
		}
		if startErr != nil {
			lastErrText = startErr.Error()
		} else {
			addr := fmt.Sprintf("127.0.0.1:%d", port)
			deadline := time.Now().Add(waitTimeout)
			for time.Now().Before(deadline) {
				select {
				case <-s.stopLoops:
					return 0, "", context.Canceled
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
					if !requireHTTP || probeLocalComfyHTTP(port) {
						return port, "", nil
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

		s.StopSSHTunnel(accountID)
		if attempt < maxAttempts {
			if strings.Contains(strings.ToLower(lastErrText), "already-active ssh session") || strings.Contains(lastErrText, "429") {
				s.ShowToast("检测到旧 SSH 连接占坑，正在自动清理并在几秒后重连…")
			} else {
				s.ShowToast("本机加密通道暂未连通，正在清理旧连接并自动重试…")
			}
			select {
			case <-time.After(retryPause):
			case <-s.stopLoops:
				return 0, "", context.Canceled
			}
		}
	}
	return 0, lastErrText, errors.New("tunnel unreachable")
}

// OpenComfyUI 一键通过本机加密 SSH 隧道直达 ComfyUI（严禁暴露无密码公网 cloudflared 链接）。
func (s *Service) OpenComfyUI(accountID string) {
	acc, ok := s.accountByID(accountID)
	if !ok {
		return
	}

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
	if !rt.ServerHealthy {
		s.ShowToast("正在为你自动安装 ComfyUI，装好后会自动弹出浏览器")
		go s.launchVideoDeployFlow(core.SessionEffectiveProfile(acc, rt))
		return
	}
	if rt.IsFreeRuntimeRestricted(acc) {
		svcCtx, cancelSvcCtx := s.serviceContext()
		defer cancelSvcCtx()
		colabURL := s.Engine.ResolveColabURL(svcCtx, acc, acc.SessionName)
		s.PresentActionPrompt(&core.UserActionPrompt{
			ID:            uuid.New().String(),
			Kind:          core.PromptComfyHelp,
			AccountID:     acc.ID,
			Title:         "免费版不支持本机加密通道",
			Message:       "Google 免费版不开放本机 SSH 隧道；为保护创作资产，本软件已禁用视频模式无密码公网链接。请点击下方打开 Colab 官方网页操作，或升级付费版使用一键私连。",
			URL:           colabURL,
			PrimaryLabel:  "打开 Colab 网页",
			PrimaryAction: "open_colab",
			CancelLabel:   "关闭",
		})
		s.ShowToast("免费版不支持本机加密通道，请前往 Colab 网页操作")
		return
	}

	if rt.SSHTunnelActive && rt.ActiveSSHTunnelPort != nil {
		existingPort := *rt.ActiveSSHTunnelPort
		if probeLocalComfyHTTP(existingPort) {
			if s.URLOpener != nil {
				s.URLOpener(fmt.Sprintf("http://127.0.0.1:%d", existingPort))
			}
			return
		}
		s.StopSSHTunnel(accountID)
	}

	port, lastErrText, err := s.establishLocalSSHTunnel(accountID, 2, waitTimeout, retryPause, true)
	if err == nil {
		if s.URLOpener != nil {
			s.URLOpener(fmt.Sprintf("http://127.0.0.1:%d", port))
		}
		return
	}
	if errors.Is(err, errSSHTunnelStarting) {
		s.ShowToast(err.Error())
		return
	}
	if errors.Is(err, context.Canceled) {
		return
	}

	svcCtx, cancelSvcCtx := s.serviceContext()
	defer cancelSvcCtx()
	colabURL := s.Engine.ResolveColabURL(svcCtx, acc, acc.SessionName)
	reasonHint := "本机加密通道两次尝试均未连通。"
	if strings.Contains(strings.ToLower(lastErrText), "already-active ssh session") || strings.Contains(lastErrText, "429") {
		reasonHint = "Google 检测到云端还有一个没关干净的旧 SSH 连接（通常等十几秒会自动释放）。"
	}
	s.PresentActionPrompt(&core.UserActionPrompt{
		ID:             uuid.New().String(),
		Kind:           core.PromptComfyHelp,
		AccountID:      acc.ID,
		Title:          "本机加密通道暂未连通",
		Message:        reasonHint + "请点击「重新连接本机通道」再试，或点击「重新部署」刷新云端画布，也可打开 Colab 官方网页查看。",
		URL:            colabURL,
		PrimaryLabel:   "重新连接本机通道",
		SecondaryLabel: "重新部署 ComfyUI",
		CancelLabel:    "关闭",
	})
	s.ShowToast("本机加密通道暂未连通，已弹出处理选项")
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
	sessionRecipe, _, _, sessionEnableLLM := rt.EffectiveSessionState(acc)
	if sessionRecipe.IsVideoComfy() && !sessionEnableLLM {
		go s.OpenComfyUI(accountID)
		return
	}
	if sessionEnableLLM {
		s.ShowToast("大模型代理模式请直接使用「同步工具」或「Claude 终端」")
		return
	}
	if rt.IsFreeRuntimeRestricted(acc) {
		s.ShowToast("免费版不支持本机加密通道，请前往 Colab 网页操作")
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

		port, errText, err := s.establishLocalSSHTunnel(accountID, 1, waitTimeout, 0, false)
		if err == nil {
			if s.URLOpener != nil {
				s.URLOpener(fmt.Sprintf("http://127.0.0.1:%d", port))
			}
			return
		}
		if errors.Is(err, errSSHTunnelStarting) {
			s.ShowToast(err.Error())
			return
		}
		if errors.Is(err, context.Canceled) {
			return
		}
		if strings.Contains(strings.ToLower(errText), "already-active ssh session") || strings.Contains(errText, "429") {
			s.ShowToast("检测到旧 SSH 连接占坑，已清理旧连接，请稍等十几秒后再点一次「打开 WebUI」")
		} else if errText != "" {
			s.ShowToast("WebUI 本机通道暂未连通：" + errText)
		} else {
			s.ShowToast("WebUI 本机通道暂未连通，请确认云端服务已在对应端口启动")
		}
	}()
}

func buildComfyViewURL(port int, item core.SessionVideoOutput) string {
	q := url.Values{}
	q.Set("filename", item.Filename)
	if item.Subfolder != "" {
		q.Set("subfolder", item.Subfolder)
	}
	outType := item.Type
	if outType == "" {
		outType = "output"
	}
	q.Set("type", outType)
	return fmt.Sprintf("http://127.0.0.1:%d/view?%s", port, q.Encode())
}

func sanitizeLocalOutputFilename(raw string) string {
	base := filepath.Base(strings.TrimSpace(raw))
	if base == "" || base == "." || base == ".." || strings.HasPrefix(base, ".") {
		return ""
	}
	base = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ':' || r < 32 {
			return '_'
		}
		return r
	}, base)
	return base
}

// publishUniqueTempFile atomically publishes an exclusive temp file to a unique non-clobbering filename in destDir.
// Uses os.Link so concurrent downloads across accounts can never overwrite each other's files.
func publishUniqueTempFile(tmpPath, destDir, safeName string) (string, error) {
	ext := filepath.Ext(safeName)
	stem := strings.TrimSuffix(safeName, ext)
	for i := 1; i < 1000; i++ {
		name := safeName
		if i > 1 {
			name = fmt.Sprintf("%s-%d%s", stem, i, ext)
		}
		target := filepath.Join(destDir, name)
		err := os.Link(tmpPath, target)
		if err == nil {
			_ = os.Remove(tmpPath)
			return target, nil
		}
		if errors.Is(err, os.ErrExist) {
			continue
		}
		// Fallback for filesystems without hard-link support: O_CREATE|O_EXCL copy
		f, fErr := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if errors.Is(fErr, os.ErrExist) {
			continue
		}
		if fErr != nil {
			return "", fErr
		}
		src, sErr := os.Open(tmpPath)
		if sErr != nil {
			_ = f.Close()
			_ = os.Remove(target)
			return "", sErr
		}
		_, cpErr := io.Copy(f, src)
		_ = src.Close()
		cErr := f.Close()
		if cpErr != nil || cErr != nil {
			_ = os.Remove(target)
			if cpErr != nil {
				return "", cpErr
			}
			return "", cErr
		}
		_ = os.Remove(tmpPath)
		return target, nil
	}
	return "", errors.New("下载目录同名文件过多，请先清理后再试")
}

func (s *Service) resolveLocalDownloadDir() (string, error) {
	if strings.TrimSpace(s.downloadBaseDir) != "" {
		if err := os.MkdirAll(s.downloadBaseDir, 0o755); err != nil {
			return "", err
		}
		return s.downloadBaseDir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return "", errors.New("无法定位本机下载目录")
	}
	dir := filepath.Join(home, "Downloads", "ColabTools")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func (s *Service) ensureLocalComfyPort(accountID string, acc core.AccountProfile, rt core.AccountRuntimeState) (int, error) {
	if rt.SSHTunnelActive && rt.ActiveSSHTunnelPort != nil && probeLocalComfyHTTP(*rt.ActiveSSHTunnelPort) {
		return *rt.ActiveSSHTunnelPort, nil
	}
	if rt.IsFreeRuntimeRestricted(acc) {
		return 0, errors.New("免费版不支持本机加密通道")
	}
	waitTimeout := s.comfyTunnelWaitTimeout
	if waitTimeout <= 0 {
		waitTimeout = 22 * time.Second
	}
	retryPause := s.comfyRetryPause
	if retryPause <= 0 {
		retryPause = 4 * time.Second
	}
	port, _, err := s.establishLocalSSHTunnel(accountID, 2, waitTimeout, retryPause, true)
	return port, err
}

// PreviewLatestSessionVideo opens the newest video produced in the current session via the encrypted local ComfyUI tunnel.
func (s *Service) PreviewLatestSessionVideo(accountID string) {
	acc, ok := s.accountByID(accountID)
	if !ok {
		return
	}
	rt := s.StateFor(accountID)
	if !rt.Phase.IsRunning() {
		s.ShowToast("机器未运行，请前往网盘或本机下载目录查看成片")
		return
	}
	if len(rt.SessionOutputs) == 0 {
		s.ShowToast("本次开机还没有可预览的新视频")
		return
	}
	port, err := s.ensureLocalComfyPort(accountID, acc, rt)
	if err != nil {
		s.ShowToast("本机加密通道暂未连通，请先点「开 ComfyUI」再试")
		return
	}
	if s.URLOpener != nil {
		s.URLOpener(buildComfyViewURL(port, rt.SessionOutputs[0]))
	}
}

// DownloadLatestSessionVideo downloads the newest video produced in the current session to ~/Downloads/ColabTools/.
func (s *Service) DownloadLatestSessionVideo(accountID string) {
	rt := s.StateFor(accountID)
	if len(rt.SessionOutputs) == 0 {
		s.ShowToast("本次开机还没有可下载的新视频")
		return
	}
	s.downloadSessionOutputs(accountID, []core.SessionVideoOutput{rt.SessionOutputs[0]})
}

// DownloadAllTempVideos enumerates ALL unsaved temporary videos on the cloud VM (no 10-item cap)
// and downloads every single one to ~/Downloads/ColabTools/, reporting exact success/failure counts.
func (s *Service) DownloadAllTempVideos(accountID string) {
	acc, ok := s.accountByID(accountID)
	if !ok {
		return
	}
	rt := s.StateFor(accountID)
	if !rt.Phase.IsRunning() {
		s.ShowToast("云端机器已停机，无法从临时盘下载")
		return
	}
	var targets []core.SessionVideoOutput
	if listed, err := s.Engine.ListRemoteTempVideos(context.Background(), acc); err == nil && len(listed) > 0 {
		targets = listed
	} else {
		for _, out := range rt.SessionOutputs {
			if !out.SavedToDrive {
				targets = append(targets, out)
			}
		}
		if len(targets) == 0 && len(rt.SessionOutputs) > 0 {
			targets = []core.SessionVideoOutput{rt.SessionOutputs[0]}
		}
	}
	if len(targets) == 0 {
		s.ShowToast("未检测到可直下的成片列表，正在为你打开 ComfyUI 手动保存")
		go s.OpenComfyUI(accountID)
		return
	}
	s.downloadSessionOutputs(accountID, targets)
}

func (s *Service) downloadSessionOutputs(accountID string, items []core.SessionVideoOutput) {
	acc, ok := s.accountByID(accountID)
	if !ok || len(items) == 0 {
		return
	}
	s.mu.Lock()
	rt := s.Runtime[accountID]
	if !rt.Phase.IsRunning() {
		s.mu.Unlock()
		s.ShowToast("云端机器已停机，无法从临时盘下载")
		return
	}
	if rt.DownloadingOutput {
		s.mu.Unlock()
		s.ShowToast("正在下载成片到本机，请稍候…")
		return
	}
	rt.DownloadingOutput = true
	s.Runtime[accountID] = rt
	s.mu.Unlock()
	s.notifyUI()

	defer func() {
		s.mu.Lock()
		if cur, exists := s.Runtime[accountID]; exists {
			cur.DownloadingOutput = false
			s.Runtime[accountID] = cur
		}
		s.mu.Unlock()
		s.notifyUI()
	}()

	destDir, err := s.resolveLocalDownloadDir()
	if err != nil {
		s.ShowToast("准备本机下载目录失败：" + err.Error())
		return
	}

	port, tunnelErr := s.ensureLocalComfyPort(accountID, acc, rt)
	httpClient := &http.Client{Timeout: 120 * time.Second}
	var savedPaths []string
	var failedNames []string
	var lastErr error
	totalRequested := 0

	for _, item := range items {
		safeName := sanitizeLocalOutputFilename(item.Filename)
		if safeName == "" {
			continue
		}
		totalRequested++
		downloaded := false
		var publishedPath string

		// 1. Prefer local encrypted SSH tunnel when item belongs to ComfyUI's active output directory
		isExplicitRoot := strings.HasPrefix(item.Type, "/")
		if !isExplicitRoot && tunnelErr == nil && port > 0 {
			viewURL := buildComfyViewURL(port, item)
			resp, err := httpClient.Get(viewURL)
			if err == nil {
				ct := strings.ToLower(resp.Header.Get("Content-Type"))
				if resp.StatusCode >= 200 && resp.StatusCode < 300 && !strings.Contains(ct, "text/html") {
					tmpF, fErr := os.CreateTemp(destDir, ".colab-dl-*.part")
					if fErr == nil {
						tmpPath := tmpF.Name()
						written, cpErr := io.Copy(tmpF, resp.Body)
						closeErr := tmpF.Close()
						if cpErr == nil && closeErr == nil && written > 0 {
							if pub, pubErr := publishUniqueTempFile(tmpPath, destDir, safeName); pubErr == nil {
								downloaded = true
								publishedPath = pub
							} else {
								_ = os.Remove(tmpPath)
								lastErr = pubErr
							}
						} else {
							_ = os.Remove(tmpPath)
							if cpErr != nil {
								lastErr = cpErr
							} else if closeErr != nil {
								lastErr = closeErr
							} else {
								lastErr = errors.New("云端返回空文件")
							}
						}
					} else {
						lastErr = fErr
					}
				} else {
					lastErr = fmt.Errorf("云端返回异常状态 (%d)", resp.StatusCode)
				}
				_ = resp.Body.Close()
			} else {
				lastErr = errors.New("本机通道读取失败")
			}
		}

		// 2. Reliable fallback via official `colab download` into an exclusive 0600 temp file
		if !downloaded {
			outBase := rt.EffectiveOutputDir
			if isExplicitRoot {
				outBase = item.Type
			} else if outBase == "" {
				if acc.UseGoogleDrive && rt.DriveActuallyMounted && acc.DriveOutputDir != "" {
					outBase = acc.DriveOutputDir
				} else {
					outBase = "/content/output"
				}
			}
			remotePath := path.Join(outBase, item.Subfolder, item.Filename)
			tmpF, tErr := os.CreateTemp(destDir, ".colab-cli-dl-*.part")
			if tErr != nil {
				lastErr = tErr
			} else {
				tmpPath := tmpF.Name()
				_ = tmpF.Close()
				if dlErr := s.Engine.DownloadRemoteFile(context.Background(), acc, remotePath, tmpPath); dlErr == nil {
					if st, stErr := os.Stat(tmpPath); stErr == nil && st.Size() > 0 {
						if pub, pubErr := publishUniqueTempFile(tmpPath, destDir, safeName); pubErr == nil {
							downloaded = true
							publishedPath = pub
						} else {
							_ = os.Remove(tmpPath)
							lastErr = pubErr
						}
					} else {
						_ = os.Remove(tmpPath)
						lastErr = errors.New("下载文件为空")
					}
				} else {
					_ = os.Remove(tmpPath)
					lastErr = dlErr
				}
			}
		}

		if downloaded && publishedPath != "" {
			savedPaths = append(savedPaths, publishedPath)
		} else {
			failedNames = append(failedNames, safeName)
		}
	}

	if len(savedPaths) == 0 {
		errMsg := "下载失败"
		if lastErr != nil {
			errMsg = lastErr.Error()
		}
		s.ShowToast("下载成片失败：" + errMsg)
		return
	}
	if len(failedNames) > 0 {
		s.ShowToast(fmt.Sprintf("仅成功保存 %d/%d 条，以下未保存：%s（暂勿关机）", len(savedPaths), totalRequested, strings.Join(failedNames, "、")))
	} else if len(savedPaths) == 1 {
		s.ShowToast("已下回本机：" + filepath.Base(savedPaths[0]))
	} else {
		s.ShowToast(fmt.Sprintf("已全部下回本机（%d/%d 条，位于 ~/Downloads/ColabTools）", len(savedPaths), totalRequested))
	}
	if s.revealFileFn != nil {
		s.revealFileFn(savedPaths[0])
	}
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
		rt := s.StateFor(acc.ID)
		if len(rt.SessionOutputs) > 0 && rt.SessionOutputs[0].SavedToDrive && strings.TrimSpace(rt.SessionOutputs[0].Filename) != "" {
			queryTerm = strings.TrimSpace(rt.SessionOutputs[0].Filename)
		} else if dir := strings.TrimSpace(acc.DriveOutputDir); dir != "" {
			cleaned := path.Clean(strings.ReplaceAll(dir, `\`, "/"))
			base := path.Base(cleaned)
			if base != "" && base != "." && base != "/" {
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

func (s *Service) OpenColabWebHome(accountID string) {
	acc, ok := s.accountByID(accountID)
	if !ok {
		return
	}
	target := "https://colab.research.google.com/"
	if email := strings.TrimSpace(acc.Email); email != "" {
		target += "?authuser=" + url.QueryEscape(email)
	}
	s.mu.RLock()
	opener := s.URLOpener
	s.mu.RUnlock()
	if opener != nil {
		opener(target)
	}
}

func (s *Service) OpenColabInBrowser(accountID string) {
	acc, ok := s.accountByID(accountID)
	if !ok {
		return
	}
	go func() {
		if u := s.Engine.ResolveColabURL(context.Background(), acc, acc.SessionName); u != "" {
			s.mu.RLock()
			opener := s.URLOpener
			s.mu.RUnlock()
			if opener != nil {
				opener(u)
				return
			}
		}
		s.OpenColabWebHome(accountID)
	}()
}

func (s *Service) OpenColabPayPage() {
	if s.URLOpener != nil {
		s.URLOpener("https://colab.research.google.com/signup")
	}
}
