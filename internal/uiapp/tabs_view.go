package uiapp

import (
	"fmt"
	"math"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/LJMStark/Colab-Tools/internal/app"
	"github.com/LJMStark/Colab-Tools/internal/core"
	"github.com/LJMStark/Colab-Tools/internal/integrations"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// MARK: - Tab 2: Compute & Polite Queue Workbench

func (m *MainUI) renderComputePage(c *ui.Context, pal Palette) {
	ui.Column(c).FillWidth().Gap(4).Children(func() {
		ui.Text(c, "算力与抢卡").FontSize(22).Bold().TextColor(pal.TextPrimary)
		ui.Text(c, "选好目标显卡顺位、保底降级与清晨错峰预约，让排队更省心且不触发官方风控。").FontSize(13).TextColor(pal.TextMuted)
	})

	acc, ok := m.Svc.SelectedAccount()
	if !ok {
		SectionCard(c, pal, "", "", func() {
			ui.Text(c, "请先绑定一个 Google Colab 账号后再配置算力策略。").FontSize(13.5).TextColor(pal.TextMuted)
		})
		return
	}

	// 1. Hardware Target 2-Column Grid + Priority Reordering
	SectionCard(c, pal, "目标显卡与缺货排队", acc.Tier.DisplayName(), func() {
		enableQueue := acc.EnablePoliteQueue
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				ui.Text(c, "目标显卡缺货时自动排队").FontSize(13.5).FontWeight(600).TextColor(pal.TextPrimary)
				ui.Text(c, "遇到官方暂无库存 (503) 自动按合规节奏重试，不必反复手动点击启动").FontSize(12).TextColor(pal.TextMuted)
			})
			if ui.Switch(c, &enableQueue).Clicked() || enableQueue != acc.EnablePoliteQueue {
				cp := acc
				cp.EnablePoliteQueue = enableQueue
				m.Svc.UpdateAccount(cp)
			}
		})

		targets := core.SanitizedQueueTargets(acc.QueueTargets, acc.Tier)
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Text(c, "点击卡片勾选目标显卡，用 ↑ ↓ 调整尝试顺位").FontSize(12).TextColor(pal.TextMuted)
			ui.Spacer(c)
			aTint := pal.Amber
			Badge(c, pal, "顺位 "+core.QueueTargetsSummary(targets), &aTint)
		})

		queueables := acc.Tier.QueueableAccelerators()
		ui.Grid(c).FillWidth().ColumnTracks(ui.Fr(1), ui.Fr(1)).Gap(10).Children(func() {
			for _, hw := range queueables {
				hwOpt := hw
				pos := slices.Index(targets, hwOpt)
				selected := pos >= 0
				bg := pal.SubtleBg
				border := pal.SubtleBorder
				if selected {
					bg = WithAlpha(pal.Amber, 0.13)
					border = WithAlpha(pal.Amber, 0.55)
				}
				ui.Row(c).FillWidth().Padding(11, 13).Radius(11).Background(bg).Border(1, border).AlignItems(ui.Center).Gap(10).OnClick(func() {
					cp := acc
					cur := core.SanitizedQueueTargets(cp.QueueTargets, cp.Tier)
					if idx := slices.Index(cur, hwOpt); idx >= 0 {
						if len(cur) > 1 {
							cur = append(cur[:idx], cur[idx+1:]...)
						}
					} else {
						cur = append(cur, hwOpt)
					}
					cp.QueueTargets = cur
					m.Svc.UpdateAccount(cp)
				}).Children(func() {
					if selected {
						ui.Row(c).Width(28).Height(26).Radius(7).Background(pal.Amber).Justify(ui.Center).AlignItems(ui.Center).Shrink(0).Children(func() {
							ui.Textf(c, "#%d", pos+1).FontSize(12).Bold().FontFeatures("tnum").TextColor(pal.PrimaryBtnFg)
						})
					} else {
						ui.Row(c).Width(28).Height(26).Radius(7).Background(pal.SubtleBg).Justify(ui.Center).AlignItems(ui.Center).Shrink(0).Children(func() {
							ui.Text(c, "+").FontSize(14).FontWeight(600).TextColor(pal.TextMuted)
						})
					}

					ui.Column(c).Grow(1).Gap(2).Children(func() {
						ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
							ui.Text(c, hwOpt.ShortChipLabel()).FontSize(13.5).FontWeight(600).TextColor(pal.TextPrimary)
							if role := hwOpt.RoleTag(); role != "" {
								Badge(c, pal, role, nil)
							}
						})
						ui.Text(c, hwOpt.SpecSubtitle()).FontSize(11.5).TextColor(pal.TextMuted)
					})

					if selected && len(targets) > 1 {
						ui.Row(c).Gap(4).Shrink(0).Children(func() {
							if pos > 0 {
								SmallActionButton(c, pal, "↑", nil, false, func() {
									cp := acc
									cur := slices.Clone(targets)
									cur[pos-1], cur[pos] = cur[pos], cur[pos-1]
									cp.QueueTargets = cur
									m.Svc.UpdateAccount(cp)
								})
							}
							if pos < len(targets)-1 {
								SmallActionButton(c, pal, "↓", nil, false, func() {
									cp := acc
									cur := slices.Clone(targets)
									cur[pos+1], cur[pos] = cur[pos], cur[pos+1]
									cp.QueueTargets = cur
									m.Svc.UpdateAccount(cp)
								})
							}
						})
					}
				})
			}
		})

		if acc.Tier.SupportsHighMem() {
			hm := acc.HighMem
			if ui.Checkbox(c, &hm, "申请时同时请求高内存形态 (High-RAM)").Clicked() || hm != acc.HighMem {
				cp := acc
				cp.HighMem = hm
				m.Svc.UpdateAccount(cp)
			}
		}
	})

	// 2. Two-Stage Auto-Fallback
	SectionCard(c, pal, "两阶段自动降级（先冲高配，再保底）", "", func() {
		fbOn := acc.QueueFallbackEnabled
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				ui.Text(c, "前几轮专注抢主力卡，仍缺货时自动加入保底卡").FontSize(13.5).FontWeight(600).TextColor(pal.TextPrimary)
				ui.Text(c, "既不错过稀缺旗舰卡，又避免苦等 40 分钟一无所获").FontSize(12).TextColor(pal.TextMuted)
			})
			if ui.Switch(c, &fbOn).Clicked() || fbOn != acc.QueueFallbackEnabled {
				cp := acc
				cp.QueueFallbackEnabled = fbOn
				m.Svc.UpdateAccount(cp)
			}
		})

		if acc.QueueFallbackEnabled {
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "前").FontSize(13).TextColor(pal.TextMuted)
				for _, r := range []int{1, 2, 3, 4} {
					rVal := r
					var tint *ui.Color
					if acc.QueueFallbackAfterRounds == rVal {
						t := pal.Amber
						tint = &t
					}
					SmallActionButton(c, pal, fmt.Sprintf("%d 轮", rVal), tint, false, func() {
						cp := acc
						cp.QueueFallbackAfterRounds = rVal
						m.Svc.UpdateAccount(cp)
					})
				}
				ui.Textf(c, "仅尝试主力卡，第 %d 轮起加入保底：", acc.QueueFallbackAfterRounds+1).FontSize(13).TextColor(pal.TextMuted)
				for _, cand := range core.EligibleFallbackTargets(acc.QueueTargets, acc.Tier) {
					cOpt := cand
					var tint *ui.Color
					if acc.QueueFallbackTarget == cOpt {
						t := pal.Emerald
						tint = &t
					}
					SmallActionButton(c, pal, cOpt.ShortChipLabel(), tint, false, func() {
						cp := acc
						cp.QueueFallbackTarget = cOpt
						m.Svc.UpdateAccount(cp)
					})
				}
			})
		}
	})

	// 3. Off-Peak Scheduled Start
	SectionCard(c, pal, "错峰定时预约（推荐清晨 06:00–08:00 低峰）", "", func() {
		schOn := acc.ScheduledStartEnabled
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				ui.Text(c, "按指定时间自动启动申请").FontSize(13.5).FontWeight(600).TextColor(pal.TextPrimary)
				ui.Text(c, "避开欧美工作高峰，到点自动唤醒并按上方策略拿卡（错过超过 10 分钟自动过期不误启）").FontSize(12).TextColor(pal.TextMuted)
			})
			if ui.Switch(c, &schOn).Clicked() || schOn != acc.ScheduledStartEnabled {
				cp := acc
				cp.ScheduledStartEnabled = schOn
				if schOn {
					nf := core.NextScheduledFireDate(cp.ScheduledStartHour, cp.ScheduledStartMinute, time.Now())
					cp.ScheduledNextFireAt = &nf
				} else {
					cp.ScheduledNextFireAt = nil
				}
				m.Svc.UpdateAccount(cp)
			}
		})

		if acc.ScheduledStartEnabled {
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "低峰快捷").FontSize(13).TextColor(pal.TextMuted)
				presets := [][2]int{{6, 30}, {7, 0}, {7, 30}}
				for _, hm := range presets {
					h, minVal := hm[0], hm[1]
					var tint *ui.Color
					if acc.ScheduledStartHour == h && acc.ScheduledStartMinute == minVal {
						t := pal.Emerald
						tint = &t
					}
					SmallActionButton(c, pal, fmt.Sprintf("%02d:%02d", h, minVal), tint, false, func() {
						cp := acc
						cp.ScheduledStartHour = h
						cp.ScheduledStartMinute = minVal
						nf := core.NextScheduledFireDate(h, minVal, time.Now())
						cp.ScheduledNextFireAt = &nf
						m.Svc.UpdateAccount(cp)
					})
				}

				ui.Box(c).Width(8)
				SmallActionButton(c, pal, "−15分", nil, false, func() {
					m.adjustScheduleMinutes(acc, -15)
				})
				ui.Textf(c, "%02d:%02d", acc.ScheduledStartHour, acc.ScheduledStartMinute).FontSize(14).FontWeight(600).FontFeatures("tnum").TextColor(pal.TextPrimary)
				SmallActionButton(c, pal, "+15分", nil, false, func() {
					m.adjustScheduleMinutes(acc, 15)
				})

				ui.Spacer(c)
				var onceTint, dailyTint *ui.Color
				if !acc.ScheduledStartRepeatDaily {
					t := pal.Amber
					onceTint = &t
				} else {
					t := pal.Emerald
					dailyTint = &t
				}
				SmallActionButton(c, pal, "仅一次", onceTint, false, func() {
					cp := acc
					cp.ScheduledStartRepeatDaily = false
					nf := core.NextScheduledFireDate(cp.ScheduledStartHour, cp.ScheduledStartMinute, time.Now())
					cp.ScheduledNextFireAt = &nf
					m.Svc.UpdateAccount(cp)
				})
				SmallActionButton(c, pal, "每天重复", dailyTint, false, func() {
					cp := acc
					cp.ScheduledStartRepeatDaily = true
					nf := core.NextScheduledFireDate(cp.ScheduledStartHour, cp.ScheduledStartMinute, time.Now())
					cp.ScheduledNextFireAt = &nf
					m.Svc.UpdateAccount(cp)
				})
			})
			if acc.ScheduledNextFireAt != nil {
				ui.Textf(c, "下次触发：%s", acc.ScheduledNextFireAt.Format("2006年01月02日 15:04")).FontSize(12).FontFeatures("tnum").TextColor(pal.Emerald)
			}
		}
	})

	// 4. Local 6-Slot Allocation Hit-Rate Heatmap (lock-safe snapshot)
	hwList := acc.Tier.QueueableAccelerators()
	selectedHW := m.SelectedInsightHW
	if selectedHW == "" && len(hwList) > 0 {
		selectedHW = hwList[0].ShortChipLabel()
	}
	totalSamples, slots, recSummary := m.Svc.InsightsSnapshot(selectedHW)
	SectionCard(c, pal, "本机时段命中率参考（纯本地统计 · 绝不上传）", fmt.Sprintf("共 %d 次记录", totalSamples), func() {
		ui.Row(c).Gap(6).Children(func() {
			for _, hw := range hwList {
				label := hw.ShortChipLabel()
				var tint *ui.Color
				if label == selectedHW {
					t := pal.Sky
					tint = &t
				}
				SmallActionButton(c, pal, label, tint, false, func() {
					m.SelectedInsightHW = label
				})
			}
		})

		ui.Grid(c).FillWidth().ColumnTracks(ui.Fr(1), ui.Fr(1), ui.Fr(1), ui.Fr(1), ui.Fr(1), ui.Fr(1)).Gap(10).Children(func() {
			for _, slot := range slots {
				s := slot
				ui.Column(c).FillWidth().AlignItems(ui.Center).Gap(6).Children(func() {
					rate, hasData := s.Rate()
					pctLabel := "—"
					if hasData {
						pctLabel = fmt.Sprintf("%d%%", int(math.Round(rate*100)))
					}
					ui.Text(c, pctLabel).FontSize(12.5).FontWeight(600).FontFeatures("tnum").TextColor(pal.TextPrimary)
					ui.Box(c).FillWidth().Height(48).Draw(func(p *ui.Painter, r ui.Rect) {
						p.Fill(r, pal.SubtleBg, 6)
						if hasData && rate > 0 {
							h := max(float32(8), r.H*float32(rate))
							col := pal.Amber
							if rate >= 0.5 {
								col = pal.Emerald
							}
							p.Fill(ui.Rect{X: r.X, Y: r.Y + r.H - h, W: r.W, H: h}, col, 6)
						}
					})
					ui.Text(c, s.Label).FontSize(11.5).FontFeatures("tnum").TextColor(pal.TextMuted)
				})
			}
		})

		if recSummary != "" {
			ui.Text(c, "✦ "+recSummary).FontSize(12.5).TextColor(pal.Sky)
		} else {
			ui.Text(c, "累积满 3 次本地申请记录后，将自动标出你的黄金拿卡时段。").FontSize(12).TextColor(pal.TextMuted)
		}
	})

	ui.Text(c, "🛡 防风控护栏始终开启：低频随机间隔重试（上限 5 轮 / 40 分钟），遇到限流 (429) 或超额 (412) 立即自动熔断并冷却 15 分钟。").
		FontSize(12).TextColor(pal.TextMuted)
}

