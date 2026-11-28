package integrations

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/LJMStark/Colab-Tools/internal/core"
	"github.com/LJMStark/Colab-Tools/internal/runner"
	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

var (
	ErrInvalidEndpoint    = errors.New("服务端点地址格式无效")
	ErrInvalidModelAlias  = errors.New("模型别名包含非法字符")
	ErrInvalidIdentifier  = errors.New("配置标识符不合法")
	ErrUnmanagedConflict  = errors.New("检测到非本应用托管的同名配置，为安全已取消写入")
	ErrMalformedConfig    = errors.New("现有配置文件格式异常或读取失败，已保留原文件")
	ErrUnsafeFilePath     = errors.New("目标路径或备份目录不安全，已取消写入")
	ErrDatabaseNotFound   = errors.New("未检测到 CC Switch 数据库 (~/.cc-switch/cc-switch.db)")
	ErrActiveProviderLock = errors.New("目标渠道当前处于激活或官方状态，已跳过覆写以保护配置")
	ErrBackupFailed       = errors.New("数据库在线备份失败，已中止写入")
	ErrIO                 = errors.New("读写配置文件失败")
)

var (
	keyRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	envRe = regexp.MustCompile(`^[A-Z0-9_]{1,80}$`)
	mu    sync.Mutex
)

type DetectionSnapshot struct {
	CCSwitchInstalled   bool
	OpenCodeInstalled   bool
	CodexInstalled      bool
	ClaudeCodeInstalled bool
}

type AccountIdentifiers struct {
	OpenCodeProviderKey string
	CodexTableKey       string
	EnvKey              string
}

func IdentifiersForAccount(accountID string) AccountIdentifiers {
	compact := strings.ToLower(strings.ReplaceAll(accountID, "-", ""))
	if compact == "" {
		compact = "default"
	}
	return AccountIdentifiers{
		OpenCodeProviderKey: "colab-tools-" + compact,
		CodexTableKey:       "colab_tools_" + compact,
		EnvKey:              "COLAB_TOOLS_API_KEY_" + strings.ToUpper(compact),
	}
}

type CopyProtocolKind string

const (
	ProtocolOpenAI    CopyProtocolKind = "OpenAI (/v1)"
	ProtocolAnthropic CopyProtocolKind = "Anthropic"
)

func userHome() string {
	h, _ := os.UserHomeDir()
	return h
}

func DefaultCCSwitchDBPath() string {
	return filepath.Join(userHome(), ".cc-switch", "cc-switch.db")
}

func DefaultCCSwitchBackupDir() string {
	return filepath.Join(userHome(), ".cc-switch", "backups")
}

func DefaultOpenCodeConfigPath() string {
	return filepath.Join(userHome(), ".config", "opencode", "opencode.json")
}

func DefaultCodexConfigPath() string {
	return filepath.Join(userHome(), ".codex", "config.toml")
}

func DefaultBackupDir() string {
	return filepath.Join(core.ConfigDir(), "backups")
}

func DetectInstalledTools() DetectionSnapshot {
	home := userHome()
	hasBin := func(name string) bool {
		if _, err := exec.LookPath(name); err == nil {
			return true
		}
		for _, d := range []string{filepath.Join(home, ".local", "bin"), "/opt/homebrew/bin", "/usr/local/bin"} {
			if st, err := os.Stat(filepath.Join(d, name)); err == nil && !st.IsDir() {
				return true
			}
		}
		return false
	}
	exists := func(p string) bool {
		_, err := os.Stat(p)
		return err == nil
	}

	return DetectionSnapshot{
		CCSwitchInstalled:   exists(DefaultCCSwitchDBPath()),
		OpenCodeInstalled:   exists(DefaultOpenCodeConfigPath()) || exists(filepath.Join(home, ".config", "opencode")) || hasBin("opencode"),
		CodexInstalled:      exists(DefaultCodexConfigPath()) || exists(filepath.Join(home, ".codex")) || hasBin("codex"),
		ClaudeCodeInstalled: hasBin("claude") || exists(filepath.Join(home, ".claude")),
	}
}

