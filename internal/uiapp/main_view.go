package uiapp

import (
	"fmt"
	"math"
	"runtime"
	"strings"
	"sync"
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
	AgreedRemoteScript   bool
	ShowHomeOptions      bool // Centered overlay inspector for recipe, GPU strategy & options
	ShowDeployLog        bool // Centered overlay showing remote deploy & ComfyUI logs
	ShowErrorRaw         bool // Toggle raw CLI snippet in error banner
	ShowAdvancedSettings bool // Collapsible advanced parameters in Settings (collapsed by default)
	EditingTierAccountID string

	// OAuth2 Add-Account form state
	formMu          sync.Mutex
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

	// History tab filter & pagination state
	HistoryKindFilter core.HistoryEntryKind
	HistoryDaysFilter int
	HistoryAccountID  string
	HistoryPageLimit  int
}

func NewMainUI(svc *app.Service) *MainUI {
	return &MainUI{
		Svc:               svc,
		SelectedCopyProto: integrations.ProtocolOpenAI,
		NewAccTierIdx:     3, // Colab Pro default
		HistoryDaysFilter: 7,
		HistoryPageLimit:  60,
	}
}

func (m *MainUI) isDark() bool {
	if m.ForceTheme == "dark" {
		return true
	}
	if m.ForceTheme == "light" {
		return false
	}
	switch m.Svc.ViewState().ThemePreference {
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

	vs := m.Svc.ViewState()

	ui.Column(c).Fill().LinearGradient(ui.LinearGradient{
		From:  pal.BgTop,
		To:    pal.BgBottom,
		Angle: 180,
	}).Draw(func(p *ui.Painter, r ui.Rect) {
		DrawPrecisionBackdrop(p, r, pal)
	}).Children(func() {
		if vs.ShowingOnboarding {
			m.renderOnboarding(c, pal)
		} else {
			m.renderTopCapsuleBar(c, pal)
			ui.Box(c).FillWidth().Height(1).Background(pal.Divider)

			body := func() {
				ui.Column(c).FillWidth().Padding(16, 24, 22, 24).Gap(14).Children(func() {
					switch vs.SelectedSection {
					case app.SectionHome:
						m.renderStatusPage(c, pal)
					case app.SectionHistory:
						m.renderHistoryPage(c, pal)
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
	if prompt := m.Svc.CurrentActionPrompt(); prompt != nil {
		m.renderActionPromptOverlay(c, pal, prompt)
	}

	// Floating bottom-right toast banner (non-blocking PassThrough so clicks underneath always work)
	if toast := vs.ToastMessage; toast != "" {
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
				scrollMaxH := float32(508)
				cardMaxH := float32(596)
				if m.PreviewMode {
					scrollMaxH = 820
					cardMaxH = 910
				}
				card := PrecisionCard(c, pal, 20, 18, func() {
					ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(12).Children(func() {
						DrawPrecisionLED(c, pal.Amber, 12)
						ui.Text(c, "显卡策略与进阶选项").FontSize(16).FontWeight(600).TextColor(pal.TextPrimary)
						ui.Spacer(c)
						SmallActionButton(c, pal, "完成", nil, false, func() {
							m.ShowHomeOptions = false
						})
					})

					ui.Box(c).FillWidth().Height(1).Background(pal.Divider)

					ui.Scroll(c).FillWidth().Grow(1).MaxHeight(scrollMaxH).Children(func() {
						ui.Column(c).FillWidth().Gap(12).Padding(2, 4, 4, 0).Children(func() {
							m.renderHomeOptionsDrawer(c, pal, acc, rt)
						})
					})
				}).Width(780).MaxHeight(cardMaxH).Gap(12).
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
							ui.Text(c, "云端后台与日志").FontSize(16).FontWeight(600).TextColor(pal.TextPrimary)
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

					// 云端后台直达操作栏 + Drive 目录与画布补模速查
					ui.Column(c).FillWidth().Gap(8).Padding(10, 12).Radius(10).Background(pal.SubtleBg).Border(1, pal.SubtleBorder).Children(func() {
						ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
							ui.Text(c, "云端后台直达").FontSize(12.5).FontWeight(600).TextColor(pal.TextPrimary)
							ui.Spacer(c)
							SmallActionButton(c, pal, "Colab 网页", &pal.Sky, !rt.Phase.IsRunning(), func() {
								m.Svc.OpenColabInBrowser(acc.ID)
							})
							termDisabled := !rt.Phase.IsRunning() || rt.IsFreeRuntimeRestricted(acc)
							SmallActionButton(c, pal, ">_ 云端终端", &pal.Amber, termDisabled, func() {
								go m.Svc.OpenCloudTerminal(acc.ID)
							})
							SmallActionButton(c, pal, "Drive 目录", nil, false, func() {
								m.Svc.OpenDriveOutputs(acc.ID)
							})
							if rt.Phase.IsRunning() && acc.UseGoogleDrive && rt.DriveMountedKnown && !rt.DriveActuallyMounted {
								SmallActionButton(c, pal, "重挂 Drive", &pal.Coral, false, func() {
									go m.Svc.RemountDriveNow(acc.ID)
								})
							}
						})
						ui.Text(c, "Drive 标准目录：workflows/（工作流增量自动备份）· models/<checkpoints|loras|vae|diffusion_models>/（零拷贝直挂）· outputs/（成片直出）").
							FontSize(11.5).TextColor(pal.TextMuted)
						ui.Text(c, "换工作流缺节点/缺模型：在 ComfyUI 顶栏点「Manager」补节点，点「Missing Models」直接在云端高速补齐所需模型。").
							FontSize(11.5).TextColor(pal.TextMuted)
					})

					logText := rt.DeployLogTail
					if rt.DeployLogLoading && logText == "" {
						logText = "正在连接云端机器读取 /tmp/colab-tools-deploy.log 与 /tmp/comfyui.log…"
					} else if logText == "" {
						logText = "点击右上角「刷新日志」拉取云端实时安装记录与端口状态。"
					}
					ui.Scroll(c).FillWidth().Grow(1).MaxHeight(356).Children(func() {
						ui.Column(c).FillWidth().Padding(12, 14).Radius(10).Background(pal.SubtleBg).Border(1, pal.SubtleBorder).Children(func() {
							ui.Text(c, logText).FontSize(11.5).Font("monospace").TextColor(pal.TextPrimary)
						})
					})
				}).Width(820).MaxHeight(596).Gap(12).
					Shadow(0, 20, 50, -8, ui.RGBA(0, 0, 0, 0.45))
			})
	})
}

func (m *MainUI) renderActionPromptOverlay(c *ui.Context, pal Palette, prompt *core.UserActionPrompt) {
	if prompt == nil {
		return
	}
	ui.Overlay(c, func() {
		ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).
			Background(ui.RGBA(0, 0, 0, 0.50))

		ui.Column(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).
			PassThrough().
			Padding(16, 24).
			AlignItems(ui.Center).
			Justify(ui.Center).
			Children(func() {
				PrecisionCard(c, pal, 20, 22, func() {
					led := pal.Amber
					dangerPrimary := false
					switch prompt.Kind {
					case core.PromptComfyHelp:
						led = pal.Sky
					case core.PromptConfirmStop, core.PromptConfirmStopAll, core.PromptConfirmRemoveAccount, core.PromptConfirmQuit:
						led = pal.Coral
						dangerPrimary = true
					case core.PromptTempOutputGuard, core.PromptWebStopGuide, core.PromptSwitchModeWhileRunning:
						led = pal.Amber
					}
					ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
						DrawPrecisionLED(c, led, 13)
						ui.Text(c, prompt.Title).FontSize(16.5).Bold().TextColor(pal.TextPrimary).Grow(1)
					})

					ui.Box(c).FillWidth().Height(1).Background(pal.Divider)

					ui.Column(c).FillWidth().Gap(10).Children(func() {
						renderActionPromptMessage(c, pal, prompt.Message)
						if prompt.URL != "" && (prompt.Kind == core.PromptDriveAuth || (prompt.Kind == core.PromptComfyHelp && prompt.PrimaryAction != "open_colab")) {
							urlBtnLabel := "浏览器没弹出？点此重新打开授权页"
							if prompt.Kind == core.PromptComfyHelp {
								urlBtnLabel = "直接打开 Colab 官方网页"
							}
							ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
								SmallActionButton(c, pal, urlBtnLabel, &pal.Sky, false, func() {
									m.Svc.ResolveActionPromptByID(prompt.ID, "open_url")
								})
							})
						}
					})

					ui.Box(c).FillWidth().Height(1).Background(pal.Divider)

					ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
						if prompt.CancelLabel != "" {
							SmallActionButton(c, pal, prompt.CancelLabel, nil, false, func() {
								m.Svc.ResolveActionPromptByID(prompt.ID, "cancel")
							})
						}
						ui.Spacer(c)
						if prompt.SecondaryLabel != "" {
							PillButton(c, pal, prompt.SecondaryLabel, false, false, func() {
								m.Svc.ResolveActionPromptByID(prompt.ID, "secondary")
							})
						}
						if prompt.PrimaryLabel != "" {
							PrimaryBtn(c, pal, prompt.PrimaryLabel, "", dangerPrimary, false, func() {
								m.Svc.ResolveActionPromptByID(prompt.ID, "primary")
							})
						}
					})
				}).Width(540).Gap(14).
					Shadow(0, 20, 50, -8, ui.RGBA(0, 0, 0, 0.45))
			})
	})
}

