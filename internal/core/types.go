package core

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

// SubscriptionTier enumerates official Google AI / Colab subscription tiers.
type SubscriptionTier string

const (
	TierAIUltra      SubscriptionTier = "ai_ultra"
	TierColabProPlus SubscriptionTier = "colab_pro_plus"
	TierAIPro        SubscriptionTier = "ai_pro"
	TierColabPro     SubscriptionTier = "colab_pro"
	TierPayAsYouGo   SubscriptionTier = "pay_as_you_go"
	TierFree         SubscriptionTier = "free"
)

var AllSubscriptionTiers = []SubscriptionTier{
	TierAIUltra,
	TierColabProPlus,
	TierAIPro,
	TierColabPro,
	TierPayAsYouGo,
	TierFree,
}

func (t SubscriptionTier) DisplayName() string {
	switch t {
	case TierAIUltra:
		return "Google AI Ultra"
	case TierColabProPlus:
		return "Colab Pro+"
	case TierAIPro:
		return "Google AI Pro"
	case TierColabPro:
		return "Colab Pro"
	case TierPayAsYouGo:
		return "Pay As You Go"
	case TierFree:
		return "Free 免费版"
	default:
		return "Colab Pro"
	}
}

func (t SubscriptionTier) ShortBadge() string {
	switch t {
	case TierAIUltra:
		return "AI Ultra"
	case TierColabProPlus:
		return "Pro+"
	case TierAIPro:
		return "AI Pro"
	case TierColabPro:
		return "Pro"
	case TierPayAsYouGo:
		return "PAYG"
	case TierFree:
		return "Free"
	default:
		return "Pro"
	}
}

func (t SubscriptionTier) MaxSessionHours() int {
	if t == TierFree {
		return 12
	}
	return 24
}

func (t SubscriptionTier) SupportsHighMem() bool {
	return t != TierFree
}

func (t SubscriptionTier) QueueableAccelerators() []AcceleratorOption {
	switch t {
	case TierAIUltra, TierColabProPlus:
		return []AcceleratorOption{AccG4, AccH100, AccA100, AccL4, AccT4, AccTPUV6e1, AccTPUV5e1}
	case TierAIPro, TierColabPro, TierPayAsYouGo:
		return []AcceleratorOption{AccA100, AccL4, AccT4, AccTPUV5e1}
	case TierFree:
		return []AcceleratorOption{AccT4}
	default:
		return []AcceleratorOption{AccA100, AccL4, AccT4}
	}
}

func (t SubscriptionTier) DefaultAccelerator() AcceleratorOption {
	switch t {
	case TierAIUltra, TierColabProPlus:
		return AccAutoBest
	case TierAIPro, TierColabPro, TierPayAsYouGo:
		return AccAutoPro
	case TierFree:
		return AccT4
	default:
		return AccAutoPro
	}
}

func SuggestedTier(balance float64) SubscriptionTier {
	if balance > 700 {
		return TierAIUltra
	}
	if balance > 150 {
		return TierColabProPlus
	}
	if balance > 0.01 {
		return TierColabPro
	}
	return TierFree
}

// AcceleratorOption represents official `colab new` hardware targets.
type AcceleratorOption string

const (
	AccAutoBest AcceleratorOption = "AUTO_BEST"
	AccAutoPro  AcceleratorOption = "AUTO_PRO"
	AccG4       AcceleratorOption = "G4"
	AccH100     AcceleratorOption = "H100"
	AccA100     AcceleratorOption = "A100"
	AccL4       AcceleratorOption = "L4"
	AccT4       AcceleratorOption = "T4"
	AccTPUV6e1  AcceleratorOption = "V6E1"
	AccTPUV5e1  AcceleratorOption = "V5E1"
	AccCPU      AcceleratorOption = "CPU"
)

var canonicalEligibleOrder = []AcceleratorOption{
	AccG4, AccH100, AccA100, AccL4, AccT4, AccTPUV6e1, AccTPUV5e1,
}

// NormalizeEligibleAccelerators parses raw server GPU/TPU codes (from ccu-info eligibleGpus/eligibleTpus)
// into a deduplicated slice ordered from highest-tier GPU to TPU.
func NormalizeEligibleAccelerators(raw []string) []AcceleratorOption {
	present := map[AcceleratorOption]bool{}
	for _, item := range raw {
		u := strings.ToUpper(strings.TrimSpace(item))
		u = strings.ReplaceAll(u, "-", "")
		u = strings.ReplaceAll(u, "_", "")
		switch u {
		case "G4":
			present[AccG4] = true
		case "H100":
			present[AccH100] = true
		case "A100":
			present[AccA100] = true
		case "L4":
			present[AccL4] = true
		case "T4":
			present[AccT4] = true
		case "V6E1":
			present[AccTPUV6e1] = true
		case "V5E1":
			present[AccTPUV5e1] = true
		}
	}
	if len(present) == 0 {
		return nil
	}
	out := make([]AcceleratorOption, 0, len(present))
	for _, opt := range canonicalEligibleOrder {
		if present[opt] {
			out = append(out, opt)
		}
	}
	return out
}

func IsValidAccelerator(raw string) bool {
	switch AcceleratorOption(raw) {
	case AccAutoBest, AccAutoPro, AccG4, AccH100, AccA100, AccL4, AccT4, AccTPUV6e1, AccTPUV5e1, AccCPU:
		return true
	default:
		return false
	}
}

func (a AcceleratorOption) Label() string {
	switch a {
	case AccAutoBest:
		return "自动高配 (G4→H100→A100→L4)"
	case AccAutoPro:
		return "自动优选 (A100→L4→T4)"
	case AccG4:
		return "GPU · G4 (RTX PRO 6000 96G)"
	case AccH100:
		return "GPU · H100 (80G)"
	case AccA100:
		return "GPU · A100 (40/80G)"
	case AccL4:
		return "GPU · L4 (24G · 性价比主力)"
	case AccT4:
		return "GPU · T4 (16G)"
	case AccTPUV6e1:
		return "TPU · v6e-1"
	case AccTPUV5e1:
		return "TPU · v5e-1"
	case AccCPU:
		return "CPU 标准环境"
	default:
		return string(a)
	}
}

func (a AcceleratorOption) ShortChipLabel() string {
	switch a {
	case AccAutoBest:
		return "自动高配"
	case AccAutoPro:
		return "自动优选"
	case AccG4:
		return "G4"
	case AccH100:
		return "H100"
	case AccA100:
		return "A100"
	case AccL4:
		return "L4"
	case AccT4:
		return "T4"
	case AccTPUV6e1:
		return "v6e-1"
	case AccTPUV5e1:
		return "v5e-1"
	case AccCPU:
		return "CPU"
	default:
		return string(a)
	}
}

func (a AcceleratorOption) SpecSubtitle() string {
	switch a {
	case AccG4:
		return "96GB 显存 · 约 7 CU/h"
	case AccH100:
		return "80GB HBM3 · 约 18 CU/h"
	case AccA100:
		return "40/80GB · 约 12 CU/h"
	case AccL4:
		return "24GB 显存 · 约 2.3 CU/h"
	case AccT4:
		return "16GB 显存 · 约 1.3 CU/h"
	case AccTPUV6e1:
		return "TPU v6e · 约 6 CU/h"
	case AccTPUV5e1:
		return "TPU v5e · 约 3 CU/h"
	case AccAutoBest:
		return "高配优先自动尝试"
	case AccAutoPro:
		return "常用卡自动优选"
	case AccCPU:
		return "标准 CPU 环境"
	default:
		return ""
	}
}

func (a AcceleratorOption) RoleTag() string {
	switch a {
	case AccG4:
		return "大显存主力"
	case AccH100:
		return "极速旗舰"
	case AccA100:
		return "训练/视频"
	case AccL4:
		return "性价比保底"
	case AccT4:
		return "轻量入门"
	case AccTPUV6e1:
		return "JAX/新TPU"
	case AccTPUV5e1:
		return "TPU 推理"
	default:
		return ""
	}
}

type ProbeTarget struct {
	Flag    string // "--gpu", "--tpu", or "" for CPU
	Value   string
	Display string
}

