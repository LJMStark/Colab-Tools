package core

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const AllocationMaxSafeCount = 1_000_000

type AllocationBucket struct {
	Attempts  int `json:"attempts"`
	Successes int `json:"successes"`
}

func NewAllocationBucket(attempts, successes int) AllocationBucket {
	a := clampInt(attempts, 0, AllocationMaxSafeCount)
	s := clampInt(successes, 0, a)
	return AllocationBucket{Attempts: a, Successes: s}
}

type TimeSlotHitRate struct {
	ID        int // 0..5 representing 00-04 .. 20-24
	Label     string
	Attempts  int
	Successes int
}

func (t TimeSlotHitRate) Rate() (float64, bool) {
	if t.Attempts <= 0 {
		return 0, false
	}
	r := float64(t.Successes) / float64(t.Attempts)
	return math.Min(1.0, math.Max(0.0, r)), true
}

// AllocationInsights is a strictly local, privacy-preserving 6-slot allocation hit-rate store.
type AllocationInsights struct {
	BucketsByHardware map[string]map[string]AllocationBucket `json:"bucketsByHardware"`
}

func NewAllocationInsights() AllocationInsights {
	return AllocationInsights{
		BucketsByHardware: map[string]map[string]AllocationBucket{},
	}
}

func DefaultInsightsPath() string {
	return filepath.Join(ConfigDir(), "allocation-stats.json")
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

func (a *AllocationInsights) Sanitize() {
	clean := make(map[string]map[string]AllocationBucket)
	for hw, hours := range a.BucketsByHardware {
		norm := NormalizeHardwareKey(hw)
		if norm == "" {
			continue
		}
		if clean[norm] == nil {
			clean[norm] = make(map[string]AllocationBucket)
		}
		for hKey, rawB := range hours {
			hInt, err := strconv.Atoi(hKey)
			if err != nil || hInt < 0 || hInt > 23 {
				continue
			}
			b := NewAllocationBucket(rawB.Attempts, rawB.Successes)
			k := strconv.Itoa(hInt)
			existing := clean[norm][k]
			mergedAtt := clampInt(existing.Attempts+b.Attempts, 0, AllocationMaxSafeCount)
			mergedSuc := clampInt(existing.Successes+b.Successes, 0, mergedAtt)
			clean[norm][k] = AllocationBucket{Attempts: mergedAtt, Successes: mergedSuc}
		}
		if len(clean[norm]) == 0 {
			delete(clean, norm)
		}
	}
	a.BucketsByHardware = clean
}

func (a *AllocationInsights) Record(hardware string, succeeded bool, at time.Time) {
	key := NormalizeHardwareKey(hardware)
	if key == "" {
		return
	}
	if a.BucketsByHardware == nil {
		a.BucketsByHardware = map[string]map[string]AllocationBucket{}
	}
	if a.BucketsByHardware[key] == nil {
		a.BucketsByHardware[key] = map[string]AllocationBucket{}
	}
	hk := strconv.Itoa(clampInt(at.Hour(), 0, 23))
	old := a.BucketsByHardware[key][hk]
	nextAtt := clampInt(old.Attempts+1, 0, AllocationMaxSafeCount)
	inc := 0
	if succeeded {
		inc = 1
	}
	nextSuc := clampInt(old.Successes+inc, 0, nextAtt)
	a.BucketsByHardware[key][hk] = AllocationBucket{Attempts: nextAtt, Successes: nextSuc}
}

func (a AllocationInsights) TotalAttempts(hardware string) int {
	key := NormalizeHardwareKey(hardware)
	total := 0
	for _, b := range a.BucketsByHardware[key] {
		total = clampInt(total+max(0, b.Attempts), 0, AllocationMaxSafeCount)
	}
	return total
}

func (a AllocationInsights) SixSlotHeatmap(hardware string) []TimeSlotHitRate {
	key := NormalizeHardwareKey(hardware)
	hwMap := a.BucketsByHardware[key]
	labels := []string{"00–04", "04–08", "08–12", "12–16", "16–20", "20–24"}
	out := make([]TimeSlotHitRate, 6)
	for slot := 0; slot < 6; slot++ {
		att := 0
		suc := 0
		for h := slot * 4; h < slot*4+4; h++ {
			if b, ok := hwMap[strconv.Itoa(h)]; ok {
				cb := NewAllocationBucket(b.Attempts, b.Successes)
				att = clampInt(att+cb.Attempts, 0, AllocationMaxSafeCount)
				suc = clampInt(suc+cb.Successes, 0, att)
			}
		}
		out[slot] = TimeSlotHitRate{
			ID:        slot,
			Label:     labels[slot],
			Attempts:  att,
			Successes: suc,
		}
	}
	return out
}

func (a AllocationInsights) RecommendedWindowSummary(hardware string) string {
	key := NormalizeHardwareKey(hardware)
	slots := a.SixSlotHeatmap(key)
	total := 0
	var ranked []TimeSlotHitRate
	for _, s := range slots {
		total = clampInt(total+s.Attempts, 0, AllocationMaxSafeCount)
		if r, ok := s.Rate(); ok && r > 0 {
			ranked = append(ranked, s)
		}
	}
	if total < 3 {
		return ""
	}
	if len(ranked) == 0 {
		return fmt.Sprintf("近期 %s 本地尚无命中记录，建议尝试清晨低峰预约", key)
	}
	sort.Slice(ranked, func(i, j int) bool {
		ri, _ := ranked[i].Rate()
		rj, _ := ranked[j].Rate()
		if ri != rj {
			return ri > rj
		}
		if ranked[i].Attempts != ranked[j].Attempts {
			return ranked[i].Attempts > ranked[j].Attempts
		}
		return ranked[i].ID < ranked[j].ID
	})
	best := ranked[0]
	rate, _ := best.Rate()
	pct := int(math.Round(rate * 100))
	return fmt.Sprintf("本机历史 %s 在 %s 命中率最高（%d%% · %d/%d）", key, best.Label, pct, best.Successes, best.Attempts)
}

func LoadAllocationInsights(path string) AllocationInsights {
	if path == "" {
		path = DefaultInsightsPath()
	}
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 {
		return NewAllocationInsights()
	}
	var out AllocationInsights
	if err := json.Unmarshal(raw, &out); err != nil {
		return NewAllocationInsights()
	}
	out.Sanitize()
	return out
}

func (a AllocationInsights) Save(path string) error {
	if path == "" {
		path = DefaultInsightsPath()
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, fmt.Sprintf(".insights.%s.tmp", uuid.New().String()[:8]))
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
