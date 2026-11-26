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

// MARK: - Reusable Power-User Subpanel: Smart Strategy Protection & Custom GPU Priority
// Embedded inside the Home options drawer.

func (m *MainUI) renderSmartStrategyDetails(c *ui.Context, pal Palette, acc core.AccountProfile) {
	isH3Orig := !acc.EnableLLMModule && acc.DeployRecipe == core.RecipeH3Video && acc.H3Variant == core.H3VariantOriginal
	fbOn, fbRounds, fbTarget := core.EffectiveFallback(acc)

	var note string
	var noteCol ui.Color = pal.TextMuted
	switch {
	case isH3Orig && !acc.AllowCrossModelFallback:
		note = "显存保护已开启：H3 原版 (31B) 仅抢高显存大卡，不会自动降级到 L4。"
	case isH3Orig && acc.AllowCrossModelFallback:
		note = fmt.Sprintf("已允许跨模型保底：前 %d 轮主攻大卡，第 %d 轮起自动加入 %s 保底并切轻量 GGUF。", fbRounds, fbRounds+1, fbTarget.ShortChipLabel())
		noteCol = pal.Amber
	case fbOn:
		note = fmt.Sprintf("已启用安全保底：前 %d 轮主攻大卡，第 %d 轮起自动加入 %s 保底。", fbRounds, fbRounds+1, fbTarget.ShortChipLabel())
	default:
		note = "当前方案已按显存需求自动匹配稳妥顺位，不会误开不兼容显卡。"
	}

	ui.Row(c).FillWidth().Padding(8, 12).Radius(9).Background(pal.SubtleBg).Border(1, pal.SubtleBorder).AlignItems(ui.Center).Gap(8).Children(func() {
		DrawPrecisionLED(c, pal.Amber, 10)
		ui.Text(c, note).FontSize(12).TextColor(noteCol).Grow(1)
	})

	if isH3Orig {
		allowFB := acc.AllowCrossModelFallback
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
			ui.Column(c).Grow(1).Gap(2).Cursor(ui.CursorPointer).OnClick(func() {
				cp := acc
				cp.AllowCrossModelFallback = !acc.AllowCrossModelFallback
				m.Svc.UpdateAccount(cp)
			}).Children(func() {
				ui.Text(c, "大卡 2 轮无货时，允许保底开 L4 并自动切轻量 GGUF").FontSize(13).FontWeight(600).TextColor(pal.TextPrimary)
				ui.Text(c, "默认关闭以保留 31B 原版满血画质；开启后若大卡抢不到，将改开 L4 并自动切为轻量 GGUF").FontSize(11.5).TextColor(pal.TextMuted)
			})
			if ui.Switch(c, &allowFB).Clicked() || allowFB != acc.AllowCrossModelFallback {
				cp := acc
				cp.AllowCrossModelFallback = allowFB
				m.Svc.UpdateAccount(cp)
			}
		})
	}
}