func (a AcceleratorOption) ProbeTargets() []ProbeTarget {
	switch a {
	case AccAutoBest:
		return []ProbeTarget{
			{Flag: "--gpu", Value: "G4", Display: "G4"},
			{Flag: "--gpu", Value: "H100", Display: "H100"},
			{Flag: "--gpu", Value: "A100", Display: "A100"},
			{Flag: "--gpu", Value: "L4", Display: "L4"},
		}
	case AccAutoPro:
		return []ProbeTarget{
			{Flag: "--gpu", Value: "A100", Display: "A100"},
			{Flag: "--gpu", Value: "L4", Display: "L4"},
			{Flag: "--gpu", Value: "T4", Display: "T4"},
		}
	case AccG4:
		return []ProbeTarget{{Flag: "--gpu", Value: "G4", Display: "G4"}}
	case AccH100:
		return []ProbeTarget{{Flag: "--gpu", Value: "H100", Display: "H100"}}
	case AccA100:
		return []ProbeTarget{{Flag: "--gpu", Value: "A100", Display: "A100"}}
	case AccL4:
		return []ProbeTarget{{Flag: "--gpu", Value: "L4", Display: "L4"}}
	case AccT4:
		return []ProbeTarget{{Flag: "--gpu", Value: "T4", Display: "T4"}}
	case AccTPUV6e1:
		return []ProbeTarget{{Flag: "--tpu", Value: "v6e1", Display: "TPU v6e-1"}}
	case AccTPUV5e1:
		return []ProbeTarget{{Flag: "--tpu", Value: "v5e1", Display: "TPU v5e-1"}}
	case AccCPU:
		return []ProbeTarget{{Flag: "", Value: "", Display: "CPU"}}
	default:
		return []ProbeTarget{{Flag: "--gpu", Value: "L4", Display: "L4"}}
	}
}

func EstimatedHourlyCU(rawHardware string) float64 {
	g := strings.ToUpper(rawHardware)
	switch {
	case strings.Contains(g, "H100"):
		return 18.0
	case strings.Contains(g, "A100"):
		return 12.0
	case strings.Contains(g, "G4") || strings.Contains(g, "6000"):
		return 7.0
	case strings.Contains(g, "L4"):
		return 2.33
	case strings.Contains(g, "T4"):
		return 1.3
	case strings.Contains(g, "V6E"):
		return 6.0
	case strings.Contains(g, "V5E"):
		return 3.0
	default:
		return 0.1
	}
}

// WorkloadKind represents the 3 active compute workload profiles.
type WorkloadKind string

const (
	WorkloadVideo   WorkloadKind = "video"
	WorkloadLLM     WorkloadKind = "llm"
	WorkloadGeneral WorkloadKind = "general"
)

var AllWorkloadKinds = []WorkloadKind{
	WorkloadVideo,
	WorkloadLLM,
	WorkloadGeneral,
}

func ParseWorkloadKind(raw string) (WorkloadKind, bool) {
	w := WorkloadKind(strings.ToLower(strings.TrimSpace(raw)))
	if slices.Contains(AllWorkloadKinds, w) {
		return w, true
	}
	return WorkloadGeneral, false
}

func (w WorkloadKind) DisplayName() string {
	switch w {
	case WorkloadLLM:
		return "LLM 大模型"
	case WorkloadVideo:
		return "视频生成"
	default:
		return "训练 / 通用"
	}
}

func (w WorkloadKind) ShortBadge() string {
	switch w {
	case WorkloadLLM:
		return "LLM"
	case WorkloadVideo:
		return "视频"
	default:
		return "通用"
	}
}

func (w WorkloadKind) DefaultRemotePort() int {
	switch w {
	case WorkloadLLM:
		return 18080
	case WorkloadVideo:
		return 8188
	default:
		return 8888
	}
}

func (w WorkloadKind) WebUIButtonTitle() string {
	switch w {
	case WorkloadVideo:
		return "WebUI"
	default:
		return "网页"
	}
}

type AccountAuthMode string

const (
	AuthSystemDefaultOAuth2 AccountAuthMode = "system_oauth2"
	AuthIsolatedVaultADC    AccountAuthMode = "isolated_keychain_adc"
)

type StatusBarDisplayMode string

const (
	StatusBarIconOnly StatusBarDisplayMode = "icon_only"
	StatusBarCompact  StatusBarDisplayMode = "compact"
	StatusBarDetailed StatusBarDisplayMode = "detailed"
)

type AccountColorTag string

const (
	ColorAmber   AccountColorTag = "#F59E0B"
	ColorEmerald AccountColorTag = "#10B981"
	ColorSky     AccountColorTag = "#3B82F6"
	ColorViolet  AccountColorTag = "#8B5CF6"
	ColorCoral   AccountColorTag = "#EF4444"
	ColorCyan    AccountColorTag = "#06B6D4"
)

var AllAccountColors = []AccountColorTag{ColorAmber, ColorEmerald, ColorSky, ColorViolet, ColorCoral, ColorCyan}

// DeployRecipe defines one-click built-in deployment presets shipped inside Colab Tools.
type DeployRecipe string

const (
	RecipeNone           DeployRecipe = "none"
	RecipeH3Video        DeployRecipe = "h3_video"
	RecipeComfyUIGeneral DeployRecipe = "comfyui_general"
	RecipeLLMProxy       DeployRecipe = "llm_proxy"
)

func (r DeployRecipe) DisplayName() string {
	switch r {
	case RecipeH3Video:
		return "H3 视频模型"
	case RecipeComfyUIGeneral:
		return "通用 ComfyUI"
	case RecipeLLMProxy:
		return "大模型代理"
	default:
		return "只开机不装东西"
	}
}

func (r DeployRecipe) IsVideoComfy() bool {
	return r == RecipeH3Video || r == RecipeComfyUIGeneral
}

// H3Variant selects which MiniMax H3 / HyperFlow H3 diffusion weights to deploy on top of the shared 21.5GB base.
type H3Variant string

const (
	H3VariantFlash     H3Variant = "flash"      // HyperFlow H3 Flash 13.7B INT8 fast
	H3VariantOriginal  H3Variant = "original"   // Official MiniMax H3 31B
	H3VariantFlashGGUF H3Variant = "flash_gguf" // HyperFlow H3 Flash GGUF (24GB L4 friendly)
	H3VariantCustom    H3Variant = "custom"     // Custom HuggingFace repo/files or Drive-only
)

func (v H3Variant) DisplayName() string {
	switch v {
	case H3VariantOriginal:
		return "H3 原版 (31B)"
	case H3VariantFlashGGUF:
		return "H3 Flash 轻量 GGUF"
	case H3VariantCustom:
		return "自定义 / 仅用 Drive"
	default:
		return "H3 Flash 极速版"
	}
}

var (
	hfRepoRegex = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*$`)
	hfFileRegex = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)
)

func IsValidHFRepo(repo string) bool {
	r := strings.TrimSpace(repo)
	return r != "" && len(r) <= 160 && hfRepoRegex.MatchString(r) && !strings.Contains(r, "..")
}

func SanitizeCustomHFFiles(raw string) string {
	var clean []string
	for _, part := range strings.Split(raw, ",") {
		f := strings.TrimSpace(part)
		if f == "" || len(f) > 220 || strings.HasPrefix(f, "/") || strings.Contains(f, "..") || !hfFileRegex.MatchString(f) {
			continue
		}
		clean = append(clean, f)
	}
	return strings.Join(clean, ",")
}

func SanitizeDriveSubpath(raw, fallback string) string {
	p := strings.TrimSpace(raw)
	if p == "" {
		return fallback
	}
	if strings.Contains(p, "..") || strings.ContainsAny(p, "\r\n\t;`$|&><\"'") {
		return fallback
	}
	if !strings.HasPrefix(p, "/content/drive/MyDrive") {
		p = "/content/drive/MyDrive/" + strings.TrimLeft(p, "/")
	}
	return strings.TrimRight(p, "/")
}

// BuildDeployEnv constructs validated, injection-safe environment variables for the unified ComfyUI deploy script.
func BuildDeployEnv(acc AccountProfile) (map[string]string, error) {
	acc.Sanitize()
	boolStr := func(b bool) string {
		if b {
			return "1"
		}
		return "0"
	}
	recipe := "general"
	if acc.DeployRecipe == RecipeH3Video {
		recipe = "h3"
	}
	cloudIdleStop := 0
	if acc.AutoShutdownEnabled {
		cloudIdleStop = max(acc.IdleTimeoutMinutes+15, 30)
	}
	env := map[string]string{
		"COMFY_RECIPE":                  recipe,
		"SERVICE_PORT":                  fmt.Sprintf("%d", acc.RemoteServicePort),
		"WORKLOAD_KIND":                 string(acc.WorkloadKind),
		"USE_GOOGLE_DRIVE":              boolStr(acc.UseGoogleDrive),
		"DRIVE_MODELS_DIR":              acc.DriveModelsDir,
		"DRIVE_OUTPUT_DIR":              acc.DriveOutputDir,
		"DRIVE_WHEELS_DIR":              "/content/drive/MyDrive/ColabTools/wheels",
		"CACHE_BASE_TO_DRIVE":           boolStr(acc.UseGoogleDrive),
		"RESTORE_CUSTOM_NODES_SNAPSHOT": boolStr(acc.RestoreCustomNodesSnapshot),
		"IDLE_AUTO_STOP_MINUTES":        fmt.Sprintf("%d", cloudIdleStop),
	}
	if acc.DeployRecipe == RecipeH3Video {
		env["H3_VARIANT"] = string(acc.H3Variant)
		env["H3_GGUF_QUANT"] = acc.H3GGUFQuant
		driveOnly := acc.H3Variant == H3VariantCustom && acc.H3DriveOnly
		env["H3_DRIVE_ONLY"] = boolStr(driveOnly)
		env["H3_ENABLE_SOL_ATTN"] = "1"
		if acc.H3Variant == H3VariantCustom && !driveOnly {
			repo := strings.TrimSpace(acc.H3CustomHFRepo)
			files := SanitizeCustomHFFiles(acc.H3CustomHFFiles)
			if !IsValidHFRepo(repo) || files == "" {
				return nil, fmt.Errorf("自定义模式请填合法的 HuggingFace 仓库（如 owner/repo）和文件名，或勾选「只用我 Drive 里的现成模型」")
			}
			env["H3_CUSTOM_HF_REPO"] = repo
			env["H3_CUSTOM_HF_FILES"] = files
		}
	}
	return env, nil
}