func (m *MainUI) adjustScheduleMinutes(acc core.AccountProfile, delta int) {
	total := (acc.ScheduledStartHour*60 + acc.ScheduledStartMinute + delta) % (24 * 60)
	if total < 0 {
		total += 24 * 60
	}
	cp := acc
	cp.ScheduledStartHour = total / 60
	cp.ScheduledStartMinute = total % 60
	nf := core.NextScheduledFireDate(cp.ScheduledStartHour, cp.ScheduledStartMinute, time.Now())
	cp.ScheduledNextFireAt = &nf
	m.Svc.UpdateAccount(cp)
}

// MARK: - Tab 3: Multi-Client Integrations Hub

func (m *MainUI) renderIntegrationsPage(c *ui.Context, pal Palette) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Gap(4).Children(func() {
			ui.Text(c, "接入工具").FontSize(22).Bold().TextColor(pal.TextPrimary)
			ui.Text(c, "自动检测本机 AI 编程客户端，支持无感安全写入或双协议一键复制。").FontSize(13).TextColor(pal.TextMuted)
		})
		SmallActionButton(c, pal, "↻ 重新检测", nil, false, func() {
			m.Svc.RefreshClientDetection()
			m.Svc.ShowToast("已完成本机客户端探测")
		})
	})

	acc, ok := m.Svc.SelectedAccount()
	if !ok {
		SectionCard(c, pal, "", "", func() {
			ui.Text(c, "请先绑定 Google Colab 账号。").FontSize(13.5).TextColor(pal.TextMuted)
		})
		return
	}
	rt := m.Svc.StateFor(acc.ID)
	det := m.Svc.ClientDetection
	ids := integrations.IdentifiersForAccount(acc.ID)

	SectionCard(c, pal, "原生安全同步（按账号独立隔离 · 写前 0600 备份 · 绝不改官方默认）", "", func() {
		// CC Switch
		ccOn := acc.AutoSyncCCSwitch
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
			ui.Column(c).Grow(1).Gap(3).Children(func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "CC Switch").FontSize(14).FontWeight(600).TextColor(pal.TextPrimary)
					if det.CCSwitchInstalled {
						e := pal.Emerald
						Badge(c, pal, "已检测到", &e)
					} else {
						Badge(c, pal, "未检测到", nil)
					}
					if rt.CCSwitchSynced {
						s := pal.Sky
						Badge(c, pal, "已同步", &s)
					}
				})
				ui.Text(c, "写入独立待机渠道（WAL 安全事务 + 备份），不改动当前激活的官方订阅").FontSize(12).TextColor(pal.TextMuted)
			})
			SmallActionButton(c, pal, "立即同步", nil, rt.PublicURL == "", func() {
				cp := acc
				cp.AutoSyncCCSwitch = true
				m.Svc.UpdateAccount(cp)
				go m.Svc.SyncAllEnabledClients(acc.ID, false)
			})
			if ui.Switch(c, &ccOn).Clicked() || ccOn != acc.AutoSyncCCSwitch {
				cp := acc
				cp.AutoSyncCCSwitch = ccOn
				m.Svc.UpdateAccount(cp)
			}
		})

		if acc.AutoSyncCCSwitch {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
				ui.Text(c, "渠道显示名称").FontSize(13).TextColor(pal.TextMuted)
				ui.Spacer(c)
				ui.TextInput(c, &m.CCSwitchNameDraft).Width(280).Placeholder("Colab Qwen3.8-27B")
				SmallActionButton(c, pal, "保存", nil, false, func() {
					if strings.TrimSpace(m.CCSwitchNameDraft) != "" {
						cp := acc
						cp.CCSwitchName = strings.TrimSpace(m.CCSwitchNameDraft)
						m.Svc.UpdateAccount(cp)
						m.Svc.ShowToast("已保存渠道名称")
					}
				})
			})
		}

		ui.Box(c).FillWidth().Height(1).Background(pal.Divider)

		// OpenCode
		ocOn := acc.AutoSyncOpenCode
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
			ui.Column(c).Grow(1).Gap(3).Children(func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "OpenCode").FontSize(14).FontWeight(600).TextColor(pal.TextPrimary)
					if det.OpenCodeInstalled {
						e := pal.Emerald
						Badge(c, pal, "已检测到", &e)
					} else {
						Badge(c, pal, "未检测到", nil)
					}
					if rt.OpenCodeSynced {
						s := pal.Sky
						Badge(c, pal, "已同步", &s)
					}
				})
				ui.Textf(c, "按账号独立合并至 opencode.json（不存明文 Key，引用环境变量 %s）", ids.EnvKey).FontSize(12).TextColor(pal.TextMuted).MaxLines(1).Ellipsis("…")
			})
			SmallActionButton(c, pal, "立即同步", nil, rt.PublicURL == "", func() {
				cp := acc
				cp.AutoSyncOpenCode = true
				m.Svc.UpdateAccount(cp)
				go m.Svc.SyncAllEnabledClients(acc.ID, false)
			})
			if ui.Switch(c, &ocOn).Clicked() || ocOn != acc.AutoSyncOpenCode {
				cp := acc
				cp.AutoSyncOpenCode = ocOn
				m.Svc.UpdateAccount(cp)
			}
		})

		ui.Box(c).FillWidth().Height(1).Background(pal.Divider)

		// Codex CLI
		cdxOn := acc.AutoSyncCodex
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
			ui.Column(c).Grow(1).Gap(3).Children(func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "Codex CLI").FontSize(14).FontWeight(600).TextColor(pal.TextPrimary)
					if det.CodexInstalled {
						e := pal.Emerald
						Badge(c, pal, "已检测到", &e)
					} else {
						Badge(c, pal, "未检测到", nil)
					}
					if rt.CodexSynced {
						s := pal.Sky
						Badge(c, pal, "已同步", &s)
					}
				})
				ui.Text(c, "按账号独立托管区块写入 ~/.codex/config.toml（仅 env_key，不改动官方默认渠道）").FontSize(12).TextColor(pal.TextMuted)
			})
			SmallActionButton(c, pal, "立即同步", nil, rt.PublicURL == "", func() {
				cp := acc
				cp.AutoSyncCodex = true
				m.Svc.UpdateAccount(cp)
				go m.Svc.SyncAllEnabledClients(acc.ID, false)
			})
			if ui.Switch(c, &cdxOn).Clicked() || cdxOn != acc.AutoSyncCodex {
				cp := acc
				cp.AutoSyncCodex = cdxOn
				m.Svc.UpdateAccount(cp)
			}
		})

		ui.Box(c).FillWidth().Height(1).Background(pal.Divider)

		// Claude Code
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
			ui.Column(c).Grow(1).Gap(3).Children(func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "Claude Code").FontSize(14).FontWeight(600).TextColor(pal.TextPrimary)
					if det.ClaudeCodeInstalled {
						e := pal.Emerald
						Badge(c, pal, "已检测到", &e)
					} else {
						Badge(c, pal, "未检测到", nil)
					}
				})
				ui.Text(c, "通过一次性自清理脚本安全注入环境变量拉起终端，API Key 不留命令历史").FontSize(12).TextColor(pal.TextMuted)
			})
			SmallActionButton(c, pal, "安全拉起终端", nil, rt.PublicURL == "", func() {
				m.Svc.LaunchClaudeTerminal(acc.ID)
			})
		})
	})

	// Universal Dual-Protocol Copy Hub
	SectionCard(c, pal, "通用接入（一键复制给其他客户端）", "", func() {
		ui.Text(c, "适用于 Cherry Studio · Cline · Roo Code · Cursor · Claude Code Router · ccm 等任意支持标准 API 的工具").
			FontSize(12).TextColor(pal.TextMuted)

		online := rt.PublicURL != ""
		displayURL := rt.PublicURL
		if displayURL == "" {
			displayURL = "https://<your-colab-endpoint>.trycloudflare.com"
		}
		safeEnv := integrations.ExportEnvSnippet(m.SelectedCopyProto, displayURL, rt.ModelAlias, "", ids.EnvKey, false)

		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
			for _, proto := range []integrations.CopyProtocolKind{integrations.ProtocolOpenAI, integrations.ProtocolAnthropic} {
				pKind := proto
				var tint *ui.Color
				if m.SelectedCopyProto == pKind {
					t := pal.Sky
					tint = &t
				}
				SmallActionButton(c, pal, string(pKind), tint, false, func() {
					m.SelectedCopyProto = pKind
				})
			}
			ui.Spacer(c)
			if online {
				e := pal.Emerald
				Badge(c, pal, "实时端点已就绪", &e)
			} else {
				Badge(c, pal, "离线占位模板", nil)
			}
		})

		ui.Column(c).FillWidth().Padding(12).Radius(10).Background(pal.SubtleBg).Border(1, pal.SubtleBorder).Children(func() {
			ui.Text(c, safeEnv).FontSize(12).Font("monospace").TextColor(pal.TextPrimary)
		})

		ui.Row(c).FillWidth().Gap(8).Children(func() {
			SmallActionButton(c, pal, "复制安全模板", nil, false, func() {
				m.Svc.CopyToClipboard(safeEnv, "环境变量安全模板")
			})
			SmallActionButton(c, pal, "复制 JSON", nil, false, func() {
				js := integrations.ExportJSONSnippet(m.SelectedCopyProto, displayURL, rt.ModelAlias, "", ids.EnvKey, false)
				m.Svc.CopyToClipboard(js, "JSON 安全模板")
			})
			SmallActionButton(c, pal, "仅复制地址", nil, !online, func() {
				u := rt.PublicURL
				if m.SelectedCopyProto == integrations.ProtocolOpenAI {
					u = integrations.OpenAIBaseURL(u)
				}
				m.Svc.CopyToClipboard(u, "服务地址")
			})
			ui.Spacer(c)
			SmallActionButton(c, pal, "复制含真实密钥", &pal.Amber, !online, func() {
				key, _ := m.Svc.Vault.LoadLLMAPIKey(acc.ID)
				if key == "" {
					m.Svc.ShowToast("当前账号尚未拉取到真实 API Key")
					return
				}
				raw := integrations.ExportEnvSnippet(m.SelectedCopyProto, rt.PublicURL, rt.ModelAlias, key, ids.EnvKey, true)
				m.Svc.CopyToClipboard(raw, "含真实密钥配置（请注意妥善保管）")
			})
		})
	})
}