func OpenAIBaseURL(baseURL string) string {
	clean := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if strings.HasSuffix(clean, "/v1") {
		return clean
	}
	return clean + "/v1"
}

// SyncOpenCode safely merges an isolated standby provider into opencode.json using `{env:<envKey>}` (zero plaintext keys).
func SyncOpenCode(baseURL, modelAlias, providerKey, envKey, displayName, configPath, backupDir string) (string, error) {
	if !core.IsValidEndpointURL(baseURL) {
		return "", ErrInvalidEndpoint
	}
	if !core.IsValidModelAlias(modelAlias) {
		return "", ErrInvalidModelAlias
	}
	if !keyRe.MatchString(providerKey) || !envRe.MatchString(envKey) {
		return "", ErrInvalidIdentifier
	}
	if configPath == "" {
		configPath = DefaultOpenCodeConfigPath()
	}
	if backupDir == "" {
		backupDir = DefaultBackupDir()
	}

	mu.Lock()
	defer mu.Unlock()

	target, err := resolveSafeWritableTarget(configPath)
	if err != nil {
		return "", err
	}

	root := map[string]any{}
	if target.Exists {
		raw, rErr := os.ReadFile(target.ResolvedPath)
		if rErr != nil {
			return "", fmt.Errorf("%w: %v", ErrMalformedConfig, rErr)
		}
		if len(strings.TrimSpace(string(raw))) > 0 {
			if err := json.Unmarshal(raw, &root); err != nil || root == nil {
				return "", fmt.Errorf("%w: opencode.json 解析失败", ErrMalformedConfig)
			}
		}
		if err := createPrivateBackup(target.ResolvedPath, "opencode", "json", backupDir); err != nil {
			return "", err
		}
	} else {
		if err := os.MkdirAll(filepath.Dir(target.ResolvedPath), 0o755); err != nil {
			return "", fmt.Errorf("%w: %v", ErrIO, err)
		}
		root["$schema"] = "https://opencode.ai/config.json"
	}

	var providers map[string]any
	if existing, ok := root["provider"]; ok {
		pMap, ok := existing.(map[string]any)
		if !ok {
			return "", fmt.Errorf("%w: provider 字段不是 JSON 对象", ErrMalformedConfig)
		}
		providers = pMap
	} else {
		providers = map[string]any{}
	}

	var entry map[string]any
	if existingEntry, ok := providers[providerKey]; ok {
		eMap, ok := existingEntry.(map[string]any)
		if !ok || !isManagedOpenCodeEntry(eMap) {
			return "", fmt.Errorf("%w: opencode.json 已存在未托管的 provider.%s", ErrUnmanagedConflict, providerKey)
		}
		entry = eMap
	} else {
		entry = map[string]any{}
	}

	opts, _ := entry["options"].(map[string]any)
	if opts == nil {
		opts = map[string]any{}
	}
	opts["baseURL"] = OpenAIBaseURL(baseURL)
	opts["apiKey"] = fmt.Sprintf("{env:%s}", envKey)

	models, _ := entry["models"].(map[string]any)
	if models == nil {
		models = map[string]any{}
	}
	mEntry, _ := models[modelAlias].(map[string]any)
	if mEntry == nil {
		mEntry = map[string]any{}
	}
	mEntry["name"] = modelAlias + " (Colab)"
	models[modelAlias] = mEntry

	entry["npm"] = "@ai-sdk/openai-compatible"
	entry["name"] = displayName
	entry["options"] = opts
	entry["models"] = models
	entry["x_colab_tools_managed"] = true

	providers[providerKey] = entry
	root["provider"] = providers

	outBytes, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrIO, err)
	}
	outBytes = append(outBytes, '\n')
	if err := writeAtomicallyPreservingMode(outBytes, target.ResolvedPath, target.FileMode); err != nil {
		return "", err
	}
	return target.ResolvedPath, nil
}

