package uiapp

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/LJMStark/Colab-Tools/internal/app"
	"github.com/LJMStark/Colab-Tools/internal/core"
	"github.com/egoist/mygo/ui"
)

const homeBentoCardHeight float32 = 196

func (m *MainUI) selectH3Preset(acc core.AccountProfile, variant core.H3Variant) {
	m.DraftAccountID = ""
	m.Svc.RequestSelectTaskPreset(acc.ID, core.RecipeH3Video, variant)
}

// Card 1: CU 余额与速率（纯财务视角，无开关机按钮）
func (m *MainUI) bentoBillingCU(c *ui.Context, pal Palette, acc core.AccountProfile, rt core.AccountRuntimeState) {
	webBilling := !rt.Phase.IsRunning() && rt.CURatePerHour > 0
	led := pal.Emerald
	if webBilling {
		led = pal.Amber
	} else if !rt.Phase.IsRunning() {
		led = pal.TextFaint
	}

	pendingBalance := !rt.Phase.IsRunning() && rt.LastUsageFetchAt == nil && rt.CURatePerHour == 0
	balanceFetchFailed := pendingBalance && rt.LastError != ""

	rateText := "0.00 CU/h · 未扣点"
	rateCol := pal.TextMuted
	rateWeight := 500
	footLeft := "账单自动记录"
	footCol := pal.TextMuted

	if rt.Phase.IsRunning() {
		if rt.LastUsageFetchAt != nil || rt.CURatePerHour > 0 {
			rateText = fmt.Sprintf("%.2f CU/h", rt.CURatePerHour)
			rateCol = pal.Emerald
			if rt.CURatePerHour >= 12.0 {
				rateCol = pal.Amber
			}
			rateWeight = 600
		} else if acc.IsFreeOnly() {
			rateText = "免费池 · 0.00 CU/h"
		} else {
			rateText = "速率同步中…"
		}
	} else if webBilling {
		rateText = fmt.Sprintf("网页端扣点 %.2f CU/h", rt.CURatePerHour)
		rateCol = pal.Amber
		rateWeight = 600
		footLeft = "请到 Colab 网页检查是否忘关"
		footCol = pal.Amber
	} else if balanceFetchFailed {
		rateText = "点数读取失败 · 点右上角刷新"
		rateCol = pal.Amber
	} else if pendingBalance {
		rateText = "正在读取点数与运行状态"
	}

	card := PrecisionCard(c, pal, 16, 16, func() {
		TileHeader(c, pal, led, "CU 余额与速率", acc.DisplayTierBadge(), nil)

		ui.Column(c).FillWidth().Gap(4).Children(func() {
			ui.Row(c).AlignItems(ui.End).Gap(5).Children(func() {
				if balanceFetchFailed {
					ui.Text(c, "—").FontSize(28).Bold().TextColor(pal.TextMuted).MaxLines(1)
				} else if pendingBalance {
					ui.Text(c, "读取中…").FontSize(24).Bold().TextColor(pal.TextMuted).MaxLines(1)
				} else {
					ui.Text(c, core.FormatCU(rt.CUBalance)).FontSize(28).Bold().FontFeatures("tnum").TextColor(pal.TextPrimary).MaxLines(1)
					ui.Text(c, "CU").FontSize(13).FontWeight(500).TextColor(pal.TextMuted).Padding(0, 0, 4, 0)
				}
			})
			ui.Text(c, rateText).FontSize(13).FontWeight(rateWeight).FontFeatures("tnum").TextColor(rateCol).MaxLines(1).Ellipsis("…")
		})

		ui.Spacer(c)

		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, footLeft).FontSize(12).TextColor(footCol).Grow(1).MaxLines(1).Ellipsis("…")
			SmallActionButton(c, pal, "账单 ›", nil, false, func() {
				m.Svc.SetSection(app.SectionHistory)
			})
		})
	}).Height(homeBentoCardHeight).Gap(8)
	if rt.Phase.IsRunning() {
		card.Draw(func(p *ui.Painter, r ui.Rect) {
			p.FillRadialGradient(r, ui.RadialGradient{
				Center: ui.Point{X: 0.78, Y: 0.32},
				Radius: max(r.W, r.H) * 0.72,
				Stops: []ui.GradientStop{
					{Color: WithAlpha(pal.Emerald, 0.06), At: 0.0},
					{Color: WithAlpha(pal.Emerald, 0.0), At: 1.0},
				},
			}, 16)
		})
	}
}

// Card 2: 实时负载（简洁两态：未开机 / 开机实时负载）
func (m *MainUI) bentoLiveLoad(c *ui.Context, pal Palette, acc core.AccountProfile, rt core.AccountRuntimeState) {
	if !rt.Phase.IsRunning() {
		BentoCard(c, pal, homeBentoCardHeight, func() {
			TileHeader(c, pal, pal.TextFaint, "实时负载", "", nil)
			ui.Spacer(c)
			ui.Text(c, "未开机").FontSize(26).Bold().TextColor(pal.TextMuted).MaxLines(1)
			ui.Spacer(c)
			ui.Text(c, "开机后实时显示 GPU 负载与显存占用").FontSize(12).TextColor(pal.TextFaint).MaxLines(1).Ellipsis("…")
		})
		return
	}

	vramPct := int(math.Round(rt.VRAMRatio() * 100))
	highLoad := rt.ActivitySignal.IsBusy() || rt.GPUUtilPercent >= 80 || (rt.VRAMTotalMB > 0 && vramPct >= 90)
	badge := "正常"
	eTint := pal.Emerald
	bTint := &eTint
	if highLoad {
		badge = "高负载"
		aTint := pal.Amber
		bTint = &aTint
	}
	barTint := pal.Emerald
	if rt.VRAMTotalMB > 0 && vramPct >= 94 {
		barTint = pal.Amber
	}

	BentoCard(c, pal, homeBentoCardHeight, func() {
		TileHeader(c, pal, pal.Amber, "实时负载", badge, bTint)
		ui.Spacer(c)
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
			ui.Textf(c, "GPU %d%%", rt.GPUUtilPercent).FontSize(13.5).FontWeight(600).FontFeatures("tnum").
				TextColor(pal.TextPrimary).Width(84).Shrink(0).MaxLines(1)
			ui.Column(c).Grow(1).Children(func() {
				DrawSparkline(c, pal, rt.GPUUtilHistory, pal.Amber, 24)
			})
		})
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
			vramLabel := "显存 —"
			if rt.VRAMTotalMB > 0 {
				vramLabel = fmt.Sprintf("显存 %d%%", vramPct)
			}
			ui.Text(c, vramLabel).FontSize(13.5).FontWeight(600).FontFeatures("tnum").
				TextColor(pal.TextPrimary).Width(84).Shrink(0).MaxLines(1)
			ui.Column(c).Grow(1).Children(func() {
				DrawVRAMSegments(c, pal, rt.VRAMRatio(), barTint, 20)
			})
		})
		ui.Spacer(c)
	})
}

