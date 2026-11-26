package uiapp

import (
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
	m.DraftAccountID = ""
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
// (Task recipe switcher + GPU strategy + Drive storage or LLM integrations).
func (m *MainUI) renderHomeOptionsDrawer(c *ui.Context, pal Palette, acc core.AccountProfile, rt core.AccountRuntimeState) {
	isVideo := !acc.EnableLLMModule && (acc.DeployRecipe.IsVideoComfy() || acc.WorkloadKind == core.WorkloadVideo)

	// Top Block: Layered Task Picker (4 primary tasks + compact row for advanced recipes)
	activeRecipeLabel := acc.DeployRecipe.DisplayName()
	if acc.DeployRecipe == core.RecipeH3Video {
		activeRecipeLabel = acc.H3Variant.DisplayName()
	}
	SectionCard(c, pal, "任务玩法", activeRecipeLabel, func() {
		ui.Grid(c).FillWidth().ColumnTracks(ui.Fr(1), ui.Fr(1), ui.Fr(1), ui.Fr(1)).Gap(8).Children(func() {
			m.compactOverlayChip(c, pal, "H3 Flash 极速版", acc.DeployRecipe == core.RecipeH3Video && acc.H3Variant == core.H3VariantFlash, func() {
				m.selectH3Preset(acc, core.H3VariantFlash)
			})
			m.compactOverlayChip(c, pal, "H3 原版 (31B)", acc.DeployRecipe == core.RecipeH3Video && acc.H3Variant == core.H3VariantOriginal, func() {
				m.selectH3Preset(acc, core.H3VariantOriginal)
			})
			m.compactOverlayChip(c, pal, "轻量 GGUF", acc.DeployRecipe == core.RecipeH3Video && acc.H3Variant == core.H3VariantFlashGGUF, func() {
				m.selectH3Preset(acc, core.H3VariantFlashGGUF)
			})
			m.compactOverlayChip(c, pal, "大模型代理", acc.DeployRecipe == core.RecipeLLMProxy, func() {
				m.DraftAccountID = ""
				m.Svc.ApplyDeployRecipe(acc.ID, core.RecipeLLMProxy)
			})
		})

		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "其他进阶玩法：").FontSize(11.5).TextColor(pal.TextMuted).Shrink(0)
			m.compactOverlayChip(c, pal, "自定义 HF / 网盘模型", acc.DeployRecipe == core.RecipeH3Video && acc.H3Variant == core.H3VariantCustom, func() {
				m.selectH3Preset(acc, core.H3VariantCustom)
			})
			m.compactOverlayChip(c, pal, "通用 ComfyUI", acc.DeployRecipe == core.RecipeComfyUIGeneral, func() {
				m.DraftAccountID = ""
				m.Svc.ApplyDeployRecipe(acc.ID, core.RecipeComfyUIGeneral)
			})
			m.compactOverlayChip(c, pal, "纯开机", acc.DeployRecipe == core.RecipeNone, func() {
				m.DraftAccountID = ""
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

	// Block B: Single Google Drive Toggle for Video or LLM Coding Tool Integrations
	if acc.DeployRecipe == core.RecipeLLMProxy {
		m.renderIntegrationsPanel(c, pal, acc, rt)
	}
	if isVideo {
		SectionCard(c, pal, "视频存储", "已自动启用本机加密直连与加速", func() {
			useDrive := acc.UseGoogleDrive
			ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
				ui.Column(c).Grow(1).Gap(2).Children(func() {
					ui.Text(c, "成片自动存进 Google Drive").FontSize(13.5).FontWeight(600).TextColor(pal.TextPrimary)
					ui.Text(c, "开启后自动缓存底座模型并实时保存成片，自定义目录可在「设置 → 高级选项」调整。").FontSize(12).TextColor(pal.TextMuted)
				})
				if ui.Switch(c, &useDrive).Clicked() || useDrive != acc.UseGoogleDrive {
					cp := acc
					cp.UseGoogleDrive = useDrive
					if useDrive {
						cp.CacheBaseToDrive = true
					}
					m.Svc.UpdateAccount(cp)
				}
			})
		})
	}

	// Allow switching/redeploying quickly when opened from a running dashboard
	if rt.Phase.IsRunning() && acc.DeployRecipe.IsVideoComfy() {
		cliReady := m.Svc.ViewState().CLIInstalled
		ui.Row(c).FillWidth().Justify(ui.End).Gap(8).Children(func() {
			SmallActionButton(c, pal, "按新选择立即重新部署", &pal.Amber, !cliReady, func() {
				m.Svc.DeployNow(acc.ID)
			})
		})
	}
}