func renderActionPromptMessage(c *ui.Context, pal Palette, message string) {
	if !strings.Contains(message, "第 1 步") {
		ui.Text(c, message).FontSize(13.5).TextColor(pal.TextPrimary)
		return
	}
	lines := strings.Split(message, "\n")
	i := 0
	for i < len(lines) {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			i++
			continue
		}
		if strings.HasPrefix(line, "第 ") && strings.Contains(line, "步") {
			var steps []string
			for i < len(lines) {
				s := strings.TrimSpace(lines[i])
				if s == "" {
					i++
					continue
				}
				if strings.HasPrefix(s, "第 ") && strings.Contains(s, "步") {
					steps = append(steps, s)
					i++
				} else {
					break
				}
			}
			ui.Column(c).FillWidth().Padding(12, 14).Radius(10).
				Background(pal.RecessedBg).
				Border(1, pal.RecessedBorder).
				Gap(7).Children(func() {
				for _, step := range steps {
					ui.Text(c, step).FontSize(13).FontWeight(500).TextColor(pal.TextPrimary)
				}
			})
			continue
		}
		var block []string
		for i < len(lines) {
			s := strings.TrimSpace(lines[i])
			if s == "" {
				i++
				continue
			}
			if strings.HasPrefix(s, "第 ") && strings.Contains(s, "步") {
				break
			}
			block = append(block, s)
			i++
		}
		if len(block) > 0 {
			ui.Text(c, strings.Join(block, "\n")).FontSize(13).TextColor(pal.TextMuted)
		}
	}
}

