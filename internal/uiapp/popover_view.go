package uiapp

import (
	"fmt"
	"math"

	"github.com/LJMStark/Colab-Tools/internal/app"
	"github.com/LJMStark/Colab-Tools/internal/core"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// PopoverUI renders the compact 420pt menu-bar / system-tray companion HUD.
type PopoverUI struct {
	Svc          *app.Service
	ForceTheme   string
	OverviewMode bool
	OnOpenMain   func(sec app.MainSection)
	OnQuit       func()
}

func NewPopoverUI(svc *app.Service) *PopoverUI {
	return &PopoverUI{Svc: svc}
}

func (p *PopoverUI) isDark() bool {
	if p.ForceTheme == "dark" {
		return true
	}
	if p.ForceTheme == "light" {
		return false
	}
	switch p.Svc.Settings.ThemePreference {
	case "dark":
		return true
	case "light":
		return false
	default:
		if mygo.App.IsReady() {
			return mygo.Theme.IsDark()
		}
		return true
	}
}

func (p *PopoverUI) View(c *ui.Context) {
	dark := p.isDark()
	if dark {
		c.SetTheme(ui.DarkTheme())
	} else {
		c.SetTheme(ui.LightTheme())
	}
	pal := PaletteFor(dark)

	ui.Column(c).Fill().LinearGradient(ui.LinearGradient{
		From:  pal.BgTop,
		To:    pal.BgBottom,
		Angle: 180,
	}).Children(func() {
		p.renderHeader(c, pal)
		ui.Box(c).FillWidth().Height(1).Background(pal.Divider)

		accounts := p.Svc.AccountsSnapshot()
		if len(accounts) >= 2 {
			p.renderAccountStrip(c, pal, accounts)
			ui.Box(c).FillWidth().Height(1).Background(pal.Divider)
		}

		ui.Column(c).FillWidth().Grow(1).Padding(14, 16, 12, 16).Gap(10).Children(func() {
			if len(accounts) == 0 {
				SectionCard(c, pal, "", "", func() {
					ui.Column(c).FillWidth().Gap(10).AlignItems(ui.Center).Padding(16, 8).Children(func() {
						ui.Text(c, "尚未配置 Google Colab 账号").FontSize(14).FontWeight(600).TextColor(pal.TextPrimary)
						PillButton(c, pal, "打开开箱向导", true, false, func() {
							p.Svc.ReopenOnboarding()
							if p.OnOpenMain != nil {
								p.OnOpenMain(app.SectionStatus)
							}
						})
					})
				})
			} else if p.OverviewMode && len(accounts) >= 2 {
				p.renderMultiOverview(c, pal, accounts)
			} else if acc, ok := p.Svc.SelectedAccount(); ok {
				p.renderSingleDetail(c, pal, acc)
			}
		})

		ui.Box(c).FillWidth().Height(1).Background(pal.Divider)
		p.renderFooter(c, pal)
	})
}

func (p *PopoverUI) renderHeader(c *ui.Context, pal Palette) {
	running := p.Svc.RunningAccountCount()
	dotCol := pal.TextFaint
	pill := "待机"
	if running >= 2 {
		dotCol = pal.Emerald
		pill = fmt.Sprintf("%d 运行中", running)
	} else if acc, ok := p.Svc.SelectedAccount(); ok {
		rt := p.Svc.StateFor(acc.ID)
		if rt.Phase.IsRunning() {
			dotCol = pal.Emerald
			if acc.EnableLLMModule {
				pill = fmt.Sprintf("%s · %dK", rt.ShortGPUName(), rt.ContextLimit/1024)
			} else if acc.DeployRecipe == core.RecipeH3Video {
				shortV := "H3 Flash"
				if acc.H3Variant == core.H3VariantOriginal {
					shortV = "H3 原版"
				} else if acc.H3Variant == core.H3VariantFlashGGUF {
					shortV = "H3 GGUF"
				} else if acc.H3Variant == core.H3VariantCustom {
					shortV = "H3 自定义"
				}
				pill = fmt.Sprintf("%s · %s", rt.ShortGPUName(), shortV)
			} else {
				pill = fmt.Sprintf("%s · %s", rt.ShortGPUName(), acc.WorkloadKind.DisplayName())
			}
		}
	}

	ui.Row(c).FillWidth().Padding(12, 16).AlignItems(ui.Center).Gap(8).Children(func() {
		if appIconBitmap != nil {
			ui.Image(c, appIconBitmap).Width(20).Height(20).Radius(5).Clip().Shrink(0)
		}
		DrawPrecisionLED(c, dotCol, 12)
		ui.Text(c, "Colab Tools").FontSize(14.5).Bold().TextColor(pal.TextPrimary).MaxLines(1)
		ui.Row(c).Padding(3, 8).Radius(6).Background(pal.RecessedBg).Border(1, pal.RecessedBorder).InsetShadow(0, 1, 1, 0, pal.RecessedInnerShadow).Shrink(0).AlignItems(ui.Center).Children(func() {
			ui.Text(c, pill).FontSize(11).FontWeight(600).FontFeatures("tnum").TextColor(pal.TextMuted).MaxLines(1)
		})
		ui.Spacer(c)
		SmallActionButton(c, pal, "大屏", nil, false, func() {
			if p.OnOpenMain != nil {
				p.OnOpenMain(app.SectionStatus)
			}
		})
		popoverIconButton(c, pal, "refresh", func() {
			go p.Svc.RefreshAllAccounts(true, false)
		})
		popoverIconButton(c, pal, "settings", func() {
			if p.OnOpenMain != nil {
				p.OnOpenMain(app.SectionSettings)
			}
		})
	})
}

func (p *PopoverUI) renderAccountStrip(c *ui.Context, pal Palette, accounts []core.AccountProfile) {
	selAcc, _ := p.Svc.SelectedAccount()
	ui.Row(c).FillWidth().Padding(8, 14).Gap(6).AlignItems(ui.Center).Children(func() {
		var ovTint *ui.Color
		if p.OverviewMode {
			t := pal.Amber
			ovTint = &t
		}
		SmallActionButton(c, pal, "总览", ovTint, false, func() {
			p.OverviewMode = true
		})
		for _, a := range accounts {
			acc := a
			rt := p.Svc.StateFor(acc.ID)
			var tint *ui.Color
			if !p.OverviewMode && acc.ID == selAcc.ID {
				t := pal.Emerald
				tint = &t
			}
			SmallActionButton(c, pal, fmt.Sprintf("%s %.0f", acc.DisplayName, rt.CUBalance), tint, false, func() {
				p.OverviewMode = false
				p.Svc.SelectAccount(acc.ID)
			})
		}
	})
}

func (p *PopoverUI) renderMultiOverview(c *ui.Context, pal Palette, accounts []core.AccountProfile) {
	totalBal, totalRate := p.Svc.TotalBalanceAndRate()
	running := p.Svc.RunningAccountCount()

	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			ui.Text(c, core.FormatCU(totalBal)).FontSize(28).Bold().FontFeatures("tnum").TextColor(pal.TextPrimary)
			ui.Textf(c, "%d 个运行中 · 总计 %.2f CU/hr", running, totalRate).FontSize(12).FontFeatures("tnum").TextColor(pal.TextMuted)
		})
		if p.Svc.TotalManagedRunningSessionCount() > 0 {
			SmallActionButton(c, pal, "⏻ 全停止损", &pal.Coral, false, func() {
				go p.Svc.StopAllManagedSessions()
			})
		}
	})

	SectionCard(c, pal, fmt.Sprintf("多账号并行工作区 (%d)", len(accounts)), "点数 / 控制", func() {
		for idx, a := range accounts {
			if idx > 0 {
				ui.Box(c).FillWidth().Height(1).Background(pal.Divider)
			}
			acc := a
			rt := p.Svc.StateFor(acc.ID)
			dot := AccountTagColor(pal, acc.ColorTag)
			if rt.Phase.IsRunning() {
				dot = pal.Emerald
			}
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				DrawPrecisionLED(c, dot, 11)
				ui.Column(c).Grow(1).Gap(2).OnClick(func() {
					p.OverviewMode = false
					p.Svc.SelectAccount(acc.ID)
				}).Children(func() {
					ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
						ui.Text(c, acc.DisplayName).FontSize(13.5).FontWeight(600).TextColor(pal.TextPrimary).MaxLines(1).Ellipsis("…")
						Badge(c, pal, acc.Tier.ShortBadge(), nil)
					})
					sub := acc.Email
					if rt.Phase.IsRunning() {
						sub = fmt.Sprintf("%s · %.1f/%.0fG", rt.ShortGPUName(), float64(rt.VRAMUsedMB)/1024.0, float64(rt.VRAMTotalMB)/1024.0)
					}
					ui.Text(c, sub).FontSize(12).TextColor(pal.TextMuted).MaxLines(1).Ellipsis("…")
				})
				ui.Text(c, core.FormatCU(rt.CUBalance)).FontSize(13.5).FontWeight(600).FontFeatures("tnum").TextColor(pal.TextPrimary)
				if rt.Phase.IsRunning() {
					SmallActionButton(c, pal, "停", &pal.Coral, false, func() {
						go p.Svc.StopAccountSession(acc.ID, "", "")
					})
				} else {
					SmallActionButton(c, pal, "启", nil, !p.Svc.CLIInstalled, func() {
						p.Svc.StartAccountSession(acc.ID)
					})
				}
			})
		}
	})

	ui.Row(c).FillWidth().Gap(8).Children(func() {
		p.popoverQuickBtn(c, pal, "打开主页", false, false, func() {
			if p.OnOpenMain != nil {
				p.OnOpenMain(app.SectionHome)
			}
		})
		p.popoverQuickBtn(c, pal, "管理账号", false, false, func() {
			if p.OnOpenMain != nil {
				p.OnOpenMain(app.SectionAccounts)
			}
		})
		p.popoverQuickBtn(c, pal, "偏好设置", false, false, func() {
			if p.OnOpenMain != nil {
				p.OnOpenMain(app.SectionSettings)
			}
		})
	})
}

