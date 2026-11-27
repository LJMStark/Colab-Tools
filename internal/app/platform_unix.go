//go:build !windows

package app

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/LJMStark/Colab-Tools/internal/core"
	"github.com/LJMStark/Colab-Tools/internal/runner"
	"github.com/google/uuid"
)

func openTerminalCommand(shellCmd string) error {
	scriptDir := filepath.Join(core.ConfigDir(), "launchers")
	if err := os.MkdirAll(scriptDir, 0o700); err != nil {
		return err
	}
	scriptPath := filepath.Join(scriptDir, fmt.Sprintf("launch-%s.command", uuid.New().String()[:8]))
	body := fmt.Sprintf("#!/usr/bin/env bash\nrm -f %s\n%s\n", runner.ShellEscapePOSIX(scriptPath), shellCmd)
	if err := os.WriteFile(scriptPath, []byte(body), 0o700); err != nil {
		return err
	}
	cmd := exec.Command("open", "-a", "Terminal", scriptPath)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// ensureLocalSSHKey 检查本地 ~/.ssh 下是否有可用的密钥对；若均不存在则静默生成 ed25519 密钥。
func ensureLocalSSHKey() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return "", fmt.Errorf("无法定位用户主目录")
	}
	sshDir := filepath.Join(home, ".ssh")
	isPairReady := func(privPath string) bool {
		privInfo, err1 := os.Stat(privPath)
		pubInfo, err2 := os.Stat(privPath + ".pub")
		return err1 == nil && !privInfo.IsDir() && err2 == nil && !pubInfo.IsDir()
	}

	edPath := filepath.Join(sshDir, "id_ed25519")
	if isPairReady(edPath) {
		return edPath, nil
	}
	rsaPath := filepath.Join(sshDir, "id_rsa")
	if isPairReady(rsaPath) {
		return rsaPath, nil
	}

	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		return "", err
	}
	_ = os.Chmod(sshDir, 0o700)

	targetKey := edPath
	if _, err := os.Stat(edPath); err == nil {
		// 避免覆盖用户已有但缺少 .pub 的旧文件，改用独立文件名
		targetKey = filepath.Join(sshDir, "id_colab_tools_ed25519")
		if isPairReady(targetKey) {
			return targetKey, nil
		}
	}

	cmd := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", targetKey, "-C", "colab-tools")
	cmd.Stdin = nil
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("生成本机 SSH 密钥失败: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	_ = os.Chmod(targetKey, 0o600)
	_ = os.Chmod(targetKey+".pub", 0o644)
	return targetKey, nil
}

// cleanupStaleColabSSH 仅精确清理匹配当前账号身份（隔离 --config 或 --auth=oauth2）与会话名的本机残留 SSH 进程，绝不误杀其他账号或其他 SSH 隧道。
func cleanupStaleColabSSH(profile core.AccountProfile) {
	sessionName := profile.SessionName
	if !core.IsValidSessionName(sessionName) {
		return
	}
	pat := regexp.MustCompile(`(?i)\bcolab\b.*\bssh\b.*--proxy-mode.*-s\s+['"]?` + regexp.QuoteMeta(sessionName) + `(['"\s]|$)`)
	isoCfg := ""
	if profile.AuthMode == core.AuthIsolatedVaultADC {
		isoCfg = profile.IsolatedConfigFilePath()
	}
	out, err := exec.Command("ps", "-eo", "pid=,args=").Output()
	if err != nil {
		return
	}
	selfPID := os.Getpid()
	for _, rawLine := range strings.Split(string(out), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil || pid <= 1 || pid == selfPID {
			continue
		}
		argsStr := strings.TrimSpace(strings.TrimPrefix(line, fields[0]))
		if !pat.MatchString(argsStr) {
			continue
		}
		if isoCfg != "" {
			if !strings.Contains(argsStr, isoCfg) {
				continue
			}
		} else {
			if !strings.Contains(argsStr, "--auth=oauth2") || strings.Contains(argsStr, "--config") {
				continue
			}
		}
		if proc, err := os.FindProcess(pid); err == nil && proc != nil {
			_ = proc.Signal(syscall.SIGTERM)
		}
	}
}

func sshControlSocketPath(accountID, sessionName string) string {
	sum := sha256.Sum256([]byte(core.ConfigDir() + "|" + accountID + "|" + sessionName))
	return filepath.Join("/tmp", "ct-ssh-"+hex.EncodeToString(sum[:6])+".sock")
}