// MARK: - Tab 4: Multi-Account Manager & Official OAuth2 Binding

func (m *MainUI) renderAccountsPage(c *ui.Context, pal Palette) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Gap(4).Children(func() {
			ui.Text(c, "多账号管理").FontSize(22).Bold().TextColor(pal.TextPrimary)
			ui.Text(c, "每个账号独立加密存放于系统凭据库（macOS 钥匙串 / Windows 凭据管理器），会话与点数完全隔离。").FontSize(13).TextColor(pal.TextMuted)
		})
		ui.Row(c).Gap(8).Children(func() {
			SmallActionButton(c, pal, "导入终端默认账号", nil, !m.Svc.CLIInstalled || m.Svc.IsImportingDefault, func() {
				go func() {
					if !m.Svc.TryImportSystemDefaultAccount() {
						m.Svc.ShowToast("未检测到终端默认 colab 登录，请点击右侧走官方授权")
					}
				}()
			})
			PillButton(c, pal, "+ 绑定新账号", true, !m.Svc.CLIInstalled, func() {
				m.ShowAddAccPanel = !m.ShowAddAccPanel
			})
		})
	})

	if m.ShowAddAccPanel {
		SectionCard(c, pal, "通过官方 OAuth2 绑定新账号", "", func() {
			m.renderEmbeddedOAuthBinder(c, pal, func() {
				m.ShowAddAccPanel = false
			})
		})
	}

	accounts := m.Svc.AccountsSnapshot()
	selAcc, _ := m.Svc.SelectedAccount()
	SectionCard(c, pal, fmt.Sprintf("已绑定账号 (%d)", len(accounts)), "", func() {
		if len(accounts) == 0 {
			ui.Text(c, "暂无绑定账号，点击上方「+ 绑定新账号」或「导入终端默认账号」开始。").FontSize(13).TextColor(pal.TextMuted)
			return
		}
		for idx, acc := range accounts {
			if idx > 0 {
				ui.Box(c).FillWidth().Height(1).Background(pal.Divider)
			}
			accCopy := acc
			rt := m.Svc.StateFor(accCopy.ID)
			isSel := accCopy.ID == selAcc.ID
			ui.Row(c).FillWidth().Gap(10).Padding(6, 0).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Width(10).Height(10).Radius(5).Background(AccountTagColor(pal, accCopy.ColorTag))
				ui.Column(c).Grow(1).Gap(2).Children(func() {
					ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
						ui.Text(c, accCopy.DisplayName).FontSize(14).FontWeight(600).TextColor(pal.TextPrimary)
						Badge(c, pal, accCopy.Tier.DisplayName(), nil)
						if isSel {
							s := pal.Sky
							Badge(c, pal, "当前焦点", &s)
						}
					})
					email := accCopy.Email
					if email == "" {
						email = "默认官方 CLI 账号"
					}
					ui.Textf(c, "%s · 会话 %s · 余额 %s CU", email, accCopy.SessionName, core.FormatCU(rt.CUBalance)).FontSize(12).TextColor(pal.TextMuted)
				})
				if !isSel {
					SmallActionButton(c, pal, "设为焦点", nil, false, func() {
						m.Svc.SelectAccount(accCopy.ID)
					})
				}
				SmallActionButton(c, pal, "切换订阅", nil, false, func() {
					cp := accCopy
					curIdx := slices.Index(core.AllSubscriptionTiers, cp.Tier)
					nextTier := core.AllSubscriptionTiers[(curIdx+1)%len(core.AllSubscriptionTiers)]
					cp.Tier = nextTier
					cp.PreferredAccelerator = nextTier.DefaultAccelerator()
					cp.Sanitize()
					m.Svc.UpdateAccount(cp)
					m.Svc.ShowToast("已切换订阅档位为：" + nextTier.DisplayName())
				})
				SmallActionButton(c, pal, "移除", &pal.Coral, false, func() {
					m.Svc.RemoveAccount(accCopy.ID)
				})
			})
		}
	})
}

