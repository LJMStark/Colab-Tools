package uiapp

import (
	"fmt"
	"math"
	"runtime"
	"strings"
	"time"

	"github.com/LJMStark/Colab-Tools/internal/app"
	"github.com/LJMStark/Colab-Tools/internal/core"
	"github.com/LJMStark/Colab-Tools/internal/integrations"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// MainUI holds transient form/editor state for the desktop console window.
type MainUI struct {
	Svc         *app.Service
	PreviewMode bool
	ForceTheme  string // "", "dark", "light" (used for deterministic screenshot exports)

	SelectedCopyProto    integrations.CopyProtocolKind
	SelectedInsightHW    string
	ShowAccountDropdown  bool
	AgreedRemoteScript   bool
	ShowInstallLogs      bool
	ShowHomeOptions      bool // Centered overlay inspector for recipe, GPU strategy & options
	ShowDeployLog        bool // Centered overlay showing remote deploy & ComfyUI logs
	ShowErrorRaw         bool // Toggle raw CLI snippet in error banner
	ShowAdvancedSettings bool // Collapsible advanced parameters in Settings (collapsed by default)
	EditingTierAccountID string

	// OAuth2 Add-Account form state
	NewAccName      string
	NewAccTierIdx   int
	NewAccAuthURL   string
	NewAccAuthCode  string
	NewAccError     string
	NewAccWorking   bool
	ShowAddAccPanel bool

	// Drafts for editable text fields
	CCSwitchNameDraft  string
	DriveScriptDraft   string
	RemotePortDraft    string
	LocalPortDraft     string
	H3CustomRepoDraft  string
	H3CustomFilesDraft string
	DriveModelsDraft   string
	DriveOutputDraft   string
	DraftAccountID     string
	PortValidationErr  string
}

func timeNow() time.Time { return time.Now() }

func NewMainUI(svc *app.Service) *MainUI {
	return &MainUI{
		Svc:               svc,
		SelectedCopyProto: integrations.ProtocolOpenAI,
		NewAccTierIdx:     3, // Colab Pro default
	}
}

func (m *MainUI) isDark() bool {
	if m.ForceTheme == "dark" {
		return true
	}
	if m.ForceTheme == "light" {
		return false
	}
	switch m.Svc.Settings.ThemePreference {
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

func (m *MainUI) syncDraftsIfNeeded(acc core.AccountProfile) {
	if m.DraftAccountID != acc.ID {
		m.DraftAccountID = acc.ID
		m.CCSwitchNameDraft = acc.CCSwitchName
		m.DriveScriptDraft = acc.DriveScriptPath
		m.RemotePortDraft = fmt.Sprintf("%d", acc.RemoteServicePort)
		m.LocalPortDraft = fmt.Sprintf("%d", acc.LocalSSHTunnelPort)
		m.H3CustomRepoDraft = acc.H3CustomHFRepo
		m.H3CustomFilesDraft = acc.H3CustomHFFiles
		m.DriveModelsDraft = acc.DriveModelsDir
		m.DriveOutputDraft = acc.DriveOutputDir
		m.PortValidationErr = ""
	}
}

// View builds the full Mole-inspired desktop console UI on every frame.
func (m *MainUI) View(c *ui.Context) {
	dark := m.isDark()
	if dark {
		c.SetTheme(ui.DarkTheme())
	} else {
		c.SetTheme(ui.LightTheme())
	}
	pal := PaletteFor(dark)

	if acc, ok := m.Svc.SelectedAccount(); ok {
		m.syncDraftsIfNeeded(acc)
	}

	ui.Column(c).Fill().LinearGradient(ui.LinearGradient{
		From:  pal.BgTop,
		To:    pal.BgBottom,
		Angle: 180,
	}).Draw(func(p *ui.Painter, r ui.Rect) {
		DrawPrecisionBackdrop(p, r, pal)
	}).Children(func() {
		if m.Svc.ShowingOnboarding {
			m.renderOnboarding(c, pal)
		} else {
			m.renderTopCapsuleBar(c, pal)
			ui.Box(c).FillWidth().Height(1).Background(pal.Divider)

			body := func() {
				ui.Column(c).FillWidth().Padding(16, 24, 22, 24).Gap(14).Children(func() {
					switch m.Svc.SelectedSection {
					case app.SectionHome:
						m.renderStatusPage(c, pal)
					case app.SectionAccounts:
						m.centeredWorkbench(c, func() { m.renderAccountsPage(c, pal) })
					case app.SectionSettings:
						m.centeredWorkbench(c, func() { m.renderSettingsPage(c, pal) })
					}
				})
			}

			if m.PreviewMode {
				body()
			} else {
				ui.Scroll(c).FillWidth().Grow(1).Children(body)
			}
		}
	})

	// Centered Inspector Modal Overlay for GPU strategy & run options
	if m.ShowHomeOptions {
		m.renderHomeOptionsOverlay(c, pal)
	}
	if m.ShowDeployLog {
		m.renderDeployLogOverlay(c, pal)
	}

	// Floating bottom-right toast banner (non-blocking PassThrough so clicks underneath always work)
	if toast := m.Svc.ToastMessage; toast != "" {
		ui.Overlay(c, func() {
			ui.Row(c).Absolute().Right(20).Bottom(20).PassThrough().
				Gap(8).Padding(10, 16).Radius(999).
				Background(pal.CardBg).Border(1, pal.SubtleBorder).
				Shadow(0, 4, 14, 0, ui.RGBA(0, 0, 0, 0.25)).
				AlignItems(ui.Center).Children(func() {
				ui.Box(c).Width(8).Height(8).Radius(4).Background(pal.Emerald)
				ui.Text(c, toast).FontSize(13).FontWeight(500).TextColor(pal.TextPrimary).MaxLines(1)
			})
		})
	}
}

func (m *MainUI) renderHomeOptionsOverlay(c *ui.Context, pal Palette) {
	acc, hasAcc := m.Svc.SelectedAccount()
	if !hasAcc {
		return
	}
	rt := m.Svc.StateFor(acc.ID)

	ui.Overlay(c, func() {
		// Full-screen translucent scrim (click outside card to close)
		ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).
			Background(ui.RGBA(0, 0, 0, 0.45)).
			OnClick(func() {
				m.ShowHomeOptions = false
			})

		// PassThrough wrapper centers the PrecisionCard without swallowing outside clicks
		ui.Column(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).
			PassThrough().
			Padding(16, 24).
			AlignItems(ui.Center).
			Justify(ui.Center).
			Children(func() {
				card := PrecisionCard(c, pal, 20, 18, func() {
					ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(12).Children(func() {
						DrawPrecisionLED(c, pal.Amber, 12)
						ui.Text(c, "任务方案与显卡选项").FontSize(16).FontWeight(600).TextColor(pal.TextPrimary)
						ui.Spacer(c)
						SmallActionButton(c, pal, "完成", nil, false, func() {
							m.ShowHomeOptions = false
						})
					})

					ui.Box(c).FillWidth().Height(1).Background(pal.Divider)

					ui.Scroll(c).FillWidth().Grow(1).MaxHeight(592).Children(func() {
						ui.Column(c).FillWidth().Gap(12).Padding(2, 4, 4, 0).Children(func() {
							m.renderHomeOptionsDrawer(c, pal, acc, rt)
						})
					})
				}).Width(780).MaxHeight(676).Gap(12).
					Shadow(0, 20, 50, -8, ui.RGBA(0, 0, 0, 0.42))
				_ = card
			})
	})
}

func (m *MainUI) openDeployLogOverlay(accountID string) {
	m.ShowHomeOptions = false
	m.ShowDeployLog = true
	m.Svc.RefreshDeployLog(accountID)
}

func (m *MainUI) renderDeployLogOverlay(c *ui.Context, pal Palette) {
	acc, hasAcc := m.Svc.SelectedAccount()
	if !hasAcc {
		return
	}
	rt := m.Svc.StateFor(acc.ID)

	ui.Overlay(c, func() {
		ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).
			Background(ui.RGBA(0, 0, 0, 0.48)).
			OnClick(func() {
				m.ShowDeployLog = false
			})

		ui.Column(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).
			PassThrough().
			Padding(16, 24).
			AlignItems(ui.Center).
			Justify(ui.Center).
			Children(func() {
				PrecisionCard(c, pal, 20, 18, func() {
					ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
						led := pal.Sky
						if rt.DeployRunning {
							led = pal.Amber
						} else if rt.DeployFailed {
							led = pal.Coral
						} else if rt.ServerHealthy {
							led = pal.Emerald
						}
						DrawPrecisionLED(c, led, 12)
						ui.Column(c).Grow(1).Gap(2).Children(func() {
							ui.Text(c, "云端部署日志与状态核查").FontSize(16).FontWeight(600).TextColor(pal.TextPrimary)
							sub := fmt.Sprintf("会话 %s · %s", acc.SessionName, rt.ShortGPUName())
							if rt.DeployRunning && rt.WorkloadMetricValue != "" {
								sub += fmt.Sprintf(" · %s %s", rt.WorkloadMetricValue, rt.WorkloadMetricUnit)
							} else if rt.DeployFailed {
								sub += " · 上次部署中断"
							} else if rt.ServerHealthy {
								sub += " · ComfyUI 已就绪"
							} else if rt.Phase.IsRunning() {
								sub += " · 裸机未部署"
							}
							ui.Text(c, sub).FontSize(12).TextColor(pal.TextMuted).MaxLines(1)
						})
						refreshLbl := "刷新日志"
						if rt.DeployLogLoading {
							refreshLbl = "读取中…"
						}
						SmallActionButton(c, pal, refreshLbl, nil, rt.DeployLogLoading, func() {
							m.Svc.RefreshDeployLog(acc.ID)
						})
						if rt.Phase.IsRunning() && acc.DeployRecipe.IsVideoComfy() && !rt.DeployRunning {
							depLbl := "立即部署"
							if rt.DeployFailed || rt.ServerHealthy {
								depLbl = "重新部署"
							}
							SmallActionButton(c, pal, depLbl, &pal.Amber, false, func() {
								m.Svc.DeployNow(acc.ID)
							})
						}
						SmallActionButton(c, pal, "完成", nil, false, func() {
							m.ShowDeployLog = false
						})
					})

					ui.Box(c).FillWidth().Height(1).Background(pal.Divider)

					logText := rt.DeployLogTail
					if rt.DeployLogLoading && logText == "" {
						logText = "正在连接云端机器读取 /tmp/colab-tools-deploy.log 与 /tmp/comfyui.log…"
					} else if logText == "" {
						logText = "点击右上角「刷新日志」拉取云端实时安装记录与端口状态。"
					}
					ui.Scroll(c).FillWidth().Grow(1).MaxHeight(480).Children(func() {
						ui.Column(c).FillWidth().Padding(12, 14).Radius(10).Background(pal.SubtleBg).Border(1, pal.SubtleBorder).Children(func() {
							ui.Text(c, logText).FontSize(11.5).Font("monospace").TextColor(pal.TextPrimary)
						})
					})
				}).Width(780).MaxHeight(620).Gap(12).
					Shadow(0, 20, 50, -8, ui.RGBA(0, 0, 0, 0.45))
			})
	})
}

