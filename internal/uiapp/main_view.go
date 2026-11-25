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

type SettingsSubTab int

const (
	SubTabGeneral SettingsSubTab = iota
	SubTabDoctor
	SubTabAdvanced
	SubTabAbout
)

// MainUI holds transient form/editor state for the desktop console window.
type MainUI struct {
	Svc         *app.Service
	PreviewMode bool
	ForceTheme  string // "", "dark", "light" (used for deterministic screenshot exports)

	SettingsSubTab      SettingsSubTab
	SelectedCopyProto   integrations.CopyProtocolKind
	SelectedInsightHW   string
	ShowAccountDropdown bool
	AgreedRemoteScript  bool
	ShowInstallLogs     bool

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
	}).Children(func() {
		if m.Svc.ShowingOnboarding {
			m.renderOnboarding(c, pal)
		} else {
			m.renderTopCapsuleBar(c, pal)
			ui.Box(c).FillWidth().Height(1).Background(pal.Divider)

			body := func() {
				ui.Column(c).FillWidth().Padding(18, 24, 24, 24).Gap(14).Children(func() {
					switch m.Svc.SelectedSection {
					case app.SectionStatus:
						m.renderStatusPage(c, pal)
					case app.SectionDeploy:
						m.centeredWorkbench(c, func() { m.renderDeployPage(c, pal) })
					case app.SectionCompute:
						m.centeredWorkbench(c, func() { m.renderComputePage(c, pal) })
					case app.SectionIntegrations:
						m.centeredWorkbench(c, func() { m.renderIntegrationsPage(c, pal) })
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

	// Floating bottom-right toast banner
	if toast := m.Svc.ToastMessage; toast != "" {
		ui.Overlay(c, func() {
			ui.Column(c).Fill().Justify(ui.End).AlignItems(ui.End).Padding(20).Children(func() {
				ui.Row(c).Gap(8).Padding(10, 16).Radius(999).
					Background(pal.CardBg).Border(1, pal.SubtleBorder).
					Shadow(0, 4, 14, 0, ui.RGBA(0, 0, 0, 0.25)).
					AlignItems(ui.Center).Children(func() {
					ui.Box(c).Width(8).Height(8).Radius(4).Background(pal.Emerald)
					ui.Text(c, toast).FontSize(13).FontWeight(500).TextColor(pal.TextPrimary).MaxLines(1)
				})
			})
		})
	}
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
			ui.Row(c).Gap(6).Padding(6, 12).Radius(999).Background(pal.SubtleBg).Border(1, pal.SubtleBorder).AlignItems(ui.Center).OnClick(func() {
				// Cycle account quickly on click
				for i, a := range accounts {
					if a.ID == selAcc.ID {
						next := accounts[(i+1)%len(accounts)]
						m.Svc.SelectAccount(next.ID)
						break
					}
				}
			}).Children(func() {
				ui.Box(c).Width(8).Height(8).Radius(4).Background(AccountTagColor(pal, selAcc.ColorTag))
				ui.Text(c, selAcc.DisplayName).FontSize(13.5).FontWeight(600).TextColor(pal.TextPrimary).MaxLines(1)
				ui.Text(c, "▾").FontSize(11).TextColor(pal.TextMuted)
			})
		}

		ui.Spacer(c)

		// Centered Mole floating capsule navigation
		ui.Row(c).Gap(4).Padding(5, 6).Radius(999).Background(pal.NavBg).Border(1, pal.SubtleBorder).AlignItems(ui.Center).Children(func() {
			if appIconBitmap != nil {
				ui.Image(c, appIconBitmap).Width(26).Height(26).Radius(13).Clip()
			} else {
				ui.Box(c).Width(26).Height(26).Radius(13).Background(pal.Amber)
			}
			ui.Box(c).Width(2)

			for idx, title := range app.MainSectionTitles {
				sec := app.MainSection(idx)
				selected := m.Svc.SelectedSection == sec
				el := ui.Row(c).Padding(6, 15).Radius(999).AlignItems(ui.Center)
				if selected {
					el.Background(pal.NavPillBg).Shadow(0, 1, 5, 0, ui.RGBA(0, 0, 0, 0.18))
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

		// Right live status pill
		pillColor, pillLabel := m.topRightPill(pal)
		ui.Row(c).Gap(7).Padding(6, 13).Radius(999).Background(pal.SubtleBg).Border(1, pal.SubtleBorder).AlignItems(ui.Center).OnClick(func() {
			if !m.Svc.CLIInstalled {
				m.Svc.ReopenOnboarding()
			} else {
				go m.Svc.RefreshAllAccounts(true, false)
			}
		}).Children(func() {
			ui.Box(c).Width(8).Height(8).Radius(4).Background(pillColor)
			ui.Text(c, pillLabel).FontSize(12.5).FontWeight(500).FontFeatures("tnum").TextColor(pal.TextPrimary).MaxLines(1)
		})
	})
}

func (m *MainUI) topRightPill(pal Palette) (ui.Color, string) {
	if !m.Svc.CLIInstalled {
		return pal.Coral, "需安装 Colab CLI"
	}
	running := m.Svc.RunningAccountCount()
	if running >= 2 {
		return pal.Emerald, fmt.Sprintf("%d 运行中 · 刷新", running)
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
	return pal.TextFaint, fmt.Sprintf("v%s · 刷新", app.AppVersion)
}

// MARK: - Tab 1: Mole-Style 4x2 Bento Status Dashboard + Bottom Live Session Table

func (m *MainUI) renderStatusPage(c *ui.Context, pal Palette) {
	if !m.Svc.CLIInstalled {
		ui.Row(c).FillWidth().Padding(14).Radius(14).
			Background(WithAlpha(pal.Amber, 0.12)).Border(1, WithAlpha(pal.Amber, 0.34)).
			AlignItems(ui.Center).Gap(12).Children(func() {
			ui.Box(c).Width(10).Height(10).Radius(5).Background(pal.Amber)
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

	// Active queue or circuit-breaker banner
	if rt.Phase.IsQueuing() {
		countdown := "正在探测库存…"
		if rt.Phase.NextRetryAt != nil {
			rem := max(0, int(math.Ceil(time.Until(*rt.Phase.NextRetryAt).Seconds())))
			countdown = fmt.Sprintf("距下次探测 %02d:%02d", rem/60, rem%60)
		}
		ui.Row(c).FillWidth().Padding(10, 14).Radius(12).
			Background(WithAlpha(pal.Amber, 0.13)).Border(1, WithAlpha(pal.Amber, 0.34)).
			AlignItems(ui.Center).Gap(10).Children(func() {
			ui.Box(c).Width(8).Height(8).Radius(4).Background(pal.Amber)
			ui.Textf(c, "合规排队中 · 第 %d/%d 轮（%s）· %s", rt.Phase.QueueAttempt, rt.Phase.QueueMax, rt.Phase.TargetsSummary, countdown).
				FontSize(13).FontWeight(500).FontFeatures("tnum").TextColor(pal.TextPrimary).Grow(1)
			SmallActionButton(c, pal, "取消排队", &pal.Coral, false, func() {
				m.Svc.CancelQueue(acc.ID)
			})
		})
	} else if rt.QueueCircuitBreakTill != nil && rt.QueueCircuitBreakTill.After(time.Now()) {
		remMin := max(1, int(math.Ceil(time.Until(*rt.QueueCircuitBreakTill).Minutes())))
		ui.Row(c).FillWidth().Padding(10, 14).Radius(12).
			Background(WithAlpha(pal.Coral, 0.13)).Border(1, WithAlpha(pal.Coral, 0.34)).
			AlignItems(ui.Center).Gap(10).Children(func() {
			ui.Box(c).Width(8).Height(8).Radius(4).Background(pal.Coral)
			ui.Textf(c, "已触发防风控硬熔断保护，冷却剩余约 %d 分钟后可重新申请。", remMin).
				FontSize(13).FontWeight(500).TextColor(pal.TextPrimary)
		})
	}

	// 4x2 Mole Bento Grid
	ui.Grid(c).FillWidth().ColumnTracks(ui.Fr(1), ui.Fr(1), ui.Fr(1), ui.Fr(1)).Gap(14).Children(func() {
		m.bentoHealthCU(c, pal, acc, rt)
		m.bentoVRAM(c, pal, rt)
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
		if rt.ActivitySignal.IsBusy() {
			statusWord = "推理中"
		} else {
			statusWord = "很好"
		}
		statusTint = pal.Emerald
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

	BentoCard(c, pal, 176, func() {
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
			blocked := (acc.EnablePoliteQueue && isCooling) || !m.Svc.CLIInstalled
			label := "▶  一键启动"
			if acc.EnablePoliteQueue {
				label = "↻  排队启动"
			}
			bg := pal.NavPillBg
			fg := pal.NavPillText
			if blocked {
				bg = pal.SubtleBg
				fg = pal.TextFaint
			}
			el := ui.Row(c).FillWidth().Padding(7, 10).Radius(8).Background(bg).Justify(ui.Center)
			if !blocked {
				el.OnClick(func() { m.Svc.StartAccountSession(acc.ID) })
			}
			el.Children(func() {
				ui.Text(c, label).FontSize(12.5).FontWeight(600).TextColor(fg)
			})
		}
	})
}

func (m *MainUI) bentoVRAM(c *ui.Context, pal Palette, rt core.AccountRuntimeState) {
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
}

func (m *MainUI) bentoGPU(c *ui.Context, pal Palette, acc core.AccountProfile, rt core.AccountRuntimeState) {
	offlineBilling := !rt.Phase.IsRunning() && rt.CURatePerHour > 0
	badge := acc.PreferredAccelerator.ShortChipLabel()
	var bTint *ui.Color
	if rt.Phase.IsRunning() {
		badge = rt.ShortGPUName()
	} else if offlineBilling {
		badge = "仍在计费"
		t := pal.Amber
		bTint = &t
	}
	foot := "当前未计费"
	footColor := pal.TextMuted
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
	} else if offlineBilling {
		foot = fmt.Sprintf("其他会话计费 · %.2f CU/h", rt.CURatePerHour)
		footColor = pal.Amber
	} else if rt.LastUsageFetchAt == nil {
		foot = "等待同步用量"
	}

	BentoCard(c, pal, 176, func() {
		TileHeader(c, pal, pal.Amber, "GPU", badge, bTint)
		ui.Row(c).AlignItems(ui.End).Gap(4).Children(func() {
			if rt.Phase.IsRunning() {
				ui.Textf(c, "%d", rt.GPUUtilPercent).FontSize(28).Bold().FontFeatures("tnum").TextColor(pal.TextPrimary)
				ui.Text(c, "%").FontSize(14).FontWeight(500).TextColor(pal.TextMuted).Padding(0, 0, 4, 0)
			} else {
				ui.Text(c, "—").FontSize(28).Bold().TextColor(pal.TextPrimary)
			}
		})
		DrawSparkline(c, pal, rt.GPUUtilHistory, pal.Amber, 30)
		ui.Spacer(c)
		ui.Text(c, foot).FontSize(12).FontFeatures("tnum").TextColor(footColor).MaxLines(1)
	})
}

func (m *MainUI) bentoWorkload(c *ui.Context, pal Palette, acc core.AccountProfile, rt core.AccountRuntimeState) {
	kind := acc.WorkloadKind
	if kind == core.WorkloadGeneral && rt.DetectedWorkloadKind != nil {
		kind = *rt.DetectedWorkloadKind
	}
	tint := WorkloadColor(pal, kind)

	BentoCard(c, pal, 176, func() {
		if kind == core.WorkloadLLM {
			TileHeader(c, pal, tint, "上下文", fmt.Sprintf("%dK", rt.ContextLimit/1024), nil)
			ui.Row(c).AlignItems(ui.End).Gap(4).Children(func() {
				if rt.Phase.IsRunning() && rt.ServerHealthy {
					ui.Text(c, core.FormatTokens(rt.PromptTokens)).FontSize(28).Bold().FontFeatures("tnum").TextColor(pal.TextPrimary)
					ui.Text(c, "tok").FontSize(13.5).FontWeight(500).TextColor(pal.TextMuted).Padding(0, 0, 4, 0)
				} else {
					ui.Text(c, "—").FontSize(28).Bold().TextColor(pal.TextPrimary)
				}
			})
			prog := 0.0
			if rt.ServerHealthy {
				prog = rt.ContextRatio()
			}
			ui.Box(c).Height(4)
			DrawHorizonBar(c, pal, prog, tint, 8)
			ui.Spacer(c)
			foot := "等待模型服务在线"
			if rt.ServerHealthy {
				foot = fmt.Sprintf("已用 %d%% · %s KV · #%d", int(math.Round(rt.ContextRatio()*100)), rt.KVQuant, max(0, rt.LastTaskID))
			}
			ui.Text(c, foot).FontSize(12).FontFeatures("tnum").TextColor(pal.TextMuted).MaxLines(1)
		} else if acc.DeployRecipe.IsVideoComfy() || kind == core.WorkloadVideo {
			title := "视频部署与队列"
			badge := acc.H3Variant.DisplayName()
			var bTint *ui.Color
			if acc.DeployRecipe == core.RecipeComfyUIGeneral {
				badge = "ComfyUI"
			}
			if rt.DeployRunning {
				title = "部署进度"
				a := pal.Amber
				bTint = &a
			} else if rt.Phase.IsRunning() && rt.RenderedVideoCount > 0 {
				badge = fmt.Sprintf("已出 %d 条", rt.RenderedVideoCount)
				e := pal.Emerald
				bTint = &e
			} else if rt.Phase.IsRunning() && rt.ServerHealthy {
				e := pal.Emerald
				bTint = &e
			}
			TileHeader(c, pal, tint, title, badge, bTint)

			ui.Row(c).AlignItems(ui.End).Gap(6).Children(func() {
				if rt.Phase.IsRunning() {
					if rt.DeployRunning && rt.WorkloadMetricValue != "" {
						ui.Text(c, rt.WorkloadMetricValue).FontSize(26).Bold().FontFeatures("tnum").TextColor(pal.TextPrimary)
						ui.Text(c, rt.WorkloadMetricUnit).FontSize(13).FontWeight(500).TextColor(pal.TextMuted).Padding(0, 0, 4, 0)
					} else if rt.QueueRunning > 0 || rt.QueuePending > 0 {
						ui.Textf(c, "渲染 %d", rt.QueueRunning).FontSize(26).Bold().FontFeatures("tnum").TextColor(pal.TextPrimary)
						ui.Textf(c, "排队 %d", rt.QueuePending).FontSize(13).FontWeight(500).TextColor(pal.Amber).Padding(0, 0, 4, 0)
					} else if rt.ServerHealthy {
						ui.Text(c, "已就绪").FontSize(25).Bold().TextColor(pal.Emerald)
						ui.Text(c, ":8188").FontSize(13).FontFeatures("tnum").TextColor(pal.TextMuted).Padding(0, 0, 4, 0)
					} else {
						ui.Text(c, "待启动").FontSize(25).Bold().TextColor(pal.TextPrimary)
					}
				} else {
					ui.Text(c, "待开机").FontSize(25).Bold().TextColor(pal.TextPrimary)
				}
			})

			prog := 0.0
			if rt.Phase.IsRunning() {
				if rt.WorkloadMetricRatio != nil {
					prog = *rt.WorkloadMetricRatio
				} else if rt.QueueRunning > 0 {
					prog = 0.72
				} else if rt.ServerHealthy {
					prog = 1.0
				}
			}
			ui.Box(c).Height(4)
			DrawHorizonBar(c, pal, prog, tint, 8)
			ui.Spacer(c)
			foot := "开机自动装好模型并打开 ComfyUI"
			if rt.Phase.IsRunning() {
				if rt.WorkloadDetail != "" {
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
			TileHeader(c, pal, tint, kind.MetricCardTitle(), fmt.Sprintf(":%d", port), nil)
			ui.Row(c).AlignItems(ui.End).Gap(4).Children(func() {
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
					ui.Text(c, "—").FontSize(28).Bold().TextColor(pal.TextPrimary)
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
			ui.Box(c).Height(4)
			DrawHorizonBar(c, pal, prog, tint, 8)
			ui.Spacer(c)
			foot := fmt.Sprintf("%s · CUDA %d 进程", kind.DisplayName(), rt.CUDAProcessCount)
			if rt.WorkloadDetail != "" {
				foot = rt.WorkloadDetail
			}
			ui.Text(c, foot).FontSize(12).FontFeatures("tnum").TextColor(pal.TextMuted).MaxLines(1)
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

	BentoCard(c, pal, 176, func() {
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
	})
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
			ui.Box(c).Width(8).Height(8).Radius(4).Background(dot)
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
	targets := core.SanitizedQueueTargets(acc.QueueTargets, acc.Tier)
	headline := acc.PreferredAccelerator.ShortChipLabel()
	badge := "单次启动"
	var bTint *ui.Color
	if acc.EnablePoliteQueue {
		headline = core.QueueTargetsSummary(targets)
		badge = "排队已开"
		t := pal.Amber
		bTint = &t
	}
	BentoCard(c, pal, 176, func() {
		TileHeader(c, pal, pal.Amber, "抢卡与预约", badge, bTint)
		ui.Text(c, headline).FontSize(21).Bold().TextColor(pal.TextPrimary).MaxLines(1).Ellipsis("…")
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			hasTag := false
			if acc.EnablePoliteQueue && acc.QueueFallbackEnabled {
				hasTag = true
				Badge(c, pal, fmt.Sprintf("%d轮保底 %s", acc.QueueFallbackAfterRounds, acc.QueueFallbackTarget.ShortChipLabel()), nil)
			}
			if acc.ScheduledStartEnabled {
				hasTag = true
				e := pal.Emerald
				Badge(c, pal, fmt.Sprintf("预约 %02d:%02d", acc.ScheduledStartHour, acc.ScheduledStartMinute), &e)
			}
			if !hasTag {
				ui.Text(c, "未设保底或定时预约").FontSize(12).TextColor(pal.TextFaint)
			}
		})
		ui.Spacer(c)
		ui.Row(c).FillWidth().Padding(6, 10).Radius(8).Background(pal.SubtleBg).AlignItems(ui.Center).OnClick(func() {
			m.Svc.SetSection(app.SectionCompute)
		}).Children(func() {
			ui.Text(c, "配置顺位、降级与预约").FontSize(12).FontWeight(500).TextColor(pal.TextPrimary)
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
		if rt.SSHTunnelActive {
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
				ui.Row(c).FillWidth().Gap(7).Children(func() {
					m.quickCardBtn(c, pal, "开 ComfyUI", rt.Phase.IsRunning() && rt.ServerHealthy, !rt.Phase.IsRunning(), func() {
						go m.Svc.OpenComfyUI(acc.ID)
					})
					m.quickCardBtn(c, pal, "换 H3 版本", false, false, func() {
						m.Svc.SetSection(app.SectionDeploy)
					})
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
				} else if rt.PublicURL != "" {
					syncLabel = "同步工具"
				}
				m.quickCardBtn(c, pal, syncLabel, allSynced, false, func() {
					if hasTargets && rt.PublicURL != "" {
						go m.Svc.SyncAllEnabledClients(acc.ID, false)
					} else {
						m.Svc.SetSection(app.SectionIntegrations)
					}
				})

				if acc.EnableLLMModule {
					m.quickCardBtn(c, pal, ">_ Claude", false, !rt.Phase.IsRunning() || rt.PublicURL == "", func() {
						m.Svc.LaunchClaudeTerminal(acc.ID)
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

	ui.Column(c).FillWidth().Padding(18).Gap(10).Radius(16).Background(pal.CardBg).Border(1, pal.CardBorder).Children(func() {
		ui.Row(c).FillWidth().Gap(10).Padding(0, 6).AlignItems(ui.Center).Children(func() {
			ui.Row(c).Grow(1).MinWidth(220).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Textf(c, "账号与活跃会话 (%d)", len(accounts)).FontSize(14).FontWeight(600).TextColor(pal.TextPrimary).MaxLines(1)
				if m.Svc.TotalManagedRunningSessionCount() >= 2 {
					SmallActionButton(c, pal, "⏻ 全部关机止损", &pal.Coral, false, func() {
						go m.Svc.StopAllManagedSessions()
					})
				}
			})
			ui.Text(c, "硬件与形态").FontSize(12).FontWeight(500).TextColor(pal.TextFaint).Width(colHW).MaxLines(1)
			ui.Text(c, "显存占用").FontSize(12).FontWeight(500).TextColor(pal.TextFaint).Width(colVRAM).MaxLines(1)
			ui.Text(c, "CU 余额").FontSize(12).FontWeight(500).TextColor(pal.TextFaint).Width(colCU).TextAlign(ui.End).MaxLines(1)
			ui.Text(c, "消耗速率").FontSize(12).FontWeight(500).TextColor(pal.TextFaint).Width(colRate).TextAlign(ui.End).MaxLines(1)
			ui.Text(c, "快捷操作").FontSize(12).FontWeight(500).TextColor(pal.TextFaint).Width(colAct).TextAlign(ui.End).MaxLines(1)
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
					ui.Box(c).Width(9).Height(9).Radius(4.5).Background(dotCol).Shrink(0)
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
	})
}

func stringsContainsTrimmed(s string) bool {
	return strings.TrimSpace(s) != ""
}
