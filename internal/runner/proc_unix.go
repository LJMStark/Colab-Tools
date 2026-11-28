//go:build !windows

package runner

import (
	"os/exec"
	"syscall"
	"time"
)

func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// DetachControllingTTY places cmd in a new POSIX session with no controlling TTY
// so interactive CLIs (e.g. `colab drivemount`) that probe /dev/tty fall back to stdin.
func DetachControllingTTY(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// KillProcessGroup terminates the POSIX process group headed by cmd.Process.Pid.
func KillProcessGroup(cmd *exec.Cmd) {
	killProcessGroup(cmd)
}

// killProcessGroup sends SIGTERM to the entire POSIX process group, waits briefly,
// then escalates to SIGKILL and confirms no members remain alive (ESRCH).
func killProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil || cmd.Process.Pid <= 0 {
		return
	}
	pgid := cmd.Process.Pid
	_ = syscall.Kill(-pgid, syscall.SIGTERM)

	termDeadline := time.Now().Add(1200 * time.Millisecond)
	for time.Now().Before(termDeadline) {
		if err := syscall.Kill(-pgid, 0); err == syscall.ESRCH {
			return
		}
		time.Sleep(40 * time.Millisecond)
	}

	_ = syscall.Kill(-pgid, syscall.SIGKILL)
	killDeadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(killDeadline) {
		if err := syscall.Kill(-pgid, 0); err == syscall.ESRCH {
			return
		}
		time.Sleep(40 * time.Millisecond)
	}
}