func (m *MainUI) centeredWorkbench(c *ui.Context, fn func()) {
	ui.Column(c).FillWidth().AlignItems(ui.Center).Children(func() {
		ui.Column(c).FillWidth().MaxWidth(920).Gap(16).Children(fn)
	})
}

// MARK: - Top Mole-Style Floating Capsule Bar

func (m *MainUI) renderTopCapsuleBar(c *ui.Context, pal Palette) {
	accounts := m.Svc.AccountsSnapshot()
	selAcc, hasSel := m.Svc.SelectedAccount()
	leftPad := float32(24)
	topPad := float32(16)
	if !m.PreviewMode && runtime.GOOS == "darwin" {
		leftPad = 84 // Clear macOS traffic lights when live
		topPad = 14
	}

	ui.Row(c).FillWidth().Padding(topPad, 24, 14, leftPad).AlignItems(ui.Center).DragWindow().Children(func() {
		// Left multi-account selector pill
		if len(accounts) >= 2 && hasSel {
			ui.Row(c).Gap(7).Padding(6, 12).Radius(999).
				Background(pal.SubtleBg).
				Border(1, ui.Color{}).
				BorderLinearGradient(ui.LinearGradient{From: pal.CardBorderTop, To: pal.CardBorderBottom, Angle: 180}).
				InsetShadow(0, 1, 0, 0, pal.CardInnerTopLight).
				AlignItems(ui.Center).
				Cursor(ui.CursorPointer).
				OnClick(func() {
					// Cycle account quickly on click
					for i, a := range accounts {
						if a.ID == selAcc.ID {
							next := accounts[(i+1)%len(accounts)]
							m.Svc.SelectAccount(next.ID)
							break
						}
					}
				}).Children(func() {
				DrawPrecisionLED(c, AccountTagColor(pal, selAcc.ColorTag), 10)
				ui.Text(c, selAcc.DisplayName).FontSize(13.5).FontWeight(600).TextColor(pal.TextPrimary).MaxLines(1)
				ui.Text(c, "▾").FontSize(11).TextColor(pal.TextMuted)
			})
		}

		ui.Spacer(c)

		// Centered Mole floating capsule navigation with metallic bevel & inner highlight
		ui.Row(c).Gap(4).Padding(5, 6).Radius(999).
			Background(pal.NavBg).
			Border(1, ui.Color{}).
			BorderLinearGradient(ui.LinearGradient{From: pal.CardBorderTop, To: pal.CardBorderBottom, Angle: 180}).
			InsetShadow(0, 1, 0, 0, pal.CardInnerTopLight).
			Shadow(0, 4, 14, -4, pal.CardShadow).
			AlignItems(ui.Center).Children(func() {
			if appIconBitmap != nil {
				ui.Image(c, appIconBitmap).Width(26).Height(26).Radius(13).Clip()
			} else {
				ui.Box(c).Width(26).Height(26).Radius(13).Background(pal.Amber)
			}
			ui.Box(c).Width(2)

			for idx, title := range app.MainSectionTitles {
				sec := app.MainSection(idx)
				selected := m.Svc.SelectedSection == sec
				el := ui.Row(c).Padding(6, 15).Radius(999).AlignItems(ui.Center).Cursor(ui.CursorPointer)
				if selected {
					el.Background(pal.NavPillBg).
						InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.24)).
						Shadow(0, 2, 7, 0, ui.RGBA(0, 0, 0, 0.20))
				}
				el.OnClick(func() {
					m.Svc.SetSection(sec)
				}).Children(func() {
					fg := pal.TextMuted
					w := 500
					if selected {
						fg = pal.NavPillText
						w = 600
					}
					ui.Text(c, title).FontSize(13.5).FontWeight(w).TextColor(fg)
				})
			}
		})

		ui.Spacer(c)

		// Right live status pill with aligned refresh vector icon
		pillColor, pillLabel := m.topRightPill(pal)
		ui.Row(c).Gap(7).Padding(6, 13).Radius(999).
			Background(pal.SubtleBg).
			Border(1, ui.Color{}).
			BorderLinearGradient(ui.LinearGradient{From: pal.CardBorderTop, To: pal.CardBorderBottom, Angle: 180}).
			InsetShadow(0, 1, 0, 0, pal.CardInnerTopLight).
			AlignItems(ui.Center).
			Cursor(ui.CursorPointer).
			OnClick(func() {
				if !m.Svc.CLIInstalled {
					m.Svc.ReopenOnboarding()
				} else {
					go m.Svc.RefreshAllAccounts(true, false)
				}
			}).Children(func() {
			DrawPrecisionLED(c, pillColor, 10)
			ui.Text(c, pillLabel).FontSize(12.5).FontWeight(500).FontFeatures("tnum").TextColor(pal.TextPrimary).MaxLines(1)
			if m.Svc.CLIInstalled {
				ToolbarIcon(c, "refresh", 13, pal.TextMuted)
			}
		})
	})
}

func (m *MainUI) topRightPill(pal Palette) (ui.Color, string) {
	if !m.Svc.CLIInstalled {
		return pal.Coral, "需安装 Colab CLI"
	}
	running := m.Svc.RunningAccountCount()
	if running >= 2 {
		return pal.Emerald, fmt.Sprintf("%d 运行中", running)
	}
	if running == 1 {
		for _, a := range m.Svc.AccountsSnapshot() {
			rt := m.Svc.StateFor(a.ID)
			if rt.Phase.IsRunning() {
				return pal.Emerald, fmt.Sprintf("运行中 · %s", rt.ShortGPUName())
			}
		}
	}
	for _, a := range m.Svc.AccountsSnapshot() {
		rt := m.Svc.StateFor(a.ID)
		if rt.Phase.IsQueuing() {
			return pal.Amber, fmt.Sprintf("排队 %d/%d", rt.Phase.QueueAttempt, rt.Phase.QueueMax)
		}
	}
	return pal.TextFaint, fmt.Sprintf("v%s", app.AppVersion)
}

func drawBannerSemanticBar(p *ui.Painter, r ui.Rect, col ui.Color) {
	h := max(float32(16), r.H-16)
	p.Fill(ui.Rect{X: r.X + 4, Y: r.Y + (r.H-h)/2, W: 4, H: h}, col, 2)
}

// MARK: - Tab 1: Mole-Style 4x2 Bento Status Dashboard + Bottom Live Session Table

