package core_test

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LJMStark/Colab-Tools/internal/core"
)

func TestHistoryEnumDisplayNamesAndPath(t *testing.T) {
	shutdownCases := map[core.ShutdownReason]string{
		core.ShutdownManual:         "手动关机",
		core.ShutdownAutoIdle:       "空闲自动关机",
		core.ShutdownBatchStop:      "全部关机止损",
		core.ShutdownCapReached:     "24h 上限",
		core.ShutdownExternalOrLost: "外部或异常断开",
		core.ShutdownReason("other"): "other",
	}
	for in, want := range shutdownCases {
		if got := in.DisplayName(); got != want {
			t.Fatalf("ShutdownReason(%q).DisplayName() = %q, want %q", in, got, want)
		}
	}

	allocCases := map[core.AllocationOutcomeTag]string{
		core.AllocTagSuccess:     "抢到",
		core.AllocTagShortage:    "缺货",
		core.AllocTagRateLimited: "限流冷却",
		core.AllocTagCancelled:   "已取消",
		core.AllocTagError:       "异常失败",
		core.AllocationOutcomeTag("x"): "x",
	}
	for in, want := range allocCases {
		if got := in.DisplayName(); got != want {
			t.Fatalf("AllocationOutcomeTag(%q).DisplayName() = %q, want %q", in, got, want)
		}
	}

	billingCases := map[core.BillingBasis]string{
		core.BillingMeasured:  "实测",
		core.BillingEstimated: "估算",
		core.BillingFree:      "免费",
		core.BillingBasis("y"): "y",
	}
	for in, want := range billingCases {
		if got := in.DisplayName(); got != want {
			t.Fatalf("BillingBasis(%q).DisplayName() = %q, want %q", in, got, want)
		}
	}

	tmpDir := t.TempDir()
	t.Setenv("COLAB_TOOLS_CONFIG_DIR", tmpDir)
	wantPath := filepath.Join(tmpDir, "history.json")
	if got := core.HistoryFilePath(); got != wantPath {
		t.Fatalf("HistoryFilePath() = %q, want %q", got, wantPath)
	}
}