// BuildLLMDeployEnv constructs validated environment variables for the built-in LLM proxy deploy script.
func BuildLLMDeployEnv(acc AccountProfile) map[string]string {
	acc.Sanitize()
	useDrive := "0"
	if acc.UseGoogleDrive {
		useDrive = "1"
	}
	env := map[string]string{
		"LLAMA_PORT":       fmt.Sprintf("%d", acc.RemoteServicePort),
		"SERVICE_PORT":     fmt.Sprintf("%d", acc.RemoteServicePort),
		"WORKLOAD_KIND":    string(WorkloadLLM),
		"USE_GOOGLE_DRIVE": useDrive,
	}
	if repo := strings.TrimSpace(acc.LLMHFRepo); IsValidHFRepo(repo) {
		env["HF_REPO"] = repo
	}
	if alias := strings.TrimSpace(acc.LLMModelAlias); IsValidModelAlias(alias) {
		env["MODEL_ALIAS"] = alias
	}
	return env
}

// GPUStrategyMode unifies card picking into 3 beginner-friendly modes so users never juggle dual settings.
type GPUStrategyMode string

const (
	GPUStrategyAutoSmart GPUStrategyMode = "auto_smart" // 智能挑卡（按所选任务自动配最稳顺位，没货自动顺延）
	GPUStrategyEcoSaver  GPUStrategyMode = "eco_saver"  // 省点模式（优先 L4 / T4）
	GPUStrategyCustom    GPUStrategyMode = "custom"     // 高级自定义（自选顺位 + 保底 + 定时预约）
)

func (g GPUStrategyMode) DisplayName() string {
	switch g {
	case GPUStrategyEcoSaver:
		return "省点模式"
	case GPUStrategyCustom:
		return "高级自定义"
	default:
		return "智能挑卡"
	}
}

func isH3VideoRecipe(acc AccountProfile) bool {
	return !acc.EnableLLMModule && acc.DeployRecipe == RecipeH3Video
}

// IsSmallVRAMHardware reports whether the hardware label refers to a small-VRAM GPU (L4 or T4).
func IsSmallVRAMHardware(hardware string) bool {
	u := strings.ToUpper(hardware)
	return strings.Contains(u, "L4") || strings.Contains(u, "T4")
}

// ShouldCrossModelDowngrade reports whether an H3 31B account with explicit cross-model fallback
// was allocated a small-VRAM card (L4/T4) and should automatically downgrade weights.
func ShouldCrossModelDowngrade(acc AccountProfile, allocatedHardware string) bool {
	return isH3VideoRecipe(acc) &&
		acc.H3Variant == H3VariantOriginal &&
		acc.AllowCrossModelFallback &&
		IsSmallVRAMHardware(allocatedHardware)
}

// EffectiveQueueableAccelerators returns the authoritative hardware pool for this account:
// server-reported eligibleGpus/eligibleTpus when available, falling back to Tier presets otherwise.
func (a AccountProfile) EffectiveQueueableAccelerators() []AcceleratorOption {
	if len(a.EligibleAccelerators) > 0 {
		raw := make([]string, 0, len(a.EligibleAccelerators))
		for _, opt := range a.EligibleAccelerators {
			raw = append(raw, string(opt))
		}
		if norm := NormalizeEligibleAccelerators(raw); len(norm) > 0 {
			return norm
		}
	}
	return a.Tier.QueueableAccelerators()
}

// HasHighTierGPU reports whether this account is eligible for G4 or H100 high-tier cards.
func (a AccountProfile) HasHighTierGPU() bool {
	allowed := a.EffectiveQueueableAccelerators()
	return slices.Contains(allowed, AccG4) || slices.Contains(allowed, AccH100)
}

// IsFreeOnly reports whether this account is restricted to the free T4-only pool.
func (a AccountProfile) IsFreeOnly() bool {
	if len(a.EligibleAccelerators) > 0 {
		allowed := a.EffectiveQueueableAccelerators()
		return len(allowed) == 1 && allowed[0] == AccT4
	}
	return a.Tier == TierFree
}

func (a AccountProfile) SupportsHighMem() bool {
	return !a.IsFreeOnly()
}

func (a AccountProfile) MaxSessionHours() int {
	if a.IsFreeOnly() {
		return 12
	}
	return 24
}

func (a AccountProfile) EligibleCardsSummary() string {
	allowed := a.EffectiveQueueableAccelerators()
	if len(allowed) == 0 {
		return "待识别"
	}
	parts := make([]string, 0, len(allowed))
	for _, opt := range allowed {
		parts = append(parts, opt.ShortChipLabel())
	}
	return strings.Join(parts, " / ")
}

func (a AccountProfile) DisplayTierBadge() string {
	if a.TierUserConfirmed {
		return a.Tier.ShortBadge()
	}
	if len(a.EligibleAccelerators) > 0 {
		if a.HasHighTierGPU() {
			return "高配卡池"
		}
		if a.IsFreeOnly() {
			return "免费卡池"
		}
		return "付费卡池"
	}
	return a.Tier.ShortBadge()
}

// SmartQueueTargets derives the most reliable GPU priority order for an account's real hardware eligibility and task.
func SmartQueueTargets(acc AccountProfile) []AcceleratorOption {
	if acc.IsFreeOnly() {
		return []AcceleratorOption{AccT4}
	}
	if !acc.HasHighTierGPU() {
		if isH3VideoRecipe(acc) {
			switch acc.H3Variant {
			case H3VariantOriginal:
				// 31B Original strictly requires A100 on standard paid accounts; never put L4/T4 in the queue.
				return []AcceleratorOption{AccA100}
			case H3VariantFlash:
				// H3 Flash needs >=24GB VRAM; never put 16GB T4 in the queue.
				return []AcceleratorOption{AccA100, AccL4}
			case H3VariantFlashGGUF:
				return []AcceleratorOption{AccL4, AccA100}
			}
		}
		return []AcceleratorOption{AccA100, AccL4}
	}
	// High-tier accounts (eligible for G4 / H100)
	if isH3VideoRecipe(acc) {
		switch acc.H3Variant {
		case H3VariantOriginal:
			// 31B Original stays strictly on high-VRAM GPUs and never blindly falls back to 24GB L4.
			return []AcceleratorOption{AccG4, AccA100, AccH100}
		case H3VariantFlashGGUF:
			return []AcceleratorOption{AccL4, AccG4, AccA100}
		}
	}
	// H3 Flash, General ComfyUI, LLM Proxy, Pure Compute:
	// prioritize G4 and A100 in primary rounds, then EffectiveFallback appends L4 from round 3.
	return []AcceleratorOption{AccG4, AccA100}
}

func ecoSaverQueueTargets(acc AccountProfile) []AcceleratorOption {
	if acc.IsFreeOnly() {
		return []AcceleratorOption{AccT4}
	}
	if isH3VideoRecipe(acc) {
		switch acc.H3Variant {
		case H3VariantOriginal:
			if acc.HasHighTierGPU() {
				return []AcceleratorOption{AccA100, AccG4}
			}
			return []AcceleratorOption{AccA100}
		case H3VariantFlash, H3VariantFlashGGUF:
			return []AcceleratorOption{AccL4}
		}
	}
	return []AcceleratorOption{AccL4, AccT4}
}

// EffectiveQueueTargets is the single source of truth for which cards will be attempted and in what order.
func EffectiveQueueTargets(acc AccountProfile) []AcceleratorOption {
	allowed := acc.EffectiveQueueableAccelerators()
	switch acc.GPUStrategy {
	case GPUStrategyEcoSaver:
		return SanitizedQueueTargetsWithAllowed(ecoSaverQueueTargets(acc), allowed)
	case GPUStrategyCustom:
		return SanitizedQueueTargetsWithAllowed(acc.QueueTargets, allowed)
	default:
		return SanitizedQueueTargetsWithAllowed(SmartQueueTargets(acc), allowed)
	}
}

