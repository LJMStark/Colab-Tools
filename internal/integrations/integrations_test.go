package integrations_test

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LJMStark/Colab-Tools/internal/integrations"
	_ "modernc.org/sqlite"
)

func TestOpenCodeCodexPerAccountIsolationAndBackups(t *testing.T) {
	tmp := t.TempDir()
	ocPath := filepath.Join(tmp, "opencode.json")
	cdxPath := filepath.Join(tmp, "config.toml")
	bakDir := filepath.Join(tmp, "backups")

	id1 := integrations.IdentifiersForAccount("11111111-1111-1111-1111-111111111111")
	id2 := integrations.IdentifiersForAccount("22222222-2222-2222-2222-222222222222")
	if id1.OpenCodeProviderKey == id2.OpenCodeProviderKey || id1.EnvKey == id2.EnvKey {
		t.Fatal("distinct accounts must produce isolated keys")
	}

	// Existing user provider in OpenCode must be preserved and raw key never written
	_ = os.WriteFile(ocPath, []byte(`{"provider":{"anthropic":{"name":"Official"}}}`), 0o600)
	if _, err := integrations.SyncOpenCode("https://demo.trycloudflare.com", "qwen3.8", id1.OpenCodeProviderKey, id1.EnvKey, "Acc1", ocPath, bakDir); err != nil {
		t.Fatalf("sync opencode 1 failed: %v", err)
	}
	if _, err := integrations.SyncOpenCode("https://demo2.trycloudflare.com", "qwen3.8", id2.OpenCodeProviderKey, id2.EnvKey, "Acc2", ocPath, bakDir); err != nil {
		t.Fatalf("sync opencode 2 failed: %v", err)
	}
	rawOC, _ := os.ReadFile(ocPath)
	var parsed map[string]any
	if err := json.Unmarshal(rawOC, &parsed); err != nil {
		t.Fatal(err)
	}
	provs := parsed["provider"].(map[string]any)
	if _, ok := provs["anthropic"]; !ok {
		t.Fatal("existing official provider was lost")
	}
	if !strings.Contains(string(rawOC), "{env:"+id1.EnvKey+"}") || !strings.Contains(string(rawOC), "{env:"+id2.EnvKey+"}") {
		t.Fatal("expected env-var-only apiKey references")
	}

	// Unmanaged conflict protection in OpenCode
	_ = os.WriteFile(ocPath, []byte(`{"provider":{"`+id1.OpenCodeProviderKey+`":{"name":"User Custom"}}}`), 0o600)
	if _, err := integrations.SyncOpenCode("https://demo.trycloudflare.com", "qwen3.8", id1.OpenCodeProviderKey, id1.EnvKey, "Acc1", ocPath, bakDir); err == nil {
		t.Fatal("expected unmanaged conflict error for OpenCode")
	}

	// Invalid UTF-8 in Codex config.toml must refuse overwrite
	_ = os.WriteFile(cdxPath, []byte{0xff, 0xfe, 0xfd}, 0o600)
	if _, err := integrations.SyncCodexCLI("https://demo.trycloudflare.com", "qwen3.8", id1.CodexTableKey, id1.EnvKey, "Acc1", cdxPath, bakDir); err == nil {
		t.Fatal("expected invalid UTF-8 rejection")
	}

	// Valid Codex TOML preserves default provider and is idempotent
	_ = os.WriteFile(cdxPath, []byte("model_provider = \"openai\"\n"), 0o600)
	if _, err := integrations.SyncCodexCLI("https://demo.trycloudflare.com", "qwen3.8", id1.CodexTableKey, id1.EnvKey, "Acc1", cdxPath, bakDir); err != nil {
		t.Fatalf("codex sync 1 failed: %v", err)
	}
	if _, err := integrations.SyncCodexCLI("https://demo-new.trycloudflare.com", "qwen3.8", id1.CodexTableKey, id1.EnvKey, "Acc1", cdxPath, bakDir); err != nil {
		t.Fatalf("codex sync 2 failed: %v", err)
	}
	rawCdx, _ := os.ReadFile(cdxPath)
	txt := string(rawCdx)
	if !strings.Contains(txt, `model_provider = "openai"`) {
		t.Fatal("default model_provider altered")
	}
	if strings.Count(txt, "[model_providers."+id1.CodexTableKey+"]") != 1 {
		t.Fatalf("expected exactly one managed table, got:\n%s", txt)
	}
	if st, err := os.Stat(bakDir); err == nil && st.Mode().Perm() != 0o700 {
		t.Fatalf("backup dir permissions must be 0700, got %o", st.Mode().Perm())
	}
}

