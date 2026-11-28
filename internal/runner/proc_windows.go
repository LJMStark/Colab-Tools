//go:build windows

package runner

import (
	"context"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
}

// DetachControllingTTY hides the console window and isolates cmd in its own process group.
func DetachControllingTTY(cmd *exec.Cmd) {
	configureProcessGroup(cmd)
}

// KillProcessGroup terminates the entire Windows process tree rooted at cmd.
func KillProcessGroup(cmd *exec.Cmd) {
	killProcessGroup(cmd)
}

// killProcessGroup terminates the entire Windows process tree (/T /F) so spawned wsl.exe/shell children never leak.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil || cmd.Process.Pid <= 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	killCmd := exec.CommandContext(ctx, "taskkill.exe", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
	killCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	_ = killCmd.Run()
	_ = cmd.Process.Kill()
}
