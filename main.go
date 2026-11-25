package main

import (
	"fmt"
	"log"
	"runtime"

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
	var quitting bool

	openMainWindow := func(sec app.MainSection, onboarding bool) {
		if onboarding {
			svc.ShowingOnboarding = true
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
				if quitting {
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
		running := svc.RunningAccountCount()
		bal, _ := svc.TotalBalanceAndRate()
		_ = tray.SetIcon(uiapp.TrayIconPNG(running > 0), true)
		if runtime.GOOS == "darwin" {
			switch svc.Settings.StatusBarMode {
			case "icon_only":
				tray.SetTitle("")
			case "detailed":
				if running > 0 {
					tray.SetTitle(fmt.Sprintf(" %d GPU · %.0f CU", running, bal))
				} else {
					tray.SetTitle(fmt.Sprintf(" %.0f CU", bal))
				}
			default:
				if running > 0 {
					tray.SetTitle(fmt.Sprintf(" %d GPU", running))
				} else {
					tray.SetTitle("")
				}
			}
		}
		tray.SetToolTip(fmt.Sprintf("Colab Tools v%s — %d 运行中 · %.1f CU", app.AppVersion, running, bal))
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
	popUI.OnQuit = func() {
		quitting = true
		svc.Shutdown()
		mygo.App.Quit()
	}

	mygo.App.WhenReady(func() {
		if !mygo.App.RequestSingleInstanceLock() {
			mygo.App.Quit()
			return
		}
		mygo.App.OnSecondInstance(func(_ []string, _ string) {
			openMainWindow(app.SectionStatus, false)
		})
		mygo.App.OnActivate(func(hasVisibleWindows bool) {
			if !hasVisibleWindows {
				openMainWindow(svc.SelectedSection, false)
			}
		})
		mygo.App.OnBeforeQuit(func(_ *mygo.QuitEvent) {
			quitting = true
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
					Click: func(_ *mygo.MenuItem, _ *mygo.Window) { openMainWindow(app.SectionStatus, false) },
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
						openMainWindow(app.SectionStatus, true)
					},
				},
				mygo.Separator(),
				{
					ID:    "stop-all",
					Label: "全部关机止损",
					Click: func(_ *mygo.MenuItem, _ *mygo.Window) { go svc.StopAllManagedSessions() },
				},
				mygo.Separator(),
				{
					ID:    "quit",
					Label: "退出 Colab Tools",
					Click: func(_ *mygo.MenuItem, _ *mygo.Window) {
						quitting = true
						svc.Shutdown()
						mygo.App.Quit()
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
					openMainWindow(app.SectionStatus, false)
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
		openMainWindow(app.SectionStatus, svc.ShowingOnboarding)
	})

	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
