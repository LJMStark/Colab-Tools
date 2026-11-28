package colab

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LJMStark/Colab-Tools/internal/core"
	"github.com/LJMStark/Colab-Tools/internal/runner"
	"github.com/LJMStark/Colab-Tools/internal/vault"
)

type UsageSnapshot struct {
	Balance           float64
	RatePerHour       float64
	ActiveAssignments int
}

type WhoamiInfo struct {
	Provider  string
	Email     string
	ExpiresIn string
}

type RemoteTelemetry struct {
	GPUName            string
	VRAMUsedMB         int
	VRAMTotalMB        int
	RAMUsedMB          int
	RAMTotalMB         int
	GPUUtil            *int
	CPUUtil            *int
	CUDAProcessCount   int
	PublicURL          string
	DisplayUIURL       string
	ModelFile          string
	ModelAlias         string
	ContextLimit       int
	KVQuant            string
	Healthy            bool
	UptimeSeconds      int
	StatusPayload      *core.WorkloadStatusPayload
	StatusAgeSeconds   *int
	ComfyQueue         *core.ComfyQueueSummary
	DetectedWebPort    *int
	DeployAlive        bool
	RenderedVideoCount int
	DriveMounted       *bool
	EffectiveOutputDir string
	TempOutputVideos   *int
	CloudflaredActive  bool
}

var ErrSessionParseFailed = errors.New("colab sessions 输出格式异常，已保留上次状态防止误判离线")

type ProbeAttemptRecord struct {
	Hardware  string
	Succeeded bool
}

type ProvisionRoundResult struct {
	Outcome           core.ProvisionOutcome
	UnsupportedValues map[string]bool
	ProbeRecords      []ProbeAttemptRecord
	UsedStandardRAM   bool
}

type OAuthSessionTicket struct {
	AuthURL         string
	CodeVerifier    string
	ColabPythonPath string
}

type Engine struct {
	Runner runner.Runner
	Vault  *vault.Vault
}

func NewEngine(r runner.Runner, v *vault.Vault) *Engine {
	if r == nil {
		r = runner.NewOSRunner()
	}
	if v == nil {
		v = vault.New(nil)
	}
	return &Engine{Runner: r, Vault: v}
}

func (e *Engine) useWindowsNativeExec() bool {
	if runtime.GOOS != "windows" {
		return false
	}
	if osr, ok := e.Runner.(*runner.OSRunner); ok {
		return !osr.UseWSLOnWindows
	}
	return false
}

func (e *Engine) colabPrefixArgs(profile core.AccountProfile, useWSLPaths bool) ([]string, error) {
	switch profile.AuthMode {
	case core.AuthIsolatedVaultADC:
		args := []string{"--auth=adc"}
		if cfg := profile.IsolatedConfigFilePath(); cfg != "" {
			_ = os.MkdirAll(filepath.Dir(cfg), 0o700)
			if useWSLPaths {
				cfg = runner.WindowsToWSLPath(cfg)
			}
			args = append(args, "--config", cfg)
		}
		return args, nil
	case core.AuthSystemDefaultOAuth2, "":
		return []string{"--auth=oauth2"}, nil
	default:
		return nil, fmt.Errorf("无法识别账号认证模式 %q，已拒绝回退至系统默认账号", profile.AuthMode)
	}
}

func (e *Engine) RunColab(ctx context.Context, profile core.AccountProfile, subArgs []string, stdin string, timeout time.Duration) runner.Result {
	nativeWin := e.useWindowsNativeExec()
	useWSLPaths := runtime.GOOS == "windows" && !nativeWin
	prefix, err := e.colabPrefixArgs(profile, useWSLPaths)
	if err != nil {
		return runner.Result{ExitCode: -1, Stderr: err.Error()}
	}
	cliArgs := append(prefix, subArgs...)

	execWithEnv := func(extraEnv map[string]string) runner.Result {
		opts := runner.RunOptions{Stdin: stdin, ExtraEnv: extraEnv, Timeout: timeout}
		if nativeWin {
			return e.Runner.Run(ctx, "colab", cliArgs, opts)
		}
		allArgs := append([]string{"colab"}, cliArgs...)
		escaped := make([]string, len(allArgs))
		for i, a := range allArgs {
			escaped[i] = runner.ShellEscapePOSIX(a)
		}
		return e.Runner.RunShell(ctx, strings.Join(escaped, " "), opts)
	}

	if profile.AuthMode != core.AuthIsolatedVaultADC {
		return execWithEnv(nil)
	}
	var res runner.Result
	err = e.Vault.WithEphemeralADCFile(profile.ID, func(adcPath string) error {
		res = execWithEnv(map[string]string{"GOOGLE_APPLICATION_CREDENTIALS": adcPath})
		return nil
	})
	if err != nil {
		return runner.Result{ExitCode: -1, Stderr: err.Error()}
	}
	return res
}

// Strict official CLI output parsers

func ParseWhoami(stdout string) (*WhoamiInfo, bool) {
	var provider, email, expires string
	for _, raw := range strings.Split(runner.StripCR(stdout), "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, "Auth provider:"):
			provider = strings.TrimSpace(strings.TrimPrefix(line, "Auth provider:"))
		case strings.HasPrefix(line, "Email:"):
			email = strings.TrimSpace(strings.TrimPrefix(line, "Email:"))
		case strings.HasPrefix(line, "Expires in:"):
			expires = strings.TrimSpace(strings.TrimPrefix(line, "Expires in:"))
		}
	}
	if email == "" || !strings.Contains(email, "@") {
		return nil, false
	}
	return &WhoamiInfo{Provider: provider, Email: email, ExpiresIn: expires}, true
}

func ParseUsage(stdout string) (*UsageSnapshot, bool) {
	var bal, rate *float64
	var active *int
	for _, raw := range strings.Split(runner.StripCR(stdout), "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, "Current balance:"):
			num := strings.TrimSpace(strings.ReplaceAll(strings.TrimPrefix(line, "Current balance:"), "compute units", ""))
			if v, err := strconv.ParseFloat(num, 64); err == nil {
				bal = &v
			}
		case strings.HasPrefix(line, "Usage rate:"):
			num := strings.TrimSpace(strings.ReplaceAll(strings.TrimPrefix(line, "Usage rate:"), "/hr", ""))
			if v, err := strconv.ParseFloat(num, 64); err == nil {
				rate = &v
			}
		case strings.HasPrefix(line, "Active assignments:"):
			num := strings.TrimSpace(strings.TrimPrefix(line, "Active assignments:"))
			if v, err := strconv.Atoi(num); err == nil {
				active = &v
			}
		}
	}
	if bal == nil || rate == nil || active == nil {
		return nil, false
	}
	return &UsageSnapshot{Balance: *bal, RatePerHour: *rate, ActiveAssignments: *active}, true
}

func isExplicitEmptySessionsLine(lower string) bool {
	return strings.Contains(lower, "no active sessions") ||
		strings.Contains(lower, "no sessions") ||
		strings.Contains(lower, "0 active")
}

func isSessionHeaderOrDecorLine(lower string) bool {
	if lower == "" {
		return true
	}
	if strings.HasPrefix(lower, "active sessions:") ||
		strings.HasPrefix(lower, "sessions:") ||
		strings.HasPrefix(lower, "hint:") ||
		strings.HasPrefix(lower, "tip:") ||
		(strings.HasPrefix(lower, "[colab] pruned ") && !strings.Contains(lower, "|")) {
		return true
	}
	return strings.Trim(lower, "-=_ ") == ""
}

// ParseSessionsStrict parses `colab sessions` output and returns ErrSessionParseFailed if any
// unrecognized/malformed non-header line appears (preventing partial/truncated output from being treated as offline).
func ParseSessionsStrict(stdout string) ([]core.ColabSessionItem, error) {
	var items []core.ColabSessionItem
	unrecognized := 0
	firstUnrecognized := ""

	for _, raw := range strings.Split(runner.StripCR(stdout), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		if isExplicitEmptySessionsLine(lower) || isSessionHeaderOrDecorLine(lower) {
			continue
		}
		if !strings.HasPrefix(line, "[") || !strings.Contains(line, "| Hardware:") {
			unrecognized++
			if firstUnrecognized == "" {
				firstUnrecognized = line
			}
			continue
		}
		closeIdx := strings.Index(line, "]")
		if closeIdx <= 1 {
			unrecognized++
			if firstUnrecognized == "" {
				firstUnrecognized = line
			}
			continue
		}
		name := line[1:closeIdx]
		rest := strings.TrimSpace(line[closeIdx+1:])
		parts := strings.Split(rest, "|")
		if len(parts) < 2 {
			unrecognized++
			if firstUnrecognized == "" {
				firstUnrecognized = line
			}
			continue
		}
		endpoint := strings.TrimSpace(parts[0])
		if endpoint == "" {
			unrecognized++
			if firstUnrecognized == "" {
				firstUnrecognized = line
			}
			continue
		}
		hw := ""
		shape := "Standard"
		variant := "DEFAULT"
		for _, p := range parts[1:] {
			p = strings.TrimSpace(p)
			switch {
			case strings.HasPrefix(p, "Hardware:"):
				hw = strings.TrimSpace(strings.TrimPrefix(p, "Hardware:"))
			case strings.HasPrefix(p, "Shape:"):
				shape = strings.TrimSpace(strings.TrimPrefix(p, "Shape:"))
			case strings.HasPrefix(p, "Variant:"):
				variant = strings.TrimSpace(strings.TrimPrefix(p, "Variant:"))
			}
		}
		if hw == "" {
			unrecognized++
			if firstUnrecognized == "" {
				firstUnrecognized = line
			}
			continue
		}
		items = append(items, core.ColabSessionItem{
			Name:     name,
			Endpoint: endpoint,
			Hardware: hw,
			Shape:    shape,
			Variant:  variant,
			IsOrphan: name == "?",
		})
	}
	if unrecognized > 0 {
		if len(firstUnrecognized) > 120 {
			firstUnrecognized = firstUnrecognized[:120] + "…"
		}
		return nil, fmt.Errorf("%w（%s）", ErrSessionParseFailed, firstUnrecognized)
	}
	return items, nil
}

