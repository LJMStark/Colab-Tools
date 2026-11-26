package uiapp

import (
	"fmt"
	"strings"

	"github.com/LJMStark/Colab-Tools/internal/core"
	"github.com/egoist/mygo/ui"
)

// MARK: - Home Idle Launchpad (First-Principles Zero-Config Experience)

func (m *MainUI) renderHomeIdleLaunchpad(c *ui.Context, pal Palette, acc core.AccountProfile, rt core.AccountRuntimeState) {
	isVideo := !acc.EnableLLMModule && (acc.DeployRecipe.IsVideoComfy() || acc.WorkloadKind == core.WorkloadVideo)
	moreSelected := acc.DeployRecipe == core.RecipeComfyUIGeneral ||
		acc.DeployRecipe == core.RecipeNone ||
		(acc.DeployRecipe == core.RecipeH3Video && (acc.H3Variant == core.H3VariantFlashGGUF || acc.H3Variant == core.H3VariantCustom))

	// 1. Compact Account & Balance Strip (replaces 8 empty dashed monitoring cards while offline)
	hrs, basis, hasEst := rt.EstimatedHoursRemaining(acc)
	offlineBilling := !rt.Phase.IsRunning() && rt.CURatePerHour > 0
	ui.Row(c).FillWidth().Padding(16, 20).Radius(16).Background(pal.CardBg).Border(1, pal.CardBorder).AlignItems(ui.Center).Gap(14).Children(func() {
		ui.Box(c).Width(10).Height(10).Radius(5).Background(AccountTagColor(pal, acc.ColorTag)).Shrink(0)
		ui.Column(c).Grow(1).Gap(3).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, acc.DisplayName).FontSize(15).FontWeight(600).TextColor(pal.TextPrimary).MaxLines(1)
				Badge(c, pal, acc.Tier.DisplayName(), nil)
				if offlineBilling {
					a := pal.Amber
					Badge(c, pal, fmt.Sprintf("⚠ 外部会话计费 %.2f/h", rt.CURatePerHour), &a)
				} else {
					Badge(c, pal, "待机中 · 未计费", nil)
				}
			})
			sub := "准备就绪，选好下面要跑的任务就能一键开跑"
			if hasEst {
				sub = fmt.Sprintf("按当前优先卡 %s 估算，余额约可用 %.0f 小时（单次最长 %d 小时）", basis, hrs, acc.Tier.MaxSessionHours())
			} else if acc.Tier == core.TierFree {
				sub = "免费体验池（T4 / CPU）· 单次最长 12 小时"
			}
			ui.Text(c, sub).FontSize(12).TextColor(pal.TextMuted).MaxLines(1).Ellipsis("…")
		})
		ui.Column(c).AlignItems(ui.End).Gap(2).Shrink(0).Children(func() {
			ui.Row(c).AlignItems(ui.End).Gap(4).Children(func() {
				ui.Text(c, core.FormatCU(rt.CUBalance)).FontSize(26).Bold().FontFeatures("tnum").TextColor(pal.TextPrimary)
				ui.Text(c, "CU").FontSize(12.5).FontWeight(600).TextColor(pal.TextMuted).Padding(0, 0, 4, 0)
			})
			if acc.GPUStrategy == core.GPUStrategyCustom {
				ui.Text(c, "当前为自定义挑卡 · 点此切回智能挑卡").FontSize(11.5).TextColor(pal.Sky).OnClick(func() {
					cp := acc
					cp.GPUStrategy = core.GPUStrategyAutoSmart
					cp.EnablePoliteQueue = true
					m.Svc.UpdateAccount(cp)
					m.Svc.ShowToast("已切换为智能挑卡（自动优先稳妥高配卡）")
				})
			}
		})
	})

	// 2. Step 1: One-Click Scenario Picker (4 clean cards, H3 Flash selected by default)
	SectionCard(c, pal, "你想用云端显卡干嘛？", "点一下选好就行，其他都已按最佳方案配齐", func() {
		ui.Grid(c).FillWidth().ColumnTracks(ui.Fr(1), ui.Fr(1), ui.Fr(1), ui.Fr(1)).Gap(10).Children(func() {
			// Card 1: H3 Flash (Default Recommended)
			flashSel := acc.DeployRecipe == core.RecipeH3Video && acc.H3Variant == core.H3VariantFlash
			m.scenarioCard(c, pal, "H3 Flash 极速版", "默认推荐", "6 步出片 · 速度快省点数", "自动装 ComfyUI + 存 Drive", flashSel, true, func() {
				m.selectH3Preset(acc, core.H3VariantFlash)
				m.ShowMoreRecipes = false
			})

			// Card 2: H3 Original 31B
			origSel := acc.DeployRecipe == core.RecipeH3Video && acc.H3Variant == core.H3VariantOriginal
			m.scenarioCard(c, pal, "H3 原版 (31B)", "画质首选", "官方满血双权重 · 画质最高", "优先大显存卡 G4 / A100", origSel, false, func() {
				m.selectH3Preset(acc, core.H3VariantOriginal)
				m.ShowMoreRecipes = false
			})

			// Card 3: LLM Proxy
			llmSel := acc.DeployRecipe == core.RecipeLLMProxy
			m.scenarioCard(c, pal, "大模型代理", "编程用", "给 Claude / Codex / Cursor", "自动同步本地编程工具", llmSel, false, func() {
				m.Svc.ApplyDeployRecipe(acc.ID, core.RecipeLLMProxy)
				m.ShowMoreRecipes = false
			})

			// Card 4: More modes (GGUF / Custom / General ComfyUI / Pure Compute)
			moreBadge := "更多"
			if acc.DeployRecipe == core.RecipeH3Video && acc.H3Variant == core.H3VariantFlashGGUF {
				moreBadge = "轻量 GGUF"
			} else if acc.DeployRecipe == core.RecipeH3Video && acc.H3Variant == core.H3VariantCustom {
				moreBadge = "自定义 H3"
			} else if acc.DeployRecipe == core.RecipeComfyUIGeneral {
				moreBadge = "ComfyUI"
			} else if acc.DeployRecipe == core.RecipeNone {
				moreBadge = "纯开机"
			}
			m.scenarioCard(c, pal, "更多玩法", moreBadge, "轻量 GGUF · 通用 ComfyUI", "或自定义模型 / 纯开机", moreSelected || m.ShowMoreRecipes, false, func() {
				m.ShowMoreRecipes = !m.ShowMoreRecipes
			})
		})

		// Expandable sub-row ONLY when user clicks "更多玩法" or already picked an extended recipe
		if m.ShowMoreRecipes || moreSelected {
			ui.Column(c).FillWidth().Padding(12).Gap(10).Radius(11).Background(pal.SubtleBg).Border(1, pal.SubtleBorder).Children(func() {
				ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "扩展模式：").FontSize(12.5).FontWeight(500).TextColor(pal.TextMuted)
					m.modeChip(c, pal, "H3 轻量 GGUF（24G L4 可跑）", acc.DeployRecipe == core.RecipeH3Video && acc.H3Variant == core.H3VariantFlashGGUF, func() {
						m.selectH3Preset(acc, core.H3VariantFlashGGUF)
					})
					m.modeChip(c, pal, "自定义 HF / 只用 Drive 模型", acc.DeployRecipe == core.RecipeH3Video && acc.H3Variant == core.H3VariantCustom, func() {
						m.selectH3Preset(acc, core.H3VariantCustom)
					})
					m.modeChip(c, pal, "通用 ComfyUI", acc.DeployRecipe == core.RecipeComfyUIGeneral, func() {
						m.Svc.ApplyDeployRecipe(acc.ID, core.RecipeComfyUIGeneral)
					})
					m.modeChip(c, pal, "只开机不装模型", acc.DeployRecipe == core.RecipeNone, func() {
						m.Svc.ApplyDeployRecipe(acc.ID, core.RecipeNone)
					})
				})

				if acc.DeployRecipe == core.RecipeH3Video && acc.H3Variant == core.H3VariantFlashGGUF {
					ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Text(c, "GGUF 精度：").FontSize(12.5).TextColor(pal.TextMuted)
						for _, q := range []struct{ val, lbl string }{
							{"Q4_K_M", "Q4_K_M（约 8GB · 推荐）"},
							{"Q6_K", "Q6_K（约 11GB · 更细）"},
							{"Q8_0", "Q8_0（约 15GB · 接近原版）"},
						} {
							qv := q.val
							m.modeChip(c, pal, q.lbl, acc.H3GGUFQuant == qv, func() {
								cp := acc
								cp.H3GGUFQuant = qv
								m.Svc.UpdateAccount(cp)
							})
						}
					})
				} else if acc.DeployRecipe == core.RecipeH3Video && acc.H3Variant == core.H3VariantCustom {
					driveOnly := acc.H3DriveOnly
					ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
						ui.Column(c).Grow(1).Gap(2).Children(func() {
							ui.Text(c, "只用我 Google Drive 里的现成模型（不外网下载）").FontSize(13).FontWeight(500).TextColor(pal.TextPrimary)
							ui.Text(c, "直接把你 Drive 里的 diffusion_models 拷到高速盘开跑。").FontSize(11.5).TextColor(pal.TextMuted)
						})
						if ui.Switch(c, &driveOnly).Clicked() || driveOnly != acc.H3DriveOnly {
							cp := acc
							cp.H3DriveOnly = driveOnly
							m.Svc.UpdateAccount(cp)
						}
					})
					if !acc.H3DriveOnly {
						ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
							ui.TextInput(c, &m.H3CustomRepoDraft).Placeholder("HuggingFace 仓库，如 owner/repo").Width(240)
							ui.TextInput(c, &m.H3CustomFilesDraft).Placeholder("模型文件名（多个用英文逗号隔开）").Grow(1)
							SmallActionButton(c, pal, "保存来源", &pal.Amber, false, func() {
								repo := strings.TrimSpace(m.H3CustomRepoDraft)
								files := core.SanitizeCustomHFFiles(m.H3CustomFilesDraft)
								if !core.IsValidHFRepo(repo) || files == "" {
									m.Svc.ShowToast("仓库格式应为 owner/repo，且文件名不能包含空格或特殊字符")
									return
								}
								cp := acc
								cp.H3CustomHFRepo = repo
								cp.H3CustomHFFiles = files
								m.Svc.UpdateAccount(cp)
								m.Svc.ShowToast("已保存自定义 H3 模型来源")
							})
						})
					}
				}
			})
		}
	})

	// 3. Step 2: Zero-Config Smart Summary + Big Primary Launch Button
	isCooling := rt.QueueCircuitBreakTill != nil && rt.QueueCircuitBreakTill.After(timeNow())
	ui.Column(c).FillWidth().Padding(18, 20).Gap(12).Radius(16).Background(pal.CardBg).Border(1, pal.CardBorder).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(16).Children(func() {
			ui.Column(c).Grow(1).Gap(6).Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "已帮你配好默认方案，直接点右边开跑就行").FontSize(14.5).FontWeight(600).TextColor(pal.TextPrimary)
					if acc.ScheduledStartEnabled {
						e := pal.Emerald
						Badge(c, pal, fmt.Sprintf("已设预约 %02d:%02d", acc.ScheduledStartHour, acc.ScheduledStartMinute), &e)
					}
				})
				m.summaryBullet(c, pal, pal.Amber, "挑卡策略："+core.EffectiveGPUPlanSummary(acc)+"，没货或无配额自动试下一张")
				if isVideo {
					driveText := "纯云端运行（未开 Drive，出片后请手动下载）"
					if acc.UseGoogleDrive {
						driveText = "联动 Google Drive（先拷本地高速盘不掉盘，每条成片实时存 Drive）"
					}
					if acc.AutoOpenComfyUI {
						driveText += " · 装好自动加密直开 ComfyUI"
					}
					m.summaryBullet(c, pal, pal.Emerald, "模型与成片："+driveText)
				} else if acc.DeployRecipe == core.RecipeLLMProxy {
					m.summaryBullet(c, pal, pal.Sky, "编程接入：开机自动起大模型接口（:18080），已选的编程工具会自动同步")
				} else {
					m.summaryBullet(c, pal, pal.TextMuted, "纯净环境：只负责抢卡开机和空闲守护，不自动安装模型脚本")
				}
				shutText := "自动关机已关闭（用完请记得手动停卡，避免扣点）"
				shutCol := pal.Coral
				if acc.AutoShutdownEnabled {
					shutCol = pal.Cyan
					mins := acc.IdleTimeoutMinutes
					if isVideo && acc.StopAfterQueueDrained {
						mins = acc.QueueDrainMinutes
						shutText = fmt.Sprintf("防忘关省点：视频队列全部跑完后空闲 %d 分钟自动停卡，并复查官网确认", mins)
					} else {
						shutText = fmt.Sprintf("防忘关省点：连续空闲 %d 分钟自动停卡，不怕挂一晚白扣点", mins)
					}
				}
				m.summaryBullet(c, pal, shutCol, shutText)
			})

			ui.Column(c).AlignItems(ui.End).Gap(8).Shrink(0).Children(func() {
				cta := "▶  一键开机并运行"
				if acc.DeployRecipe == core.RecipeNone {
					cta = "▶  一键开机"
				}
				disabled := !m.Svc.CLIInstalled || isCooling
				PillButton(c, pal, cta, true, disabled, func() {
					m.Svc.DeployNow(acc.ID)
				})
				optLabel := "调整显卡与选项 ▾"
				if m.ShowHomeOptions {
					optLabel = "收起选项 ▴"
				}
				SmallActionButton(c, pal, optLabel, nil, false, func() {
					m.ShowHomeOptions = !m.ShowHomeOptions
				})
			})
		})
	})

	// 4. Contextual LLM Integrations panel (ONLY visible when user picks LLM Proxy — zero clutter for video users)
	if acc.DeployRecipe == core.RecipeLLMProxy {
		m.renderIntegrationsPanel(c, pal, acc, rt)
	}

	// 5. Optional Collapsible Fine-Tuning Drawer (hidden by default for zero-config beginners)
	if m.ShowHomeOptions {
		m.renderHomeOptionsDrawer(c, pal, acc, rt)
	}
}