func (m *MainUI) renderEmbeddedOAuthBinder(c *ui.Context, pal Palette, onSuccess func()) {
	ui.Column(c).FillWidth().Gap(12).Children(func() {
		ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "账号备注名").FontSize(13).TextColor(pal.TextMuted).Width(90)
			ui.TextInput(c, &m.NewAccName).Placeholder("例如：Work / AI Ultra（可留空自动用邮箱前缀）").Grow(1)
		})

		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "订阅档位").FontSize(13).TextColor(pal.TextMuted).Width(90)
			for idx, tier := range core.AllSubscriptionTiers {
				tIdx := idx
				var tint *ui.Color
				if m.NewAccTierIdx == tIdx {
					t := pal.Amber
					tint = &t
				}
				SmallActionButton(c, pal, tier.ShortBadge(), tint, false, func() {
					m.NewAccTierIdx = tIdx
				})
			}
		})

		ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
			PillButton(c, pal, "1. 打开浏览器完成 Google 官方授权", true, m.NewAccWorking || !m.Svc.CLIInstalled, func() {
				m.NewAccWorking = true
				m.NewAccError = ""
				go func() {
					u, err := m.Svc.BeginOAuthFlow()
					m.NewAccWorking = false
					if err != nil {
						m.NewAccError = err.Error()
					} else {
						m.NewAccAuthURL = u
						m.Svc.ShowToast("已打开浏览器，授权后请复制授权码粘贴到下方")
					}
					if m.Svc.OnUIInvalidate != nil {
						m.Svc.OnUIInvalidate()
					}
				}()
			})
			if m.NewAccAuthURL != "" {
				SmallActionButton(c, pal, "复制授权链接", nil, false, func() {
					m.Svc.CopyToClipboard(m.NewAccAuthURL, "Google OAuth2 链接")
				})
			}
		})

		ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
			ui.TextInput(c, &m.NewAccAuthCode).Placeholder("2. 粘贴浏览器返回的 Google 授权码").Grow(1)
			PillButton(c, pal, "完成绑定", true, m.NewAccWorking || strings.TrimSpace(m.NewAccAuthCode) == "", func() {
				m.NewAccWorking = true
				m.NewAccError = ""
				tier := core.AllSubscriptionTiers[m.NewAccTierIdx%len(core.AllSubscriptionTiers)]
				name := m.NewAccName
				code := m.NewAccAuthCode
				go func() {
					err := m.Svc.CompleteOAuthFlow(name, tier, code)
					m.NewAccWorking = false
					if err != nil {
						m.NewAccError = err.Error()
					} else {
						m.NewAccName = ""
						m.NewAccAuthCode = ""
						m.NewAccAuthURL = ""
						if onSuccess != nil {
							onSuccess()
						}
					}
					if m.Svc.OnUIInvalidate != nil {
						m.Svc.OnUIInvalidate()
					}
				}()
			})
		})

		if m.NewAccError != "" {
			ui.Text(c, "⚠ "+m.NewAccError).FontSize(12).TextColor(pal.Coral)
		}
	})
}