func (m *MainUI) renderStatusPage(c *ui.Context, pal Palette) {
	if !m.Svc.CLIInstalled {
		ui.Row(c).FillWidth().Padding(14, 16, 14, 18).Radius(14).
			Background(WithAlpha(pal.Amber, 0.10)).
			Border(1, WithAlpha(pal.Amber, 0.30)).
			InsetShadow(0, 1, 0, 0, pal.CardInnerTopLight).
			DrawOver(func(p *ui.Painter, r ui.Rect) {
				drawBannerSemanticBar(p, r, pal.Amber)
			}).
			AlignItems(ui.Center).Gap(12).Children(func() {
			DrawPrecisionLED(c, pal.Amber, 12)
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				ui.Text(c, "未检测到可用的官方 google-colab-cli").FontSize(13.5).FontWeight(600).TextColor(pal.TextPrimary)
				detail := "打开开箱体检向导即可一键安装并绑定你的 Google Colab 账号。"
				if m.Svc.EnvReport.ColabBrokenReason != "" {
					detail = m.Svc.EnvReport.ColabBrokenReason
				}
				ui.Text(c, detail).FontSize(12).TextColor(pal.TextMuted)
			})
			PillButton(c, pal, "打开一键安装向导", true, false, func() {
				m.Svc.ReopenOnboarding()
			})
		})
	}

	acc, hasAcc := m.Svc.SelectedAccount()
	if !hasAcc {
		SectionCard(c, pal, "", "", func() {
			ui.Column(c).FillWidth().Padding(32, 16).AlignItems(ui.Center).Gap(12).Children(func() {
				if appIconBitmap != nil {
					ui.Image(c, appIconBitmap).Width(60).Height(60).Radius(15).Clip()
				}
				ui.Text(c, "尚未绑定 Google Colab 账号").FontSize(20).Bold().TextColor(pal.TextPrimary)
				ui.Text(c, "完成一键环境体检与官方 OAuth2 授权，即可在这里实时监控点数与云端 GPU。").FontSize(13.5).TextColor(pal.TextMuted)
				ui.Row(c).Gap(10).Padding(6, 0, 0, 0).Children(func() {
					PillButton(c, pal, "打开开箱引导向导", true, false, func() {
						m.Svc.ReopenOnboarding()
					})
					PillButton(c, pal, "前往账号页手动添加", false, false, func() {
						m.Svc.SetSection(app.SectionAccounts)
					})
				})
			})
		})
		return
	}

	rt := m.Svc.StateFor(acc.ID)

	// Active queue, rate-limit cooldown, or actionable error banner (always plain Chinese with clear recovery)
	if rt.Phase.IsQueuing() {
		countdown := "正在按顺位尝试拿卡…"
		if rt.Phase.Detail != "" {
			countdown = rt.Phase.Detail
		}
		if rt.Phase.NextRetryAt != nil {
			rem := max(0, int(math.Ceil(time.Until(*rt.Phase.NextRetryAt).Seconds())))
			countdown = fmt.Sprintf("暂未有货，%02d:%02d 后自动再试下一轮", rem/60, rem%60)
		}
		ui.Row(c).FillWidth().Padding(11, 14, 11, 18).Radius(14).
			Background(WithAlpha(pal.Amber, 0.11)).
			Border(1, WithAlpha(pal.Amber, 0.30)).
			InsetShadow(0, 1, 0, 0, pal.CardInnerTopLight).
			DrawOver(func(p *ui.Painter, r ui.Rect) {
				drawBannerSemanticBar(p, r, pal.Amber)
			}).
			AlignItems(ui.Center).Gap(10).Children(func() {
			DrawPrecisionLED(c, pal.Amber, 12)
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				ui.Textf(c, "自动排队抢卡中 · 第 %d/%d 轮（%s）· %s", rt.Phase.QueueAttempt, rt.Phase.QueueMax, rt.Phase.TargetsSummary, countdown).
					FontSize(13).FontWeight(500).FontFeatures("tnum").TextColor(pal.TextPrimary)
				ui.Text(c, "可以关掉或切走窗口去忙别的，顶部菜单栏会实时显示进度，抢到/装好或未抢到都会弹窗响铃通知你。").
					FontSize(11.5).TextColor(pal.TextMuted)
			})
			SmallActionButton(c, pal, "取消排队", &pal.Coral, false, func() {
				m.Svc.CancelQueue(acc.ID)
			})
		})
	} else if rt.QueueCircuitBreakTill != nil && rt.QueueCircuitBreakTill.After(time.Now()) {
		remMin := max(1, int(math.Ceil(time.Until(*rt.QueueCircuitBreakTill).Minutes())))
		title := rt.LastErrorTitle
		if title == "" {
			title = "Google 提示请求偏频繁，先歇一会儿保护账号"
		}
		detail := rt.LastError
		if detail == "" {
			detail = fmt.Sprintf("建议等待约 %d 分钟后再申请；如果刚换了显卡或确认无误，也可以点右侧直接解除等待。", remMin)
		} else {
			detail = fmt.Sprintf("%s（还剩约 %d 分钟）", detail, remMin)
		}
		ui.Column(c).FillWidth().Padding(12, 14, 12, 18).Gap(8).Radius(14).
			Background(WithAlpha(pal.Coral, 0.10)).
			Border(1, WithAlpha(pal.Coral, 0.30)).
			InsetShadow(0, 1, 0, 0, pal.CardInnerTopLight).
			DrawOver(func(p *ui.Painter, r ui.Rect) {
				drawBannerSemanticBar(p, r, pal.Coral)
			}).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
				DrawPrecisionLED(c, pal.Coral, 12)
				ui.Column(c).Grow(1).Gap(2).Children(func() {
					ui.Text(c, title).FontSize(13.5).FontWeight(600).TextColor(pal.TextPrimary)
					ui.Text(c, detail).FontSize(12).TextColor(pal.TextMuted)
				})
				if rt.LastErrorRaw != "" {
					lbl := "查看详情"
					if m.ShowErrorRaw {
						lbl = "收起详情"
					}
					SmallActionButton(c, pal, lbl, nil, false, func() {
						m.ShowErrorRaw = !m.ShowErrorRaw
					})
				}
				SmallActionButton(c, pal, "解除等待", &pal.Amber, false, func() {
					m.Svc.ClearCooldown(acc.ID)
				})
			})
			if m.ShowErrorRaw && rt.LastErrorRaw != "" {
				ui.Text(c, "原始反馈："+rt.LastErrorRaw).FontSize(11.5).Font("monospace").TextColor(pal.TextMuted)
			}
		})
	} else if rt.LastError != "" && !rt.Phase.IsRunning() {
		title := rt.LastErrorTitle
		if title == "" {
			title = "刚才这次申请没成功"
		}
		ui.Column(c).FillWidth().Padding(12, 14, 12, 18).Gap(8).Radius(14).
			Background(WithAlpha(pal.Amber, 0.10)).
			Border(1, WithAlpha(pal.Amber, 0.30)).
			InsetShadow(0, 1, 0, 0, pal.CardInnerTopLight).
			DrawOver(func(p *ui.Painter, r ui.Rect) {
				drawBannerSemanticBar(p, r, pal.Amber)
			}).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
				DrawPrecisionLED(c, pal.Amber, 12)
				ui.Column(c).Grow(1).Gap(2).Children(func() {
					ui.Text(c, title).FontSize(13.5).FontWeight(600).TextColor(pal.TextPrimary)
					ui.Text(c, rt.LastError).FontSize(12).TextColor(pal.TextMuted)
				})
				if rt.LastErrorRaw != "" {
					lbl := "详情"
					if m.ShowErrorRaw {
						lbl = "收起"
					}
					SmallActionButton(c, pal, lbl, nil, false, func() {
						m.ShowErrorRaw = !m.ShowErrorRaw
					})
				}
				switch rt.LastErrorAction {
				case "stop_existing":
					SmallActionButton(c, pal, "停掉旧机器", &pal.Coral, false, func() {
						go m.Svc.StopAllManagedSessions()
					})
				case "reauth":
					SmallActionButton(c, pal, "去重新授权", &pal.Amber, false, func() {
						m.Svc.SetSection(app.SectionAccounts)
					})
				default:
					SmallActionButton(c, pal, "直接重试", &pal.Amber, !m.Svc.CLIInstalled, func() {
						m.Svc.DeployNow(acc.ID)
					})
				}
				SmallActionButton(c, pal, "✕", nil, false, func() {
					m.Svc.ClearAccountError(acc.ID)
				})
			})
			if m.ShowErrorRaw && rt.LastErrorRaw != "" {
				ui.Text(c, "原始反馈："+rt.LastErrorRaw).FontSize(11.5).Font("monospace").TextColor(pal.TextMuted)
			}
		})
	} else if rt.Phase.IsRunning() && rt.DeployFailed {
		errDetail := rt.DeployError
		if errDetail == "" {
			errDetail = "请查看云端日志定位具体失败原因后重试。"
		}
		ui.Row(c).FillWidth().Padding(11, 14, 11, 18).Radius(14).
			Background(WithAlpha(pal.Coral, 0.11)).
			Border(1, WithAlpha(pal.Coral, 0.32)).
			InsetShadow(0, 1, 0, 0, pal.CardInnerTopLight).
			DrawOver(func(p *ui.Painter, r ui.Rect) {
				drawBannerSemanticBar(p, r, pal.Coral)
			}).
			AlignItems(ui.Center).Gap(10).Children(func() {
			DrawPrecisionLED(c, pal.Coral, 12)
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				ui.Text(c, "云端自动部署中断").FontSize(13.5).FontWeight(600).TextColor(pal.TextPrimary)
				ui.Text(c, errDetail).FontSize(12).TextColor(pal.TextMuted).MaxLines(2).Ellipsis("…")
			})
			SmallActionButton(c, pal, "查看日志", nil, false, func() {
				m.openDeployLogOverlay(acc.ID)
			})
			SmallActionButton(c, pal, "重新部署", &pal.Amber, false, func() {
				m.Svc.DeployNow(acc.ID)
			})
		})
	} else if rt.Phase.IsRunning() && !rt.ServerHealthy && !rt.DeployRunning {
		if hasWarn, warnMsg := rt.HasVRAMWarningForRecipe(acc); hasWarn {
			ui.Row(c).FillWidth().Padding(11, 14, 11, 18).Radius(14).
				Background(WithAlpha(pal.Amber, 0.11)).
				Border(1, WithAlpha(pal.Amber, 0.30)).
				InsetShadow(0, 1, 0, 0, pal.CardInnerTopLight).
				DrawOver(func(p *ui.Painter, r ui.Rect) {
					drawBannerSemanticBar(p, r, pal.Amber)
				}).
				AlignItems(ui.Center).Gap(10).Children(func() {
				DrawPrecisionLED(c, pal.Amber, 12)
				ui.Column(c).Grow(1).Gap(2).Children(func() {
					ui.Text(c, "当前显卡显存与所选视频模型不匹配").FontSize(13.5).FontWeight(600).TextColor(pal.TextPrimary)
					ui.Text(c, warnMsg).FontSize(12).TextColor(pal.TextMuted)
				})
				SmallActionButton(c, pal, "改用轻量 GGUF", &pal.Amber, false, func() {
					cp := acc
					cp.H3Variant = core.H3VariantFlashGGUF
					m.Svc.UpdateAccount(cp)
					m.Svc.ShowToast("已切为 H3 Flash 轻量 (GGUF)，点「立即部署」即可开始安装")
				})
				SmallActionButton(c, pal, "调整方案", nil, false, func() {
					m.ShowHomeOptions = true
				})
			})
		}
	}

	// Unified 4x2 Precision Bento Dashboard (both Idle and Active) + Bottom Live Session Table
	ui.Grid(c).FillWidth().ColumnTracks(ui.Fr(1), ui.Fr(1), ui.Fr(1), ui.Fr(1)).Gap(14).Children(func() {
		m.bentoHealthCU(c, pal, acc, rt)
		m.bentoVRAM(c, pal, acc, rt)
		m.bentoGPU(c, pal, acc, rt)
		m.bentoWorkload(c, pal, acc, rt)
		m.bentoBatteryQuota(c, pal, acc, rt)
		m.bentoIdleWatchdog(c, pal, acc, rt)
		m.bentoStrategy(c, pal, acc)
		m.bentoQuickIntegrations(c, pal, acc, rt)
	})

	// Bottom Live Sessions & Multi-Account Table
	m.renderLiveAccountsTable(c, pal, acc)
}