func (m *MainUI) centeredWorkbench(c *ui.Context, fn func()) {
	ui.Column(c).FillWidth().AlignItems(ui.Center).Children(func() {
		ui.Column(c).FillWidth().MaxWidth(920).Gap(16).Children(fn)
	})
}

// MARK: - Top Mole-Style Floating Capsule Bar

func (m *MainUI) renderTopCapsuleBar(c *ui.Context, pal Palette) {
	vs := m.Svc.ViewState()
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
				ui.Text(c, selAcc.DisplayName).FontSize(13.5).FontWeight(600).TextColor(pal.TextPrimary).MaxWidth(140).MaxLines(1).Ellipsis("…")
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
				selected := vs.SelectedSection == sec
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
		pillColor, pillLabel := m.topRightPill(pal, vs.CLIInstalled)
		ui.Row(c).Gap(7).Padding(6, 13).Radius(999).
			Background(pal.SubtleBg).
			Border(1, ui.Color{}).
			BorderLinearGradient(ui.LinearGradient{From: pal.CardBorderTop, To: pal.CardBorderBottom, Angle: 180}).
			InsetShadow(0, 1, 0, 0, pal.CardInnerTopLight).
			AlignItems(ui.Center).
			Cursor(ui.CursorPointer).
			OnClick(func() {
				if !vs.CLIInstalled {
					m.Svc.ReopenOnboarding()
				} else {
					go m.Svc.RefreshAllAccounts(true, false)
				}
			}).Children(func() {
			DrawPrecisionLED(c, pillColor, 10)
			ui.Text(c, pillLabel).FontSize(12.5).FontWeight(500).FontFeatures("tnum").TextColor(pal.TextPrimary).MaxLines(1)
			if vs.CLIInstalled {
				ToolbarIcon(c, "refresh", 13, pal.TextMuted)
			}
		})
	})
}