// MARK: - Tab 5: Unified Settings (General / Doctor / Advanced / About)

func (m *MainUI) renderSettingsPage(c *ui.Context, pal Palette) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Gap(4).Children(func() {
			ui.Text(c, "设置").FontSize(22).Bold().TextColor(pal.TextPrimary)
			ui.Text(c, "所有偏好修改即时生效，无需手动保存。").FontSize(13).TextColor(pal.TextMuted)
		})
		ui.Row(c).Gap(6).Children(func() {
			tabs := []string{"通用与关机", "环境体检", "高级参数", "关于"}
			for idx, title := range tabs {
				st := SettingsSubTab(idx)
				var tint *ui.Color
				if m.SettingsSubTab == st {
					t := pal.Amber
					tint = &t
				}
				SmallActionButton(c, pal, title, tint, false, func() {
					m.SettingsSubTab = st
				})
			}
		})
	})

	switch m.SettingsSubTab {
	case SubTabGeneral:
		m.renderSettingsGeneral(c, pal)
	case SubTabDoctor:
		m.renderSettingsDoctor(c, pal)
	case SubTabAdvanced:
		m.renderSettingsAdvanced(c, pal)
	case SubTabAbout:
		m.renderSettingsAbout(c, pal)
	}
}

func (m *MainUI) renderSettingsGeneral(c *ui.Context, pal Palette) {
	SectionCard(c, pal, "应用与托盘", "", func() {
		openAtLogin := false
		if mygo.App.IsReady() {
			openAtLogin = mygo.App.OpenAtLogin()
		}
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				ui.Text(c, "开机自动启动").FontSize(13.5).FontWeight(600).TextColor(pal.TextPrimary)
				ui.Text(c, "登录系统后自动常驻顶部菜单栏 / 系统托盘（支持 macOS 与 Windows）").FontSize(12).TextColor(pal.TextMuted)
			})
			if ui.Switch(c, &openAtLogin).Clicked() {
				if mygo.App.IsReady() {
					if err := mygo.App.SetOpenAtLogin(openAtLogin); err != nil {
						m.Svc.ShowToast("设置开机自启失败：" + err.Error())
					} else {
						m.Svc.ShowToast("已更新开机自启设置")
					}
				}
			}
		})

		ui.Box(c).FillWidth().Height(1).Background(pal.Divider)

		// Appearance theme selector
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				ui.Text(c, "界面外观主题").FontSize(13.5).FontWeight(600).TextColor(pal.TextPrimary)
				ui.Text(c, "默认跟随系统深浅色自动切换").FontSize(12).TextColor(pal.TextMuted)
			})
			ui.Row(c).Gap(6).Children(func() {
				opts := [][2]string{{"system", "跟随系统"}, {"dark", "深色炭灰"}, {"light", "浅色宣纸"}}
				for _, o := range opts {
					k, lbl := o[0], o[1]
					var tint *ui.Color
					if m.Svc.Settings.ThemePreference == k {
						t := pal.Amber
						tint = &t
					}
					SmallActionButton(c, pal, lbl, tint, false, func() {
						m.Svc.UpdateSettings(func(s *core.AppSettings) { s.ThemePreference = k })
					})
				}
			})
		})

		ui.Box(c).FillWidth().Height(1).Background(pal.Divider)

		// Menu bar / tray status text format (macOS)
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				ui.Text(c, "顶部菜单栏状态显示").FontSize(13.5).FontWeight(600).TextColor(pal.TextPrimary)
				ui.Text(c, "控制顶部菜单栏图标旁是否展示运行卡数与点数（macOS 生效）").FontSize(12).TextColor(pal.TextMuted)
			})
			ui.Row(c).Gap(6).Children(func() {
				opts := []struct {
					mode core.StatusBarDisplayMode
					lbl  string
				}{
					{core.StatusBarIconOnly, "仅图标"},
					{core.StatusBarCompact, "简洁"},
					{core.StatusBarDetailed, "详细"},
				}
				cur := m.Svc.Settings.StatusBarMode
				if cur == "" {
					cur = core.StatusBarCompact
				}
				for _, o := range opts {
					k, lbl := o.mode, o.lbl
					var tint *ui.Color
					if cur == k {
						t := pal.Amber
						tint = &t
					}
					SmallActionButton(c, pal, lbl, tint, false, func() {
						m.Svc.UpdateSettings(func(s *core.AppSettings) { s.StatusBarMode = k })
					})
				}
			})
		})

		ui.Box(c).FillWidth().Height(1).Background(pal.Divider)

		notif := m.Svc.Settings.EnableNotifications
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				ui.Text(c, "桌面通知提醒").FontSize(13.5).FontWeight(600).TextColor(pal.TextPrimary)
				ui.Text(c, "排到卡、预约触发、空闲预警与自动关机时推送通知（计费止损警告始终保留）").FontSize(12).TextColor(pal.TextMuted)
			})
			if ui.Switch(c, &notif).Clicked() || notif != m.Svc.Settings.EnableNotifications {
				m.Svc.UpdateSettings(func(s *core.AppSettings) { s.EnableNotifications = notif })
			}
		})
	})

	if acc, ok := m.Svc.SelectedAccount(); ok {
		SectionCard(c, pal, "空闲自动关机（防止忘记关机浪费点数）", acc.DisplayName, func() {
			autoShut := acc.AutoShutdownEnabled
			ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
				ui.Column(c).Grow(1).Gap(2).Children(func() {
					ui.Text(c, "开启空闲自动关机").FontSize(13.5).FontWeight(600).TextColor(pal.TextPrimary)
					ui.Text(c, "确认长时间无任务运行后自动释放实例停止计费").FontSize(12).TextColor(pal.TextMuted)
				})
				if ui.Switch(c, &autoShut).Clicked() || autoShut != acc.AutoShutdownEnabled {
					cp := acc
					cp.AutoShutdownEnabled = autoShut
					m.Svc.UpdateAccount(cp)
				}
			})

			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Text(c, "连续空闲多久关机").FontSize(13).TextColor(pal.TextMuted)
				ui.Spacer(c)
				for _, mins := range []int{15, 30, 40, 60} {
					mv := mins
					var tint *ui.Color
					if acc.IdleTimeoutMinutes == mv {
						t := pal.Amber
						tint = &t
					}
					SmallActionButton(c, pal, fmt.Sprintf("%d 分钟", mv), tint, false, func() {
						cp := acc
						cp.IdleTimeoutMinutes = mv
						m.Svc.UpdateAccount(cp)
					})
				}
			})
		})
	}
}

