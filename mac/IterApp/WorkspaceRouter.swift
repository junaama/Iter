import Observation

@Observable
final class WorkspaceRouter {
    var route: Route = .me
    var showsStackShareSheet = false

    func openDashboard() {
        route = .me
        showsStackShareSheet = false
    }

    func openStackShare() {
        route = .stack
        showsStackShareSheet = true
    }

    func openSettings() {
        route = .settings
        showsStackShareSheet = false
    }
}