// EffectiveFallback returns two-stage fallback settings aligned with GPUStrategy.
func EffectiveFallback(acc AccountProfile) (enabled bool, afterRounds int, target AcceleratorOption) {
	switch acc.GPUStrategy {
	case GPUStrategyEcoSaver:
		return false, 2, AccT4
	case GPUStrategyCustom:
		return acc.QueueFallbackEnabled, acc.QueueFallbackAfterRounds, acc.QueueFallbackTarget
	default:
		if acc.IsFreeOnly() {
			return false, 2, AccT4
		}
		if isH3VideoRecipe(acc) {
			switch acc.H3Variant {
			case H3VariantOriginal:
				return acc.AllowCrossModelFallback, 2, AccL4
			case H3VariantFlashGGUF:
				return false, 2, AccL4
			case H3VariantFlash:
				if acc.HasHighTierGPU() {
					return true, 2, AccL4
				}
				return false, 2, AccL4
			}
		}
		if acc.HasHighTierGPU() {
			return true, 2, AccL4
		}
		return true, 2, AccT4
	}
}

// EffectiveUseHighMem determines whether --high-mem should be requested.
func EffectiveUseHighMem(acc AccountProfile) bool {
	if !acc.SupportsHighMem() {
		return false
	}
	switch acc.GPUStrategy {
	case GPUStrategyEcoSaver:
		return false
	case GPUStrategyCustom:
		return acc.HighMem
	default:
		return true
	}
}

// EffectiveGPUPlanSummary returns a concise plain-Chinese description of the active GPU strategy.
func EffectiveGPUPlanSummary(acc AccountProfile) string {
	targets := EffectiveQueueTargets(acc)
	return fmt.Sprintf("%s（%s）", acc.GPUStrategy.DisplayName(), QueueTargetsSummary(targets))
}

// CheckTaskHardwareCompatibility blocks starting a billable GPU when the user's selected task/custom targets
// have no valid intersection with the account's eligible hardware pool.
func CheckTaskHardwareCompatibility(acc AccountProfile) error {
	allowed := acc.EffectiveQueueableAccelerators()
	if len(allowed) == 0 {
		return fmt.Errorf("当前账号暂无可用加速卡配额，请先点右上角刷新状态或在 Colab 网页核对订阅")
	}
	if acc.GPUStrategy == GPUStrategyCustom {
		hasAny := false
		for _, opt := range acc.QueueTargets {
			if slices.Contains(allowed, opt) {
				hasAny = true
				break
			}
		}
		if !hasAny {
			return fmt.Errorf("你自定义的显卡顺位与当前账号可用显卡（%s）没有交集，请先到「调整策略与进阶选项」重新勾选", acc.EligibleCardsSummary())
		}
		return nil
	}
	if isH3VideoRecipe(acc) && acc.H3Variant == H3VariantOriginal && !acc.AllowCrossModelFallback {
		hasBigVRAM := slices.Contains(allowed, AccG4) || slices.Contains(allowed, AccH100) || slices.Contains(allowed, AccA100)
		if !hasBigVRAM {
			return fmt.Errorf("H3 原版 (31B) 需要 A100/G4/H100 级别大显存，当前账号可用显卡（%s）无法直接运行；请改选「H3 Flash 极速版」「轻量 GGUF」或在策略里开启保底降级", acc.EligibleCardsSummary())
		}
	}
	return nil
}

// SessionEffectiveProfile returns a profile copy anchored to the live running session's locked mode and service port,
// ensuring telemetry probes, auto-redeploy, workflow flush, and billing labels never drift to next-boot edits.
func SessionEffectiveProfile(acc AccountProfile, rt AccountRuntimeState) AccountProfile {
	if !rt.Phase.IsRunning() || !rt.SessionLocked {
		return acc
	}
	eff := acc
	eff.DeployRecipe = rt.SessionRecipe
	eff.H3Variant = rt.SessionH3Variant
	eff.WorkloadKind = rt.SessionWorkloadKind
	eff.EnableLLMModule = rt.SessionEnableLLM
	if rt.SessionIdleTimeoutMin >= 5 {
		eff.IdleTimeoutMinutes = rt.SessionIdleTimeoutMin
	}
	if eff.EnableLLMModule || eff.DeployRecipe == RecipeLLMProxy {
		if eff.RemoteServicePort == 8188 || eff.RemoteServicePort <= 0 {
			eff.RemoteServicePort = 18080
		}
	} else if eff.DeployRecipe.IsVideoComfy() {
		if eff.RemoteServicePort == 18080 || eff.RemoteServicePort <= 0 {
			eff.RemoteServicePort = 8188
		}
	}
	return eff
}

// PrelaunchSummary returns a concise plain-Chinese pre-start overview: priority GPUs, estimated CU/h, and output destination.
func PrelaunchSummary(acc AccountProfile) string {
	targets := EffectiveQueueTargets(acc)
	tgtText := QueueTargetsSummary(targets)
	rateText := "费率待定"
	if acc.IsFreeOnly() {
		if acc.Tier == TierFree && len(acc.EligibleAccelerators) == 0 {
			rateText = "免费池"
		} else {
			rateText = "免费池/约 1.3 CU/h"
		}
	} else if len(targets) > 0 {
		var rates []string
		for i, t := range targets {
			if i >= 2 {
				break
			}
			r := EstimatedHourlyCU(string(t))
			if r == math.Trunc(r) {
				rates = append(rates, fmt.Sprintf("%.0f", r))
			} else {
				rates = append(rates, fmt.Sprintf("%.1f", r))
			}
		}
		rateText = fmt.Sprintf("约 %s CU/h", strings.Join(rates, "/"))
	}
	dest := "纯开机"
	switch {
	case acc.EnableLLMModule || acc.DeployRecipe == RecipeLLMProxy:
		dest = "大模型代理"
	case acc.DeployRecipe.IsVideoComfy():
		if acc.UseGoogleDrive {
			dest = "成片存 Google Drive"
		} else {
			dest = "成片仅存云电脑（关机清空）"
		}
	}
	return fmt.Sprintf("优先抢 %s · %s · %s", tgtText, rateText, dest)
}

// EffectiveIdleTimeoutMinutes returns the single unified idle shutdown timeout shared by the watchdog loop and UI countdowns.
func EffectiveIdleTimeoutMinutes(acc AccountProfile) int {
	if acc.IdleTimeoutMinutes < 5 {
		return 40
	}
	return acc.IdleTimeoutMinutes
}

// AccountProfile persists a single Google Colab account's preferences.
type AccountProfile struct {
	ID                   string            `json:"id"`
	DisplayName          string            `json:"displayName"`
	Email                string            `json:"email"`
	ColorTag             AccountColorTag     `json:"colorTag"`
	Tier                 SubscriptionTier    `json:"tier"`
	TierUserConfirmed    bool                `json:"tierUserConfirmed,omitempty"`
	EligibleAccelerators []AcceleratorOption `json:"eligibleAccelerators,omitempty"`
	AuthMode             AccountAuthMode     `json:"authMode"`
	SessionName          string            `json:"sessionName"`
	GPUStrategy          GPUStrategyMode   `json:"gpuStrategy,omitempty"`
	PreferredAccelerator AcceleratorOption `json:"preferredAccelerator"`
	HighMem              bool              `json:"highMem"`
	WorkloadKind         WorkloadKind      `json:"workloadKind"`
	EnableLLMModule      bool              `json:"enableLLMModule"`

	// Built-in Video / H3 One-Click Deployment & Google Drive Sync
	DeployRecipe               DeployRecipe `json:"deployRecipe,omitempty"`
	H3Variant                  H3Variant    `json:"h3Variant,omitempty"`
	H3GGUFQuant                string       `json:"h3GGUFQuant,omitempty"`
	H3CustomHFRepo             string       `json:"h3CustomHFRepo,omitempty"`
	H3CustomHFFiles            string       `json:"h3CustomHFFiles,omitempty"`
	H3DriveOnly                bool         `json:"h3DriveOnly,omitempty"`
	UseGoogleDrive             bool         `json:"useGoogleDrive"`
	DriveModelsDir             string       `json:"driveModelsDir,omitempty"`
	DriveOutputDir             string       `json:"driveOutputDir,omitempty"`
	RestoreCustomNodesSnapshot bool         `json:"restoreCustomNodesSnapshot,omitempty"`

	// Built-in LLM Proxy optional overrides (empty uses generic open defaults)
	LLMHFRepo     string `json:"llmHFRepo,omitempty"`
	LLMModelAlias string `json:"llmModelAlias,omitempty"`

	// Polite queue & persistent watch
	QueuePersistentMode      bool                `json:"queuePersistentMode,omitempty"`
	QueuePersistentMaxHours  int                 `json:"queuePersistentMaxHours,omitempty"`
	QueueTargets             []AcceleratorOption `json:"queueTargets"`
	QueueFallbackEnabled     bool                `json:"queueFallbackEnabled"`
	QueueFallbackAfterRounds int                 `json:"queueFallbackAfterRounds"`
	QueueFallbackTarget      AcceleratorOption   `json:"queueFallbackTarget"`
	AllowCrossModelFallback  bool                `json:"allowCrossModelFallback,omitempty"`

	// Idle watchdog
	AutoShutdownEnabled bool `json:"autoShutdownEnabled"`
	IdleTimeoutMinutes  int  `json:"idleTimeoutMinutes"`
	WarnBeforeMinutes   int  `json:"warnBeforeMinutes"`

	// Workload script & client sync
	DriveScriptPath    string `json:"driveScriptPath"`
	AutoSyncCCSwitch   bool   `json:"autoSyncCCSwitch"`
	AutoSyncOpenCode   bool   `json:"autoSyncOpenCode"`
	AutoSyncCodex      bool   `json:"autoSyncCodex"`
	CCSwitchProviderID string `json:"ccSwitchProviderId"`
	CCSwitchName       string `json:"ccSwitchProviderName"`
	LocalSSHTunnelPort int    `json:"localSSHTunnelPort"`
	RemoteServicePort  int    `json:"remoteServicePort"`
}