func (m *MainUI) topRightPill(pal Palette, cliInstalled bool) (ui.Color, string) {
	if !cliInstalled {
		return pal.Coral, "需安装 Colab CLI"
	}
	running := m.Svc.RunningAccountCount()
	if running >= 2 {
		return pal.Emerald, fmt.Sprintf("%d 台已开机", running)
	}
	if running == 1 {
		for _, a := range m.Svc.AccountsSnapshot() {
			rt := m.Svc.StateFor(a.ID)
			if rt.Phase.IsRunning() {
				return pal.Emerald, fmt.Sprintf("已开机 · %s", rt.ShortGPUName())
			}
		}
	}
	for _, a := range m.Svc.AccountsSnapshot() {
		rt := m.Svc.StateFor(a.ID)
		if rt.Phase.IsQueuing() {
			if rt.Phase.QueuePersistent {
				return pal.Amber, fmt.Sprintf("守候 第%d轮", rt.Phase.QueueAttempt)
			}
			return pal.Amber, fmt.Sprintf("排队 %d/%d", rt.Phase.QueueAttempt, rt.Phase.QueueMax)
		}
		if rt.Phase.Kind == core.PhaseStarting {
			return pal.Amber, "正在开机"
		}
	}
	return pal.TextFaint, "已关机"
}

func drawBannerSemanticBar(p *ui.Painter, r ui.Rect, col ui.Color) {
	h := max(float32(16), r.H-16)
	p.Fill(ui.Rect{X: r.X + 4, Y: r.Y + (r.H-h)/2, W: 4, H: h}, col, 2)
}

// MARK: - Tab 1: Mole-Style 3x2 Zero-Duplication Bento Status Dashboard + Conditional Live Session Table