func (m *MainUI) renderCustomGPUStrategyDetails(c *ui.Context, pal Palette, acc core.AccountProfile) {
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
			ui.Row(c).FillWidth().Padding(10, 12).Radius(11).Background(bg).Border(1, border).AlignItems(ui.Center).Gap(10).OnClick(func() {
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
					ui.Row(c).Width(26).Height(24).Radius(7).Background(pal.Amber).Justify(ui.Center).AlignItems(ui.Center).Shrink(0).Children(func() {
						ui.Textf(c, "#%d", pos+1).FontSize(11.5).Bold().FontFeatures("tnum").TextColor(pal.PrimaryBtnFg)
					})
				} else {
					ui.Row(c).Width(26).Height(24).Radius(7).Background(pal.SubtleBg).Justify(ui.Center).AlignItems(ui.Center).Shrink(0).Children(func() {
						ui.Text(c, "+").FontSize(13.5).FontWeight(600).TextColor(pal.TextMuted)
					})
				}

				ui.Column(c).Grow(1).Gap(2).Children(func() {
					ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
						ui.Text(c, hwOpt.ShortChipLabel()).FontSize(13).FontWeight(600).TextColor(pal.TextPrimary)
						if role := hwOpt.RoleTag(); role != "" {
							Badge(c, pal, role, nil)
						}
					})
					ui.Text(c, hwOpt.SpecSubtitle()).FontSize(11).TextColor(pal.TextMuted)
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

	ui.Box(c).FillWidth().Height(1).Background(pal.Divider)

	// Two-Stage Auto-Fallback
	fbOn := acc.QueueFallbackEnabled
	ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			ui.Text(c, "两阶段保底（前几轮只抢主力卡，拿不到再加保底卡）").FontSize(13).FontWeight(600).TextColor(pal.TextPrimary)
			ui.Text(c, "既不错过稀缺大卡，也避免一直空等").FontSize(11.5).TextColor(pal.TextMuted)
		})
		if ui.Switch(c, &fbOn).Clicked() || fbOn != acc.QueueFallbackEnabled {
			cp := acc
			cp.QueueFallbackEnabled = fbOn
			m.Svc.UpdateAccount(cp)
		}
	})

	if acc.QueueFallbackEnabled {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "前").FontSize(12.5).TextColor(pal.TextMuted)
			for _, r := range []int{1, 2, 3, 4} {
				rVal := r
				var tint *ui.Color
				if acc.QueueFallbackAfterRounds == rVal {
					t := pal.Amber
					tint = &t
				}
				SmallActionButton(c, pal, fmt.Sprintf("%d轮", rVal), tint, false, func() {
					cp := acc
					cp.QueueFallbackAfterRounds = rVal
					m.Svc.UpdateAccount(cp)
				})
			}
			ui.Text(c, "后加入保底：").FontSize(12.5).TextColor(pal.TextMuted)
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

	ui.Box(c).FillWidth().Height(1).Background(pal.Divider)

	// Off-Peak Scheduled Start
	schOn := acc.ScheduledStartEnabled
	ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			ui.Text(c, "清晨错峰定时预约（推荐 06:30–07:30）").FontSize(13).FontWeight(600).TextColor(pal.TextPrimary)
			ui.Text(c, "定时预约适合明天低峰自动开，持续守候适合现在挂着等卡（错过超 10 分钟自动过期不误启）").FontSize(11.5).TextColor(pal.TextMuted)
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
			for _, hm := range [][2]int{{6, 30}, {7, 0}, {7, 30}} {
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
			SmallActionButton(c, pal, "−15分", nil, false, func() {
				m.adjustScheduleMinutes(acc, -15)
			})
			ui.Textf(c, "%02d:%02d", acc.ScheduledStartHour, acc.ScheduledStartMinute).FontSize(13.5).FontWeight(600).FontFeatures("tnum").TextColor(pal.TextPrimary)
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

	// Local 6-Slot Allocation Hit-Rate Reference
	hwList := acc.Tier.QueueableAccelerators()
	selectedHW := m.SelectedInsightHW
	if selectedHW == "" && len(hwList) > 0 {
		selectedHW = hwList[0].ShortChipLabel()
	}
	totalSamples, slots, recSummary := m.Svc.InsightsSnapshot(selectedHW)
	if totalSamples > 0 {
		ui.Box(c).FillWidth().Height(1).Background(pal.Divider)
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Children(func() {
			ui.Textf(c, "本机时段命中率（共 %d 次记录）", totalSamples).FontSize(12.5).FontWeight(500).TextColor(pal.TextMuted)
			ui.Spacer(c)
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
		ui.Grid(c).FillWidth().ColumnTracks(ui.Fr(1), ui.Fr(1), ui.Fr(1), ui.Fr(1), ui.Fr(1), ui.Fr(1)).Gap(8).Children(func() {
			for _, slot := range slots {
				s := slot
				ui.Column(c).FillWidth().AlignItems(ui.Center).Gap(4).Children(func() {
					rate, hasData := s.Rate()
					pctLabel := "—"
					if hasData {
						pctLabel = fmt.Sprintf("%d%%", int(math.Round(rate*100)))
					}
					ui.Text(c, pctLabel).FontSize(12).FontWeight(600).FontFeatures("tnum").TextColor(pal.TextPrimary)
					ui.Box(c).FillWidth().Height(36).Draw(func(p *ui.Painter, r ui.Rect) {
						p.Fill(r, pal.SubtleBg, 5)
						if hasData && rate > 0 {
							h := max(float32(6), r.H*float32(rate))
							col := pal.Amber
							if rate >= 0.5 {
								col = pal.Emerald
							}
							p.Fill(ui.Rect{X: r.X, Y: r.Y + r.H - h, W: r.W, H: h}, col, 5)
						}
					})
					ui.Text(c, s.Label).FontSize(11).FontFeatures("tnum").TextColor(pal.TextMuted)
				})
			}
		})
		if recSummary != "" {
			ui.Text(c, "✦ "+recSummary).FontSize(12).TextColor(pal.Sky)
		}
	}
}

func (m *MainUI) renderPersistentQueueSection(c *ui.Context, pal Palette, acc core.AccountProfile) {
	isVideo := !acc.EnableLLMModule && (acc.DeployRecipe.IsVideoComfy() || acc.WorkloadKind == core.WorkloadVideo)
	applyToggle := func(next bool) {
		cp := acc
		cp.QueuePersistentMode = next
		cp.QueuePersistentMaxHours = 8
		if next && !cp.AutoShutdownEnabled {
			cp.AutoShutdownEnabled = true
			if isVideo {
				cp.StopAfterQueueDrained = true
			}
		}
		m.Svc.UpdateAccount(cp)
	}

	persistentOn := acc.QueuePersistentMode
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
		ui.Column(c).Grow(1).Gap(2).Cursor(ui.CursorPointer).OnClick(func() {
			applyToggle(!acc.QueuePersistentMode)
		}).Children(func() {
			ui.Text(c, "抢不到就持续守候（低频挂机，最长 8 小时）").FontSize(13).FontWeight(600).TextColor(pal.TextPrimary)
			ui.Text(c, "前 3 轮快速试，之后每 8–12 分钟探一次防风控；遇限流立刻停，抢到自动开启闲置关机").FontSize(11.5).TextColor(pal.TextMuted)
		})
		if ui.Switch(c, &persistentOn).Clicked() || persistentOn != acc.QueuePersistentMode {
			applyToggle(persistentOn)
		}
	})
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

// MARK: - Contextual LLM Integrations Panel (Shown ONLY when user picks "大模型代理")

func (m *MainUI) renderIntegrationsPanel(c *ui.Context, pal Palette, acc core.AccountProfile, rt core.AccountRuntimeState) {
	det := m.Svc.ClientDetection
	ids := integrations.IdentifiersForAccount(acc.ID)

	SectionCard(c, pal, "编程工具接入（开机后自动安全同步，不改你原本的官方订阅）", "", func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Text(c, "勾选你平时用的工具，大模型开机后自动写入待机渠道（写前 0600 备份、不存明文密钥）。").FontSize(12).TextColor(pal.TextMuted).Grow(1)
			neutralPrecisionButton(c, pal, nil, "refresh", "重新检测", false, func() {
				m.Svc.RefreshClientDetection()
				m.Svc.ShowToast("已完成本机编程工具探测")
			})
		})

		// CC Switch
		ccOn := acc.AutoSyncCCSwitch
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "CC Switch").FontSize(13.5).FontWeight(600).TextColor(pal.TextPrimary)
					if det.CCSwitchInstalled {
						e := pal.Emerald
						Badge(c, pal, "已检测到", &e)
					}
					if rt.CCSwitchSynced {
						s := pal.Sky
						Badge(c, pal, "已同步", &s)
					}
				})
				ui.Text(c, "写入独立待机渠道，不影响你当前激活的官方订阅").FontSize(11.5).TextColor(pal.TextMuted)
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
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Text(c, "渠道名称").FontSize(12.5).TextColor(pal.TextMuted)
				ui.TextInput(c, &m.CCSwitchNameDraft).Width(260).Placeholder("Colab Qwen3.8-27B")
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
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "OpenCode").FontSize(13.5).FontWeight(600).TextColor(pal.TextPrimary)
					if det.OpenCodeInstalled {
						e := pal.Emerald
						Badge(c, pal, "已检测到", &e)
					}
					if rt.OpenCodeSynced {
						s := pal.Sky
						Badge(c, pal, "已同步", &s)
					}
				})
				ui.Textf(c, "合并写入 opencode.json（通过环境变量 %s 引用密钥）", ids.EnvKey).FontSize(11.5).TextColor(pal.TextMuted).MaxLines(1).Ellipsis("…")
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
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "Codex CLI").FontSize(13.5).FontWeight(600).TextColor(pal.TextPrimary)
					if det.CodexInstalled {
						e := pal.Emerald
						Badge(c, pal, "已检测到", &e)
					}
					if rt.CodexSynced {
						s := pal.Sky
						Badge(c, pal, "已同步", &s)
					}
				})
				ui.Text(c, "写入独立托管区块到 ~/.codex/config.toml（仅 env_key，不动官方默认）").FontSize(11.5).TextColor(pal.TextMuted)
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

		// Claude Code + Universal Copy Hub
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "Claude Code / 通用复制（Cherry Studio · Cursor · Cline）").FontSize(13.5).FontWeight(600).TextColor(pal.TextPrimary)
					if det.ClaudeCodeInstalled {
						e := pal.Emerald
						Badge(c, pal, "Claude 已检测", &e)
					}
				})
				ui.Text(c, "可直接安全拉起 Claude 终端，或复制 OpenAI / Anthropic 配置模板").FontSize(11.5).TextColor(pal.TextMuted)
			})
			SmallActionButton(c, pal, ">_ 拉起 Claude", nil, rt.PublicURL == "", func() {
				m.Svc.LaunchClaudeTerminal(acc.ID)
			})
		})

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
			SmallActionButton(c, pal, "复制安全模板", nil, false, func() {
				m.Svc.CopyToClipboard(safeEnv, "环境变量安全模板")
			})
			SmallActionButton(c, pal, "复制 JSON", nil, false, func() {
				js := integrations.ExportJSONSnippet(m.SelectedCopyProto, displayURL, rt.ModelAlias, "", ids.EnvKey, false)
				m.Svc.CopyToClipboard(js, "JSON 安全模板")
			})
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

