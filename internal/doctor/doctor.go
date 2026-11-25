package doctor

import (
	"context"
	"fmt"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/LJMStark/Colab-Tools/internal/runner"
)

type InstallMethod string

const (
	MethodUVTool            InstallMethod = "uv_tool"
	MethodPipx              InstallMethod = "pipx"
	MethodHomebrewThenUV    InstallMethod = "brew_uv"
	MethodOfficialUVScript  InstallMethod = "official_uv_script"
	MethodWSLUVTool         InstallMethod = "wsl_uv_tool"
	MethodWSLOfficialScript InstallMethod = "wsl_official_uv_script"
	MethodWSLRequired       InstallMethod = "wsl_required"
)

func (m InstallMethod) DisplayTitle() string {
	switch m {
	case MethodUVTool:
		return "使用本机 uv 安装（最快推荐）"
	case MethodPipx:
		return "使用本机 pipx 安装"
	case MethodHomebrewThenUV:
		return "通过 Homebrew 安装 uv 与 Colab CLI"
	case MethodOfficialUVScript:
		return "通过 Astral 官方脚本安装 uv 与 Colab CLI"
	case MethodWSLUVTool:
		return "在 WSL2 内使用 uv 安装 google-colab-cli"
	case MethodWSLOfficialScript:
		return "在 WSL2 内通过 Astral 官方脚本安装 uv 与 Colab CLI"
	case MethodWSLRequired:
		return "需先启用 Windows WSL2（官方 Colab CLI 依赖 Linux 终端）"
	default:
		return "安装 google-colab-cli"
	}
}

func (m InstallMethod) ShortButtonTitle() string {
	switch m {
	case MethodUVTool, MethodWSLUVTool:
		return "一键安装 google-colab-cli"
	case MethodPipx:
		return "使用 pipx 一键安装"
	case MethodHomebrewThenUV:
		return "一键安装 uv 与 Colab CLI"
	case MethodOfficialUVScript, MethodWSLOfficialScript:
		return "同意并自动安装 uv 与 Colab CLI"
	case MethodWSLRequired:
		return "复制 WSL2 安装命令"
	default:
		return "一键安装 google-colab-cli"
	}
}

func (m InstallMethod) RequiresRemoteScriptConsent() bool {
	return m == MethodOfficialUVScript || m == MethodWSLOfficialScript
}

func (m InstallMethod) IsManualOnly() bool {
	return m == MethodWSLRequired
}

func (m InstallMethod) ManualShellCommand() string {
	switch m {
	case MethodUVTool:
		return "uv tool install --upgrade google-colab-cli"
	case MethodPipx:
		return "pipx install --force google-colab-cli"
	case MethodHomebrewThenUV:
		return "brew install uv && uv tool install --upgrade google-colab-cli"
	case MethodOfficialUVScript:
		return `curl -LsSf https://astral.sh/uv/install.sh | sh && "$HOME/.local/bin/uv" tool install --upgrade google-colab-cli`
	case MethodWSLUVTool:
		return `wsl -- sh -lc 'uv tool install --upgrade google-colab-cli'`
	case MethodWSLOfficialScript:
		return `wsl -- sh -lc 'curl -LsSf https://astral.sh/uv/install.sh | sh && "$HOME/.local/bin/uv" tool install --upgrade google-colab-cli'`
	case MethodWSLRequired:
		return "wsl --install -d Ubuntu"
	default:
		return "uv tool install google-colab-cli"
	}
}

type Report struct {
	ColabInstalled    bool
	ColabPath         string
	ColabVersion      string
	ColabBrokenReason string
	UsingWSLBridge    bool
	WSLAvailable      bool
	UVPath            string
	PipxPath          string
	BrewPath          string
	Python3Path       string
	RecommendedMethod InstallMethod
	CheckedAt         time.Time
}

func (r Report) PackageManagerSummary() string {
	if runtime.GOOS == "windows" {
		if r.UsingWSLBridge {
			if r.UVPath != "" {
				return "WSL2 已就绪 · 已检测到 uv"
			}
			return "WSL2 已就绪（将在 WSL2 内自动配置 uv）"
		}
		if !r.WSLAvailable && !r.ColabInstalled {
			return "未检测到已启用的 WSL2（请先在管理员终端运行 wsl --install）"
		}
	}
	if r.UVPath != "" {
		return "已检测到 uv"
	}
	if r.PipxPath != "" {
		return "已检测到 pipx"
	}
	if r.BrewPath != "" {
		return "已检测到 Homebrew"
	}
	if r.Python3Path != "" {
		return "仅检测到 Python 3（建议自动安装 uv）"
	}
	return "未检测到 uv / pipx / Homebrew"
}

type InstallResult struct {
	Succeeded bool
	Report    Report
	Message   string
}

var versionRe = regexp.MustCompile(`\b\d+\.\d+(?:\.\d+)*(?:[A-Za-z0-9._-]*)?\b`)

