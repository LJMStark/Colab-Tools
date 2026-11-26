package uiapp

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/egoist/mygo/ui"
)

func (m *MainUI) renderOnboarding(c *ui.Context, pal Palette) {
	topPad := float32(28)
	if !m.PreviewMode && runtime.GOOS == "darwin" {
		topPad = 36
	}
	content := func() {
		ui.Column(c).FillWidth().AlignItems(ui.Center).Padding(topPad, 28, 32, 28).Children(func() {
			ui.Column(c).FillWidth().MaxWidth(840).Gap(18).Children(func() {
				// Top header
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(14).Children(func() {
					if appIconBitmap != nil {
						ui.Image(c, appIconBitmap).Width(48).Height(48).Radius(12).Clip().Border(1, pal.SubtleBorder)
					}
					ui.Column(c).Grow(1).Gap(3).Children(func() {
						ui.Text(c, "欢迎使用 Colab Tools").FontSize(22).Bold().TextColor(pal.TextPrimary)
						ui.Text(c, "3 步完成环境体检与 Google 账号绑定，即可跨 macOS / Windows 一键管控云端 GPU / TPU").FontSize(13).TextColor(pal.TextMuted)
					})
					SmallActionButton(c, pal, "先跳过，直接看界面", nil, false, func() {
						m.Svc.CompleteOnboarding()
					})
				})

				// 3-step indicator pills
				steps := []string{"1. 环境体检与安装", "2. 绑定 Google 账号", "3. 30 秒快速上手"}
				ui.Grid(c).FillWidth().ColumnTracks(ui.Fr(1), ui.Fr(1), ui.Fr(1)).Gap(10).Children(func() {
					for i, title := range steps {
						stepNum := i + 1
						active := m.Svc.OnboardingStep == stepNum
						bg := pal.RecessedBg
						border := pal.RecessedBorder
						fg := pal.TextMuted
						if active {
							bg = WithAlpha(pal.Amber, 0.11)
							border = WithAlpha(pal.Amber, 0.58)
							fg = pal.TextPrimary
						}
						el := ui.Row(c).FillWidth().Padding(10, 14).Radius(11).Background(bg).Border(1, border).Justify(ui.Center).AlignItems(ui.Center).Gap(8).OnClick(func() {
							m.Svc.OnboardingStep = stepNum
						})
						if active {
							el.InsetShadow(0, 1, 0, 0, pal.CardInnerTopLight)
						} else {
							el.InsetShadow(0, 1, 1, 0, pal.RecessedInnerShadow)
						}
						el.Children(func() {
							if active {
								DrawPrecisionLED(c, pal.Amber, 12)
							} else {
								DrawPrecisionLED(c, pal.TextFaint, 10)
							}
							ui.Text(c, title).FontSize(13).FontWeight(600).TextColor(fg)
						})
					}
				})

				switch m.Svc.OnboardingStep {
				case 1:
					m.onboardingStep1Doctor(c, pal)
				case 2:
					m.onboardingStep2Accounts(c, pal)
				default:
					m.onboardingStep3Tour(c, pal)
				}
			})
		})
	}

	if m.PreviewMode {
		content()
	} else {
		ui.Scroll(c).FillWidth().Grow(1).Children(content)
	}
}