func (m *MainUI) bentoHealthCU(c *ui.Context, pal Palette, acc core.AccountProfile, rt core.AccountRuntimeState) {
	offlineBilling := !rt.Phase.IsRunning() && rt.CURatePerHour > 0
	statusWord := "已待机"
	statusTint := pal.TextMuted
	sub := "剩余 CU"
	switch rt.Phase.Kind {
	case core.PhaseRunning:
		if rt.DeployRunning {
			statusWord = "部署中"
			statusTint = pal.Amber
		} else if rt.DeployFailed {
			statusWord = "部署中断"
			statusTint = pal.Coral
		} else if !acc.EnableLLMModule && acc.DeployRecipe.IsVideoComfy() && !rt.ServerHealthy {
			statusWord = "待部署"
			statusTint = pal.Amber
		} else if rt.ActivitySignal.IsBusy() {
			statusWord = "推理中"
			statusTint = pal.Emerald
		} else {
			statusWord = "很好"
			statusTint = pal.Emerald
		}
		sub = core.FormatDuration(rt.SessionUptimeSec)
	case core.PhaseQueuing:
		statusWord = fmt.Sprintf("排队 %d/%d", rt.Phase.QueueAttempt, rt.Phase.QueueMax)
		statusTint = pal.Amber
		sub = rt.Phase.TargetsSummary
	case core.PhaseStarting:
		statusWord = "启动中"
		statusTint = pal.Amber
		sub = rt.Phase.Detail
	case core.PhaseStopping:
		statusWord = "关机中"
		statusTint = pal.Coral
		sub = "正在释放"
	case core.PhaseError:
		statusWord = "需检查"
		statusTint = pal.Coral
	default:
		if offlineBilling {
			statusWord = "仍有计费"
			statusTint = pal.Amber
			sub = fmt.Sprintf("%.2f CU/h", rt.CURatePerHour)
		}
	}

	card := PrecisionCard(c, pal, 16, 16, func() {
		TileHeader(c, pal, pal.Emerald, "算力状态", acc.Tier.ShortBadge(), nil)

		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Column(c).Grow(1).Gap(4).Children(func() {
				ui.Text(c, core.FormatCU(rt.CUBalance)).FontSize(28).Bold().FontFeatures("tnum").TextColor(pal.TextPrimary).MaxLines(1)
				ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
					ui.Text(c, statusWord).FontSize(12.5).FontWeight(600).TextColor(statusTint).MaxLines(1)
					ui.Text(c, "·").FontSize(12).TextColor(pal.TextFaint)
					subCol := pal.TextMuted
					if offlineBilling {
						subCol = pal.Amber
					}
					ui.Text(c, sub).FontSize(12).FontFeatures("tnum").TextColor(subCol).MaxLines(1).Ellipsis("…")
				})
			})
			DrawAuraOrb(c, pal, rt.Phase, offlineBilling, 44)
		})

		ui.Spacer(c)

		// Power action button
		isCooling := rt.QueueCircuitBreakTill != nil && rt.QueueCircuitBreakTill.After(time.Now())
		if rt.Phase.IsBusy() {
			ui.Row(c).FillWidth().Padding(7, 10).Radius(8).Background(pal.SubtleBg).Justify(ui.Center).Children(func() {
				ui.Text(c, "正在执行官方指令…").FontSize(12).TextColor(pal.TextMuted)
			})
		} else if rt.Phase.IsQueuing() {
			ui.Row(c).FillWidth().Padding(7, 10).Radius(8).
				Background(WithAlpha(pal.Coral, 0.14)).Border(1, WithAlpha(pal.Coral, 0.34)).
				Justify(ui.Center).OnClick(func() {
				m.Svc.CancelQueue(acc.ID)
			}).Children(func() {
				ui.Text(c, "✕  取消排队").FontSize(12.5).FontWeight(600).TextColor(pal.Coral)
			})
		} else if rt.Phase.IsRunning() {
			ui.Row(c).FillWidth().Padding(7, 10).Radius(8).
				Background(WithAlpha(pal.Coral, 0.14)).Border(1, WithAlpha(pal.Coral, 0.34)).
				Justify(ui.Center).OnClick(func() {
				go m.Svc.StopAccountSession(acc.ID, "", "")
			}).Children(func() {
				ui.Text(c, "⏻  关机止损").FontSize(12.5).FontWeight(600).TextColor(pal.Coral)
			})
		} else {
			blocked := isCooling || !m.Svc.CLIInstalled
			label := "一键开机并运行"
			if acc.DeployRecipe == core.RecipeNone {
				label = "一键开机"
			}
			CompactAmberLaunchButton(c, pal, label, blocked, func() {
				m.Svc.DeployNow(acc.ID)
			})
		}
	}).Height(176).Gap(8)
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

func (m *MainUI) bentoVRAM(c *ui.Context, pal Palette, acc core.AccountProfile, rt core.AccountRuntimeState) {
	if rt.Phase.IsRunning() || rt.VRAMTotalMB > 0 {
		pct := int(math.Round(rt.VRAMRatio() * 100))
		badge := "未加载"
		var tint *ui.Color
		barTint := pal.Emerald
		if rt.VRAMTotalMB > 0 {
			if pct >= 94 {
				badge = "高占用"
				t := pal.Amber
				tint = &t
				barTint = pal.Amber
			} else {
				badge = "压力正常"
			}
		}
		BentoCard(c, pal, 176, func() {
			TileHeader(c, pal, pal.Emerald, "显存", badge, tint)
			ui.Row(c).AlignItems(ui.End).Gap(4).Children(func() {
				if rt.VRAMTotalMB > 0 {
					ui.Textf(c, "%d", pct).FontSize(28).Bold().FontFeatures("tnum").TextColor(pal.TextPrimary)
					ui.Text(c, "%").FontSize(14).FontWeight(500).TextColor(pal.TextMuted).Padding(0, 0, 4, 0)
				} else {
					ui.Text(c, "—").FontSize(28).Bold().TextColor(pal.TextPrimary)
				}
			})
			DrawVRAMSegments(c, pal, rt.VRAMRatio(), barTint, 26)
			ui.Spacer(c)
			foot := "等待 GPU 分配"
			if rt.VRAMTotalMB > 0 {
				foot = fmt.Sprintf("%.1f GB / %.1f GB", float64(rt.VRAMUsedMB)/1024.0, float64(rt.VRAMTotalMB)/1024.0)
			}
			ui.Text(c, foot).FontSize(12).FontFeatures("tnum").TextColor(pal.TextMuted).MaxLines(1)
		})
		return
	}

	// Standby mode: compact 2x2 task selector inside Bento Card 2
	taskBadge := "H3 Flash"
	taskFoot := "6 步快出片 · 自动装 ComfyUI"
	badgeTint := pal.Amber
	switch acc.DeployRecipe {
	case core.RecipeH3Video:
		switch acc.H3Variant {
		case core.H3VariantOriginal:
			taskBadge = "H3 原版"
			taskFoot = "31B 满血双权重 · 画质最高"
		case core.H3VariantFlashGGUF:
			taskBadge = "轻量 GGUF"
			taskFoot = fmt.Sprintf("GGUF %s · 24G L4 可跑", acc.H3GGUFQuant)
		case core.H3VariantCustom:
			taskBadge = "自定义 H3"
			taskFoot = "自定义 HF 仓库 / 仅用 Drive"
		}
	case core.RecipeComfyUIGeneral:
		taskBadge = "ComfyUI"
		taskFoot = "纯净 ComfyUI · 自由装节点"
	case core.RecipeLLMProxy:
		taskBadge = "大模型"
		taskFoot = "自动同步 Claude / Codex / OpenCode"
		badgeTint = pal.Sky
	case core.RecipeNone:
		taskBadge = "纯开机"
		taskFoot = "纯净裸机环境 · 仅抢卡与守护"
	}

	flashSel := acc.DeployRecipe == core.RecipeH3Video && acc.H3Variant == core.H3VariantFlash
	origSel := acc.DeployRecipe == core.RecipeH3Video && acc.H3Variant == core.H3VariantOriginal
	llmSel := acc.DeployRecipe == core.RecipeLLMProxy
	moreSel := !flashSel && !origSel && !llmSel

	BentoCard(c, pal, 176, func() {
		TileHeader(c, pal, badgeTint, "当前任务", taskBadge, &badgeTint)
		ui.Grid(c).FillWidth().ColumnTracks(ui.Fr(1), ui.Fr(1)).Gap(6).Children(func() {
			m.bentoTaskChip(c, pal, "H3 Flash 极速版", flashSel, func() {
				m.selectH3Preset(acc, core.H3VariantFlash)
			})
			m.bentoTaskChip(c, pal, "H3 原版 (31B)", origSel, func() {
				m.selectH3Preset(acc, core.H3VariantOriginal)
			})
			m.bentoTaskChip(c, pal, "大模型代理", llmSel, func() {
				m.Svc.ApplyDeployRecipe(acc.ID, core.RecipeLLMProxy)
			})
			m.bentoTaskChip(c, pal, "更多玩法", moreSel, func() {
				m.ShowHomeOptions = true
			})
		})
		ui.Spacer(c)
		ui.Text(c, taskFoot).FontSize(12).TextColor(pal.TextMuted).MaxLines(1).Ellipsis("…")
	})
}