func removeSSHControlSocket(accountID, sessionName string) {
	p := sshControlSocketPath(accountID, sessionName)
	if p != "" {
		_ = os.Remove(p)
	}
}

func canReuseSSHControlSocket(sockPath string) bool {
	if sockPath == "" {
		return false
	}
	if _, err := os.Stat(sockPath); err != nil {
		return false
	}
	return exec.Command("ssh", "-S", sockPath, "-O", "check", "root@colab").Run() == nil
}

func buildCloudTerminalShellCmd(r *runner.OSRunner, profile core.AccountProfile, sockPath string, reuseSock bool, adcPath string) (string, bool) {
	if reuseSock && sockPath != "" {
		return fmt.Sprintf("ssh -t -S %s -o ControlMaster=no -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null root@colab",
			runner.ShellEscapePOSIX(sockPath)), true
	}

	colabPath := "colab"
	if r != nil {
		if p := r.LookPath("colab"); p != "" {
			colabPath = p
		}
	}
	args := []string{colabPath}
	if profile.AuthMode == core.AuthIsolatedVaultADC {
		args = append(args, "--auth=adc", "--config", profile.IsolatedConfigFilePath())
	} else {
		args = append(args, "--auth=oauth2")
	}
	args = append(args, "ssh", "-s", profile.SessionName)
	escaped := make([]string, len(args))
	for i, a := range args {
		escaped[i] = runner.ShellEscapePOSIX(a)
	}
	cmd := strings.Join(escaped, " ")
	if adcPath != "" {
		escADC := runner.ShellEscapePOSIX(adcPath)
		cmd = fmt.Sprintf("export GOOGLE_APPLICATION_CREDENTIALS=%s && trap 'rm -f %s' EXIT && %s", escADC, escADC, cmd)
	}
	return cmd, false
}

func startSSHTunnelProcess(r *runner.OSRunner, profile core.AccountProfile, localPort, remotePort int, adcPath string) (*exec.Cmd, *tunnelStderrBuffer, error) {
	colabPath := "colab"
	if r != nil {
		if p := r.LookPath("colab"); p != "" {
			colabPath = p
		}
	}
	keyPath, _ := ensureLocalSSHKey()
	removeSSHControlSocket(profile.ID, profile.SessionName)
	sockPath := sshControlSocketPath(profile.ID, profile.SessionName)

	proxyArgs := []string{colabPath}
	if profile.AuthMode == core.AuthIsolatedVaultADC {
		proxyArgs = append(proxyArgs, "--auth=adc", "--config", profile.IsolatedConfigFilePath())
	} else {
		proxyArgs = append(proxyArgs, "--auth=oauth2")
	}
	proxyArgs = append(proxyArgs, "ssh", "--proxy-mode", "-s", profile.SessionName)
	escapedProxy := make([]string, len(proxyArgs))
	for i, a := range proxyArgs {
		escapedProxy[i] = runner.ShellEscapePOSIX(a)
	}
	proxyCmd := strings.Join(escapedProxy, " ")

	// Delegate -N -L local port forwarding to local OpenSSH backed by `colab ssh --proxy-mode`
	// Session-isolated ControlMaster allows opening an interactive cloud terminal over the same single SSH session without HTTP 429.
	sshArgs := []string{
		"-N",
		"-L", fmt.Sprintf("127.0.0.1:%d:127.0.0.1:%d", localPort, remotePort),
	}
	if keyPath != "" {
		sshArgs = append(sshArgs, "-i", keyPath)
	}
	sshArgs = append(sshArgs,
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=15",
		"-o", "ControlMaster=yes",
		"-o", "ControlPath="+sockPath,
		"-o", "ControlPersist=no",
		"-o", "ExitOnForwardFailure=yes",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "ServerAliveInterval=30",
		"-o", "ServerAliveCountMax=3",
		"-o", "ProxyCommand="+proxyCmd,
		"root@colab",
	)
	cmd := exec.Command("ssh", sshArgs...)
	extra := map[string]string{}
	if adcPath != "" {
		extra["GOOGLE_APPLICATION_CREDENTIALS"] = adcPath
	}
	cmd.Env = runner.BaseEnvironment(extra)
	errBuf := &tunnelStderrBuffer{}
	cmd.Stderr = errBuf
	if err := cmd.Start(); err != nil {
		return nil, errBuf, err
	}
	return cmd, errBuf, nil
}
