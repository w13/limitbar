import AppKit
import Combine
import ServiceManagement
import SwiftUI

/// Status item and its hover/pinned panel.
/// Left click: pins/unpins panel.
/// Right click: native context menu (Refresh, Launch at Login, GitHub, Quit).
final class AppDelegate: NSResponder, NSApplicationDelegate {
    private var item: NSStatusItem!
    private var tray: PassThroughHostingView<TrayView>!
    private var panel: NSPanel!
    private var panelHost: NSHostingView<AnyView>!
    private let store = Store()
    private var subs = Set<AnyCancellable>()
    private var pinned = false
    private var showTimer: Timer?
    private var hideTimer: Timer?
    private var outsideClick: Any?
    private var overItem = false
    private var overPanel = false

    override init() { super.init() }
    required init?(coder: NSCoder) { fatalError("not used") }

    func applicationDidFinishLaunching(_ notification: Notification) {
        item = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        guard let button = item.button else { return }
        tray = PassThroughHostingView(rootView: TrayView(store: store))
        tray.translatesAutoresizingMaskIntoConstraints = false
        button.addSubview(tray)
        NSLayoutConstraint.activate([tray.centerXAnchor.constraint(equalTo: button.centerXAnchor),
                                     tray.centerYAnchor.constraint(equalTo: button.centerYAnchor)])
        button.target = self
        button.action = #selector(clicked)
        button.sendAction(on: [.leftMouseUp, .rightMouseUp])
        button.setAccessibilityLabel("AI subscription limits")
        button.addTrackingArea(NSTrackingArea(rect: .zero, options: [.mouseEnteredAndExited, .activeAlways, .inVisibleRect],
                                              owner: self, userInfo: ["zone": "item"]))
        resizeItem()
        store.$snaps.receive(on: RunLoop.main).sink { [weak self] _ in
            DispatchQueue.main.async { self?.resizeItem(); self?.resizePanel() }
        }.store(in: &subs)
        store.$busy.receive(on: RunLoop.main).sink { [weak self] _ in
            DispatchQueue.main.async { self?.resizePanel() }
        }.store(in: &subs)
        buildPanel()
    }

    private func resizeItem() {
        tray.layoutSubtreeIfNeeded()
        item.length = ceil(tray.fittingSize.width)
        item.button?.toolTip = "AI Limits (Left-click to pin, Right-click for options)"
    }

    // MARK: - Panel

    private func buildPanel() {
        panel = NSPanel(contentRect: NSRect(x: 0, y: 0, width: 530, height: 380),
                        styleMask: [.nonactivatingPanel, .borderless], backing: .buffered, defer: true)
        panel.isOpaque = false
        panel.backgroundColor = .clear
        panel.hasShadow = true
        panel.level = .popUpMenu
        panel.collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary, .transient]
        panel.isReleasedWhenClosed = false
        panel.hidesOnDeactivate = false

        let fx = NSVisualEffectView()
        fx.material = .popover
        fx.blendingMode = .behindWindow
        fx.state = .active
        fx.wantsLayer = true
        fx.layer?.cornerRadius = 16
        fx.layer?.cornerCurve = .continuous
        fx.layer?.masksToBounds = true
        fx.layer?.borderWidth = 0.5
        fx.layer?.borderColor = NSColor.white.withAlphaComponent(0.22).cgColor