// Card 3: 当前任务（主任务 2×2 + 进阶玩法紧凑行，全任务单入口直达）
func (m *MainUI) bentoCurrentTask(c *ui.Context, pal Palette, acc core.AccountProfile, rt core.AccountRuntimeState) {
	taskBadge := "H3 Flash"
	taskFoot := "6 步快出片 · 自动装 ComfyUI"
	badgeTint := pal.Amber
	showDetailBtn := false
	switch acc.DeployRecipe {
	case core.RecipeH3Video:
		switch acc.H3Variant {
		case core.H3VariantOriginal:
			taskBadge = "H3 原版"
			taskFoot = "31B 满血双权重 · 画质最高"
		case core.H3VariantFlashGGUF:
			taskBadge = "轻量 GGUF"
			taskFoot = fmt.Sprintf("24G L4 友好 · %s 精度", acc.H3GGUFQuant)
			showDetailBtn = true
		case core.H3VariantCustom:
			taskBadge = "自定义 H3"
			taskFoot = "自定义 HF 仓库 / 仅用 Drive"
			showDetailBtn = true
		}
	case core.RecipeComfyUIGeneral:
		taskBadge = "ComfyUI"
		taskFoot = "纯净 ComfyUI · 自由装节点"
		badgeTint = pal.Violet
	case core.RecipeLLMProxy:
		taskBadge = "大模型"
		taskFoot = "开机自动同步编程工具"
		badgeTint = pal.Sky
		showDetailBtn = true
	case core.RecipeNone:
		taskBadge = "纯开机"
		taskFoot = "纯净裸机环境 · 仅抢卡与守护"
		badgeTint = pal.TextMuted
	}

	pendingSwitch := rt.HasPendingModeSwitch(acc)
	footCol := pal.TextMuted
	if pendingSwitch {
		taskBadge = "下次开机"
		badgeTint = pal.Amber
		taskFoot = fmt.Sprintf("当前运行：%s · 下次开机生效", rt.EffectiveSessionTaskLabel(acc))
		footCol = pal.Amber
	}

	flashSel := acc.DeployRecipe == core.RecipeH3Video && acc.H3Variant == core.H3VariantFlash
	origSel := acc.DeployRecipe == core.RecipeH3Video && acc.H3Variant == core.H3VariantOriginal
	ggufSel := acc.DeployRecipe == core.RecipeH3Video && acc.H3Variant == core.H3VariantFlashGGUF
	customSel := acc.DeployRecipe == core.RecipeH3Video && acc.H3Variant == core.H3VariantCustom
	generalSel := acc.DeployRecipe == core.RecipeComfyUIGeneral
	llmSel := acc.DeployRecipe == core.RecipeLLMProxy
	noneSel := acc.DeployRecipe == core.RecipeNone

	BentoCard(c, pal, homeBentoCardHeight, func() {
		TileHeader(c, pal, badgeTint, "当前任务", taskBadge, &badgeTint)
		ui.Grid(c).FillWidth().ColumnTracks(ui.Fr(1), ui.Fr(1)).Gap(6).Children(func() {
			ChipToggle(c, pal, "H3 Flash 极速版", flashSel, true, func() {
				m.selectH3Preset(acc, core.H3VariantFlash)
			})
			ChipToggle(c, pal, "H3 原版 (31B)", origSel, true, func() {
				m.selectH3Preset(acc, core.H3VariantOriginal)
			})
			ChipToggle(c, pal, "轻量 GGUF", ggufSel, true, func() {
				m.selectH3Preset(acc, core.H3VariantFlashGGUF)
			})
			ChipToggle(c, pal, "大模型代理", llmSel, true, func() {
				m.DraftAccountID = ""
				m.Svc.RequestSelectTaskPreset(acc.ID, core.RecipeLLMProxy, "")
			})
		})
		ui.Grid(c).FillWidth().ColumnTracks(ui.Fr(1), ui.Fr(1), ui.Fr(1)).Gap(5).Children(func() {
			ChipToggle(c, pal, "自定义 H3", customSel, true, func() {
				m.selectH3Preset(acc, core.H3VariantCustom)
			})
			ChipToggle(c, pal, "通用 ComfyUI", generalSel, true, func() {
				m.DraftAccountID = ""
				m.Svc.RequestSelectTaskPreset(acc.ID, core.RecipeComfyUIGeneral, "")
			})
			ChipToggle(c, pal, "纯开机", noneSel, true, func() {
				m.DraftAccountID = ""
				m.Svc.RequestSelectTaskPreset(acc.ID, core.RecipeNone, "")
			})
		})
		ui.Spacer(c)
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Children(func() {
			ui.Text(c, taskFoot).FontSize(11.5).TextColor(footCol).Grow(1).MaxLines(1).Ellipsis("…")
			if showDetailBtn {
				SmallActionButton(c, pal, "配置 ›", nil, false, func() {
					m.ShowHomeOptions = true
				})
			}
		})
	})
}