func ParseSessions(stdout string) []core.ColabSessionItem {
	items, _ := ParseSessionsStrict(stdout)
	return items
}

func (e *Engine) Whoami(ctx context.Context, profile core.AccountProfile) (*WhoamiInfo, bool) {
	r := e.RunColab(ctx, profile, []string{"whoami"}, "", 20*time.Second)
	if r.ExitCode != 0 {
		return nil, false
	}
	return ParseWhoami(r.Stdout)
}

func (e *Engine) FetchUsageAndSessions(ctx context.Context, profile core.AccountProfile) (*UsageSnapshot, []core.ColabSessionItem, error) {
	u := e.RunColab(ctx, profile, []string{"usage"}, "", 25*time.Second)
	usage, ok := ParseUsage(u.Stdout)
	if u.ExitCode != 0 || !ok {
		errTxt := strings.TrimSpace(u.Stderr)
		if errTxt == "" {
			errTxt = strings.TrimSpace(u.Stdout)
		}
		if errTxt == "" {
			errTxt = "colab usage 查询失败"
		}
		return nil, nil, errors.New(errTxt)
	}
	s := e.RunColab(ctx, profile, []string{"sessions"}, "", 25*time.Second)
	if s.ExitCode != 0 {
		errTxt := strings.TrimSpace(s.Stderr)
		if errTxt == "" {
			errTxt = strings.TrimSpace(s.Stdout)
		}
		if errTxt == "" {
			errTxt = "colab sessions 查询失败"
		}
		return usage, nil, errors.New(errTxt)
	}
	sessions, parseErr := ParseSessionsStrict(s.Stdout)
	if parseErr != nil {
		return usage, nil, parseErr
	}
	return usage, sessions, nil
}

