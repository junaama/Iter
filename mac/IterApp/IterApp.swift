import SwiftUI

@main
struct IterApp: App {
    @State private var themeStore = ThemeStore()

    var body: some Scene {
        WindowGroup("iter — Workspace") {
            WorkspaceView()
                .environment(themeStore)
                .preferredColorScheme(themeStore.preferredColorScheme)
        }
        .defaultSize(width: IterSpacing.windowMaxWidth, height: IterSpacing.windowMaxHeight)
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