// Card 4: 运行与产出（始终锚定当前运行会话，绝不随下次开机偏好串台）
func (m *MainUI) bentoWorkloadOutput(c *ui.Context, pal Palette, acc core.AccountProfile, rt core.AccountRuntimeState) {
	sessionRecipe, _, kind, sessionEnableLLM := rt.EffectiveSessionState(acc)
	if kind == core.WorkloadGeneral && rt.DetectedWorkloadKind != nil {
		kind = *rt.DetectedWorkloadKind
	}
	tint := WorkloadColor(pal, kind)
	isLLM := sessionEnableLLM || sessionRecipe == core.RecipeLLMProxy || kind == core.WorkloadLLM
	isVideo := !sessionEnableLLM && (sessionRecipe.IsVideoComfy() || kind == core.WorkloadVideo || (rt.Phase.IsRunning() && rt.SessionIsVideoComfy))

	BentoCard(c, pal, homeBentoCardHeight, func() {
		if isLLM {
			ctxBadge := fmt.Sprintf("%dK", rt.ContextLimit/1024)
			if rt.Phase.IsRunning() {
				TileHeader(c, pal, pal.Sky, "运行与产出", ctxBadge, nil)
				ui.Row(c).AlignItems(ui.End).Gap(4).Children(func() {
					if rt.ServerHealthy {
						ui.Text(c, core.FormatTokens(rt.PromptTokens)).FontSize(26).Bold().FontFeatures("tnum").TextColor(pal.TextPrimary)
						ui.Text(c, "tok").FontSize(13).FontWeight(500).TextColor(pal.TextMuted).Padding(0, 0, 4, 0)
					} else {
						ui.Text(c, "启动中").FontSize(23).Bold().TextColor(pal.TextPrimary)
						ui.Textf(c, ":%d", acc.RemoteServicePort).FontSize(12.5).FontFeatures("tnum").TextColor(pal.TextMuted).Padding(0, 0, 3, 0)
					}
				})
				prog := 0.0
				if rt.ServerHealthy {
					prog = rt.ContextRatio()
				}
				DrawHorizonBar(c, pal, prog, pal.Sky, 7)
			} else {
				TileHeader(c, pal, pal.Sky, "大模型代理", fmt.Sprintf(":%d", acc.RemoteServicePort), nil)
				ui.Row(c).AlignItems(ui.End).Gap(5).Children(func() {
					ui.Text(c, "待开机").FontSize(23).Bold().TextColor(pal.TextMuted)
					ui.Text(c, ctxBadge).FontSize(12.5).FontFeatures("tnum").TextColor(pal.TextFaint).Padding(0, 0, 3, 0)
				})
				DrawHorizonBar(c, pal, 0.0, WithAlpha(pal.Sky, 0.50), 7)
			}

			ui.Spacer(c)

			hasTargets := acc.AutoSyncCCSwitch || acc.AutoSyncOpenCode || acc.AutoSyncCodex
			allSynced := hasTargets && (!acc.AutoSyncCCSwitch || rt.CCSwitchSynced) && (!acc.AutoSyncOpenCode || rt.OpenCodeSynced) && (!acc.AutoSyncCodex || rt.CodexSynced)
			syncLabel := "同步工具"
			var syncTint *ui.Color
			if allSynced {
				syncLabel = "✓ 已同步"
				a := pal.Amber
				syncTint = &a
			}
			syncDisabled := !rt.Phase.IsRunning() || rt.PublicURL == ""
			ui.Row(c).FillWidth().Gap(7).Children(func() {
				ActionBtn(c, pal, syncLabel, syncTint, "", true, syncDisabled, func() {
					go m.Svc.SyncAllEnabledClients(acc.ID, false)
				})
				ActionBtn(c, pal, ">_ Claude", nil, "", true, !rt.Phase.IsRunning() || rt.PublicURL == "", func() {
					m.Svc.LaunchClaudeTerminal(acc.ID)
				})
			})
			return
		}

		if isVideo {
			title := "视频画布与产出"
			badge := "仅存云电脑"
			var bTint *ui.Color
			outputOnTemp := rt.IsTempOutputActive(acc)
			driveFallbackTemp := rt.Phase.IsRunning() && acc.UseGoogleDrive && rt.DriveMountedKnown && !rt.DriveActuallyMounted
			newCount := rt.SessionNewVideoCount
			if newCount == 0 && len(rt.SessionOutputs) > 0 {
				newCount = len(rt.SessionOutputs)
			}
			hasOutputs := len(rt.SessionOutputs) > 0

			if acc.UseGoogleDrive {
				badge = "Drive 存片"
				e := pal.Emerald
				bTint = &e
			} else {
				a := pal.Amber
				bTint = &a
			}
			if rt.Phase.IsRunning() {
				bTint = nil
				if rt.DeployRunning {
					title = "部署进度"
					badge = "安装中"
					a := pal.Amber
					bTint = &a
				} else if rt.DeployFailed {
					title = "部署中断"
					badge = "失败"
					rCol := pal.Coral
					bTint = &rCol
				} else if outputOnTemp {
					rCol := pal.Coral
					bTint = &rCol
					tempDisp := newCount
					if tempDisp <= 0 && rt.TempOutputVideos > 0 {
						tempDisp = rt.TempOutputVideos
					}
					if tempDisp > 0 {
						badge = fmt.Sprintf("临时盘·%d条(关机清)", tempDisp)
					} else {
						badge = "仅存云电脑(关机清)"
					}
				} else if newCount > 0 {
					badge = fmt.Sprintf("本次已出 %d 条", newCount)
					e := pal.Emerald
					bTint = &e
				} else if rt.ServerHealthy {
					e := pal.Emerald
					bTint = &e
					badge = "Drive 存片"
				} else {
					badge = "未部署"
					a := pal.Amber
					bTint = &a
				}
			}
			TileHeader(c, pal, pal.Violet, title, badge, bTint)

			ui.Row(c).AlignItems(ui.End).Gap(6).Children(func() {
				if rt.Phase.IsRunning() {
					if rt.DeployRunning {
						val := rt.WorkloadMetricValue
						unit := rt.WorkloadMetricUnit
						if val == "" {
							val = "准备中"
						}
						ui.Text(c, val).FontSize(24).Bold().FontFeatures("tnum").TextColor(pal.TextPrimary).MaxLines(1)
						if unit != "" {
							ui.Text(c, unit).FontSize(12.5).FontWeight(500).TextColor(pal.TextMuted).Padding(0, 0, 3, 0).MaxLines(1).Ellipsis("…")
						}
					} else if rt.DeployFailed {
						ui.Text(c, "部署失败").FontSize(23).Bold().TextColor(pal.Coral).MaxLines(1)
					} else if rt.QueueRunning > 0 || rt.QueuePending > 0 {
						ui.Textf(c, "渲染 %d", rt.QueueRunning).FontSize(24).Bold().FontFeatures("tnum").TextColor(pal.TextPrimary).MaxLines(1)
						ui.Textf(c, "排队 %d", rt.QueuePending).FontSize(13).FontWeight(500).TextColor(pal.Amber).Padding(0, 0, 3, 0).MaxLines(1)
					} else if hasOutputs {
						ui.Textf(c, "新出 %d 条", max(1, newCount)).FontSize(22).Bold().FontFeatures("tnum").TextColor(pal.TextPrimary).MaxLines(1)
						locNote := "已存网盘"
						locCol := pal.Emerald
						if !rt.SessionOutputs[0].SavedToDrive || outputOnTemp {
							locNote = "仅存临时盘"
							locCol = pal.Coral
						}
						ui.Text(c, locNote).FontSize(12).FontWeight(600).TextColor(locCol).Padding(0, 0, 3, 0).MaxLines(1)
					} else if rt.ServerHealthy {
						ui.Text(c, "已就绪").FontSize(23).Bold().TextColor(pal.Emerald).MaxLines(1)
						ui.Text(c, ":8188").FontSize(13).FontFeatures("tnum").TextColor(pal.TextMuted).Padding(0, 0, 3, 0).MaxLines(1)
					} else {
						ui.Text(c, "未部署").FontSize(23).Bold().TextColor(pal.Amber).MaxLines(1)
						ui.Text(c, "裸机").FontSize(12.5).FontWeight(500).TextColor(pal.TextMuted).Padding(0, 0, 3, 0).MaxLines(1)
					}
				} else {
					subNote := "仅存云电脑（关机清）"
					if acc.UseGoogleDrive {
						subNote = "成片直存 Drive"
					}
					ui.Text(c, "待开机").FontSize(23).Bold().TextColor(pal.TextMuted).MaxLines(1)
					ui.Text(c, subNote).FontSize(12).FontWeight(500).TextColor(pal.TextFaint).Padding(0, 0, 3, 0).MaxLines(1).Ellipsis("…")
				}
			})

			if rt.Phase.IsRunning() && rt.ServerHealthy && !rt.DeployRunning && hasOutputs {
				ui.Text(c, "最新："+rt.SessionOutputs[0].Filename).FontSize(11.5).TextColor(pal.TextMuted).MaxLines(1).Ellipsis("…")
			} else {
				prog := 0.0
				barCol := WithAlpha(pal.Violet, 0.85)
				if rt.Phase.IsRunning() {
					barCol = pal.Violet
					if rt.DeployRunning {
						barCol = pal.Amber
						if rt.WorkloadMetricRatio != nil {
							prog = *rt.WorkloadMetricRatio
						} else {
							prog = 0.08
						}
					} else if rt.DeployFailed {
						barCol = pal.Coral
						prog = 0.0
					} else if rt.WorkloadMetricRatio != nil && rt.ServerHealthy {
						prog = *rt.WorkloadMetricRatio
					} else if rt.QueueRunning > 0 {
						prog = 0.72
					} else if rt.ServerHealthy {
						barCol = pal.Emerald
						prog = 1.0
					}
				}
				DrawHorizonBar(c, pal, prog, barCol, 7)
			}

			ui.Spacer(c)

			amber := pal.Amber
			ui.Row(c).FillWidth().Gap(6).Children(func() {
				if rt.Phase.IsRunning() && rt.ServerHealthy {
					comfyLabel := "开 ComfyUI"
					var comfyTint *ui.Color
					if rt.ComfyOpening {
						comfyLabel = "连接中…"
					} else {
						comfyTint = &amber
					}
					if hasOutputs {
						ActionBtn(c, pal, "画布", comfyTint, "", false, rt.ComfyOpening, func() {
							go m.Svc.OpenComfyUI(acc.ID)
						})
						ActionBtn(c, pal, "预览", nil, "", true, false, func() {
							go m.Svc.PreviewLatestSessionVideo(acc.ID)
						})
						dlLbl := "下载"
						if rt.DownloadingOutput {
							dlLbl = "下载中…"
						}
						ActionBtn(c, pal, dlLbl, &amber, "", true, rt.DownloadingOutput, func() {
							go m.Svc.DownloadLatestSessionVideo(acc.ID)
						})
						if driveFallbackTemp {
							ActionBtn(c, pal, "重挂", nil, "", false, false, func() {
								go m.Svc.RemountDriveNow(acc.ID)
							})
						} else if rt.SessionOutputs[0].SavedToDrive {
							ActionBtn(c, pal, "网盘", nil, "", false, false, func() {
								m.Svc.OpenDriveOutputs(acc.ID)
							})
						}
					} else {
						ActionBtn(c, pal, comfyLabel, comfyTint, "", true, rt.ComfyOpening, func() {
							go m.Svc.OpenComfyUI(acc.ID)
						})
						if driveFallbackTemp {
							ActionBtn(c, pal, "重挂 Drive", &amber, "", true, false, func() {
								go m.Svc.RemountDriveNow(acc.ID)
							})
						} else {
							ActionBtn(c, pal, "Drive 成片", nil, "", true, false, func() {
								m.Svc.OpenDriveOutputs(acc.ID)
							})
						}
						var logTint *ui.Color
						if m.ShowDeployLog {
							logTint = &amber
						}
						ActionBtn(c, pal, "后台", logTint, "", false, false, func() {
							m.openDeployLogOverlay(acc.ID)
						})
					}
				} else if rt.Phase.IsRunning() && rt.DeployRunning {
					ActionBtn(c, pal, "查看日志", &amber, "", true, false, func() {
						m.openDeployLogOverlay(acc.ID)
					})
					ActionBtn(c, pal, "刷新进度", nil, "", true, false, func() {
						m.Svc.RefreshAccountTelemetryNow(acc.ID)
					})
				} else if rt.Phase.IsRunning() && rt.DeployFailed {
					ActionBtn(c, pal, "重新部署", &amber, "", true, false, func() {
						m.Svc.DeployNow(acc.ID)
					})
					ActionBtn(c, pal, "查看日志", nil, "", true, false, func() {
						m.openDeployLogOverlay(acc.ID)
					})
				} else if rt.Phase.IsRunning() {
					ActionBtn(c, pal, "立即部署", &amber, "", true, false, func() {
						m.Svc.DeployNow(acc.ID)
					})
					ActionBtn(c, pal, "查看日志", nil, "", true, false, func() {
						m.openDeployLogOverlay(acc.ID)
					})
				} else {
					ActionBtn(c, pal, "开 ComfyUI", nil, "", true, true, nil)
					ActionBtn(c, pal, "Drive 成片", nil, "", true, false, func() {
						m.Svc.OpenDriveOutputs(acc.ID)
					})
				}
			})
			return
		}

		// 纯开机 / 通用 WebUI
		port := acc.RemoteServicePort
		if rt.DetectedServicePort != nil {
			port = *rt.DetectedServicePort
		}
		TileHeader(c, pal, tint, "运行与产出", fmt.Sprintf(":%d", port), nil)
		ui.Row(c).AlignItems(ui.End).Gap(5).Children(func() {
			if rt.Phase.IsRunning() {
				v := rt.WorkloadMetricValue
				u := rt.WorkloadMetricUnit
				if v == "" {
					v = fmt.Sprintf("%d", rt.CPUUtilPercent)
					u = "% CPU"
				}
				ui.Text(c, v).FontSize(26).Bold().FontFeatures("tnum").TextColor(pal.TextPrimary).MaxLines(1)
				ui.Text(c, u).FontSize(12.5).FontWeight(500).TextColor(pal.TextMuted).Padding(0, 0, 3, 0).MaxLines(1)
			} else {
				ui.Text(c, "待开机").FontSize(23).Bold().TextColor(pal.TextMuted).MaxLines(1)
				ui.Text(c, "纯算力").FontSize(12.5).FontWeight(500).TextColor(pal.TextFaint).Padding(0, 0, 3, 0).MaxLines(1)
			}
		})
		prog := 0.0
		if rt.Phase.IsRunning() {
			if rt.WorkloadMetricRatio != nil {
				prog = *rt.WorkloadMetricRatio
			} else {
				prog = float64(rt.CPUUtilPercent) / 100.0
			}
		}
		DrawHorizonBar(c, pal, prog, tint, 7)

		ui.Spacer(c)

		amber := pal.Amber
		var openTint, logTint *ui.Color
		if rt.Phase.IsRunning() {
			openTint = &amber
		}
		if m.ShowDeployLog {
			logTint = &amber
		}
		ui.Row(c).FillWidth().Gap(7).Children(func() {
			ActionBtn(c, pal, acc.WorkloadKind.WebUIButtonTitle(), openTint, "", true, !rt.Phase.IsRunning(), func() {
				go m.Svc.OpenWorkloadWebUI(acc.ID)
			})
			ActionBtn(c, pal, "查看日志", logTint, "", true, false, func() {
				m.openDeployLogOverlay(acc.ID)
			})
		})
	})
}