func (m *MainUI) renderSettingsDoctor(c *ui.Context, pal Palette) {
	env := m.Svc.EnvReport
	SectionCard(c, pal, "官方 CLI 环境体检与修复", strings.ToUpper(runtime.GOOS), func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
			ui.Column(c).Grow(1).Gap(3).Children(func() {
				ui.Text(c, "google-colab-cli 状态").FontSize(14).FontWeight(600).TextColor(pal.TextPrimary)
				detail := "未检测到可执行文件"
				if m.Svc.CLIInstalled {
					ver := ""
					if env.ColabVersion != "" {
						ver = " v" + env.ColabVersion
					}
					bridge := ""
					if env.UsingWSLBridge {
						bridge = "（WSL2 桥接）"
					}
					detail = fmt.Sprintf("已安装%s%s · %s", ver, bridge, env.ColabPath)
				} else if env.ColabBrokenReason != "" {
					detail = env.ColabBrokenReason
				}
				ui.Text(c, detail).FontSize(12).TextColor(pal.TextMuted)
			})
			if m.Svc.CLIInstalled {
				e := pal.Emerald
				Badge(c, pal, "已就绪", &e)
			} else {
				a := pal.Amber
				Badge(c, pal, "需安装", &a)
			}
		})

		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Text(c, "包管理 / 运行环境："+env.PackageManagerSummary()).FontSize(12.5).TextColor(pal.TextMuted)
			ui.Spacer(c)
			SmallActionButton(c, pal, "↻ 重新体检", nil, m.Svc.IsCheckingEnv, func() {
				go m.Svc.RefreshEnvironmentCheck()
			})
			ui.Box(c).Width(8)
			PillButton(c, pal, "重新打开 3 步开箱向导", true, false, func() {
				m.Svc.ReopenOnboarding()
			})
		})
	})
}

