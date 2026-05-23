import Foundation
import Observation

@MainActor
@Observable
final class DashboardTeamStore {
    var dashboard: DashboardTeamResponse?
    var recentSessions: [SessionSummary] = []
    var isLoading = false
    var isInviting = false
    var errorMessage: String?
    var inviteErrorMessage: String?
    var inviteSuccessMessage: String?

    private let client: IterHTTPClient

    init(client: IterHTTPClient = IterHTTPClient()) {
        self.client = client
    }

    var memberCountLabel: String {
        guard let dashboard else { return "--" }
        return "\(dashboard.members.count)"
    }

    var sessionCountLabel: String {
        guard !recentSessions.isEmpty else {
            return dashboard.map { "\($0.members.reduce(0) { $0 + $1.sessionCount30d })" } ?? "--"
        }
        return "\(recentSessions.count)"
    }

    func load(forceRefresh: Bool = false) async {
        guard !isLoading else { return }
        isLoading = true
        errorMessage = nil
        do {
            async let dashboardResponse = client.dashboardTeam(forceRefresh: forceRefresh)
            async let sessionsResponse = client.listSessions(limit: 10)
            dashboard = try await dashboardResponse
            recentSessions = try await sessionsResponse.sessions
        } catch {
            errorMessage = error.localizedDescription
        }
        isLoading = false
    }

    func invite(email: String) async -> Bool {
        guard !isInviting else { return false }
        isInviting = true
        inviteErrorMessage = nil
        inviteSuccessMessage = nil
        defer { isInviting = false }

        do {
            try await client.inviteTeamMember(email: email)
            inviteSuccessMessage = "Invite sent."
            return true
        } catch {
            inviteErrorMessage = "Invite endpoint is not available yet: \(error.localizedDescription)"
            return false
        }
    }
}