func (e *Engine) FetchRemoteTelemetry(ctx context.Context, profile core.AccountProfile) *RemoteTelemetry {
	port := profile.RemoteServicePort
	py := fmt.Sprintf(`import subprocess, json, pathlib, time, urllib.request, re
def sh(cmd):
    r = subprocess.run(["bash", "-c", cmd], text=True, capture_output=True)
    return r.stdout.strip()
def read_cpu():
    def sample():
        parts = pathlib.Path("/proc/stat").read_text().splitlines()[0].split()[1:]
        vals = [int(x) for x in parts[:8]]
        return vals[3] + vals[4], sum(vals)
    try:
        i1, t1 = sample()
        time.sleep(0.35)
        i2, t2 = sample()
        dt = t2 - t1
        if dt <= 0: return ""
        return str(max(0, min(100, int(round(100.0 * (dt - (i2 - i1)) / dt)))))
    except Exception:
        return ""
def http_get(url, timeout=2.0):
    try:
        with urllib.request.urlopen(url, timeout=timeout) as resp:
            if 200 <= resp.status < 400:
                return resp.read(32768).decode("utf-8", errors="ignore")
    except Exception:
        pass
    return ""
print("NV=" + sh("nvidia-smi --query-gpu=name,memory.used,memory.total,utilization.gpu --format=csv,noheader,nounits 2>/dev/null | head -1 || true"))
print("CUDA_PROCS=" + sh("nvidia-smi --query-compute-apps=pid --format=csv,noheader 2>/dev/null | grep -c '[0-9]' || true"))
print("CPU=" + read_cpu())
def read_mem():
    try:
        info = {}
        for ln in pathlib.Path("/proc/meminfo").read_text().splitlines():
            p = ln.split()
            if len(p) >= 2:
                info[p[0].rstrip(":")] = int(p[1])
        tot = info.get("MemTotal", 0)
        avail = info.get("MemAvailable", info.get("MemFree", 0))
        if tot > 0:
            used = max(0, tot - avail)
            return f"{used // 1024},{tot // 1024}"
    except Exception:
        pass
    return ""
print("MEM=" + read_mem())
print("URL=" + sh("cat /tmp/public_url.txt 2>/dev/null || true"))
print("UPTIME=" + sh("awk '{print int($1)}' /proc/uptime 2>/dev/null || echo 0"))
status_p = pathlib.Path("/tmp/colab-tools-status.json")
if status_p.exists():
    try:
        print("STATUS_AGE=" + str(max(0, int(time.time() - status_p.stat().st_mtime))))
    except Exception:
        pass
    raw_s = status_p.read_text(errors="ignore").strip().replace("\n", " ")
    if raw_s:
        print("STATUS_JSON=" + raw_s)
health_ok = False
if http_get("http://127.0.0.1:%d/health"):
    health_ok = True
for cp in ([%d] if %d == 8188 else [%d, 8188]):
    cq = http_get(f"http://127.0.0.1:{cp}/queue")
    if cq and "queue_running" in cq:
        print("COMFY_QUEUE=" + cq.replace("\n", " "))
        print(f"WEB_PORT={cp}")
        health_ok = True
        break
if not health_ok:
    for wp in ([%d] + [p for p in (7860, 8188) if p != %d]):
        if http_get(f"http://127.0.0.1:{wp}/", timeout=1.2):
            print(f"WEB_PORT={wp}")
            health_ok = True
            break
print("HEALTH=" + ("ok" if health_ok else ""))
deploy_alive = False
pid_p = pathlib.Path("/tmp/colab-tools-deploy.pid")
if pid_p.exists():
    try:
        dpid = int(pid_p.read_text().strip())
        cmd_p = pathlib.Path(f"/proc/{dpid}/cmdline")
        stat_p = pathlib.Path(f"/proc/{dpid}/stat")
        if cmd_p.exists() and "colab-tools-deploy.sh" in cmd_p.read_bytes().decode("utf-8", errors="ignore"):
            st_txt = stat_p.read_text(errors="ignore") if stat_p.exists() else ""
            if ") Z " not in st_txt:
                deploy_alive = True
    except Exception:
        pass
print("DEPLOY_ALIVE=" + ("1" if deploy_alive else "0"))
drive_mounted = pathlib.Path("/content/drive/MyDrive").is_dir()
print("DRIVE_MOUNTED=" + ("1" if drive_mounted else "0"))
def has_cloudflared():
    cf_pid_p = pathlib.Path("/tmp/colab-tools-cloudflared.pid")
    if cf_pid_p.exists():
        try:
            cfpid = int(cf_pid_p.read_text().strip())
            cf_cmd = pathlib.Path(f"/proc/{cfpid}/cmdline")
            if cf_cmd.exists() and "cloudflared" in cf_cmd.read_bytes().decode("utf-8", errors="ignore"):
                return True
        except Exception:
            pass
    try:
        for proc_d in pathlib.Path("/proc").iterdir():
            if not proc_d.name.isdigit():
                continue
            try:
                cmd = (proc_d / "cmdline").read_bytes().decode("utf-8", errors="ignore")
                if "cloudflared" in cmd and "tunnel" in cmd and "127.0.0.1:" in cmd:
                    return True
            except Exception:
                pass
    except Exception:
        pass
    return False
print("CLOUDFLARED_ACTIVE=" + ("1" if has_cloudflared() else "0"))
video_exts = (".mp4", ".webm", ".mov", ".mkv", ".gif")
def count_videos_recursive(root_p):
    if not root_p.is_dir():
        return 0
    cnt = 0
    err = False
    def on_err(_e):
        nonlocal err
        err = True
    for dp, dns, fns in os.walk(root_p, onerror=on_err):
        dns[:] = [d for d in dns if not d.startswith(".")]
        for fn in fns:
            if fn.lower().endswith(video_exts):
                cnt += 1
    return -1 if (err and cnt == 0) else cnt
temp_out_cnt = count_videos_recursive(pathlib.Path("/content/output"))
print(f"TEMP_OUT_VIDEOS={temp_out_cnt}")
out_count = 0
eff_out_dir = ""
cand_dirs = [%q, "/content/drive/MyDrive/ColabTools/outputs", "/content/output"] if drive_mounted else ["/content/output"]
for odir in cand_dirs:
    if not odir:
        continue
    op = pathlib.Path(odir)
    if op.is_dir():
        if not eff_out_dir:
            eff_out_dir = odir
        c_num = count_videos_recursive(op)
        if c_num > 0:
            out_count = c_num
            eff_out_dir = odir
            break
print("EFFECTIVE_OUT_DIR=" + eff_out_dir)
print(f"OUT_VIDEOS={out_count}")
meta_p = pathlib.Path("/tmp/llama-server.meta")
if meta_p.exists():
    print("META=" + meta_p.read_text(errors="ignore").strip())
else:
    cmd = sh("ps -eo args | grep '[l]lama-server' | head -1 || true")
    m = re.search(r'-m\s+(\S+)', cmd)
    c = re.search(r'-c\s+(\d+)', cmd)
    kv = re.search(r'-ctk\s+(\S+)', cmd)
    al = re.search(r'--alias\s+(\S+)', cmd)
    print("META=" + json.dumps({
        "model": pathlib.Path(m.group(1)).name if m else "",
        "ctx": int(c.group(1)) if c else 262144,
        "kv": kv.group(1) if kv else "q4_0",
        "alias": al.group(1) if al else ""
    }))
`, port, port, port, port, port, port, profile.DriveOutputDir)

	r := e.RunColab(ctx, profile, []string{"exec", "-s", profile.SessionName}, py, 30*time.Second)
	if r.ExitCode != 0 {
		return nil
	}
	t := &RemoteTelemetry{ContextLimit: 262144, KVQuant: "q4_0"}
	for _, raw := range strings.Split(runner.StripCR(r.Stdout), "\n") {
		switch {
		case strings.HasPrefix(raw, "NV="):
			parts := strings.Split(strings.TrimPrefix(raw, "NV="), ",")
			if len(parts) >= 4 {
				t.GPUName = strings.TrimSpace(parts[0])
				t.VRAMUsedMB, _ = strconv.Atoi(strings.TrimSpace(parts[1]))
				t.VRAMTotalMB, _ = strconv.Atoi(strings.TrimSpace(parts[2]))
				if g, err := strconv.Atoi(strings.TrimSpace(parts[3])); err == nil {
					t.GPUUtil = &g
				}
			}
		case strings.HasPrefix(raw, "CUDA_PROCS="):
			t.CUDAProcessCount, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(raw, "CUDA_PROCS=")))
		case strings.HasPrefix(raw, "CPU="):
			s := strings.TrimSpace(strings.TrimPrefix(raw, "CPU="))
			if s != "" {
				if c, err := strconv.Atoi(s); err == nil {
					t.CPUUtil = &c
				}
			}
		case strings.HasPrefix(raw, "MEM="):
			parts := strings.Split(strings.TrimPrefix(raw, "MEM="), ",")
			if len(parts) >= 2 {
				t.RAMUsedMB, _ = strconv.Atoi(strings.TrimSpace(parts[0]))
				t.RAMTotalMB, _ = strconv.Atoi(strings.TrimSpace(parts[1]))
			}
		case strings.HasPrefix(raw, "URL="):
			u := strings.TrimSpace(strings.TrimPrefix(raw, "URL="))
			if core.IsValidEndpointURL(u) {
				t.PublicURL = u
			}
		case strings.HasPrefix(raw, "HEALTH="):
			t.Healthy = strings.Contains(raw, "ok")
		case strings.HasPrefix(raw, "DEPLOY_ALIVE="):
			t.DeployAlive = strings.TrimSpace(strings.TrimPrefix(raw, "DEPLOY_ALIVE=")) == "1"
		case strings.HasPrefix(raw, "DRIVE_MOUNTED="):
			val := strings.TrimSpace(strings.TrimPrefix(raw, "DRIVE_MOUNTED=")) == "1"
			t.DriveMounted = &val
		case strings.HasPrefix(raw, "CLOUDFLARED_ACTIVE="):
			t.CloudflaredActive = strings.TrimSpace(strings.TrimPrefix(raw, "CLOUDFLARED_ACTIVE=")) == "1"
		case strings.HasPrefix(raw, "EFFECTIVE_OUT_DIR="):
			t.EffectiveOutputDir = strings.TrimSpace(strings.TrimPrefix(raw, "EFFECTIVE_OUT_DIR="))
		case strings.HasPrefix(raw, "TEMP_OUT_VIDEOS="):
			if v, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(raw, "TEMP_OUT_VIDEOS="))); err == nil {
				t.TempOutputVideos = &v
			}
		case strings.HasPrefix(raw, "OUT_VIDEOS="):
			t.RenderedVideoCount, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(raw, "OUT_VIDEOS=")))
		case strings.HasPrefix(raw, "UPTIME="):
			t.UptimeSeconds, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(raw, "UPTIME=")))
		case strings.HasPrefix(raw, "WEB_PORT="):
			if wp, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(raw, "WEB_PORT="))); err == nil {
				t.DetectedWebPort = &wp
			}
		case strings.HasPrefix(raw, "STATUS_AGE="):
			if age, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(raw, "STATUS_AGE="))); err == nil {
				t.StatusAgeSeconds = &age
			}
		case strings.HasPrefix(raw, "STATUS_JSON="):
			if payload, ok := core.ParseWorkloadStatusJSON(strings.TrimPrefix(raw, "STATUS_JSON=")); ok {
				t.StatusPayload = payload
				if payload.UIURL != "" && core.IsValidEndpointURL(payload.UIURL) {
					t.DisplayUIURL = payload.UIURL
				}
				if payload.Port != nil && *payload.Port >= 1024 && *payload.Port <= 65535 {
					p := *payload.Port
					t.DetectedWebPort = &p
				}
			}
		case strings.HasPrefix(raw, "COMFY_QUEUE="):
			if cq, ok := core.ParseComfyQueueJSON(strings.TrimPrefix(raw, "COMFY_QUEUE=")); ok {
				t.ComfyQueue = cq
				t.Healthy = true
			}
		case strings.HasPrefix(raw, "META="):
			var obj struct {
				Model string `json:"model"`
				Ctx   int    `json:"ctx"`
				KV    string `json:"kv"`
				Alias string `json:"alias"`
			}
			if json.Unmarshal([]byte(strings.TrimPrefix(raw, "META=")), &obj) == nil {
				t.ModelFile = obj.Model
				if obj.Ctx > 0 {
					t.ContextLimit = obj.Ctx
				}
				if obj.KV != "" {
					t.KVQuant = obj.KV
				}
				if core.IsValidModelAlias(obj.Alias) {
					t.ModelAlias = obj.Alias
				}
			}
		}
	}
	return t
}

func (e *Engine) FetchRemoteAPIKeySafely(ctx context.Context, profile core.AccountProfile) string {
	tmp, err := e.Vault.WriteEphemeralFile("keydl", "txt", nil)
	if err != nil {
		return ""
	}
	defer vault.ShredAndRemove(tmp)
	_ = os.Remove(tmp)
	dlTarget := tmp
	if runtime.GOOS == "windows" {
		dlTarget = runner.WindowsToWSLPath(tmp)
	}
	r := e.RunColab(ctx, profile, []string{"download", "-s", profile.SessionName, "/content/drive/MyDrive/colab-llm/api_key.txt", dlTarget}, "", 25*time.Second)
	if r.ExitCode != 0 {
		return ""
	}
	raw, err := os.ReadFile(tmp)
	if err != nil {
		return ""
	}
	key := strings.TrimSpace(string(raw))
	if key == "" || strings.ContainsAny(key, " \t\r\n") {
		return ""
	}
	return key
}