func (m *MainUI) onboardingStep1Doctor(c *ui.Context, pal Palette) {
	env := m.Svc.EnvReport
	accounts := m.Svc.AccountsSnapshot()

	SectionCard(c, pal, "", "", func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).Gap(3).Children(func() {
				ui.Text(c, "本机运行环境体检").FontSize(16).FontWeight(600).TextColor(pal.TextPrimary)
				ui.Text(c, "Colab Tools 完全基于 Google 官方 google-colab-cli 驱动，不私自绕过任何官方接口。").FontSize(12.5).TextColor(pal.TextMuted)
			})
			lbl := "重新体检"
			if m.Svc.IsCheckingEnv {
				lbl = "检测中…"
			}
			onboardingRefreshButton(c, pal, lbl, m.Svc.IsCheckingEnv || m.Svc.IsInstallingCLI, func() {
				go m.Svc.RefreshEnvironmentCheck()
			})
		})

		ui.Box(c).FillWidth().Height(1).Background(pal.Divider)

		cliDetail := "未检测到 colab 命令，可点击下方按钮自动为你安装"
		if m.Svc.CLIInstalled {
			ver := ""
			if env.ColabVersion != "" {
				ver = " v" + env.ColabVersion
			}
			cliDetail = fmt.Sprintf("已安装%s · %s", ver, env.ColabPath)
		} else if env.ColabBrokenReason != "" {
			cliDetail = env.ColabBrokenReason
		}
		m.doctorRow(c, pal, "官方 google-colab-cli", m.Svc.CLIInstalled, cliDetail)

		pkgOK := env.UVPath != "" || env.PipxPath != "" || env.BrewPath != "" || env.UsingWSLBridge
		pkgTitle := "包管理环境（uv / pipx / Homebrew）"
		if runtime.GOOS == "windows" {
			pkgTitle = "Windows 运行环境（WSL2 / uv）"
		}
		m.doctorRow(c, pal, pkgTitle, pkgOK, env.PackageManagerSummary())

		accDetail := "尚未绑定账号（下一步可一键通过 Google 官方授权登录）"
		if len(accounts) > 0 {
			accDetail = fmt.Sprintf("已绑定 %d 个账号", len(accounts))
		}
		m.doctorRow(c, pal, "Google Colab 账号授权", len(accounts) > 0, accDetail)

		if !m.Svc.CLIInstalled {
			ui.Box(c).FillWidth().Height(1).Background(pal.Divider)
			ui.Column(c).FillWidth().Gap(12).Children(func() {
				ui.Text(c, "✦ 自动安装方案："+env.RecommendedMethod.DisplayTitle()).FontSize(13.5).FontWeight(600).TextColor(pal.TextPrimary)

				ui.Column(c).FillWidth().Padding(12).Radius(9).Background(pal.SubtleBg).Border(1, pal.SubtleBorder).Children(func() {
					ui.Text(c, env.RecommendedMethod.ManualShellCommand()).FontSize(12.5).Font("monospace").TextColor(pal.TextPrimary)
				})

				if env.RecommendedMethod.RequiresRemoteScriptConsent() {
					ui.Checkbox(c, &m.AgreedRemoteScript, "我知晓并将从 Astral 官方地址（astral.sh/uv/install.sh）安装 uv 及 google-colab-cli")
				}

				ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
					if m.Svc.IsInstallingCLI {
						status := "正在安装 google-colab-cli，请稍候…"
						cancelLabel := "取消安装"
						if m.Svc.IsCancellingCLIInstall {
							status = "正在终止安装进程组…"
							cancelLabel = "正在取消…"
						}
						ui.Text(c, status).FontSize(13).FontWeight(500).TextColor(pal.Amber)
						SmallActionButton(c, pal, cancelLabel, &pal.Coral, m.Svc.IsCancellingCLIInstall, func() {
							m.Svc.CancelCLIInstall()
						})
					} else {
						installDisabled := env.RecommendedMethod.RequiresRemoteScriptConsent() && !m.AgreedRemoteScript
						PillButton(c, pal, env.RecommendedMethod.ShortButtonTitle(), true, installDisabled, func() {
							m.ShowInstallLogs = true
							m.Svc.InstallColabCLIAutomatically()
						})
						PillButton(c, pal, "复制命令自己装", false, false, func() {
							m.Svc.CopyToClipboard(env.RecommendedMethod.ManualShellCommand(), "CLI 安装命令")
						})
					}
					ui.Spacer(c)
				})

				if m.Svc.CLIInstallError != "" {
					ui.Text(c, "⚠ "+m.Svc.CLIInstallError).FontSize(12.5).TextColor(pal.Coral)
				}

				if len(m.Svc.CLIInstallLogs) > 0 {
					ui.Column(c).FillWidth().Padding(10).Radius(8).Background(ui.RGBA(0, 0, 0, 0.25)).Children(func() {
						tail := m.Svc.CLIInstallLogs
						if len(tail) > 8 {
							tail = tail[len(tail)-8:]
						}
						ui.Text(c, strings.Join(tail, "\n")).FontSize(11.5).Font("monospace").TextColor(pal.TextMuted)
					})
				}
			})
		} else {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Padding(6, 0, 0, 0).Children(func() {
				ui.Text(c, "✓ 环境已就绪，google-colab-cli 可正常工作").FontSize(13.5).FontWeight(600).TextColor(pal.Emerald)
				ui.Spacer(c)
				PillButton(c, pal, "下一步：绑定 Google 账号 →", true, false, func() {
					m.Svc.OnboardingStep = 2
				})
			})
		}
	})
}