func NewDefaultAccountProfile(name, email string, tier SubscriptionTier, authMode AccountAuthMode) AccountProfile {
	id := uuid.New().String()
	shortID := strings.ToLower(strings.ReplaceAll(id, "-", ""))
	if len(shortID) > 6 {
		shortID = shortID[:6]
	}
	if name == "" {
		name = "主账号"
	}
	defaultTargets := tier.QueueableAccelerators()
	if len(defaultTargets) > 2 {
		defaultTargets = defaultTargets[:2]
	}
	fallback := DefaultFallbackTarget(defaultTargets, tier)
	return AccountProfile{
		ID:                         id,
		DisplayName:                name,
		Email:                      email,
		ColorTag:                   ColorAmber,
		Tier:                       tier,
		AuthMode:                   authMode,
		SessionName:                fmt.Sprintf("colab-%s", shortID),
		GPUStrategy:                GPUStrategyAutoSmart,
		PreferredAccelerator:       tier.DefaultAccelerator(),
		HighMem:                    tier.SupportsHighMem(),
		WorkloadKind:               WorkloadVideo,
		EnableLLMModule:            false,
		DeployRecipe:               RecipeH3Video,
		H3Variant:                  H3VariantFlash,
		H3GGUFQuant:                "Q4_K_M",
		UseGoogleDrive:             true,
		DriveModelsDir:             "",
		DriveOutputDir:             "/content/drive/MyDrive/ColabTools/outputs",
		RestoreCustomNodesSnapshot: false,
		QueuePersistentMode:        false,
		QueuePersistentMaxHours:    QueuePersistentDefaultHours,
		QueueTargets:               defaultTargets,
		QueueFallbackEnabled:       false,
		QueueFallbackAfterRounds:   2,
		QueueFallbackTarget:        fallback,
		AutoShutdownEnabled:        true,
		IdleTimeoutMinutes:         10,
		WarnBeforeMinutes:          2,
		DriveScriptPath:            "/content/drive/MyDrive/colab-llm/scripts/start_server.sh",
		AutoSyncCCSwitch:           false,
		AutoSyncOpenCode:           false,
		AutoSyncCodex:              false,
		CCSwitchProviderID:         "colab-tools-" + shortID,
		CCSwitchName:               fmt.Sprintf("Colab (%s)", name),
		LocalSSHTunnelPort:         8188,
		RemoteServicePort:          8188,
	}
}

// Sanitize repairs unknown enum values or out-of-range fields after decoding untrusted/future JSON.
func (a *AccountProfile) Sanitize() {
	if a.ID == "" {
		a.ID = uuid.New().String()
	}
	if a.DisplayName == "" {
		a.DisplayName = "主账号"
	}
	if !slices.Contains(AllSubscriptionTiers, a.Tier) {
		a.Tier = TierColabPro
	}
	if a.AuthMode == "isolated_adc" {
		a.AuthMode = AuthIsolatedVaultADC
	} else if a.AuthMode == "" {
		a.AuthMode = AuthSystemDefaultOAuth2
	}
	a.SessionName = SanitizeSessionName(a.SessionName)
	switch a.GPUStrategy {
	case GPUStrategyAutoSmart, GPUStrategyEcoSaver, GPUStrategyCustom:
	default:
		a.GPUStrategy = GPUStrategyAutoSmart
	}
	if !IsValidAccelerator(string(a.PreferredAccelerator)) {
		a.PreferredAccelerator = a.Tier.DefaultAccelerator()
	}
	if _, ok := ParseWorkloadKind(string(a.WorkloadKind)); !ok {
		if a.EnableLLMModule {
			a.WorkloadKind = WorkloadLLM
		} else {
			a.WorkloadKind = WorkloadGeneral
		}
	}
	if a.WorkloadKind == WorkloadLLM {
		a.EnableLLMModule = true
	}
	switch a.DeployRecipe {
	case RecipeNone, RecipeH3Video, RecipeComfyUIGeneral, RecipeLLMProxy:
	default:
		if a.EnableLLMModule {
			a.DeployRecipe = RecipeLLMProxy
		} else {
			a.DeployRecipe = RecipeNone
		}
	}
	switch a.H3Variant {
	case H3VariantFlash, H3VariantOriginal, H3VariantFlashGGUF, H3VariantCustom:
	default:
		a.H3Variant = H3VariantFlash
	}
	switch a.H3GGUFQuant {
	case "Q4_K_M", "Q6_K", "Q8_0":
	default:
		a.H3GGUFQuant = "Q4_K_M"
	}
	if a.DriveModelsDir != "" {
		a.DriveModelsDir = SanitizeDriveSubpath(a.DriveModelsDir, "")
	}
	a.DriveOutputDir = SanitizeDriveSubpath(a.DriveOutputDir, "/content/drive/MyDrive/ColabTools/outputs")
	if len(a.EligibleAccelerators) > 0 {
		raw := make([]string, 0, len(a.EligibleAccelerators))
		for _, opt := range a.EligibleAccelerators {
			raw = append(raw, string(opt))
		}
		a.EligibleAccelerators = NormalizeEligibleAccelerators(raw)
	}
	allowed := a.EffectiveQueueableAccelerators()
	a.QueueTargets = SanitizedQueueTargetsWithAllowed(a.QueueTargets, allowed)
	if a.QueuePersistentMaxHours <= 0 {
		a.QueuePersistentMaxHours = QueuePersistentDefaultHours
	} else {
		a.QueuePersistentMaxHours = clampInt(a.QueuePersistentMaxHours, 1, 24)
	}
	if a.QueueFallbackAfterRounds < 1 {
		a.QueueFallbackAfterRounds = 1
	}
	if a.QueueFallbackAfterRounds > QueueMaxAttempts-1 {
		a.QueueFallbackAfterRounds = QueueMaxAttempts - 1
	}
	if !IsValidAccelerator(string(a.QueueFallbackTarget)) || !slices.Contains(allowed, a.QueueFallbackTarget) {
		a.QueueFallbackTarget = DefaultFallbackTargetWithAllowed(a.QueueTargets, allowed)
	}
	if a.IdleTimeoutMinutes < 5 {
		a.IdleTimeoutMinutes = 40
	}
	if a.WarnBeforeMinutes < 0 {
		a.WarnBeforeMinutes = 2
	}
	if a.RemoteServicePort < 1024 || a.RemoteServicePort > 65535 {
		a.RemoteServicePort = a.WorkloadKind.DefaultRemotePort()
	}
	if a.LocalSSHTunnelPort < 1024 || a.LocalSSHTunnelPort > 65535 {
		a.LocalSSHTunnelPort = a.RemoteServicePort
	}
	if a.CCSwitchProviderID == "" {
		compact := strings.ReplaceAll(strings.ToLower(a.ID), "-", "")
		if len(compact) > 6 {
			compact = compact[:6]
		}
		a.CCSwitchProviderID = "colab-tools-" + compact
	}
}

func (a AccountProfile) IsolatedConfigFilePath() string {
	if a.AuthMode != AuthIsolatedVaultADC {
		return ""
	}
	return filepath.Join(ConfigDir(), "profiles", a.ID, "sessions.json")
}

const CurrentConfigSchemaVersion = 1

// AppSettings is the root persisted workspace configuration.
type AppSettings struct {
	SchemaVersion            int                  `json:"schemaVersion,omitempty"`
	Accounts                 []AccountProfile     `json:"accounts"`
	SelectedAccountID        string               `json:"selectedAccountId"`
	StatusBarMode            StatusBarDisplayMode `json:"statusBarMode"`
	BackgroundRefreshSeconds int                  `json:"backgroundRefreshSeconds"`
	ManualRefreshDebounceSec int                  `json:"manualRefreshDebounceSeconds"`
	EnableNotifications      bool                 `json:"enableNotifications"`
	HasCompletedOnboarding   bool                 `json:"hasCompletedOnboarding"`
	ThemePreference          string               `json:"themePreference"` // "system", "dark", "light"
	QueueCooldownsByAccount  map[string]time.Time `json:"queueCooldownsByAccount,omitempty"`
}