func originKey(u *url.URL) string {
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if port == "" {
		if scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return scheme + "://" + host + ":" + port
}

// FetchSlots queries llama-server `/slots` and strictly blocks cross-origin redirects so `x-api-key` never leaks.
func FetchSlots(ctx context.Context, baseURL, apiKey string) ([]core.SlotInfo, bool) {
	if !core.IsValidEndpointURL(baseURL) {
		return nil, false
	}
	target := strings.TrimRight(baseURL, "/") + "/slots"
	parsed, err := url.Parse(target)
	if err != nil {
		return nil, false
	}
	allowed := originKey(parsed)
	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if originKey(req.URL) != allowed {
				return http.ErrUseLastResponse
			}
			if len(via) >= 3 {
				return errors.New("too many redirects")
			}
			return nil
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, false
	}
	if apiKey != "" {
		req.Header.Set("x-api-key", apiKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return nil, false
	}
	var slots []core.SlotInfo
	if err := json.Unmarshal(body, &slots); err != nil {
		return nil, false
	}
	return slots, true
}

func (e *Engine) ProvisionSession(
	ctx context.Context,
	profile core.AccountProfile,
	overrideTargets []core.ProbeTarget,
	deadline *time.Time,
	interTargetDelay func() time.Duration,
	onProgress func(string),
) ProvisionRoundResult {
	res := ProvisionRoundResult{UnsupportedValues: map[string]bool{}}
	if !core.IsValidSessionName(profile.SessionName) {
		res.Outcome = core.ProvisionOutcome{Kind: core.OutcomeTransientError, Code: "INVALID_SESSION", Message: "会话名不合法（仅允许字母、数字、._-）"}
		return res
	}
	targets := overrideTargets
	if len(targets) == 0 {
		for _, opt := range core.EffectiveQueueTargets(profile) {
			targets = append(targets, opt.ProbeTargets()...)
		}
	}
	if len(targets) == 0 {
		res.Outcome = core.ProvisionOutcome{Kind: core.OutcomeTransientError, Code: "NO_TARGET", Message: "未选择任何目标算力卡"}
		return res
	}

	lastOutcome := core.ProvisionOutcome{Kind: core.OutcomeCapacity503, Message: "当前目标算力暂时没货"}
	sawCapacity := false
	useHighMem := core.EffectiveUseHighMem(profile)

	for idx, target := range targets {
		if ctx.Err() != nil {
			res.Outcome = core.ProvisionOutcome{Kind: core.OutcomeTransientError, Code: "CANCELLED", Message: "已取消申请"}
			return res
		}
		if deadline != nil && time.Until(*deadline) < core.QueueMinProvisionBudget {
			res.Outcome = core.ProvisionOutcome{Kind: core.OutcomeBudgetExhausted, Message: "已达排队总时长上限"}
			return res
		}
		if idx > 0 && interTargetDelay != nil {
			pause := interTargetDelay()
			if deadline != nil && time.Until(*deadline)-pause < core.QueueMinProvisionBudget {
				res.Outcome = core.ProvisionOutcome{Kind: core.OutcomeBudgetExhausted, Message: "已达排队总时长上限"}
				return res
			}
			if pause > 0 {
				if onProgress != nil {
					onProgress(fmt.Sprintf("自动改试 %s（间隔 %ds）…", target.Display, int(pause.Seconds())))
				}
				select {
				case <-time.After(pause):
				case <-ctx.Done():
					res.Outcome = core.ProvisionOutcome{Kind: core.OutcomeTransientError, Code: "CANCELLED", Message: "已取消申请"}
					return res
				}
			}
		}

		stepTimeout := 50 * time.Second
		if deadline != nil {
			rem := time.Until(*deadline)
			if rem < core.QueueMinProvisionBudget {
				res.Outcome = core.ProvisionOutcome{Kind: core.OutcomeBudgetExhausted, Message: "已达排队总时长上限"}
				return res
			}
			if rem < stepTimeout {
				stepTimeout = time.Duration(math.Floor(rem.Seconds())) * time.Second
			}
		}

		if onProgress != nil {
			onProgress(fmt.Sprintf("正在申请 %s…", target.Display))
		}
		args := []string{"new", "-s", profile.SessionName}
		if target.Flag != "" && target.Value != "" {
			args = append(args, target.Flag, target.Value)
		}
		if useHighMem {
			args = append(args, "--high-mem")
		}
		r := e.RunColab(ctx, profile, args, "", stepTimeout)
		if r.ExitCode == 0 {
			res.ProbeRecords = append(res.ProbeRecords, ProbeAttemptRecord{Hardware: target.Display, Succeeded: true})
			res.Outcome = core.ProvisionOutcome{Kind: core.OutcomeAllocated, Hardware: target.Display}
			return res
		}
		outcome := core.ClassifyProvisionFailure(r.ExitCode, r.TimedOut, r.Stdout, r.Stderr, target.Display)
		handleTimeout := func(toOutcome core.ProvisionOutcome, standardRAM bool) (done bool, shouldContinue bool) {
			_, activeSessions, checkErr := e.FetchUsageAndSessions(ctx, profile)
			if checkErr == nil {
				for _, sess := range activeSessions {
					if !sess.IsOrphan && sess.Name == profile.SessionName {
						hw := strings.TrimSpace(sess.Hardware)
						if hw == "" {
							hw = target.Display
						}
						res.ProbeRecords = append(res.ProbeRecords, ProbeAttemptRecord{Hardware: hw, Succeeded: true})
						if standardRAM {
							res.UsedStandardRAM = true
						}
						res.Outcome = core.ProvisionOutcome{Kind: core.OutcomeAllocated, Hardware: hw}
						return true, false
					}
				}
				res.ProbeRecords = append(res.ProbeRecords, ProbeAttemptRecord{Hardware: target.Display, Succeeded: false})
				if !sawCapacity {
					lastOutcome = toOutcome
				}
				if onProgress != nil {
					onProgress(fmt.Sprintf("%s 申请超时且确认未开机，自动切换下一张卡…", target.Display))
				}
				return false, true
			}
			// Do not blindly retry when verification failed to avoid duplicate billing.
			res.Outcome = toOutcome
			return true, false
		}
		handle412 := func(preOutcome core.ProvisionOutcome) (done bool, shouldContinue bool) {
			// Official google-colab-cli 0.8.x uses HTTP 412 for BOTH "too many active sessions"
			// AND "temporary usage/capacity limit for the requested accelerator".
			// Check active sessions on server: if check fails or 0 active sessions, treat this card as unavailable and try the next card!
			_, activeSessions, checkErr := e.FetchUsageAndSessions(ctx, profile)
			if checkErr != nil {
				res.ProbeRecords = append(res.ProbeRecords, ProbeAttemptRecord{Hardware: target.Display, Succeeded: false})
				sawCapacity = true
				lastOutcome = core.ProvisionOutcome{
					Kind:      core.OutcomeCapacity503,
					Hardware:  target.Display,
					Code:      "412_CHECK_ERR",
					Message:   fmt.Sprintf("%s 暂时受限或查询波动，将自动改试下一张卡", target.Display),
					RawDetail: preOutcome.RawDetail,
				}
				if onProgress != nil {
					onProgress(fmt.Sprintf("%s 暂时受限，自动切换下一张卡…", target.Display))
				}
				return false, true
			}
			if len(activeSessions) == 0 {
				res.ProbeRecords = append(res.ProbeRecords, ProbeAttemptRecord{Hardware: target.Display, Succeeded: false})
				sawCapacity = true
				lastOutcome = core.ProvisionOutcome{
					Kind:      core.OutcomeCapacity503,
					Hardware:  target.Display,
					Code:      "412_CAPACITY",
					Message:   fmt.Sprintf("%s 暂时受限或满载，将自动改试下一张卡", target.Display),
					RawDetail: preOutcome.RawDetail,
				}
				if onProgress != nil {
					onProgress(fmt.Sprintf("%s 暂时受限，自动切换下一张卡…", target.Display))
				}
				return false, true
			}
			preOutcome.Message = fmt.Sprintf("账号里已有 %d 个运行中的机器，达到同时开机上限，请先关掉旧机器", len(activeSessions))
			res.Outcome = preOutcome
			return true, false
		}
		switch outcome.Kind {
		case core.OutcomeAllocated:
			res.ProbeRecords = append(res.ProbeRecords, ProbeAttemptRecord{Hardware: target.Display, Succeeded: true})
			res.Outcome = outcome
			return res
		case core.OutcomeRateLimited, core.OutcomeAuthError, core.OutcomeBudgetExhausted:
			res.Outcome = outcome
			return res
		case core.OutcomePrecondition412:
			done, cont := handle412(outcome)
			if done {
				return res
			}
			if cont {
				continue
			}
		case core.OutcomeCapacity503:
			if useHighMem {
				waitDur := 2 * time.Second
				canRetryStd := deadline == nil || time.Until(*deadline)-waitDur >= core.QueueMinProvisionBudget
				if canRetryStd {
					if onProgress != nil {
						onProgress(fmt.Sprintf("%s 高内存暂缺，2 秒后尝试标准内存…", target.Display))
					}
					select {
					case <-time.After(waitDur):
					case <-ctx.Done():
						res.Outcome = core.ProvisionOutcome{Kind: core.OutcomeTransientError, Code: "CANCELLED", Message: "已取消申请"}
						return res
					}
					stdTimeout := 50 * time.Second
					if deadline != nil {
						rem := time.Until(*deadline)
						if rem < core.QueueMinProvisionBudget {
							res.Outcome = core.ProvisionOutcome{Kind: core.OutcomeBudgetExhausted, Message: "已达排队总时长上限"}
							return res
						}
						if rem < stdTimeout {
							stdTimeout = time.Duration(math.Floor(rem.Seconds())) * time.Second
						}
					}
					stdArgs := []string{"new", "-s", profile.SessionName}
					if target.Flag != "" && target.Value != "" {
						stdArgs = append(stdArgs, target.Flag, target.Value)
					}
					stdR := e.RunColab(ctx, profile, stdArgs, "", stdTimeout)
					if stdR.ExitCode == 0 {
						res.ProbeRecords = append(res.ProbeRecords, ProbeAttemptRecord{Hardware: target.Display, Succeeded: true})
						res.UsedStandardRAM = true
						res.Outcome = core.ProvisionOutcome{Kind: core.OutcomeAllocated, Hardware: target.Display}
						return res
					}
					stdOutcome := core.ClassifyProvisionFailure(stdR.ExitCode, stdR.TimedOut, stdR.Stdout, stdR.Stderr, target.Display)
					switch stdOutcome.Kind {
					case core.OutcomeAllocated:
						res.ProbeRecords = append(res.ProbeRecords, ProbeAttemptRecord{Hardware: target.Display, Succeeded: true})
						res.UsedStandardRAM = true
						res.Outcome = stdOutcome
						return res
					case core.OutcomeRateLimited, core.OutcomeAuthError, core.OutcomeBudgetExhausted:
						res.Outcome = stdOutcome
						return res
					case core.OutcomePrecondition412:
						done, cont := handle412(stdOutcome)
						if done {
							return res
						}
						if cont {
							continue
						}
					case core.OutcomeUnsupportedTier400:
						if target.Value != "" {
							res.UnsupportedValues[strings.ToUpper(target.Value)] = true
						}
						if !sawCapacity {
							lastOutcome = stdOutcome
						}
						if onProgress != nil {
							onProgress(fmt.Sprintf("当前账号无 %s 配额，自动跳过…", target.Display))
						}
						continue
					case core.OutcomeTransientError:
						if stdOutcome.Code == "TIMEOUT" {
							done, cont := handleTimeout(stdOutcome, true)
							if done {
								return res
							}
							if cont {
								continue
							}
						}
						outcome = stdOutcome
					default:
						outcome = stdOutcome
					}
				}
			}
			res.ProbeRecords = append(res.ProbeRecords, ProbeAttemptRecord{Hardware: target.Display, Succeeded: false})
			sawCapacity = true
			lastOutcome = outcome
			if onProgress != nil {
				onProgress(fmt.Sprintf("%s 暂时没货，自动切换下一张卡…", target.Display))
			}
		case core.OutcomeUnsupportedTier400:
			if target.Value != "" {
				res.UnsupportedValues[strings.ToUpper(target.Value)] = true
			}
			if !sawCapacity {
				lastOutcome = outcome
			}
			if onProgress != nil {
				onProgress(fmt.Sprintf("当前账号无 %s 配额，自动跳过…", target.Display))
			}
		case core.OutcomeTransientError:
			if outcome.Code == "TIMEOUT" {
				done, cont := handleTimeout(outcome, false)
				if done {
					return res
				}
				if cont {
					continue
				}
			}
			if !sawCapacity {
				lastOutcome = outcome
			}
		}
	}
	res.Outcome = lastOutcome
	return res
}

func (e *Engine) StopSession(ctx context.Context, profile core.AccountProfile, sessionName string) runner.Result {
	target := sessionName
	if target == "" {
		target = profile.SessionName
	}
	if !core.IsValidSessionName(target) {
		return runner.Result{ExitCode: -1, Stderr: "无效的会话名"}
	}
	return e.RunColab(ctx, profile, []string{"stop", "-s", target}, "", 30*time.Second)
}

func (e *Engine) EnsureDriveMounted(ctx context.Context, profile core.AccountProfile) bool {
	ok, _ := e.EnsureDriveMountedWithReason(ctx, profile)
	return ok
}

// EnsureDriveMountedWithReason mounts Google Drive with a bounded wait and returns a plain-Chinese hint on failure.
func (e *Engine) EnsureDriveMountedWithReason(ctx context.Context, profile core.AccountProfile) (bool, string) {
	checkPy := "import os\nprint('DRIVE_MOUNTED=1' if os.path.isdir('/content/drive/MyDrive') else 'DRIVE_MOUNTED=0')\n"
	check := e.RunColab(ctx, profile, []string{"exec", "-s", profile.SessionName}, checkPy, 20*time.Second)
	if check.ExitCode == 0 && strings.Contains(check.Stdout, "DRIVE_MOUNTED=1") {
		return true, ""
	}
	m := e.RunColab(ctx, profile, []string{"drivemount", "-s", profile.SessionName, "/content/drive"}, "", 35*time.Second)
	combined := m.Stdout + "\n" + m.Stderr
	if m.ExitCode != 0 {
		if strings.Contains(combined, "Authorization needed") || strings.Contains(combined, "Press Enter") {
			return false, "Google Drive 需要首次浏览器授权，本次已自动切纯云端继续部署"
		}
		if m.TimedOut {
			return false, "Google Drive 挂载等待超时，已自动切纯云端继续部署"
		}
		return false, "Google Drive 暂未挂载成功，先直接云端运行（成片请记得保存）"
	}
	verify := e.RunColab(ctx, profile, []string{"exec", "-s", profile.SessionName}, checkPy, 20*time.Second)
	if verify.ExitCode == 0 && strings.Contains(verify.Stdout, "DRIVE_MOUNTED=1") {
		return true, ""
	}
	return false, "Google Drive 暂未完成授权，先直接云端运行（成片请记得保存）"
}

var (
	cloudflaredURLRegex = regexp.MustCompile(`https://[a-zA-Z0-9-]+\.trycloudflare\.com`)
	driveAuthURLRegex   = regexp.MustCompile(`https://accounts\.google\.com/[^\s"'<>]+`)
)

// EnsureCloudflaredFallbackURL 在云端按需安装并后台拉起 cloudflared 临时隧道指向本地端口，最多等待 20 秒提取安全链接。
func (e *Engine) EnsureCloudflaredFallbackURL(ctx context.Context, profile core.AccountProfile, remotePort int) (string, error) {
	if !core.IsValidSessionName(profile.SessionName) {
		return "", errors.New("会话名不合法")
	}
	if remotePort <= 0 || remotePort > 65535 {
		remotePort = 8188
	}

	py := fmt.Sprintf(`import os, pathlib, platform, re, shutil, signal, subprocess, time, urllib.request

port = %d
log_path = pathlib.Path("/tmp/colab-tools-cloudflared.log")
pid_path = pathlib.Path("/tmp/colab-tools-cloudflared.pid")
url_pat = re.compile(r"https://[a-zA-Z0-9-]+\.trycloudflare\.com")

def alive_for_port():
    if not pid_path.exists():
        return False
    try:
        pid = int(pid_path.read_text().strip())
        cmd_p = pathlib.Path(f"/proc/{pid}/cmdline")
        if not cmd_p.exists():
            return False
        cmd = cmd_p.read_bytes().decode("utf-8", errors="ignore")
        return "cloudflared" in cmd and f"127.0.0.1:{port}" in cmd
    except Exception:
        return False

if alive_for_port() and log_path.exists():
    m = url_pat.search(log_path.read_text(errors="ignore"))
    if m:
        print("CLOUDFLARED_URL=" + m.group(0))
        raise SystemExit(0)

cf_bin = shutil.which("cloudflared") or ""
if not cf_bin:
    cand = pathlib.Path("/tmp/cloudflared")
    if cand.exists() and os.access(cand, os.X_OK):
        cf_bin = str(cand)
    else:
        arch = platform.machine().lower()
        suffix = "arm64" if ("aarch64" in arch or "arm64" in arch) else "amd64"
        dl_url = f"https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-{suffix}"
        tmp_dl = pathlib.Path("/tmp/cloudflared.dl")
        urllib.request.urlretrieve(dl_url, str(tmp_dl))
        os.chmod(tmp_dl, 0o755)
        tmp_dl.replace(cand)
        cf_bin = str(cand)

if pid_path.exists():
    try:
        old_pid = int(pid_path.read_text().strip())
        old_cmd_p = pathlib.Path(f"/proc/{old_pid}/cmdline")
        if old_cmd_p.exists() and "cloudflared" in old_cmd_p.read_bytes().decode("utf-8", errors="ignore"):
            os.killpg(old_pid, signal.SIGTERM)
    except Exception:
        pass

logf = open(log_path, "wb")
proc = subprocess.Popen(
    [cf_bin, "tunnel", "--url", f"http://127.0.0.1:{port}", "--no-autoupdate"],
    stdout=logf,
    stderr=subprocess.STDOUT,
    stdin=subprocess.DEVNULL,
    start_new_session=True,
)
logf.close()
pid_path.write_text(str(proc.pid))

deadline = time.time() + 20.0
found = ""
while time.time() < deadline:
    if log_path.exists():
        m = url_pat.search(log_path.read_text(errors="ignore"))
        if m:
            found = m.group(0)
            break
    if proc.poll() is not None:
        break
    time.sleep(0.5)

if found:
    print("CLOUDFLARED_URL=" + found)
else:
    tail = log_path.read_text(errors="ignore")[-400:] if log_path.exists() else ""
    print("CLOUDFLARED_ERR=" + tail.replace("\n", " "))
`, remotePort)

	r := e.RunColab(ctx, profile, []string{"exec", "-s", profile.SessionName}, py, 50*time.Second)
	for _, raw := range strings.Split(runner.StripCR(r.Stdout), "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "CLOUDFLARED_URL=") {
			u := strings.TrimSpace(strings.TrimPrefix(line, "CLOUDFLARED_URL="))
			if m := cloudflaredURLRegex.FindString(u); m != "" && core.IsValidEndpointURL(m) {
				return m, nil
			}
		}
	}
	errMsg := strings.TrimSpace(r.Stderr)
	if errMsg == "" {
		errMsg = strings.TrimSpace(r.Stdout)
	}
	if errMsg == "" {
		errMsg = "未能启动云端临时通道"
	}
	return "", errors.New(errMsg)
}

