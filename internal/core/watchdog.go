package core

import (
	"encoding/json"
	"strings"
	"time"
)

const (
	DefaultSignalMaxAge   = 90 * time.Second
	HardwareSignalMaxAge  = 210 * time.Second
	StatusFileMaxAgeSec   = 300
	HardwareIdleMaxGPUPct = 5
	HardwareIdleMaxCPUPct = 25
)

type WatchdogActionKind string

const (
	WatchdogNone           WatchdogActionKind = "none"
	WatchdogPausedNoSignal WatchdogActionKind = "paused_no_signal"
	WatchdogWarn           WatchdogActionKind = "warn"
	WatchdogShutdown       WatchdogActionKind = "shutdown"
)

type WatchdogAction struct {
	Kind             WatchdogActionKind
	RemainingMinutes int
}

// WorkloadStatusPayload models `/tmp/colab-tools-status.json`.
type WorkloadStatusPayload struct {
	Kind         string   `json:"kind"`
	Name         string   `json:"name"`
	UIURL        string   `json:"ui_url"`
	Port         *int     `json:"port"`
	MetricLabel  string   `json:"metric_label"`
	MetricValue  string   `json:"metric_value"`
	MetricUnit   string   `json:"metric_unit"`
	MetricRatio  *float64 `json:"metric_ratio"`
	Busy         *bool    `json:"busy"`
	QueueRunning *int     `json:"queue_running"`
	QueuePending *int     `json:"queue_pending"`
	Detail       string   `json:"detail"`
}

func (p WorkloadStatusPayload) ParsedKind() (WorkloadKind, bool) {
	if strings.TrimSpace(p.Kind) == "" {
		return WorkloadGeneral, false
	}
	return ParseWorkloadKind(p.Kind)
}

func (p WorkloadStatusPayload) IsWorkloadBusy() (bool, bool) {
	if p.Busy != nil {
		return *p.Busy, true
	}
	if p.QueueRunning != nil && p.QueuePending != nil {
		return (*p.QueueRunning + *p.QueuePending) > 0, true
	}
	if p.QueueRunning != nil {
		return *p.QueueRunning > 0, true
	}
	if p.QueuePending != nil {
		return *p.QueuePending > 0, true
	}
	return false, false
}

type ComfyQueueSummary struct {
	Running int
	Pending int
}

func (c ComfyQueueSummary) IsBusy() bool {
	return c.Running > 0 || c.Pending > 0
}

func ParseWorkloadStatusJSON(raw string) (*WorkloadStatusPayload, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, false
	}
	var p WorkloadStatusPayload
	if err := json.Unmarshal([]byte(trimmed), &p); err != nil {
		return nil, false
	}
	return &p, true
}

func ParseComfyQueueJSON(raw string) (*ComfyQueueSummary, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, false
	}
	var obj struct {
		QueueRunning []any `json:"queue_running"`
		QueuePending []any `json:"queue_pending"`
	}
	if err := json.Unmarshal([]byte(trimmed), &obj); err != nil {
		return nil, false
	}
	if obj.QueueRunning == nil || obj.QueuePending == nil {
		return nil, false
	}
	return &ComfyQueueSummary{
		Running: len(obj.QueueRunning),
		Pending: len(obj.QueuePending),
	}, true
}

// EvaluateHardwareActivity requires BOTH GPU < 5% AND CPU < 25% to count as idle.
// Missing readings or TPU hardware NEVER map to idle.
func EvaluateHardwareActivity(gpuUtil, cpuUtil *int, isTPU bool, now time.Time) ActivitySignal {
	if isTPU || gpuUtil == nil || cpuUtil == nil {
		return ActivitySignal{Kind: SignalUnknown}
	}
	if *gpuUtil < HardwareIdleMaxGPUPct && *cpuUtil < HardwareIdleMaxCPUPct {
		return ActivitySignal{Kind: SignalIdle, ObservedAt: now}
	}
	return ActivitySignal{Kind: SignalBusy, ObservedAt: now}
}

