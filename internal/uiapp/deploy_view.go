package uiapp

import (
	"fmt"
	"strings"

	"github.com/LJMStark/Colab-Tools/internal/app"
	"github.com/LJMStark/Colab-Tools/internal/core"
	"github.com/egoist/mygo/ui"
)

// MARK: - Tab 2: Zero-Hands Video & Model Deployment Workbench (H3 Original / Flash / Custom)

func (m *MainUI) renderDeployPage(c *ui.Context, pal Palette) {
	acc, ok := m.Svc.SelectedAccount()
	if !ok {
		SectionCard(c, pal, "", "", func() {
			ui.Text(c, "请先绑定一个 Google Colab 账号，再选择要一键部署的模型。").FontSize(13.5).TextColor(pal.TextMuted)
		})
		return
	}
	rt := m.Svc.StateFor(acc.ID)

	// Header row
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(12).Children(func() {
		ui.Column(c).Grow(1).Gap(4).Children(func() {
			ui.Text(c, "一键部署模型").FontSize(22).Bold().TextColor(pal.TextPrimary)
			ui.Text(c, "选好要跑的模型，开机后自动把脚本送上去装，不用写代码，也不用手动传文件到网盘。").FontSize(13).TextColor(pal.TextMuted)
		})
		if acc.DeployRecipe.IsVideoComfy() && rt.Phase.IsRunning() {
			SmallActionButton(c, pal, "↻ 重新部署", nil, !m.Svc.CLIInstalled, func() {
				m.Svc.DeployNow(acc.ID)
			})
			PillButton(c, pal, "打开 ComfyUI", true, false, func() {
				go m.Svc.OpenComfyUI(acc.ID)
			})
		} else {
			cta := "▶ 立即开机并部署"
			if acc.DeployRecipe == core.RecipeNone {
				cta = "▶ 立即开机"
			}
			PillButton(c, pal, cta, true, !m.Svc.CLIInstalled || rt.Phase.IsBusy(), func() {
				m.Svc.DeployNow(acc.ID)
			})
		}
	})

	// Step 1: Purpose / Recipe selector (4 clean cards)
	SectionCard(c, pal, "第一步 · 选这台机器拿来干嘛", acc.DisplayName, func() {
		recipes := []struct {
			id   core.DeployRecipe
			name string
			sub  string
			tag  string
		}{
			{core.RecipeH3Video, "H3 视频模型", "有声视频生成 · 自动装好 ComfyUI", "热门"},
			{core.RecipeComfyUIGeneral, "通用 ComfyUI", "自由跑各类视频 / 生图工作流", "画布"},
			{core.RecipeLLMProxy, "大模型代理", "给 Claude / OpenCode / Codex 用", "编程"},
			{core.RecipeNone, "只开机不装", "纯算力机器，自己连上去折腾", "纯净"},
		}
		ui.Grid(c).FillWidth().ColumnTracks(ui.Fr(1), ui.Fr(1), ui.Fr(1), ui.Fr(1)).Gap(10).Children(func() {
			for _, item := range recipes {
				rec := item
				sel := acc.DeployRecipe == rec.id
				bg := pal.SubtleBg
				border := pal.SubtleBorder
				if sel {
					bg = WithAlpha(pal.Amber, 0.13)
					border = WithAlpha(pal.Amber, 0.60)
				}
				ui.Column(c).FillWidth().Padding(12).Gap(5).Radius(12).Background(bg).Border(1, border).OnClick(func() {
					m.Svc.ApplyDeployRecipe(acc.ID, rec.id)
				}).Children(func() {
					ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
						ui.Text(c, rec.name).FontSize(13.5).FontWeight(600).TextColor(pal.TextPrimary).MaxLines(1)
						ui.Spacer(c)
						var t *ui.Color
						if sel {
							a := pal.Amber
							t = &a
						}
						Badge(c, pal, rec.tag, t)
					})
					ui.Text(c, rec.sub).FontSize(11.5).TextColor(pal.TextMuted).MaxLines(2)
				})
			}
		})
	})

	// Step 2: H3 Variant Selector (Original + Flash front and center; others neatly tucked below)
	if acc.DeployRecipe == core.RecipeH3Video {
		SectionCard(c, pal, "第二步 · 选 H3 版本（默认两档，共用 21.5GB 底座不重复下载）", acc.H3Variant.DisplayName(), func() {
			// Primary two-column default choice: Original vs Flash
			ui.Grid(c).FillWidth().ColumnTracks(ui.Fr(1), ui.Fr(1)).Gap(12).Children(func() {
				m.h3VariantCard(c, pal, acc, core.H3VariantOriginal,
					"H3 原版（31B 官方满血）",
					"画质上限最高 · 原版双权重",
					"适合 A100 80G / G4 96G / H100，画质党首选。",
					"画质首选",
				)
				m.h3VariantCard(c, pal, acc, core.H3VariantFlash,
					"H3 Flash 极速版（13.7B）",
					"6 步出片 · 速度快、更省点数",
					"实测出片速度大幅提升，省时省点，日常做片推荐。",
					"默认推荐",
				)
			})

			// Compact switcher for other versions (GGUF / Custom HF / Drive-only)
			otherActive := acc.H3Variant == core.H3VariantFlashGGUF || acc.H3Variant == core.H3VariantCustom
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Padding(2, 0).Children(func() {
				ui.Text(c, "还想换别的版本？").FontSize(12.5).TextColor(pal.TextMuted)
				var ggufTint, custTint *ui.Color
				if acc.H3Variant == core.H3VariantFlashGGUF {
					s := pal.Sky
					ggufTint = &s
				}
				if acc.H3Variant == core.H3VariantCustom {
					s := pal.Sky
					custTint = &s
				}
				SmallActionButton(c, pal, "Flash 轻量 GGUF（24G L4 可跑）", ggufTint, false, func() {
					cp := acc
					cp.H3Variant = core.H3VariantFlashGGUF
					m.Svc.UpdateAccount(cp)
				})
				SmallActionButton(c, pal, "自定义链接 / 只用 Drive 已有模型", custTint, false, func() {
					cp := acc
					cp.H3Variant = core.H3VariantCustom
					m.Svc.UpdateAccount(cp)
				})
				ui.Spacer(c)
				if otherActive {
					Badge(c, pal, "已选扩展版本", &pal.Sky)
				}
			})

			// Detail controls only when user picks GGUF or Custom
			if acc.H3Variant == core.H3VariantFlashGGUF {
				ui.Row(c).FillWidth().Padding(10, 12).Radius(10).Background(pal.SubtleBg).AlignItems(ui.Center).Gap(8).Children(func() {
					ui.Text(c, "GGUF 体积档位：").FontSize(12.5).TextColor(pal.TextPrimary)
					for _, q := range []struct {
						val, lbl string
					}{
						{"Q4_K_M", "Q4_K_M（约 8GB · 推荐）"},
						{"Q6_K", "Q6_K（约 11GB · 画质更细）"},
						{"Q8_0", "Q8_0（约 15GB · 接近原精度）"},
					} {
						qv := q.val
						var qt *ui.Color
						if acc.H3GGUFQuant == qv {
							t := pal.Amber
							qt = &t
						}
						SmallActionButton(c, pal, q.lbl, qt, false, func() {
							cp := acc
							cp.H3GGUFQuant = qv
							m.Svc.UpdateAccount(cp)
						})
					}
				})
			} else if acc.H3Variant == core.H3VariantCustom {
				ui.Column(c).FillWidth().Padding(12).Gap(10).Radius(10).Background(pal.SubtleBg).Children(func() {
					driveOnly := acc.H3DriveOnly
					ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
						ui.Column(c).Grow(1).Gap(2).Children(func() {
							ui.Text(c, "只用我 Google Drive 里的现成模型（不外网下载）").FontSize(13).FontWeight(600).TextColor(pal.TextPrimary)
							ui.Text(c, "把你 Drive 的 diffusion_models 目录直接拷到本地高速盘开跑。").FontSize(12).TextColor(pal.TextMuted)
						})
						if ui.Switch(c, &driveOnly).Clicked() || driveOnly != acc.H3DriveOnly {
							cp := acc
							cp.H3DriveOnly = driveOnly
							m.Svc.UpdateAccount(cp)
						}
					})
					if !acc.H3DriveOnly {
						ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
							ui.TextInput(c, &m.H3CustomRepoDraft).Placeholder("HuggingFace 仓库，如 owner/repo").Width(260)
							ui.TextInput(c, &m.H3CustomFilesDraft).Placeholder("模型文件名（多个用英文逗号隔开）").Grow(1)
							SmallActionButton(c, pal, "保存", &pal.Amber, false, func() {
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
				})
			}

			ui.Box(c).FillWidth().Height(1).Background(pal.Divider)

			// sol-attn speed toggle
			solOn := acc.H3EnableSolAttn
			ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
				ui.Column(c).Grow(1).Gap(2).Children(func() {
					ui.Text(c, "开启 sol-attn 稀疏加速（推荐）").FontSize(13.5).FontWeight(600).TextColor(pal.TextPrimary)
					ui.Text(c, "单条视频平均能快约三成；会优先安装你 Drive/ColabTools/wheels 里现成的预编译包，不用每次现编译。").FontSize(12).TextColor(pal.TextMuted)
				})
				if ui.Switch(c, &solOn).Clicked() || solOn != acc.H3EnableSolAttn {
					cp := acc
					cp.H3EnableSolAttn = solOn
					m.Svc.UpdateAccount(cp)
				}
			})
		})
	}

	// Step 3: Google Drive & ComfyUI open / auto-stop preferences (shown for H3 & General ComfyUI)
	if acc.DeployRecipe.IsVideoComfy() {
		SectionCard(c, pal, "第三步 · Google Drive 存片与打开方式", "不软链直读 · 防掉盘防白跑", func() {
			useDrive := acc.UseGoogleDrive
			ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
				ui.Column(c).Grow(1).Gap(2).Children(func() {
					ui.Text(c, "联动 Google Drive（复用模型 + 每出一条视频立刻存盘）").FontSize(13.5).FontWeight(600).TextColor(pal.TextPrimary)
					ui.Text(c, "开机先把 Drive 里的模型拷到本地高速盘跑，不卡顿不掉盘；每条成片实时落盘 Drive，中途断线也不白跑。关掉也能纯云端试跑。").FontSize(12).TextColor(pal.TextMuted)
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
						ui.Text(c, "首次从网上下载完底座后，顺手存一份回 Drive").FontSize(13).FontWeight(500).TextColor(pal.TextPrimary)
						ui.Text(c, "以后再开机直接从 Drive 拷，换 H3 原版或 Flash 都不用重复下 21.5GB 底座。").FontSize(11.5).TextColor(pal.TextMuted)
					})
					if ui.Switch(c, &cacheBase).Clicked() || cacheBase != acc.CacheBaseToDrive {
						cp := acc
						cp.CacheBaseToDrive = cacheBase
						m.Svc.UpdateAccount(cp)
					}
				})

				ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "成片保存位置").FontSize(12.5).TextColor(pal.TextMuted).Width(92)
					ui.TextInput(c, &m.DriveOutputDraft).Placeholder("/content/drive/MyDrive/ColabTools/outputs").Grow(1)
					SmallActionButton(c, pal, "保存目录", nil, false, func() {
						cp := acc
						cp.DriveModelsDir = core.SanitizeDriveSubpath(m.DriveModelsDraft, "")
						cp.DriveOutputDir = core.SanitizeDriveSubpath(m.DriveOutputDraft, "/content/drive/MyDrive/ColabTools/outputs")
						m.Svc.UpdateAccount(cp)
						m.Svc.ShowToast("已保存 Google Drive 目录设置")
					})
					SmallActionButton(c, pal, "去 Drive 看成片", &pal.Sky, false, func() {
						m.Svc.OpenDriveOutputs()
					})
				})
			}

			ui.Box(c).FillWidth().Height(1).Background(pal.Divider)

			autoOpen := acc.AutoOpenComfyUI
			ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
				ui.Column(c).Grow(1).Gap(2).Children(func() {
					ui.Text(c, "装好后自动通过本机加密通道打开 ComfyUI（推荐）").FontSize(13.5).FontWeight(600).TextColor(pal.TextPrimary)
					ui.Text(c, "自动连到 127.0.0.1:8188 并弹出浏览器，不暴露公网网址，也不会像 Colab 网页那样动不动黑屏断流。").FontSize(12).TextColor(pal.TextMuted)
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
					ui.Text(c, "方便在手机或另一台电脑上看进度。临时网址没有密码，注意不要外传。").FontSize(12).TextColor(pal.TextMuted)
				})
				if ui.Switch(c, &cfOn).Clicked() || cfOn != acc.EnableCloudflared {
					cp := acc
					cp.EnableCloudflared = cfOn
					m.Svc.UpdateAccount(cp)
				}
			})

			ui.Box(c).FillWidth().Height(1).Background(pal.Divider)

			drainOn := acc.StopAfterQueueDrained
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Column(c).Grow(1).Gap(2).Children(func() {
					ui.Text(c, "视频队列全部跑完后，自动关机省点").FontSize(13.5).FontWeight(600).TextColor(pal.TextPrimary)
					ui.Text(c, "不用熬夜等出片：队列清零并连续空闲到设定时间后自动停卡，并再次查官网确认真的停了。").FontSize(12).TextColor(pal.TextMuted)
				})
				if drainOn {
					for _, mins := range []int{10, 15, 30} {
						mv := mins
						var mt *ui.Color
						if acc.QueueDrainMinutes == mv {
							t := pal.Emerald
							mt = &t
						}
						SmallActionButton(c, pal, fmt.Sprintf("%d分钟", mv), mt, false, func() {
							cp := acc
							cp.QueueDrainMinutes = mv
							m.Svc.UpdateAccount(cp)
						})
					}
				}
				if ui.Switch(c, &drainOn).Clicked() || drainOn != acc.StopAfterQueueDrained {
					cp := acc
					cp.StopAfterQueueDrained = drainOn
					m.Svc.UpdateAccount(cp)
				}
			})
		})
	} else if acc.DeployRecipe == core.RecipeLLMProxy {
		SectionCard(c, pal, "大模型代理模式（Claude Code / OpenCode / Codex）", "", func() {
			ui.Text(c, "已切到大模型代理模式（端口 18080）。开机后会自动拉起云端大模型接口并安全同步到你的编程工具。").FontSize(13).TextColor(pal.TextMuted)
			ui.Row(c).Gap(8).Children(func() {
				SmallActionButton(c, pal, "去配置编程工具接入 ›", &pal.Sky, false, func() {
					m.Svc.SetSection(app.SectionIntegrations)
				})
				SmallActionButton(c, pal, "自定义启动脚本路径 ›", nil, false, func() {
					m.SettingsSubTab = SubTabAdvanced
					m.Svc.SetSection(app.SectionSettings)
				})
			})
		})
	} else {
		SectionCard(c, pal, "纯算力开机模式", "", func() {
			ui.Text(c, "这个模式只负责帮你把高配 GPU/TPU 卡抢到手、盯紧点数和空闲自动关机，不会自动往机器里装任何模型。").FontSize(13).TextColor(pal.TextMuted)
		})
	}
}

func (m *MainUI) h3VariantCard(c *ui.Context, pal Palette, acc core.AccountProfile, variant core.H3Variant, title, highlight, desc, badge string) {
	sel := acc.H3Variant == variant
	bg := pal.SubtleBg
	border := pal.SubtleBorder
	if sel {
		bg = WithAlpha(pal.Amber, 0.14)
		border = WithAlpha(pal.Amber, 0.65)
	}
	ui.Column(c).FillWidth().Padding(14).Gap(6).Radius(13).Background(bg).Border(1, border).OnClick(func() {
		cp := acc
		cp.H3Variant = variant
		m.Svc.UpdateAccount(cp)
	}).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, title).FontSize(14.5).Bold().TextColor(pal.TextPrimary).MaxLines(1)
			ui.Spacer(c)
			var bt *ui.Color
			if sel {
				t := pal.Amber
				bt = &t
			} else if variant == core.H3VariantFlash {
				t := pal.Emerald
				bt = &t
			}
			Badge(c, pal, badge, bt)
		})
		ui.Text(c, highlight).FontSize(12.5).FontWeight(600).TextColor(pal.Emerald)
		ui.Text(c, desc).FontSize(12).TextColor(pal.TextMuted).MaxLines(2)
	})
}