// SyncCodexCLI safely upserts an isolated `[model_providers.<tableKey>]` block in config.toml.
func SyncCodexCLI(baseURL, modelAlias, tableKey, envKey, displayName, configPath, backupDir string) (string, error) {
	if !core.IsValidEndpointURL(baseURL) {
		return "", ErrInvalidEndpoint
	}
	if !core.IsValidModelAlias(modelAlias) {
		return "", ErrInvalidModelAlias
	}
	if !keyRe.MatchString(tableKey) || !envRe.MatchString(envKey) {
		return "", ErrInvalidIdentifier
	}
	if configPath == "" {
		configPath = DefaultCodexConfigPath()
	}
	if backupDir == "" {
		backupDir = DefaultBackupDir()
	}

	mu.Lock()
	defer mu.Unlock()

	target, err := resolveSafeWritableTarget(configPath)
	if err != nil {
		return "", err
	}

	existing := ""
	if target.Exists {
		raw, rErr := os.ReadFile(target.ResolvedPath)
		if rErr != nil {
			return "", fmt.Errorf("%w: %v", ErrMalformedConfig, rErr)
		}
		if !utf8.Valid(raw) {
			return "", fmt.Errorf("%w: config.toml 包含无效 UTF-8 字节", ErrMalformedConfig)
		}
		existing = string(raw)
		if err := createPrivateBackup(target.ResolvedPath, "codex-config", "toml", backupDir); err != nil {
			return "", err
		}
	} else {
		if err := os.MkdirAll(filepath.Dir(target.ResolvedPath), 0o755); err != nil {
			return "", fmt.Errorf("%w: %v", ErrIO, err)
		}
	}

	beginMarker := "# BEGIN COLAB-TOOLS MANAGED " + tableKey
	endMarker := "# END COLAB-TOOLS MANAGED " + tableKey
	legacyBegin := "# BEGIN COLAB-TOOLS MANAGED"
	legacyEnd := "# END COLAB-TOOLS MANAGED"

	norm := strings.ReplaceAll(existing, "\r\n", "\n")
	lines := strings.Split(norm, "\n")

	var beginIdxs, endIdxs []int
	inTD, inTS := false, false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !inTD && !inTS {
			if trimmed == beginMarker || (tableKey == "colab_tools" && trimmed == legacyBegin) {
				beginIdxs = append(beginIdxs, i)
			} else if trimmed == endMarker || (tableKey == "colab_tools" && trimmed == legacyEnd) {
				endIdxs = append(endIdxs, i)
			}
		}
		updateMultilineTOML(line, &inTD, &inTS)
	}
	if inTD || inTS {
		return "", fmt.Errorf("%w: config.toml 存在未闭合的多行字符串", ErrMalformedConfig)
	}
	if len(beginIdxs) != len(endIdxs) || len(beginIdxs) > 1 {
		return "", fmt.Errorf("%w: config.toml 中 Colab Tools 托管标记不成对或重复", ErrMalformedConfig)
	}
	if len(beginIdxs) == 1 && beginIdxs[0] >= endIdxs[0] {
		return "", fmt.Errorf("%w: config.toml 中 Colab Tools 托管标记顺序错乱", ErrMalformedConfig)
	}

	hasRange := len(beginIdxs) == 1
	bIdx, eIdx := -1, -1
	if hasRange {
		bIdx, eIdx = beginIdxs[0], endIdxs[0]
	}

	inTD, inTS = false, false
	for i, line := range lines {
		inManaged := hasRange && i >= bIdx && i <= eIdx
		if !inManaged && !inTD && !inTS && isTOMLModelProviderHeader(line, tableKey) {
			return "", fmt.Errorf("%w: config.toml 已存在未托管的 [model_providers.%s]", ErrUnmanagedConflict, tableKey)
		}
		updateMultilineTOML(line, &inTD, &inTS)
	}

	block := []string{
		beginMarker,
		fmt.Sprintf("[model_providers.%s]", tableKey),
		fmt.Sprintf(`name = "%s"`, escapeTOMLString(displayName)),
		fmt.Sprintf(`base_url = "%s"`, escapeTOMLString(OpenAIBaseURL(baseURL))),
		`wire_api = "responses"`,
		fmt.Sprintf(`env_key = "%s"`, envKey),
		endMarker,
	}

	var updated string
	if hasRange {
		outLines := make([]string, 0, len(lines)-((eIdx-bIdx)+1)+len(block))
		outLines = append(outLines, lines[:bIdx]...)
		outLines = append(outLines, block...)
		outLines = append(outLines, lines[eIdx+1:]...)
		updated = strings.Join(outLines, "\n")
	} else if strings.TrimSpace(norm) == "" {
		updated = strings.Join(block, "\n") + "\n"
	} else {
		sep := "\n\n"
		if strings.HasSuffix(norm, "\n\n") {
			sep = ""
		} else if strings.HasSuffix(norm, "\n") {
			sep = "\n"
		}
		updated = norm + sep + strings.Join(block, "\n") + "\n"
	}

	if err := writeAtomicallyPreservingMode([]byte(updated), target.ResolvedPath, target.FileMode); err != nil {
		return "", err
	}
	return target.ResolvedPath, nil
}