type LLMSlotsEvaluation struct {
	Signal          ActivitySignal
	MaxPromptTokens int
	MaxContext      int
	LatestTaskID    int
}

// EvaluateLLMSlots aggregates all llama-server /slots entries so a busy secondary slot never triggers idle.
func EvaluateLLMSlots(slots []SlotInfo, now time.Time) LLMSlotsEvaluation {
	res := LLMSlotsEvaluation{
		Signal:       ActivitySignal{Kind: SignalUnknown},
		LatestTaskID: -1,
	}
	if len(slots) == 0 {
		return res
	}
	anyBusy := false
	allExplicitIdle := true
	for _, s := range slots {
		if s.NPromptTokens != nil && *s.NPromptTokens > res.MaxPromptTokens {
			res.MaxPromptTokens = *s.NPromptTokens
		}
		if s.NCtx != nil && *s.NCtx > res.MaxContext {
			res.MaxContext = *s.NCtx
		}
		if s.IDTask != nil && *s.IDTask > res.LatestTaskID {
			res.LatestTaskID = *s.IDTask
		}
		if s.IsProcessing == nil {
			allExplicitIdle = false
		} else if *s.IsProcessing {
			anyBusy = true
			allExplicitIdle = false
		}
	}
	if anyBusy {
		res.Signal = ActivitySignal{Kind: SignalBusy, ObservedAt: now}
	} else if allExplicitIdle {
		res.Signal = ActivitySignal{Kind: SignalIdle, ObservedAt: now}
	}
	return res
}

type WatchdogInput struct {
	IsRunning             bool
	EnableLLMModule       bool
	ActivitySignal        ActivitySignal
	AutoShutdownEnabled   bool
	IdleSeconds           int
	TimeoutMinutes        int
	WarnBeforeMinutes     int
	AlreadyWarned         bool
	Now                   time.Time
	MaxSignalAge          time.Duration
	AllowHardwareFallback bool
}

// EvaluateWatchdog deterministically decides whether to pause, warn, or shut down.
func EvaluateWatchdog(in WatchdogInput) WatchdogAction {
	if !in.IsRunning || !in.AutoShutdownEnabled {
		return WatchdogAction{Kind: WatchdogNone}
	}
	if !in.EnableLLMModule && !in.AllowHardwareFallback {
		return WatchdogAction{Kind: WatchdogPausedNoSignal}
	}
	maxAge := in.MaxSignalAge
	if maxAge <= 0 {
		maxAge = DefaultSignalMaxAge
	}
	if in.AllowHardwareFallback && !in.EnableLLMModule && maxAge < HardwareSignalMaxAge {
		maxAge = HardwareSignalMaxAge
	}

	switch in.ActivitySignal.Kind {
	case SignalUnknown:
		return WatchdogAction{Kind: WatchdogPausedNoSignal}
	case SignalBusy:
		if in.Now.Sub(in.ActivitySignal.ObservedAt) > maxAge {
			return WatchdogAction{Kind: WatchdogPausedNoSignal}
		}
		return WatchdogAction{Kind: WatchdogNone}
	case SignalIdle:
		if in.Now.Sub(in.ActivitySignal.ObservedAt) > maxAge {
			return WatchdogAction{Kind: WatchdogPausedNoSignal}
		}
	}

	limitMin := in.TimeoutMinutes
	if limitMin < 5 {
		limitMin = 5
	}
	limitSec := limitMin * 60
	warnMin := in.WarnBeforeMinutes
	if warnMin < 0 {
		warnMin = 0
	}
	warnSec := limitSec - warnMin*60
	if warnSec < 0 {
		warnSec = 0
	}

	if in.IdleSeconds >= limitSec {
		return WatchdogAction{Kind: WatchdogShutdown}
	}
	if warnMin > 0 && !in.AlreadyWarned && in.IdleSeconds >= warnSec {
		leftMin := (limitSec - in.IdleSeconds + 59) / 60
		if leftMin < 1 {
			leftMin = 1
		}
		return WatchdogAction{Kind: WatchdogWarn, RemainingMinutes: leftMin}
	}
	return WatchdogAction{Kind: WatchdogNone}
}