// StopCloudflaredTunnel terminates any running cloudflared quick tunnel started on the remote Colab VM and verifies exit.
func (e *Engine) StopCloudflaredTunnel(ctx context.Context, profile core.AccountProfile) error {
	if !core.IsValidSessionName(profile.SessionName) {
		return errors.New("会话名不合法")
	}
	py := `import os, pathlib, signal, time
pid_path = pathlib.Path("/tmp/colab-tools-cloudflared.pid")
log_path = pathlib.Path("/tmp/colab-tools-cloudflared.log")
pub_path = pathlib.Path("/tmp/public_url.txt")

def find_cf_pids():
    found = set()
    if pid_path.exists():
        try:
            p = int(pid_path.read_text().strip())
            cmd_p = pathlib.Path(f"/proc/{p}/cmdline")
            if cmd_p.exists() and "cloudflared" in cmd_p.read_bytes().decode("utf-8", errors="ignore"):
                found.add(p)
        except Exception:
            pass
    try:
        for d in pathlib.Path("/proc").iterdir():
            if not d.name.isdigit():
                continue
            try:
                cmd = (d / "cmdline").read_bytes().decode("utf-8", errors="ignore")
                if "cloudflared" in cmd and "tunnel" in cmd and "127.0.0.1:" in cmd:
                    found.add(int(d.name))
            except Exception:
                pass
    except Exception:
        pass
    return sorted(found)

for sig in (signal.SIGTERM, signal.SIGKILL):
    pids = find_cf_pids()
    if not pids:
        break
    for pid in pids:
        try:
            os.killpg(pid, sig)
        except Exception:
            try:
                os.kill(pid, sig)
            except Exception:
                pass
    deadline = time.time() + (2.5 if sig == signal.SIGTERM else 1.0)
    while time.time() < deadline and find_cf_pids():
        time.sleep(0.2)

remaining = find_cf_pids()
if remaining:
    print(f"CLOUDFLARED_STILL_ALIVE={remaining}")
    raise SystemExit(1)

pid_path.unlink(missing_ok=True)
log_path.unlink(missing_ok=True)
if pub_path.exists():
    try:
        if "trycloudflare.com" in pub_path.read_text(errors="ignore"):
            pub_path.unlink(missing_ok=True)
    except Exception:
        pass
print("CLOUDFLARED_STOPPED=1")
`
	r := e.RunColab(ctx, profile, []string{"exec", "-s", profile.SessionName}, py, 20*time.Second)
	out := strings.TrimSpace(runner.StripCR(r.Stdout))
	if r.ExitCode != 0 || !strings.Contains(out, "CLOUDFLARED_STOPPED=1") {
		msg := strings.TrimSpace(r.Stderr)
		if msg == "" {
			msg = out
		}
		if msg == "" {
			msg = "关闭云端临时公网通道失败，未能确认进程退出"
		}
		return errors.New(msg)
	}
	return nil
}