// MARK: - Tab 2: Clean Multi-Account Manager & Official OAuth2 Binding

func (m *MainUI) renderAccountsPage(c *ui.Context, pal Palette) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Gap(4).Children(func() {
			ui.Text(c, "账号").FontSize(22).Bold().TextColor(pal.TextPrimary)
			ui.Text(c, "账号凭证加密存在本机系统凭据库（macOS 钥匙串 / Windows 凭据管理器），多账号点数完全隔离。").FontSize(13).TextColor(pal.TextMuted)
		})
		PillButton(c, pal, "+ 绑定新账号", true, !m.Svc.CLIInstalled, func() {
			m.ShowAddAccPanel = !m.ShowAddAccPanel
		})
	})

	if m.ShowAddAccPanel {
		SectionCard(c, pal, "通过 Google 官方 OAuth2 绑定新账号", "", func() {
			ui.Row(c).FillWidth().Padding(8, 12).Radius(9).Background(pal.SubtleBg).Border(1, pal.SubtleBorder).AlignItems(ui.Center).Gap(10).Children(func() {
				ui.Text(c, "本机终端若已登录过 google-colab-cli，可直接一键导入：").FontSize(12).TextColor(pal.TextMuted).Grow(1)
				SmallActionButton(c, pal, "导入终端已登账号", nil, !m.Svc.CLIInstalled || m.Svc.IsImportingDefault, func() {
					go func() {
						if !m.Svc.TryImportSystemDefaultAccount() {
							m.Svc.ShowToast("未检测到终端默认 colab 登录，请按下方步骤完成 Google 官方授权")
						}
					}()
				})
			})
			m.renderEmbeddedOAuthBinder(c, pal, func() {
				m.ShowAddAccPanel = false
			})
		})
	}

	accounts := m.Svc.AccountsSnapshot()
	selAcc, _ := m.Svc.SelectedAccount()
	SectionCard(c, pal, fmt.Sprintf("已绑定账号 (%d)", len(accounts)), "", func() {
		if len(accounts) == 0 {
			ui.Text(c, "还没有绑定账号，点上方「+ 绑定新账号」即可开始（展开后也支持导入终端已登账号）。").FontSize(13).TextColor(pal.TextMuted)
			return
		}
		for idx, acc := range accounts {
			if idx > 0 {
				ui.Box(c).FillWidth().Height(1).Background(pal.Divider)
			}
			accCopy := acc
			rt := m.Svc.StateFor(accCopy.ID)
			isSel := accCopy.ID == selAcc.ID
			editingTier := m.EditingTierAccountID == accCopy.ID

			ui.Column(c).FillWidth().Gap(8).Padding(6, 0).Children(func() {
				ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
					DrawPrecisionLED(c, AccountTagColor(pal, accCopy.ColorTag), 13)
					ui.Column(c).Grow(1).Gap(2).Children(func() {
						ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
							ui.Text(c, accCopy.DisplayName).FontSize(14).FontWeight(600).TextColor(pal.TextPrimary)
							recessedStatusPill(c, pal, nil, accCopy.Tier.DisplayName())
							if isSel {
								e := pal.Emerald
								recessedStatusPill(c, pal, &e, "当前使用")
							}
						})
						email := accCopy.Email
						if email == "" {
							email = "默认官方 CLI 账号"
						}
						ui.Textf(c, "%s · 余额 %s CU · %s", email, core.FormatCU(rt.CUBalance), core.EffectiveGPUPlanSummary(accCopy)).FontSize(12).TextColor(pal.TextMuted)
					})
					if !isSel {
						SmallActionButton(c, pal, "切换使用", nil, false, func() {
							m.Svc.SelectAccount(accCopy.ID)
						})
					}
					tierBtn := "改订阅"
					if editingTier {
						tierBtn = "收起"
					}
					SmallActionButton(c, pal, tierBtn, nil, false, func() {
						if editingTier {
							m.EditingTierAccountID = ""
						} else {
							m.EditingTierAccountID = accCopy.ID
						}
					})
					SmallActionButton(c, pal, "移除", &pal.Coral, false, func() {
						m.Svc.RemoveAccount(accCopy.ID)
					})
				})

				// Direct tier picker (replaces blind cycling)
				if editingTier {
					ui.Row(c).FillWidth().Padding(8, 10).Radius(9).Background(pal.SubtleBg).Gap(6).AlignItems(ui.Center).Children(func() {
						ui.Text(c, "选择订阅档位：").FontSize(12).TextColor(pal.TextMuted)
						for _, tOpt := range core.AllSubscriptionTiers {
							tierVal := tOpt
							var tint *ui.Color
							if accCopy.Tier == tierVal {
								a := pal.Amber
								tint = &a
							}
							SmallActionButton(c, pal, tierVal.ShortBadge(), tint, false, func() {
								cp := accCopy
								cp.Tier = tierVal
								cp.PreferredAccelerator = tierVal.DefaultAccelerator()
								cp.Sanitize()
								m.Svc.UpdateAccount(cp)
								m.EditingTierAccountID = ""
								m.Svc.ShowToast("已切换订阅档位为：" + tierVal.DisplayName())
							})
						}
					})
				}
			})
		}
	})
}