func (p *PopoverUI) popoverQuickBtn(c *ui.Context, pal Palette, label string, active, disabled bool, onClick func()) {
	bg := pal.SubtleBg
	border := pal.SubtleBorder
	fg := pal.TextPrimary
	if active {
		bg = WithAlpha(pal.Amber, 0.16)
		border = WithAlpha(pal.Amber, 0.42)
		fg = pal.Amber
	} else if disabled {
		bg = pal.DisabledBg
		border = pal.DisabledBorder
		fg = pal.TextFaint
	}
	el := ui.Row(c).Grow(1).Padding(7, 8).Radius(8).Background(bg).Border(1, border).AlignItems(ui.Center).Justify(ui.Center)
	if !disabled && onClick != nil {
		el.OnClick(onClick)
	}
	el.Children(func() {
		ui.Text(c, label).FontSize(12).FontWeight(500).TextColor(fg).MaxLines(1)
	})
}

func (p *PopoverUI) renderSingleDetail(c *ui.Context, pal Palette, acc core.AccountProfile) {
	rt := p.Svc.StateFor(acc.ID)

	// Hero row
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.End).Children(func() {
				ui.Text(c, core.FormatCU(rt.CUBalance)).FontSize(28).Bold().FontFeatures("tnum").TextColor(pal.TextPrimary)
				st := "待机中"
				col := pal.TextMuted
				if rt.Phase.IsRunning() {
					st = "运行良好"
					col = pal.Emerald
				}
				ui.Text(c, st).FontSize(13).FontWeight(600).TextColor(col).Padding(0, 0, 4, 0)
			})
			ui.Row(c).Gap(6).Children(func() {
				Badge(c, pal, acc.Tier.ShortBadge(), nil)
				if rt.Phase.IsRunning() {
					Badge(c, pal, rt.ShortGPUName(), nil)
					Badge(c, pal, core.FormatDuration(rt.SessionUptimeSec), nil)
				}
			})
		})
		if rt.Phase.IsRunning() {
			SmallActionButton(c, pal, "⏻ 关机止损", &pal.Coral, false, func() {
				go p.Svc.StopAccountSession(acc.ID, "", "")
			})
		} else if rt.Phase.IsQueuing() {
			SmallActionButton(c, pal, "取消排队", &pal.Coral, false, func() {
				p.Svc.CancelQueue(acc.ID)
			})
		} else {
			popoverLaunchButton(c, pal, "启动", !p.Svc.CLIInstalled, func() {
				p.Svc.DeployNow(acc.ID)
			})
		}
	})

	if rt.LastError != "" && !rt.Phase.IsRunning() {
		ui.Row(c).FillWidth().Padding(8, 10).Radius(9).Background(WithAlpha(pal.Amber, 0.12)).Border(1, WithAlpha(pal.Amber, 0.35)).AlignItems(ui.Center).Gap(6).Children(func() {
			ui.Text(c, rt.LastError).FontSize(11.5).TextColor(pal.TextPrimary).Grow(1).MaxLines(2).Ellipsis("…")
			SmallActionButton(c, pal, "查看", nil, false, func() {
				if p.OnOpenMain != nil {
					p.OnOpenMain(app.SectionHome)
				}
			})
		})
	}

	// 2x2 Compact HUD Cards
	ui.Grid(c).FillWidth().ColumnTracks(ui.Fr(1), ui.Fr(1)).Gap(10).Children(func() {
		BentoCard(c, pal, 126, func() {
			pct := int(math.Round(rt.VRAMRatio() * 100))
			TileHeader(c, pal, pal.Emerald, "显存", fmt.Sprintf("%d%%", pct), nil)
			DrawVRAMSegments(c, pal, rt.VRAMRatio(), pal.Amber, 22)
			ui.Spacer(c)
			ui.Textf(c, "%.1f / %.1f GB", float64(rt.VRAMUsedMB)/1024.0, float64(rt.VRAMTotalMB)/1024.0).FontSize(11.5).FontFeatures("tnum").TextColor(pal.TextMuted)
		})
		BentoCard(c, pal, 126, func() {
			TileHeader(c, pal, pal.Amber, "GPU", rt.ShortGPUName(), nil)
			DrawSparkline(c, pal, rt.GPUUtilHistory, pal.Amber, 26)
			ui.Spacer(c)
			ui.Textf(c, "%d%% · %.2f/h", rt.GPUUtilPercent, rt.CURatePerHour).FontSize(11.5).FontFeatures("tnum").TextColor(pal.TextMuted)
		})
	})

	// Quota + active scheme summary
	hrs, basis, ok := rt.EstimatedHoursRemaining(acc)
	energyRatio := 0.0
	if ok {
		capHours := float64(max(24, acc.Tier.MaxSessionHours()))
		energyRatio = math.Min(1.0, math.Max(0.05, hrs/capHours))
	}
	SectionCard(c, pal, "算力续航与方案", fmt.Sprintf("上限 %dh", acc.Tier.MaxSessionHours()), func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				if ok {
					ui.Textf(c, "%.0f 小时（按 %s）", hrs, basis).FontSize(15.5).Bold().FontFeatures("tnum").TextColor(pal.TextPrimary)
				} else {
					ui.Text(c, acc.Tier.DisplayName()).FontSize(15).Bold().TextColor(pal.TextPrimary)
				}
				ui.Text(c, core.EffectiveGPUPlanSummary(acc)).FontSize(11.5).TextColor(pal.TextMuted).MaxLines(1).Ellipsis("…")
			})
			SmallActionButton(c, pal, "换方案 ›", nil, false, func() {
				if p.OnOpenMain != nil {
					p.OnOpenMain(app.SectionHome)
				}
			})
		})
		drawPopoverAmberEnergyBar(c, pal, energyRatio, !ok)
	})

	// Idle Watchdog + Quick Actions card
	effMins := core.EffectiveIdleTimeoutMinutes(acc, rt)
	remMin := max(0, (rt.RemainingIdleSeconds(effMins)+59)/60)
	guardStatus := "未开启"
	if acc.AutoShutdownEnabled {
		if rt.Phase.IsRunning() {
			guardStatus = fmt.Sprintf("剩余 %d 分钟", remMin)
		} else {
			guardStatus = fmt.Sprintf("%d 分钟自动关机", effMins)
		}
	}
	SectionCard(c, pal, "空闲守护与快捷操作", guardStatus, func() {
		hasTargets := acc.AutoSyncCCSwitch || acc.AutoSyncOpenCode || acc.AutoSyncCodex
		allSynced := hasTargets && (!acc.AutoSyncCCSwitch || rt.CCSwitchSynced) && (!acc.AutoSyncOpenCode || rt.OpenCodeSynced) && (!acc.AutoSyncCodex || rt.CodexSynced)
		isVideo := !acc.EnableLLMModule && (acc.DeployRecipe.IsVideoComfy() || acc.WorkloadKind == core.WorkloadVideo)
		ui.Column(c).FillWidth().Gap(7).Children(func() {
			ui.Row(c).FillWidth().Gap(7).Children(func() {
				if isVideo {
					p.popoverQuickBtn(c, pal, "打开 ComfyUI", rt.Phase.IsRunning() && rt.ServerHealthy, !rt.Phase.IsRunning(), func() {
						go p.Svc.OpenComfyUI(acc.ID)
					})
					p.popoverQuickBtn(c, pal, "Drive 成片", false, false, func() {
						p.Svc.OpenDriveOutputs()
					})
				} else {
					syncLbl := "配置接入"
					if allSynced {
						syncLbl = "✓ 已同步"
					} else if rt.PublicURL != "" {
						syncLbl = "同步工具"
					}
					p.popoverQuickBtn(c, pal, syncLbl, allSynced, false, func() {
						if hasTargets && rt.PublicURL != "" {
							go p.Svc.SyncAllEnabledClients(acc.ID, false)
						} else if p.OnOpenMain != nil {
							p.OnOpenMain(app.SectionHome)
						}
					})
					p.popoverQuickBtn(c, pal, ">_ Claude", false, !rt.Phase.IsRunning() || rt.PublicURL == "", func() {
						p.Svc.LaunchClaudeTerminal(acc.ID)
					})
				}
			})
			ui.Row(c).FillWidth().Gap(7).Children(func() {
				p.popoverQuickBtn(c, pal, "续命", false, !rt.Phase.IsRunning() || !acc.AutoShutdownEnabled, func() {
					p.Svc.ResetIdleTimer(acc.ID)
				})
				sshLbl := "SSH 隧道"
				if rt.SSHTunnelActive {
					sshLbl = "断隧道"
				}
				p.popoverQuickBtn(c, pal, sshLbl, rt.SSHTunnelActive, (!rt.Phase.IsRunning() && !rt.SSHTunnelActive) || rt.IsFreeRuntimeRestricted(acc), func() {
					p.Svc.ToggleSSHTunnel(acc.ID)
				})
				p.popoverQuickBtn(c, pal, "浏览器", false, !rt.Phase.IsRunning(), func() {
					p.Svc.OpenColabInBrowser(acc.ID)
				})
			})
		})
	})
}

