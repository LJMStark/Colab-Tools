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
    private var store: WorkspaceStore?
    private var eventMonitor: Any?

    func applicationDidFinishLaunching(_ notification: Notification) {
        if PreviewExporter.runIfRequested() {
            NSApplication.shared.terminate(nil)
            return
        }
        let s = WorkspaceStore()
        store = s
        s.onStatusBarNeedsUpdate = { [weak self] in
            self?.updateStatusItem()
        }
        // Keep popover pinned open during browser OAuth2 authorization so pasted codes aren't lost
        s.onPopoverPinChanged = { [weak self] pinned in
            self?.popover?.behavior = pinned ? .applicationDefined : .transient
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
        pop.contentViewController = NSHostingController(rootView: RootPopoverView(store: s))
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
        let headerTitle = running.isEmpty
            ? "Colab Tools · 已关闭"
            : "Colab Tools · \(running.count) 运行中 · 余额 \(Formatters.cu(store.totalBalanceCU)) CU"
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
            } else {
                menu.addItem(withTitle: "一键启动 (\(acc.displayName))", action: #selector(menuStartCurrent), keyEquivalent: "")
            }
        }

        if running.count >= 2 {
            menu.addItem(.separator())
            menu.addItem(withTitle: "全部账号关机止损", action: #selector(menuStopAll), keyEquivalent: "")
        }

        menu.addItem(.separator())
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

    @objc private func menuPay() { store?.openPayPage() }
    @objc private func menuQuit() { NSApp.terminate(nil) }

    private func updateStatusItem() {
        guard let store, let button = statusItem?.button else { return }
        let running = store.runningAccounts
        let anyProcessing = running.contains(where: { $0.1.isProcessing })
        let anyBusy = store.settings.accounts.contains(where: { store.state(for: $0.id).phase.isBusy })

        button.image = StatusBarIcon.makeTemplateImage(isRunning: !running.isEmpty, isProcessing: anyProcessing)
        button.imagePosition = .imageLeading

        let title: String
        switch store.settings.menuBarMode {
        case .iconOnly:
            title = ""
        case .compact:
            if anyBusy {
                title = " 处理中"
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