// EnsureDriveMountedInteractive 以交互管道运行 `colab drivemount`，遇到 Google Drive 浏览器授权提示时通过回调弹窗引导用户一步完成授权。
func (e *Engine) EnsureDriveMountedInteractive(ctx context.Context, profile core.AccountProfile, onAuthRequired func(authURL string) (proceed bool)) (bool, string) {
	if !core.IsValidSessionName(profile.SessionName) {
		return false, "会话名不合法"
	}
	checkPy := "import os\nprint('DRIVE_MOUNTED=1' if os.path.isdir('/content/drive/MyDrive') else 'DRIVE_MOUNTED=0')\n"
	check := e.RunColab(ctx, profile, []string{"exec", "-s", profile.SessionName}, checkPy, 20*time.Second)
	if check.ExitCode == 0 && strings.Contains(check.Stdout, "DRIVE_MOUNTED=1") {
		return true, ""
	}

	var adcPath string
	if profile.AuthMode == core.AuthIsolatedVaultADC {
		var mounted bool
		var reason string
		err := e.Vault.WithEphemeralADCFile(profile.ID, func(p string) error {
			adcPath = p
			mounted, reason = e.runInteractiveDriveMount(ctx, profile, adcPath, checkPy, onAuthRequired)
			return nil
		})
		if err != nil {
			return false, "读取账号凭据失败，先直接云端运行（成片请记得保存）"
		}
		return mounted, reason
	}
	return e.runInteractiveDriveMount(ctx, profile, "", checkPy, onAuthRequired)
}

func (e *Engine) runInteractiveDriveMount(
	ctx context.Context,
	profile core.AccountProfile,
	adcPath string,
	checkPy string,
	onAuthRequired func(authURL string) (proceed bool),
) (bool, string) {
	nativeWin := e.useWindowsNativeExec()
	useWSLPaths := runtime.GOOS == "windows" && !nativeWin
	prefix, err := e.colabPrefixArgs(profile, useWSLPaths)
	if err != nil {
		return false, err.Error()
	}
	cliArgs := append(prefix, "drivemount", "-s", profile.SessionName, "/content/drive")

	colabExe := "colab"
	if e.Runner != nil {
		if p := e.Runner.LookPath("colab"); p != "" {
			colabExe = p
		}
	}

	mountCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var cmd *exec.Cmd
	extraEnv := map[string]string{
		"PYTHONUNBUFFERED": "1",
	}
	if onAuthRequired != nil && (runtime.GOOS != "windows" || useWSLPaths) {
		// Suppress google-colab-cli's internal webbrowser.open() so Colab Tools opens the browser exactly once.
		extraEnv["BROWSER"] = "true"
	}
	if adcPath != "" {
		extraEnv["GOOGLE_APPLICATION_CREDENTIALS"] = adcPath
	}

	if useWSLPaths {
		allArgs := append([]string{"colab"}, cliArgs...)
		escaped := make([]string, len(allArgs))
		for i, a := range allArgs {
			escaped[i] = runner.ShellEscapePOSIX(a)
		}
		joined := strings.Join(escaped, " ")
		detachedCmd := "if setsid -w true >/dev/null 2>&1; then exec setsid -w " + joined + "; else exec " + joined + "; fi"
		distro := ""
		if osr, ok := e.Runner.(*runner.OSRunner); ok {
			distro = osr.WSLDistro
		}
		exe, wslArgs := runner.BuildWSLCommand(distro, detachedCmd, extraEnv)
		cmd = exec.CommandContext(mountCtx, exe, wslArgs...)
	} else {
		cmd = exec.CommandContext(mountCtx, colabExe, cliArgs...)
		cmd.Env = runner.BaseEnvironment(extraEnv)
	}
	runner.DetachControllingTTY(cmd)
	cmd.Cancel = func() error {
		runner.KillProcessGroup(cmd)
		return nil
	}
	cmd.WaitDelay = 3 * time.Second

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return false, "Google Drive 挂载启动失败，先直接云端运行（成片请记得保存）"
	}
	defer stdinPipe.Close()
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw

	if err := cmd.Start(); err != nil {
		_ = pw.Close()
		_ = pr.Close()
		return false, "Google Drive 挂载启动失败，先直接云端运行（成片请记得保存）"
	}

	chunkCh := make(chan string, 32)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		defer close(chunkCh)
		buf := make([]byte, 4096)
		for {
			n, rErr := pr.Read(buf)
			if n > 0 {
				chunkCh <- string(buf[:n])
			}
			if rErr != nil {
				return
			}
		}
	}()

	waitDone := make(chan error, 1)
	go func() {
		waitErr := cmd.Wait()
		_ = pw.Close()
		waitDone <- waitErr
	}()

	var (
		accumulated     strings.Builder
		accMu           sync.Mutex
		authTriggered   bool
		skippedByUser   bool
		pendingURLFound string
		pendingURLAt    time.Time
		sawAuthHint     bool
		authDecisionCh  chan bool
		timedOutMsg     string
		waitErr         error
		processDone     bool
	)

	preAuthTimeout := 35 * time.Second
	postAuthTimeout := 90 * time.Second
	timeoutTimer := time.NewTimer(preAuthTimeout)
	defer timeoutTimer.Stop()

	flushTicker := time.NewTicker(150 * time.Millisecond)
	defer flushTicker.Stop()

	triggerAuth := func(authURL string) {
		authTriggered = true
		if !timeoutTimer.Stop() {
			select {
			case <-timeoutTimer.C:
			default:
			}
		}
		if onAuthRequired == nil {
			cancel()
			timedOutMsg = "Google Drive 需要首次浏览器授权，本次已自动切纯云端继续部署"
			return
		}
		ch := make(chan bool, 1)
		authDecisionCh = ch
		go func() {
			ch <- onAuthRequired(authURL)
		}()
	}

	checkAuthPrompt := func(now time.Time) {
		if authTriggered {
			return
		}
		accMu.Lock()
		txt := runner.StripCR(accumulated.String())
		accMu.Unlock()

		if loc := driveAuthURLRegex.FindStringIndex(txt); loc != nil {
			u := txt[loc[0]:loc[1]]
			// 若 URL 后面已经出现空白/换行，或者已停顿超过 180ms 未再延长，说明完整 URL 已接收完毕
			if loc[1] < len(txt) {
				triggerAuth(u)
				return
			}
			if pendingURLFound != u {
				pendingURLFound = u
				pendingURLAt = now
			} else if now.Sub(pendingURLAt) >= 180*time.Millisecond {
				triggerAuth(u)
				return
			}
		}

		lower := strings.ToLower(txt)
		if strings.Contains(lower, "authorization needed") || strings.Contains(lower, "press enter") {
			sawAuthHint = true
		}
	}

	for !processDone || chunkCh != nil {
		select {
		case chunk, ok := <-chunkCh:
			if !ok {
				chunkCh = nil
				continue
			}
			accMu.Lock()
			accumulated.WriteString(chunk)
			accMu.Unlock()
			checkAuthPrompt(time.Now())
		case <-flushTicker.C:
			checkAuthPrompt(time.Now())
		case proceed := <-authDecisionCh:
			authDecisionCh = nil
			if !proceed {
				skippedByUser = true
				cancel()
				_ = stdinPipe.Close()
				continue
			}
			_, _ = io.WriteString(stdinPipe, "\n")
			timeoutTimer.Reset(postAuthTimeout)
		case <-timeoutTimer.C:
			if !authTriggered && sawAuthHint {
				timedOutMsg = "未能获取到 Google Drive 授权链接，请稍后重试或点击「重挂 Drive」"
			} else {
				timedOutMsg = "Google Drive 挂载等待超时，已自动切纯云端继续部署"
			}
			cancel()
		case waitErr = <-waitDone:
			waitDone = nil
			processDone = true
		}
	}

	_ = pr.Close()
	<-readDone

	if skippedByUser {
		return false, "已跳过 Google Drive，直接纯云端运行"
	}
	if timedOutMsg != "" {
		return false, timedOutMsg
	}

	accMu.Lock()
	combined := runner.StripCR(accumulated.String())
	accMu.Unlock()

	if waitErr != nil {
		if strings.Contains(combined, "Authorization needed") || strings.Contains(combined, "Press Enter") {
			return false, "Google Drive 需要首次浏览器授权，本次已自动切纯云端继续部署"
		}
		return false, "Google Drive 暂未挂载成功，先直接云端运行（成片请记得保存）"
	}

	verify := e.RunColab(ctx, profile, []string{"exec", "-s", profile.SessionName}, checkPy, 20*time.Second)
	if verify.ExitCode == 0 && strings.Contains(verify.Stdout, "DRIVE_MOUNTED=1") {
		return true, ""
	}
	return false, "Google Drive 暂未完成授权，先直接云端运行（成片请记得保存）"
}

