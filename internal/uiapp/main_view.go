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

func timeNow() time.Time { return time.Now() }

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

					ui.Scroll(c).FillWidth().Grow(1).MaxHeight(508).Children(func() {
						ui.Column(c).FillWidth().Gap(12).Padding(2, 4, 4, 0).Children(func() {
							m.renderHomeOptionsDrawer(c, pal, acc, rt)
						})
					})
				}).Width(780).MaxHeight(596).Gap(12).
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
							if rt.Phase.IsRunning() && (rt.CloudflaredActive || strings.Contains(rt.DisplayUIURL, "trycloudflare.com")) {
								SmallActionButton(c, pal, "关公网通道", &pal.Coral, false, func() {
									go m.Svc.StopCloudflaredTunnel(acc.ID)
								})
							}
						})
						ui.Text(c, "Drive 标准目录：workflows/（工作流自动双向同步）· models/<checkpoints|loras|vae|diffusion_models>/（零拷贝直挂）· outputs/（成片直出）").
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
					case core.PromptTempOutputGuard, core.PromptWebStopGuide:
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
							actionModalPrimaryButton(c, pal, prompt.PrimaryLabel, dangerPrimary, func() {
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

func actionModalPrimaryButton(c *ui.Context, pal Palette, label string, danger bool, onClick func()) {
	el := ui.Row(c).
		Height(36).
		Padding(0, 18).
		Radius(9).
		Gap(6).
		Shrink(0).
		AlignItems(ui.Center).
		Justify(ui.Center)

	if onClick != nil {
		el.OnClick(onClick)
	}
	fg := pal.PrimaryBtnFg
	if danger {
		hovered := el.Hovered()
		topCol := pal.Coral
		botCol := pal.Coral.Mix(ui.RGB(0, 0, 0), 0.14)
		shadowBlur := float32(16)
		shadowAlpha := float32(0.36)
		if hovered {
			topCol = topCol.Mix(ui.RGB(255, 255, 255), 0.08)
			botCol = botCol.Mix(ui.RGB(255, 255, 255), 0.06)
			shadowBlur = 20
			shadowAlpha = 0.46
		}
		el.LinearGradient(ui.LinearGradient{From: topCol, To: botCol, Angle: 180}).
			Border(1, WithAlpha(pal.Coral, 0.85)).
			InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.28)).
			Shadow(0, 6, shadowBlur, -3, WithAlpha(pal.Coral, shadowAlpha)).
			Cursor(ui.CursorPointer)
		fg = ui.RGB(255, 255, 255)
	} else {
		styleAmberMetallicButton(el, pal, false)
	}
	el.Children(func() {
		ui.Text(c, label).FontSize(13.5).FontWeight(600).TextColor(fg).MaxLines(1)
	})
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
					if isShortage || isH3Orig {
						SmallActionButton(c, pal, "预约 07:00 抢卡", &pal.Sky, false, func() {
							m.Svc.QuickScheduleMorningStart(acc.ID, 7, 0)
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
		m.bentoCurrentTask(c, pal, acc)
		m.bentoWorkloadOutput(c, pal, acc, rt)
		m.bentoIdleWatchdog(c, pal, acc, rt)
		m.bentoStrategy(c, pal, acc)
	})

	// Unified Running Machines Card (shows every active GPU across managed & external sessions; hidden when 0 running)
	accounts := m.Svc.AccountsSnapshot()
	runningRows, totalBurnRate, hasRateSample := m.collectRunningMachineRows(accounts)
	if len(runningRows) > 0 {
		m.renderRunningMachinesCard(c, pal, runningRows, totalBurnRate, hasRateSample, len(accounts) >= 2)
	}

	// Multi-Account Overview Table (only when 2+ accounts are bound)
	if len(accounts) >= 2 {
		m.renderLiveAccountsTable(c, pal, acc)
	}
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
	remCapSec := rt.RemainingCapSeconds(acc.Tier)
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
					ui.Textf(c, "开机优先 %s · 显存/内存待开机", targetsSummary).FontSize(12).TextColor(pal.TextFaint).MaxLines(1).Ellipsis("…")
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
					detail := fmt.Sprintf("单次上限 %dh", acc.Tier.MaxSessionHours())
					if hasEst {
						detail = fmt.Sprintf("余额可跑约 %.0fh · 上限 %dh", hrs, acc.Tier.MaxSessionHours())
					}
					if acc.AutoShutdownEnabled && !rt.ActivitySignal.IsBusy() {
						effMins := core.EffectiveIdleTimeoutMinutes(acc, rt)
						remIdle := max(0, (rt.RemainingIdleSeconds(effMins)+59)/60)
						detail = fmt.Sprintf("余额可跑 %.0fh · 空闲停卡 %dm", hrs, remIdle)
						if !hasEst {
							detail = fmt.Sprintf("空闲停卡倒计时 %d 分钟", remIdle)
						}
					}
					ui.Text(c, detail).FontSize(12).FontFeatures("tnum").TextColor(pal.TextMuted).MaxLines(1).Ellipsis("…")
				} else {
					if hasEst {
						ui.Row(c).Gap(4).AlignItems(ui.End).Children(func() {
							ui.Textf(c, "%.0f", hrs).FontSize(21).Bold().FontFeatures("tnum").TextColor(pal.TextPrimary).MaxLines(1)
							ui.Text(c, "小时").FontSize(12.5).FontWeight(500).TextColor(pal.TextMuted).Padding(0, 0, 2, 0)
						})
					} else if acc.Tier == core.TierFree {
						ui.Text(c, "免费池 · 12h").FontSize(19).Bold().TextColor(pal.TextPrimary).MaxLines(1)
					} else {
						ui.Text(c, "—").FontSize(21).Bold().TextColor(pal.TextMuted)
					}
					subRight := fmt.Sprintf("当前未扣点 · 0.00 CU/h（按 %s 估）", basis)
					if !hasEst {
						subRight = "当前未扣点 · 0.00 CU/h"
					}
					subRightCol := pal.TextFaint
					if len(rt.Sessions) > 0 && rt.CURatePerHour > 0 {
						subRight = fmt.Sprintf("Colab 网页端扣点中 · %.2f CU/h", rt.CURatePerHour)
						subRightCol = pal.Amber
					}
					ui.Text(c, subRight).FontSize(12).FontFeatures("tnum").TextColor(subRightCol).MaxLines(1).Ellipsis("…")
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

const homeBentoCardHeight float32 = 184

// Card 1: CU 余额与速率（纯财务视角，无开关机按钮）
func (m *MainUI) bentoBillingCU(c *ui.Context, pal Palette, acc core.AccountProfile, rt core.AccountRuntimeState) {
	webBilling := !rt.Phase.IsRunning() && len(rt.Sessions) > 0 && rt.CURatePerHour > 0
	led := pal.Emerald
	if webBilling {
		led = pal.Amber
	} else if !rt.Phase.IsRunning() {
		led = pal.TextFaint
	}

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
		} else if acc.Tier == core.TierFree {
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
	}

	card := PrecisionCard(c, pal, 16, 16, func() {
		TileHeader(c, pal, led, "CU 余额与速率", acc.Tier.ShortBadge(), nil)

		ui.Column(c).FillWidth().Gap(4).Children(func() {
			ui.Row(c).AlignItems(ui.End).Gap(5).Children(func() {
				ui.Text(c, core.FormatCU(rt.CUBalance)).FontSize(28).Bold().FontFeatures("tnum").TextColor(pal.TextPrimary).MaxLines(1)
				ui.Text(c, "CU").FontSize(13).FontWeight(500).TextColor(pal.TextMuted).Padding(0, 0, 4, 0)
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

// Card 3: 当前任务（全天候常驻，开机后绝不变脸消失）
func (m *MainUI) bentoCurrentTask(c *ui.Context, pal Palette, acc core.AccountProfile) {
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
			taskFoot = "24G L4 友好 · 省点稳定"
		case core.H3VariantCustom:
			taskBadge = "自定义 H3"
			taskFoot = "自定义 HF 仓库 / 仅用 Drive"
		}
	case core.RecipeComfyUIGeneral:
		taskBadge = "ComfyUI"
		taskFoot = "纯净 ComfyUI · 自由装节点"
		badgeTint = pal.Violet
	case core.RecipeLLMProxy:
		taskBadge = "大模型"
		taskFoot = "开机自动同步编程工具"
		badgeTint = pal.Sky
	case core.RecipeNone:
		taskBadge = "纯开机"
		taskFoot = "纯净裸机环境 · 仅抢卡与守护"
		badgeTint = pal.TextMuted
	}

	flashSel := acc.DeployRecipe == core.RecipeH3Video && acc.H3Variant == core.H3VariantFlash
	origSel := acc.DeployRecipe == core.RecipeH3Video && acc.H3Variant == core.H3VariantOriginal
	ggufSel := acc.DeployRecipe == core.RecipeH3Video && acc.H3Variant == core.H3VariantFlashGGUF
	llmSel := acc.DeployRecipe == core.RecipeLLMProxy

	BentoCard(c, pal, homeBentoCardHeight, func() {
		TileHeader(c, pal, badgeTint, "当前任务", taskBadge, &badgeTint)
		ui.Grid(c).FillWidth().ColumnTracks(ui.Fr(1), ui.Fr(1)).Gap(6).Children(func() {
			m.bentoTaskChip(c, pal, "H3 Flash 极速版", flashSel, func() {
				m.selectH3Preset(acc, core.H3VariantFlash)
			})
			m.bentoTaskChip(c, pal, "H3 原版 (31B)", origSel, func() {
				m.selectH3Preset(acc, core.H3VariantOriginal)
			})
			m.bentoTaskChip(c, pal, "轻量 GGUF", ggufSel, func() {
				m.selectH3Preset(acc, core.H3VariantFlashGGUF)
			})
			m.bentoTaskChip(c, pal, "大模型代理", llmSel, func() {
				m.DraftAccountID = ""
				m.Svc.ApplyDeployRecipe(acc.ID, core.RecipeLLMProxy)
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

// Card 4: 运行与产出（合并部署/队列与高频快捷动作，零 SSH 隧道按钮、零更多玩法）
func (m *MainUI) bentoWorkloadOutput(c *ui.Context, pal Palette, acc core.AccountProfile, rt core.AccountRuntimeState) {
	kind := acc.WorkloadKind
	if kind == core.WorkloadGeneral && rt.DetectedWorkloadKind != nil {
		kind = *rt.DetectedWorkloadKind
	}
	tint := WorkloadColor(pal, kind)
	isLLM := acc.DeployRecipe == core.RecipeLLMProxy || kind == core.WorkloadLLM
	isVideo := !acc.EnableLLMModule && (acc.DeployRecipe.IsVideoComfy() || kind == core.WorkloadVideo)

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
			if allSynced {
				syncLabel = "✓ 已同步"
			}
			syncDisabled := !rt.Phase.IsRunning() || rt.PublicURL == ""
			ui.Row(c).FillWidth().Gap(7).Children(func() {
				m.quickCardBtn(c, pal, syncLabel, allSynced, syncDisabled, func() {
					go m.Svc.SyncAllEnabledClients(acc.ID, false)
				})
				m.quickCardBtn(c, pal, ">_ Claude", false, !rt.Phase.IsRunning() || rt.PublicURL == "", func() {
					m.Svc.LaunchClaudeTerminal(acc.ID)
				})
			})
			return
		}

		if isVideo {
			title := "视频画布与产出"
			badge := "纯云端"
			var bTint *ui.Color
			driveFallbackTemp := rt.Phase.IsRunning() && acc.UseGoogleDrive && rt.DriveMountedKnown && !rt.DriveActuallyMounted
			publicTunnelOpen := rt.Phase.IsRunning() && (rt.CloudflaredActive || strings.Contains(rt.DisplayUIURL, "trycloudflare.com"))
			if acc.UseGoogleDrive {
				badge = "Drive 存片"
				e := pal.Emerald
				bTint = &e
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
				} else if driveFallbackTemp {
					rCol := pal.Coral
					bTint = &rCol
					if rt.RenderedVideoCount > 0 {
						badge = fmt.Sprintf("临时盘·%d条", rt.RenderedVideoCount)
					} else {
						badge = "临时盘(关机清)"
					}
				} else if publicTunnelOpen {
					a := pal.Amber
					bTint = &a
					badge = "公网无密"
				} else if rt.RenderedVideoCount > 0 {
					badge = fmt.Sprintf("已出 %d 条", rt.RenderedVideoCount)
					e := pal.Emerald
					bTint = &e
				} else if rt.ServerHealthy {
					e := pal.Emerald
					bTint = &e
					if acc.UseGoogleDrive && (!rt.DriveMountedKnown || rt.DriveActuallyMounted) {
						badge = "Drive 存片"
					} else {
						badge = "已就绪"
					}
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
					} else if rt.ServerHealthy {
						ui.Text(c, "已就绪").FontSize(23).Bold().TextColor(pal.Emerald).MaxLines(1)
						ui.Text(c, ":8188").FontSize(13).FontFeatures("tnum").TextColor(pal.TextMuted).Padding(0, 0, 3, 0).MaxLines(1)
					} else {
						ui.Text(c, "未部署").FontSize(23).Bold().TextColor(pal.Amber).MaxLines(1)
						ui.Text(c, "裸机").FontSize(12.5).FontWeight(500).TextColor(pal.TextMuted).Padding(0, 0, 3, 0).MaxLines(1)
					}
				} else {
					subNote := "纯云端暂存"
					if acc.UseGoogleDrive {
						subNote = "成片直存 Drive"
					}
					ui.Text(c, "待开机").FontSize(23).Bold().TextColor(pal.TextMuted).MaxLines(1)
					ui.Text(c, subNote).FontSize(12).FontWeight(500).TextColor(pal.TextFaint).Padding(0, 0, 3, 0).MaxLines(1).Ellipsis("…")
				}
			})

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

			ui.Spacer(c)

			ui.Row(c).FillWidth().Gap(6).Children(func() {
				if rt.Phase.IsRunning() && rt.ServerHealthy {
					comfyLabel := "开 ComfyUI"
					if rt.ComfyOpening {
						comfyLabel = "连接中…"
					}
					m.quickCardBtn(c, pal, comfyLabel, !rt.ComfyOpening, rt.ComfyOpening, func() {
						go m.Svc.OpenComfyUI(acc.ID)
					})
					if driveFallbackTemp {
						m.quickCardBtn(c, pal, "重挂 Drive", true, false, func() {
							go m.Svc.RemountDriveNow(acc.ID)
						})
					} else if publicTunnelOpen {
						m.quickCardBtn(c, pal, "关公网通道", true, false, func() {
							go m.Svc.StopCloudflaredTunnel(acc.ID)
						})
					} else {
						m.quickCardBtn(c, pal, "Drive 成片", false, false, func() {
							m.Svc.OpenDriveOutputs(acc.ID)
						})
					}
					m.quickCardCompactBtn(c, pal, "后台", m.ShowDeployLog, false, func() {
						m.openDeployLogOverlay(acc.ID)
					})
				} else if rt.Phase.IsRunning() && rt.DeployRunning {
					m.quickCardBtn(c, pal, "查看日志", true, false, func() {
						m.openDeployLogOverlay(acc.ID)
					})
					m.quickCardBtn(c, pal, "刷新进度", false, false, func() {
						m.Svc.RefreshAccountTelemetryNow(acc.ID)
					})
				} else if rt.Phase.IsRunning() && rt.DeployFailed {
					m.quickCardBtn(c, pal, "重新部署", true, false, func() {
						m.Svc.DeployNow(acc.ID)
					})
					m.quickCardBtn(c, pal, "查看日志", false, false, func() {
						m.openDeployLogOverlay(acc.ID)
					})
				} else if rt.Phase.IsRunning() {
					m.quickCardBtn(c, pal, "立即部署", true, false, func() {
						m.Svc.DeployNow(acc.ID)
					})
					m.quickCardBtn(c, pal, "查看日志", false, false, func() {
						m.openDeployLogOverlay(acc.ID)
					})
				} else {
					m.quickCardBtn(c, pal, "开 ComfyUI", false, true, nil)
					m.quickCardBtn(c, pal, "Drive 成片", false, false, func() {
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

		ui.Row(c).FillWidth().Gap(7).Children(func() {
			m.quickCardBtn(c, pal, acc.WorkloadKind.WebUIButtonTitle(), rt.Phase.IsRunning(), !rt.Phase.IsRunning(), func() {
				go m.Svc.OpenWorkloadWebUI(acc.ID)
			})
			m.quickCardBtn(c, pal, "查看日志", m.ShowDeployLog, false, func() {
				m.openDeployLogOverlay(acc.ID)
			})
		})
	})
}

// Card 5: 空闲自动关机（全站唯一空闲关机控制点，统一 10m / 15m / 40m）
func (m *MainUI) bentoIdleWatchdog(c *ui.Context, pal Palette, acc core.AccountProfile, rt core.AccountRuntimeState) {
	wd := m.Svc.WatchdogStatusFor(acc)
	effectiveMins := core.EffectiveIdleTimeoutMinutes(acc, rt)
	remMin := max(0, (rt.RemainingIdleSeconds(effectiveMins)+59)/60)
	enabled := acc.AutoShutdownEnabled

	BentoCard(c, pal, homeBentoCardHeight, func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(7).Children(func() {
			dot := pal.TextFaint
			if enabled {
				dot = pal.Cyan
			}
			DrawPrecisionLED(c, dot, 12)
			ui.Text(c, "空闲自动关机").FontSize(13).FontWeight(500).TextColor(pal.TextMuted)
			ui.Spacer(c)
			if ui.Switch(c, &enabled).Clicked() || enabled != acc.AutoShutdownEnabled {
				cp := acc
				cp.AutoShutdownEnabled = enabled
				cp.StopAfterQueueDrained = enabled
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

		// Unified 3-pill selector across all modes: 10m / 15m / 40m
		presets := []int{10, 15, 40}
		activeMins := acc.IdleTimeoutMinutes
		if activeMins <= 0 && acc.QueueDrainMinutes > 0 {
			activeMins = acc.QueueDrainMinutes
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
				ui.Row(c).Grow(1).Padding(5, 0).Radius(7).Background(bg).Justify(ui.Center).AlignItems(ui.Center).Cursor(ui.CursorPointer).OnClick(func() {
					cp := acc
					cp.IdleTimeoutMinutes = minsVal
					cp.QueueDrainMinutes = minsVal
					cp.StopAfterQueueDrained = true
					cp.AutoShutdownEnabled = true
					m.Svc.UpdateAccount(cp)
				}).Children(func() {
					ui.Textf(c, "%dm", minsVal).FontSize(12).FontWeight(w).FontFeatures("tnum").TextColor(fg)
				})
			}
		})

		ui.Spacer(c)
		foot := "已关软件停卡 · Google 仍回收久闲机"
		if acc.AutoShutdownEnabled {
			if rt.Phase.IsRunning() {
				foot = fmt.Sprintf("当前空闲已计时 %s", core.FormatDuration(rt.IdleSeconds))
			} else {
				foot = "闲下来自动关机，不怕忘关烧点"
			}
		}
		ui.Text(c, foot).FontSize(12).TextColor(pal.TextMuted).MaxLines(1).Ellipsis("…")
	})
}

// Card 6: 挑卡与守候（全主页唯一的 ShowHomeOptions 浮层入口）
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
			ui.Text(c, "调整策略与进阶选项").FontSize(12).FontWeight(500).TextColor(pal.TextPrimary)
			ui.Spacer(c)
			ui.Text(c, "›").FontSize(14).TextColor(pal.TextMuted)
		})
	})
}

func (m *MainUI) quickCardBtn(c *ui.Context, pal Palette, label string, active, disabled bool, onClick func()) {
	m.quickCardButtonStyled(c, pal, label, active, disabled, true, onClick)
}

func (m *MainUI) quickCardCompactBtn(c *ui.Context, pal Palette, label string, active, disabled bool, onClick func()) {
	m.quickCardButtonStyled(c, pal, label, active, disabled, false, onClick)
}

func (m *MainUI) quickCardButtonStyled(c *ui.Context, pal Palette, label string, active, disabled, grow bool, onClick func()) {
	bg := pal.SubtleBg
	border := pal.SubtleBorder
	fg := pal.TextPrimary
	if disabled {
		bg = pal.DisabledBg
		border = pal.DisabledBorder
		fg = pal.TextFaint
	} else if active {
		bg = WithAlpha(pal.Amber, 0.18)
		border = WithAlpha(pal.Amber, 0.45)
	}
	el := ui.Row(c).Padding(8, 10).Radius(8.5).Background(bg).Border(1, border).AlignItems(ui.Center).Justify(ui.Center)
	if grow {
		el.Grow(1)
	} else {
		el.Shrink(0)
	}
	if !disabled && onClick != nil {
		el.Cursor(ui.CursorPointer).OnClick(onClick)
	}
	el.Children(func() {
		ui.Text(c, label).FontSize(12).FontWeight(500).TextColor(fg).MaxLines(1)
	})
}

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
	if acc.Tier == core.TierFree && rt.CURatePerHour == 0 {
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

		// Ensure a running or stopping primary machine still appears before the next sessions refresh arrives
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

		// Deduplicate account-level billing across duplicate bindings of the same Google email
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

func (m *MainUI) renderRunningMachinesCard(c *ui.Context, pal Palette, rows []runningMachineRow, totalRate float64, hasRateSample bool, showAccountName bool) {
	const (
		colHW     float32 = 136
		colStatus float32 = 162
		colRate   float32 = 104
		colAct    float32 = 96
	)
	hasWebManual := false
	for _, r := range rows {
		if r.isWebManual {
			hasWebManual = true
			break
		}
	}
	managedCount := m.Svc.TotalManagedRunningSessionCount()

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
				// Column 1: LED + Source badge + concise identifier
				ui.Row(c).Grow(1).MinWidth(210).Gap(8).AlignItems(ui.Center).Children(func() {
					ledCol := pal.Emerald
					badgeText := "本软件"
					badgeTint := pal.Emerald
					label := rowCopy.account.DisplayName
					if !showAccountName {
						if rowCopy.isPrimary {
							label = rowCopy.account.DeployRecipe.DisplayName()
							if rowCopy.account.DeployRecipe == core.RecipeH3Video {
								label = rowCopy.account.H3Variant.DisplayName()
							}
						} else if !rowCopy.isWebManual {
							label = rowCopy.session.Name
						} else {
							label = "网页或其他设备开启"
						}
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
						if showAccountName && rowCopy.session.Name != "" {
							label = fmt.Sprintf("%s · %s", rowCopy.account.DisplayName, rowCopy.session.Name)
						}
					}
					DrawPrecisionLED(c, ledCol, 11)
					Badge(c, pal, badgeText, &badgeTint)
					ui.Text(c, label).FontSize(12.5).FontWeight(500).TextColor(pal.TextPrimary).Grow(1).MaxLines(1).Ellipsis("…")
				})

				// Column 2: Hardware & shape
				ui.Text(c, rowCopy.hardwareSpec).FontSize(13).FontWeight(600).TextColor(pal.TextPrimary).Width(colHW).MaxLines(1).Ellipsis("…")

				// Column 3: Status / VRAM
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

				// Column 4: Burn rate
				rCol := pal.TextPrimary
				if rowCopy.isStopping {
					rCol = pal.TextMuted
				} else if rowCopy.rateAmber {
					rCol = pal.Amber
				}
				ui.Text(c, rowCopy.rateText).FontSize(12.5).FontWeight(600).FontFeatures("tnum").TextColor(rCol).Width(colRate).TextAlign(ui.End).MaxLines(1)

				// Column 5: Action (always clickable; web manual machines open 3-step guide modal)
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

// MARK: - Bottom Mole-Style Live Sessions & Accounts Table

func (m *MainUI) renderLiveAccountsTable(c *ui.Context, pal Palette, selected core.AccountProfile) {
	cliReady := m.Svc.ViewState().CLIInstalled
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
					if core.EffectiveUseHighMem(accCopy) && !rt.SessionUsedStandardRAM {
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
		}
	}).Gap(10)
}

func stringsContainsTrimmed(s string) bool {
	return strings.TrimSpace(s) != ""
}