func (m *MainUI) selectH3Preset(acc core.AccountProfile, variant core.H3Variant) {
	cp := acc
	cp.DeployRecipe = core.RecipeH3Video
	cp.H3Variant = variant
	cp.WorkloadKind = core.WorkloadVideo
	cp.EnableLLMModule = false
	cp.RemoteServicePort = 8188
	cp.LocalSSHTunnelPort = 8188
	if cp.QueueDrainMinutes < 5 {
		cp.QueueDrainMinutes = 10
	}
	if cp.IdleTimeoutMinutes == 40 {
		cp.IdleTimeoutMinutes = cp.QueueDrainMinutes
	}
	m.Svc.UpdateAccount(cp)
	m.Svc.ClearAccountError(acc.ID)
}

func (m *MainUI) scenarioCard(c *ui.Context, pal Palette, title, badge, line1, line2 string, selected, recommend bool, onClick func()) {
	bg := pal.SubtleBg
	border := pal.SubtleBorder
	if selected {
		bg = WithAlpha(pal.Amber, 0.14)
		border = WithAlpha(pal.Amber, 0.65)
	}
	ui.Column(c).FillWidth().Padding(13).Gap(5).Radius(13).Background(bg).Border(1, border).OnClick(onClick).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Children(func() {
			ui.Text(c, title).FontSize(14).Bold().TextColor(pal.TextPrimary).MaxLines(1).Ellipsis("…")
			ui.Spacer(c)
			var bt *ui.Color
			if selected {
				t := pal.Amber
				bt = &t
			} else if recommend {
				t := pal.Emerald
				bt = &t
			}
			Badge(c, pal, badge, bt)
		})
		ui.Text(c, line1).FontSize(12).FontWeight(500).TextColor(pal.TextPrimary).MaxLines(1).Ellipsis("…")
		ui.Text(c, line2).FontSize(11.5).TextColor(pal.TextMuted).MaxLines(1).Ellipsis("…")
	})
}

