package uiapp

import (
	"fmt"
	"strings"

	"github.com/LJMStark/Colab-Tools/internal/core"
	"github.com/egoist/mygo/ui"
)

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

func (m *MainUI) modeChip(c *ui.Context, pal Palette, label string, active bool, onClick func()) {
	var tint *ui.Color
	if active {
		t := pal.Amber
		tint = &t
	}
	SmallActionButton(c, pal, label, tint, false, onClick)
}

func (m *MainUI) compactOverlayChip(c *ui.Context, pal Palette, label string, active bool, onClick func()) {
	bg := pal.SubtleBg
	border := pal.SubtleBorder
	fg := pal.TextPrimary
	fw := 500
	if active {
		bg = WithAlpha(pal.Amber, 0.16)
		border = WithAlpha(pal.Amber, 0.50)
		fg = pal.Amber
		fw = 600
	}
	el := ui.Row(c).Padding(5, 8).Radius(7).Background(bg).Border(1, border).
		AlignItems(ui.Center).Justify(ui.Center).Shrink(0).Cursor(ui.CursorPointer)
	if onClick != nil {
		el.OnClick(onClick)
	}
	el.Children(func() {
		ui.Text(c, label).FontSize(11.5).FontWeight(fw).TextColor(fg).MaxLines(1)
	})
}

// renderHomeOptionsDrawer renders the clean, consolidated inspector inside the centered ui.Overlay
// (Extended recipe quick-switcher + GPU strategy + Drive/ComfyUI or LLM integrations + unified Auto-Shutdown).
func (m *MainUI) renderHomeOptionsDrawer(c *ui.Context, pal Palette, acc core.AccountProfile, rt core.AccountRuntimeState) {
	isVideo := !acc.EnableLLMModule && (acc.DeployRecipe.IsVideoComfy() || acc.WorkloadKind == core.WorkloadVideo)

	// Top Block: Extended Mode Quick-Switcher + Contextual Sub-Options
	activeRecipeLabel := acc.DeployRecipe.DisplayName()
	if acc.DeployRecipe == core.RecipeH3Video {
		activeRecipeLabel = acc.H3Variant.DisplayName()
	}
	SectionCard(c, pal, "扩展模式快捷切", activeRecipeLabel, func() {
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			m.compactOverlayChip(c, pal, "H3 Flash", acc.DeployRecipe == core.RecipeH3Video && acc.H3Variant == core.H3VariantFlash, func() {
				m.selectH3Preset(acc, core.H3VariantFlash)
			})
			m.compactOverlayChip(c, pal, "H3 原版", acc.DeployRecipe == core.RecipeH3Video && acc.H3Variant == core.H3VariantOriginal, func() {
				m.selectH3Preset(acc, core.H3VariantOriginal)
			})
			m.compactOverlayChip(c, pal, "轻量 GGUF", acc.DeployRecipe == core.RecipeH3Video && acc.H3Variant == core.H3VariantFlashGGUF, func() {
				m.selectH3Preset(acc, core.H3VariantFlashGGUF)
			})
			m.compactOverlayChip(c, pal, "自定义 HF 或 Drive", acc.DeployRecipe == core.RecipeH3Video && acc.H3Variant == core.H3VariantCustom, func() {
				m.selectH3Preset(acc, core.H3VariantCustom)
			})
			m.compactOverlayChip(c, pal, "通用 ComfyUI", acc.DeployRecipe == core.RecipeComfyUIGeneral, func() {
				m.Svc.ApplyDeployRecipe(acc.ID, core.RecipeComfyUIGeneral)
			})
			m.compactOverlayChip(c, pal, "大模型代理", acc.DeployRecipe == core.RecipeLLMProxy, func() {
				m.Svc.ApplyDeployRecipe(acc.ID, core.RecipeLLMProxy)
			})
			m.compactOverlayChip(c, pal, "只开机", acc.DeployRecipe == core.RecipeNone, func() {
				m.Svc.ApplyDeployRecipe(acc.ID, core.RecipeNone)
			})
		})

		if acc.DeployRecipe == core.RecipeH3Video && acc.H3Variant == core.H3VariantFlashGGUF {
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "GGUF 精度：").FontSize(12).TextColor(pal.TextMuted)
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
					ui.Text(c, "只用我 Google Drive 里的现成模型（不外网下载）").FontSize(12.5).FontWeight(500).TextColor(pal.TextPrimary)
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
					ui.TextInput(c, &m.H3CustomRepoDraft).Placeholder("HuggingFace 仓库，如 owner/repo").Width(220)
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
				borderW := float32(1)
				if sel {
					bg = WithAlpha(pal.Amber, 0.08)
					border = WithAlpha(pal.Amber, 0.72)
					borderW = 1.5
				}
				ui.Column(c).FillWidth().Padding(11, 12).Gap(4).Radius(11).Background(bg).Border(borderW, border).Cursor(ui.CursorPointer).OnClick(func() {
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

		if acc.GPUStrategy == core.GPUStrategyAutoSmart {
			ui.Box(c).FillWidth().Height(1).Background(pal.Divider)
			m.renderSmartStrategyDetails(c, pal, acc)
		} else if acc.GPUStrategy == core.GPUStrategyCustom {
			ui.Box(c).FillWidth().Height(1).Background(pal.Divider)
			m.renderCustomGPUStrategyDetails(c, pal, acc)
		}

		ui.Box(c).FillWidth().Height(1).Background(pal.Divider)
		m.renderPersistentQueueSection(c, pal, acc)
	})

	// Block B: Video Drive & ComfyUI Options or LLM Coding Tool Integrations
	if acc.DeployRecipe == core.RecipeLLMProxy {
		m.renderIntegrationsPanel(c, pal, acc, rt)
	}
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