func (m *MainUI) bentoTaskChip(c *ui.Context, pal Palette, label string, selected bool, onClick func()) {
	bg := pal.SubtleBg
	border := pal.SubtleBorder
	borderW := float32(1)
	fw := 500
	if selected {
		bg = WithAlpha(pal.Amber, 0.14)
		border = WithAlpha(pal.Amber, 0.72)
		borderW = 1.2
		fw = 600
	}
	el := ui.Row(c).FillWidth().Padding(7, 6).Radius(8).
		Background(bg).Border(borderW, border).
		AlignItems(ui.Center).Justify(ui.Center).Cursor(ui.CursorPointer)
	if !selected && el.Hovered() {
		el.Background(pal.CardTop).Border(1, pal.CardBorderBottom)
	}
	if onClick != nil {
		el.OnClick(onClick)
	}
	el.Children(func() {
		ui.Text(c, label).FontSize(11.5).FontWeight(fw).TextColor(pal.TextPrimary).MaxLines(1).Ellipsis("…")
	})
}

func (m *MainUI) bentoGPU(c *ui.Context, pal Palette, acc core.AccountProfile, rt core.AccountRuntimeState) {
	offlineBilling := !rt.Phase.IsRunning() && rt.CURatePerHour > 0
	primaryChip := acc.PreferredAccelerator.ShortChipLabel()
	if targets := core.EffectiveQueueTargets(acc); len(targets) > 0 {
		primaryChip = targets[0].ShortChipLabel()
	}
	estRate := core.EstimatedHourlyCU(primaryChip)

	badge := "待机就绪"
	var bTint *ui.Color
	if rt.Phase.IsRunning() {
		badge = rt.ShortGPUName()
	} else if offlineBilling {
		badge = "仍在计费"
		t := pal.Amber
		bTint = &t
	}

	foot := fmt.Sprintf("%s · 约 %.2f CU/h", acc.GPUStrategy.DisplayName(), estRate)
	if acc.Tier == core.TierFree {
		foot = "免费体验池 · 按需排队"
	}
	footColor := pal.TextMuted
	sparkPts := []float64{24, 26, 25, 27, 26, 25, 26}
	sparkTint := WithAlpha(pal.Amber, 0.52)

	if rt.Phase.IsRunning() {
		rateStr := "速率待同步"
		if rt.LastUsageFetchAt != nil || rt.CURatePerHour > 0 {
			rateStr = fmt.Sprintf("%.2f CU/h", rt.CURatePerHour)
		}
		if rt.ActivitySignal.IsBusy() {
			foot = "高负载 · " + rateStr
		} else {
			foot = "低负载 · " + rateStr
		}
		sparkPts = rt.GPUUtilHistory
		sparkTint = pal.Amber
	} else if offlineBilling {
		foot = fmt.Sprintf("其他会话计费 · %.2f CU/h", rt.CURatePerHour)
		footColor = pal.Amber
	}

	BentoCard(c, pal, 176, func() {
		TileHeader(c, pal, pal.Amber, "GPU", badge, bTint)
		ui.Row(c).AlignItems(ui.End).Gap(5).Children(func() {
			if rt.Phase.IsRunning() {
				ui.Textf(c, "%d", rt.GPUUtilPercent).FontSize(28).Bold().FontFeatures("tnum").TextColor(pal.TextPrimary)
				ui.Text(c, "%").FontSize(14).FontWeight(500).TextColor(pal.TextMuted).Padding(0, 0, 4, 0)
			} else if offlineBilling {
				ui.Text(c, "—").FontSize(28).Bold().TextColor(pal.TextPrimary)
			} else {
				ui.Text(c, primaryChip).FontSize(28).Bold().TextColor(pal.TextPrimary).MaxLines(1)
				ui.Text(c, "首选").FontSize(12.5).FontWeight(500).TextColor(pal.TextMuted).Padding(0, 0, 4, 0)
			}
		})
		DrawSparkline(c, pal, sparkPts, sparkTint, 30)
		ui.Spacer(c)
		ui.Text(c, foot).FontSize(12).FontFeatures("tnum").TextColor(footColor).MaxLines(1).Ellipsis("…")
	})
}

