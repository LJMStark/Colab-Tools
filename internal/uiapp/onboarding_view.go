package uiapp

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/LJMStark/Colab-Tools/internal/core"
	"github.com/egoist/mygo/ui"
)

func (m *MainUI) renderOnboarding(c *ui.Context, pal Palette) {
	vs := m.Svc.ViewState()
	topPad := float32(28)
	if !m.PreviewMode && runtime.GOOS == "darwin" {
		topPad = 36
	}
	step := vs.OnboardingStep
	if step != 2 {
		step = 1
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
						ui.Text(c, "2 步完成环境体检与 Google 账号绑定，即可在 macOS 上一键管控云端 GPU 与 ComfyUI").FontSize(13).TextColor(pal.TextMuted)
					})
					SmallActionButton(c, pal, "先跳过，直接看界面", nil, false, func() {
						m.Svc.CompleteOnboarding()
					})
				})

				// 2-step indicator pills
				steps := []string{"1. 环境体检与安装", "2. 绑定 Google 账号"}
				ui.Grid(c).FillWidth().ColumnTracks(ui.Fr(1), ui.Fr(1)).Gap(10).Children(func() {
					for i, title := range steps {
						stepNum := i + 1
						active := step == stepNum
						bg := pal.RecessedBg
						border := pal.RecessedBorder
						fg := pal.TextMuted
						if active {
							bg = WithAlpha(pal.Amber, 0.11)
							border = WithAlpha(pal.Amber, 0.58)
							fg = pal.TextPrimary
						}
						el := ui.Row(c).FillWidth().Padding(10, 14).Radius(11).Background(bg).Border(1, border).Justify(ui.Center).AlignItems(ui.Center).Gap(8).Cursor(ui.CursorPointer).OnClick(func() {
							m.Svc.SetOnboardingStep(stepNum)
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

				if step == 1 {
					m.onboardingStep1Doctor(c, pal)
				} else {
					m.onboardingStep2Accounts(c, pal)
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
	vs := m.Svc.ViewState()
	env := vs.EnvReport
	accounts := m.Svc.AccountsSnapshot()

	SectionCard(c, pal, "", "", func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).Gap(3).Children(func() {
				ui.Text(c, "本机运行环境体检").FontSize(16).FontWeight(600).TextColor(pal.TextPrimary)
				ui.Text(c, "Colab Tools 完全基于 Google 官方 google-colab-cli 驱动，不私自绕过任何官方接口。").FontSize(12.5).TextColor(pal.TextMuted)
			})
			lbl := "重新体检"
			if vs.IsCheckingEnv {
				lbl = "检测中…"
			}
			ActionBtn(c, pal, lbl, nil, "refresh", false, vs.IsCheckingEnv || vs.IsInstallingCLI, func() {
				go m.Svc.RefreshEnvironmentCheck()
			})
		})

		ui.Box(c).FillWidth().Height(1).Background(pal.Divider)

		cliDetail := "未检测到 colab 命令，可点击下方按钮自动为你安装"
		if vs.CLIInstalled {
			ver := ""
			if env.ColabVersion != "" {
				ver = " v" + env.ColabVersion
			}
			cliDetail = fmt.Sprintf("已安装%s · %s", ver, env.ColabPath)
		} else if env.ColabBrokenReason != "" {
			cliDetail = env.ColabBrokenReason
		}
		m.doctorRow(c, pal, "官方 google-colab-cli", vs.CLIInstalled, cliDetail)

		pkgOK := env.UVPath != "" || env.PipxPath != "" || env.BrewPath != ""
		m.doctorRow(c, pal, "包管理环境（uv / pipx / Homebrew）", pkgOK, env.PackageManagerSummary())

		accDetail := "尚未绑定账号（下一步可一键通过 Google 官方授权登录）"
		if len(accounts) > 0 {
			accDetail = fmt.Sprintf("已绑定 %d 个账号", len(accounts))
		}
		m.doctorRow(c, pal, "Google Colab 账号授权", len(accounts) > 0, accDetail)

		if !vs.CLIInstalled {
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
					if vs.IsInstallingCLI {
						status := "正在安装 google-colab-cli，请稍候…"
						cancelLabel := "取消安装"
						if vs.IsCancellingCLIInstall {
							status = "正在终止安装进程组…"
							cancelLabel = "正在取消…"
						}
						ui.Text(c, status).FontSize(13).FontWeight(500).TextColor(pal.Amber)
						SmallActionButton(c, pal, cancelLabel, &pal.Coral, vs.IsCancellingCLIInstall, func() {
							m.Svc.CancelCLIInstall()
						})
					} else {
						installDisabled := env.RecommendedMethod.RequiresRemoteScriptConsent() && !m.AgreedRemoteScript
						PillButton(c, pal, env.RecommendedMethod.ShortButtonTitle(), true, installDisabled, func() {
							m.Svc.InstallColabCLIAutomatically()
						})
						PillButton(c, pal, "复制命令自己装", false, false, func() {
							m.Svc.CopyToClipboard(env.RecommendedMethod.ManualShellCommand(), "CLI 安装命令")
						})
					}
					ui.Spacer(c)
				})

				if vs.CLIInstallError != "" {
					ui.Text(c, "⚠ "+vs.CLIInstallError).FontSize(12.5).TextColor(pal.Coral)
				}

				if len(vs.CLIInstallLogs) > 0 {
					ui.Column(c).FillWidth().Padding(10).Radius(8).Background(ui.RGBA(0, 0, 0, 0.25)).Children(func() {
						tail := vs.CLIInstallLogs
						if len(tail) > 8 {
							tail = tail[len(tail)-8:]
						}
						ui.Text(c, strings.Join(tail, "\n")).FontSize(11.5).Font("monospace").TextColor(pal.TextMuted)
					})
				}
			})
		} else {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Padding(6, 0, 0, 0).Children(func() {
				ui.Text(c, "✓ 环境已就绪，google-colab-cli 可正常工作").FontSize(13.5).FontWeight(600).TextColor(pal.Emerald)
				ui.Spacer(c)
				if len(accounts) > 0 {
					PillButton(c, pal, "进入 Colab Tools 主控制台 →", true, false, func() {
						m.Svc.CompleteOnboarding()
					})
				} else {
					SmallActionButton(c, pal, "导入终端已登账号并进入", nil, vs.IsImportingDefault, func() {
						go func() {
							if m.Svc.TryImportSystemDefaultAccount() {
								m.Svc.CompleteOnboarding()
							} else {
								m.Svc.ShowToast("未检测到终端默认 colab 登录，请点击下一步完成 Google 授权")
								m.Svc.SetOnboardingStep(2)
							}
						}()
					})
					PillButton(c, pal, "下一步：绑定 Google 账号 →", true, false, func() {
						m.Svc.SetOnboardingStep(2)
					})
				}
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
		StatusPill(c, pal, &dotCol, badgeText)
	})
}