func DefaultAppSettings() AppSettings {
	return AppSettings{
		SchemaVersion:            CurrentConfigSchemaVersion,
		Accounts:                 []AccountProfile{},
		StatusBarMode:            StatusBarCompact,
		BackgroundRefreshSeconds: 180,
		ManualRefreshDebounceSec: 5,
		EnableNotifications:      true,
		HasCompletedOnboarding:   false,
		ThemePreference:          "dark",
		QueueCooldownsByAccount:  map[string]time.Time{},
	}
}

// ConfigDir returns the unified macOS/Unix config directory (~/.config/colab-tools).
func ConfigDir() string {
	if custom := strings.TrimSpace(os.Getenv("COLAB_TOOLS_CONFIG_DIR")); custom != "" {
		return custom
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".", ".colab-tools")
	}
	return filepath.Join(home, ".config", "colab-tools")
}

func DefaultConfigPath() string {
	return filepath.Join(ConfigDir(), "config.json")
}

var appleRefEpoch = time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)

func parseFlexTimestamp(v any) (time.Time, bool) {
	switch tv := v.(type) {
	case string:
		if t, err := time.Parse(time.RFC3339Nano, tv); err == nil {
			return t, true
		}
		if t, err := time.Parse(time.RFC3339, tv); err == nil {
			return t, true
		}
	case float64:
		// Swift JSONEncoder default DateEncodingStrategy encodes seconds since 2001-01-01T00:00:00Z.
		// Heuristic: timestamps > 1_000_000_000 (~2001 in Unix) are Unix epoch; smaller modern values (~7e8) are Apple reference epoch.
		if tv > 1_000_000_000 {
			sec := int64(tv)
			nsec := int64((tv - float64(sec)) * 1e9)
			return time.Unix(sec, nsec).UTC(), true
		}
		sec := int64(tv)
		nsec := int64((tv - float64(sec)) * 1e9)
		return appleRefEpoch.Add(time.Duration(sec)*time.Second + time.Duration(nsec)*time.Nanosecond), true
	}
	return time.Time{}, false
}

// normalizeLegacySettingsJSON migrates Swift JSONEncoder numeric dates and legacy cooldown keys
// before Go's typed unmarshal runs so upgrading from v0.3.6 never drops accounts or active cooldowns.
func normalizeLegacySettingsJSON(raw []byte) ([]byte, bool, error) {
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, false, err
	}
	_, hasOnboardingKey := doc["hasCompletedOnboarding"]

	if accs, ok := doc["accounts"].([]any); ok {
		for _, item := range accs {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			// Populate friendly video/Drive defaults for accounts upgraded from earlier versions
			if _, hasRecipe := m["deployRecipe"]; !hasRecipe {
				if _, exists := m["useGoogleDrive"]; !exists {
					m["useGoogleDrive"] = true
				}
			}
			if _, exists := m["queuePersistentMaxHours"]; !exists {
				m["queuePersistentMaxHours"] = QueuePersistentDefaultHours
			}
			// Populate v0.3.10 unified GPU strategy cleanly when upgrading from earlier configs
			if _, hasStrat := m["gpuStrategy"]; !hasStrat {
				if pq, ok := m["enablePoliteQueue"].(bool); ok && pq {
					m["gpuStrategy"] = string(GPUStrategyCustom)
				} else {
					m["gpuStrategy"] = string(GPUStrategyAutoSmart)
				}
			}
		}
	}

	mergedCooldowns := map[string]string{}
	absorbCooldowns := func(key string) {
		rawMap, ok := doc[key].(map[string]any)
		if !ok {
			return
		}
		for accID, rawVal := range rawMap {
			if t, ok := parseFlexTimestamp(rawVal); ok && t.After(time.Now()) {
				if prevStr, exists := mergedCooldowns[accID]; exists {
					if prevT, err := time.Parse(time.RFC3339Nano, prevStr); err == nil && prevT.After(t) {
						continue
					}
				}
				mergedCooldowns[accID] = t.Format(time.RFC3339Nano)
			}
		}
	}
	absorbCooldowns("circuitBreakUntilByAccount")
	absorbCooldowns("queueCooldownsByAccount")
	if len(mergedCooldowns) > 0 {
		doc["queueCooldownsByAccount"] = mergedCooldowns
	} else {
		delete(doc, "queueCooldownsByAccount")
	}
	delete(doc, "circuitBreakUntilByAccount")

	out, err := json.Marshal(doc)
	if err != nil {
		return nil, hasOnboardingKey, err
	}
	return out, hasOnboardingKey, nil
}

func LoadAppSettings(path string) AppSettings {
	if path == "" {
		path = DefaultConfigPath()
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return DefaultAppSettings()
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return DefaultAppSettings()
	}

	normalized, hasOnboardingKey, normErr := normalizeLegacySettingsJSON(raw)
	if normErr != nil {
		// Preserve unreadable existing config as a backup instead of silently destroying it
		backupPath := fmt.Sprintf("%s.corrupt.%d", path, time.Now().Unix())
		_ = os.WriteFile(backupPath, raw, 0o600)
		return DefaultAppSettings()
	}

	s := DefaultAppSettings()
	if err := json.Unmarshal(normalized, &s); err != nil {
		backupPath := fmt.Sprintf("%s.corrupt.%d", path, time.Now().Unix())
		_ = os.WriteFile(backupPath, raw, 0o600)
		return DefaultAppSettings()
	}
	if s.SchemaVersion > CurrentConfigSchemaVersion {
		backupPath := fmt.Sprintf("%s.future-v%d.%d", path, s.SchemaVersion, time.Now().Unix())
		_ = os.WriteFile(backupPath, raw, 0o600)
	}
	if s.SchemaVersion <= 0 {
		s.SchemaVersion = CurrentConfigSchemaVersion
	}
	if !hasOnboardingKey && len(s.Accounts) > 0 {
		s.HasCompletedOnboarding = true
	}
	if s.BackgroundRefreshSeconds < 60 {
		s.BackgroundRefreshSeconds = 180
	}
	if s.ManualRefreshDebounceSec < 3 {
		s.ManualRefreshDebounceSec = 5
	}
	if s.QueueCooldownsByAccount == nil {
		s.QueueCooldownsByAccount = map[string]time.Time{}
	}
	for i := range s.Accounts {
		s.Accounts[i].Sanitize()
	}
	return s
}

