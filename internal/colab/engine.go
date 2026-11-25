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
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
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
	GPUName          string
	VRAMUsedMB       int
	VRAMTotalMB      int
	GPUUtil          *int
	CPUUtil          *int
	CUDAProcessCount int
	PublicURL        string
	DisplayUIURL     string
	ModelFile        string
	ModelAlias       string
	ContextLimit     int
	KVQuant          string
	Healthy          bool
	UptimeSeconds    int
	StatusPayload      *core.WorkloadStatusPayload
	StatusAgeSeconds   *int
	ComfyQueue         *core.ComfyQueueSummary
	DetectedWebPort    *int
	DeployAlive        bool
	RenderedVideoCount int
}

type ProbeAttemptRecord struct {
	Hardware  string
	Succeeded bool
}

type ProvisionRoundResult struct {
	Outcome           core.ProvisionOutcome
	UnsupportedValues map[string]bool
	ProbeRecords      []ProbeAttemptRecord
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

func ParseSessions(stdout string) []core.ColabSessionItem {
	var items []core.ColabSessionItem
	for _, raw := range strings.Split(runner.StripCR(stdout), "\n") {
		line := strings.TrimSpace(raw)
		if !strings.HasPrefix(line, "[") || !strings.Contains(line, "| Hardware:") {
			continue
		}
		closeIdx := strings.Index(line, "]")
		if closeIdx <= 1 {
			continue
		}
		name := line[1:closeIdx]
		rest := strings.TrimSpace(line[closeIdx+1:])
		parts := strings.Split(rest, "|")
		if len(parts) < 2 {
			continue
		}
		endpoint := strings.TrimSpace(parts[0])
		if endpoint == "" {
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
	return usage, ParseSessions(s.Stdout), nil
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
out_count = 0
for odir in [%q, "/content/drive/MyDrive/ColabTools/outputs", "/content/output"]:
    op = pathlib.Path(odir)
    if op.is_dir():
        try:
            out_count = sum(1 for f in op.iterdir() if f.is_file() and f.suffix.lower() in (".mp4", ".webm", ".mov", ".mkv", ".gif"))
            if out_count > 0:
                break
        except Exception:
            pass
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
		case strings.HasPrefix(raw, "URL="):
			u := strings.TrimSpace(strings.TrimPrefix(raw, "URL="))
			if core.IsValidEndpointURL(u) {
				t.PublicURL = u
			}
		case strings.HasPrefix(raw, "HEALTH="):
			t.Healthy = strings.Contains(raw, "ok")
		case strings.HasPrefix(raw, "DEPLOY_ALIVE="):
			t.DeployAlive = strings.TrimSpace(strings.TrimPrefix(raw, "DEPLOY_ALIVE=")) == "1"
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
				t.Healthy = true
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
		res.Outcome = core.ProvisionOutcome{Kind: core.OutcomeCircuitBroken, Code: "INVALID_SESSION", Message: "会话名不合法（仅允许字母、数字、._-）"}
		return res
	}
	targets := overrideTargets
	if len(targets) == 0 {
		targets = profile.PreferredAccelerator.ProbeTargets()
	}
	if len(targets) == 0 {
		res.Outcome = core.ProvisionOutcome{Kind: core.OutcomeCircuitBroken, Code: "NO_TARGET", Message: "未选择任何目标算力卡"}
		return res
	}

	lastOutcome := core.ProvisionOutcome{Kind: core.OutcomeCapacity503, Message: "当前目标算力无库存 (503)"}
	saw503 := false

	for idx, target := range targets {
		if ctx.Err() != nil {
			res.Outcome = core.ProvisionOutcome{Kind: core.OutcomeCircuitBroken, Code: "CANCELLED", Message: "已取消申请"}
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
					onProgress(fmt.Sprintf("准备尝试 %s（间隔 %ds）…", target.Display, int(pause.Seconds())))
				}
				select {
				case <-time.After(pause):
				case <-ctx.Done():
					res.Outcome = core.ProvisionOutcome{Kind: core.OutcomeCircuitBroken, Code: "CANCELLED", Message: "已取消申请"}
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
			onProgress(fmt.Sprintf("申请 %s…", target.Display))
		}
		args := []string{"new", "-s", profile.SessionName}
		if target.Flag != "" && target.Value != "" {
			args = append(args, target.Flag, target.Value)
		}
		if profile.HighMem && profile.Tier.SupportsHighMem() {
			args = append(args, "--high-mem")
		}
		r := e.RunColab(ctx, profile, args, "", stepTimeout)
		if r.ExitCode == 0 {
			res.ProbeRecords = append(res.ProbeRecords, ProbeAttemptRecord{Hardware: target.Display, Succeeded: true})
			res.Outcome = core.ProvisionOutcome{Kind: core.OutcomeAllocated, Hardware: target.Display}
			return res
		}
		outcome := core.ClassifyProvisionFailure(r.ExitCode, r.TimedOut, r.Stdout, r.Stderr, target.Display)
		switch outcome.Kind {
		case core.OutcomeAllocated:
			res.ProbeRecords = append(res.ProbeRecords, ProbeAttemptRecord{Hardware: target.Display, Succeeded: true})
			res.Outcome = outcome
			return res
		case core.OutcomeCircuitBroken, core.OutcomeBudgetExhausted:
			res.Outcome = outcome
			return res
		case core.OutcomeCapacity503:
			res.ProbeRecords = append(res.ProbeRecords, ProbeAttemptRecord{Hardware: target.Display, Succeeded: false})
			saw503 = true
			lastOutcome = outcome
		case core.OutcomeUnsupportedTier400:
			if target.Value != "" {
				res.UnsupportedValues[strings.ToUpper(target.Value)] = true
			}
			if !saw503 {
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
	checkPy := "import os\nprint('DRIVE_MOUNTED=1' if os.path.isdir('/content/drive/MyDrive') else 'DRIVE_MOUNTED=0')\n"
	check := e.RunColab(ctx, profile, []string{"exec", "-s", profile.SessionName}, checkPy, 20*time.Second)
	if check.ExitCode == 0 && strings.Contains(check.Stdout, "DRIVE_MOUNTED=1") {
		return true
	}
	m := e.RunColab(ctx, profile, []string{"drivemount", "-s", profile.SessionName, "/content/drive"}, "", 90*time.Second)
	if m.ExitCode != 0 {
		return false
	}
	verify := e.RunColab(ctx, profile, []string{"exec", "-s", profile.SessionName}, checkPy, 20*time.Second)
	return verify.ExitCode == 0 && strings.Contains(verify.Stdout, "DRIVE_MOUNTED=1")
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
if pid_path.exists():
    try:
        old_pid = int(pid_path.read_text().strip())
        if pathlib.Path(f"/proc/{old_pid}/cmdline").exists():
            os.killpg(old_pid, signal.SIGTERM)
    except Exception:
        pass
script_path.write_bytes(base64.b64decode("%s"))
os.chmod(script_path, 0o700)
extra = json.loads(base64.b64decode("%s").decode("utf-8"))
env = os.environ.copy()
for k, v in extra.items():
    env[str(k)] = str(v)
logf = open("/tmp/colab-tools-deploy.log", "wb")
proc = subprocess.Popen(["bash", "/tmp/colab-tools-deploy.sh"], stdout=logf, stderr=subprocess.STDOUT, stdin=subprocess.DEVNULL, env=env, start_new_session=True)
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