func (m *MainUI) onboardingStep2Accounts(c *ui.Context, pal Palette) {
	vs := m.Svc.ViewState()
	accounts := m.Svc.AccountsSnapshot()
	SectionCard(c, pal, "", "", func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).Gap(3).Children(func() {
				ui.Text(c, "绑定你的第一个 Google Colab 账号").FontSize(16).FontWeight(600).TextColor(pal.TextPrimary)
				ui.Text(c, "凭证仅加密存于本机 macOS 钥匙串；绑定后自动识别你的邮箱、余额与实际可用显卡。").FontSize(12.5).TextColor(pal.TextMuted)
			})
			SmallActionButton(c, pal, "自动导入终端已登账号", nil, !vs.CLIInstalled || vs.IsImportingDefault, func() {
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
						ui.Textf(c, "%s · %s", acc.DisplayName, acc.DisplayTierBadge()).FontSize(13.5).FontWeight(600).TextColor(pal.TextPrimary)
						email := acc.Email
						if email == "" {
							email = "本机已登录的 Google 账号"
						}
						ui.Textf(c, "%s · 可用显卡：%s", email, acc.EligibleCardsSummary()).FontSize(12).TextColor(pal.TextMuted)
					})
					balTxt := "读取中…"
					if rt.LastUsageFetchAt != nil {
						balTxt = fmt.Sprintf("%s CU", core.FormatCU(rt.CUBalance))
					}
					ui.Text(c, balTxt).FontSize(14).FontWeight(600).FontFeatures("tnum").TextColor(pal.TextPrimary)
					e := pal.Emerald
					StatusPill(c, pal, &e, "已绑定")
				})
			}
			ui.Row(c).FillWidth().Padding(6, 0, 0, 0).Children(func() {
				PillButton(c, pal, "← 返回体检", false, false, func() {
					m.Svc.SetOnboardingStep(1)
				})
				ui.Spacer(c)
				PillButton(c, pal, "进入 Colab Tools 主控制台 →", true, false, func() {
					m.Svc.CompleteOnboarding()
				})
			})
		} else {
			m.renderEmbeddedOAuthBinder(c, pal, func() {
				m.Svc.CompleteOnboarding()
			})
		}
	})
}