func (s AppSettings) Save(path string) error {
	if path == "" {
		path = DefaultConfigPath()
	}
	if s.SchemaVersion <= 0 {
		s.SchemaVersion = CurrentConfigSchemaVersion
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, fmt.Sprintf(".config.%s.tmp", uuid.New().String()[:8]))
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// InstancePhase represents the live lifecycle of an account's primary session.
type InstancePhaseKind string

const (
	PhaseOffline  InstancePhaseKind = "offline"
	PhaseQueuing  InstancePhaseKind = "queuing"
	PhaseStarting InstancePhaseKind = "starting"
	PhaseRunning  InstancePhaseKind = "running"
	PhaseStopping InstancePhaseKind = "stopping"
	PhaseError    InstancePhaseKind = "error"
)

type InstancePhase struct {
	Kind            InstancePhaseKind
	Detail          string
	QueueAttempt    int
	QueueMax        int
	QueuePersistent bool
	QueueMaxHours   int
	NextRetryAt     *time.Time
	TargetsSummary  string
}

type ActionPromptKind string

const (
	PromptDriveAuth            ActionPromptKind = "drive_auth"
	PromptComfyHelp            ActionPromptKind = "comfy_help"
	PromptConfirmStop          ActionPromptKind = "confirm_stop"
	PromptConfirmStopAll       ActionPromptKind = "confirm_stop_all"
	PromptConfirmRemoveAccount ActionPromptKind = "confirm_remove_account"
	PromptConfirmQuit          ActionPromptKind = "confirm_quit"
	PromptTempOutputGuard        ActionPromptKind = "temp_output_guard"
	PromptWebStopGuide           ActionPromptKind = "web_stop_guide"
	PromptSwitchModeWhileRunning ActionPromptKind = "switch_mode_running"
)

// UserActionPrompt 描述需要用户在桌面弹窗中确认或处理的小白向引导操作。
type UserActionPrompt struct {
	ID                  string
	Kind                ActionPromptKind
	AccountID           string
	SessionName         string
	ExpectedEndpoint    string
	Title               string
	Message             string
	URL                 string
	PrimaryLabel        string
	PrimaryAction       string
	SecondaryLabel      string
	SecondaryAction     string
	CancelLabel         string
	TargetRecipe        DeployRecipe
	TargetH3Variant     H3Variant
	HasDownloadableTemp bool
}

// SessionVideoOutput describes a single video produced during the current running session.
type SessionVideoOutput struct {
	Filename     string `json:"filename"`
	Subfolder    string `json:"subfolder,omitempty"`
	Type         string `json:"type,omitempty"`
	SavedToDrive bool   `json:"savedToDrive"`
}

func (p InstancePhase) IsRunning() bool { return p.Kind == PhaseRunning }
func (p InstancePhase) IsQueuing() bool { return p.Kind == PhaseQueuing }
func (p InstancePhase) IsBusy() bool    { return p.Kind == PhaseStarting || p.Kind == PhaseStopping }

type ColabSessionItem struct {
	Name     string
	Endpoint string
	Hardware string
	Shape    string
	Variant  string
	IsOrphan bool
}

type ActivitySignalKind int

const (
	SignalUnknown ActivitySignalKind = iota
	SignalBusy
	SignalIdle
)

type ActivitySignal struct {
	Kind       ActivitySignalKind
	ObservedAt time.Time
}

func (s ActivitySignal) IsBusy() bool { return s.Kind == SignalBusy }

type ActivitySignalSource string

const (
	SignalSourceNone                ActivitySignalSource = "none"
	SignalSourceLLMSlots            ActivitySignalSource = "llm_slots"
	SignalSourceStatusFile          ActivitySignalSource = "status_file"
	SignalSourceComfyQueue          ActivitySignalSource = "comfy_queue"
	SignalSourceHardwareDualLowLoad ActivitySignalSource = "hardware_dual_low_load"
)

type SlotInfo struct {
	ID            *int  `json:"id"`
	NCtx          *int  `json:"n_ctx"`
	IsProcessing  *bool `json:"is_processing"`
	IDTask        *int  `json:"id_task"`
	NPromptTokens *int  `json:"n_prompt_tokens"`
}

// AccountRuntimeState holds live telemetry and ephemeral runtime bookkeeping for an account.
type AccountRuntimeState struct {
	Generation        int
	Phase             InstancePhase
	CUBalance         float64
	CURatePerHour     float64
	ActiveAssignments int
	Sessions          []ColabSessionItem

	ActiveEndpoint   string
	ActiveHardware   string
	GPUName          string
	VRAMUsedMB       int
	VRAMTotalMB      int
	RAMUsedMB        int
	RAMTotalMB       int
	GPUUtilPercent   int
	CPUUtilPercent   int
	CUDAProcessCount int
	SessionUptimeSec int

	// Strictly separated LLM API endpoint vs non-LLM display WebUI URL
	ModelFile           string
	ModelAlias          string
	ContextLimit        int
	KVQuant             string
	PublicURL           string
	LLMURLBoundEndpoint string
	DisplayUIURL        string
	ServerHealthy       bool

	DetectedWorkloadKind       *WorkloadKind
	WorkloadName               string
	WorkloadMetricLabel        string
	WorkloadMetricValue        string
	WorkloadMetricUnit         string
	WorkloadMetricRatio        *float64
	WorkloadDetail             string
	QueueRunning               int
	QueuePending               int
	DetectedServicePort        *int
	QueueInFlight              bool
	DeployRunning              bool
	DeployPreLaunch            bool
	DeployFailed               bool
	DeployError                string
	DeployStartingUntil        time.Time
	DeployNotifiedFailEndpoint string
	DeployLogTail              string
	DeployLogLoading           bool
	RenderedVideoCount             int
	SessionNewVideoCount           int
	SessionOutputs                 []SessionVideoOutput
	DownloadingOutput              bool
	SessionIsVideoComfy            bool
	SessionLocked                  bool
	SessionRecipe                  DeployRecipe
	SessionH3Variant               H3Variant
	SessionWorkloadKind            WorkloadKind
	SessionEnableLLM               bool
	SessionIdleTimeoutMin          int
	AutoOpenedEndpoint             string
	ComfyOpening                   bool
	DriveMountedKnown              bool
	DriveActuallyMounted           bool
	EffectiveOutputDir             string
	TempOutputVideos               int
	TempOutputScanKnown            bool
	WatchdogDeferredForTempOutputs bool
	TempGuardGraceUntil            time.Time

	ActivitySignal ActivitySignal
	SignalSource   ActivitySignalSource
	LastTaskID     int
	PromptTokens   int
	GPUUtilHistory []float64
	VRAMHistory    []float64

	LastActiveAt         time.Time
	IdleSeconds          int
	WarnedForCurrentIdle bool

	UsageFetching         bool
	LastUsageFetchAt      *time.Time
	CCSwitchSynced        bool
	OpenCodeSynced        bool
	CodexSynced           bool
	SSHTunnelActive       bool
	ActiveSSHTunnelPort   *int
	QueueCircuitBreakTill *time.Time
	LastError             string
	LastErrorTitle        string
	LastErrorAction       string // "", "retry", "stop_existing", "reauth", "clear_cooldown"
	LastErrorRaw          string

	// In-memory runtime ledger for current active session & last allocation
	SessionStartedAt          time.Time
	SessionStartBalance       float64
	SessionStartBalanceValid  bool
	SessionAdopted            bool
	SessionMaxAssignments     int
	SessionMeasuredSingleRate float64
	SessionEstimatedCU        float64
	SessionLastSampleAt       time.Time
	SessionFallbackUsed       bool
	SessionModelDowngraded    bool
	SessionUsedStandardRAM    bool
	LastAllocHadShortage      bool
	QueueStartedAt            time.Time
	LastUnexpectedStopMsg     string
}

func NewAccountRuntimeState() AccountRuntimeState {
	return AccountRuntimeState{
		Phase:        InstancePhase{Kind: PhaseOffline},
		ModelAlias:   "qwen3.8-27b-uncensored",
		ContextLimit: 262144,
		KVQuant:      "q4_0",
		LastTaskID:   -1,
		LastActiveAt: time.Now(),
	}
}

func (rt *AccountRuntimeState) AppendTelemetrySample(gpuUtil int, vramRatio float64) {
	rt.GPUUtilHistory = append(rt.GPUUtilHistory, math.Min(100, math.Max(0, float64(gpuUtil))))
	if len(rt.GPUUtilHistory) > 24 {
		rt.GPUUtilHistory = rt.GPUUtilHistory[len(rt.GPUUtilHistory)-24:]
	}
	rt.VRAMHistory = append(rt.VRAMHistory, math.Min(1, math.Max(0, vramRatio))*100)
	if len(rt.VRAMHistory) > 24 {
		rt.VRAMHistory = rt.VRAMHistory[len(rt.VRAMHistory)-24:]
	}
}

func (rt AccountRuntimeState) IsFreeRuntimeRestricted(acc AccountProfile) bool {
	return acc.IsFreeOnly() && rt.CUBalance <= 0.01
}

// EffectiveSessionState returns the active runtime's locked mode when running,
// falling back to account next-boot preferences when offline or unlocked.
func (rt AccountRuntimeState) EffectiveSessionState(acc AccountProfile) (DeployRecipe, H3Variant, WorkloadKind, bool) {
	if rt.Phase.IsRunning() && rt.SessionLocked {
		return rt.SessionRecipe, rt.SessionH3Variant, rt.SessionWorkloadKind, rt.SessionEnableLLM
	}
	return acc.DeployRecipe, acc.H3Variant, acc.WorkloadKind, acc.EnableLLMModule
}

func (rt AccountRuntimeState) EffectiveSessionEnableLLM(acc AccountProfile) bool {
	_, _, _, isLLM := rt.EffectiveSessionState(acc)
	return isLLM
}

func (rt AccountRuntimeState) EffectiveSessionIdleTimeout(acc AccountProfile) int {
	if rt.Phase.IsRunning() && rt.SessionLocked && rt.SessionIdleTimeoutMin >= 5 {
		return rt.SessionIdleTimeoutMin
	}
	return EffectiveIdleTimeoutMinutes(acc)
}

func (rt AccountRuntimeState) EffectiveSessionTaskLabel(acc AccountProfile) string {
	recipe, variant, _, isLLM := rt.EffectiveSessionState(acc)
	if isLLM || recipe == RecipeLLMProxy {
		return RecipeLLMProxy.DisplayName()
	}
	if recipe == RecipeH3Video {
		return variant.DisplayName()
	}
	return recipe.DisplayName()
}

// HasPendingModeSwitch reports whether the user changed next-boot task preferences while a different mode is running.
func (rt AccountRuntimeState) HasPendingModeSwitch(acc AccountProfile) bool {
	if !rt.Phase.IsRunning() || !rt.SessionLocked {
		return false
	}
	if rt.SessionRecipe != acc.DeployRecipe || rt.SessionEnableLLM != acc.EnableLLMModule {
		return true
	}
	if rt.SessionRecipe == RecipeH3Video && rt.SessionH3Variant != acc.H3Variant {
		return true
	}
	return false
}

// IsTempOutputActive reports whether the running video session is writing to ephemeral cloud disk.
func (rt AccountRuntimeState) IsTempOutputActive(acc AccountProfile) bool {
	if !rt.Phase.IsRunning() {
		return false
	}
	recipe, _, kind, isLLM := rt.EffectiveSessionState(acc)
	if isLLM || (!recipe.IsVideoComfy() && kind != WorkloadVideo && !rt.SessionIsVideoComfy) {
		return false
	}
	if strings.HasPrefix(rt.EffectiveOutputDir, "/content/output") || strings.HasPrefix(rt.EffectiveOutputDir, "/content/ComfyUI/output") {
		return true
	}
	if !acc.UseGoogleDrive {
		return true
	}
	return rt.DriveMountedKnown && !rt.DriveActuallyMounted
}

func (rt AccountRuntimeState) RemainingTempGuardGraceSeconds(now time.Time) int {
	if !rt.WatchdogDeferredForTempOutputs || rt.TempGuardGraceUntil.IsZero() {
		return 0
	}
	rem := int(math.Ceil(rt.TempGuardGraceUntil.Sub(now).Seconds()))
	if rem < 0 {
		return 0
	}
	return rem
}

func (rt AccountRuntimeState) IsTPUHardware() bool {
	src := rt.GPUName
	if src == "" {
		src = rt.ActiveHardware
	}
	u := strings.ToUpper(src)
	return strings.Contains(u, "TPU") || strings.Contains(u, "V6E") || strings.Contains(u, "V5E") || strings.Contains(u, "V2-") || strings.Contains(u, "V3-")
}

func (rt AccountRuntimeState) VRAMRatio() float64 {
	if rt.VRAMTotalMB <= 0 {
		return 0
	}
	return math.Min(1, math.Max(0, float64(rt.VRAMUsedMB)/float64(rt.VRAMTotalMB)))
}

func (rt AccountRuntimeState) ContextRatio() float64 {
	if rt.ContextLimit <= 0 {
		return 0
	}
	return math.Min(1, math.Max(0, float64(rt.PromptTokens)/float64(rt.ContextLimit)))
}

func (rt AccountRuntimeState) ShortGPUName() string {
	src := rt.GPUName
	if src == "" {
		src = rt.ActiveHardware
	}
	u := strings.ToUpper(src)
	switch {
	case strings.Contains(u, "H100"):
		return "H100"
	case strings.Contains(u, "A100"):
		return "A100"
	case strings.Contains(u, "6000") || strings.Contains(u, "G4"):
		return "G4"
	case strings.Contains(u, "L4"):
		return "L4"
	case strings.Contains(u, "T4"):
		return "T4"
	case strings.Contains(u, "V6E"):
		return "TPU v6e"
	case strings.Contains(u, "V5E"):
		return "TPU v5e"
	case strings.Contains(u, "CPU"):
		return "CPU"
	case src == "":
		return "Runtime"
	default:
		return src
	}
}

func (rt AccountRuntimeState) ShortModelName() string {
	if rt.ModelFile == "" {
		return rt.ModelAlias
	}
	s := strings.ReplaceAll(rt.ModelFile, ".gguf", "")
	s = strings.ReplaceAll(s, "Huihui-", "")
	return s
}

// EstimatedHoursRemaining uses live runtime rate when running, and the account's target GPU (or L4 baseline) when offline.
func (rt AccountRuntimeState) EstimatedHoursRemaining(acc AccountProfile) (hours float64, basis string, ok bool) {
	if rt.CUBalance <= 0 {
		return 0, "", false
	}
	if rt.Phase.IsRunning() {
		rate := rt.CURatePerHour
		if rate <= 0 {
			hw := rt.GPUName
			if hw == "" {
				hw = rt.ActiveHardware
			}
			rate = EstimatedHourlyCU(hw)
		}
		if rate <= 0 {
			return 0, "", false
		}
		return rt.CUBalance / rate, rt.ShortGPUName(), true
	}

	ref := acc.PreferredAccelerator
	if targets := EffectiveQueueTargets(acc); len(targets) > 0 {
		ref = targets[0]
	}
	switch ref {
	case AccG4:
		basis = "G4"
	case AccH100:
		basis = "H100"
	case AccA100:
		basis = "A100"
	case AccL4:
		basis = "L4"
	case AccT4:
		basis = "T4"
	case AccTPUV6e1:
		basis = "TPU v6e"
	case AccTPUV5e1:
		basis = "TPU v5e"
	case AccCPU:
		basis = "CPU"
	default:
		if acc.IsFreeOnly() {
			basis = "T4"
		} else {
			basis = "L4"
		}
	}
	rate := EstimatedHourlyCU(basis)
	if rate <= 0 {
		return 0, "", false
	}
	return rt.CUBalance / rate, basis, true
}

func (rt AccountRuntimeState) RemainingCapSeconds(tier SubscriptionTier) int {
	rem := tier.MaxSessionHours()*3600 - rt.SessionUptimeSec
	if rem < 0 {
		return 0
	}
	return rem
}

func (rt AccountRuntimeState) RemainingCapSecondsForAccount(acc AccountProfile) int {
	rem := acc.MaxSessionHours()*3600 - rt.SessionUptimeSec
	if rem < 0 {
		return 0
	}
	return rem
}

func (rt AccountRuntimeState) RemainingIdleSeconds(timeoutMinutes int) int {
	m := timeoutMinutes
	if m < 5 {
		m = 5
	}
	rem := m*60 - rt.IdleSeconds
	if rem < 0 {
		return 0
	}
	return rem
}

// IsDeployFailedStatus reports whether a remote /tmp/colab-tools-status.json payload explicitly marks a failed deploy.
func IsDeployFailedStatus(p *WorkloadStatusPayload) bool {
	if p == nil {
		return false
	}
	label := strings.TrimSpace(p.MetricLabel)
	val := strings.TrimSpace(p.MetricValue)
	return label == "部署中断" || val == "失败"
}

// HasVRAMWarningForRecipe flags when the running GPU clearly has insufficient VRAM for the selected H3 video model.
func (rt AccountRuntimeState) HasVRAMWarningForRecipe(acc AccountProfile) (bool, string) {
	if !rt.Phase.IsRunning() || acc.EnableLLMModule || acc.DeployRecipe != RecipeH3Video {
		return false, ""
	}
	isSmallCard := false
	vramGB := float64(rt.VRAMTotalMB) / 1024.0
	if rt.VRAMTotalMB > 0 && rt.VRAMTotalMB <= 26000 {
		isSmallCard = true
	} else {
		hw := strings.ToUpper(rt.ShortGPUName() + " " + rt.ActiveHardware)
		if strings.Contains(hw, "L4") || strings.Contains(hw, "T4") {
			isSmallCard = true
			if vramGB <= 0 {
				if strings.Contains(hw, "T4") {
					vramGB = 15.0
				} else {
					vramGB = 22.5
				}
			}
		}
	}
	if !isSmallCard {
		return false, ""
	}
	switch acc.H3Variant {
	case H3VariantOriginal:
		return true, fmt.Sprintf("当前是 %s（%.1f GB 显存），跑不动 H3 原版 (31B)。建议改用「H3 Flash 轻量 GGUF」或关机重抢 G4/A100/H100。", rt.ShortGPUName(), vramGB)
	case H3VariantFlash:
		if rt.VRAMTotalMB > 0 && rt.VRAMTotalMB <= 17000 {
			return true, fmt.Sprintf("当前 %s 显存仅 %.1f GB，跑 H3 Flash 极速版易爆显存，建议换「H3 Flash 轻量 GGUF」。", rt.ShortGPUName(), vramGB)
		}
	}
	return false, ""
}

// InputValidator blocks shell metacharacters and invalid URLs/identifiers across all platforms.
var (
	sessionNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	modelAliasRe  = regexp.MustCompile(`^[A-Za-z0-9._:@/-]{1,128}$`)
)

func IsValidSessionName(name string) bool {
	return sessionNameRe.MatchString(name)
}

func SanitizeSessionName(raw string) string {
	clean := strings.TrimSpace(raw)
	if IsValidSessionName(clean) {
		return clean
	}
	var b strings.Builder
	for _, r := range clean {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			b.WriteRune(r)
		}
	}
	out := strings.TrimLeft(b.String(), "._-")
	if len(out) > 64 {
		out = out[:64]
	}
	if IsValidSessionName(out) {
		return out
	}
	return "colab-session"
}

func IsValidModelAlias(alias string) bool {
	if alias == "" || strings.Contains(alias, "..") {
		return false
	}
	return modelAliasRe.MatchString(alias)
}

func IsValidEndpointURL(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || strings.ContainsAny(trimmed, " \t\r\n'\"`$;&|<>") {
		return false
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "https" && scheme != "http" {
		return false
	}
	return u.Host != ""
}

func FormatCU(v float64) string {
	return fmt.Sprintf("%.1f", v)
}

func FormatDuration(seconds int) string {
	if seconds < 0 {
		seconds = 0
	}
	h := seconds / 3600
	m := (seconds % 3600) / 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

// FormatStopwatch renders elapsed seconds as HH:MM:SS for the live power indicator stopwatch.
func FormatStopwatch(seconds int) string {
	if seconds < 0 {
		seconds = 0
	}
	h := seconds / 3600
	m := (seconds % 3600) / 60
	s := seconds % 60
	return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}

func FormatTokens(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1fK", float64(n)/1000.0)
	}
	return fmt.Sprintf("%d", n)
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
