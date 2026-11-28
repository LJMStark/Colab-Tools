package main

import (
	"fmt"
	"log"
	"runtime"
	"sync/atomic"

	"github.com/LJMStark/Colab-Tools/internal/app"
	"github.com/LJMStark/Colab-Tools/internal/uiapp"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func main() {
	mygo.App.SetName("Colab Tools")
	mygo.App.SetVersion(app.AppVersion)

	svc := app.NewService(false)
	mainUI := uiapp.NewMainUI(svc)
	popUI := uiapp.NewPopoverUI(svc)

	var mainWin *mygo.Window
	var flyout *mygo.Window
	var tray *mygo.Tray
	var quitting atomic.Bool

	openMainWindow := func(sec app.MainSection, onboarding bool) {
		if onboarding {
			svc.SetShowingOnboarding(true)
		} else {
			svc.SetSection(sec)
		}
		if runtime.GOOS == "darwin" {
			mygo.App.SetActivationPolicy(mygo.ActivationPolicyRegular)
		}
		if mainWin == nil || mainWin.IsDestroyed() {
			mainWin = mygo.NewWindow(mygo.WindowOptions{
				Title:         "Colab Tools",
				Width:         1160,
				Height:        760,
				MinWidth:      980,
				MinHeight:     660,
				StateKey:      "colab-tools-main-console",
				TitleBarStyle: mygo.TitleBarHidden,
				Content:       ui.View(mainUI.View),
			})
			mainWin.OnClose(func(e *mygo.CloseEvent) {
				if quitting.Load() {
					return
				}
				// Hide to quiet menu bar / system tray instead of quitting the background monitor
				e.PreventDefault()
				mainWin.Hide()
				if runtime.GOOS == "darwin" {
					mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
				}
			})
		}
		mainWin.Show()
		mainWin.Focus()
		mygo.App.Focus()
		mainWin.Invalidate()
	}

	updateTray := func() {
		if tray == nil {
			return
		}
		st := svc.TrayLiveStatus()
		_ = tray.SetIcon(uiapp.TrayIconPNG(st.RunningCount > 0), true)

		var title string
		var tooltip string
		switch {
		case st.Queuing:
			if st.QueuePersistent {
				maxH := st.QueueMaxHours
				if maxH <= 0 {
					maxH = 8
				}
				title = fmt.Sprintf(" 守候 第%d轮", st.QueueAttempt)
				if st.TargetsSummary != "" {
					tooltip = fmt.Sprintf("Colab Tools v%s — 持续守候第 %d 轮（上限 %dh · %s）", app.AppVersion, st.QueueAttempt, maxH, st.TargetsSummary)
				} else {
					tooltip = fmt.Sprintf("Colab Tools v%s — 持续守候第 %d 轮（上限 %dh）", app.AppVersion, st.QueueAttempt, maxH)
				}
			} else {
				title = fmt.Sprintf(" 抢卡 %d/%d", st.QueueAttempt, st.QueueMax)
				if st.TargetsSummary != "" {
					tooltip = fmt.Sprintf("Colab Tools v%s — 正在抢卡第 %d/%d 轮（%s）", app.AppVersion, st.QueueAttempt, st.QueueMax, st.TargetsSummary)
				} else {
					tooltip = fmt.Sprintf("Colab Tools v%s — 正在抢卡第 %d/%d 轮", app.AppVersion, st.QueueAttempt, st.QueueMax)
				}
			}
		case st.Deploying || st.Starting:
			if st.Deploying && st.HardwareLabel != "" {
				title = fmt.Sprintf(" 部署 %s", st.HardwareLabel)
				tooltip = fmt.Sprintf("Colab Tools v%s — 正在自动部署 %s", app.AppVersion, st.HardwareLabel)
			} else if st.Deploying {
				title = " 部署中"
				tooltip = fmt.Sprintf("Colab Tools v%s — 正在自动部署环境", app.AppVersion)
			} else if st.HardwareLabel != "" {
				title = fmt.Sprintf(" 部署 %s", st.HardwareLabel)
				tooltip = fmt.Sprintf("Colab Tools v%s — 正在开机 %s", app.AppVersion, st.HardwareLabel)
			} else {
				title = " 开机中"
				tooltip = fmt.Sprintf("Colab Tools v%s — 正在申请开机", app.AppVersion)
			}
		case st.RunningCount > 0:
			if st.StatusBarMode == "detailed" {
				title = fmt.Sprintf(" %d GPU · %.0f CU", st.RunningCount, st.TotalBalance)
			} else {
				title = fmt.Sprintf(" %d GPU", st.RunningCount)
			}
			tooltip = fmt.Sprintf("Colab Tools v%s — %d 运行中 · %.1f CU", app.AppVersion, st.RunningCount, st.TotalBalance)
		case st.FocusHasError:
			title = " 未抢到"
			if st.FocusErrorText != "" {
				tooltip = fmt.Sprintf("Colab Tools v%s — 未抢到（%s）", app.AppVersion, st.FocusErrorText)
			} else {
				tooltip = fmt.Sprintf("Colab Tools v%s — 上次抢卡未成功", app.AppVersion)
			}
		default:
			if st.StatusBarMode == "detailed" {
				title = fmt.Sprintf(" %.0f CU", st.TotalBalance)
			} else {
				title = ""
			}
			tooltip = fmt.Sprintf("Colab Tools v%s — %d 运行中 · %.1f CU", app.AppVersion, st.RunningCount, st.TotalBalance)
		}

		if runtime.GOOS == "darwin" {
			if st.StatusBarMode == "icon_only" {
				tray.SetTitle("")
			} else {
				tray.SetTitle(title)
			}
		}
		tray.SetToolTip(tooltip)
	}

	doFinalQuit := func() {
		if quitting.Swap(true) {
			return
		}
		svc.Shutdown()
		mygo.App.Quit()
	}
	requestAppQuit := func() {
		if flyout != nil && !flyout.IsDestroyed() {
			flyout.Hide()
		}
		if svc.TotalManagedRunningSessionCount() > 0 {
			openMainWindow(app.SectionHome, false)
			svc.RequestConfirmQuit(doFinalQuit)
			return
		}
		doFinalQuit()
	}

	svc.OnOpenMainWin = openMainWindow
	svc.OnUIInvalidate = func() {
		if mainWin != nil && !mainWin.IsDestroyed() {
			mainWin.Invalidate()
		}
		if flyout != nil && !flyout.IsDestroyed() {
			flyout.Invalidate()
		}
	}
	svc.OnTrayUpdate = updateTray
	popUI.OnOpenMain = func(sec app.MainSection) {
		if flyout != nil && !flyout.IsDestroyed() {
			flyout.Hide()
		}
		openMainWindow(sec, false)
	}
	popUI.OnOpenOnboarding = func() {
		if flyout != nil && !flyout.IsDestroyed() {
			flyout.Hide()
		}
		svc.ReopenOnboarding()
		openMainWindow(app.SectionHome, true)
	}
	popUI.OnRequestStop = func(accountID string) {
		if flyout != nil && !flyout.IsDestroyed() {
			flyout.Hide()
		}
		openMainWindow(app.SectionHome, false)
		svc.RequestConfirmStop(accountID)
	}
	popUI.OnRequestStopAll = func() {
		if flyout != nil && !flyout.IsDestroyed() {
			flyout.Hide()
		}
		openMainWindow(app.SectionHome, false)
		svc.RequestConfirmStopAll()
	}
	popUI.OnQuit = requestAppQuit

	mygo.App.WhenReady(func() {
		if !mygo.App.RequestSingleInstanceLock() {
			mygo.App.Quit()
			return
		}
		mygo.App.OnSecondInstance(func(_ []string, _ string) {
			openMainWindow(app.SectionHome, false)
		})
		mygo.App.OnActivate(func(hasVisibleWindows bool) {
			if !hasVisibleWindows {
				openMainWindow(svc.ViewState().SelectedSection, false)
			}
		})
		mygo.App.OnBeforeQuit(func(e *mygo.QuitEvent) {
			if quitting.Load() {
				return
			}
			if !e.SessionEnding && svc.TotalManagedRunningSessionCount() > 0 {
				e.PreventDefault()
				requestAppQuit()
				return
			}
			quitting.Store(true)
			svc.Shutdown()
		})

		// Start quiet in tray unless onboarding/user opens main console
		if runtime.GOOS == "darwin" {
			mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
		}

		var err error
		tray, err = mygo.NewTray(mygo.TrayOptions{
			ID:             "colab-tools-status-tray",
			Icon:           uiapp.TrayIconPNG(false),
			IconIsTemplate: true,
			ToolTip:        "Colab Tools v" + app.AppVersion,
		})
		if err == nil {
			flyout = mygo.NewFlyout(mygo.FlyoutOptions{
				Tray:         tray,
				Width:        420,
				Height:       620,
				Popover:      true,
				CornerRadius: 14,
				Content:      ui.View(popUI.View),
			})
			trayMenu := mygo.NewMenu([]*mygo.MenuItem{
				{
					ID:    "open-main",
					Label: "打开主控制台…",
					Click: func(_ *mygo.MenuItem, _ *mygo.Window) { openMainWindow(app.SectionHome, false) },
				},
				{
					ID:    "refresh-all",
					Label: "立即刷新全部账号",
					Click: func(_ *mygo.MenuItem, _ *mygo.Window) { go svc.RefreshAllAccounts(true, false) },
				},
				{
					ID:    "doctor",
					Label: "环境体检与开箱向导…",
					Click: func(_ *mygo.MenuItem, _ *mygo.Window) {
						svc.ReopenOnboarding()
						openMainWindow(app.SectionHome, true)
					},
				},
				mygo.Separator(),
				{
					ID:    "stop-all",
					Label: "全部关机止损…",
					Click: func(_ *mygo.MenuItem, _ *mygo.Window) {
						openMainWindow(app.SectionHome, false)
						svc.RequestConfirmStopAll()
					},
				},
				mygo.Separator(),
				{
					ID:    "quit",
					Label: "退出 Colab Tools",
					Click: func(_ *mygo.MenuItem, _ *mygo.Window) {
						requestAppQuit()
					},
				},
			})

			tray.OnClick(func() {
				if flyout != nil && !flyout.IsDestroyed() {
					if flyout.IsVisible() {
						flyout.Hide()
					} else {
						flyout.Show()
						flyout.Focus()
					}
				} else {
					openMainWindow(app.SectionHome, false)
				}
			})
			tray.OnRightClick(func() {
				tray.SetMenu(trayMenu)
				tray.PopUpMenu()
				tray.SetMenu(nil)
			})
		}

		updateTray()
		svc.StartBackgroundServices()
		// Open main console on initial launch for immediate visibility
		openMainWindow(app.SectionHome, svc.ViewState().ShowingOnboarding)
	})

	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