// Card 5: 空闲自动关机（全站唯一空闲关机控制点，统一 10m / 15m / 40m）
func (m *MainUI) bentoIdleWatchdog(c *ui.Context, pal Palette, acc core.AccountProfile, rt core.AccountRuntimeState) {
	wd := m.Svc.WatchdogStatusFor(acc)
	effectiveMins := rt.EffectiveSessionIdleTimeout(acc)
	remMin := max(0, (rt.RemainingIdleSeconds(effectiveMins)+59)/60)
	remGraceSec := rt.RemainingTempGuardGraceSeconds(time.Now())
	remGraceMin := max(1, (remGraceSec+59)/60)
	inTempGrace := rt.Phase.IsRunning() && rt.WatchdogDeferredForTempOutputs && remGraceSec > 0
	enabled := acc.AutoShutdownEnabled

	BentoCard(c, pal, homeBentoCardHeight, func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(7).Children(func() {
			dot := pal.TextFaint
			if inTempGrace {
				dot = pal.Amber
			} else if enabled {
				dot = pal.Cyan
			}
			DrawPrecisionLED(c, dot, 12)
			ui.Text(c, "空闲自动关机").FontSize(13).FontWeight(500).TextColor(pal.TextMuted)
			ui.Spacer(c)
			if ui.Switch(c, &enabled).Clicked() || enabled != acc.AutoShutdownEnabled {
				cp := acc
				cp.AutoShutdownEnabled = enabled
				m.Svc.UpdateAccount(cp)
			}
		})

		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			if !acc.AutoShutdownEnabled {
				ui.Text(c, "OFF").FontSize(26).Bold().TextColor(pal.TextMuted)
			} else if !rt.Phase.IsRunning() {
				ui.Row(c).AlignItems(ui.End).Gap(4).Children(func() {
					ui.Textf(c, "%d", acc.IdleTimeoutMinutes).FontSize(26).Bold().FontFeatures("tnum").TextColor(pal.TextPrimary)
					ui.Text(c, "min").FontSize(13).FontWeight(500).TextColor(pal.TextMuted).Padding(0, 0, 3, 0)
				})
			} else if inTempGrace {
				ui.Row(c).AlignItems(ui.End).Gap(4).Children(func() {
					ui.Textf(c, "宽限 %d", remGraceMin).FontSize(22).Bold().FontFeatures("tnum").TextColor(pal.Amber)
					ui.Text(c, "min").FontSize(12.5).FontWeight(500).TextColor(pal.Amber).Padding(0, 0, 3, 0)
				})
			} else if wd.Kind == core.WatchdogPausedNoSignal {
				ui.Text(c, "暂停").FontSize(24).Bold().TextColor(pal.Amber)
			} else if rt.ActivitySignal.IsBusy() {
				ui.Text(c, "活跃").FontSize(24).Bold().TextColor(pal.Emerald)
			} else {
				ui.Row(c).AlignItems(ui.End).Gap(4).Children(func() {
					ui.Textf(c, "%d", remMin).FontSize(26).Bold().FontFeatures("tnum").TextColor(pal.TextPrimary)
					ui.Text(c, "min").FontSize(13).FontWeight(500).TextColor(pal.TextMuted).Padding(0, 0, 3, 0)
				})
			}
			ui.Spacer(c)
			if rt.Phase.IsRunning() && acc.AutoShutdownEnabled && wd.Kind != core.WatchdogPausedNoSignal {
				SmallActionButton(c, pal, "续命", nil, false, func() {
					m.Svc.ResetIdleTimer(acc.ID)
				})
			}
		})

		presets := []int{10, 15, 40}
		activeMins := effectiveMins
		ui.Row(c).FillWidth().Gap(6).Children(func() {
			for _, mins := range presets {
				sel := activeMins == mins && acc.AutoShutdownEnabled
				minsVal := mins
				ui.Column(c).Grow(1).Children(func() {
					ChipToggle(c, pal, fmt.Sprintf("%dm", minsVal), sel, true, func() {
						cp := acc
						cp.IdleTimeoutMinutes = minsVal
						cp.AutoShutdownEnabled = true
						m.Svc.UpdateAccount(cp)
					})
				})
			}
		})

		ui.Spacer(c)
		foot := "已关软件停卡 · Google 仍回收久闲机"
		footCol := pal.TextMuted
		if acc.AutoShutdownEnabled {
			if inTempGrace {
				foot = fmt.Sprintf("护片宽限中 · 仍在计费 %.2f CU/h", rt.CURatePerHour)
				footCol = pal.Amber
			} else if rt.Phase.IsRunning() {
				foot = fmt.Sprintf("当前空闲已计时 %s", core.FormatDuration(rt.IdleSeconds))
			} else {
				foot = "闲下来自动关机，不怕忘关烧点"
			}
		}
		ui.Text(c, foot).FontSize(12).TextColor(footCol).MaxLines(1).Ellipsis("…")
	})
}

