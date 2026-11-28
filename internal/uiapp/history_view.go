package uiapp

import (
	"fmt"
	"math"
	"path/filepath"

	"github.com/LJMStark/Colab-Tools/internal/core"
	"github.com/egoist/mygo/ui"
)

func formatHistoryShortDur(seconds int) string {
	if seconds <= 0 {
		return "0m"
	}
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	return core.FormatDuration(seconds)
}

func (m *MainUI) renderHistoryPage(c *ui.Context, pal Palette) {
	days := m.HistoryDaysFilter
	if days != 0 && days != 7 && days != 30 {
		days = 7
	}
	limit := m.HistoryPageLimit
	if limit <= 0 {
		limit = 60
	}

	// 顶部 1×4 Bento KPI 汇总卡随时间/账号筛选实时联动，切类型分段时仍保留完整四维总览。
	kpiFilter := core.HistoryFilter{
		AccountID: m.HistoryAccountID,
		Days:      days,
	}
	_, sum := m.Svc.HistorySnapshot(kpiFilter)

	listFilter := core.HistoryFilter{
		Kind:      m.HistoryKindFilter,
		AccountID: m.HistoryAccountID,
		Days:      days,
	}
	records, _ := m.Svc.HistorySnapshot(listFilter)

	// 1. 顶部 1×4 Bento KPI 汇总卡
	ui.Grid(c).FillWidth().ColumnTracks(ui.Fr(1), ui.Fr(1), ui.Fr(1), ui.Fr(1)).Gap(14).Children(func() {
		// 卡 1：累计消耗 CU
		BentoCard(c, pal, 128, func() {
			TileHeader(c, pal, pal.Amber, "累计消耗 CU", "", nil)
			ui.Row(c).AlignItems(ui.End).Gap(5).Children(func() {
				ui.Text(c, core.FormatCU(sum.TotalConsumedCU)).FontSize(26).Bold().FontFeatures("tnum").TextColor(pal.TextPrimary).MaxLines(1)
				ui.Text(c, "CU").FontSize(13).FontWeight(500).TextColor(pal.TextMuted).Padding(0, 0, 3, 0)
			})
			ui.Spacer(c)
			foot := "尚无计费记录"
			if sum.MeasuredSessions > 0 || sum.EstimatedSessions > 0 {
				foot = fmt.Sprintf("实测 %d 次 · 估算 %d 次", sum.MeasuredSessions, sum.EstimatedSessions)
			}
			ui.Text(c, foot).FontSize(12).FontFeatures("tnum").TextColor(pal.TextMuted).MaxLines(1).Ellipsis("…")
		})

		// 卡 2：累计开机时长
		BentoCard(c, pal, 128, func() {
			TileHeader(c, pal, pal.Sky, "累计开机时长", "", nil)
			ui.Text(c, core.FormatDuration(sum.TotalDurationSec)).FontSize(26).Bold().FontFeatures("tnum").TextColor(pal.TextPrimary).MaxLines(1)
			ui.Spacer(c)
			foot := "尚无开机记录"
			if sum.SessionCount > 0 {
				if sum.TopHardware != "" {
					foot = fmt.Sprintf("%d 次会话 · 主力 %s", sum.SessionCount, sum.TopHardware)
				} else {
					foot = fmt.Sprintf("%d 次会话", sum.SessionCount)
				}
			}
			ui.Text(c, foot).FontSize(12).FontFeatures("tnum").TextColor(pal.TextMuted).MaxLines(1).Ellipsis("…")
		})

		// 卡 3：抢卡成功率
		BentoCard(c, pal, 128, func() {
			TileHeader(c, pal, pal.Violet, "抢卡成功率", "", nil)
			rateText := "—"
			if sum.AllocAttempts > 0 {
				pct := int(math.Round(float64(sum.AllocSuccesses) * 100.0 / float64(sum.AllocAttempts)))
				rateText = fmt.Sprintf("%d%%", pct)
			}
			ui.Text(c, rateText).FontSize(26).Bold().FontFeatures("tnum").TextColor(pal.TextPrimary).MaxLines(1)
			ui.Spacer(c)
			foot := "尚无抢卡记录"
			if sum.AllocAttempts > 0 {
				foot = fmt.Sprintf("%d/%d 次 · 均耗时 %s", sum.AllocSuccesses, sum.AllocAttempts, formatHistoryShortDur(sum.AvgAllocDurationSec))
			}
			ui.Text(c, foot).FontSize(12).FontFeatures("tnum").TextColor(pal.TextMuted).MaxLines(1).Ellipsis("…")
		})

		// 卡 4：看门狗止损（纯事实口径，严禁猜测精确省点）
		BentoCard(c, pal, 128, func() {
			TileHeader(c, pal, pal.Emerald, "看门狗止损", "", nil)
			ui.Textf(c, "拦截 %d 次", sum.WatchdogStops).FontSize(26).Bold().FontFeatures("tnum").TextColor(pal.Emerald).MaxLines(1)
			ui.Spacer(c)
			foot := "闲置自动关机守护"
			if sum.LastWatchdogDetail != "" {
				foot = sum.LastWatchdogDetail
			}
			ui.Text(c, foot).FontSize(12).FontFeatures("tnum").TextColor(pal.TextMuted).MaxLines(1).Ellipsis("…")
		})
	})

	// 2. 筛选工具条 + 统一时间线明细表（严禁嵌套 ui.Table / ui.Scroll）
	accounts := m.Svc.AccountsSnapshot()
	const (
		colDur    float32 = 108
		colMetric float32 = 126
		colStatus float32 = 122
	)

	PrecisionCard(c, pal, 16, 18, func() {
		// 筛选与导出工具条
		ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
			// 左：类型三态分段
			ui.Row(c).Padding(3).Radius(9).Background(pal.RecessedBg).Border(1, pal.RecessedBorder).InsetShadow(0, 1, 1, 0, pal.RecessedInnerShadow).Gap(2).Shrink(0).Children(func() {
				for _, opt := range []struct {
					kind  core.HistoryEntryKind
					label string
				}{
					{"", "全部"},
					{core.HistoryKindSession, "算力账单"},
					{core.HistoryKindAllocation, "抢卡记录"},
				} {
					k, lbl := opt.kind, opt.label
					ChipToggle(c, pal, lbl, m.HistoryKindFilter == k, false, func() {
						m.HistoryKindFilter = k
						m.HistoryPageLimit = 60
					})
				}
			})

			// 中：时间三态分段
			ui.Row(c).Padding(3).Radius(9).Background(pal.RecessedBg).Border(1, pal.RecessedBorder).InsetShadow(0, 1, 1, 0, pal.RecessedInnerShadow).Gap(2).Shrink(0).Children(func() {
				for _, opt := range []struct {
					days  int
					label string
				}{
					{7, "近 7 天"},
					{30, "近 30 天"},
					{0, "全部"},
				} {
					d, lbl := opt.days, opt.label
					ChipToggle(c, pal, lbl, days == d, false, func() {
						m.HistoryDaysFilter = d
						m.HistoryPageLimit = 60
					})
				}
			})

			// 多账号（>=2）时增加轻量账号切换按钮
			if len(accounts) >= 2 {
				accLabel := "全部账号"
				var accTint *ui.Color
				if m.HistoryAccountID != "" {
					found := false
					for _, a := range accounts {
						if a.ID == m.HistoryAccountID {
							accLabel = a.DisplayName
							t := AccountTagColor(pal, a.ColorTag)
							accTint = &t
							found = true
							break
						}
					}
					if !found {
						m.HistoryAccountID = ""
					}
				}
				SmallActionButton(c, pal, accLabel, accTint, false, func() {
					if m.HistoryAccountID == "" {
						m.HistoryAccountID = accounts[0].ID
					} else {
						nextID := ""
						for i, a := range accounts {
							if a.ID == m.HistoryAccountID && i+1 < len(accounts) {
								nextID = accounts[i+1].ID
								break
							}
						}
						m.HistoryAccountID = nextID
					}
					m.HistoryPageLimit = 60
				})
			}

			ui.Spacer(c)

			// 右：导出 CSV
			SmallActionButton(c, pal, "导出 CSV", nil, false, func() {
				f := listFilter
				go func() {
					outPath, err := m.Svc.ExportHistoryCSV(f)
					if err != nil {
						m.Svc.ShowToast("导出记录失败：" + err.Error())
						return
					}
					if outPath != "" {
						m.Svc.ShowToast("已导出 CSV：" + filepath.Base(outPath))
					} else {
						m.Svc.ShowToast("已导出历史记录 CSV")
					}
				}()
			})
		})

		ui.Box(c).FillWidth().Height(1).Background(pal.Divider)

		if len(records) == 0 {
			ui.Column(c).FillWidth().Padding(42, 16).AlignItems(ui.Center).Gap(6).Children(func() {
				ui.Text(c, "暂无记录").FontSize(15).FontWeight(600).TextColor(pal.TextPrimary)
				ui.Text(c, "完成一次抢卡或关机后会自动在这里记账").FontSize(12.5).TextColor(pal.TextMuted)
			})
			return
		}

		// 表头行
		ui.Row(c).FillWidth().Gap(10).Padding(0, 6).AlignItems(ui.Center).Children(func() {
			ui.Textf(c, "时间线明细 (%d)", len(records)).FontSize(12).FontWeight(500).TextColor(pal.TextMuted).Grow(1).MinWidth(240).MaxLines(1)
			ui.Text(c, "时长 / 轮次").FontSize(12).FontWeight(500).TextColor(pal.TextMuted).Width(colDur).MaxLines(1)
			ui.Text(c, "消耗 / 硬件").FontSize(12).FontWeight(500).TextColor(pal.TextMuted).Width(colMetric).TextAlign(ui.End).MaxLines(1)
			ui.Text(c, "结果与原因").FontSize(12).FontWeight(500).TextColor(pal.TextMuted).Width(colStatus).TextAlign(ui.End).MaxLines(1)
		})

		ui.Box(c).FillWidth().Height(1).Background(pal.RowDivider)

		shown := records
		if len(shown) > limit {
			shown = shown[:limit]
		}

		for idx, rec := range shown {
			if idx > 0 {
				ui.Box(c).FillWidth().Height(1).Background(pal.RowDivider)
			}
			m.renderHistoryRow(c, pal, rec, colDur, colMetric, colStatus)
		}

		if len(records) > limit {
			rem := len(records) - limit
			ui.Row(c).FillWidth().Padding(8, 0, 2, 0).Justify(ui.Center).Children(func() {
				SmallActionButton(c, pal, fmt.Sprintf("显示更多（还有 %d 条）", rem), nil, false, func() {
					m.HistoryPageLimit = limit + 60
				})
			})
		}
	}).Gap(10)
}