// FetchDeployLogTail retrieves recent lines of remote deploy & ComfyUI logs plus live process/port summary.
func (e *Engine) FetchDeployLogTail(ctx context.Context, profile core.AccountProfile, lines int) (string, error) {
	if !core.IsValidSessionName(profile.SessionName) {
		return "", errors.New("会话名不合法")
	}
	if lines <= 0 {
		lines = 60
	}
	if lines > 200 {
		lines = 200
	}
	port := profile.RemoteServicePort
	if port <= 0 {
		port = 8188
	}
	py := fmt.Sprintf(`import pathlib, subprocess, json, shutil
lines = %d
port = %d
out = []
status_p = pathlib.Path("/tmp/colab-tools-status.json")
pid_p = pathlib.Path("/tmp/colab-tools-deploy.pid")
wf_pid_p = pathlib.Path("/tmp/colab-tools-wf-sync.pid")
deploy_log = pathlib.Path("/tmp/colab-tools-deploy.log")
comfy_log = pathlib.Path("/tmp/comfyui.log")

alive = False
dpid = ""
if pid_p.exists():
    dpid = pid_p.read_text(errors="ignore").strip()
    if dpid and pathlib.Path(f"/proc/{dpid}/cmdline").exists():
        cmd = pathlib.Path(f"/proc/{dpid}/cmdline").read_bytes().decode("utf-8", errors="ignore")
        if "colab-tools-deploy.sh" in cmd:
            alive = True

wf_alive = False
wf_pid = ""
if wf_pid_p.exists():
    wf_pid = wf_pid_p.read_text(errors="ignore").strip()
    if wf_pid and pathlib.Path(f"/proc/{wf_pid}/cmdline").exists():
        wcmd = pathlib.Path(f"/proc/{wf_pid}/cmdline").read_bytes().decode("utf-8", errors="ignore")
        if "colab-tools-wf-sync.py" in wcmd:
            wf_alive = True

p_check = subprocess.run(["bash", "-c", f"ss -tlnp 2>/dev/null | grep -E ':{port}\\b' || true"], text=True, capture_output=True).stdout.strip()
drive_ok = pathlib.Path("/content/drive/MyDrive").is_dir()
comfy_dir = pathlib.Path("/content/ComfyUI").is_dir()
drive_wf_dir = pathlib.Path("/content/drive/MyDrive/ColabTools/workflows")
wf_count = 0
if drive_wf_dir.is_dir():
    try:
        wf_count = sum(1 for p in drive_wf_dir.rglob("*.json") if p.is_file() and ".part." not in p.name and "examples" not in p.relative_to(drive_wf_dir).parts)
    except Exception:
        pass
try:
    du = shutil.disk_usage("/content")
    disk_line = f"{du.free / (1024**3):.1f} GB / {du.total / (1024**3):.1f} GB"
except Exception:
    disk_line = "未知"

out.append("=== 云端实时状态 ===")
out.append(f"部署进程: {'运行中 (PID ' + dpid + ')' if alive else '未运行'}")
out.append(f"端口 :{port}: {'已监听' if p_check else '未监听'}")
out.append(f"ComfyUI 目录: {'已存在 (/content/ComfyUI)' if comfy_dir else '尚未安装'}")
out.append(f"Google Drive: {'已挂载' if drive_ok else '未挂载'}")
out.append(f"工作流同步: {'运行中 (PID ' + wf_pid + ')' if wf_alive else '未运行'}（Drive workflows 共 {wf_count} 个）")
out.append(f"本地磁盘剩余: {disk_line}")
if status_p.exists():
    raw = status_p.read_text(errors="ignore").strip()
    try:
        obj = json.loads(raw)
        out.append(f"阶段状态: {obj.get('metric_label','')} {obj.get('metric_value','')} {obj.get('metric_unit','')} — {obj.get('detail','')}")
    except Exception:
        out.append(f"阶段状态: {raw[:200]}")

if not deploy_log.exists() and not comfy_log.exists() and not status_p.exists():
    out.append("")
    out.append("云端机器上目前没有任何部署记录。")
    out.append("这台机器是刚开机或之前启动的裸机，尚未安装 ComfyUI，点击「立即部署」即可开始自动安装。")
else:
    if deploy_log.exists():
        text = deploy_log.read_text(errors="ignore").splitlines()
        out.append("")
        out.append(f"=== 部署日志 /tmp/colab-tools-deploy.log（最后 {min(len(text), lines)} 行） ===")
        out.extend(text[-lines:] if text else ["(空文件)"])
    if comfy_log.exists():
        ctext = comfy_log.read_text(errors="ignore").splitlines()
        out.append("")
        out.append(f"=== ComfyUI 日志 /tmp/comfyui.log（最后 {min(len(ctext), 30)} 行） ===")
        out.extend(ctext[-30:] if ctext else ["(空文件)"])

print("\n".join(out))
`, lines, port)

	r := e.RunColab(ctx, profile, []string{"exec", "-s", profile.SessionName}, py, 25*time.Second)
	if r.ExitCode != 0 {
		msg := strings.TrimSpace(r.Stderr)
		if msg == "" {
			msg = strings.TrimSpace(r.Stdout)
		}
		if msg == "" {
			msg = "无法连接云端会话读取日志，请稍后重试"
		}
		return "", errors.New(msg)
	}
	return strings.TrimSpace(runner.StripCR(r.Stdout)), nil
}

// FlushWorkspaceToDrive triggers a one-shot workflow & optional custom-node snapshot flush before stopping the VM.
func (e *Engine) FlushWorkspaceToDrive(ctx context.Context, profile core.AccountProfile) error {
	if !core.IsValidSessionName(profile.SessionName) {
		return errors.New("会话名不合法")
	}
	snapFlag := "0"
	if profile.RestoreCustomNodesSnapshot {
		snapFlag = "1"
	}
	py := fmt.Sprintf(`import os, pathlib, subprocess
if not pathlib.Path("/content/drive/MyDrive").is_dir():
    print("FLUSH_NO_DRIVE=1")
    raise SystemExit(2)
p = pathlib.Path("/tmp/colab-tools-flush.py")
if not p.is_file():
    print("FLUSH_SKIP=1")
    raise SystemExit(0)
env = os.environ.copy()
env["RESTORE_CUSTOM_NODES_SNAPSHOT"] = "%s"
r = subprocess.run(["python3", str(p)], env=env, text=True, capture_output=True)
if r.stdout:
    print(r.stdout.strip())
raise SystemExit(r.returncode)
`, snapFlag)
	r := e.RunColab(ctx, profile, []string{"exec", "-s", profile.SessionName}, py, 25*time.Second)
	out := strings.TrimSpace(runner.StripCR(r.Stdout))
	if strings.Contains(out, "FLUSH_SKIP=1") {
		return errors.New("云端未发现工作流同步脚本，无法确认最新工作流已同步")
	}
	if strings.Contains(out, "FLUSH_NO_DRIVE=1") {
		return errors.New("Google Drive 未挂载，工作流未能同步到 Drive")
	}
	if r.ExitCode != 0 || !strings.Contains(out, "FLUSH_OK") {
		msg := strings.TrimSpace(r.Stderr)
		if msg == "" {
			msg = out
		}
		if msg == "" {
			msg = "云端工作流同步失败"
		}
		return errors.New(msg)
	}
	return nil
}