func (m *MainUI) renderStatusPage(c *ui.Context, pal Palette) {
	vs := m.Svc.ViewState()
	if !vs.CLIInstalled {
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
				if vs.EnvReport.ColabBrokenReason != "" {
					detail = vs.EnvReport.ColabBrokenReason
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
		headline := fmt.Sprintf("自动排队抢卡中 · 第 %d/%d 轮（%s）· %s", rt.Phase.QueueAttempt, rt.Phase.QueueMax, rt.Phase.TargetsSummary, countdown)
		subline := "可以关掉或切走窗口去忙别的，顶部菜单栏会实时显示进度，抢到/装好或未抢到都会弹窗响铃通知你。"
		if rt.Phase.QueuePersistent {
			maxH := rt.Phase.QueueMaxHours
			if maxH <= 0 {
				maxH = acc.QueuePersistentMaxHours
				if maxH <= 0 {
					maxH = 8
				}
			}
			headline = fmt.Sprintf("持续守候抢卡中 · 守候 第 %d 轮（上限 %dh）· %s · %s", rt.Phase.QueueAttempt, maxH, rt.Phase.TargetsSummary, countdown)
			subline = "已进入低频防风控守候（约 8–12 分钟/轮），遇限流立刻保护停机，抢到后自动开启闲置关机并响铃通知你。"
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
				ui.Text(c, headline).
					FontSize(13).FontWeight(500).FontFeatures("tnum").TextColor(pal.TextPrimary)
				ui.Text(c, subline).
					FontSize(11.5).TextColor(pal.TextMuted)
			})
			if !rt.Phase.QueuePersistent && !acc.QueuePersistentMode {
				SmallActionButton(c, pal, "切持续守候", &pal.Amber, false, func() {
					cp := acc
					cp.QueuePersistentMode = true
					if cp.QueuePersistentMaxHours <= 0 {
						cp.QueuePersistentMaxHours = 8
					}
					if !cp.AutoShutdownEnabled {
						cp.AutoShutdownEnabled = true
					}
					m.Svc.UpdateAccount(cp)
					m.Svc.ShowToast(fmt.Sprintf("已切为持续守候，下轮起将延续至 %d 小时（无需取消重来）", cp.QueuePersistentMaxHours))
				})
			}
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
					SmallActionButton(c, pal, "停掉本号旧机器", &pal.Coral, false, func() {
						go m.Svc.StopAccountManagedSessions(acc.ID)
					})
				case "reauth":
					SmallActionButton(c, pal, "去重新授权", &pal.Amber, false, func() {
						m.Svc.SetSection(app.SectionAccounts)
					})
				default:
					isH3Orig := acc.DeployRecipe == core.RecipeH3Video && acc.H3Variant == core.H3VariantOriginal
					isShortage := rt.LastAllocHadShortage || strings.Contains(rt.LastError, "无货") || strings.Contains(rt.LastError, "没货") || strings.Contains(rt.LastError, "503")
					if isH3Orig {
						SmallActionButton(c, pal, "改用 L4 轻量版", &pal.Emerald, !vs.CLIInstalled, func() {
							m.Svc.SwitchToLightGGUFAndStart(acc.ID)
						})
					}
					if (isShortage || isH3Orig) && !acc.QueuePersistentMode {
						SmallActionButton(c, pal, "开启 8h 持续守候", &pal.Sky, !vs.CLIInstalled, func() {
							cp := acc
							cp.QueuePersistentMode = true
							m.Svc.UpdateAccount(cp)
							m.Svc.DeployNow(acc.ID)
						})
					}
					SmallActionButton(c, pal, "直接重试", &pal.Amber, !vs.CLIInstalled, func() {
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
	} else if !rt.Phase.IsRunning() && !rt.Phase.IsQueuing() && rt.LastUnexpectedStopMsg != "" {
		ui.Row(c).FillWidth().Padding(11, 14, 11, 18).Radius(14).
			Background(WithAlpha(pal.Amber, 0.11)).
			Border(1, WithAlpha(pal.Amber, 0.32)).
			InsetShadow(0, 1, 0, 0, pal.CardInnerTopLight).
			DrawOver(func(p *ui.Painter, r ui.Rect) {
				drawBannerSemanticBar(p, r, pal.Amber)
			}).
			AlignItems(ui.Center).Gap(10).Children(func() {
			DrawPrecisionLED(c, pal.Amber, 12)
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				ui.Text(c, "上次云端机器已被 Google 自动回收停机").FontSize(13.5).FontWeight(600).TextColor(pal.TextPrimary)
				ui.Text(c, rt.LastUnexpectedStopMsg).FontSize(12).TextColor(pal.TextMuted).MaxLines(2).Ellipsis("…")
			})
			SmallActionButton(c, pal, "查看记录", nil, false, func() {
				m.Svc.SetSection(app.SectionHistory)
			})
			SmallActionButton(c, pal, "✕", nil, false, func() {
				m.Svc.ClearUnexpectedStopNotice(acc.ID)
			})
		})
	}

	// Primary Top Power & Live Stopwatch Control Strip: [Left: Machine + Memory] [Center: Clickable Power + Stopwatch] [Right: Available Time]
	m.renderPowerHeroStrip(c, pal, acc, rt)

	// Unified 3x2 Zero-Duplication Bento Dashboard (both Idle and Active)
	ui.Grid(c).FillWidth().ColumnTracks(ui.Fr(1), ui.Fr(1), ui.Fr(1)).Gap(14).Children(func() {
		m.bentoBillingCU(c, pal, acc, rt)
		m.bentoLiveLoad(c, pal, acc, rt)
		m.bentoCurrentTask(c, pal, acc, rt)
		m.bentoWorkloadOutput(c, pal, acc, rt)
		m.bentoIdleWatchdog(c, pal, acc, rt)
		m.bentoStrategy(c, pal, acc)
	})

	// Unified Accounts & Running Cloud Machines Table (hidden when single-account & 0 running)
	m.renderAccountsAndMachinesTable(c, pal, acc)
}