func (p *PopoverUI) renderFooter(c *ui.Context, pal Palette) {
	ui.Row(c).FillWidth().Padding(9, 16).AlignItems(ui.Center).Gap(10).Children(func() {
		ui.Text(c, "v"+app.AppVersion).FontSize(11.5).FontFeatures("tnum").TextColor(pal.TextFaint)
		ui.Spacer(c)
		ui.Text(c, "主控制台").FontSize(12).FontWeight(500).TextColor(pal.TextMuted).OnClick(func() {
			if p.OnOpenMain != nil {
				p.OnOpenMain(app.SectionStatus)
			}
		})
		ui.Text(c, "·").FontSize(12).TextColor(pal.TextFaint)
		ui.Text(c, "设置").FontSize(12).FontWeight(500).TextColor(pal.TextMuted).OnClick(func() {
			if p.OnOpenMain != nil {
				p.OnOpenMain(app.SectionSettings)
			}
		})
		ui.Text(c, "·").FontSize(12).TextColor(pal.TextFaint)
		ui.Text(c, "退出").FontSize(12).FontWeight(500).TextColor(pal.Coral).OnClick(func() {
			if p.OnQuit != nil {
				p.OnQuit()
			}
		})
	})
}

func popoverIconButton(c *ui.Context, pal Palette, iconName string, onClick func()) {
	el := ui.Row(c).
		Width(28).
		Height(26).
		Radius(7).
		AlignItems(ui.Center).
		Justify(ui.Center).
		Shrink(0)

	top := pal.SubtleBg
	if el.Hovered() {
		top = pal.CardTop
	}
	el.Background(top).
		Border(1, pal.SubtleBorder).
		InsetShadow(0, 1, 0, 0, pal.CardInnerTopLight)
	if onClick != nil {
		el.Cursor(ui.CursorPointer).OnClick(onClick)
	}
	el.Children(func() {
		ToolbarIcon(c, iconName, 14, pal.TextMuted)
	})
}