// ParseVersionString extracts semver tokens from `Version: 0.8.0` or `google-colab-cli 0.8.2`.
func ParseVersionString(output string) string {
	for _, raw := range strings.Split(runner.StripCR(output), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if m := versionRe.FindString(line); m != "" {
			return m
		}
	}
	return ""
}

func ChooseInstallMethod(goos string, uvPath, pipxPath, brewPath string, wslAvailable bool) InstallMethod {
	if goos == "windows" {
		if !wslAvailable {
			return MethodWSLRequired
		}
		if uvPath != "" {
			return MethodWSLUVTool
		}
		return MethodWSLOfficialScript
	}
	if uvPath != "" {
		return MethodUVTool
	}
	if pipxPath != "" {
		return MethodPipx
	}
	if brewPath != "" {
		return MethodHomebrewThenUV
	}
	return MethodOfficialUVScript
}

// Inspect checks CLI health using official `colab version` (falling back to `--version`).
// On Windows, probes native `colab` first, then transparently falls back to WSL2 bridge detection.
func Inspect(ctx context.Context, r *runner.OSRunner) Report {
	if r == nil {
		r = runner.NewOSRunner()
	}
	if runtime.GOOS == "windows" {
		return inspectWindows(ctx, r)
	}
	return inspectUnix(ctx, r)
}

func inspectUnix(ctx context.Context, r runner.Runner) Report {
	colabPath := r.LookPath("colab")
	uvPath := r.LookPath("uv")
	pipxPath := r.LookPath("pipx")
	brewPath := r.LookPath("brew")
	py3Path := r.LookPath("python3")

	healthy, version, broken := probeColabHealth(ctx, r, colabPath)
	return Report{
		ColabInstalled:    healthy,
		ColabPath:         colabPath,
		ColabVersion:      version,
		ColabBrokenReason: broken,
		UVPath:            uvPath,
		PipxPath:          pipxPath,
		BrewPath:          brewPath,
		Python3Path:       py3Path,
		RecommendedMethod: ChooseInstallMethod(runtime.GOOS, uvPath, pipxPath, brewPath, false),
		CheckedAt:         time.Now(),
	}
}

func inspectWindows(ctx context.Context, r *runner.OSRunner) Report {
	// 1. Check native Windows colab first (future-proof once PR #86 merges upstream)
	nativeRunner := &runner.OSRunner{UseWSLOnWindows: false}
	if nativePath := nativeRunner.LookPath("colab.exe"); nativePath != "" {
		if ok, ver, _ := probeColabHealth(ctx, nativeRunner, nativePath); ok {
			r.UseWSLOnWindows = false
			return Report{
				ColabInstalled:    true,
				ColabPath:         nativePath,
				ColabVersion:      ver,
				UsingWSLBridge:    false,
				RecommendedMethod: MethodUVTool,
				CheckedAt:         time.Now(),
			}
		}
	}

	// 2. Check WSL2 availability via `wsl.exe --status` & `wsl.exe -- true`
	wslOK := false
	if nativeRunner.LookPath("wsl.exe") != "" {
		probe := nativeRunner.Run(ctx, "wsl.exe", []string{"--", "true"}, runner.RunOptions{Timeout: 8 * time.Second})
		wslOK = probe.ExitCode == 0
	}
	if !wslOK {
		r.UseWSLOnWindows = true
		return Report{
			ColabInstalled:    false,
			WSLAvailable:      false,
			UsingWSLBridge:    false,
			RecommendedMethod: MethodWSLRequired,
			CheckedAt:         time.Now(),
		}
	}

	// 3. Probe inside WSL2
	r.UseWSLOnWindows = true
	wslRunner := &runner.OSRunner{UseWSLOnWindows: true, WSLDistro: r.WSLDistro}
	whichColab := strings.TrimSpace(wslRunner.RunShell(ctx, "command -v colab || true", runner.RunOptions{Timeout: 8 * time.Second}).Stdout)
	whichUV := strings.TrimSpace(wslRunner.RunShell(ctx, "command -v uv || true", runner.RunOptions{Timeout: 8 * time.Second}).Stdout)
	whichPy := strings.TrimSpace(wslRunner.RunShell(ctx, "command -v python3 || true", runner.RunOptions{Timeout: 8 * time.Second}).Stdout)

	healthy := false
	version := ""
	broken := ""
	if whichColab != "" {
		res := wslRunner.RunShell(ctx, "colab version", runner.RunOptions{Timeout: 10 * time.Second})
		out := res.Stdout
		if out == "" {
			out = res.Stderr
		}
		if res.ExitCode == 0 {
			if v := ParseVersionString(out); v != "" {
				healthy = true
				version = v
			}
		}
		if !healthy {
			fb := wslRunner.RunShell(ctx, "colab --version", runner.RunOptions{Timeout: 10 * time.Second})
			fbOut := fb.Stdout
			if fbOut == "" {
				fbOut = fb.Stderr
			}
			if fb.ExitCode == 0 {
				if v := ParseVersionString(fbOut); v != "" {
					healthy = true
					version = v
				}
			}
		}
		if !healthy {
			broken = fmt.Sprintf("WSL2 中检测到 %s 但无法执行 colab version，请一键重装修复", whichColab)
		}
	}

	return Report{
		ColabInstalled:    healthy,
		ColabPath:         whichColab,
		ColabVersion:      version,
		ColabBrokenReason: broken,
		UsingWSLBridge:    true,
		WSLAvailable:      true,
		UVPath:            whichUV,
		Python3Path:       whichPy,
		RecommendedMethod: ChooseInstallMethod("windows", whichUV, "", "", true),
		CheckedAt:         time.Now(),
	}
}