func (m *MainUI) bentoWorkload(c *ui.Context, pal Palette, acc core.AccountProfile, rt core.AccountRuntimeState) {
	kind := acc.WorkloadKind
	if kind == core.WorkloadGeneral && rt.DetectedWorkloadKind != nil {
		kind = *rt.DetectedWorkloadKind
	}
	tint := WorkloadColor(pal, kind)

	BentoCard(c, pal, 176, func() {
		if acc.DeployRecipe == core.RecipeLLMProxy || kind == core.WorkloadLLM {
			ctxBadge := fmt.Sprintf("%dK", rt.ContextLimit/1024)
			if rt.Phase.IsRunning() {
				TileHeader(c, pal, pal.Sky, "上下文", ctxBadge, nil)
				ui.Row(c).AlignItems(ui.End).Gap(4).Children(func() {
					if rt.ServerHealthy {
						ui.Text(c, core.FormatTokens(rt.PromptTokens)).FontSize(28).Bold().FontFeatures("tnum").TextColor(pal.TextPrimary)
						ui.Text(c, "tok").FontSize(13.5).FontWeight(500).TextColor(pal.TextMuted).Padding(0, 0, 4, 0)
					} else {
						ui.Text(c, "启动中").FontSize(25).Bold().TextColor(pal.TextPrimary)
					}
				})
				prog := 0.0
				if rt.ServerHealthy {
					prog = rt.ContextRatio()
				}
				ui.Box(c).Height(4)
				DrawHorizonBar(c, pal, prog, pal.Sky, 8)
				ui.Spacer(c)
				foot := "等待模型服务在线"
				if rt.ServerHealthy {
					foot = fmt.Sprintf("已用 %d%% · %s KV · #%d", int(math.Round(rt.ContextRatio()*100)), rt.KVQuant, max(0, rt.LastTaskID))
				}
				ui.Text(c, foot).FontSize(12).FontFeatures("tnum").TextColor(pal.TextMuted).MaxLines(1)
			} else {
				TileHeader(c, pal, pal.Sky, "大模型代理", ctxBadge, nil)
				ui.Row(c).AlignItems(ui.End).Gap(5).Children(func() {
					ui.Text(c, "已就绪").FontSize(25).Bold().TextColor(pal.TextPrimary)
					ui.Textf(c, ":%d", acc.RemoteServicePort).FontSize(13).FontFeatures("tnum").TextColor(pal.TextMuted).Padding(0, 0, 4, 0)
				})
				ui.Box(c).Height(4)
				DrawHorizonBar(c, pal, 0.85, WithAlpha(pal.Sky, 0.85), 8)
				ui.Spacer(c)
				ui.Text(c, "开机自动同步编程工具").FontSize(12).TextColor(pal.TextMuted).MaxLines(1)
			}
		} else if acc.DeployRecipe.IsVideoComfy() || kind == core.WorkloadVideo {
			title := "视频部署与队列"
			badge := "纯云端"
			var bTint *ui.Color
			if acc.UseGoogleDrive {
				badge = "Drive 存片"
				e := pal.Emerald
				bTint = &e
			}
			if rt.Phase.IsRunning() {
				badge = acc.H3Variant.DisplayName()
				if acc.DeployRecipe == core.RecipeComfyUIGeneral {
					badge = "ComfyUI"
				}
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
				} else if rt.RenderedVideoCount > 0 {
					badge = fmt.Sprintf("已出 %d 条", rt.RenderedVideoCount)
					e := pal.Emerald
					bTint = &e
				} else if rt.ServerHealthy {
					e := pal.Emerald
					bTint = &e
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
						ui.Text(c, val).FontSize(26).Bold().FontFeatures("tnum").TextColor(pal.TextPrimary)
						if unit != "" {
							ui.Text(c, unit).FontSize(13).FontWeight(500).TextColor(pal.TextMuted).Padding(0, 0, 4, 0)
						}
					} else if rt.DeployFailed {
						ui.Text(c, "部署失败").FontSize(25).Bold().TextColor(pal.Coral)
					} else if rt.QueueRunning > 0 || rt.QueuePending > 0 {
						ui.Textf(c, "渲染 %d", rt.QueueRunning).FontSize(26).Bold().FontFeatures("tnum").TextColor(pal.TextPrimary)
						ui.Textf(c, "排队 %d", rt.QueuePending).FontSize(13).FontWeight(500).TextColor(pal.Amber).Padding(0, 0, 4, 0)
					} else if rt.ServerHealthy {
						ui.Text(c, "已就绪").FontSize(25).Bold().TextColor(pal.Emerald)
						ui.Text(c, ":8188").FontSize(13).FontFeatures("tnum").TextColor(pal.TextMuted).Padding(0, 0, 4, 0)
					} else {
						ui.Text(c, "未部署").FontSize(25).Bold().TextColor(pal.Amber)
						ui.Text(c, "裸机").FontSize(13).FontWeight(500).TextColor(pal.TextMuted).Padding(0, 0, 4, 0)
					}
				} else {
					subNote := "本机加密通道"
					if acc.AutoOpenComfyUI {
						subNote = "自动开 ComfyUI"
					}
					ui.Text(c, "已配齐").FontSize(25).Bold().TextColor(pal.TextPrimary)
					ui.Text(c, subNote).FontSize(12.5).FontWeight(500).TextColor(pal.TextMuted).Padding(0, 0, 4, 0)
				}
			})

			prog := 0.85
			barCol := WithAlpha(pal.Violet, 0.85)
			if rt.Phase.IsRunning() {
				barCol = pal.Violet
				prog = 0.0
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
			ui.Box(c).Height(4)
			DrawHorizonBar(c, pal, prog, barCol, 8)
			ui.Spacer(c)
			foot := "开机自动装好 · 纯云端暂存"
			if acc.UseGoogleDrive {
				foot = "开机自动装好 · 成片直存 Drive"
			}
			if rt.Phase.IsRunning() {
				if rt.DeployFailed && rt.DeployError != "" {
					foot = rt.DeployError
				} else if rt.DeployRunning && rt.WorkloadDetail != "" {
					foot = rt.WorkloadDetail
				} else if !rt.ServerHealthy && !rt.DeployRunning {
					foot = "未装 ComfyUI · 点下方「立即部署」"
				} else if rt.WorkloadDetail != "" {
					foot = rt.WorkloadDetail
				} else if acc.UseGoogleDrive {
					foot = "成片实时存 Drive · 断线不白跑"
				} else {
					foot = "纯云端运行 · 记得下载成片"
				}
			}
			ui.Text(c, foot).FontSize(12).FontFeatures("tnum").TextColor(pal.TextMuted).MaxLines(1).Ellipsis("…")
		} else {
			port := acc.RemoteServicePort
			if rt.DetectedServicePort != nil {
				port = *rt.DetectedServicePort
			}
			cardTitle := kind.MetricCardTitle()
			if acc.DeployRecipe == core.RecipeNone {
				cardTitle = "运行环境"
			}
			TileHeader(c, pal, tint, cardTitle, fmt.Sprintf(":%d", port), nil)
			ui.Row(c).AlignItems(ui.End).Gap(5).Children(func() {
				if rt.Phase.IsRunning() {
					v := rt.WorkloadMetricValue
					u := rt.WorkloadMetricUnit
					if v == "" {
						v = fmt.Sprintf("%d", rt.CPUUtilPercent)
						u = "% CPU"
					}
					ui.Text(c, v).FontSize(28).Bold().FontFeatures("tnum").TextColor(pal.TextPrimary)
					ui.Text(c, u).FontSize(13).FontWeight(500).TextColor(pal.TextMuted).Padding(0, 0, 4, 0)
				} else {
					ui.Text(c, "裸机就绪").FontSize(25).Bold().TextColor(pal.TextPrimary)
					ui.Text(c, "纯算力").FontSize(12.5).FontWeight(500).TextColor(pal.TextMuted).Padding(0, 0, 4, 0)
				}
			})
			prog := 0.65
			if rt.Phase.IsRunning() {
				if rt.WorkloadMetricRatio != nil {
					prog = *rt.WorkloadMetricRatio
				} else {
					prog = float64(rt.CPUUtilPercent) / 100.0
				}
			}
			ui.Box(c).Height(4)
			DrawHorizonBar(c, pal, prog, tint, 8)
			ui.Spacer(c)
			foot := "仅抢卡与守护 · 不装额外模型"
			if rt.Phase.IsRunning() {
				foot = fmt.Sprintf("%s · CUDA %d 进程", kind.DisplayName(), rt.CUDAProcessCount)
				if rt.WorkloadDetail != "" {
					foot = rt.WorkloadDetail
				}
			}
			ui.Text(c, foot).FontSize(12).FontFeatures("tnum").TextColor(pal.TextMuted).MaxLines(1).Ellipsis("…")
		}
	})
}

func (m *MainUI) bentoBatteryQuota(c *ui.Context, pal Palette, acc core.AccountProfile, rt core.AccountRuntimeState) {
	capSec := float64(max(1, acc.Tier.MaxSessionHours()*3600))
	ratio := 1.0
	if rt.Phase.IsRunning() {
		ratio = float64(rt.RemainingCapSeconds(acc.Tier)) / capSec
	}
	hrs, basis, ok := rt.EstimatedHoursRemaining(acc)

	card := PrecisionCard(c, pal, 16, 16, func() {
		TileHeader(c, pal, pal.Emerald, "算力续航", fmt.Sprintf("上限 %dh", acc.Tier.MaxSessionHours()), nil)
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			if ok {
				ui.Row(c).AlignItems(ui.End).Gap(4).Children(func() {
					ui.Textf(c, "%.0f", hrs).FontSize(28).Bold().FontFeatures("tnum").TextColor(pal.TextPrimary).MaxLines(1)
					ui.Text(c, "小时").FontSize(13.5).FontWeight(500).TextColor(pal.TextMuted).Padding(0, 0, 4, 0)
				})
			} else {
				lbl := "—"
				if acc.Tier == core.TierFree {
					lbl = "免费池"
				}
				ui.Text(c, lbl).FontSize(24).Bold().TextColor(pal.TextPrimary)
			}
			ui.Spacer(c)
			ringCol := pal.Emerald
			if ratio < 0.15 {
				ringCol = pal.Amber
			}
			DrawRingGauge(c, pal, ratio, ringCol, 44)
		})
		ui.Spacer(c)
		foot := fmt.Sprintf("%s · %s", acc.Tier.DisplayName(), acc.WorkloadKind.DisplayName())
		if rt.Phase.IsRunning() {
			foot = fmt.Sprintf("本轮剩余 %s · %s", core.FormatDuration(rt.RemainingCapSeconds(acc.Tier)), acc.WorkloadKind.DisplayName())
		} else if ok {
			foot = fmt.Sprintf("按 %s 估算 · %s", basis, acc.WorkloadKind.DisplayName())
		}
		ui.Text(c, foot).FontSize(12).FontFeatures("tnum").TextColor(pal.TextMuted).MaxLines(1).Ellipsis("…")
	}).Height(176).Gap(8)
	if rt.Phase.IsRunning() {
		card.Draw(func(p *ui.Painter, r ui.Rect) {
			p.FillRadialGradient(r, ui.RadialGradient{
				Center: ui.Point{X: 0.78, Y: 0.45},
				Radius: max(r.W, r.H) * 0.72,
				Stops: []ui.GradientStop{
					{Color: WithAlpha(pal.Emerald, 0.06), At: 0.0},
					{Color: WithAlpha(pal.Emerald, 0.0), At: 1.0},
				},
			}, 16)
		})
	}
}