func (m *MainUI) doctorRow(c *ui.Context, pal Palette, title string, ok bool, detail string) {
	ui.Row(c).FillWidth().Padding(12, 14).Radius(12).
		Background(pal.RecessedBg).
		Border(1, pal.RecessedBorder).
		InsetShadow(0, 1, 2, 0, pal.RecessedInnerShadow).
		AlignItems(ui.Center).Gap(12).Children(func() {
		dotCol := pal.Amber
		badgeText := "待完成"
		if ok {
			dotCol = pal.Emerald
			badgeText = "已就绪"
		}
		DrawPrecisionLED(c, dotCol, 14)
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			ui.Text(c, title).FontSize(13.5).FontWeight(600).TextColor(pal.TextPrimary)
			ui.Text(c, detail).FontSize(12).TextColor(pal.TextMuted)
		})
		ui.Row(c).Padding(4, 9).Radius(7).Background(pal.CardTop).Border(1, pal.SubtleBorder).Gap(5).AlignItems(ui.Center).Shrink(0).Children(func() {
			DrawPrecisionLED(c, dotCol, 8)
			ui.Text(c, badgeText).FontSize(11.5).FontWeight(600).TextColor(pal.TextMuted).MaxLines(1)
		})
	})
}

func (m *MainUI) onboardingStep2Accounts(c *ui.Context, pal Palette) {
	accounts := m.Svc.AccountsSnapshot()
	SectionCard(c, pal, "", "", func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).Gap(3).Children(func() {
				ui.Text(c, "绑定你的第一个 Google Colab 账号").FontSize(16).FontWeight(600).TextColor(pal.TextPrimary)
				ui.Text(c, "凭证仅加密存于本机系统凭据库；支持同时绑定多个 AI Ultra / Pro+ / Pro / Free 账号。").FontSize(12.5).TextColor(pal.TextMuted)
			})
			SmallActionButton(c, pal, "自动导入终端已登账号", nil, !m.Svc.CLIInstalled || m.Svc.IsImportingDefault, func() {
				go func() {
					if !m.Svc.TryImportSystemDefaultAccount() {
						m.Svc.ShowToast("未检测到终端默认 colab 登录，请用下方按钮走官方授权")
					}
				}()
			})
		})

		ui.Box(c).FillWidth().Height(1).Background(pal.Divider)

		if len(accounts) > 0 {
			for _, acc := range accounts {
				rt := m.Svc.StateFor(acc.ID)
				ui.Row(c).FillWidth().Padding(12).Radius(12).Background(pal.RecessedBg).Border(1, pal.RecessedBorder).InsetShadow(0, 1, 2, 0, pal.RecessedInnerShadow).AlignItems(ui.Center).Gap(10).Children(func() {
					DrawPrecisionLED(c, AccountTagColor(pal, acc.ColorTag), 13)
					ui.Column(c).Grow(1).Gap(2).Children(func() {
						ui.Textf(c, "%s · %s", acc.DisplayName, acc.Tier.DisplayName()).FontSize(13.5).FontWeight(600).TextColor(pal.TextPrimary)
						email := acc.Email
						if email == "" {
							email = "默认官方 CLI 账号"
						}
						ui.Text(c, email).FontSize(12).TextColor(pal.TextMuted)
					})
					ui.Textf(c, "%.1f CU", rt.CUBalance).FontSize(14).FontWeight(600).FontFeatures("tnum").TextColor(pal.TextPrimary)
					ui.Row(c).Padding(4, 9).Radius(7).Background(pal.CardTop).Border(1, pal.SubtleBorder).Gap(5).AlignItems(ui.Center).Shrink(0).Children(func() {
						DrawPrecisionLED(c, pal.Emerald, 8)
						ui.Text(c, "已绑定").FontSize(11.5).FontWeight(600).TextColor(pal.TextMuted).MaxLines(1)
					})
				})
			}
			ui.Row(c).FillWidth().Padding(6, 0, 0, 0).Children(func() {
				ui.Spacer(c)
				PillButton(c, pal, "下一步：30 秒快速上手 →", true, false, func() {
					m.Svc.OnboardingStep = 3
				})
			})
		} else {
			m.renderEmbeddedOAuthBinder(c, pal, func() {
				m.Svc.OnboardingStep = 3
			})
		}
	})
}

