import AppKit
import SwiftUI

@MainActor
final class MenuBarController: NSObject, NSPopoverDelegate {
    private enum IconState {
        case running
        case paused
        case disconnected
    }

    private var statusItem: NSStatusItem?
    private let popover = NSPopover()
    private weak var daemonClient: DaemonClient?
    private weak var router: WorkspaceRouter?
    private var openWorkspace: (() -> Void)?
    private var pollTask: Task<Void, Never>?
    private var didRequestInitialRefresh = false

    func install(
        daemonClient: DaemonClient,
        router: WorkspaceRouter,
        openWorkspace: @escaping () -> Void
    ) {
        self.daemonClient = daemonClient
        self.router = router
        self.openWorkspace = openWorkspace

        if statusItem == nil {
            let item = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
            item.button?.target = self
            item.button?.action = #selector(togglePopover)
            item.button?.sendAction(on: [.leftMouseUp, .rightMouseUp])
            item.button?.setAccessibilityLabel("Iter status")
            statusItem = item

            popover.behavior = .transient
            popover.delegate = self
        }

        updateStatusItemIcon()
        refreshStatusItemOnce()
    }

    @objc private func togglePopover() {
        guard let button = statusItem?.button else { return }
        if popover.isShown {
            closePopover()
            return
        }
        showPopover(from: button)
    }

    private func showPopover(from button: NSStatusBarButton) {
        guard let daemonClient, let router else { return }
        let actions = MenubarActions(
            openDashboard: { [weak self] in self?.openDashboard() },
            shareStack: { [weak self] in self?.shareStack() },
            openSettings: { [weak self] in self?.openSettings() },
            statusChanged: { [weak self] in self?.updateStatusItemIcon() },
            quit: { NSApp.terminate(nil) }
        )
        popover.contentViewController = NSHostingController(
            rootView: MenubarDropdownView(
                daemonClient: daemonClient,
                actions: actions
            )
            .environment(router)
        )
        popover.contentSize = NSSize(width: 304, height: 286)
        popover.show(relativeTo: button.bounds, of: button, preferredEdge: .minY)
        startPolling()
    }

    private func closePopover() {
        stopPolling()
        popover.performClose(nil)
    }

    nonisolated func popoverDidClose(_ notification: Notification) {
        Task { @MainActor in
            stopPolling()
        }
    }

    private func startPolling() {
        stopPolling()
        pollTask = Task { [weak self] in
            while !Task.isCancelled {
                guard let self else { return }
                await self.daemonClient?.refresh()
                self.updateStatusItemIcon()
                try? await Task.sleep(nanoseconds: 5_000_000_000)
            }
        }
    }

    private func stopPolling() {
        pollTask?.cancel()
        pollTask = nil
    }

    private func refreshStatusItemOnce() {
        guard !didRequestInitialRefresh else { return }
        didRequestInitialRefresh = true
        Task { [weak self] in
            guard let self else { return }
            await self.daemonClient?.refresh()
            self.updateStatusItemIcon()
        }
    }

    private func openDashboard() {
        router?.openDashboard()
        focusWorkspace()
        closePopover()
    }

    private func shareStack() {
        router?.openStackShare()
        focusWorkspace()
        closePopover()
    }

    private func openSettings() {
        router?.openSettings()
        focusWorkspace()
        closePopover()
    }

    private func focusWorkspace() {
        openWorkspace?()
        NSApp.activate(ignoringOtherApps: true)
        DispatchQueue.main.async {
            let window = NSApp.windows.first { candidate in
                candidate.title.contains("Workspace") && !candidate.isMiniaturized
            } ?? NSApp.windows.first { candidate in
                candidate.canBecomeKey && !(candidate is NSPanel)
            }
            window?.makeKeyAndOrderFront(nil)
        }
    }

    private func updateStatusItemIcon() {
        guard let button = statusItem?.button else { return }
        let state = iconState
        button.attributedTitle = attributedIcon(for: state)
        button.toolTip = tooltip(for: state)
        button.setAccessibilityLabel(tooltip(for: state))
    }

    private var iconState: IconState {
        guard let daemonClient, daemonClient.connected else {
            return .disconnected
        }
        return daemonClient.status.paused ? .paused : .running
    }

    private func attributedIcon(for state: IconState) -> NSAttributedString {
        var attributes: [NSAttributedString.Key: Any] = [
            .font: NSFont.monospacedSystemFont(ofSize: 14, weight: .semibold),
            .foregroundColor: color(for: state),
            .baselineOffset: -1
        ]
        if state == .disconnected {
            attributes[.strikethroughStyle] = NSUnderlineStyle.single.rawValue
            attributes[.strikethroughColor] = color(for: state)
        }
        return NSAttributedString(string: "i", attributes: attributes)
    }

    private func color(for state: IconState) -> NSColor {
        switch state {
        case .running:
            return NSColor(calibratedRed: 0.92, green: 0.43, blue: 0.24, alpha: 1)
        case .paused:
            return NSColor(calibratedRed: 0.77, green: 0.62, blue: 0.20, alpha: 1)
        case .disconnected:
            return NSColor.secondaryLabelColor
        }
    }

    private func tooltip(for state: IconState) -> String {
        switch state {
        case .running:
            return "Iter running"
        case .paused:
            return "Iter paused"
        case .disconnected:
            return "Iter disconnected"
        }
    }
}

struct MenuBarInstaller: View {
    @Environment(\.openWindow) private var openWindow

    let controller: MenuBarController
    let daemonClient: DaemonClient
    let router: WorkspaceRouter

    var body: some View {
        Color.clear
            .frame(width: 0, height: 0)
            .onAppear {
                controller.install(
                    daemonClient: daemonClient,
                    router: router,
                    openWorkspace: { openWindow(id: "workspace") }
                )
            }
    }
}
