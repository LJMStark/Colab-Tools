package core

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	HistoryMaxAgeDays = 90
	HistoryMaxRecords = 1000
)

type HistoryEntryKind string

const (
	HistoryKindSession    HistoryEntryKind = "session"
	HistoryKindAllocation HistoryEntryKind = "allocation"
)

type ShutdownReason string

const (
	ShutdownManual         ShutdownReason = "manual"
	ShutdownAutoIdle       ShutdownReason = "auto_idle"
	ShutdownBatchStop      ShutdownReason = "batch_stop"
	ShutdownCapReached     ShutdownReason = "cap_reached"
	ShutdownExternalOrLost ShutdownReason = "external_or_lost"
)

func (r ShutdownReason) DisplayName() string {
	switch r {
	case ShutdownManual:
		return "手动关机"
	case ShutdownAutoIdle:
		return "空闲自动关机"
	case ShutdownBatchStop:
		return "全部关机止损"
	case ShutdownCapReached:
		return "24h 上限"
	case ShutdownExternalOrLost:
		return "外部或异常断开"
	default:
		return string(r)
	}
}

type AllocationOutcomeTag string

const (
	AllocTagSuccess     AllocationOutcomeTag = "success"
	AllocTagShortage    AllocationOutcomeTag = "shortage"
	AllocTagRateLimited AllocationOutcomeTag = "rate_limited"
	AllocTagCancelled   AllocationOutcomeTag = "cancelled"
	AllocTagError       AllocationOutcomeTag = "error"
)

func (a AllocationOutcomeTag) DisplayName() string {
	switch a {
	case AllocTagSuccess:
		return "抢到"
	case AllocTagShortage:
		return "缺货"
	case AllocTagRateLimited:
		return "限流冷却"
	case AllocTagCancelled:
		return "已取消"
	case AllocTagError:
		return "异常失败"
	default:
		return string(a)
	}
}

type BillingBasis string

const (
	BillingMeasured  BillingBasis = "measured"
	BillingEstimated BillingBasis = "estimated"
	BillingFree      BillingBasis = "free"
)

func (b BillingBasis) DisplayName() string {
	switch b {
	case BillingMeasured:
		return "实测"
	case BillingEstimated:
		return "估算"
	case BillingFree:
		return "免费"
	default:
		return string(b)
	}
}

func NormalizeHardwareKey(raw string) string {
	u := strings.ToUpper(raw)
	switch {
	case strings.Contains(u, "G4") || strings.Contains(u, "6000"):
		return "G4"
	case strings.Contains(u, "H100"):
		return "H100"
	case strings.Contains(u, "A100"):
		return "A100"
	case strings.Contains(u, "L4"):
		return "L4"
	case strings.Contains(u, "T4"):
		return "T4"
	case strings.Contains(u, "V6E"):
		return "v6e-1"
	case strings.Contains(u, "V5E"):
		return "v5e-1"
	default:
		return strings.TrimSpace(raw)
	}
}

