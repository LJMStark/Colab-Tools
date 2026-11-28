package runner

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const MaxPipeBytes = 2 * 1024 * 1024

type Result struct {
	ExitCode int
	Stdout   string
	Stderr   string
	TimedOut bool
	Canceled bool
}

type RunOptions struct {
	Stdin    string
	ExtraEnv map[string]string
	Timeout  time.Duration
}

type Runner interface {
	Run(ctx context.Context, name string, args []string, opts RunOptions) Result
	RunShell(ctx context.Context, shellCmd string, opts RunOptions) Result
	RunStreamingShell(ctx context.Context, shellCmd string, timeout time.Duration, onLine func(string)) Result
	LookPath(name string) string
}

// OSRunner executes commands on the host OS.
type OSRunner struct{}

func NewOSRunner() *OSRunner {
	return &OSRunner{}
}

// BaseEnvironment builds a sanitized environment for `colab` CLI calls:
// - Prepends standard user tool paths (~/.local/bin, /opt/homebrew/bin, /usr/local/bin)
// - Removes all_proxy / ALL_PROXY (prevents urllib3/requests socks5 stalls)
// - Enforces single-layer retries via COLAB_CLI_ASSIGN_RETRIES=0
func BaseEnvironment(extra map[string]string) []string {
	envMap := make(map[string]string)
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			envMap[k] = v
		}
	}
	delete(envMap, "all_proxy")
	delete(envMap, "ALL_PROXY")
	envMap["COLAB_CLI_ASSIGN_RETRIES"] = "0"

	home, _ := os.UserHomeDir()
	extras := []string{
		filepath.Join(home, ".local", "bin"),
		"/opt/homebrew/bin",
		"/usr/local/bin",
		"/usr/bin",
		"/bin",
		"/usr/sbin",
		"/sbin",
	}
	cur := envMap["PATH"]
	seen := make(map[string]bool)
	var merged []string
	for _, p := range append(extras, strings.Split(cur, ":")...) {
		if p != "" && !seen[p] {
			seen[p] = true
			merged = append(merged, p)
		}
	}
	envMap["PATH"] = strings.Join(merged, ":")

	for k, v := range extra {
		envMap[k] = v
	}
	out := make([]string, 0, len(envMap))
	for k, v := range envMap {
		out = append(out, k+"="+v)
	}
	return out
}

func ShellEscapePOSIX(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// StripCR normalizes CRLF outputs cleanly to LF.
func StripCR(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}

func (r *OSRunner) LookPath(name string) string {
	home, _ := os.UserHomeDir()
	dirs := []string{
		filepath.Join(home, ".local", "bin"),
		"/opt/homebrew/bin",
		"/usr/local/bin",
	}
	for _, d := range dirs {
		cand := filepath.Join(d, name)
		if st, err := os.Stat(cand); err == nil && !st.IsDir() && (st.Mode()&0o111) != 0 {
			return cand
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return ""
}

func (r *OSRunner) Run(ctx context.Context, name string, args []string, opts RunOptions) Result {
	return runProcessGroup(ctx, name, args, opts)
}

func (r *OSRunner) RunShell(ctx context.Context, shellCmd string, opts RunOptions) Result {
	return runProcessGroup(ctx, "/bin/sh", []string{"-c", shellCmd}, opts)
}

func (r *OSRunner) RunStreamingShell(ctx context.Context, shellCmd string, timeout time.Duration, onLine func(string)) Result {
	return runStreamingProcessGroup(ctx, "/bin/sh", []string{"-c", shellCmd}, timeout, onLine)
}

// boundedBuffer caps retained bytes at MaxPipeBytes while still accepting writes so child processes never deadlock.
type boundedBuffer struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	limit int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	rem := b.limit - b.buf.Len()
	if rem > 0 {
		if len(p) <= rem {
			_, _ = b.buf.Write(p)
		} else {
			_, _ = b.buf.Write(p[:rem])
		}
	}
	return len(p), nil
}

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return StripCR(b.buf.String())
}

func runProcessGroup(parentCtx context.Context, name string, args []string, opts RunOptions) Result {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(parentCtx, timeout)
	defer cancel()

	cmd := exec.Command(name, args...)
	cmd.Env = BaseEnvironment(opts.ExtraEnv)
	configureProcessGroup(cmd)

	outBuf := &boundedBuffer{limit: MaxPipeBytes}
	errBuf := &boundedBuffer{limit: MaxPipeBytes}
	cmd.Stdout = outBuf
	cmd.Stderr = errBuf
	if opts.Stdin != "" {
		cmd.Stdin = strings.NewReader(opts.Stdin)
	}

	if err := cmd.Start(); err != nil {
		return Result{ExitCode: -1, Stderr: err.Error()}
	}

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- cmd.Wait()
	}()

	var waitErr error
	var timedOut, canceled bool

	select {
	case waitErr = <-waitDone:
	case <-ctx.Done():
		if errors.Is(parentCtx.Err(), context.Canceled) {
			canceled = true
		} else {
			timedOut = true
		}
		killProcessGroup(cmd)
		select {
		case waitErr = <-waitDone:
		case <-time.After(3 * time.Second):
		}
	}

	exitCode := 0
	if canceled {
		exitCode = -2
	} else if timedOut {
		exitCode = -15
	} else if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}
	return Result{
		ExitCode: exitCode,
		Stdout:   outBuf.String(),
		Stderr:   errBuf.String(),
		TimedOut: timedOut,
		Canceled: canceled,
	}
}

