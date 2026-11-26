//go:build !windows

package app

import (
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
	return cmd.Start()
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

// cleanupStaleColabSSH 仅精确清理匹配 colab.*ssh.*--proxy-mode.*-s <sessionName> 的本机残留进程，绝不误杀用户其他 SSH 隧道。
func cleanupStaleColabSSH(sessionName string) {
	if !core.IsValidSessionName(sessionName) {
		return
	}
	pat := regexp.MustCompile(`(?i)\bcolab\b.*\bssh\b.*--proxy-mode.*-s\s+['"]?` + regexp.QuoteMeta(sessionName) + `(['"\s]|$)`)
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
		if proc, err := os.FindProcess(pid); err == nil && proc != nil {
			_ = proc.Signal(syscall.SIGTERM)
		}
	}
}

func startSSHTunnelProcess(r *runner.OSRunner, profile core.AccountProfile, localPort, remotePort int, adcPath string) (*exec.Cmd, *tunnelStderrBuffer, error) {
	colabPath := "colab"
	if r != nil {
		if p := r.LookPath("colab"); p != "" {
			colabPath = p
		}
	}
	keyPath, _ := ensureLocalSSHKey()

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
		"-o", "ControlMaster=no",
		"-o", "ControlPath=none",
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
