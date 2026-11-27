//go:build windows

package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	scriptPath := filepath.Join(scriptDir, fmt.Sprintf("launch-%s.sh", uuid.New().String()[:8]))
	wslScript := runner.WindowsToWSLPath(scriptPath)
	body := fmt.Sprintf("#!/usr/bin/env bash\nrm -f %s\n%s\nexec bash\n", runner.ShellEscapePOSIX(wslScript), shellCmd)
	if err := os.WriteFile(scriptPath, []byte(body), 0o600); err != nil {
		return err
	}
	cmd := exec.Command("cmd.exe", "/c", "start", "Colab Tools Terminal", "wsl.exe", "--", "bash", wslScript)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func cleanupStaleColabSSH(_ core.AccountProfile) {}

func sshControlSocketPath(_, _ string) string { return "" }

func removeSSHControlSocket(_, _ string) {}

func canReuseSSHControlSocket(_ string) bool { return false }

func buildCloudTerminalShellCmd(r *runner.OSRunner, profile core.AccountProfile, _ string, _ bool, adcPath string) (string, bool) {
	args := []string{"colab"}
	if profile.AuthMode == core.AuthIsolatedVaultADC {
		cfg := profile.IsolatedConfigFilePath()
		if r == nil || r.UseWSLOnWindows {
			cfg = runner.WindowsToWSLPath(cfg)
		}
		args = append(args, "--auth=adc", "--config", cfg)
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
		if r == nil || r.UseWSLOnWindows {
			adcPath = runner.WindowsToWSLPath(adcPath)
		}
		escADC := runner.ShellEscapePOSIX(adcPath)
		cmd = fmt.Sprintf("export GOOGLE_APPLICATION_CREDENTIALS=%s && trap 'rm -f %s' EXIT && %s", escADC, escADC, cmd)
	}
	return cmd, false
}

func startSSHTunnelProcess(r *runner.OSRunner, profile core.AccountProfile, localPort, remotePort int, adcPath string) (*exec.Cmd, *tunnelStderrBuffer, error) {
	useWSL := r == nil || r.UseWSLOnWindows
	errBuf := &tunnelStderrBuffer{}
	if useWSL {
		proxyParts := []string{"colab"}
		if profile.AuthMode == core.AuthIsolatedVaultADC {
			proxyParts = append(proxyParts, "--auth=adc", "--config", runner.WindowsToWSLPath(profile.IsolatedConfigFilePath()))
		} else {
			proxyParts = append(proxyParts, "--auth=oauth2")
		}
		proxyParts = append(proxyParts, "ssh", "--proxy-mode", "-s", profile.SessionName)
		escapedProxy := make([]string, len(proxyParts))
		for i, a := range proxyParts {
			escapedProxy[i] = runner.ShellEscapePOSIX(a)
		}
		proxyCmd := strings.Join(escapedProxy, " ")

		sshParts := []string{
			"ssh", "-N",
			"-L", fmt.Sprintf("127.0.0.1:%d:127.0.0.1:%d", localPort, remotePort),
			"-o", "BatchMode=yes",
			"-o", "ConnectTimeout=15",
			"-o", "ControlMaster=no",
			"-o", "ControlPath=none",
			"-o", "ExitOnForwardFailure=yes",
			"-o", "StrictHostKeyChecking=no",
			"-o", "UserKnownHostsFile=/dev/null",
			"-o", "ServerAliveInterval=30",
			"-o", "ServerAliveCountMax=3",
			"-o", "ProxyCommand=" + proxyCmd,
			"root@colab",
		}
		escapedSSH := make([]string, len(sshParts))
		for i, a := range sshParts {
			escapedSSH[i] = runner.ShellEscapePOSIX(a)
		}
		extra := map[string]string{}
		if adcPath != "" {
			extra["GOOGLE_APPLICATION_CREDENTIALS"] = adcPath
		}
		distro := ""
		if r != nil {
			distro = r.WSLDistro
		}
		exe, wslArgs := runner.BuildWSLCommand(distro, strings.Join(escapedSSH, " "), extra)
		cmd := exec.Command(exe, wslArgs...)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		cmd.Stderr = errBuf
		if err := cmd.Start(); err != nil {
			return nil, errBuf, err
		}
		return cmd, errBuf, nil
	}

	// Native Windows OpenSSH + native colab.exe proxy-mode
	proxyParts := []string{"colab"}
	if profile.AuthMode == core.AuthIsolatedVaultADC {
		proxyParts = append(proxyParts, "--auth=adc", "--config", fmt.Sprintf(`"%s"`, profile.IsolatedConfigFilePath()))
	} else {
		proxyParts = append(proxyParts, "--auth=oauth2")
	}
	proxyParts = append(proxyParts, "ssh", "--proxy-mode", "-s", profile.SessionName)
	proxyCmd := strings.Join(proxyParts, " ")

	sshArgs := []string{
		"-N",
		"-L", fmt.Sprintf("127.0.0.1:%d:127.0.0.1:%d", localPort, remotePort),
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=15",
		"-o", "ControlMaster=no",
		"-o", "ControlPath=none",
		"-o", "ExitOnForwardFailure=yes",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=NUL",
		"-o", "ServerAliveInterval=30",
		"-o", "ServerAliveCountMax=3",
		"-o", "ProxyCommand=" + proxyCmd,
		"root@colab",
	}
	cmd := exec.Command("ssh.exe", sshArgs...)
	extra := map[string]string{}
	if adcPath != "" {
		extra["GOOGLE_APPLICATION_CREDENTIALS"] = adcPath
	}
	cmd.Env = runner.BaseEnvironment(extra)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Stderr = errBuf
	if err := cmd.Start(); err != nil {
		return nil, errBuf, err
	}
	return cmd, errBuf, nil
}