func (m *MainUI) renderEmbeddedOAuthBinder(c *ui.Context, pal Palette, onSuccess func()) {
	ui.Column(c).FillWidth().Gap(12).Children(func() {
		ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "账号备注名").FontSize(13).TextColor(pal.TextMuted).Width(90)
			ui.TextInput(c, &m.NewAccName).Placeholder("例如：主号 / AI Ultra（可留空自动用邮箱前缀）").Grow(1)
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

// MARK: - Tab 3: Single-Page Clean Settings (Zero Nested Sub-Tabs)

func (m *MainUI) renderSettingsPage(c *ui.Context, pal Palette) {
	ui.Column(c).FillWidth().Gap(4).Children(func() {
		ui.Text(c, "设置").FontSize(22).Bold().TextColor(pal.TextPrimary)
		ui.Text(c, "只保留常用系统偏好，模型与显卡在「主页」一步就能选好，修改即时生效。").FontSize(13).TextColor(pal.TextMuted)
	})

	// 1. Common Preferences
	SectionCard(c, pal, "常用偏好", "", func() {
		openAtLogin := false
		if mygo.App.IsReady() {
			openAtLogin = mygo.App.OpenAtLogin()
		}
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				ui.Text(c, "开机自动启动").FontSize(13.5).FontWeight(600).TextColor(pal.TextPrimary)
				ui.Text(c, "登录电脑后静默常驻菜单栏 / 托盘，方便随时看进度").FontSize(12).TextColor(pal.TextMuted)
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

		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				ui.Text(c, "界面外观").FontSize(13.5).FontWeight(600).TextColor(pal.TextPrimary)
				ui.Text(c, "默认跟随系统深浅色自动切换").FontSize(12).TextColor(pal.TextMuted)
			})
			ui.Row(c).Padding(3).Radius(9).Background(pal.RecessedBg).Border(1, pal.RecessedBorder).InsetShadow(0, 1, 1, 0, pal.RecessedInnerShadow).Gap(2).Shrink(0).Children(func() {
				for _, o := range [][2]string{{"system", "跟随系统"}, {"dark", "深色"}, {"light", "浅色"}} {
					k, lbl := o[0], o[1]
					segmentedOptionPill(c, pal, lbl, m.Svc.Settings.ThemePreference == k, func() {
						m.Svc.UpdateSettings(func(s *core.AppSettings) { s.ThemePreference = k })
					})
				}
			})
		})

		ui.Box(c).FillWidth().Height(1).Background(pal.Divider)

		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				ui.Text(c, "顶部菜单栏状态显示").FontSize(13.5).FontWeight(600).TextColor(pal.TextPrimary)
				ui.Text(c, "控制顶部菜单栏图标旁是否显示运行卡数与点数（macOS）").FontSize(12).TextColor(pal.TextMuted)
			})
			ui.Row(c).Padding(3).Radius(9).Background(pal.RecessedBg).Border(1, pal.RecessedBorder).InsetShadow(0, 1, 1, 0, pal.RecessedInnerShadow).Gap(2).Shrink(0).Children(func() {
				cur := m.Svc.Settings.StatusBarMode
				if cur == "" {
					cur = core.StatusBarCompact
				}
				for _, o := range []struct {
					mode core.StatusBarDisplayMode
					lbl  string
				}{
					{core.StatusBarIconOnly, "仅图标"},
					{core.StatusBarCompact, "简洁"},
					{core.StatusBarDetailed, "详细"},
				} {
					k, lbl := o.mode, o.lbl
					segmentedOptionPill(c, pal, lbl, cur == k, func() {
						m.Svc.UpdateSettings(func(s *core.AppSettings) { s.StatusBarMode = k })
					})
				}
			})
		})

		ui.Box(c).FillWidth().Height(1).Background(pal.Divider)

		notif := m.Svc.Settings.EnableNotifications
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				ui.Text(c, "桌面通知提醒").FontSize(13.5).FontWeight(600).TextColor(pal.TextPrimary)
				ui.Text(c, "抢到卡、装好 ComfyUI、未抢到卡或即将自动关机时弹窗响铃提醒").FontSize(12).TextColor(pal.TextMuted)
			})
			SmallActionButton(c, pal, "测试通知", nil, false, func() {
				go m.Svc.SendTestNotification()
			})
			if ui.Switch(c, &notif).Clicked() || notif != m.Svc.Settings.EnableNotifications {
				m.Svc.UpdateSettings(func(s *core.AppSettings) { s.EnableNotifications = notif })
			}
		})
	})

	// 2. Official CLI Environment (single compact card, no separate sub-tab)
	env := m.Svc.EnvReport
	SectionCard(c, pal, "运行环境（官方 google-colab-cli）", strings.ToUpper(runtime.GOOS), func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				detail := "未检测到可执行文件，可点右侧重新打开开箱向导一键安装"
				if m.Svc.CLIInstalled {
					ver := ""
					if env.ColabVersion != "" {
						ver = " v" + env.ColabVersion
					}
					bridge := ""
					if env.UsingWSLBridge {
						bridge = "（WSL2 桥接）"
					}
					detail = fmt.Sprintf("已就绪%s%s · %s", ver, bridge, env.ColabPath)
				} else if env.ColabBrokenReason != "" {
					detail = env.ColabBrokenReason
				}
				ui.Text(c, detail).FontSize(12.5).TextColor(pal.TextPrimary)
				ui.Text(c, "包管理环境："+env.PackageManagerSummary()).FontSize(11.5).TextColor(pal.TextMuted)
			})
			if m.Svc.CLIInstalled {
				e := pal.Emerald
				recessedStatusPill(c, pal, &e, "正常")
			} else {
				a := pal.Amber
				recessedStatusPill(c, pal, &a, "需安装")
			}
			neutralPrecisionButton(c, pal, nil, "refresh", "重新体检", m.Svc.IsCheckingEnv, func() {
				go m.Svc.RefreshEnvironmentCheck()
			})
			SmallActionButton(c, pal, "开箱向导", nil, false, func() {
				m.Svc.ReopenOnboarding()
			})
		})
	})

	// 3. Collapsible Advanced & Troubleshooting (hidden by default so beginners are never overwhelmed)
	if acc, ok := m.Svc.SelectedAccount(); ok {
		SectionCard(c, pal, "高级选项与一键重置", "平时不用动，折腾自定义脚本或目录时再展开", func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
				e := pal.Emerald
				neutralPrecisionButton(c, pal, &e, "", "恢复小白推荐默认", false, func() {
					m.Svc.ResetAccountToRecommendedDefaults(acc.ID)
				})
				ui.Spacer(c)
				advLbl := "展开高级参数 ▾"
				if m.ShowAdvancedSettings {
					advLbl = "收起高级参数 ▴"
				}
				SmallActionButton(c, pal, advLbl, nil, false, func() {
					m.ShowAdvancedSettings = !m.ShowAdvancedSettings
				})
			})

			if m.ShowAdvancedSettings {
				ui.Box(c).FillWidth().Height(1).Background(pal.Divider)

				ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "Drive 成片目录").FontSize(12.5).TextColor(pal.TextMuted).Width(96)
					ui.TextInput(c, &m.DriveOutputDraft).Placeholder("/content/drive/MyDrive/ColabTools/outputs").Grow(1)
					SmallActionButton(c, pal, "保存目录", nil, false, func() {
						cp := acc
						cp.DriveModelsDir = core.SanitizeDriveSubpath(m.DriveModelsDraft, "")
						cp.DriveOutputDir = core.SanitizeDriveSubpath(m.DriveOutputDraft, "/content/drive/MyDrive/ColabTools/outputs")
						m.Svc.UpdateAccount(cp)
						m.Svc.ShowToast("已保存 Google Drive 成片目录")
					})
				})

				ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "自定义启动脚本").FontSize(12.5).TextColor(pal.TextMuted).Width(96)
					ui.TextInput(c, &m.DriveScriptDraft).Grow(1)
					SmallActionButton(c, pal, "保存", nil, false, func() {
						cp := acc
						cp.DriveScriptPath = strings.TrimSpace(m.DriveScriptDraft)
						if cp.DeployRecipe.IsVideoComfy() && cp.DriveScriptPath != "" {
							cp.DeployRecipe = core.RecipeNone
						}
						m.Svc.UpdateAccount(cp)
						m.Svc.ShowToast("已保存自定义远端脚本路径")
					})
				})

				if strings.TrimSpace(acc.DriveScriptPath) != "" {
					ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
						ui.Text(c, "远端端口").FontSize(12.5).TextColor(pal.TextMuted).Width(96)
						ui.TextInput(c, &m.RemotePortDraft).Width(90)
						ui.Text(c, "本地通道端口").FontSize(12.5).TextColor(pal.TextMuted)
						ui.TextInput(c, &m.LocalPortDraft).Width(90)
						SmallActionButton(c, pal, "校验并保存", nil, false, func() {
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
				}
			}
		})
	}

	// 4. Compact About Strip
	SectionCard(c, pal, "", "", func() {
		ui.Row(c).FillWidth().Gap(14).AlignItems(ui.Center).Children(func() {
			if appIconBitmap != nil {
				ui.Image(c, appIconBitmap).Width(48).Height(48).Radius(12).Clip().Border(1, pal.SubtleBorder)
			}
			ui.Column(c).Grow(1).Gap(4).Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "Colab Tools").FontSize(16).Bold().TextColor(pal.TextPrimary)
					a := pal.Amber
					recessedStatusPill(c, pal, &a, "v"+app.AppVersion)
					s := pal.Sky
					recessedStatusPill(c, pal, &s, "macOS & Windows")
				})
				ui.Text(c, "全调用 Google 官方 google-colab-cli · 纯 Go 零依赖跨平台桌面控制台").FontSize(12).TextColor(pal.TextMuted)
			})
			SmallActionButton(c, pal, "GitHub", nil, false, func() {
				if m.Svc.URLOpener != nil {
					m.Svc.URLOpener("https://github.com/LJMStark/Colab-Tools")
				}
			})
			SmallActionButton(c, pal, "X (@starkljm)", nil, false, func() {
				if m.Svc.URLOpener != nil {
					m.Svc.URLOpener("https://x.com/starkljm")
				}
			})
			SmallActionButton(c, pal, "Colab 续费", nil, false, func() {
				m.Svc.OpenColabPayPage()
			})
		})
	})
}