func (m *MainUI) renderSettingsAdvanced(c *ui.Context, pal Palette) {
	acc, ok := m.Svc.SelectedAccount()
	if !ok {
		return
	}
	SectionCard(c, pal, "负载形态与端口参数", acc.DisplayName, func() {
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "工作负载类型").FontSize(13).TextColor(pal.TextMuted).Width(100)
			for _, kind := range core.AllWorkloadKinds {
				k := kind
				var tint *ui.Color
				if acc.WorkloadKind == k {
					t := WorkloadColor(pal, k)
					tint = &t
				}
				SmallActionButton(c, pal, k.ShortBadge(), tint, false, func() {
					cp := acc
					cp.WorkloadKind = k
					cp.EnableLLMModule = (k == core.WorkloadLLM)
					if k == core.WorkloadLLM {
						cp.DeployRecipe = core.RecipeLLMProxy
					} else if k != core.WorkloadVideo {
						cp.DeployRecipe = core.RecipeNone
					}
					cp.RemoteServicePort = k.DefaultRemotePort()
					cp.LocalSSHTunnelPort = k.DefaultRemotePort()
					m.RemotePortDraft = fmt.Sprintf("%d", cp.RemoteServicePort)
					m.LocalPortDraft = fmt.Sprintf("%d", cp.LocalSSHTunnelPort)
					m.Svc.UpdateAccount(cp)
				})
			}
		})

		hwFallback := acc.EnableHardwareIdle
		if ui.Checkbox(c, &hwFallback, "非 LLM 场景开启 GPU(<5%) + CPU(<25%) 双低载兜底关机（下载/转码防误杀）").Clicked() || hwFallback != acc.EnableHardwareIdle {
			cp := acc
			cp.EnableHardwareIdle = hwFallback
			m.Svc.UpdateAccount(cp)
		}

		ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Drive 脚本路径").FontSize(13).TextColor(pal.TextMuted).Width(100)
			ui.TextInput(c, &m.DriveScriptDraft).Grow(1)
			SmallActionButton(c, pal, "保存", nil, false, func() {
				cp := acc
				cp.DriveScriptPath = strings.TrimSpace(m.DriveScriptDraft)
				if cp.DeployRecipe.IsVideoComfy() && cp.DriveScriptPath != "" {
					// Explicit custom Drive script takes precedence over built-in video recipes
					cp.DeployRecipe = core.RecipeNone
				}
				m.Svc.UpdateAccount(cp)
				m.Svc.ShowToast("已保存远端脚本路径（启动将使用该自定义脚本）")
			})
		})

		ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "远端端口").FontSize(13).TextColor(pal.TextMuted).Width(100)
			ui.TextInput(c, &m.RemotePortDraft).Width(100)
			ui.Text(c, "本地隧道端口").FontSize(13).TextColor(pal.TextMuted)
			ui.TextInput(c, &m.LocalPortDraft).Width(100)
			SmallActionButton(c, pal, "校验并保存端口", nil, false, func() {
				rp, err1 := strconv.Atoi(strings.TrimSpace(m.RemotePortDraft))
				lp, err2 := strconv.Atoi(strings.TrimSpace(m.LocalPortDraft))
				if err1 != nil || err2 != nil || rp < 1024 || rp > 65535 || lp < 1024 || lp > 65535 {
					m.PortValidationErr = "端口必须是 1024–65535 之间的整数"
					return
				}
				m.PortValidationErr = ""
				cp := acc
				cp.RemoteServicePort = rp
				cp.LocalSSHTunnelPort = lp
				m.Svc.UpdateAccount(cp)
				m.Svc.ShowToast(fmt.Sprintf("端口已保存（远端 :%d / 本地 :%d）", rp, lp))
			})
		})
		if m.PortValidationErr != "" {
			ui.Text(c, "⚠ "+m.PortValidationErr).FontSize(12).TextColor(pal.Coral)
		}
	})
}