func TestCCSwitchZeroCGOWALBackupAndActiveProtection(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "cc-switch.db")
	bakDir := filepath.Join(tmp, "backups")

	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		PRAGMA journal_mode=WAL;
		CREATE TABLE providers (
			id TEXT PRIMARY KEY,
			app_type TEXT NOT NULL,
			name TEXT NOT NULL,
			settings_config TEXT NOT NULL,
			website_url TEXT,
			category TEXT,
			created_at INTEGER,
			sort_index INTEGER,
			notes TEXT,
			icon TEXT,
			icon_color TEXT,
			meta TEXT,
			is_current INTEGER NOT NULL DEFAULT 0,
			in_failover_queue INTEGER DEFAULT 0,
			cost_multiplier TEXT,
			provider_type TEXT
		);
		INSERT INTO providers (id, app_type, name, settings_config, category, sort_index, is_current)
		VALUES ('official-claude', 'claude', 'Official', '{"env":{"A":"1"}}', 'official', 1, 1);
	`)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	// Refuses overwriting active/official provider
	if _, err := integrations.SyncCCSwitch("official-claude", "Bad", "https://demo.trycloudflare.com", "sk-test", "qwen", 262144, "L4", "Acc", dbPath, bakDir); err == nil {
		t.Fatal("must refuse overwriting active/official provider")
	}

	// Inserts new standby provider cleanly and writes WAL-safe backup
	if _, err := integrations.SyncCCSwitch("colab-standby", "Colab Qwen", "https://demo.trycloudflare.com", "sk-test", "qwen", 262144, "L4", "Acc", dbPath, bakDir); err != nil {
		t.Fatalf("standby insert failed: %v", err)
	}
	entries, _ := os.ReadDir(bakDir)
	if len(entries) == 0 {
		t.Fatal("expected VACUUM INTO WAL-safe backup file")
	}

	// Refuses writing when CC Switch schema changed (missing expected columns)
	badDBPath := filepath.Join(tmp, "cc-switch-changed.db")
	badDB, err := sql.Open("sqlite", "file:"+filepath.ToSlash(badDBPath))
	if err != nil {
		t.Fatal(err)
	}
	_, err = badDB.Exec(`CREATE TABLE providers (id TEXT PRIMARY KEY, name TEXT);`)
	if err != nil {
		t.Fatal(err)
	}
	_ = badDB.Close()
	if _, err := integrations.SyncCCSwitch("colab-standby", "Colab Qwen", "https://demo.trycloudflare.com", "sk-test", "qwen", 262144, "L4", "Acc", badDBPath, bakDir); err == nil {
		t.Fatal("must refuse writing when CC Switch providers table schema is incompatible")
	}
}

func TestSafeDefaultCopySnippetsNeverLeakSecret(t *testing.T) {
	safe := integrations.ExportEnvSnippet(integrations.ProtocolOpenAI, "https://demo.trycloudflare.com", "qwen", "sk-secret-token", "COLAB_TOOLS_API_KEY_TEST", false)
	if strings.Contains(safe, "sk-secret-token") {
		t.Fatal("default safe snippet leaked raw secret")
	}
	raw := integrations.ExportEnvSnippet(integrations.ProtocolOpenAI, "https://demo.trycloudflare.com", "qwen", "sk-secret-token", "COLAB_TOOLS_API_KEY_TEST", true)
	if !strings.Contains(raw, "sk-secret-token") {
		t.Fatal("explicit secret snippet missing secret")
	}
}