func (m *MainUI) bentoIdleWatchdog(c *ui.Context, pal Palette, acc core.AccountProfile, rt core.AccountRuntimeState) {
	wd := m.Svc.WatchdogStatusFor(acc)
	effectiveMins := core.EffectiveIdleTimeoutMinutes(acc, rt)
	remMin := max(0, (rt.RemainingIdleSeconds(effectiveMins)+59)/60)
	enabled := acc.AutoShutdownEnabled

	BentoCard(c, pal, 176, func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(7).Children(func() {
			dot := pal.TextFaint
			if enabled {
				dot = pal.Cyan
			}
			DrawPrecisionLED(c, dot, 12)
			ui.Text(c, "空闲关机").FontSize(13).FontWeight(500).TextColor(pal.TextMuted)
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

		// Mole 3-pill selector (Video: 10m / 15m / 40m; General/LLM: 15m / 40m / 60m)
		isVideo := !acc.EnableLLMModule && (acc.DeployRecipe.IsVideoComfy() || acc.WorkloadKind == core.WorkloadVideo)
		presets := []int{15, 40, 60}
		activeMins := acc.IdleTimeoutMinutes
		if isVideo {
			presets = []int{10, 15, 40}
			if acc.StopAfterQueueDrained {
				activeMins = acc.QueueDrainMinutes
			}
		}
		ui.Row(c).FillWidth().Gap(6).Children(func() {
			for _, mins := range presets {
				sel := activeMins == mins && acc.AutoShutdownEnabled
				bg := pal.SubtleBg
				fg := pal.TextMuted
				w := 500
				if sel {
					bg = WithAlpha(pal.Amber, 0.24)
					fg = pal.TextPrimary
					w = 600
				}
				minsVal := mins
				ui.Row(c).Grow(1).Padding(5, 0).Radius(7).Background(bg).Justify(ui.Center).AlignItems(ui.Center).OnClick(func() {
					cp := acc
					cp.IdleTimeoutMinutes = minsVal
					if isVideo {
						cp.StopAfterQueueDrained = true
						cp.QueueDrainMinutes = minsVal
					}
					cp.AutoShutdownEnabled = true
					m.Svc.UpdateAccount(cp)
				}).Children(func() {
					ui.Textf(c, "%dm", minsVal).FontSize(12).FontWeight(w).FontFeatures("tnum").TextColor(fg)
				})
			}
		})

		ui.Spacer(c)
		foot := "已关闭，需要自己手动停卡"
		if acc.AutoShutdownEnabled {
			if rt.Phase.IsRunning() {
				foot = fmt.Sprintf("当前空闲已计时 %s", core.FormatDuration(rt.IdleSeconds))
			} else if isVideo && acc.StopAfterQueueDrained {
				foot = fmt.Sprintf("跑完队列 %d 分钟自动关机省点", acc.QueueDrainMinutes)
			} else {
				foot = "闲下来自动关机，不怕忘关烧点"
			}
		}
		ui.Text(c, foot).FontSize(12).TextColor(pal.TextMuted).MaxLines(1)
	})
}

func (m *MainUI) bentoStrategy(c *ui.Context, pal Palette, acc core.AccountProfile) {
	targets := core.EffectiveQueueTargets(acc)
	headline := core.QueueTargetsSummary(targets)
	badge := acc.GPUStrategy.DisplayName()
	t := pal.Amber
	bTint := &t
	fbOn, fbRounds, fbTarget := core.EffectiveFallback(acc)
	BentoCard(c, pal, 176, func() {
		TileHeader(c, pal, pal.Amber, "挑卡策略", badge, bTint)
		ui.Text(c, headline).FontSize(21).Bold().TextColor(pal.TextPrimary).MaxLines(1).Ellipsis("…")
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			hasTag := false
			if fbOn {
				hasTag = true
				Badge(c, pal, fmt.Sprintf("%d轮保底 %s", fbRounds, fbTarget.ShortChipLabel()), nil)
			}
			if acc.ScheduledStartEnabled {
				hasTag = true
				e := pal.Emerald
				Badge(c, pal, fmt.Sprintf("预约 %02d:%02d", acc.ScheduledStartHour, acc.ScheduledStartMinute), &e)
			}
			if !hasTag {
				ui.Text(c, "没货自动顺延下一张卡").FontSize(12).TextColor(pal.TextMuted)
			}
		})
		ui.Spacer(c)
		ui.Row(c).FillWidth().Padding(6, 10).Radius(8).Background(pal.SubtleBg).AlignItems(ui.Center).Cursor(ui.CursorPointer).OnClick(func() {
			m.ShowHomeOptions = !m.ShowHomeOptions
		}).Children(func() {
			ui.Text(c, "调整显卡与选项").FontSize(12).FontWeight(500).TextColor(pal.TextPrimary)
			ui.Spacer(c)
			ui.Text(c, "›").FontSize(14).TextColor(pal.TextMuted)
		})
	})
}

func (m *MainUI) bentoQuickIntegrations(c *ui.Context, pal Palette, acc core.AccountProfile, rt core.AccountRuntimeState) {
	isVideo := !acc.EnableLLMModule && (acc.DeployRecipe.IsVideoComfy() || acc.WorkloadKind == core.WorkloadVideo)
	if isVideo {
		badge := "本机加密"
		var bTint *ui.Color
		if rt.Phase.IsRunning() && rt.DeployRunning {
			badge = "部署中"
			t := pal.Amber
			bTint = &t
		} else if rt.Phase.IsRunning() && rt.DeployFailed {
			badge = "部署中断"
			t := pal.Coral
			bTint = &t
		} else if rt.Phase.IsRunning() && !rt.ServerHealthy {
			badge = "待部署"
			t := pal.Amber
			bTint = &t
		} else if rt.SSHTunnelActive {
			badge = "通道在线"
			t := pal.Emerald
			bTint = &t
		} else if rt.DisplayUIURL != "" {
			badge = "公网就绪"
			t := pal.Sky
			bTint = &t
		}
		BentoCard(c, pal, 176, func() {
			TileHeader(c, pal, pal.Sky, "视频快捷操作", badge, bTint)
			ui.Column(c).FillWidth().Gap(7).Children(func() {
				if rt.Phase.IsRunning() && !rt.ServerHealthy {
					ui.Row(c).FillWidth().Gap(7).Children(func() {
						if rt.DeployRunning {
							m.quickCardBtn(c, pal, "查看日志", true, false, func() {
								m.openDeployLogOverlay(acc.ID)
							})
							m.quickCardBtn(c, pal, "刷新进度", false, false, func() {
								m.Svc.RefreshAccountTelemetryNow(acc.ID)
							})
						} else if rt.DeployFailed {
							m.quickCardBtn(c, pal, "重新部署", true, false, func() {
								m.Svc.DeployNow(acc.ID)
							})
							m.quickCardBtn(c, pal, "查看日志", false, false, func() {
								m.openDeployLogOverlay(acc.ID)
							})
						} else {
							m.quickCardBtn(c, pal, "立即部署", true, false, func() {
								m.Svc.DeployNow(acc.ID)
							})
							m.quickCardBtn(c, pal, "查看日志", false, false, func() {
								m.openDeployLogOverlay(acc.ID)
							})
						}
					})
					ui.Row(c).FillWidth().Gap(7).Children(func() {
						m.quickCardBtn(c, pal, "更多玩法", m.ShowHomeOptions, false, func() {
							m.ShowHomeOptions = true
						})
						m.quickCardBtn(c, pal, "Drive 成片", false, false, func() {
							m.Svc.OpenDriveOutputs()
						})
					})
				} else {
					ui.Row(c).FillWidth().Gap(7).Children(func() {
						m.quickCardBtn(c, pal, "开 ComfyUI", rt.Phase.IsRunning() && rt.ServerHealthy, !rt.Phase.IsRunning(), func() {
							go m.Svc.OpenComfyUI(acc.ID)
						})
						if rt.Phase.IsRunning() {
							m.quickCardBtn(c, pal, "部署日志", m.ShowDeployLog, false, func() {
								m.openDeployLogOverlay(acc.ID)
							})
						} else {
							m.quickCardBtn(c, pal, "更多玩法", m.ShowHomeOptions, false, func() {
								m.ShowHomeOptions = true
							})
						}
					})
					ui.Row(c).FillWidth().Gap(7).Children(func() {
						tunLabel := "本机通道"
						if rt.SSHTunnelActive {
							tunLabel = "断通道"
						}
						tunDisabled := (!rt.Phase.IsRunning() && !rt.SSHTunnelActive) || rt.IsFreeRuntimeRestricted(acc)
						m.quickCardBtn(c, pal, tunLabel, rt.SSHTunnelActive, tunDisabled, func() {
							m.Svc.ToggleSSHTunnel(acc.ID)
						})
						m.quickCardBtn(c, pal, "Drive 成片", false, false, func() {
							m.Svc.OpenDriveOutputs()
						})
					})
				}
			})
			ui.Spacer(c)
		})
		return
	}

	hasTargets := acc.AutoSyncCCSwitch || acc.AutoSyncOpenCode || acc.AutoSyncCodex
	allSynced := hasTargets && (!acc.AutoSyncCCSwitch || rt.CCSwitchSynced) && (!acc.AutoSyncOpenCode || rt.OpenCodeSynced) && (!acc.AutoSyncCodex || rt.CodexSynced)
	badge := "快捷入口"
	var bTint *ui.Color
	if allSynced {
		badge = "已同步"
		t := pal.Emerald
		bTint = &t
	} else if rt.SSHTunnelActive {
		badge = "隧道在线"
		t := pal.Sky
		bTint = &t
	}

	BentoCard(c, pal, 176, func() {
		TileHeader(c, pal, pal.Sky, "接入与快捷", badge, bTint)
		ui.Column(c).FillWidth().Gap(7).Children(func() {
			ui.Row(c).FillWidth().Gap(7).Children(func() {
				syncLabel := "配置接入"
				if allSynced {
					syncLabel = "✓ 已同步"
				} else if rt.PublicURL != "" && hasTargets {
					syncLabel = "同步工具"
				}
				m.quickCardBtn(c, pal, syncLabel, allSynced || m.ShowHomeOptions, false, func() {
					if hasTargets && rt.PublicURL != "" && !allSynced {
						go m.Svc.SyncAllEnabledClients(acc.ID, false)
					} else {
						m.ShowHomeOptions = true
					}
				})

				if acc.EnableLLMModule {
					m.quickCardBtn(c, pal, ">_ Claude", false, !rt.Phase.IsRunning() || rt.PublicURL == "", func() {
						m.Svc.LaunchClaudeTerminal(acc.ID)
					})
				} else if acc.DeployRecipe == core.RecipeNone {
					m.quickCardBtn(c, pal, "更多玩法", m.ShowHomeOptions, false, func() {
						m.ShowHomeOptions = true
					})
				} else {
					m.quickCardBtn(c, pal, acc.WorkloadKind.WebUIButtonTitle(), false, !rt.Phase.IsRunning(), func() {
						m.Svc.OpenWorkloadWebUI(acc.ID)
					})
				}
			})

			ui.Row(c).FillWidth().Gap(7).Children(func() {
				sshLabel := "SSH 隧道"
				if rt.SSHTunnelActive {
					sshLabel = "断隧道"
				}
				sshDisabled := (!rt.Phase.IsRunning() && !rt.SSHTunnelActive) || rt.IsFreeRuntimeRestricted(acc)
				m.quickCardBtn(c, pal, sshLabel, rt.SSHTunnelActive, sshDisabled, func() {
					m.Svc.ToggleSSHTunnel(acc.ID)
				})

				m.quickCardBtn(c, pal, "浏览器", false, !rt.Phase.IsRunning(), func() {
					m.Svc.OpenColabInBrowser(acc.ID)
				})
			})
		})
		ui.Spacer(c)
	})
}