// Card 6: 挑卡与守候（显卡策略与精细选项入口）
func (m *MainUI) bentoStrategy(c *ui.Context, pal Palette, acc core.AccountProfile) {
	targets := core.EffectiveQueueTargets(acc)
	headline := core.QueueTargetsSummary(targets)
	badge := acc.GPUStrategy.DisplayName()
	t := pal.Amber
	bTint := &t
	fbOn, fbRounds, fbTarget := core.EffectiveFallback(acc)
	BentoCard(c, pal, homeBentoCardHeight, func() {
		TileHeader(c, pal, pal.Amber, "挑卡与守候", badge, bTint)
		ui.Text(c, headline).FontSize(21).Bold().TextColor(pal.TextPrimary).MaxLines(1).Ellipsis("…")
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			hasTag := false
			if fbOn {
				hasTag = true
				Badge(c, pal, fmt.Sprintf("%d轮保底 %s", fbRounds, fbTarget.ShortChipLabel()), nil)
			}
			if acc.QueuePersistentMode {
				hasTag = true
				hrs := acc.QueuePersistentMaxHours
				if hrs <= 0 {
					hrs = 8
				}
				a := pal.Amber
				Badge(c, pal, fmt.Sprintf("守候 %dh", hrs), &a)
			}
			if !hasTag {
				ui.Text(c, "没货自动顺延下一张卡").FontSize(12).TextColor(pal.TextMuted)
			}
		})
		ui.Spacer(c)
		ui.Row(c).FillWidth().Padding(6, 10).Radius(8).Background(pal.SubtleBg).Border(1, pal.SubtleBorder).AlignItems(ui.Center).Cursor(ui.CursorPointer).OnClick(func() {
			m.ShowHomeOptions = !m.ShowHomeOptions
		}).Children(func() {
			ui.Text(c, "调整策略与进阶选项").FontSize(12).FontWeight(500).TextColor(pal.TextPrimary)
			ui.Spacer(c)
			ui.Text(c, "›").FontSize(14).TextColor(pal.TextMuted)
		})
	})
}

// renderHomeOptionsDrawer renders the focused inspector inside the centered ui.Overlay:
// contextual recipe parameters (only when GGUF / Custom H3 / LLM selected) + GPU strategy + Drive toggle.
func (m *MainUI) renderHomeOptionsDrawer(c *ui.Context, pal Palette, acc core.AccountProfile, rt core.AccountRuntimeState) {
	isVideo := !acc.EnableLLMModule && (acc.DeployRecipe.IsVideoComfy() || acc.WorkloadKind == core.WorkloadVideo)

	if acc.DeployRecipe == core.RecipeH3Video && acc.H3Variant == core.H3VariantFlashGGUF {
		SectionCard(c, pal, "轻量 GGUF 精度", acc.H3GGUFQuant, func() {
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				for _, q := range []struct{ val, lbl string }{
					{"Q4_K_M", "Q4_K_M（约 8GB · 推荐）"},
					{"Q6_K", "Q6_K（约 11GB · 更细）"},
					{"Q8_0", "Q8_0（约 15GB · 接近原版）"},
				} {
					qv := q.val
					ChipToggle(c, pal, q.lbl, acc.H3GGUFQuant == qv, false, func() {
						cp := acc
						cp.H3GGUFQuant = qv
						m.Svc.UpdateAccount(cp)
					})
				}
			})
		})
	} else if acc.DeployRecipe == core.RecipeH3Video && acc.H3Variant == core.H3VariantCustom {
		SectionCard(c, pal, "自定义 H3 模型来源", "主页可随时切换任务", func() {
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
		})
	}

	// GPU Strategy & Persistent Queue
	SectionCard(c, pal, "显卡怎么挑", core.EffectiveGPUPlanSummary(acc), func() {
		modes := []struct {
			mode core.GPUStrategyMode
			name string
			desc string
			tag  string
		}{
			{core.GPUStrategyAutoSmart, "智能挑卡", "按所选任务自动配最稳顺位，没货自动顺延", "推荐"},
			{core.GPUStrategyEcoSaver, "省点模式", "优先 L4 (24G) / T4，每小时约 2.3 CU 最省钱", "省钱"},
			{core.GPUStrategyCustom, "高级自定义", "自己排多卡顺位与两阶段保底", "老手"},
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
					m.Svc.UpdateAccount(cp)
				}
			})
		})
	}

	if rt.Phase.IsRunning() && acc.DeployRecipe.IsVideoComfy() {
		cliReady := m.Svc.ViewState().CLIInstalled
		ui.Row(c).FillWidth().Justify(ui.End).Gap(8).Children(func() {
			SmallActionButton(c, pal, "按新选择立即重新部署", &pal.Amber, !cliReady, func() {
				m.Svc.DeployNow(acc.ID)
			})
		})
	}
}

// MARK: - Unified Bottom Table: Accounts & Running Cloud Machines

type runningMachineRow struct {
	account      core.AccountProfile
	session      core.ColabSessionItem
	isPrimary    bool
	isWebManual  bool
	isStopping   bool
	hardwareSpec string
	statusText   string
	vramRatio    float32
	hasVRAM      bool
	rateText     string
	rateAmber    bool
}

func primaryRowStatusAndVRAM(rt core.AccountRuntimeState) (string, float32, bool) {
	if rt.Phase.Kind == core.PhaseStopping {
		return "正在关机…", 0, false
	}
	if rt.VRAMTotalMB > 0 {
		return fmt.Sprintf("显存 %.1f/%.0fG", float64(rt.VRAMUsedMB)/1024.0, float64(rt.VRAMTotalMB)/1024.0), float32(rt.VRAMRatio()), true
	}
	return "运行中", 0, false
}

func formatSessionRateEstimate(acc core.AccountProfile, rt core.AccountRuntimeState, sess core.ColabSessionItem, accRowCount int) string {
	singleConfirmed := accRowCount == 1 && rt.CURatePerHour > 0 && (rt.ActiveAssignments == 0 || rt.ActiveAssignments == 1)
	if singleConfirmed {
		return fmt.Sprintf("%.2f CU/h", rt.CURatePerHour)
	}
	if acc.IsFreeOnly() && rt.CURatePerHour == 0 {
		return "免费池"
	}
	if core.NormalizeHardwareKey(sess.Hardware) != "" {
		return fmt.Sprintf("约 %.2f CU/h", core.EstimatedHourlyCU(sess.Hardware))
	}
	return "费率未知"
}