// SyncCCSwitch performs a zero-CGO WAL-safe SQLite online backup (VACUUM INTO) and BEGIN IMMEDIATE standby write.
func SyncCCSwitch(
	providerID, providerName, baseURL, apiKey, modelAlias string,
	contextLimit int,
	gpuLabel, accountLabel string,
	dbPath, backupDir string,
) (string, error) {
	if dbPath == "" {
		dbPath = DefaultCCSwitchDBPath()
	}
	if backupDir == "" {
		backupDir = DefaultCCSwitchBackupDir()
	}
	if _, err := os.Stat(dbPath); err != nil {
		return "", ErrDatabaseNotFound
	}
	if !core.IsValidEndpointURL(baseURL) || strings.TrimSpace(apiKey) == "" || !core.IsValidModelAlias(modelAlias) {
		return "", ErrInvalidEndpoint
	}

	mu.Lock()
	defer mu.Unlock()

	if err := ensurePrivateBackupDir(backupDir); err != nil {
		return "", err
	}

	// Open pure-Go modernc.org/sqlite with busy_timeout
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)", filepath.ToSlash(dbPath))
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrIO, err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	// Verify expected CC Switch providers schema before writing so upstream schema changes never corrupt user data.
	if err := verifyCCSwitchSchema(db); err != nil {
		return "", err
	}

	// Daily WAL-safe online backup via SQLite VACUUM INTO (includes committed WAL pages cleanly without CGO)
	dayPrefix := "cc-switch.db.bak-colabtools-" + time.Now().Format("20060102")
	entries, _ := os.ReadDir(backupDir)
	hasDaily := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), dayPrefix) {
			hasDaily = true
			break
		}
	}
	if !hasDaily {
		bakPath := filepath.Join(backupDir, fmt.Sprintf("cc-switch.db.bak-colabtools-%s-%s",
			time.Now().Format("20060102-150405"), uuid.New().String()[:6]))
		if _, err := db.Exec("VACUUM INTO ?", bakPath); err != nil {
			_ = os.Remove(bakPath)
			return "", fmt.Errorf("%w: %v", ErrBackupFailed, err)
		}
		_ = os.Chmod(bakPath, 0o600)
	}

	autoCompact := contextLimit - 16384
	if autoCompact < 32768 {
		autoCompact = 32768
	}
	managedEnv := map[string]string{
		"ANTHROPIC_BASE_URL":                      baseURL,
		"ANTHROPIC_AUTH_TOKEN":                    apiKey,
		"ANTHROPIC_MODEL":                         modelAlias,
		"ANTHROPIC_SMALL_FAST_MODEL":              modelAlias,
		"ANTHROPIC_DEFAULT_HAIKU_MODEL":           modelAlias,
		"ANTHROPIC_DEFAULT_SONNET_MODEL":          modelAlias,
		"ANTHROPIC_DEFAULT_OPUS_MODEL":            modelAlias,
		"CLAUDE_CODE_DISABLE_1M_CONTEXT":          "1",
		"CLAUDE_CODE_MAX_CONTEXT_TOKENS":          fmt.Sprintf("%d", contextLimit),
		"CLAUDE_CODE_AUTO_COMPACT_WINDOW":         fmt.Sprintf("%d", autoCompact),
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
	}
	if gpuLabel == "" {
		gpuLabel = "GPU"
	}
	notes := fmt.Sprintf("Colab Tools [%s] · %s (%dK) · 自动同步", accountLabel, gpuLabel, contextLimit/1024)

	if _, err := db.Exec("BEGIN IMMEDIATE"); err != nil {
		return "", fmt.Errorf("%w: %v", ErrIO, err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = db.Exec("ROLLBACK")
		}
	}()

	var isCurrent int
	var rawCfg, category string
	rowErr := db.QueryRow(
		"SELECT is_current, settings_config, COALESCE(category, '') FROM providers WHERE id=? AND app_type='claude'",
		providerID,
	).Scan(&isCurrent, &rawCfg, &category)

	if rowErr == nil {
		if isCurrent != 0 || category == "official" {
			return "", ErrActiveProviderLock
		}
		rootObj := map[string]any{}
		_ = json.Unmarshal([]byte(rawCfg), &rootObj)
		envObj, _ := rootObj["env"].(map[string]any)
		if envObj == nil {
			envObj = map[string]any{}
		}
		for k, v := range managedEnv {
			envObj[k] = v
		}
		rootObj["env"] = envObj
		mergedBytes, _ := json.Marshal(rootObj)

		res, err := db.Exec(
			"UPDATE providers SET name=?, settings_config=?, notes=? WHERE id=? AND app_type='claude' AND is_current=0",
			providerName, string(mergedBytes), notes, providerID,
		)
		if err != nil {
			return "", fmt.Errorf("%w: %v", ErrIO, err)
		}
		if affected, _ := res.RowsAffected(); affected != 1 {
			return "", ErrActiveProviderLock
		}
		if _, err := db.Exec("COMMIT"); err != nil {
			return "", fmt.Errorf("%w: %v", ErrIO, err)
		}
		committed = true
		return "已安全更新 CC Switch 待机渠道（官方订阅未受影响）", nil
	} else if !errors.Is(rowErr, sql.ErrNoRows) {
		return "", fmt.Errorf("%w: %v", ErrIO, rowErr)
	}

	newBytes, _ := json.Marshal(map[string]any{"env": managedEnv})
	var maxSort int
	_ = db.QueryRow("SELECT COALESCE(MAX(sort_index), 0) FROM providers WHERE app_type='claude'").Scan(&maxSort)

	_, err = db.Exec(`
		INSERT INTO providers (
			id, app_type, name, settings_config, website_url, category,
			created_at, sort_index, notes, icon, icon_color, meta,
			is_current, in_failover_queue, cost_multiplier, provider_type
		) VALUES (?, 'claude', ?, ?, 'https://colab.research.google.com', 'custom', ?, ?, ?, 'google', '#F59E0B', '{}', 0, 0, '0.0', NULL)
	`, providerID, providerName, string(newBytes), time.Now().UnixMilli(), maxSort+1, notes)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrIO, err)
	}
	if _, err := db.Exec("COMMIT"); err != nil {
		return "", fmt.Errorf("%w: %v", ErrIO, err)
	}
	committed = true
	return "已新增 CC Switch 待机渠道（官方订阅保持激活）", nil
}