func (m *MainUI) renderSettingsAbout(c *ui.Context, pal Palette) {
	SectionCard(c, pal, "", "", func() {
		ui.Row(c).FillWidth().Gap(16).AlignItems(ui.Center).Children(func() {
			if appIconBitmap != nil {
				ui.Image(c, appIconBitmap).Width(56).Height(56).Radius(14).Clip()
			}
			ui.Column(c).Grow(1).Gap(4).Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "Colab Tools").FontSize(18).Bold().TextColor(pal.TextPrimary)
					Badge(c, pal, "v"+app.AppVersion, &pal.Amber)
					Badge(c, pal, "Win + Mac · Pure Go", &pal.Sky)
				})
				ui.Text(c, "原生跨平台 Google Colab CLI 算力控制台与托盘伴侣（基于 egoist/mygo 零 CGO 驱动）").FontSize(13).TextColor(pal.TextMuted)
				ui.Text(c, "100% 调用官方 google-colab-cli，遵守官方服务条款，不私自逆向或绕过任何配额。").FontSize(12).TextColor(pal.TextFaint)
			})
		})
		ui.Row(c).Gap(10).Children(func() {
			SmallActionButton(c, pal, "GitHub 仓库", nil, false, func() {
				if m.Svc.URLOpener != nil {
					m.Svc.URLOpener("https://github.com/LJMStark/Colab-Tools")
				}
			})
			SmallActionButton(c, pal, "作者 X (@starkljm)", nil, false, func() {
				if m.Svc.URLOpener != nil {
					m.Svc.URLOpener("https://x.com/starkljm")
				}
			})
			SmallActionButton(c, pal, "官方 Colab 续费", nil, false, func() {
				m.Svc.OpenColabPayPage()
			})
		})
	})
}
