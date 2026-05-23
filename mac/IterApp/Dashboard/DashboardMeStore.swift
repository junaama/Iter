import Foundation
import Observation

@MainActor
@Observable
final class DashboardMeStore {
    var dashboard: DashboardMeResponse?
    var isLoading = false
    var errorMessage: String?

    private let client: IterHTTPClient

    init(client: IterHTTPClient = IterHTTPClient()) {
        self.client = client
    }

    var sessionCountLabel: String {
        guard let dashboard else { return "--" }
        return "\(dashboard.recentSessions.count)"
    }

    func load(forceRefresh: Bool = false) async {
        guard !isLoading else { return }
        isLoading = true
        errorMessage = nil
        do {
            dashboard = try await client.dashboardMe(forceRefresh: forceRefresh)
        } catch {
            errorMessage = error.localizedDescription
        }
        isLoading = false
    }
}