func TestHistoryStoreAddQueryPruneAndPersistence(t *testing.T) {
	tmpDir := t.TempDir()
	histPath := filepath.Join(tmpDir, "history.json")

	// Missing file -> empty store
	missingStore := core.LoadHistoryStore(histPath)
	recs, sum := missingStore.Query(core.HistoryFilter{}, time.Now())
	if len(recs) != 0 || sum.SessionCount != 0 {
		t.Fatalf("expected empty store for missing file, got %d recs", len(recs))
	}

	// Corrupt file -> safe fallback empty store
	corruptPath := filepath.Join(tmpDir, "corrupt.json")
	if err := os.WriteFile(corruptPath, []byte("{corrupt-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	corruptStore := core.LoadHistoryStore(corruptPath)
	recs, _ = corruptStore.Query(core.HistoryFilter{}, time.Now())
	if len(recs) != 0 {
		t.Fatalf("expected empty store for corrupt file, got %d recs", len(recs))
	}

	// 90-day pruning + auto-ID + negative clamp + descending sort + disk persistence
	now := time.Now()
	store := core.LoadHistoryStore(histPath)
	store.AddRecord(core.HistoryRecord{
		Kind:        core.HistoryKindSession,
		AccountID:   "acc-old",
		AccountName: "Old",
		StartedAt:   now.AddDate(0, 0, -(core.HistoryMaxAgeDays + 2)),
		DurationSec: 600,
	})
	store.AddRecord(core.HistoryRecord{
		Kind:        core.HistoryKindSession,
		AccountID:   "acc-1",
		AccountName: "Work",
		StartedAt:   now.Add(-2 * time.Hour),
		DurationSec: -10,
		ConsumedCU:  -5,
		Hardware:    "L4",
	})
	store.AddRecord(core.HistoryRecord{
		Kind:        core.HistoryKindSession,
		AccountID:   "acc-1",
		AccountName: "Work",
		StartedAt:   now.Add(-1 * time.Hour),
		DurationSec: 3600,
		ConsumedCU:  12.5,
		Hardware:    "A100",
	})

	info, err := os.Stat(histPath)
	if err != nil {
		t.Fatalf("expected persisted history.json: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("expected 0600 file perm, got %o", perm)
	}

	reloaded := core.LoadHistoryStore(histPath)
	got, _ := reloaded.Query(core.HistoryFilter{}, now)
	if len(got) != 2 {
		t.Fatalf("expected 2 records after 90-day prune, got %d", len(got))
	}
	if got[0].ID == "" || got[1].ID == "" {
		t.Fatal("expected auto-generated record IDs")
	}
	if !got[0].StartedAt.After(got[1].StartedAt) {
		t.Fatal("expected descending StartedAt order")
	}
	if got[1].DurationSec != 0 || got[1].ConsumedCU != 0 {
		t.Fatalf("expected negative values clamped to 0, got dur=%d cu=%v", got[1].DurationSec, got[1].ConsumedCU)
	}

	// Clear persists empty slice
	reloaded.Clear()
	afterClear := core.LoadHistoryStore(histPath)
	clearedRecs, _ := afterClear.Query(core.HistoryFilter{}, now)
	if len(clearedRecs) != 0 {
		t.Fatalf("expected 0 records after Clear, got %d", len(clearedRecs))
	}

	// 1000-record cap in memory store
	mem := core.NewMemoryHistoryStore()
	total := core.HistoryMaxRecords + 50
	for i := 0; i < total; i++ {
		mem.AddRecord(core.HistoryRecord{
			ID:          fmt.Sprintf("rec-%04d", i),
			Kind:        core.HistoryKindAllocation,
			AccountID:   "acc-cap",
			AccountName: "Cap",
			StartedAt:   now.Add(-time.Duration(total-i) * time.Second),
			DurationSec: 5,
		})
	}
	capped, _ := mem.Query(core.HistoryFilter{}, now)
	if len(capped) != core.HistoryMaxRecords {
		t.Fatalf("expected %d records after cap prune, got %d", core.HistoryMaxRecords, len(capped))
	}
	if capped[0].ID != fmt.Sprintf("rec-%04d", total-1) {
		t.Fatalf("expected newest record first, got %q", capped[0].ID)
	}
	if capped[len(capped)-1].ID != "rec-0050" {
		t.Fatalf("expected oldest kept record rec-0050, got %q", capped[len(capped)-1].ID)
	}
}

func TestHistoryStoreQuerySummaryAndExportCSV(t *testing.T) {
	mem := core.NewMemoryHistoryStore()
	now := time.Date(2026, 11, 26, 18, 0, 0, 0, time.Local)

	end1 := time.Date(2026, 11, 26, 16, 45, 0, 0, time.Local)
	start1 := end1.Add(-2 * time.Hour)
	end2 := time.Date(2026, 11, 24, 10, 15, 0, 0, time.Local)
	start2 := end2.Add(-1 * time.Hour)

	// Two A100 sessions (one auto_idle watchdog stop), one G4 session 15 days ago, one Free T4 session
	mem.AddRecord(core.HistoryRecord{
		ID:              "s1",
		Kind:            core.HistoryKindSession,
		AccountID:       "acc-a",
		AccountName:     "主力账号",
		SessionName:     "colab-main",
		StartedAt:       start1,
		EndedAt:         &end1,
		DurationSec:     7200,
		Hardware:        "NVIDIA A100-SXM4-40GB",
		HighMem:         true,
		RecipeLabel:     "H3 原版 (31B)",
		ConsumedCU:      24.0,
		BillingBasis:    core.BillingMeasured,
		ShutdownReason:  core.ShutdownAutoIdle,
		FallbackUsed:    false,
		ModelDowngraded: false,
		Detail:          "自动休眠，停机成功",
	})
	mem.AddRecord(core.HistoryRecord{
		ID:             "s2",
		Kind:           core.HistoryKindSession,
		AccountID:      "acc-a",
		AccountName:    "主力账号",
		SessionName:    "colab-main",
		StartedAt:      start2,
		EndedAt:        &end2,
		DurationSec:    3600,
		Hardware:       "A100",
		HighMem:        true,
		RecipeLabel:    "H3 Flash",
		ConsumedCU:     11.5,
		BillingBasis:   core.BillingEstimated,
		ShutdownReason: core.ShutdownManual,
	})
	mem.AddRecord(core.HistoryRecord{
		ID:             "s3",
		Kind:           core.HistoryKindSession,
		AccountID:      "acc-b",
		AccountName:    "备用账号",
		SessionName:    "colab-sub",
		StartedAt:      now.Add(-15 * 24 * time.Hour),
		DurationSec:    1800,
		Hardware:       "G4",
		ConsumedCU:     3.5,
		BillingBasis:   core.BillingMeasured,
		ShutdownReason: core.ShutdownAutoIdle,
	})
	mem.AddRecord(core.HistoryRecord{
		ID:             "s4",
		Kind:           core.HistoryKindSession,
		AccountID:      "acc-b",
		AccountName:    "备用账号",
		SessionName:    "colab-free",
		StartedAt:      now.Add(-45 * 24 * time.Hour),
		DurationSec:    900,
		Hardware:       "T4",
		BillingBasis:   core.BillingFree,
		ShutdownReason: core.ShutdownCapReached,
	})

	// Allocation records
	mem.AddRecord(core.HistoryRecord{
		ID:             "a1",
		Kind:           core.HistoryKindAllocation,
		AccountID:      "acc-a",
		AccountName:    "主力账号",
		StartedAt:      now.Add(-3 * time.Hour),
		DurationSec:    100,
		Hardware:       "A100",
		TargetsSummary: "G4 → A100",
		Rounds:         2,
		AllocOutcome:   core.AllocTagSuccess,
		FallbackUsed:   true,
	})
	mem.AddRecord(core.HistoryRecord{
		ID:             "a2",
		Kind:           core.HistoryKindAllocation,
		AccountID:      "acc-a",
		AccountName:    "主力账号",
		StartedAt:      now.Add(-4 * time.Hour),
		DurationSec:    200,
		TargetsSummary: "G4 → A100",
		Rounds:         5,
		AllocOutcome:   core.AllocTagShortage,
	})

	// Filter 7 days for acc-a
	recs7, sum7 := mem.Query(core.HistoryFilter{AccountID: "acc-a", Days: 7}, now)
	if len(recs7) != 4 {
		t.Fatalf("expected 4 records for acc-a within 7d, got %d", len(recs7))
	}
	if sum7.SessionCount != 2 || sum7.MeasuredSessions != 1 || sum7.EstimatedSessions != 1 {
		t.Fatalf("unexpected session summary: %+v", sum7)
	}
	if sum7.TotalDurationSec != 10800 || sum7.TotalConsumedCU != 35.5 {
		t.Fatalf("unexpected totals: %+v", sum7)
	}
	if sum7.TopHardware != "A100" {
		t.Fatalf("expected TopHardware A100, got %q", sum7.TopHardware)
	}
	if sum7.AllocAttempts != 2 || sum7.AllocSuccesses != 1 || sum7.AvgAllocDurationSec != 150 {
		t.Fatalf("unexpected alloc summary: %+v", sum7)
	}
	if sum7.WatchdogStops != 1 || sum7.LastWatchdogDetail != "最近 11-26 16:45 · A100" {
		t.Fatalf("unexpected watchdog summary: %+v", sum7)
	}

	// Filter Kind=session, Days=0 (all)
	recsAllSess, sumAllSess := mem.Query(core.HistoryFilter{Kind: core.HistoryKindSession, Days: 0}, now)
	if len(recsAllSess) != 4 || sumAllSess.SessionCount != 4 || sumAllSess.WatchdogStops != 2 {
		t.Fatalf("unexpected all-session summary: recs=%d sum=%+v", len(recsAllSess), sumAllSess)
	}

	// CSV export with UTF-8 BOM and valid structure
	csvOut := mem.ExportCSV(core.HistoryFilter{AccountID: "acc-a", Days: 7}, now)
	if !strings.HasPrefix(csvOut, "\uFEFF") {
		t.Fatal("expected UTF-8 BOM prefix in ExportCSV")
	}
	parsed, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(csvOut, "\uFEFF"))).ReadAll()
	if err != nil {
		t.Fatalf("invalid CSV output: %v", err)
	}
	if len(parsed) != 5 { // header + 4 rows
		t.Fatalf("expected 5 CSV rows (1 header + 4 data), got %d", len(parsed))
	}
	allCSV := strings.Join(func() []string {
		lines := make([]string, 0, len(parsed))
		for _, row := range parsed {
			lines = append(lines, strings.Join(row, ","))
		}
		return lines
	}(), "\n")
	for _, want := range []string{"抢卡记录", "抢到", "开机会话", "空闲自动关机", "实测"} {
		if !strings.Contains(allCSV, want) {
			t.Fatalf("expected localized label %q in CSV output:\n%s", want, allCSV)
		}
	}
}

func TestHistoryStoreConcurrentAccess(t *testing.T) {
	mem := core.NewMemoryHistoryStore()
	now := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				mem.AddRecord(core.HistoryRecord{
					Kind:        core.HistoryKindSession,
					AccountID:   fmt.Sprintf("acc-%d", worker%2),
					AccountName: "Concurrent",
					StartedAt:   now.Add(-time.Duration(j) * time.Minute),
					DurationSec: 60,
					Hardware:    "L4",
					ConsumedCU:  0.1,
				})
				_, _ = mem.Query(core.HistoryFilter{Days: 7}, now)
				_ = mem.ExportCSV(core.HistoryFilter{Days: 7}, now)
			}
		}(i)
	}
	wg.Wait()

	recs, sum := mem.Query(core.HistoryFilter{}, now)
	if len(recs) != 160 || sum.SessionCount != 160 {
		t.Fatalf("expected 160 concurrent records, got %d (sum=%d)", len(recs), sum.SessionCount)
	}
}