func popoverLaunchButton(c *ui.Context, pal Palette, label string, disabled bool, onClick func()) {
	el := ui.Row(c).
		Height(34).
		Padding(0, 15).
		Radius(9).
		Gap(6).
		Shrink(0).
		AlignItems(ui.Center).
		Justify(ui.Center)

	fg := pal.PrimaryBtnFg
	if disabled {
		fg = pal.TextFaint
		el.Background(pal.DisabledBg).Border(1, pal.DisabledBorder)
	} else {
		topCol := pal.PrimaryBtnTop
		botCol := pal.PrimaryBtnBottom
		shadowBlur := float32(12)
		shadowAlpha := float32(0.36)
		if el.Hovered() {
			topCol = topCol.Mix(ui.RGB(255, 255, 255), 0.08)
			botCol = botCol.Mix(ui.RGB(255, 255, 255), 0.06)
			shadowBlur = 16
			shadowAlpha = 0.46
		}
		el.LinearGradient(ui.LinearGradient{From: topCol, To: botCol, Angle: 180}).
			Border(1, ui.Color{}).
			BorderLinearGradient(ui.LinearGradient{From: pal.PrimaryBtnBorderTop, To: pal.PrimaryBtnBorderBottom, Angle: 180}).
			InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.48)).
			Shadow(0, 4, shadowBlur, -2, ui.RGBA(235, 148, 14, shadowAlpha)).
			Cursor(ui.CursorPointer)
		if onClick != nil {
			el.OnClick(onClick)
		}
	}

	el.Children(func() {
		ToolbarIcon(c, "play", 11, fg)
		ui.Text(c, label).FontSize(13).FontWeight(600).TextColor(fg).MaxLines(1)
	})
}

func drawPopoverAmberEnergyBar(c *ui.Context, pal Palette, ratio float64, neutral bool) {
	prog := float32(math.Min(1.0, math.Max(0.0, ratio)))
	ui.Box(c).FillWidth().Height(5).Draw(func(p *ui.Painter, r ui.Rect) {
		rad := r.H / 2
		p.Fill(r, pal.RecessedBg, rad)
		p.Stroke(r, pal.RecessedBorder, rad, 0.8)
		if prog <= 0 {
			return
		}
		w := max(r.H, r.W*prog)
		fillRect := ui.Rect{X: r.X, Y: r.Y, W: w, H: r.H}
		if neutral {
			p.Fill(fillRect, WithAlpha(pal.TextMuted, 0.45), rad)
			return
		}
		p.FillGradient(fillRect, ui.LinearGradient{
			From:  ui.RGB(245, 158, 11),
			To:    ui.RGB(255, 209, 120),
			Angle: 90,
		}, rad)
	})
}
