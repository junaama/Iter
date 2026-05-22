import Observation
import SwiftUI

@Observable
final class ThemeStore {
    private enum Storage {
        static let key = "dev.iter.theme"
    }

    enum Theme: String {
        case light
        case dark

        var colorScheme: ColorScheme {
            switch self {
            case .light:
                return .light
            case .dark:
                return .dark
            }
        }
    }

    var theme: Theme {
        didSet {
            UserDefaults.standard.set(theme.rawValue, forKey: Storage.key)
        }
    }

    var preferredColorScheme: ColorScheme {
        theme.colorScheme
    }

    var toggleTitle: String {
        switch theme {
        case .light:
            return "Use Dark Theme"
        case .dark:
            return "Use Light Theme"
        }
    }

    init(defaults: UserDefaults = .standard) {
        let stored = defaults.string(forKey: Storage.key).flatMap(Theme.init(rawValue:))
        self.theme = stored ?? .light
    }

    func toggleTheme() {
        theme = theme == .light ? .dark : .light
    }
}