func verifyCCSwitchSchema(db *sql.DB) error {
	rows, err := db.Query("PRAGMA table_info(providers)")
	if err != nil {
		return fmt.Errorf("%w: 无法读取 CC Switch 表结构：%v", ErrMalformedConfig, err)
	}
	defer rows.Close()

	cols := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			return fmt.Errorf("%w: 解析 CC Switch 表结构失败：%v", ErrMalformedConfig, err)
		}
		cols[strings.ToLower(strings.TrimSpace(name))] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("%w: 校验 CC Switch 表结构失败：%v", ErrMalformedConfig, err)
	}
	required := []string{
		"id", "app_type", "name", "settings_config", "website_url", "category",
		"created_at", "sort_index", "notes", "icon", "icon_color", "meta",
		"is_current", "in_failover_queue", "cost_multiplier", "provider_type",
	}
	var missing []string
	for _, col := range required {
		if !cols[col] {
			missing = append(missing, col)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: CC Switch 数据库结构已变更（缺少列 %s），已中止写入以保护配置", ErrMalformedConfig, strings.Join(missing, ", "))
	}
	return nil
}

// ExportEnvSnippet builds safe-by-default shell export templates (never leaks raw secret unless includeRawSecret=true).
func ExportEnvSnippet(kind CopyProtocolKind, baseURL, modelAlias, apiKey, envKey string, includeRawSecret bool) string {
	safeEnv := envKey
	if !envRe.MatchString(safeEnv) {
		safeEnv = "COLAB_TOOLS_API_KEY"
	}
	useSecret := includeRawSecret && strings.TrimSpace(apiKey) != ""
	switch kind {
	case ProtocolOpenAI:
		keyLine := fmt.Sprintf("export %s='<your-colab-api-key>'", safeEnv)
		if useSecret {
			keyLine = fmt.Sprintf("export %s=%s", safeEnv, runner.ShellEscapePOSIX(apiKey))
		}
		return strings.Join([]string{
			keyLine,
			fmt.Sprintf("export OPENAI_BASE_URL=%s", runner.ShellEscapePOSIX(OpenAIBaseURL(baseURL))),
			fmt.Sprintf(`export OPENAI_API_KEY="$%s"`, safeEnv),
			fmt.Sprintf("# Model: %s", modelAlias),
		}, "\n")
	default:
		cleanURL := strings.TrimRight(strings.TrimSpace(baseURL), "/")
		tokLine := fmt.Sprintf(`export ANTHROPIC_AUTH_TOKEN="${%s:-your-colab-api-key}"`, safeEnv)
		if useSecret {
			tokLine = fmt.Sprintf("export ANTHROPIC_AUTH_TOKEN=%s", runner.ShellEscapePOSIX(apiKey))
		}
		return strings.Join([]string{
			fmt.Sprintf("export ANTHROPIC_BASE_URL=%s", runner.ShellEscapePOSIX(cleanURL)),
			tokLine,
			fmt.Sprintf("export ANTHROPIC_MODEL=%s", runner.ShellEscapePOSIX(modelAlias)),
		}, "\n")
	}
}