type HistoryRecord struct {
	ID              string               `json:"id"`
	Kind            HistoryEntryKind     `json:"kind"`
	InFlight        bool                 `json:"inFlight,omitempty"`
	AccountID       string               `json:"accountId"`
	AccountName     string               `json:"accountName"`
	AccountColor    AccountColorTag      `json:"accountColor,omitempty"`
	SessionName     string               `json:"sessionName,omitempty"`
	Endpoint        string               `json:"endpoint,omitempty"`
	StartedAt       time.Time            `json:"startedAt"`
	EndedAt         *time.Time           `json:"endedAt,omitempty"`
	DurationSec     int                  `json:"durationSec"`
	Hardware        string               `json:"hardware,omitempty"`
	HighMem         bool                 `json:"highMem,omitempty"`
	RecipeLabel     string               `json:"recipeLabel,omitempty"`
	ConsumedCU      float64              `json:"consumedCU,omitempty"`
	BillingBasis    BillingBasis         `json:"billingBasis,omitempty"`
	ShutdownReason  ShutdownReason       `json:"shutdownReason,omitempty"`
	FallbackUsed    bool                 `json:"fallbackUsed,omitempty"`
	ModelDowngraded bool                 `json:"modelDowngraded,omitempty"`
	TargetsSummary  string               `json:"targetsSummary,omitempty"`
	Rounds          int                  `json:"rounds,omitempty"`
	AllocOutcome    AllocationOutcomeTag `json:"allocOutcome,omitempty"`
	Detail          string               `json:"detail,omitempty"`

	// In-flight crash-recovery metadata (only persisted while InFlight == true, stripped on close)
	StartBalance       float64    `json:"startBalance,omitempty"`
	StartBalanceValid  bool       `json:"startBalanceValid,omitempty"`
	Adopted            bool       `json:"adopted,omitempty"`
	MaxAssignments     int        `json:"maxAssignments,omitempty"`
	MeasuredSingleRate float64    `json:"measuredSingleRate,omitempty"`
	LastSampleAt       *time.Time `json:"lastSampleAt,omitempty"`
	LastUptimeSec      int        `json:"lastUptimeSec,omitempty"`
	UsedStandardRAM    bool       `json:"usedStandardRAM,omitempty"`
}

type HistoryFilter struct {
	Kind      HistoryEntryKind
	AccountID string
	Days      int // 7 / 30 / 0=全部
}

type HistorySummary struct {
	TotalConsumedCU     float64
	MeasuredSessions    int
	EstimatedSessions   int
	TotalDurationSec    int
	SessionCount        int
	TopHardware         string
	AllocAttempts       int
	AllocSuccesses      int
	AvgAllocDurationSec int
	WatchdogStops       int
	LastWatchdogDetail  string
}

const CurrentHistorySchemaVersion = 1

type historyFilePayload struct {
	SchemaVersion int             `json:"schemaVersion,omitempty"`
	Records       []HistoryRecord `json:"records"`
}

// HistoryStore persists local session consumption and allocation attempt records with thread safety.
type HistoryStore struct {
	mu              sync.Mutex
	path            string
	memoryOnly      bool
	records         []HistoryRecord
	corruptReadOnly bool
	loadErr         error
	backupPath      string
}

func HistoryFilePath() string {
	return filepath.Join(ConfigDir(), "history.json")
}

func NewMemoryHistoryStore() *HistoryStore {
	return &HistoryStore{
		memoryOnly: true,
		records:    []HistoryRecord{},
	}
}

func LoadHistoryStore(path string) *HistoryStore {
	if path == "" {
		path = HistoryFilePath()
	}
	store := &HistoryStore{
		path:    path,
		records: []HistoryRecord{},
	}
	raw, err := os.ReadFile(path)
	if err != nil || len(strings.TrimSpace(string(raw))) == 0 {
		return store
	}
	var payload historyFilePayload
	if err := json.Unmarshal(raw, &payload); err == nil && payload.Records != nil {
		if payload.SchemaVersion > CurrentHistorySchemaVersion {
			backupPath := fmt.Sprintf("%s.future-v%d.%d", path, payload.SchemaVersion, time.Now().Unix())
			_ = os.WriteFile(backupPath, raw, 0o600)
			store.corruptReadOnly = true
			store.backupPath = backupPath
			store.loadErr = fmt.Errorf("历史账单版本 (v%d) 高于当前客户端支持版本，已备份并设为只读保护", payload.SchemaVersion)
		}
		store.records = payload.Records
	} else {
		var list []HistoryRecord
		if err := json.Unmarshal(raw, &list); err != nil {
			backupPath := fmt.Sprintf("%s.corrupt.%d", path, time.Now().Unix())
			_ = os.WriteFile(backupPath, raw, 0o600)
			store.corruptReadOnly = true
			store.backupPath = backupPath
			store.loadErr = fmt.Errorf("历史账单文件损坏，已备份至 %s 并启用只读保护防止覆写", filepath.Base(backupPath))
			return store
		}
		store.records = list
	}
	store.sanitizeAndPruneLocked(time.Now())
	return store
}