func (m *MainUI) collectRunningMachineRows(accounts []core.AccountProfile) ([]runningMachineRow, float64, bool) {
	var rows []runningMachineRow
	totalRate := 0.0
	hasRateSample := false
	seenEndpoints := map[string]bool{}
	seenBillingIdent := map[string]bool{}

	for _, acc := range accounts {
		rt := m.Svc.StateFor(acc.ID)
		var accRows []runningMachineRow
		hasPrimaryInSessions := false
		isStoppingPrimary := rt.Phase.Kind == core.PhaseStopping

		for _, sess := range rt.Sessions {
			ep := strings.TrimSpace(sess.Endpoint)
			if ep != "" && seenEndpoints[ep] {
				continue
			}
			isPrimary := !sess.IsOrphan && sess.Name == acc.SessionName
			if isPrimary {
				hasPrimaryInSessions = true
			}
			hw := strings.TrimSpace(sess.Hardware)
			if hw == "" && isPrimary {
				hw = rt.ShortGPUName()
			}
			if hw == "" {
				hw = "GPU"
			}
			shape := strings.TrimSpace(sess.Shape)
			spec := hw
			if shape != "" {
				spec = fmt.Sprintf("%s · %s", hw, shape)
			}

			statusText := "运行中"
			var vramRatio float32
			hasVRAM := false
			stoppingRow := false
			if isPrimary {
				statusText, vramRatio, hasVRAM = primaryRowStatusAndVRAM(rt)
				stoppingRow = isStoppingPrimary
			} else if sess.IsOrphan {
				statusText = "运行中 · 读不到显存"
			}
			if ep != "" {
				seenEndpoints[ep] = true
			}

			accRows = append(accRows, runningMachineRow{
				account:      acc,
				session:      sess,
				isPrimary:    isPrimary,
				isWebManual:  sess.IsOrphan,
				isStopping:   stoppingRow,
				hardwareSpec: spec,
				statusText:   statusText,
				vramRatio:    vramRatio,
				hasVRAM:      hasVRAM,
			})
		}

		if (rt.Phase.IsRunning() || isStoppingPrimary) && !hasPrimaryInSessions {
			ep := strings.TrimSpace(rt.ActiveEndpoint)
			if ep == "" || !seenEndpoints[ep] {
				if ep != "" {
					seenEndpoints[ep] = true
				}
				hw := rt.ShortGPUName()
				if hw == "" {
					hw = "GPU"
				}
				statusText, vramRatio, hasVRAM := primaryRowStatusAndVRAM(rt)
				accRows = append(accRows, runningMachineRow{
					account: acc,
					session: core.ColabSessionItem{
						Name:     acc.SessionName,
						Endpoint: ep,
						Hardware: hw,
					},
					isPrimary:    true,
					isStopping:   isStoppingPrimary,
					hardwareSpec: hw,
					statusText:   statusText,
					vramRatio:    vramRatio,
					hasVRAM:      hasVRAM,
				})
			}
		}

		billingKey := strings.ToLower(strings.TrimSpace(acc.Email))
		if billingKey == "" {
			billingKey = "id:" + acc.ID
		}
		if !seenBillingIdent[billingKey] {
			if rt.LastUsageFetchAt != nil || rt.CURatePerHour > 0 {
				hasRateSample = true
			}
			if rt.CURatePerHour > 0 && (len(accRows) > 0 || rt.ActiveAssignments > 0) {
				totalRate += rt.CURatePerHour
			}
			seenBillingIdent[billingKey] = true
		}

		for i := range accRows {
			r := &accRows[i]
			if r.isStopping {
				r.rateText = "停机中"
			} else {
				r.rateText = formatSessionRateEstimate(acc, rt, r.session, len(accRows))
			}
			r.rateAmber = r.isWebManual
		}
		rows = append(rows, accRows...)
	}
	return rows, totalRate, hasRateSample
}

