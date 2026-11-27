package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

const CurrentActiveLedgerSchemaVersion = 1

// ActiveSessionLedgerEntry snapshots an in-flight running session so billing & uptime survive app restarts or crashes.
type ActiveSessionLedgerEntry struct {
	AccountID          string          `json:"accountId"`
	AccountName        string          `json:"accountName"`
	AccountColor       AccountColorTag `json:"accountColor,omitempty"`
	SessionName        string          `json:"sessionName"`
	Endpoint           string          `json:"endpoint"`
	Hardware           string          `json:"hardware,omitempty"`
	HighMem            bool            `json:"highMem,omitempty"`
	RecipeLabel        string          `json:"recipeLabel,omitempty"`
	StartedAt          time.Time       `json:"startedAt"`
	StartBalance       float64         `json:"startBalance,omitempty"`
	StartBalanceValid  bool            `json:"startBalanceValid,omitempty"`
	Adopted            bool            `json:"adopted,omitempty"`
	MaxAssignments     int             `json:"maxAssignments,omitempty"`
	MeasuredSingleRate float64         `json:"measuredSingleRate,omitempty"`
	EstimatedCU        float64         `json:"estimatedCU,omitempty"`
	LastSampleAt       time.Time       `json:"lastSampleAt"`
	LastUptimeSec      int             `json:"lastUptimeSec,omitempty"`
	FallbackUsed       bool            `json:"fallbackUsed,omitempty"`
	ModelDowngraded    bool            `json:"modelDowngraded,omitempty"`
	UsedStandardRAM    bool            `json:"usedStandardRAM,omitempty"`
}

type activeLedgerPayload struct {
	SchemaVersion int                                 `json:"schemaVersion,omitempty"`
	UpdatedAt     time.Time                           `json:"updatedAt,omitempty"`
	Entries       map[string]ActiveSessionLedgerEntry `json:"entries"`
}

func ActiveLedgerFilePath() string {
	return filepath.Join(ConfigDir(), "active_sessions.json")
}

// LoadActiveLedger reads persisted in-flight sessions. Corrupt files are backed up and ignored safely.
func LoadActiveLedger(path string) map[string]ActiveSessionLedgerEntry {
	if path == "" {
		path = ActiveLedgerFilePath()
	}
	out := map[string]ActiveSessionLedgerEntry{}
	raw, err := os.ReadFile(path)
	if err != nil || len(strings.TrimSpace(string(raw))) == 0 {
		return out
	}
	var payload activeLedgerPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		backupPath := fmt.Sprintf("%s.corrupt.%d", path, time.Now().Unix())
		_ = os.WriteFile(backupPath, raw, 0o600)
		return out
	}
	if payload.SchemaVersion > CurrentActiveLedgerSchemaVersion {
		backupPath := fmt.Sprintf("%s.future-v%d.%d", path, payload.SchemaVersion, time.Now().Unix())
		_ = os.WriteFile(backupPath, raw, 0o600)
	}
	for k, v := range payload.Entries {
		if strings.TrimSpace(k) == "" || strings.TrimSpace(v.Endpoint) == "" {
			continue
		}
		if v.AccountID == "" {
			v.AccountID = k
		}
		if v.EstimatedCU < 0 {
			v.EstimatedCU = 0
		}
		if v.LastUptimeSec < 0 {
			v.LastUptimeSec = 0
		}
		out[k] = v
	}
	return out
}

// SaveActiveLedger atomically writes the active session snapshots to disk.
func SaveActiveLedger(path string, entries map[string]ActiveSessionLedgerEntry) error {
	if path == "" {
		path = ActiveLedgerFilePath()
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	clean := make(map[string]ActiveSessionLedgerEntry, len(entries))
	for k, v := range entries {
		if strings.TrimSpace(k) == "" || strings.TrimSpace(v.Endpoint) == "" {
			continue
		}
		clean[k] = v
	}
	data, err := json.MarshalIndent(activeLedgerPayload{
		SchemaVersion: CurrentActiveLedgerSchemaVersion,
		UpdatedAt:     time.Now().UTC(),
		Entries:       clean,
	}, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, fmt.Sprintf(".active_sessions.%s.tmp", uuid.New().String()[:8]))
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