// LoadError returns any parse/corruption error encountered while loading the history file along with the backup path.
func (s *HistoryStore) LoadError() (error, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadErr, s.backupPath
}

// IsCorruptReadOnly reports whether disk writes are blocked to prevent overwriting a corrupt history file.
func (s *HistoryStore) IsCorruptReadOnly() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.corruptReadOnly
}

func cloneRecord(rec HistoryRecord) HistoryRecord {
	if rec.EndedAt != nil {
		t := *rec.EndedAt
		rec.EndedAt = &t
	}
	if rec.LastSampleAt != nil {
		t := *rec.LastSampleAt
		rec.LastSampleAt = &t
	}
	return rec
}

func normalizeHistoryRecords(records []HistoryRecord, now time.Time) []HistoryRecord {
	if now.IsZero() {
		now = time.Now()
	}
	cutoff := now.Add(-time.Duration(HistoryMaxAgeDays) * 24 * time.Hour)
	inFlight := make([]HistoryRecord, 0, 4)
	kept := make([]HistoryRecord, 0, len(records))
	for _, rec := range records {
		if rec.Kind != HistoryKindSession && rec.Kind != HistoryKindAllocation {
			continue
		}
		if rec.StartedAt.IsZero() {
			rec.StartedAt = now
		}
		if rec.ID == "" {
			rec.ID = uuid.New().String()
		}
		if rec.DurationSec < 0 {
			rec.DurationSec = 0
		}
		if rec.ConsumedCU < 0 {
			rec.ConsumedCU = 0
		}
		if rec.LastUptimeSec < 0 {
			rec.LastUptimeSec = 0
		}
		if rec.InFlight {
			if strings.TrimSpace(rec.AccountID) == "" || strings.TrimSpace(rec.Endpoint) == "" {
				continue
			}
			inFlight = append(inFlight, cloneRecord(rec))
			continue
		}
		if rec.StartedAt.Before(cutoff) {
			continue
		}
		kept = append(kept, cloneRecord(rec))
	}
	sort.SliceStable(kept, func(i, j int) bool {
		return kept[i].StartedAt.After(kept[j].StartedAt)
	})
	if len(kept) > HistoryMaxRecords {
		kept = kept[:HistoryMaxRecords]
	}
	return append(inFlight, kept...)
}

func (s *HistoryStore) sanitizeAndPruneLocked(now time.Time) {
	s.records = normalizeHistoryRecords(s.records, now)
}