func ExportJSONSnippet(kind CopyProtocolKind, baseURL, modelAlias, apiKey, envKey string, includeRawSecret bool) string {
	safeEnv := envKey
	if !envRe.MatchString(safeEnv) {
		safeEnv = "COLAB_TOOLS_API_KEY"
	}
	keyVal := fmt.Sprintf("${%s}", safeEnv)
	if includeRawSecret && strings.TrimSpace(apiKey) != "" {
		keyVal = apiKey
	}
	var obj map[string]any
	if kind == ProtocolOpenAI {
		obj = map[string]any{
			"name":    "Colab Tools",
			"type":    "openai-compatible",
			"baseURL": OpenAIBaseURL(baseURL),
			"apiKey":  keyVal,
			"model":   modelAlias,
		}
	} else {
		obj = map[string]any{
			"name":      "Colab Tools",
			"type":      "anthropic",
			"baseURL":   strings.TrimRight(strings.TrimSpace(baseURL), "/"),
			"authToken": keyVal,
			"model":     modelAlias,
		}
	}
	b, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(b)
}

func isManagedOpenCodeEntry(m map[string]any) bool {
	if flag, ok := m["x_colab_tools_managed"].(bool); ok && flag {
		return true
	}
	npm, _ := m["npm"].(string)
	opts, _ := m["options"].(map[string]any)
	if npm != "@ai-sdk/openai-compatible" || opts == nil {
		return false
	}
	key, _ := opts["apiKey"].(string)
	return strings.HasPrefix(key, "{env:COLAB_TOOLS_API_KEY") && strings.HasSuffix(key, "}")
}