func (m *MainUI) renderHistoryRow(c *ui.Context, pal Palette, rec core.HistoryRecord, colDur, colMetric, colStatus float32) {
	dotCol := AccountTagColor(pal, rec.AccountColor)
	accName := rec.AccountName
	if accName == "" {
		accName = "默认账号"
	}

	ui.Row(c).FillWidth().Gap(10).Padding(8, 6).AlignItems(ui.Center).Children(func() {
		// 左侧主信息列
		ui.Row(c).Grow(1).MinWidth(240).Gap(10).AlignItems(ui.Center).Children(func() {
			DrawPrecisionLED(c, dotCol, 11)
			ui.Column(c).Grow(1).Gap(3).Children(func() {
				if rec.Kind == core.HistoryKindSession {
					hw := core.NormalizeHardwareKey(rec.Hardware)
					if hw == "" {
						hw = "云端 GPU"
					}
					task := rec.RecipeLabel
					if task == "" {
						task = rec.SessionName
					}
					headline := hw
					if task != "" {
						headline = fmt.Sprintf("%s · %s", hw, task)
					}

					ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
						skyTint := pal.Sky
						Badge(c, pal, "账单", &skyTint)
						ui.Text(c, accName).FontSize(13).FontWeight(600).TextColor(pal.TextPrimary).MaxLines(1)
						ui.Text(c, headline).FontSize(13).FontWeight(500).TextColor(pal.TextPrimary).MaxLines(1).Ellipsis("…")
						if rec.FallbackUsed {
							aTint := pal.Amber
							Badge(c, pal, "保底", &aTint)
						}
						if rec.ModelDowngraded {
							sTint := pal.Sky
							Badge(c, pal, "已切轻量", &sTint)
						}
					})

					timeRange := rec.StartedAt.Local().Format("01-02 15:04")
					if rec.EndedAt != nil && !rec.EndedAt.IsZero() {
						timeRange = fmt.Sprintf("%s → %s", rec.StartedAt.Local().Format("01-02 15:04"), rec.EndedAt.Local().Format("15:04"))
					}
					sub := timeRange
					if rec.Detail != "" {
						sub = fmt.Sprintf("%s · %s", timeRange, rec.Detail)
					}
					ui.Text(c, sub).FontSize(11.5).FontFeatures("tnum").TextColor(pal.TextMuted).MaxLines(1).Ellipsis("…")
				} else {
					targets := rec.TargetsSummary
					if targets == "" {
						targets = core.NormalizeHardwareKey(rec.Hardware)
					}
					if targets == "" {
						targets = "自动抢卡队列"
					}
					ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
						vTint := pal.Violet
						Badge(c, pal, "抢卡", &vTint)
						ui.Text(c, accName).FontSize(13).FontWeight(600).TextColor(pal.TextPrimary).MaxLines(1)
						ui.Text(c, targets).FontSize(13).FontWeight(500).TextColor(pal.TextPrimary).MaxLines(1).Ellipsis("…")
						if rec.FallbackUsed {
							aTint := pal.Amber
							Badge(c, pal, "保底", &aTint)
						}
					})

					sub := rec.StartedAt.Local().Format("01-02 15:04")
					if rec.Detail != "" {
						sub = fmt.Sprintf("%s · %s", sub, rec.Detail)
					}
					ui.Text(c, sub).FontSize(11.5).FontFeatures("tnum").TextColor(pal.TextMuted).MaxLines(1).Ellipsis("…")
				}
			})
		})

		// 中列：运行时长 或 抢卡轮次耗时
		if rec.Kind == core.HistoryKindSession {
			ui.Text(c, core.FormatDuration(rec.DurationSec)).FontSize(12.5).FontWeight(500).FontFeatures("tnum").TextColor(pal.TextPrimary).Width(colDur).MaxLines(1)
		} else {
			rounds := max(1, rec.Rounds)
			ui.Textf(c, "第 %d 轮 · %s", rounds, formatHistoryShortDur(rec.DurationSec)).FontSize(12.5).FontWeight(500).FontFeatures("tnum").TextColor(pal.TextPrimary).Width(colDur).MaxLines(1)
		}

		// 右列：消耗 CU + 口径小标签，或 抢到的硬件
		ui.Row(c).Width(colMetric).Justify(ui.End).AlignItems(ui.Center).Gap(5).Children(func() {
			if rec.Kind == core.HistoryKindSession {
				ui.Textf(c, "%s CU", core.FormatCU(rec.ConsumedCU)).FontSize(12.5).FontWeight(600).FontFeatures("tnum").TextColor(pal.TextPrimary).MaxLines(1)
				if basisLbl := rec.BillingBasis.DisplayName(); basisLbl != "" {
					var bTint *ui.Color
					if rec.BillingBasis == core.BillingMeasured {
						e := pal.Emerald
						bTint = &e
					}
					Badge(c, pal, basisLbl, bTint)
				}
			} else {
				hw := core.NormalizeHardwareKey(rec.Hardware)
				if hw == "" {
					ui.Text(c, "—").FontSize(12.5).TextColor(pal.TextFaint).MaxLines(1)
				} else {
					ui.Text(c, hw).FontSize(12.5).FontWeight(600).TextColor(pal.TextPrimary).MaxLines(1)
				}
			}
		})

		// 最右：语义徽章
		ui.Row(c).Width(colStatus).Justify(ui.End).AlignItems(ui.Center).Children(func() {
			if rec.Kind == core.HistoryKindSession {
				label := rec.ShutdownReason.DisplayName()
				if label == "" {
					label = "已结束"
				}
				var tint *ui.Color
				switch rec.ShutdownReason {
				case core.ShutdownAutoIdle, core.ShutdownBatchStop:
					t := pal.Emerald
					tint = &t
				case core.ShutdownCapReached:
					t := pal.Amber
					tint = &t
				case core.ShutdownExternalOrLost:
					t := pal.Coral
					tint = &t
				}
				Badge(c, pal, label, tint)
			} else {
				label := rec.AllocOutcome.DisplayName()
				if label == "" {
					label = "已记录"
				}
				var tint *ui.Color
				switch rec.AllocOutcome {
				case core.AllocTagSuccess:
					t := pal.Emerald
					tint = &t
				case core.AllocTagShortage:
					t := pal.Amber
					tint = &t
				case core.AllocTagRateLimited, core.AllocTagError:
					t := pal.Coral
					tint = &t
				}
				Badge(c, pal, label, tint)
			}
		})
	})
}