func recessedStatusPill(c *ui.Context, pal Palette, dotCol *ui.Color, label string) {
	ui.Row(c).
		Padding(4, 9).
		Radius(7).
		Background(pal.RecessedBg).
		Border(1, pal.RecessedBorder).
		InsetShadow(0, 1, 1, 0, pal.RecessedInnerShadow).
		Gap(5).
		AlignItems(ui.Center).
		Shrink(0).
		Children(func() {
			if dotCol != nil {
				DrawPrecisionLED(c, *dotCol, 9)
			}
			ui.Text(c, label).FontSize(11.5).FontWeight(600).FontFeatures("tnum").TextColor(pal.TextMuted).MaxLines(1)
		})
}

func neutralPrecisionButton(c *ui.Context, pal Palette, dotCol *ui.Color, iconName, label string, disabled bool, onClick func()) {
	el := ui.Row(c).
		Padding(6, 12).
		Radius(8).
		Gap(6).
		AlignItems(ui.Center).
		Justify(ui.Center).
		Shrink(0)

	fg := pal.TextPrimary
	if disabled {
		fg = pal.TextFaint
		el.Background(pal.DisabledBg).Border(1, pal.DisabledBorder)
	} else {
		top := pal.CardTop
		bot := pal.RecessedBg
		if el.Hovered() {
			top = top.Mix(ui.RGB(255, 255, 255), 0.06)
			bot = bot.Mix(pal.CardTop, 0.35)
		}
		el.LinearGradient(ui.LinearGradient{From: top, To: bot, Angle: 180}).
			Border(1, pal.SubtleBorder).
			InsetShadow(0, 1, 0, 0, pal.CardInnerTopLight).
			Shadow(0, 1, 3, 0, pal.CardShadow).
			Cursor(ui.CursorPointer)
		if onClick != nil {
			el.OnClick(onClick)
		}
	}

	el.Children(func() {
		if dotCol != nil && !disabled {
			DrawPrecisionLED(c, *dotCol, 10)
		}
		if iconName != "" {
			icCol := pal.TextMuted
			if disabled {
				icCol = pal.TextFaint
			}
			ToolbarIcon(c, iconName, 13, icCol)
		}
		ui.Text(c, label).FontSize(12).FontWeight(500).TextColor(fg).MaxLines(1)
	})
}

func segmentedOptionPill(c *ui.Context, pal Palette, label string, active bool, onClick func()) {
	el := ui.Row(c).
		Padding(5, 11).
		Radius(6.5).
		Gap(5).
		AlignItems(ui.Center).
		Justify(ui.Center).
		Shrink(0)

	fg := pal.TextMuted
	weight := 500
	if active {
		fg = pal.TextPrimary
		weight = 600
		el.Background(pal.CardTop).
			Border(1, pal.SubtleBorder).
			InsetShadow(0, 1, 0, 0, pal.CardInnerTopLight).
			Shadow(0, 1, 2, 0, pal.CardShadow)
	} else if el.Hovered() {
		fg = pal.TextPrimary
		el.Background(WithAlpha(pal.CardTop, 0.45))
	}
	if onClick != nil {
		el.Cursor(ui.CursorPointer).OnClick(onClick)
	}
	el.Children(func() {
		if active {
			DrawPrecisionLED(c, pal.Amber, 8)
		}
		ui.Text(c, label).FontSize(12).FontWeight(weight).TextColor(fg).MaxLines(1)
	})
}