        panelHost = NSHostingView(rootView: AnyView(PanelView(store: store, onQuit: { NSApp.terminate(nil) })))
        panelHost.translatesAutoresizingMaskIntoConstraints = false
        fx.addSubview(panelHost)
        NSLayoutConstraint.activate([panelHost.leadingAnchor.constraint(equalTo: fx.leadingAnchor),
                                     panelHost.trailingAnchor.constraint(equalTo: fx.trailingAnchor),
                                     panelHost.topAnchor.constraint(equalTo: fx.topAnchor),
                                     panelHost.bottomAnchor.constraint(equalTo: fx.bottomAnchor)])
        panel.contentView = fx
        fx.addTrackingArea(NSTrackingArea(rect: .zero, options: [.mouseEnteredAndExited, .activeAlways, .inVisibleRect],
                                          owner: self, userInfo: ["zone": "panel"]))
    }

    private func resizePanel() {
        guard panel != nil else { return }
        panelHost.layoutSubtreeIfNeeded()
        let size = panelHost.fittingSize
        guard size.height > 0, let button = item.button, let win = button.window else { return }
        let r = win.convertToScreen(button.convert(button.bounds, to: nil))
        guard let screen = (win.screen ?? NSScreen.main)?.visibleFrame else { return }
        let width: CGFloat = 530
        let maxAllowedHeight = min(screen.height - 60, 640)
        let height = min(size.height, maxAllowedHeight)

        var x = r.midX - width / 2
        x = min(max(x, screen.minX + 8), screen.maxX - width - 8)
        let y = max(screen.minY + 8, r.minY - height - 5)
        panel.setFrame(NSRect(x: x, y: y, width: width, height: height), display: panel.isVisible)
    }

    // MARK: - Actions

    @objc private func clicked() {
        if let event = NSApp.currentEvent, event.type == .rightMouseUp {
            showContextMenu()
            return
        }
        pinned.toggle()
        if pinned {
            showPanel()
            armOutsideClick()
        } else {
            disarmOutsideClick()
            hidePanel()
        }
    }

    private func showContextMenu() {
        let menu = NSMenu()
        let ver = Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String ?? "1.2.0"
        let titleItem = NSMenuItem(title: "LimitBar v\(ver)", action: nil, keyEquivalent: "")
        titleItem.isEnabled = false
        menu.addItem(titleItem)
        menu.addItem(NSMenuItem.separator())

        let refreshItem = NSMenuItem(title: "Refresh All", action: #selector(refreshAll), keyEquivalent: "r")
        refreshItem.target = self
        menu.addItem(refreshItem)

        let loginItem = NSMenuItem(title: "Launch at Login", action: #selector(toggleLogin), keyEquivalent: "")
        loginItem.target = self
        loginItem.state = SMAppService.mainApp.status == .enabled ? .on : .off
        menu.addItem(loginItem)

        menu.addItem(NSMenuItem.separator())

        let repoItem = NSMenuItem(title: "GitHub Repository…", action: #selector(openRepo), keyEquivalent: "")
        repoItem.target = self
        menu.addItem(repoItem)

        let quitItem = NSMenuItem(title: "Quit LimitBar", action: #selector(quitApp), keyEquivalent: "q")
        quitItem.target = self
        menu.addItem(quitItem)

        item.menu = menu
        item.button?.performClick(nil)
        item.menu = nil
    }

    @objc private func refreshAll() {
        store.refresh(force: true)
    }

    @objc private func toggleLogin() {
        do {
            if SMAppService.mainApp.status == .enabled {
                try SMAppService.mainApp.unregister()
            } else {
                try SMAppService.mainApp.register()
            }
        } catch {}
    }

    @objc private func openRepo() {
        if let url = URL(string: "https://github.com/w13/limitbar") {
            NSWorkspace.shared.open(url)
        }
    }

    @objc private func quitApp() {
        NSApp.terminate(nil)
    }

    override func mouseEntered(with event: NSEvent) {
        guard let zone = event.trackingArea?.userInfo?["zone"] as? String else { return }
        if zone == "item" { overItem = true; store.refresh(olderThan: 90) }
        if zone == "panel" { overPanel = true }
        hideTimer?.invalidate()
        hideTimer = nil
        if !panel.isVisible && showTimer == nil {
            showTimer = Timer.scheduledTimer(withTimeInterval: 0.15, repeats: false) { [weak self] _ in self?.showPanel() }
        }
    }

    override func mouseExited(with event: NSEvent) {
        guard let zone = event.trackingArea?.userInfo?["zone"] as? String else { return }
        if zone == "item" { overItem = false }
        if zone == "panel" { overPanel = false }
        showTimer?.invalidate()
        showTimer = nil
        guard !pinned else { return }
        if hideTimer == nil {
            hideTimer = Timer.scheduledTimer(withTimeInterval: 0.25, repeats: false) { [weak self] _ in
                guard let self = self, !self.overItem, !self.overPanel, !self.pinned else { return }
                self.hidePanel()
            }
        }
    }

    private func showPanel() {
        showTimer?.invalidate()
        showTimer = nil
        resizePanel()
        panel.orderFrontRegardless()
    }

    private func hidePanel() {
        hideTimer?.invalidate()
        hideTimer = nil
        panel.orderOut(nil)
        pinned = false
        disarmOutsideClick()
    }

    private func armOutsideClick() {
        guard outsideClick == nil else { return }
        outsideClick = NSEvent.addGlobalMonitorForEvents(matching: [.leftMouseDown, .rightMouseDown]) { [weak self] _ in
            guard let self = self, self.pinned else { return }
            self.hidePanel()
        }
    }

    private func disarmOutsideClick() {
        if let m = outsideClick { NSEvent.removeMonitor(m); outsideClick = nil }
    }
}

/// NSHostingView that never intercepts mouse movements, so tracking areas below it see enters and exits.
final class PassThroughHostingView<V: View>: NSHostingView<V> {
    override func hitTest(_ point: NSPoint) -> NSView? { nil }
}