func (m *MainUI) modeChip(c *ui.Context, pal Palette, label string, active bool, onClick func()) {
	var tint *ui.Color
	if active {
		t := pal.Amber
		tint = &t
	}
	SmallActionButton(c, pal, label, tint, false, onClick)
}

func (m *MainUI) summaryBullet(c *ui.Context, pal Palette, dotCol ui.Color, text string) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
		ui.Box(c).Width(6).Height(6).Radius(3).Background(dotCol).Shrink(0)
		ui.Text(c, text).FontSize(12.5).TextColor(pal.TextMuted).MaxLines(1).Ellipsis("…")
	})
}

// renderHomeOptionsDrawer renders the clean, consolidated customization drawer (GPU strategy + Drive/ComfyUI + single Shutdown setting).
func (m *MainUI) renderHomeOptionsDrawer(c *ui.Context, pal Palette, acc core.AccountProfile, rt core.AccountRuntimeState) {
	isVideo := !acc.EnableLLMModule && (acc.DeployRecipe.IsVideoComfy() || acc.WorkloadKind == core.WorkloadVideo)

	// Block A: Simplified 3-Option GPU Picker (with optional Advanced Custom expansion)
	SectionCard(c, pal, "显卡怎么挑", core.EffectiveGPUPlanSummary(acc), func() {
		modes := []struct {
			mode core.GPUStrategyMode
			name string
			desc string
			tag  string
		}{
			{core.GPUStrategyAutoSmart, "智能挑卡", "按所选任务自动配最稳顺位，没货自动顺延", "推荐"},
			{core.GPUStrategyEcoSaver, "省点模式", "优先 L4 (24G) / T4，每小时约 2.3 CU 最省钱", "省钱"},
			{core.GPUStrategyCustom, "高级自定义", "自己排多卡顺位、两阶段降级、定时预约", "老手"},
		}
		ui.Grid(c).FillWidth().ColumnTracks(ui.Fr(1), ui.Fr(1), ui.Fr(1)).Gap(10).Children(func() {
			for _, item := range modes {
				it := item
				sel := acc.GPUStrategy == it.mode
				bg := pal.SubtleBg
				border := pal.SubtleBorder
				if sel {
					bg = WithAlpha(pal.Amber, 0.13)
					border = WithAlpha(pal.Amber, 0.60)
				}
				ui.Column(c).FillWidth().Padding(11, 12).Gap(4).Radius(11).Background(bg).Border(1, border).OnClick(func() {
					cp := acc
					cp.GPUStrategy = it.mode
					if it.mode != core.GPUStrategyCustom {
						cp.EnablePoliteQueue = true
					}
					m.Svc.UpdateAccount(cp)
				}).Children(func() {
					ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
						ui.Text(c, it.name).FontSize(13.5).FontWeight(600).TextColor(pal.TextPrimary)
						ui.Spacer(c)
						var bt *ui.Color
						if sel {
							t := pal.Amber
							bt = &t
						}
						Badge(c, pal, it.tag, bt)
					})
					ui.Text(c, it.desc).FontSize(11.5).TextColor(pal.TextMuted).MaxLines(2)
				})
			}
		})

		if acc.GPUStrategy == core.GPUStrategyCustom {
			ui.Box(c).FillWidth().Height(1).Background(pal.Divider)
			m.renderCustomGPUStrategyDetails(c, pal, acc)
		}
	})

	// Block B: Video Drive & ComfyUI Options (only for video recipes)
	if isVideo {
		SectionCard(c, pal, "Google Drive 存片与打开方式", "默认已开齐，一般不用改", func() {
			useDrive := acc.UseGoogleDrive
			ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
				ui.Column(c).Grow(1).Gap(2).Children(func() {
					ui.Text(c, "联动 Google Drive（复用模型 + 成片实时存盘）").FontSize(13.5).FontWeight(600).TextColor(pal.TextPrimary)
					ui.Text(c, "开机先把 Drive 里的模型拷到本地高速盘跑，不卡顿不掉盘；每出一条视频立刻存进 Drive。").FontSize(12).TextColor(pal.TextMuted)
				})
				if ui.Switch(c, &useDrive).Clicked() || useDrive != acc.UseGoogleDrive {
					cp := acc
					cp.UseGoogleDrive = useDrive
					m.Svc.UpdateAccount(cp)
				}
			})

			if acc.UseGoogleDrive {
				cacheBase := acc.CacheBaseToDrive
				ui.Row(c).FillWidth().Padding(8, 12).Radius(10).Background(pal.SubtleBg).AlignItems(ui.Center).Children(func() {
					ui.Column(c).Grow(1).Gap(2).Children(func() {
						ui.Text(c, "首次下载完 21.5GB 底座后，顺手存一份回 Drive").FontSize(13).FontWeight(500).TextColor(pal.TextPrimary)
						ui.Text(c, "下次开机直接从 Drive 拷，切换 H3 原版或 Flash 都不用重下。").FontSize(11.5).TextColor(pal.TextMuted)
					})
					if ui.Switch(c, &cacheBase).Clicked() || cacheBase != acc.CacheBaseToDrive {
						cp := acc
						cp.CacheBaseToDrive = cacheBase
						m.Svc.UpdateAccount(cp)
					}
				})

				ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "成片目录").FontSize(12.5).TextColor(pal.TextMuted).Width(72)
					ui.TextInput(c, &m.DriveOutputDraft).Placeholder("/content/drive/MyDrive/ColabTools/outputs").Grow(1)
					SmallActionButton(c, pal, "保存目录", nil, false, func() {
						cp := acc
						cp.DriveModelsDir = core.SanitizeDriveSubpath(m.DriveModelsDraft, "")
						cp.DriveOutputDir = core.SanitizeDriveSubpath(m.DriveOutputDraft, "/content/drive/MyDrive/ColabTools/outputs")
						m.Svc.UpdateAccount(cp)
						m.Svc.ShowToast("已保存 Google Drive 成片目录")
					})
					SmallActionButton(c, pal, "去 Drive 看成片", &pal.Sky, false, func() {
						m.Svc.OpenDriveOutputs()
					})
				})
			}

			ui.Box(c).FillWidth().Height(1).Background(pal.Divider)

			if acc.DeployRecipe == core.RecipeH3Video {
				solOn := acc.H3EnableSolAttn
				ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
					ui.Column(c).Grow(1).Gap(2).Children(func() {
						ui.Text(c, "开启 sol-attn 稀疏加速（推荐）").FontSize(13.5).FontWeight(500).TextColor(pal.TextPrimary)
						ui.Text(c, "单条视频平均能快约三成，优先安装 Drive/ColabTools/wheels 里现成的预编译包。").FontSize(12).TextColor(pal.TextMuted)
					})
					if ui.Switch(c, &solOn).Clicked() || solOn != acc.H3EnableSolAttn {
						cp := acc
						cp.H3EnableSolAttn = solOn
						m.Svc.UpdateAccount(cp)
					}
				})
				ui.Box(c).FillWidth().Height(1).Background(pal.Divider)
			}

			autoOpen := acc.AutoOpenComfyUI
			ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
				ui.Column(c).Grow(1).Gap(2).Children(func() {
					ui.Text(c, "装好后自动通过本机加密通道打开 ComfyUI（推荐）").FontSize(13.5).FontWeight(500).TextColor(pal.TextPrimary)
					ui.Text(c, "直连 127.0.0.1:8188 弹出浏览器，不暴露公网网址、不易黑屏断流。").FontSize(12).TextColor(pal.TextMuted)
				})
				if ui.Switch(c, &autoOpen).Clicked() || autoOpen != acc.AutoOpenComfyUI {
					cp := acc
					cp.AutoOpenComfyUI = autoOpen
					m.Svc.UpdateAccount(cp)
				}
			})

			cfOn := acc.EnableCloudflared
			ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
				ui.Column(c).Grow(1).Gap(2).Children(func() {
					ui.Text(c, "同时生成 cloudflared 跨设备临时链接（可选）").FontSize(13.5).FontWeight(500).TextColor(pal.TextPrimary)
					ui.Text(c, "方便在手机或另一台电脑看进度（临时网址无密码，请勿外传）。").FontSize(12).TextColor(pal.TextMuted)
				})
				if ui.Switch(c, &cfOn).Clicked() || cfOn != acc.EnableCloudflared {
					cp := acc
					cp.EnableCloudflared = cfOn
					m.Svc.UpdateAccount(cp)
				}
			})
		})
	}

	// Block C: Single Unified Auto-Shutdown Setting (eliminates 3 conflicting places across the app)
	SectionCard(c, pal, "跑完自动关机省点（全应用统一设置）", acc.DisplayName, func() {
		autoShut := acc.AutoShutdownEnabled
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				ui.Text(c, "任务空闲后自动停卡止损").FontSize(13.5).FontWeight(600).TextColor(pal.TextPrimary)
				ui.Text(c, "下载大模型或合成音视频时绝不误关；队列清空且持续空闲后自动停机并复查官网。").FontSize(12).TextColor(pal.TextMuted)
			})
			if autoShut {
				presets := []int{15, 40, 60}
				curMins := acc.IdleTimeoutMinutes
				if isVideo {
					presets = []int{10, 15, 30}
					if acc.StopAfterQueueDrained {
						curMins = acc.QueueDrainMinutes
					}
				}
				for _, mins := range presets {
					mv := mins
					var mt *ui.Color
					if curMins == mv {
						t := pal.Emerald
						mt = &t
					}
					SmallActionButton(c, pal, fmt.Sprintf("%d分钟", mv), mt, false, func() {
						cp := acc
						cp.AutoShutdownEnabled = true
						cp.IdleTimeoutMinutes = mv
						if isVideo {
							cp.StopAfterQueueDrained = true
							cp.QueueDrainMinutes = mv
						}
						m.Svc.UpdateAccount(cp)
					})
				}
			}
			if ui.Switch(c, &autoShut).Clicked() || autoShut != acc.AutoShutdownEnabled {
				cp := acc
				cp.AutoShutdownEnabled = autoShut
				if isVideo {
					cp.StopAfterQueueDrained = autoShut
				}
				m.Svc.UpdateAccount(cp)
			}
		})
	})

	// Allow switching/redeploying quickly when opened from a running dashboard
	if rt.Phase.IsRunning() && acc.DeployRecipe.IsVideoComfy() {
		ui.Row(c).FillWidth().Justify(ui.End).Gap(8).Children(func() {
			SmallActionButton(c, pal, "按新选择立即重新部署", &pal.Amber, !m.Svc.CLIInstalled, func() {
				m.Svc.DeployNow(acc.ID)
			})
		})
	}
}