func (m *MainUI) renderPowerHeroStrip(c *ui.Context, pal Palette, acc core.AccountProfile, rt core.AccountRuntimeState) {
	isRunning := rt.Phase.IsRunning()
	isQueuing := rt.Phase.IsQueuing()
	isStarting := rt.Phase.Kind == core.PhaseStarting
	isStopping := rt.Phase.Kind == core.PhaseStopping
	isCooling := rt.QueueCircuitBreakTill != nil && rt.QueueCircuitBreakTill.After(time.Now())

	targets := core.EffectiveQueueTargets(acc)
	targetsSummary := core.QueueTargetsSummary(targets)

	// Compute right-side available time
	remCapSec := rt.RemainingCapSecondsForAccount(acc)
	hrs, basis, hasEst := rt.EstimatedHoursRemaining(acc)

	card := PrecisionCard(c, pal, 16, 14, func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(16).Children(func() {
			// LEFT: Allocated machine + VRAM & RAM info
			ui.Column(c).Grow(1).MinWidth(220).Gap(4).Children(func() {
				leftLED := pal.TextFaint
				leftTitle := "云端机器与内存"
				if isRunning {
					leftLED = pal.Emerald
				} else if isQueuing || isStarting {
					leftLED = pal.Amber
					leftTitle = "正在申请机器"
				}
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					DrawPrecisionLED(c, leftLED, 10)
					ui.Text(c, leftTitle).FontSize(12).FontWeight(500).TextColor(pal.TextMuted)
				})

				if isRunning {
					shape := "Standard RAM"
					if core.EffectiveUseHighMem(acc) && !rt.SessionUsedStandardRAM {
						shape = "High-RAM"
					}
					ui.Row(c).Gap(7).AlignItems(ui.Center).Children(func() {
						ui.Text(c, rt.ShortGPUName()).FontSize(21).Bold().TextColor(pal.TextPrimary).MaxLines(1)
						e := pal.Emerald
						Badge(c, pal, shape, &e)
					})
					memLine := fmt.Sprintf("显存 — · %s", shape)
					if rt.VRAMTotalMB > 0 && rt.RAMTotalMB > 0 {
						memLine = fmt.Sprintf("显存 %.1f/%.1f GB · 内存 %.1f/%.0f GB",
							float64(rt.VRAMUsedMB)/1024.0, float64(rt.VRAMTotalMB)/1024.0,
							float64(rt.RAMUsedMB)/1024.0, float64(rt.RAMTotalMB)/1024.0)
					} else if rt.VRAMTotalMB > 0 {
						memLine = fmt.Sprintf("显存 %.1f/%.1f GB · %s",
							float64(rt.VRAMUsedMB)/1024.0, float64(rt.VRAMTotalMB)/1024.0, shape)
					} else if rt.RAMTotalMB > 0 {
						memLine = fmt.Sprintf("内存 %.1f/%.0f GB · 显存同步中",
							float64(rt.RAMUsedMB)/1024.0, float64(rt.RAMTotalMB)/1024.0)
					}
					ui.Text(c, memLine).FontSize(12).FontFeatures("tnum").TextColor(pal.TextMuted).MaxLines(1).Ellipsis("…")
				} else if isQueuing || isStarting {
					tgt := rt.Phase.TargetsSummary
					if tgt == "" {
						tgt = targetsSummary
					}
					ui.Row(c).Gap(7).AlignItems(ui.Center).Children(func() {
						ui.Text(c, tgt).FontSize(19).Bold().TextColor(pal.TextPrimary).MaxLines(1).Ellipsis("…")
						a := pal.Amber
						if rt.Phase.QueuePersistent {
							Badge(c, pal, fmt.Sprintf("守候 第%d轮", max(1, rt.Phase.QueueAttempt)), &a)
						} else if isQueuing {
							Badge(c, pal, fmt.Sprintf("第 %d/%d 轮", max(1, rt.Phase.QueueAttempt), max(1, rt.Phase.QueueMax)), &a)
						}
					})
					sub := "正在向官方申请显卡与内存…"
					if rt.Phase.Detail != "" {
						sub = rt.Phase.Detail
					}
					ui.Text(c, sub).FontSize(12).TextColor(pal.TextMuted).MaxLines(1).Ellipsis("…")
				} else {
					ui.Row(c).Gap(7).AlignItems(ui.Center).Children(func() {
						ui.Text(c, "未分配机器").FontSize(19).Bold().TextColor(pal.TextMuted).MaxLines(1)
						Badge(c, pal, "已停机", nil)
					})
					preCol := pal.TextMuted
					if acc.DeployRecipe.IsVideoComfy() && !acc.UseGoogleDrive {
						preCol = pal.Amber
					}
					ui.Text(c, core.PrelaunchSummary(acc)).FontSize(12).FontFeatures("tnum").TextColor(preCol).MaxLines(1).Ellipsis("…")
				}
			})

			// CENTER: Unmistakable Power Status & Live Stopwatch Button (click when offline to start)
			centerBg := pal.SubtleBg
			centerBorder := WithAlpha(pal.Amber, 0.44)
			iconBg := WithAlpha(pal.TextFaint, 0.14)
			iconCol := pal.TextMuted
			stateLbl := "已关机"
			stateCol := pal.TextPrimary
			subBadge := "点击开机"
			subBadgeCol := pal.Amber
			watchText := "00:00:00"
			watchCol := pal.TextFaint
			clickable := !isCooling && m.Svc.ViewState().CLIInstalled
			var onCenterClick func()

			if isRunning {
				centerBg = WithAlpha(pal.Emerald, 0.13)
				centerBorder = WithAlpha(pal.Emerald, 0.50)
				iconBg = WithAlpha(pal.Emerald, 0.22)
				iconCol = pal.Emerald
				stateLbl = "已开机"
				stateCol = pal.Emerald
				subBadge = "点此关机"
				subBadgeCol = pal.Coral
				watchText = core.FormatStopwatch(rt.SessionUptimeSec)
				watchCol = pal.TextPrimary
				clickable = true
				onCenterClick = func() { m.Svc.RequestConfirmStop(acc.ID) }
			} else if isQueuing || isStarting {
				centerBg = WithAlpha(pal.Amber, 0.13)
				centerBorder = WithAlpha(pal.Amber, 0.48)
				iconBg = WithAlpha(pal.Amber, 0.22)
				iconCol = pal.Amber
				if rt.Phase.QueuePersistent {
					stateLbl = "守候中"
				} else if isQueuing {
					stateLbl = "抢卡中"
				} else {
					stateLbl = "开机中"
				}
				stateCol = pal.Amber
				subBadge = "等待中"
				subBadgeCol = pal.Amber
				waitSec := 0
				if !rt.QueueStartedAt.IsZero() {
					waitSec = max(0, int(time.Since(rt.QueueStartedAt).Seconds()))
				}
				watchText = core.FormatStopwatch(waitSec)
				watchCol = pal.TextPrimary
				clickable = isQueuing
				if isQueuing {
					subBadge = "点此取消"
					onCenterClick = func() { m.Svc.CancelQueue(acc.ID) }
				}
			} else if isStopping {
				centerBg = WithAlpha(pal.Coral, 0.12)
				centerBorder = WithAlpha(pal.Coral, 0.42)
				iconBg = WithAlpha(pal.Coral, 0.20)
				iconCol = pal.Coral
				stateLbl = "关机中"
				stateCol = pal.Coral
				subBadge = "释放中"
				subBadgeCol = pal.Coral
				watchText = core.FormatStopwatch(rt.SessionUptimeSec)
				watchCol = pal.TextMuted
				clickable = false
			} else {
				if !clickable {
					centerBorder = pal.SubtleBorder
					subBadge = "暂不可用"
					subBadgeCol = pal.TextFaint
				} else {
					onCenterClick = func() { m.Svc.DeployNow(acc.ID) }
				}
			}

			powerEl := ui.Row(c).Width(256).Padding(10, 14).Radius(13).
				AlignItems(ui.Center).Gap(12)
			if clickable && powerEl.Hovered() {
				if isRunning {
					centerBg = WithAlpha(pal.Coral, 0.14)
					centerBorder = WithAlpha(pal.Coral, 0.56)
					iconBg = WithAlpha(pal.Coral, 0.22)
					iconCol = pal.Coral
					stateCol = pal.Coral
				} else if isQueuing {
					centerBg = WithAlpha(pal.Coral, 0.13)
					centerBorder = WithAlpha(pal.Coral, 0.52)
				} else {
					centerBg = WithAlpha(pal.Amber, 0.10)
					centerBorder = WithAlpha(pal.Amber, 0.65)
				}
			}
			powerEl.Background(centerBg).Border(1.2, centerBorder)
			if clickable && onCenterClick != nil {
				powerEl.Cursor(ui.CursorPointer).OnClick(onCenterClick)
			}
			powerEl.Children(func() {
				// Crisp circular power emblem
				ui.Row(c).Width(38).Height(38).Radius(19).Background(iconBg).Border(1, WithAlpha(iconCol, 0.45)).
					AlignItems(ui.Center).Justify(ui.Center).Children(func() {
					ui.Text(c, "⏻").FontSize(18).Bold().TextColor(iconCol)
				})
				ui.Column(c).Grow(1).Gap(2).Children(func() {
					ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Children(func() {
						ui.Text(c, stateLbl).FontSize(15).Bold().TextColor(stateCol).MaxLines(1)
						ui.Spacer(c)
						ui.Text(c, subBadge).FontSize(11).FontWeight(600).TextColor(subBadgeCol).MaxLines(1)
					})
					ui.Text(c, watchText).FontSize(20).Bold().FontFeatures("tnum").TextColor(watchCol).MaxLines(1)
				})
			})

			// RIGHT: Available time (remaining session cap + estimated balance runtime)
			ui.Column(c).Grow(1).MinWidth(220).AlignItems(ui.End).Gap(4).Children(func() {
				rightLED := pal.TextFaint
				rightTitle := "预计可用时间"
				if isRunning {
					rightTitle = "本轮可用时间"
					rightLED = pal.Emerald
					if remCapSec < 2*3600 {
						rightLED = pal.Amber
					}
				}
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Text(c, rightTitle).FontSize(12).FontWeight(500).TextColor(pal.TextMuted)
					DrawPrecisionLED(c, rightLED, 10)
				})

				if isRunning {
					remCol := pal.TextPrimary
					if remCapSec < 2*3600 {
						remCol = pal.Amber
					}
					ui.Row(c).Gap(5).AlignItems(ui.End).Children(func() {
						ui.Text(c, core.FormatDuration(remCapSec)).FontSize(21).Bold().FontFeatures("tnum").TextColor(remCol).MaxLines(1)
						ui.Text(c, "剩余").FontSize(12).TextColor(pal.TextMuted).Padding(0, 0, 2, 0)
					})
					detail := fmt.Sprintf("单次上限 %dh", acc.MaxSessionHours())
					if hasEst {
						detail = fmt.Sprintf("余额可跑约 %.0fh · 上限 %dh", hrs, acc.MaxSessionHours())
					}
					if acc.AutoShutdownEnabled && !rt.ActivitySignal.IsBusy() {
						effMins := rt.EffectiveSessionIdleTimeout(acc)
						remIdle := max(0, (rt.RemainingIdleSeconds(effMins)+59)/60)
						detail = fmt.Sprintf("余额可跑 %.0fh · 空闲停卡 %dm", hrs, remIdle)
						if !hasEst {
							detail = fmt.Sprintf("空闲停卡倒计时 %d 分钟", remIdle)
						}
					}
					ui.Text(c, detail).FontSize(12).FontFeatures("tnum").TextColor(pal.TextMuted).MaxLines(1).Ellipsis("…")
				} else {
					if rt.LastUsageFetchAt == nil {
						if rt.LastError != "" {
							ui.Text(c, "—").FontSize(21).Bold().TextColor(pal.TextMuted)
							ui.Text(c, "点数读取失败 · 点右上角刷新").FontSize(12).TextColor(pal.Amber).MaxLines(1).Ellipsis("…")
						} else {
							ui.Text(c, "读取中…").FontSize(19).Bold().TextColor(pal.TextMuted).MaxLines(1)
							ui.Text(c, "正在读取账号点数与运行状态").FontSize(12).TextColor(pal.TextFaint).MaxLines(1).Ellipsis("…")
						}
					} else {
						if hasEst {
							ui.Row(c).Gap(4).AlignItems(ui.End).Children(func() {
								ui.Textf(c, "%.0f", hrs).FontSize(21).Bold().FontFeatures("tnum").TextColor(pal.TextPrimary).MaxLines(1)
								ui.Text(c, "小时").FontSize(12.5).FontWeight(500).TextColor(pal.TextMuted).Padding(0, 0, 2, 0)
							})
						} else if acc.IsFreeOnly() {
							ui.Text(c, "免费池 · 12h").FontSize(19).Bold().TextColor(pal.TextPrimary).MaxLines(1)
						} else {
							ui.Text(c, "0.0 CU").FontSize(19).Bold().FontFeatures("tnum").TextColor(pal.TextMuted).MaxLines(1)
						}
						subRight := fmt.Sprintf("当前未扣点 · 0.00 CU/h（按 %s 估）", basis)
						if !hasEst {
							subRight = "当前未扣点 · 0.00 CU/h"
						}
						subRightCol := pal.TextFaint
						if rt.CURatePerHour > 0 {
							subRight = fmt.Sprintf("Colab 网页端扣点中 · %.2f CU/h", rt.CURatePerHour)
							subRightCol = pal.Amber
						}
						ui.Text(c, subRight).FontSize(12).FontFeatures("tnum").TextColor(subRightCol).MaxLines(1).Ellipsis("…")
					}
				}
			})
		})
	})
	if isRunning {
		card.Draw(func(p *ui.Painter, r ui.Rect) {
			p.FillRadialGradient(r, ui.RadialGradient{
				Center: ui.Point{X: 0.5, Y: 0.5},
				Radius: max(r.W, r.H) * 0.48,
				Stops: []ui.GradientStop{
					{Color: WithAlpha(pal.Emerald, 0.07), At: 0.0},
					{Color: WithAlpha(pal.Emerald, 0.0), At: 1.0},
				},
			}, 16)
		})
	}
}