func (m *MainUI) quickCardBtn(c *ui.Context, pal Palette, label string, active, disabled bool, onClick func()) {
	bg := pal.SubtleBg
	border := pal.SubtleBorder
	fg := pal.TextPrimary
	if active {
		bg = WithAlpha(pal.Amber, 0.18)
		border = WithAlpha(pal.Amber, 0.45)
	} else if disabled {
		bg = pal.DisabledBg
		border = pal.DisabledBorder
		fg = pal.TextFaint
	}
	el := ui.Row(c).Grow(1).Padding(8, 6).Radius(8.5).Background(bg).Border(1, border).AlignItems(ui.Center).Justify(ui.Center)
	if !disabled && onClick != nil {
		el.OnClick(onClick)
	}
	el.Children(func() {
		ui.Text(c, label).FontSize(12).FontWeight(500).TextColor(fg).MaxLines(1)
	})
}

// MARK: - Bottom Mole-Style Live Sessions & Accounts Table

func (m *MainUI) renderLiveAccountsTable(c *ui.Context, pal Palette, selected core.AccountProfile) {
	accounts := m.Svc.AccountsSnapshot()
	const (
		colHW   float32 = 116
		colVRAM float32 = 124
		colCU   float32 = 84
		colRate float32 = 76
		colAct  float32 = 116
	)

	PrecisionCard(c, pal, 16, 18, func() {
		ui.Row(c).FillWidth().Gap(10).Padding(0, 6).AlignItems(ui.Center).Children(func() {
			ui.Row(c).Grow(1).MinWidth(220).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Textf(c, "账号与活跃会话 (%d)", len(accounts)).FontSize(14).FontWeight(600).TextColor(pal.TextPrimary).MaxLines(1)
				if m.Svc.TotalManagedRunningSessionCount() >= 2 {
					SmallActionButton(c, pal, "⏻ 全部关机止损", &pal.Coral, false, func() {
						go m.Svc.StopAllManagedSessions()
					})
				}
			})
			ui.Text(c, "硬件与形态").FontSize(12).FontWeight(500).TextColor(pal.TextMuted).Width(colHW).MaxLines(1)
			ui.Text(c, "显存占用").FontSize(12).FontWeight(500).TextColor(pal.TextMuted).Width(colVRAM).MaxLines(1)
			ui.Text(c, "CU 余额").FontSize(12).FontWeight(500).TextColor(pal.TextMuted).Width(colCU).TextAlign(ui.End).MaxLines(1)
			ui.Text(c, "消耗速率").FontSize(12).FontWeight(500).TextColor(pal.TextMuted).Width(colRate).TextAlign(ui.End).MaxLines(1)
			ui.Text(c, "快捷操作").FontSize(12).FontWeight(500).TextColor(pal.TextMuted).Width(colAct).TextAlign(ui.End).MaxLines(1)
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

				ui.Row(c).Grow(1).MinWidth(220).Gap(10).AlignItems(ui.Center).OnClick(func() {
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
							sub = fmt.Sprintf("会话 %s · %s", accCopy.SessionName, modelLabel)
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
					if accCopy.HighMem {
						shape = "High-RAM"
					}
					hwStr = fmt.Sprintf("%s · %s", rt.ShortGPUName(), shape)
					hwCol = pal.TextPrimary
				}
				ui.Text(c, hwStr).FontSize(13).TextColor(hwCol).Width(colHW).MaxLines(1).Ellipsis("…")

				ui.Row(c).Width(colVRAM).Gap(8).AlignItems(ui.Center).Children(func() {
					if rt.VRAMTotalMB > 0 {
						barTint := pal.Emerald
						if rt.VRAMRatio() >= 0.94 {
							barTint = pal.Amber
						}
						ratio := float32(rt.VRAMRatio())
						ui.Box(c).Width(54).Height(6).Draw(func(p *ui.Painter, rect ui.Rect) {
							p.Fill(rect, pal.SubtleBg, 3)
							p.Fill(ui.Rect{X: rect.X, Y: rect.Y, W: rect.W * ratio, H: rect.H}, barTint, 3)
						})
						ui.Textf(c, "%.1fG", float64(rt.VRAMUsedMB)/1024.0).FontSize(12).FontFeatures("tnum").TextColor(pal.TextMuted).MaxLines(1)
					} else {
						ui.Text(c, "—").FontSize(12).TextColor(pal.TextFaint)
					}
				})

				ui.Text(c, core.FormatCU(rt.CUBalance)).FontSize(13.5).FontWeight(600).FontFeatures("tnum").TextColor(pal.TextPrimary).Width(colCU).TextAlign(ui.End).MaxLines(1)

				// Billing-accurate rate display (never hides active extra/orphan session burn)
				rateText := "0.00/h"
				if rt.CURatePerHour > 0 {
					rateText = fmt.Sprintf("%.2f/h", rt.CURatePerHour)
				} else if rt.LastUsageFetchAt == nil {
					rateText = "—"
				}
				offlineBilling := !rt.Phase.IsRunning() && rt.CURatePerHour > 0
				rateCol := pal.TextMuted
				rateW := 400
				if offlineBilling {
					rateCol = pal.Amber
					rateW = 600
				}
				ui.Text(c, rateText).FontSize(12).FontWeight(rateW).FontFeatures("tnum").TextColor(rateCol).Width(colRate).TextAlign(ui.End).MaxLines(1)

				ui.Row(c).Width(colAct).Justify(ui.End).AlignItems(ui.Center).Gap(6).Children(func() {
					if rt.Phase.IsRunning() {
						SmallActionButton(c, pal, "关机", &pal.Coral, false, func() {
							go m.Svc.StopAccountSession(accCopy.ID, "", "")
						})
					} else if rt.Phase.IsQueuing() {
						SmallActionButton(c, pal, "取消", &pal.Coral, false, func() {
							m.Svc.CancelQueue(accCopy.ID)
						})
					} else {
						SmallActionButton(c, pal, "启动", nil, !m.Svc.CLIInstalled, func() {
							m.Svc.StartAccountSession(accCopy.ID)
						})
					}
				})
			})

			// Surface any extra or orphan `[?]` sessions right below the account row
			for _, sess := range rt.Sessions {
				if !sess.IsOrphan && sess.Name == accCopy.SessionName {
					continue
				}
				sessCopy := sess
				ui.Row(c).FillWidth().Gap(10).Padding(5, 6, 5, 24).AlignItems(ui.Center).Children(func() {
					label := fmt.Sprintf("⚠ 附加会话：%s", sessCopy.Name)
					if sessCopy.IsOrphan {
						label = "⚠ 外部孤儿会话 [?] · 请防忘关计费"
					}
					ui.Text(c, label).FontSize(12).FontWeight(500).TextColor(pal.Amber).Grow(1).MinWidth(200).MaxLines(1).Ellipsis("…")
					ui.Textf(c, "%s · %s", sessCopy.Hardware, sessCopy.Shape).FontSize(12).TextColor(pal.TextMuted).Width(colHW).MaxLines(1)
					ui.Box(c).Width(colVRAM)
					ui.Box(c).Width(colCU)
					ui.Box(c).Width(colRate)
					ui.Row(c).Width(colAct).Justify(ui.End).Children(func() {
						if !sessCopy.IsOrphan {
							SmallActionButton(c, pal, "停止", &pal.Coral, false, func() {
								go m.Svc.StopAccountSession(accCopy.ID, sessCopy.Name, "")
							})
						}
					})
				})
			}
		}
	}).Gap(10)
}

func stringsContainsTrimmed(s string) bool {
	return strings.TrimSpace(s) != ""
}