// renderAccountsAndMachinesTable renders a single unified bottom table on the Home page:
// - Hidden when single-account and 0 running machines.
// - Single-account with running machines: shows running machines (including web-manual ones).
// - Multi-account (2+): shows every bound account in one row with its primary GPU/VRAM/CU/rate,
//   plus any extra/web-manual machines nested right underneath as distinct sub-rows.
func (m *MainUI) renderAccountsAndMachinesTable(c *ui.Context, pal Palette, selected core.AccountProfile) {
	accounts := m.Svc.AccountsSnapshot()
	runningRows, totalRate, hasRateSample := m.collectRunningMachineRows(accounts)
	if len(accounts) < 2 && len(runningRows) == 0 {
		return
	}

	hasWebManual := false
	extraByAcc := map[string][]runningMachineRow{}
	for _, r := range runningRows {
		if r.isWebManual {
			hasWebManual = true
		}
		if !r.isPrimary {
			extraByAcc[r.account.ID] = append(extraByAcc[r.account.ID], r)
		}
	}
	managedCount := m.Svc.TotalManagedRunningSessionCount()

	if len(accounts) < 2 {
		m.renderSingleAccountMachinesTable(c, pal, runningRows, totalRate, hasRateSample, managedCount, hasWebManual)
		return
	}

	cliReady := m.Svc.ViewState().CLIInstalled
	const (
		colHW   float32 = 128
		colVRAM float32 = 148
		colCU   float32 = 82
		colRate float32 = 88
		colAct  float32 = 96
	)

	title := fmt.Sprintf("账号与运行机器 (%d)", len(accounts))
	if len(runningRows) > 0 {
		title = fmt.Sprintf("账号与运行机器 (%d 账号 · %d 台运行中)", len(accounts), len(runningRows))
	}

	PrecisionCard(c, pal, 16, 18, func() {
		ui.Row(c).FillWidth().Gap(10).Padding(0, 6).AlignItems(ui.Center).Children(func() {
			ui.Row(c).Grow(1).MinWidth(220).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, title).FontSize(14).FontWeight(600).TextColor(pal.TextPrimary).MaxLines(1)
				if managedCount >= 2 {
					stopAllLabel := "⏻ 全部关机止损"
					if hasWebManual {
						stopAllLabel = fmt.Sprintf("⏻ 关闭本软件 %d 台", managedCount)
					}
					SmallActionButton(c, pal, stopAllLabel, &pal.Coral, false, func() {
						m.Svc.RequestConfirmStopAll()
					})
				}
			})
			if len(runningRows) > 0 {
				rateSummary := "扣点同步中"
				rateSummaryCol := pal.TextMuted
				if totalRate > 0 {
					rateSummary = fmt.Sprintf("实时总扣点 %.2f CU/h", totalRate)
					if hasWebManual {
						rateSummaryCol = pal.Amber
					} else {
						rateSummaryCol = pal.Emerald
					}
				} else if hasRateSample {
					rateSummary = "实时总扣点 0.00 CU/h"
				}
				ui.Text(c, rateSummary).FontSize(12.5).FontWeight(600).FontFeatures("tnum").TextColor(rateSummaryCol).MaxLines(1)
			}
		})

		ui.Row(c).FillWidth().Gap(10).Padding(2, 6, 0, 6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "账号与任务").FontSize(11.5).FontWeight(500).TextColor(pal.TextFaint).Grow(1).MinWidth(210).MaxLines(1)
			ui.Text(c, "显卡与规格").FontSize(11.5).FontWeight(500).TextColor(pal.TextFaint).Width(colHW).MaxLines(1)
			ui.Text(c, "显存 / 状态").FontSize(11.5).FontWeight(500).TextColor(pal.TextFaint).Width(colVRAM).MaxLines(1)
			ui.Text(c, "CU 余额").FontSize(11.5).FontWeight(500).TextColor(pal.TextFaint).Width(colCU).TextAlign(ui.End).MaxLines(1)
			ui.Text(c, "扣点速率").FontSize(11.5).FontWeight(500).TextColor(pal.TextFaint).Width(colRate).TextAlign(ui.End).MaxLines(1)
			ui.Text(c, "操作").FontSize(11.5).FontWeight(500).TextColor(pal.TextFaint).Width(colAct).TextAlign(ui.End).MaxLines(1)
		})

		ui.Box(c).FillWidth().Height(1).Background(pal.Divider)

		for idx, acc := range accounts {
			if idx > 0 {
				ui.Box(c).FillWidth().Height(1).Background(pal.RowDivider)
			}
			rt := m.Svc.StateFor(acc.ID)
			isSel := acc.ID == selected.ID
			accCopy := acc

			rowEl := ui.Row(c).FillWidth().Gap(10).Padding(8, 8).Radius(9).AlignItems(ui.Center)
			if isSel {
				rowEl.Background(pal.RowSelectedBg).Border(1, pal.RowSelectedBorder)
			}
			rowEl.Children(func() {
				dotCol := AccountTagColor(pal, accCopy.ColorTag)
				if rt.Phase.IsRunning() {
					dotCol = pal.Emerald
				} else if rt.Phase.IsQueuing() {
					dotCol = pal.Amber
				}

				ui.Row(c).Grow(1).MinWidth(210).Gap(10).AlignItems(ui.Center).Cursor(ui.CursorPointer).OnClick(func() {
					m.Svc.SelectAccount(accCopy.ID)
				}).Children(func() {
					DrawPrecisionLED(c, dotCol, 12)
					ui.Column(c).Grow(1).Gap(2).Children(func() {
						ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
							ui.Text(c, accCopy.DisplayName).FontSize(13.5).FontWeight(600).TextColor(pal.TextPrimary).MaxLines(1).Ellipsis("…")
							Badge(c, pal, accCopy.Tier.ShortBadge(), nil)
							if isSel {
								s := pal.Sky
								Badge(c, pal, "焦点", &s)
							}
						})
						sub := "默认官方 CLI 账号"
						if rt.Phase.IsRunning() {
							modelLabel := rt.ShortModelName()
							if !accCopy.EnableLLMModule {
								if rt.WorkloadName != "" {
									modelLabel = rt.WorkloadName
								} else if accCopy.DeployRecipe == core.RecipeH3Video {
									modelLabel = accCopy.H3Variant.DisplayName()
								} else {
									modelLabel = accCopy.WorkloadKind.DisplayName()
								}
							}
							sub = fmt.Sprintf("本软件 · %s", modelLabel)
						} else if accCopy.Email != "" {
							sub = accCopy.Email
						}
						ui.Text(c, sub).FontSize(12).TextColor(pal.TextMuted).MaxLines(1).Ellipsis("…")
					})
				})

				hwStr := "已关闭"
				hwCol := pal.TextFaint
				if rt.Phase.IsRunning() {
					shape := "Std"
					if core.EffectiveUseHighMem(accCopy) && !rt.SessionUsedStandardRAM {
						shape = "High-RAM"
					}
					hwStr = fmt.Sprintf("%s · %s", rt.ShortGPUName(), shape)
					hwCol = pal.TextPrimary
				}
				ui.Text(c, hwStr).FontSize(12.5).FontWeight(500).TextColor(hwCol).Width(colHW).MaxLines(1).Ellipsis("…")

				ui.Row(c).Width(colVRAM).Gap(8).AlignItems(ui.Center).Children(func() {
					if rt.VRAMTotalMB > 0 {
						barTint := pal.Emerald
						if rt.VRAMRatio() >= 0.94 {
							barTint = pal.Amber
						}
						ratio := float32(rt.VRAMRatio())
						ui.Box(c).Width(48).Height(6).Draw(func(p *ui.Painter, rect ui.Rect) {
							p.Fill(rect, pal.SubtleBg, 3)
							p.Fill(ui.Rect{X: rect.X, Y: rect.Y, W: rect.W * ratio, H: rect.H}, barTint, 3)
						})
						ui.Textf(c, "%.1f/%.0fG", float64(rt.VRAMUsedMB)/1024.0, float64(rt.VRAMTotalMB)/1024.0).FontSize(11.5).FontFeatures("tnum").TextColor(pal.TextMuted).MaxLines(1)
					} else {
						st, stCol := AccountDisplayStatus(accCopy, rt, pal)
						ui.Text(c, st).FontSize(12).TextColor(stCol).MaxLines(1)
					}
				})

				ui.Text(c, core.FormatCU(rt.CUBalance)).FontSize(13.5).FontWeight(600).FontFeatures("tnum").TextColor(pal.TextPrimary).Width(colCU).TextAlign(ui.End).MaxLines(1)

				rateText := "0.00/h"
				if rt.CURatePerHour > 0 && (rt.Phase.IsRunning() || len(rt.Sessions) > 0) {
					rateText = fmt.Sprintf("%.2f/h", rt.CURatePerHour)
				} else if rt.LastUsageFetchAt == nil {
					rateText = "—"
				}
				webBilling := !rt.Phase.IsRunning() && len(rt.Sessions) > 0 && rt.CURatePerHour > 0
				rateCol := pal.TextMuted
				rateW := 400
				if webBilling {
					rateCol = pal.Amber
					rateW = 600
				}
				ui.Text(c, rateText).FontSize(12).FontWeight(rateW).FontFeatures("tnum").TextColor(rateCol).Width(colRate).TextAlign(ui.End).MaxLines(1)

				ui.Row(c).Width(colAct).Justify(ui.End).AlignItems(ui.Center).Gap(6).Children(func() {
					if rt.Phase.IsRunning() {
						SmallActionButton(c, pal, "关机", &pal.Coral, false, func() {
							m.Svc.RequestConfirmStop(accCopy.ID)
						})
					} else if rt.Phase.IsQueuing() {
						SmallActionButton(c, pal, "取消", &pal.Coral, false, func() {
							m.Svc.CancelQueue(accCopy.ID)
						})
					} else {
						SmallActionButton(c, pal, "启动", nil, !cliReady, func() {
							m.Svc.DeployNow(accCopy.ID)
						})
					}
				})
			})

			// Nested extra / web-manual machines under this account
			for _, extra := range extraByAcc[accCopy.ID] {
				ex := extra
				subRow := ui.Row(c).FillWidth().Gap(10).Padding(6, 8, 6, 26).Radius(8).AlignItems(ui.Center)
				if ex.isWebManual {
					subRow.Background(WithAlpha(pal.Amber, 0.06)).Border(1, WithAlpha(pal.Amber, 0.24))
				} else {
					subRow.Background(pal.SubtleBg)
				}
				subRow.Children(func() {
					ui.Row(c).Grow(1).MinWidth(192).Gap(7).AlignItems(ui.Center).Children(func() {
						badgeText := "本机其他任务"
						badgeTint := pal.Sky
						label := ex.session.Name
						if ex.isWebManual {
							badgeText = "需去网页关"
							badgeTint = pal.Amber
							label = "网页或其他设备开启"
						}
						DrawPrecisionLED(c, badgeTint, 9)
						Badge(c, pal, badgeText, &badgeTint)
						ui.Text(c, label).FontSize(12).TextColor(pal.TextMuted).Grow(1).MaxLines(1).Ellipsis("…")
					})
					ui.Text(c, ex.hardwareSpec).FontSize(12.5).FontWeight(600).TextColor(pal.TextPrimary).Width(colHW).MaxLines(1).Ellipsis("…")
					ui.Text(c, ex.statusText).FontSize(11.5).TextColor(pal.TextFaint).Width(colVRAM).MaxLines(1).Ellipsis("…")
					ui.Text(c, "—").FontSize(12).TextColor(pal.TextFaint).Width(colCU).TextAlign(ui.End).MaxLines(1)
					rCol := pal.TextMuted
					if ex.rateAmber {
						rCol = pal.Amber
					}
					ui.Text(c, ex.rateText).FontSize(12).FontWeight(600).FontFeatures("tnum").TextColor(rCol).Width(colRate).TextAlign(ui.End).MaxLines(1)
					ui.Row(c).Width(colAct).Justify(ui.End).AlignItems(ui.Center).Children(func() {
						if ex.isWebManual {
							SmallActionButton(c, pal, "关机 ↗", &pal.Amber, false, func() {
								m.Svc.RequestWebStopGuide(ex.account.ID, ex.session, ex.rateText)
							})
						} else {
							SmallActionButton(c, pal, "关机", &pal.Coral, false, func() {
								m.Svc.RequestConfirmStopSession(ex.account.ID, ex.session.Name)
							})
						}
					})
				})
			}
		}
	}).Gap(8)
}

