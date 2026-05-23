import SwiftUI

@main
struct IterApp: App {
    @State private var themeStore = ThemeStore()
    @State private var daemonClient = DaemonClient()
    @State private var router = WorkspaceRouter()
    @State private var menuBarController = MenuBarController()
    @State private var sessionStore = SessionStore()

    var body: some Scene {
        WindowGroup("iter — Workspace", id: "workspace") {
            RootSessionView()
                .environment(themeStore)
                .environment(daemonClient)
                .environment(router)
                .environment(sessionStore)
                .preferredColorScheme(themeStore.preferredColorScheme)
                .background {
                    MenuBarInstaller(
                        controller: menuBarController,
                        daemonClient: daemonClient,
                        router: router
                    )
                }
                .task {
                    sessionStore.load()
                    daemonClient.start()
                }
        }
        .defaultSize(width: IterSpacing.windowMaxWidth, height: IterSpacing.windowMaxHeight)
        .windowStyle(.hiddenTitleBar)
        .windowResizability(.contentMinSize)
        .commands {
            #if DEBUG
            CommandMenu("Debug") {
                Button(themeStore.toggleTitle) {
                    themeStore.toggleTheme()
                }
                .keyboardShortcut("t", modifiers: [.command, .shift])
            }
            #endif
        }
    }
}
