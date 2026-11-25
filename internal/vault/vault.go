package vault

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/LJMStark/Colab-Tools/internal/core"
	"github.com/egoist/mygo"
	"github.com/google/uuid"
)

const ServiceName = "io.github.ljmstark.colabtools"

var ErrNotFound = errors.New("secret not found")

type Backend interface {
	Set(key string, value []byte) error
	Get(key string) ([]byte, error)
	Delete(key string) error
}

// MyGoBackend delegates directly to MyGo's zero-CGO cross-platform Secrets module
// (macOS Keychain / Windows Credential Manager / Linux Secret Service).
type MyGoBackend struct {
	svc mygo.SecretsModule
}

func NewMyGoBackend() *MyGoBackend {
	return &MyGoBackend{svc: mygo.Secrets.Service(ServiceName)}
}

func (b *MyGoBackend) Set(key string, value []byte) error {
	return b.svc.Set(key, value)
}

func (b *MyGoBackend) Get(key string) ([]byte, error) {
	v, err := b.svc.Get(key)
	if errors.Is(err, mygo.ErrSecretNotFound) {
		return nil, ErrNotFound
	}
	return v, err
}

func (b *MyGoBackend) Delete(key string) error {
	err := b.svc.Delete(key)
	if errors.Is(err, mygo.ErrSecretNotFound) {
		return nil
	}
	return err
}

// MemoryBackend is a thread-safe in-memory backend for unit tests.
type MemoryBackend struct {
	mu   sync.RWMutex
	data map[string][]byte
}

func NewMemoryBackend() *MemoryBackend {
	return &MemoryBackend{data: map[string][]byte{}}
}

func (m *MemoryBackend) Set(key string, value []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(value))
	copy(cp, value)
	m.data[key] = cp
	return nil
}

func (m *MemoryBackend) Get(key string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.data[key]
	if !ok {
		return nil, ErrNotFound
	}
	cp := make([]byte, len(v))
	copy(cp, v)
	return cp, nil
}

func (m *MemoryBackend) Delete(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
	return nil
}

type Vault struct {
	backend      Backend
	ephemeralDir string
}

func New(backend Backend) *Vault {
	if backend == nil {
		backend = NewMyGoBackend()
	}
	return &Vault{
		backend:      backend,
		ephemeralDir: filepath.Join(core.ConfigDir(), "ephemeral"),
	}
}

func adcKey(accountID string) string    { return "adc-v2-" + accountID }
func llmAPIKey(accountID string) string { return "llm-apikey-v2-" + accountID }

func legacyADCKeys(accountID string) []string {
	keys := []string{"account:" + accountID}
	if upper := strings.ToUpper(accountID); upper != accountID {
		keys = append(keys, "account:"+upper)
	}
	if lower := strings.ToLower(accountID); lower != accountID && lower != strings.ToUpper(accountID) {
		keys = append(keys, "account:"+lower)
	}
	return keys
}

func legacyLLMAPIKeys(accountID string) []string {
	keys := []string{"apikey:" + accountID}
	if upper := strings.ToUpper(accountID); upper != accountID {
		keys = append(keys, "apikey:"+upper)
	}
	if lower := strings.ToLower(accountID); lower != accountID && lower != strings.ToUpper(accountID) {
		keys = append(keys, "apikey:"+lower)
	}
	return keys
}

// NormalizeADCCredentials validates and normalizes OAuth2 authorized_user JSON before storing in system vault.
func NormalizeADCCredentials(raw string) ([]byte, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, errors.New("凭证 JSON 为空")
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(trimmed), &obj); err != nil {
		return nil, fmt.Errorf("凭证 JSON 解析失败: %w", err)
	}
	refresh, _ := obj["refresh_token"].(string)
	clientID, _ := obj["client_id"].(string)
	if strings.TrimSpace(refresh) == "" || strings.TrimSpace(clientID) == "" {
		return nil, errors.New("凭证缺少必要字段 refresh_token 或 client_id")
	}
	if _, ok := obj["type"]; !ok {
		obj["type"] = "authorized_user"
	}
	compact, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	if len(compact) > mygo.MaxSecretSize {
		return nil, fmt.Errorf("凭证体积（%d 字节）超出系统凭据库限制（%d 字节）", len(compact), mygo.MaxSecretSize)
	}
	return compact, nil
}