func probeColabHealth(ctx context.Context, r runner.Runner, colabPath string) (healthy bool, version string, brokenReason string) {
	if colabPath == "" {
		return false, "", ""
	}
	primary := r.Run(ctx, colabPath, []string{"version"}, runner.RunOptions{Timeout: 8 * time.Second})
	primaryOut := primary.Stdout
	if primaryOut == "" {
		primaryOut = primary.Stderr
	}
	if primary.ExitCode == 0 {
		if v := ParseVersionString(primaryOut); v != "" {
			return true, v, ""
		}
	}
	fallback := r.Run(ctx, colabPath, []string{"--version"}, runner.RunOptions{Timeout: 8 * time.Second})
	fallbackOut := fallback.Stdout
	if fallbackOut == "" {
		fallbackOut = fallback.Stderr
	}
	if fallback.ExitCode == 0 {
		if v := ParseVersionString(fallbackOut); v != "" {
			return true, v, ""
		}
	}
	code := primary.ExitCode
	if code == 0 {
		code = fallback.ExitCode
	}
	detail := fmt.Sprintf("退出码 %d", code)
	if primary.TimedOut {
		detail = "执行超时"
	}
	return false, "", fmt.Sprintf("检测到 %s 但无法正常运行 colab version（%s），请一键重装修复", colabPath, detail)
}

func InstallColabCLI(
	ctx context.Context,
	r *runner.OSRunner,
	method InstallMethod,
	timeout time.Duration,
	onLine func(string),
) InstallResult {
	if r == nil {
		r = runner.NewOSRunner()
	}
	if timeout <= 0 {
		timeout = 240 * time.Second
	}
	if method == MethodWSLRequired {
		rep := Inspect(ctx, r)
		return InstallResult{
			Succeeded: false,
			Report:    rep,
			Message:   "请先以管理员身份打开 PowerShell 运行 `wsl --install -d Ubuntu` 并重启电脑",
		}
	}

	var shellCmd string
	switch method {
	case MethodUVTool, MethodWSLUVTool:
		shellCmd = "uv tool install --upgrade google-colab-cli"
	case MethodPipx:
		shellCmd = "pipx install --force google-colab-cli"
	case MethodHomebrewThenUV:
		shellCmd = "brew install uv && uv tool install --upgrade google-colab-cli"
	case MethodOfficialUVScript, MethodWSLOfficialScript:
		shellCmd = `curl -LsSf https://astral.sh/uv/install.sh | sh && "$HOME/.local/bin/uv" tool install --upgrade google-colab-cli`
	default:
		shellCmd = "uv tool install --upgrade google-colab-cli"
	}

	if onLine != nil {
		onLine("$ " + shellCmd)
	}
	res := r.RunStreamingShell(ctx, shellCmd, timeout, onLine)
	refreshed := Inspect(context.Background(), r)

	if res.Canceled || ctx.Err() == context.Canceled {
		return InstallResult{Succeeded: false, Report: refreshed, Message: "已取消安装并清理全部子进程"}
	}
	if res.TimedOut {
		return InstallResult{Succeeded: false, Report: refreshed, Message: fmt.Sprintf("安装超时（超过 %d 秒），请检查网络或复制命令到终端执行", int(timeout.Seconds()))}
	}
	if res.ExitCode == 0 && refreshed.ColabInstalled {
		ver := ""
		if refreshed.ColabVersion != "" {
			ver = "v" + refreshed.ColabVersion + " "
		}
		return InstallResult{Succeeded: true, Report: refreshed, Message: fmt.Sprintf("google-colab-cli %s安装完成并已验证可用", ver)}
	}
	if res.ExitCode == 0 && !refreshed.ColabInstalled {
		msg := refreshed.ColabBrokenReason
		if msg == "" {
			msg = "安装命令已退出，但未在 PATH (~/.local/bin) 检测到可用的 colab 命令，请展开日志排查"
		}
		return InstallResult{Succeeded: false, Report: refreshed, Message: msg}
	}
	return InstallResult{Succeeded: false, Report: refreshed, Message: fmt.Sprintf("安装未成功（退出码 %d），请展开日志排查或复制命令在终端执行", res.ExitCode)}
}