func (e *Engine) RunDriveStartScript(ctx context.Context, profile core.AccountProfile) (bool, string, string) {
	escaped := strings.ReplaceAll(strings.ReplaceAll(profile.DriveScriptPath, `\`, `\\`), `"`, `\"`)
	py := fmt.Sprintf(`import subprocess, os
env = os.environ.copy()
env["LLAMA_PORT"] = "%d"
env["SERVICE_PORT"] = "%d"
env["WORKLOAD_KIND"] = "%s"
p = subprocess.Popen(["bash", "%s"], stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, bufsize=1, env=env)
for line in p.stdout:
    print(line, end="", flush=True)
rc = p.wait()
print(f"SCRIPT_EXIT_CODE={rc}", flush=True)
`, profile.RemoteServicePort, profile.RemoteServicePort, profile.WorkloadKind, escaped)
	r := e.RunColab(ctx, profile, []string{"exec", "-s", profile.SessionName}, py, 240*time.Second)
	if r.ExitCode == 0 && strings.Contains(r.Stdout, "SCRIPT_EXIT_CODE=0") &&
		(strings.Contains(r.Stdout, "SERVER_READY=1") || strings.Contains(r.Stdout, "WORKLOAD_READY=1")) {
		return true, r.Stdout, ""
	}
	reason := "远端启动脚本未返回就绪标志"
	if r.TimedOut {
		reason = "启动脚本超时"
	}
	return false, r.Stdout, reason
}

// StartBuiltinDeployScript uploads a built-in script via Base64 over stdin (no host path/WSL issues,
// immune to shell injection) and launches it detached in the background on the Colab VM.
func (e *Engine) StartBuiltinDeployScript(ctx context.Context, profile core.AccountProfile, scriptBody string, envVars map[string]string) error {
	if !core.IsValidSessionName(profile.SessionName) {
		return errors.New("会话名不合法")
	}
	scriptB64 := base64.StdEncoding.EncodeToString([]byte(scriptBody))
	envJSON, err := json.Marshal(envVars)
	if err != nil {
		return err
	}
	envB64 := base64.StdEncoding.EncodeToString(envJSON)

	py := fmt.Sprintf(`import base64, json, os, pathlib, signal, subprocess
script_path = pathlib.Path("/tmp/colab-tools-deploy.sh")
pid_path = pathlib.Path("/tmp/colab-tools-deploy.pid")
status_path = pathlib.Path("/tmp/colab-tools-status.json")
if pid_path.exists():
    try:
        old_pid = int(pid_path.read_text().strip())
        old_cmd_p = pathlib.Path(f"/proc/{old_pid}/cmdline")
        if old_cmd_p.exists() and "colab-tools-deploy.sh" in old_cmd_p.read_bytes().decode("utf-8", errors="ignore"):
            os.killpg(old_pid, signal.SIGTERM)
    except Exception:
        pass
status_path.unlink(missing_ok=True)
script_path.write_bytes(base64.b64decode("%s"))
os.chmod(script_path, 0o700)
extra = json.loads(base64.b64decode("%s").decode("utf-8"))
env = os.environ.copy()
for k, v in extra.items():
    env[str(k)] = str(v)
logf = open("/tmp/colab-tools-deploy.log", "wb")
proc = subprocess.Popen(["bash", "/tmp/colab-tools-deploy.sh"], stdout=logf, stderr=subprocess.STDOUT, stdin=subprocess.DEVNULL, env=env, start_new_session=True)
logf.close()
pid_path.write_text(str(proc.pid))
print("DEPLOY_LAUNCHED=1")
`, scriptB64, envB64)

	r := e.RunColab(ctx, profile, []string{"exec", "-s", profile.SessionName}, py, 40*time.Second)
	if r.ExitCode == 0 && strings.Contains(r.Stdout, "DEPLOY_LAUNCHED=1") {
		return nil
	}
	msg := strings.TrimSpace(r.Stderr)
	if msg == "" {
		msg = strings.TrimSpace(r.Stdout)
	}
	if msg == "" {
		msg = "未能把部署脚本送到云端机器"
	}
	return errors.New(msg)
}

func (e *Engine) ResolveColabURL(ctx context.Context, profile core.AccountProfile, sessionName string) string {
	target := sessionName
	if target == "" {
		target = profile.SessionName
	}
	if !core.IsValidSessionName(target) {
		return ""
	}
	r := e.RunColab(ctx, profile, []string{"url", "-s", target}, "", 15*time.Second)
	for _, raw := range strings.Split(runner.StripCR(r.Stdout), "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "https://") && core.IsValidEndpointURL(line) {
			return line
		}
	}
	return ""
}

func (e *Engine) ResolveColabPythonPath(ctx context.Context) string {
	r := e.Runner.RunShell(ctx, `head -n 1 "$(command -v colab)" | sed 's/^#!//'`, runner.RunOptions{Timeout: 6 * time.Second})
	p := strings.TrimSpace(r.Stdout)
	if r.ExitCode != 0 || p == "" {
		return ""
	}
	return p
}

func (e *Engine) BeginOfficialOAuthFlow(ctx context.Context) (*OAuthSessionTicket, error) {
	pyPath := e.ResolveColabPythonPath(ctx)
	if pyPath == "" {
		return nil, errors.New("未找到官方 google-colab-cli，请先在环境体检页完成安装")
	}
	script := `import json
from importlib import resources
from google_auth_oauthlib.flow import InstalledAppFlow
from colab_cli.auth import PUBLIC_SCOPES, REMOTE_REDIRECT_URI
cfg = json.loads(resources.files("colab_cli").joinpath("oauth_config.json").read_text())
flow = InstalledAppFlow.from_client_config(cfg, PUBLIC_SCOPES)
flow.redirect_uri = REMOTE_REDIRECT_URI
auth_url, _ = flow.authorization_url(prompt="consent", token_usage="remote")
print(json.dumps({"auth_url": auth_url, "code_verifier": flow.code_verifier or ""}))`

	r := e.Runner.Run(ctx, pyPath, []string{"-c", script}, runner.RunOptions{Timeout: 15 * time.Second})
	if r.ExitCode != 0 {
		msg := strings.TrimSpace(r.Stderr)
		if msg == "" {
			msg = "无法初始化官方 OAuth2 流程（需要 google-colab-cli 0.8.x）"
		}
		return nil, errors.New(msg)
	}
	var obj struct {
		AuthURL      string `json:"auth_url"`
		CodeVerifier string `json:"code_verifier"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(r.Stdout)), &obj); err != nil || obj.AuthURL == "" {
		return nil, errors.New("解析官方 OAuth2 链接失败")
	}
	return &OAuthSessionTicket{AuthURL: obj.AuthURL, CodeVerifier: obj.CodeVerifier, ColabPythonPath: pyPath}, nil
}

func (e *Engine) CompleteOfficialOAuthFlow(ctx context.Context, ticket *OAuthSessionTicket, code string) (string, error) {
	if ticket == nil {
		return "", errors.New("缺少授权会话，请重新获取授权链接")
	}
	script := `import json, sys
from importlib import resources
from google_auth_oauthlib.flow import InstalledAppFlow
from colab_cli.auth import PUBLIC_SCOPES, REMOTE_REDIRECT_URI
payload = json.loads(sys.stdin.read())
cfg = json.loads(resources.files("colab_cli").joinpath("oauth_config.json").read_text())
flow = InstalledAppFlow.from_client_config(cfg, PUBLIC_SCOPES)
flow.redirect_uri = REMOTE_REDIRECT_URI
if payload.get("code_verifier"):
    flow.code_verifier = payload["code_verifier"]
flow.fetch_token(code=payload["code"].strip())
print(flow.credentials.to_json())`

	inJSON, _ := json.Marshal(map[string]string{
		"code":          strings.TrimSpace(code),
		"code_verifier": ticket.CodeVerifier,
	})
	r := e.Runner.Run(ctx, ticket.ColabPythonPath, []string{"-c", script}, runner.RunOptions{
		Stdin:   string(inJSON),
		Timeout: 25 * time.Second,
	})
	if r.ExitCode != 0 {
		msg := strings.TrimSpace(r.Stderr)
		if msg == "" {
			msg = "授权码无效或已过期"
		}
		return "", errors.New(msg)
	}
	return strings.TrimSpace(r.Stdout), nil
}