func (v *Vault) SaveADCCredentials(accountID, rawJSON string) error {
	norm, err := NormalizeADCCredentials(rawJSON)
	if err != nil {
		return err
	}
	return v.backend.Set(adcKey(accountID), norm)
}

func (v *Vault) LoadADCCredentials(accountID string) (string, error) {
	raw, err := v.backend.Get(adcKey(accountID))
	if err == nil {
		return string(raw), nil
	}
	if errors.Is(err, ErrNotFound) {
		for _, oldKey := range legacyADCKeys(accountID) {
			if legacyRaw, lErr := v.backend.Get(oldKey); lErr == nil && len(legacyRaw) > 0 {
				if norm, nErr := NormalizeADCCredentials(string(legacyRaw)); nErr == nil {
					if sErr := v.backend.Set(adcKey(accountID), norm); sErr == nil {
						_ = v.backend.Delete(oldKey)
					}
					return string(norm), nil
				}
				return string(legacyRaw), nil
			}
		}
	}
	return "", err
}

func (v *Vault) DeleteADCCredentials(accountID string) error {
	err := v.backend.Delete(adcKey(accountID))
	for _, oldKey := range legacyADCKeys(accountID) {
		_ = v.backend.Delete(oldKey)
	}
	return err
}

func (v *Vault) SaveLLMAPIKey(accountID, key string) error {
	clean := strings.TrimSpace(key)
	if clean == "" {
		return errors.New("API Key 不能为空")
	}
	return v.backend.Set(llmAPIKey(accountID), []byte(clean))
}

func (v *Vault) LoadLLMAPIKey(accountID string) (string, error) {
	raw, err := v.backend.Get(llmAPIKey(accountID))
	if err == nil {
		return strings.TrimSpace(string(raw)), nil
	}
	if errors.Is(err, ErrNotFound) {
		for _, oldKey := range legacyLLMAPIKeys(accountID) {
			if legacyRaw, lErr := v.backend.Get(oldKey); lErr == nil {
				clean := strings.TrimSpace(string(legacyRaw))
				if clean != "" {
					if sErr := v.backend.Set(llmAPIKey(accountID), []byte(clean)); sErr == nil {
						_ = v.backend.Delete(oldKey)
					}
					return clean, nil
				}
			}
		}
	}
	return "", err
}

func (v *Vault) DeleteLLMAPIKey(accountID string) error {
	err := v.backend.Delete(llmAPIKey(accountID))
	for _, oldKey := range legacyLLMAPIKeys(accountID) {
		_ = v.backend.Delete(oldKey)
	}
	return err
}

func (v *Vault) CleanupStaleEphemeralFiles() {
	entries, err := os.ReadDir(v.ephemeralDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			ShredAndRemove(filepath.Join(v.ephemeralDir, e.Name()))
		}
	}
}

func (v *Vault) WriteEphemeralFile(prefix, ext string, content []byte) (string, error) {
	if err := os.MkdirAll(v.ephemeralDir, 0o700); err != nil {
		return "", err
	}
	name := fmt.Sprintf("%s-%s.%s", prefix, uuid.New().String()[:12], ext)
	dst := filepath.Join(v.ephemeralDir, name)
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	_, wErr := f.Write(content)
	cErr := f.Close()
	if wErr != nil {
		ShredAndRemove(dst)
		return "", wErr
	}
	if cErr != nil {
		ShredAndRemove(dst)
		return "", cErr
	}
	return dst, nil
}

func (v *Vault) WithEphemeralADCFile(accountID string, fn func(adcPath string) error) error {
	raw, err := v.LoadADCCredentials(accountID)
	if err != nil {
		return fmt.Errorf("未找到账号凭据（请重新绑定账号）: %w", err)
	}
	path, err := v.WriteEphemeralFile("adc", "json", []byte(raw))
	if err != nil {
		return err
	}
	defer ShredAndRemove(path)
	return fn(path)
}

// ShredAndRemove overwrites sensitive temporary files with random bytes before unlinking.
func ShredAndRemove(path string) {
	st, err := os.Stat(path)
	if err == nil && !st.IsDir() && st.Size() > 0 && st.Size() <= 1024*1024 {
		if f, err := os.OpenFile(path, os.O_WRONLY, 0o600); err == nil {
			noise := make([]byte, st.Size())
			_, _ = rand.Read(noise)
			_, _ = f.WriteAt(noise, 0)
			_ = f.Sync()
			_ = f.Close()
		}
	}
	_ = os.Remove(path)
}
