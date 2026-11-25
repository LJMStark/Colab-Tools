import AppKit
import SwiftUI

@MainActor
final class SettingsWindowController: NSObject, NSWindowDelegate {
    private weak var store: WorkspaceStore?
    private var window: NSWindow?

    init(store: WorkspaceStore) {
        self.store = store
        super.init()
    }

    func show(section: WorkspaceStore.SettingsSection? = nil) {
        guard let store else { return }
        if let section {
            store.selectedSettingsSection = section
        }
        store.refreshClientDetection()

        if let existing = window {
            NSApp.activate(ignoringOtherApps: true)
            existing.makeKeyAndOrderFront(nil)
            return
        }

        let contentView = SettingsAppView(store: store, isPreviewMode: false)
        let hosting = NSHostingController(rootView: contentView)

        let win = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 820, height: 640),
            styleMask: [.titled, .closable, .miniaturizable, .resizable, .fullSizeContentView],
            backing: .buffered,
            defer: false
        )
        win.title = "Colab Tools 设置"
        win.titleVisibility = .hidden
        win.titlebarAppearsTransparent = true
        win.minSize = NSSize(width: 720, height: 520)
        win.isReleasedWhenClosed = false
        win.delegate = self
        win.contentViewController = hosting
        win.center()

        window = win
        NSApp.activate(ignoringOtherApps: true)
        win.makeKeyAndOrderFront(nil)
    }

    func close() {
        window?.close()
    }
}