func runStreamingProcessGroup(parentCtx context.Context, name string, args []string, timeout time.Duration, onLine func(string)) Result {
	if timeout <= 0 {
		timeout = 240 * time.Second
	}
	ctx, cancel := context.WithTimeout(parentCtx, timeout)
	defer cancel()

	cmd := exec.Command(name, args...)
	cmd.Env = BaseEnvironment(nil)
	configureProcessGroup(cmd)

	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw

	if err := cmd.Start(); err != nil {
		_ = pw.Close()
		_ = pr.Close()
		if onLine != nil {
			onLine("[ERROR] " + err.Error())
		}
		return Result{ExitCode: -1, Stderr: err.Error()}
	}

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		const maxLineBytes = 64 * 1024
		br := bufio.NewReaderSize(pr, 64*1024)
		var lineBuf []byte
		truncated := false
		flushLine := func() {
			if len(lineBuf) == 0 {
				truncated = false
				return
			}
			text := string(lineBuf)
			if truncated {
				text += "…[truncated]"
			}
			lineBuf = lineBuf[:0]
			truncated = false
			line := strings.TrimSpace(strings.ReplaceAll(text, "\r", ""))
			if line != "" && onLine != nil {
				onLine(line)
			}
		}
		for {
			chunk, isPrefix, err := br.ReadLine()
			if len(chunk) > 0 {
				if !truncated {
					remain := maxLineBytes - len(lineBuf)
					if len(chunk) <= remain {
						lineBuf = append(lineBuf, chunk...)
					} else {
						lineBuf = append(lineBuf, chunk[:remain]...)
						truncated = true
					}
				}
			}
			if !isPrefix {
				flushLine()
			}
			if err != nil {
				flushLine()
				break
			}
		}
	}()

	waitDone := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		_ = pw.Close()
		waitDone <- err
	}()

	var waitErr error
	var timedOut, canceled bool

	select {
	case waitErr = <-waitDone:
	case <-ctx.Done():
		if errors.Is(parentCtx.Err(), context.Canceled) {
			canceled = true
		} else {
			timedOut = true
		}
		killProcessGroup(cmd)
		_ = pw.Close()
		select {
		case waitErr = <-waitDone:
		case <-time.After(3 * time.Second):
		}
	}

	_ = pr.Close()
	select {
	case <-readDone:
	case <-time.After(1 * time.Second):
	}

	exitCode := 0
	if canceled {
		exitCode = -2
	} else if timedOut {
		exitCode = -15
	} else if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}
	return Result{
		ExitCode: exitCode,
		TimedOut: timedOut,
		Canceled: canceled,
	}
}