func isTOMLModelProviderHeader(line, tableKey string) bool {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "[") {
		return false
	}
	if idx := strings.Index(trimmed, "#"); idx >= 0 {
		trimmed = strings.TrimSpace(trimmed[:idx])
	}
	return trimmed == fmt.Sprintf("[model_providers.%s]", tableKey) ||
		trimmed == fmt.Sprintf(`[model_providers."%s"]`, tableKey) ||
		trimmed == fmt.Sprintf(`[model_providers.'%s']`, tableKey)
}

func updateMultilineTOML(line string, inTD, inTS *bool) {
	for i := 0; i < len(line); {
		if !*inTD && !*inTS && line[i] == '#' {
			return
		}
		if !*inTS && i+2 < len(line) && line[i:i+3] == `"""` {
			escaped := i > 0 && line[i-1] == '\\'
			if !escaped {
				*inTD = !*inTD
				i += 3
				continue
			}
		}
		if !*inTD && i+2 < len(line) && line[i:i+3] == `'''` {
			*inTS = !*inTS
			i += 3
			continue
		}
		i++
	}
}

func escapeTOMLString(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, `"`, `\"`)
	v = strings.ReplaceAll(v, "\n", " ")
	v = strings.ReplaceAll(v, "\r", " ")
	return v
}

func writeAtomicallyPreservingMode(data []byte, dst string, mode os.FileMode) error {
	if mode == 0 {
		mode = 0o600
	}
	dir := filepath.Dir(dst)
	tmp := filepath.Join(dir, fmt.Sprintf(".%s.colabtools.%s.tmp", filepath.Base(dst), uuid.New().String()[:8]))
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return fmt.Errorf("%w: %v", ErrIO, err)
	}
	_ = os.Chmod(tmp, mode)
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("%w: %v", ErrIO, err)
	}
	return nil
}

func createPrivateBackup(src, prefix, ext, backupDir string) error {
	if err := ensurePrivateBackupDir(backupDir); err != nil {
		return err
	}
	nonce := strings.ToLower(uuid.New().String()[:8])
	dst := filepath.Join(backupDir, fmt.Sprintf("%s-%s-%s.%s.bak", prefix, time.Now().Format("20060102-150405.000"), nonce, ext))
	raw, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrIO, err)
	}
	f, err := openExclusiveNoFollow(dst)
	if err != nil {
		return fmt.Errorf("%w: 无法创建私有备份：%v", ErrUnsafeFilePath, err)
	}
	_, wErr := f.Write(raw)
	cErr := f.Close()
	if wErr != nil || cErr != nil {
		_ = os.Remove(dst)
		return fmt.Errorf("%w: 写入备份失败", ErrIO)
	}
	_ = os.Chmod(dst, 0o600)

	// Retain latest 10 backups per prefix
	entries, err := os.ReadDir(backupDir)
	if err == nil {
		var matches []string
		for _, e := range entries {
			name := e.Name()
			if strings.HasPrefix(name, prefix+"-") && strings.HasSuffix(name, "."+ext+".bak") {
				matches = append(matches, name)
			}
		}
		sort.Strings(matches)
		if len(matches) > 10 {
			for _, old := range matches[:len(matches)-10] {
				_ = os.Remove(filepath.Join(backupDir, old))
			}
		}
	}
	return nil
}
