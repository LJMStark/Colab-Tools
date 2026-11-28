package doctor

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/LJMStark/Colab-Tools/internal/runner"
)

type InstallMethod string

const (
	MethodUVTool           InstallMethod = "uv_tool"
	MethodPipx             InstallMethod = "pipx"
	MethodHomebrewThenUV   InstallMethod = "brew_uv"
	MethodOfficialUVScript InstallMethod = "official_uv_script"
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
	default:
		return "安装 google-colab-cli"
	}
}

func (m InstallMethod) ShortButtonTitle() string {
	switch m {
	case MethodUVTool:
		return "一键安装 google-colab-cli"
	case MethodPipx:
		return "使用 pipx 一键安装"
	case MethodHomebrewThenUV:
		return "一键安装 uv 与 Colab CLI"
	case MethodOfficialUVScript:
		return "同意并自动安装 uv 与 Colab CLI"
	default:
		return "一键安装 google-colab-cli"
	}
}

func (m InstallMethod) RequiresRemoteScriptConsent() bool {
	return m == MethodOfficialUVScript
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
	default:
		return "uv tool install google-colab-cli"
	}
}

type Report struct {
	ColabInstalled    bool
	ColabPath         string
	ColabVersion      string
	ColabBrokenReason string
	UVPath            string
	PipxPath          string
	BrewPath          string
	Python3Path       string
	RecommendedMethod InstallMethod
	CheckedAt         time.Time
}

func (r Report) PackageManagerSummary() string {
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

func ChooseInstallMethod(uvPath, pipxPath, brewPath string) InstallMethod {
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
func Inspect(ctx context.Context, r *runner.OSRunner) Report {
	if r == nil {
		r = runner.NewOSRunner()
	}
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
		RecommendedMethod: ChooseInstallMethod(uvPath, pipxPath, brewPath),
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

	var shellCmd string
	switch method {
	case MethodUVTool:
		shellCmd = "uv tool install --upgrade google-colab-cli"
	case MethodPipx:
		shellCmd = "pipx install --force google-colab-cli"
	case MethodHomebrewThenUV:
		shellCmd = "brew install uv && uv tool install --upgrade google-colab-cli"
	case MethodOfficialUVScript:
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