func (s *HistoryStore) saveRecordsLocked(recs []HistoryRecord) error {
	if s.memoryOnly || s.path == "" {
		return nil
	}
	if s.corruptReadOnly {
		if s.loadErr != nil {
			return s.loadErr
		}
		return fmt.Errorf("历史账单处于只读保护状态，拒绝覆写原文件")
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if recs == nil {
		recs = []HistoryRecord{}
	}
	data, err := json.MarshalIndent(historyFilePayload{
		SchemaVersion: CurrentHistorySchemaVersion,
		Records:       recs,
	}, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, fmt.Sprintf(".history.%s.tmp", uuid.New().String()[:8]))
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func (s *HistoryStore) saveLocked() error {
	return s.saveRecordsLocked(s.records)
}

func (s *HistoryStore) AddRecord(rec HistoryRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	rec.InFlight = false
	rec.StartBalance = 0
	rec.StartBalanceValid = false
	rec.Adopted = false
	rec.MaxAssignments = 0
	rec.MeasuredSingleRate = 0
	rec.LastSampleAt = nil
	rec.LastUptimeSec = 0
	rec.UsedStandardRAM = false

	if rec.ID == "" {
		rec.ID = uuid.New().String()
	}
	if rec.StartedAt.IsZero() {
		rec.StartedAt = now
	}
	if rec.DurationSec < 0 {
		rec.DurationSec = 0
	}
	if rec.ConsumedCU < 0 {
		rec.ConsumedCU = 0
	}
	// Idempotent upsert by deterministic ID; also close any matching in-flight session for same account+endpoint.
	replaced := false
	next := make([]HistoryRecord, 0, len(s.records)+1)
	for _, cur := range s.records {
		if cur.ID == rec.ID {
			if !replaced {
				next = append(next, cloneRecord(rec))
				replaced = true
			}
			continue
		}
		if cur.InFlight && rec.Kind == HistoryKindSession && cur.AccountID == rec.AccountID && cur.Endpoint == rec.Endpoint {
			continue
		}
		next = append(next, cur)
	}
	if !replaced {
		// Prepend so records with identical StartedAt keep newest-first order.
		next = append([]HistoryRecord{cloneRecord(rec)}, next...)
	}
	candidate := normalizeHistoryRecords(next, now)
	if err := s.saveRecordsLocked(candidate); err != nil {
		return err
	}
	s.records = candidate
	return nil
}

// UpsertInFlight persists the single active in-flight session snapshot for an account inside HistoryStore.
// Rejects stale updates: never resurrects a settled record with the same ID, never regresses sample time/uptime,
// and never lets an older session overwrite a newer active session for the same account.
func (s *HistoryStore) UpsertInFlight(rec HistoryRecord) error {
	if strings.TrimSpace(rec.AccountID) == "" || strings.TrimSpace(rec.Endpoint) == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	rec.Kind = HistoryKindSession
	rec.InFlight = true
	if rec.ID == "" {
		rec.ID = uuid.New().String()
	}
	if rec.StartedAt.IsZero() {
		rec.StartedAt = now
	}
	if rec.ConsumedCU < 0 {
		rec.ConsumedCU = 0
	}
	if rec.LastUptimeSec < 0 {
		rec.LastUptimeSec = 0
	}
	if rec.LastSampleAt == nil || rec.LastSampleAt.IsZero() {
		t := now
		rec.LastSampleAt = &t
	}

	for _, cur := range s.records {
		// 1. Never resurrect a session that has already been settled.
		if !cur.InFlight && cur.ID == rec.ID {
			return nil
		}
		if !cur.InFlight && cur.Kind == HistoryKindSession &&
			cur.AccountID == rec.AccountID && cur.Endpoint == rec.Endpoint &&
			!cur.StartedAt.Before(rec.StartedAt) {
			return nil
		}
		// 2. Reject stale sample for the same in-flight session.
		if cur.InFlight && cur.ID == rec.ID {
			if cur.LastSampleAt != nil && rec.LastSampleAt != nil && cur.LastSampleAt.After(*rec.LastSampleAt) {
				return nil
			}
			if cur.LastUptimeSec > rec.LastUptimeSec && cur.ConsumedCU >= rec.ConsumedCU {
				return nil
			}
		}
		// 3. Never let an older session snapshot displace a newer in-flight session of the same account.
		if cur.InFlight && cur.AccountID == rec.AccountID && cur.ID != rec.ID && cur.StartedAt.After(rec.StartedAt) {
			return nil
		}
	}

	next := make([]HistoryRecord, 0, len(s.records)+1)
	next = append(next, cloneRecord(rec))
	for _, cur := range s.records {
		if cur.InFlight && cur.AccountID == rec.AccountID {
			continue
		}
		if cur.ID == rec.ID && cur.InFlight {
			continue
		}
		next = append(next, cur)
	}
	candidate := normalizeHistoryRecords(next, now)
	if err := s.saveRecordsLocked(candidate); err != nil {
		return err
	}
	s.records = candidate
	return nil
}

// InFlightByAccount returns a snapshot map of persisted in-flight sessions keyed by account ID.
func (s *HistoryStore) InFlightByAccount() map[string]HistoryRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]HistoryRecord{}
	for _, rec := range s.records {
		if !rec.InFlight || strings.TrimSpace(rec.AccountID) == "" || strings.TrimSpace(rec.Endpoint) == "" {
			continue
		}
		if _, exists := out[rec.AccountID]; !exists {
			out[rec.AccountID] = cloneRecord(rec)
		}
	}
	return out
}