func (m *MainUI) renderSingleAccountMachinesTable(c *ui.Context, pal Palette, rows []runningMachineRow, totalRate float64, hasRateSample bool, managedCount int, hasWebManual bool) {
	const (
		colHW     float32 = 136
		colStatus float32 = 162
		colRate   float32 = 104
		colAct    float32 = 96
	)

	PrecisionCard(c, pal, 16, 18, func() {
		ui.Row(c).FillWidth().Gap(10).Padding(0, 6).AlignItems(ui.Center).Children(func() {
			ui.Row(c).Grow(1).MinWidth(220).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Textf(c, "运行中的云端机器 (%d)", len(rows)).FontSize(14).FontWeight(600).TextColor(pal.TextPrimary).MaxLines(1)
				if managedCount >= 2 {
					stopAllLabel := "⏻ 全部关机止损"
					if hasWebManual {
						stopAllLabel = fmt.Sprintf("⏻ 关闭本软件 %d 台", managedCount)
					}
					SmallActionButton(c, pal, stopAllLabel, &pal.Coral, false, func() {
						m.Svc.RequestConfirmStopAll()
					})
				}
			})
			rateSummary := "扣点同步中"
			rateSummaryCol := pal.TextMuted
			if totalRate > 0 {
				rateSummary = fmt.Sprintf("实时总扣点 %.2f CU/h", totalRate)
				if hasWebManual {
					rateSummaryCol = pal.Amber
				} else {
					rateSummaryCol = pal.Emerald
				}
			} else if hasRateSample {
				rateSummary = "实时总扣点 0.00 CU/h"
			}
			ui.Text(c, rateSummary).FontSize(12.5).FontWeight(600).FontFeatures("tnum").TextColor(rateSummaryCol).MaxLines(1)
		})

		ui.Row(c).FillWidth().Gap(10).Padding(2, 6, 0, 6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "来源与标识").FontSize(11.5).FontWeight(500).TextColor(pal.TextFaint).Grow(1).MinWidth(210).MaxLines(1)
			ui.Text(c, "显卡与规格").FontSize(11.5).FontWeight(500).TextColor(pal.TextFaint).Width(colHW).MaxLines(1)
			ui.Text(c, "运行状态").FontSize(11.5).FontWeight(500).TextColor(pal.TextFaint).Width(colStatus).MaxLines(1)
			ui.Text(c, "扣点速率").FontSize(11.5).FontWeight(500).TextColor(pal.TextFaint).Width(colRate).TextAlign(ui.End).MaxLines(1)
			ui.Text(c, "操作").FontSize(11.5).FontWeight(500).TextColor(pal.TextFaint).Width(colAct).TextAlign(ui.End).MaxLines(1)
		})

		ui.Box(c).FillWidth().Height(1).Background(pal.Divider)

		for idx, item := range rows {
			if idx > 0 {
				ui.Box(c).FillWidth().Height(1).Background(pal.RowDivider)
			}
			rowCopy := item
			rowEl := ui.Row(c).FillWidth().Gap(10).Padding(8, 8).Radius(9).AlignItems(ui.Center)
			if rowCopy.isWebManual {
				rowEl.Background(WithAlpha(pal.Amber, 0.05)).Border(1, WithAlpha(pal.Amber, 0.24))
			}
			rowEl.Children(func() {
				ui.Row(c).Grow(1).MinWidth(210).Gap(8).AlignItems(ui.Center).Children(func() {
					ledCol := pal.Emerald
					badgeText := "本软件"
					badgeTint := pal.Emerald
					label := rowCopy.account.DeployRecipe.DisplayName()
					if rowCopy.isPrimary {
						rowRT := m.Svc.StateFor(rowCopy.account.ID)
						label = rowRT.EffectiveSessionTaskLabel(rowCopy.account)
					} else if !rowCopy.isWebManual {
						label = rowCopy.session.Name
					} else {
						label = "网页或其他设备开启"
					}
					if rowCopy.isStopping {
						ledCol = pal.Coral
						badgeText = "关机中"
						badgeTint = pal.Coral
					} else if rowCopy.isWebManual {
						ledCol = pal.Amber
						badgeText = "需去网页关"
						badgeTint = pal.Amber
					} else if !rowCopy.isPrimary {
						badgeText = "本机其他任务"
						badgeTint = pal.Sky
					}
					DrawPrecisionLED(c, ledCol, 11)
					Badge(c, pal, badgeText, &badgeTint)
					ui.Text(c, label).FontSize(12.5).FontWeight(500).TextColor(pal.TextPrimary).Grow(1).MaxLines(1).Ellipsis("…")
				})

				ui.Text(c, rowCopy.hardwareSpec).FontSize(13).FontWeight(600).TextColor(pal.TextPrimary).Width(colHW).MaxLines(1).Ellipsis("…")

				ui.Row(c).Width(colStatus).Gap(8).AlignItems(ui.Center).Children(func() {
					if rowCopy.hasVRAM {
						barTint := pal.Emerald
						if rowCopy.vramRatio >= 0.94 {
							barTint = pal.Amber
						}
						ratio := rowCopy.vramRatio
						ui.Box(c).Width(46).Height(6).Draw(func(p *ui.Painter, rect ui.Rect) {
							p.Fill(rect, pal.SubtleBg, 3)
							p.Fill(ui.Rect{X: rect.X, Y: rect.Y, W: rect.W * ratio, H: rect.H}, barTint, 3)
						})
						ui.Text(c, rowCopy.statusText).FontSize(12).FontFeatures("tnum").TextColor(pal.TextMuted).MaxLines(1).Ellipsis("…")
					} else {
						stCol := pal.TextMuted
						if rowCopy.isStopping {
							stCol = pal.Coral
						} else if rowCopy.isWebManual {
							stCol = pal.TextFaint
						}
						ui.Text(c, rowCopy.statusText).FontSize(12).TextColor(stCol).MaxLines(1).Ellipsis("…")
					}
				})

				rCol := pal.TextPrimary
				if rowCopy.isStopping {
					rCol = pal.TextMuted
				} else if rowCopy.rateAmber {
					rCol = pal.Amber
				}
				ui.Text(c, rowCopy.rateText).FontSize(12.5).FontWeight(600).FontFeatures("tnum").TextColor(rCol).Width(colRate).TextAlign(ui.End).MaxLines(1)

				ui.Row(c).Width(colAct).Justify(ui.End).AlignItems(ui.Center).Children(func() {
					switch {
					case rowCopy.isStopping:
						SmallActionButton(c, pal, "关机中…", &pal.Coral, true, nil)
					case rowCopy.isWebManual:
						SmallActionButton(c, pal, "关机 ↗", &pal.Amber, false, func() {
							m.Svc.RequestWebStopGuide(rowCopy.account.ID, rowCopy.session, rowCopy.rateText)
						})
					case rowCopy.isPrimary:
						SmallActionButton(c, pal, "关机", &pal.Coral, false, func() {
							m.Svc.RequestConfirmStop(rowCopy.account.ID)
						})
					default:
						SmallActionButton(c, pal, "关机", &pal.Coral, false, func() {
							m.Svc.RequestConfirmStopSession(rowCopy.account.ID, rowCopy.session.Name)
						})
					}
				})
			})
		}
	}).Gap(8)
}
