//go:build !windows

package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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

func startSSHTunnelProcess(r *runner.OSRunner, profile core.AccountProfile, localPort, remotePort int, adcPath string) (*exec.Cmd, error) {
	colabPath := "colab"
	if r != nil {
		if p := r.LookPath("colab"); p != "" {
			colabPath = p
		}
	}
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
		"-o", "ExitOnForwardFailure=yes",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "ServerAliveInterval=30",
		"-o", "ServerAliveCountMax=3",
		"-o", "ProxyCommand=" + proxyCmd,
		"colab",
	}
	cmd := exec.Command("ssh", sshArgs...)
	extra := map[string]string{}
	if adcPath != "" {
		extra["GOOGLE_APPLICATION_CREDENTIALS"] = adcPath
	}
	cmd.Env = runner.BaseEnvironment(extra)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}
