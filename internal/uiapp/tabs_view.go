package uiapp

import (
	"fmt"
	"runtime"
	"slices"
	"strconv"
	"strings"

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
}

func (m *MainUI) renderPersistentQueueSection(c *ui.Context, pal Palette, acc core.AccountProfile) {
	applyToggle := func(next bool) {
		cp := acc
		cp.QueuePersistentMode = next
		if cp.QueuePersistentMaxHours <= 0 {
			cp.QueuePersistentMaxHours = 8
		}
		if next && !cp.AutoShutdownEnabled {
			cp.AutoShutdownEnabled = true
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
			if acc.UseGoogleDrive {
				ui.Text(c, "提醒：已勾选挂 Google Drive，抢到卡后需在 90 秒内点浏览器「允许」；若人不在电脑前，超时会自动切纯云端继续跑（记得手动下成片）。").FontSize(11.5).TextColor(pal.Amber)
			}
		})
		if ui.Switch(c, &persistentOn).Clicked() || persistentOn != acc.QueuePersistentMode {
			applyToggle(persistentOn)
		}
	})
}

// MARK: - Contextual LLM Integrations Panel (Shown ONLY when user picks "大模型代理")

func (m *MainUI) renderIntegrationsPanel(c *ui.Context, pal Palette, acc core.AccountProfile, rt core.AccountRuntimeState) {
	det := m.Svc.ViewState().ClientDetection
	ids := integrations.IdentifiersForAccount(acc.ID)

	SectionCard(c, pal, "编程工具接入（开机后自动安全同步，不改你原本的官方订阅）", "", func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Text(c, "勾选你平时用的工具，大模型开机后自动写入待机渠道（写前 0600 备份、不存明文密钥）。").FontSize(12).TextColor(pal.TextMuted).Grow(1)
			ActionBtn(c, pal, "重新检测", nil, "refresh", false, false, func() {
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
	vs := m.Svc.ViewState()
	ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Gap(4).Children(func() {
			ui.Text(c, "账号").FontSize(22).Bold().TextColor(pal.TextPrimary)
			ui.Text(c, "账号凭证加密存在本机 macOS 系统钥匙串中，多账号点数完全隔离。").FontSize(13).TextColor(pal.TextMuted)
		})
		PillButton(c, pal, "+ 绑定新账号", true, !vs.CLIInstalled, func() {
			m.ShowAddAccPanel = !m.ShowAddAccPanel
		})
	})

	if m.ShowAddAccPanel {
		SectionCard(c, pal, "通过 Google 官方 OAuth2 绑定新账号", "", func() {
			ui.Row(c).FillWidth().Padding(8, 12).Radius(9).Background(pal.SubtleBg).Border(1, pal.SubtleBorder).AlignItems(ui.Center).Gap(10).Children(func() {
				ui.Text(c, "本机终端若已登录过 google-colab-cli，可直接一键导入：").FontSize(12).TextColor(pal.TextMuted).Grow(1)
				SmallActionButton(c, pal, "导入终端已登账号", nil, !vs.CLIInstalled || vs.IsImportingDefault, func() {
					go func() {
						if !m.Svc.TryImportSystemDefaultAccount() {
							m.Svc.ShowToast("未检测到终端默认 colab 登录，请按下方步骤完成 Google 官方授权")
						}
					}()
				})
			})
			m.renderEmbeddedOAuthBinder(c, pal, func() {
				m.formMu.Lock()
				m.ShowAddAccPanel = false
				m.formMu.Unlock()
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
							StatusPill(c, pal, nil, accCopy.Tier.DisplayName())
							if isSel {
								e := pal.Emerald
								StatusPill(c, pal, &e, "当前使用")
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
						m.Svc.RequestConfirmRemoveAccount(accCopy.ID)
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
	cliReady := m.Svc.ViewState().CLIInstalled
	m.formMu.Lock()
	working := m.NewAccWorking
	authURL := m.NewAccAuthURL
	errText := m.NewAccError
	m.formMu.Unlock()

	ui.Column(c).FillWidth().Gap(12).Children(func() {
		ui.Column(c).FillWidth().Padding(10, 12).Radius(10).
			Background(pal.RecessedBg).
			Border(1, pal.RecessedBorder).
			Gap(5).Children(func() {
			ui.Text(c, "Google 官方账号授权（3 步完成）").FontSize(12.5).FontWeight(600).TextColor(pal.TextPrimary)
			ui.Text(c, "第 1 步：点击下方「打开浏览器完成 Google 官方授权」，将自动拉起浏览器登录页。").FontSize(12).TextColor(pal.TextMuted)
			ui.Text(c, "第 2 步：选择要绑定的 Google Colab 账号并点击「允许」，复制网页最终给出的授权码。").FontSize(12).TextColor(pal.TextMuted)
			ui.Text(c, "第 3 步：把授权码粘贴到下方输入框，点击「完成绑定」即可。").FontSize(12).TextColor(pal.TextMuted)
		})

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
			PillButton(c, pal, "打开浏览器完成 Google 官方授权", true, working || !cliReady, func() {
				m.formMu.Lock()
				m.NewAccWorking = true
				m.NewAccError = ""
				m.formMu.Unlock()
				go func() {
					u, err := m.Svc.BeginOAuthFlow()
					m.formMu.Lock()
					m.NewAccWorking = false
					if err != nil {
						m.NewAccError = err.Error()
					} else {
						m.NewAccAuthURL = u
					}
					m.formMu.Unlock()
					if err == nil {
						m.Svc.ShowToast("已打开浏览器，授权后请复制授权码粘贴到下方")
					} else if m.Svc.OnUIInvalidate != nil {
						m.Svc.OnUIInvalidate()
					}
				}()
			})
			if authURL != "" {
				SmallActionButton(c, pal, "复制授权链接", nil, false, func() {
					m.Svc.CopyToClipboard(authURL, "Google OAuth2 链接")
				})
			}
		})

		ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
			ui.TextInput(c, &m.NewAccAuthCode).Placeholder("粘贴第 2 步复制的 Google 授权码").Grow(1)
			PillButton(c, pal, "完成绑定", true, working || strings.TrimSpace(m.NewAccAuthCode) == "", func() {
				m.formMu.Lock()
				m.NewAccWorking = true
				m.NewAccError = ""
				m.formMu.Unlock()
				tier := core.AllSubscriptionTiers[m.NewAccTierIdx%len(core.AllSubscriptionTiers)]
				name := m.NewAccName
				code := m.NewAccAuthCode
				go func() {
					err := m.Svc.CompleteOAuthFlow(name, tier, code)
					m.formMu.Lock()
					m.NewAccWorking = false
					if err != nil {
						m.NewAccError = err.Error()
					} else {
						m.NewAccName = ""
						m.NewAccAuthCode = ""
						m.NewAccAuthURL = ""
					}
					m.formMu.Unlock()
					if err == nil && onSuccess != nil {
						onSuccess()
					}
					if m.Svc.OnUIInvalidate != nil {
						m.Svc.OnUIInvalidate()
					}
				}()
			})
		})

		if errText != "" {
			ui.Text(c, "⚠ "+errText).FontSize(12).TextColor(pal.Coral)
		}
	})
}

// MARK: - Tab 3: Single-Page Clean Settings (Zero Nested Sub-Tabs)

func (m *MainUI) renderSettingsPage(c *ui.Context, pal Palette) {
	vs := m.Svc.ViewState()
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
					ChipToggle(c, pal, lbl, vs.ThemePreference == k, false, func() {
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
				cur := vs.StatusBarMode
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
					ChipToggle(c, pal, lbl, cur == k, false, func() {
						m.Svc.UpdateSettings(func(s *core.AppSettings) { s.StatusBarMode = k })
					})
				}
			})
		})

		ui.Box(c).FillWidth().Height(1).Background(pal.Divider)

		notif := vs.EnableNotifications
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				ui.Text(c, "桌面通知提醒").FontSize(13.5).FontWeight(600).TextColor(pal.TextPrimary)
				ui.Text(c, "抢到卡、装好 ComfyUI、未抢到卡或即将自动关机时弹窗响铃提醒").FontSize(12).TextColor(pal.TextMuted)
			})
			SmallActionButton(c, pal, "测试通知", nil, false, func() {
				go m.Svc.SendTestNotification()
			})
			if ui.Switch(c, &notif).Clicked() || notif != vs.EnableNotifications {
				m.Svc.UpdateSettings(func(s *core.AppSettings) { s.EnableNotifications = notif })
			}
		})
	})

	// 2. Official CLI Environment (single compact card, no separate sub-tab)
	env := vs.EnvReport
	SectionCard(c, pal, "运行环境（官方 google-colab-cli）", strings.ToUpper(runtime.GOOS), func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				detail := "未检测到可执行文件，可点右侧重新打开开箱向导一键安装"
				if vs.CLIInstalled {
					ver := ""
					if env.ColabVersion != "" {
						ver = " v" + env.ColabVersion
					}
					detail = fmt.Sprintf("已就绪%s · %s", ver, env.ColabPath)
				} else if env.ColabBrokenReason != "" {
					detail = env.ColabBrokenReason
				}
				ui.Text(c, detail).FontSize(12.5).TextColor(pal.TextPrimary)
				ui.Text(c, "包管理环境："+env.PackageManagerSummary()).FontSize(11.5).TextColor(pal.TextMuted)
			})
			if vs.CLIInstalled {
				e := pal.Emerald
				StatusPill(c, pal, &e, "正常")
			} else {
				a := pal.Amber
				StatusPill(c, pal, &a, "需安装")
			}
			ActionBtn(c, pal, "重新体检", nil, "refresh", false, vs.IsCheckingEnv, func() {
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
				ActionBtn(c, pal, "恢复小白推荐默认", &e, "", false, false, func() {
					m.Svc.ResetAccountToRecommendedDefaults(acc.ID)
					m.DraftAccountID = ""
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
						m.Svc.ShowToast("已保存 Google Drive 目录设置")
					})
				})

				ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "Drive 模型目录").FontSize(12.5).TextColor(pal.TextMuted).Width(96)
					ui.TextInput(c, &m.DriveModelsDraft).Placeholder("/content/drive/MyDrive/ColabTools/models（留空使用默认）").Grow(1)
				})

				snapRestore := acc.RestoreCustomNodesSnapshot
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
					ui.Column(c).Grow(1).Gap(2).Cursor(ui.CursorPointer).OnClick(func() {
						cp := acc
						cp.RestoreCustomNodesSnapshot = !acc.RestoreCustomNodesSnapshot
						m.Svc.UpdateAccount(cp)
					}).Children(func() {
						ui.Text(c, "换机自动恢复自定义节点快照").FontSize(12.5).FontWeight(500).TextColor(pal.TextPrimary)
						ui.Text(c, "默认关闭以保持最快开机；开启后将在关机前记录你在 Manager 装过的节点，下次开机自动恢复。").FontSize(11.5).TextColor(pal.TextMuted)
					})
					if ui.Switch(c, &snapRestore).Clicked() || snapRestore != acc.RestoreCustomNodesSnapshot {
						cp := acc
						cp.RestoreCustomNodesSnapshot = snapRestore
						m.Svc.UpdateAccount(cp)
					}
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
					StatusPill(c, pal, &a, "v"+app.AppVersion)
					s := pal.Sky
					StatusPill(c, pal, &s, "macOS")
				})
				ui.Text(c, "全调用 Google 官方 google-colab-cli · 纯 Go 零依赖 macOS 桌面控制台").FontSize(12).TextColor(pal.TextMuted)
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