func (m *MainUI) onboardingStep3Tour(c *ui.Context, pal Palette) {
	SectionCard(c, pal, "", "", func() {
		ui.Column(c).FillWidth().Gap(4).Children(func() {
			ui.Text(c, "一切就绪！30 秒掌握三个最实用的功能").FontSize(16).FontWeight(600).TextColor(pal.TextPrimary)
			ui.Text(c, "关闭主窗口后，Colab Tools 会安静常驻在系统菜单栏 / 托盘；随时点击托盘图标或右键菜单唤出大屏。").FontSize(12.5).TextColor(pal.TextMuted)
		})

		ui.Grid(c).FillWidth().ColumnTracks(ui.Fr(1), ui.Fr(1), ui.Fr(1)).Gap(12).Children(func() {
			m.tourTile(c, pal, "original", pal.Violet, "一键部署 H3 视频模型", "默认选「H3 原版」或「H3 Flash 极速版」，也能换轻量 GGUF 或自定义链接；开机自动装好并通过本机加密通道打开 ComfyUI。")
			m.tourTile(c, pal, "flash", pal.Emerald, "跑完自动关机省点数", "视频队列全部生成完就自动倒计时停卡，并去官网再查一次确认停了——再也不用担心忘关机挂一晚白扣几十点。")
			m.tourTile(c, pal, "llm", pal.Amber, "顺位抢卡 + 大模型代理", "热门 A100 / G4 没货时按顺位自动排队，还能约清晨低峰开机；想跑大模型给 Claude / OpenCode 用，切一下模式就行。")
		})

		ui.Row(c).FillWidth().Padding(8, 0, 0, 0).Children(func() {
			PillButton(c, pal, "← 返回上一步", false, false, func() {
				m.Svc.OnboardingStep = 2
			})
			ui.Spacer(c)
			PillButton(c, pal, "✦ 进入 Colab Tools 主控制台", true, false, func() {
				m.Svc.CompleteOnboarding()
			})
		})
	})
}

func (m *MainUI) tourTile(c *ui.Context, pal Palette, iconKind string, tint ui.Color, title, desc string) {
	PrecisionCard(c, pal, 14, 16, func() {
		ScenarioIcon(c, pal, iconKind, tint, true)
		ui.Text(c, title).FontSize(14).FontWeight(600).TextColor(pal.TextPrimary)
		ui.Text(c, desc).FontSize(12).TextColor(pal.TextMuted)
	}).Gap(10)
}

func onboardingRefreshButton(c *ui.Context, pal Palette, label string, disabled bool, onClick func()) {
	el := ui.Row(c).
		Padding(6, 12).
		Radius(8).
		Gap(6).
		AlignItems(ui.Center).
		Justify(ui.Center).
		Shrink(0)

	fg := pal.TextPrimary
	icCol := pal.TextMuted
	if disabled {
		fg = pal.TextFaint
		icCol = pal.TextFaint
		el.Background(pal.DisabledBg).Border(1, pal.DisabledBorder)
	} else {
		top := pal.CardTop
		bot := pal.RecessedBg
		if el.Hovered() {
			top = top.Mix(ui.RGB(255, 255, 255), 0.06)
			bot = bot.Mix(pal.CardTop, 0.35)
		}
		el.LinearGradient(ui.LinearGradient{From: top, To: bot, Angle: 180}).
			Border(1, pal.SubtleBorder).
			InsetShadow(0, 1, 0, 0, pal.CardInnerTopLight).
			Shadow(0, 1, 3, 0, pal.CardShadow).
			Cursor(ui.CursorPointer)
		if onClick != nil {
			el.OnClick(onClick)
		}
	}
	el.Children(func() {
		ToolbarIcon(c, "refresh", 13, icCol)
		ui.Text(c, label).FontSize(12).FontWeight(500).TextColor(fg).MaxLines(1)
	})
}
