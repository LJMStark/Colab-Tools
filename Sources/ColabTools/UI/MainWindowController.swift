import AppKit
import SwiftUI

/// Controls the Mole-style standalone desktop main window (`1160 × 760 pt`).
/// Automatically elevates to `.regular` (visible in Dock & Cmd+Tab) while open,
/// and returns cleanly to `.accessory` (menu bar background companion) when closed.
@MainActor
final class MainWindowController: NSObject, NSWindowDelegate {
    static let defaultSize = NSSize(width: 1160, height: 760)
    static let minWindowSize = NSSize(width: 980, height: 660)

    private weak var store: WorkspaceStore?
    private var window: NSWindow?

    init(store: WorkspaceStore) {
        self.store = store
        super.init()
    }

    var isVisible: Bool {
        window?.isVisible ?? false
    }

    func show(section: WorkspaceStore.MainSection? = nil, onboarding: Bool? = nil) {
        guard let store else { return }
        if let section {
            store.selectedMainSection = section
        }
        if let onboarding {
            store.showingOnboarding = onboarding
        }
        store.refreshClientDetection()

        NSApp.setActivationPolicy(.regular)

        if let existing = window {
            let current = existing.contentLayoutRect.size
            if current.width < Self.minWindowSize.width || current.height < Self.minWindowSize.height {
                existing.setContentSize(Self.clampedDefaultSize())
                existing.center()
            }
            NSApp.activate(ignoringOtherApps: true)
            existing.makeKeyAndOrderFront(nil)
            return
        }

        let targetSize = Self.clampedDefaultSize()
        let contentView = MainAppView(store: store, isPreviewMode: false)
        let hosting = NSHostingController(rootView: contentView)
        // Prevent NSHostingController from shrinking the initial NSWindow down to SwiftUI's minSize.
        hosting.sizingOptions = []

        let win = NSWindow(
            contentRect: NSRect(origin: .zero, size: targetSize),
            styleMask: [.titled, .closable, .miniaturizable, .resizable, .fullSizeContentView],
            backing: .buffered,
            defer: false
        )
        win.title = "Colab Tools"
        win.titleVisibility = .hidden
        win.titlebarAppearsTransparent = true
        win.minSize = Self.minWindowSize
        win.isReleasedWhenClosed = false
        win.delegate = self
        win.contentViewController = hosting
        win.setContentSize(targetSize)
        win.center()

        window = win
        NSApp.activate(ignoringOtherApps: true)
        win.makeKeyAndOrderFront(nil)
    }

    private static func clampedDefaultSize() -> NSSize {
        guard let visible = NSScreen.main?.visibleFrame else {
            return defaultSize
        }
        let maxW = max(minWindowSize.width, visible.width - 48)
        let maxH = max(minWindowSize.height, visible.height - 64)
        return NSSize(
            width: min(defaultSize.width, maxW),
            height: min(defaultSize.height, maxH)
        )
    }

    func close() {
        window?.close()
    }

    func windowWillClose(_ notification: Notification) {
        // Return gracefully to quiet menu-bar-only mode once the main window is dismissed
        DispatchQueue.main.async {
            if !(self.window?.isVisible ?? false) {
                NSApp.setActivationPolicy(.accessory)
            }
        }
    }
}
