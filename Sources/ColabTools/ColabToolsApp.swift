import AppKit
import ColabToolsCore
import SwiftUI

@main
struct ColabToolsEntry {
    static func main() {
        let app = NSApplication.shared
        let delegate = AppDelegate()
        app.delegate = delegate
        app.setActivationPolicy(.accessory)
        app.run()
    }
}

@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate, NSPopoverDelegate {
    private var statusItem: NSStatusItem?
    private var popover: NSPopover?
    private var settingsWindowController: SettingsWindowController?
    private var store: WorkspaceStore?
    private var eventMonitor: Any?

    func applicationDidFinishLaunching(_ notification: Notification) {
        if PreviewExporter.runIfRequested() {
            NSApplication.shared.terminate(nil)
            return
        }
        setupMainMenu()

        let s = WorkspaceStore()
        store = s
        settingsWindowController = SettingsWindowController(store: s)
        s.onStatusBarNeedsUpdate = { [weak self] in
            self?.updateStatusItem()
        }
        // Keep popover pinned open during browser OAuth2 authorization so pasted codes aren't lost
        s.onPopoverPinChanged = { [weak self] pinned in
            self?.popover?.behavior = pinned ? .applicationDefined : .transient
        }
        s.onOpenSettingsWindow = { [weak self] section in
            self?.openSettingsWindow(section: section)
        }

        let item = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        statusItem = item
        if let button = item.button {
            button.action = #selector(handleStatusBarClick(_:))
            button.target = self
            button.sendAction(on: [.leftMouseUp, .rightMouseUp])
        }

        let pop = NSPopover()
        pop.behavior = .transient
        pop.animates = true
        pop.delegate = self
        let hosting = NSHostingController(rootView: RootPopoverView(store: s))
        hosting.sizingOptions = .preferredContentSize
        pop.contentViewController = hosting
        pop.contentSize = NSSize(width: RootPopoverView.panelWidth, height: 760)
        popover = pop

        updateStatusItem()

        eventMonitor = NSEvent.addGlobalMonitorForEvents(matching: [.leftMouseDown, .rightMouseDown]) { [weak self] _ in
            guard let self, let pop = self.popover, pop.isShown, !(self.store?.showingAddAccount ?? false) else { return }
            pop.performClose(nil)
        }
    }

    func applicationWillTerminate(_ notification: Notification) {
        if let monitor = eventMonitor {
            NSEvent.removeMonitor(monitor)
        }
        store?.cleanup()
    }

    @objc private func handleStatusBarClick(_ sender: NSStatusBarButton) {
        guard let event = NSApp.currentEvent else {
            togglePopover(sender)
            return
        }
        if event.type == .rightMouseUp {
            showContextMenu()
        } else {
            togglePopover(sender)
        }
    }

    private func togglePopover(_ sender: NSStatusBarButton) {
        guard let popover, let store else { return }
        if popover.isShown {
            popover.performClose(sender)
        } else {
            NSApp.activate(ignoringOtherApps: true)
            popover.show(relativeTo: sender.bounds, of: sender, preferredEdge: .minY)
            popover.contentViewController?.view.window?.makeKey()
            Task { await store.pollAllRunningSlots() }
        }
    }

    private func showContextMenu() {
        guard let store, let statusItem else { return }
        let menu = NSMenu()
        let running = store.runningAccounts
        let queuingAcc = store.settings.accounts.first(where: { store.state(for: $0.id).phase.isQueuing })
        let busyAcc = store.settings.accounts.first(where: { store.state(for: $0.id).phase.isBusy })
        let headerTitle: String = {
            if !running.isEmpty {
                return "Colab Tools · \(running.count) 运行中 · 余额 \(Formatters.cu(store.totalBalanceCU)) CU"
            }
            if let qAcc = queuingAcc, case .queuing(let a, let m, _, _) = store.state(for: qAcc.id).phase {
                return "Colab Tools · 排队中 \(a)/\(m) (\(qAcc.displayName))"
            }
            if let bAcc = busyAcc {
                return "Colab Tools · 处理中 (\(bAcc.displayName))"
            }
            return "Colab Tools · 已关闭"
        }()
        let header = NSMenuItem(title: headerTitle, action: nil, keyEquivalent: "")
        header.isEnabled = false
        menu.addItem(header)
        menu.addItem(.separator())

        if let acc = store.selectedAccount {
            let st = store.state(for: acc.id)
            if st.phase.isRunning {
                menu.addItem(withTitle: "重置空闲计时 (\(acc.displayName))", action: #selector(menuResetIdle), keyEquivalent: "")
                menu.addItem(withTitle: "在浏览器打开 Colab", action: #selector(menuOpenWeb), keyEquivalent: "")
                menu.addItem(withTitle: "关机止损 (\(acc.displayName))", action: #selector(menuStopCurrent), keyEquivalent: "")
            } else if st.phase.isQueuing {
                menu.addItem(withTitle: "取消算力排队 (\(acc.displayName))", action: #selector(menuCancelQueue), keyEquivalent: "")
            } else {
                menu.addItem(withTitle: acc.enablePoliteQueue ? "排队启动 (\(acc.displayName))" : "一键启动 (\(acc.displayName))", action: #selector(menuStartCurrent), keyEquivalent: "")
            }
        }

        if running.count >= 2 {
            menu.addItem(.separator())
            menu.addItem(withTitle: "全部账号关机止损", action: #selector(menuStopAll), keyEquivalent: "")
        }

        menu.addItem(.separator())
        menu.addItem(withTitle: "偏好设置…", action: #selector(menuOpenSettings), keyEquivalent: ",")
        menu.addItem(withTitle: "管理订阅 / 购点 (colab pay)", action: #selector(menuPay), keyEquivalent: "")
        menu.addItem(withTitle: "退出 Colab Tools", action: #selector(menuQuit), keyEquivalent: "q")

        for item in menu.items where item.action != nil {
            item.target = self
        }
        statusItem.menu = menu
        statusItem.button?.performClick(nil)
        statusItem.menu = nil
    }

    @objc private func menuStartCurrent() {
        guard let store, let acc = store.selectedAccount else { return }
        Task { await store.startAccountSession(acc.id) }
    }

    @objc private func menuStopCurrent() {
        guard let store, let acc = store.selectedAccount else { return }
        Task { await store.stopAccountSession(acc.id) }
    }

    @objc private func menuCancelQueue() {
        guard let store, let acc = store.selectedAccount else { return }
        store.cancelQueue(for: acc.id)
    }

    @objc private func menuStopAll() {
        guard let store else { return }
        let targets = store.runningAccounts.map(\.0.id)
        Task {
            for id in targets {
                await store.stopAccountSession(id)
            }
        }
    }

    @objc private func menuResetIdle() {
        guard let store, let acc = store.selectedAccount else { return }
        store.resetIdleTimer(for: acc.id)
    }

    @objc private func menuOpenWeb() {
        guard let store, let acc = store.selectedAccount else { return }
        store.openColabInBrowser(accountId: acc.id)
    }

    @objc private func menuOpenSettings() { openSettingsWindow(section: .general) }
    @objc private func menuPay() { store?.openPayPage() }
    @objc private func menuQuit() { NSApp.terminate(nil) }

    private func openSettingsWindow(section: WorkspaceStore.SettingsSection) {
        popover?.performClose(nil)
        settingsWindowController?.show(section: section)
    }

    private func setupMainMenu() {
        let mainMenu = NSMenu()

        let appMenuItem = NSMenuItem()
        mainMenu.addItem(appMenuItem)
        let appMenu = NSMenu(title: "Colab Tools")
        let prefsItem = NSMenuItem(title: "偏好设置…", action: #selector(menuOpenSettings), keyEquivalent: ",")
        prefsItem.target = self
        appMenu.addItem(prefsItem)
        appMenu.addItem(.separator())
        let quitItem = NSMenuItem(title: "退出 Colab Tools", action: #selector(menuQuit), keyEquivalent: "q")
        quitItem.target = self
        appMenu.addItem(quitItem)
        appMenuItem.submenu = appMenu

        let editMenuItem = NSMenuItem()
        mainMenu.addItem(editMenuItem)
        let editMenu = NSMenu(title: "编辑")
        editMenu.addItem(withTitle: "撤销", action: Selector(("undo:")), keyEquivalent: "z")
        editMenu.addItem(withTitle: "重做", action: Selector(("redo:")), keyEquivalent: "Z")
        editMenu.addItem(.separator())
        editMenu.addItem(withTitle: "剪切", action: #selector(NSText.cut(_:)), keyEquivalent: "x")
        editMenu.addItem(withTitle: "拷贝", action: #selector(NSText.copy(_:)), keyEquivalent: "c")
        editMenu.addItem(withTitle: "粘贴", action: #selector(NSText.paste(_:)), keyEquivalent: "v")
        editMenu.addItem(withTitle: "全选", action: #selector(NSText.selectAll(_:)), keyEquivalent: "a")
        editMenuItem.submenu = editMenu

        let windowMenuItem = NSMenuItem()
        mainMenu.addItem(windowMenuItem)
        let windowMenu = NSMenu(title: "窗口")
        windowMenu.addItem(withTitle: "关闭窗口", action: #selector(NSWindow.performClose(_:)), keyEquivalent: "w")
        windowMenuItem.submenu = windowMenu

        NSApp.mainMenu = mainMenu
    }

    private func updateStatusItem() {
        guard let store, let button = statusItem?.button else { return }
        let running = store.runningAccounts
        let anyProcessing = running.contains(where: { $0.1.isProcessing })
        let anyBusy = store.settings.accounts.contains(where: { store.state(for: $0.id).phase.isBusy })
        let activeQueueInfo: (Int, Int)? = store.settings.accounts.compactMap { acc in
            if case .queuing(let a, let m, _, _) = store.state(for: acc.id).phase { return (a, m) }
            return nil
        }.first

        button.image = StatusBarIcon.makeTemplateImage(isRunning: !running.isEmpty, isProcessing: anyProcessing || activeQueueInfo != nil)
        button.imagePosition = .imageLeading

        let title: String
        switch store.settings.menuBarMode {
        case .iconOnly:
            title = ""
        case .compact:
            if anyBusy {
                title = " 处理中"
            } else if let q = activeQueueInfo, running.isEmpty {
                title = " 排队 \(q.0)/\(q.1)"
            } else if running.count >= 2 {
                title = String(format: " %d 运行中 · %.1f/h", running.count, store.totalHourlyRateCU)
            } else if let (acc, st) = running.first {
                let wd = store.watchdogStatus(for: acc)
                if acc.autoShutdownEnabled && wd != .pausedNoSignal && !st.isProcessing {
                    title = " \(st.shortGPUName) · \(Formatters.shortMinutes(st.remainingIdleSeconds(timeoutMinutes: acc.idleTimeoutMinutes)))"
                } else {
                    title = " \(st.shortGPUName)"
                }
            } else {
                title = ""
            }
        case .detailed:
            if anyBusy {
                title = " 处理中…"
            } else if let q = activeQueueInfo, running.isEmpty {
                title = " 排队 \(q.0)/\(q.1)"
            } else if running.count >= 2 {
                title = String(format: " %d 运行中 · %@ CU", running.count, Formatters.cu(store.totalBalanceCU))
            } else if let (acc, st) = running.first {
                let vram = st.vramTotalMB > 0 ? String(format: " %.0fG", Double(st.vramUsedMB) / 1024.0) : ""
                let wd = store.watchdogStatus(for: acc)
                let idle = (acc.autoShutdownEnabled && wd != .pausedNoSignal)
                    ? " · 剩\(Formatters.shortMinutes(st.remainingIdleSeconds(timeoutMinutes: acc.idleTimeoutMinutes)))"
                    : ""
                title = " \(st.shortGPUName)\(vram)\(idle)"
            } else {
                title = " 离线"
            }
        }

        let attrs: [NSAttributedString.Key: Any] = [
            .font: NSFont.monospacedDigitSystemFont(ofSize: 12, weight: .medium)
        ]
        button.attributedTitle = NSAttributedString(string: title, attributes: attrs)
    }
}