// DeleteInFlight removes an in-flight session snapshot for accountID (and optionally matching sessionID if non-empty).
func (s *HistoryStore) DeleteInFlight(accountID, sessionID string) error {
	if strings.TrimSpace(accountID) == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	next := make([]HistoryRecord, 0, len(s.records))
	for _, cur := range s.records {
		if cur.InFlight && cur.AccountID == accountID && (sessionID == "" || cur.ID == sessionID) {
			changed = true
			continue
		}
		next = append(next, cur)
	}
	if !changed {
		return nil
	}
	if err := s.saveRecordsLocked(next); err != nil {
		return err
	}
	s.records = next
	return nil
}

// ImportLegacyActiveSessions migrates any unsettled sessions from a legacy v0.3.20-v0.3.23 active_sessions.json
// into HistoryStore as InFlight records, then archives the legacy file once persisted.
func (s *HistoryStore) ImportLegacyActiveSessions(legacyPath string) error {
	if s.memoryOnly || strings.TrimSpace(legacyPath) == "" {
		return nil
	}
	raw, err := os.ReadFile(legacyPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	type legacyActiveSession struct {
		AccountID          string    `json:"accountId"`
		AccountName        string    `json:"accountName"`
		AccountColor       string    `json:"accountColor"`
		SessionName        string    `json:"sessionName"`
		Endpoint           string    `json:"endpoint"`
		Hardware           string    `json:"hardware"`
		HighMem            bool      `json:"highMem"`
		RecipeLabel        string    `json:"recipeLabel"`
		StartedAt          time.Time `json:"startedAt"`
		LastSampleAt       time.Time `json:"lastSampleAt"`
		LastUptimeSec      int       `json:"lastUptimeSec"`
		StartBalance       float64   `json:"startBalance"`
		StartBalanceValid  bool      `json:"startBalanceValid"`
		Adopted            bool      `json:"adopted"`
		MaxAssignments     int       `json:"maxAssignments"`
		MeasuredSingleRate float64   `json:"measuredSingleRate"`
		EstimatedCU        float64   `json:"estimatedCU"`
		FallbackUsed       bool      `json:"fallbackUsed"`
		ModelDowngraded    bool      `json:"modelDowngraded"`
		UsedStandardRAM    bool      `json:"usedStandardRAM"`
	}
	var payload struct {
		Sessions map[string]legacyActiveSession `json:"sessions"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil
	}
	for accID, item := range payload.Sessions {
		if strings.TrimSpace(item.AccountID) == "" {
			item.AccountID = accID
		}
		if strings.TrimSpace(item.AccountID) == "" || strings.TrimSpace(item.Endpoint) == "" {
			continue
		}
		started := item.StartedAt
		if started.IsZero() {
			started = item.LastSampleAt
		}
		if started.IsZero() {
			started = time.Now()
		}
		sample := item.LastSampleAt
		if sample.IsZero() {
			sample = started
		}
		id := fmt.Sprintf("legacy-%s-%s-%d", item.AccountID, item.Endpoint, started.UTC().Unix())
		rec := HistoryRecord{
			ID:                 id,
			Kind:               HistoryKindSession,
			InFlight:           true,
			AccountID:          item.AccountID,
			AccountName:        item.AccountName,
			AccountColor:       AccountColorTag(item.AccountColor),
			SessionName:        item.SessionName,
			Endpoint:           item.Endpoint,
			StartedAt:          started,
			DurationSec:        item.LastUptimeSec,
			Hardware:           NormalizeHardwareKey(item.Hardware),
			HighMem:            item.HighMem,
			RecipeLabel:        item.RecipeLabel,
			ConsumedCU:         item.EstimatedCU,
			FallbackUsed:       item.FallbackUsed,
			ModelDowngraded:    item.ModelDowngraded,
			StartBalance:       item.StartBalance,
			StartBalanceValid:  item.StartBalanceValid,
			Adopted:            item.Adopted,
			MaxAssignments:     item.MaxAssignments,
			MeasuredSingleRate: item.MeasuredSingleRate,
			LastSampleAt:       &sample,
			LastUptimeSec:      item.LastUptimeSec,
			UsedStandardRAM:    item.UsedStandardRAM,
		}
		if err := s.UpsertInFlight(rec); err != nil {
			return err
		}
	}
	_ = os.Rename(legacyPath, legacyPath+".migrated")
	return nil
}

func (s *HistoryStore) Query(filter HistoryFilter, now time.Time) ([]HistoryRecord, HistorySummary) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if now.IsZero() {
		now = time.Now()
	}
	var cutoff time.Time
	hasCutoff := filter.Days > 0
	if hasCutoff {
		cutoff = now.Add(-time.Duration(filter.Days) * 24 * time.Hour)
	}

	out := make([]HistoryRecord, 0, len(s.records))
	var summary HistorySummary
	type hwStat struct {
		sessions    int
		durationSec int
	}
	hwStats := make(map[string]hwStat)
	totalAllocDur := 0
	var latestWatchdog *HistoryRecord

	for _, rec := range s.records {
		if rec.InFlight {
			continue
		}
		if filter.Kind != "" && rec.Kind != filter.Kind {
			continue
		}
		if filter.AccountID != "" && rec.AccountID != filter.AccountID {
			continue
		}
		if hasCutoff && rec.StartedAt.Before(cutoff) {
			continue
		}
		cp := cloneRecord(rec)
		out = append(out, cp)

		switch cp.Kind {
		case HistoryKindSession:
			summary.SessionCount++
			dur := max(0, cp.DurationSec)
			summary.TotalDurationSec += dur
			if cp.ConsumedCU > 0 {
				summary.TotalConsumedCU += cp.ConsumedCU
			}
			switch cp.BillingBasis {
			case BillingMeasured:
				summary.MeasuredSessions++
			case BillingEstimated:
				summary.EstimatedSessions++
			}
			if hw := NormalizeHardwareKey(cp.Hardware); hw != "" {
				st := hwStats[hw]
				st.sessions++
				st.durationSec += dur
				hwStats[hw] = st
			}
			if cp.ShutdownReason == ShutdownAutoIdle {
				summary.WatchdogStops++
				if latestWatchdog == nil {
					wCopy := cloneRecord(cp)
					latestWatchdog = &wCopy
				}
			}
		case HistoryKindAllocation:
			summary.AllocAttempts++
			totalAllocDur += max(0, cp.DurationSec)
			if cp.AllocOutcome == AllocTagSuccess {
				summary.AllocSuccesses++
			}
		}
	}

	bestCount := -1
	bestDur := -1
	for hw, st := range hwStats {
		if st.sessions > bestCount ||
			(st.sessions == bestCount && st.durationSec > bestDur) ||
			(st.sessions == bestCount && st.durationSec == bestDur && hw < summary.TopHardware) {
			bestCount = st.sessions
			bestDur = st.durationSec
			summary.TopHardware = hw
		}
	}

	if summary.AllocAttempts > 0 {
		summary.AvgAllocDurationSec = (totalAllocDur + summary.AllocAttempts/2) / summary.AllocAttempts
	}

	if latestWatchdog != nil {
		ts := latestWatchdog.StartedAt
		if latestWatchdog.EndedAt != nil && !latestWatchdog.EndedAt.IsZero() {
			ts = *latestWatchdog.EndedAt
		}
		hw := NormalizeHardwareKey(latestWatchdog.Hardware)
		if hw != "" {
			summary.LastWatchdogDetail = fmt.Sprintf("最近 %s · %s", ts.Local().Format("01-02 15:04"), hw)
		} else {
			summary.LastWatchdogDetail = fmt.Sprintf("最近 %s", ts.Local().Format("01-02 15:04"))
		}
	}

	return out, summary
}

func sanitizeCSVText(s string) string {
	if s == "" {
		return ""
	}
	if s[0] == '\t' || s[0] == '\r' || s[0] == '\n' {
		return "'" + s
	}
	trimmed := strings.TrimLeft(s, " \t\r\n\u3000")
	if trimmed == "" {
		return s
	}
	switch trimmed[0] {
	case '=', '+', '-', '@':
		return "'" + s
	}
	return s
}

func (s *HistoryStore) ExportCSV(filter HistoryFilter, now time.Time) string {
	records, _ := s.Query(filter, now)

	var buf strings.Builder
	buf.WriteString("\uFEFF")
	w := csv.NewWriter(&buf)

	_ = w.Write([]string{
		"ID",
		"类型",
		"账号ID",
		"账号名称",
		"实例名称",
		"开始时间",
		"结束时间",
		"时长(秒)",
		"时长",
		"硬件",
		"高内存",
		"部署方案",
		"消耗CU",
		"计费口径",
		"关机原因",
		"触发保底",
		"跨模型降级",
		"队列目标",
		"抢卡轮次",
		"抢卡结果",
		"详情",
	})

	boolCN := func(v bool) string {
		if v {
			return "是"
		}
		return "否"
	}
	kindCN := func(k HistoryEntryKind) string {
		switch k {
		case HistoryKindSession:
			return "开机会话"
		case HistoryKindAllocation:
			return "抢卡记录"
		default:
			return string(k)
		}
	}

	for _, rec := range records {
		endedStr := ""
		if rec.EndedAt != nil && !rec.EndedAt.IsZero() {
			endedStr = rec.EndedAt.Local().Format("2006-01-02 15:04:05")
		}
		_ = w.Write([]string{
			sanitizeCSVText(rec.ID),
			sanitizeCSVText(kindCN(rec.Kind)),
			sanitizeCSVText(rec.AccountID),
			sanitizeCSVText(rec.AccountName),
			sanitizeCSVText(rec.SessionName),
			rec.StartedAt.Local().Format("2006-01-02 15:04:05"),
			endedStr,
			fmt.Sprintf("%d", rec.DurationSec),
			FormatDuration(rec.DurationSec),
			sanitizeCSVText(rec.Hardware),
			boolCN(rec.HighMem),
			sanitizeCSVText(rec.RecipeLabel),
			fmt.Sprintf("%.2f", rec.ConsumedCU),
			sanitizeCSVText(rec.BillingBasis.DisplayName()),
			sanitizeCSVText(rec.ShutdownReason.DisplayName()),
			boolCN(rec.FallbackUsed),
			boolCN(rec.ModelDowngraded),
			sanitizeCSVText(rec.TargetsSummary),
			fmt.Sprintf("%d", rec.Rounds),
			sanitizeCSVText(rec.AllocOutcome.DisplayName()),
			sanitizeCSVText(rec.Detail),
		})
	}
	w.Flush()
	return buf.String()
}

func (s *HistoryStore) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.corruptReadOnly {
		if s.loadErr != nil {
			return s.loadErr
		}
		return fmt.Errorf("历史账单处于只读保护状态，拒绝清空原文件")
	}
	kept := make([]HistoryRecord, 0)
	for _, cur := range s.records {
		if cur.InFlight {
			kept = append(kept, cur)
		}
	}
	if err := s.saveRecordsLocked(kept); err != nil {
		return err
	}
	s.records = kept
	return nil
}
