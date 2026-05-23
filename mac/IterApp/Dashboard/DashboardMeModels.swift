import Foundation

struct DashboardUser: Decodable, Sendable {
    let id: String
    let displayName: String
    let email: String

    enum CodingKeys: String, CodingKey {
        case id
        case displayName = "display_name"
        case email
    }
}

struct DashboardTrendPoint: Decodable, Sendable {
    let date: String
    let compositeScore: Double?
    let sessionCount: Int

    enum CodingKeys: String, CodingKey {
        case date
        case compositeScore = "composite_score"
        case sessionCount = "session_count"
    }
}

struct DashboardRecentSession: Decodable, Identifiable, Sendable {
    let id: String
    let startedAt: Date
    let compositeScore: Double?
    let harness: String
    let redactedPromptPreview: String

    enum CodingKeys: String, CodingKey {
        case id
        case startedAt = "started_at"
        case compositeScore = "composite_score"
        case harness
        case redactedPromptPreview = "redacted_prompt_preview"
    }
}

struct DashboardMeResponse: Decodable, Sendable {
    let user: DashboardUser
    let trend: [DashboardTrendPoint]
    let recentSessions: [DashboardRecentSession]

    enum CodingKeys: String, CodingKey {
        case user
        case trend
        case recentSessions = "recent_sessions"
    }
}

enum DashboardMeDisplay {
    static func kpiTiles(from dashboard: DashboardMeResponse) -> [KPITileData] {
        let trend = dashboard.trend
        let recent = dashboard.recentSessions
        let currentWeek = Array(trend.suffix(7))
        let previousWeek = Array(trend.dropLast(7).suffix(7))
        let sessionCount = trend.reduce(0) { $0 + $1.sessionCount }
        let currentWeekSessions = currentWeek.reduce(0) { $0 + $1.sessionCount }
        let previousWeekSessions = previousWeek.reduce(0) { $0 + $1.sessionCount }
        let averageScore = meanScore(trend)
        let currentWeekScore = meanScore(currentWeek)
        let previousWeekScore = meanScore(previousWeek)
        let acceptance = acceptanceRate(recent)

        return [
            KPITileData(
                label: "sessions",
                value: "\(sessionCount)",
                unit: nil,
                delta: percentDelta(current: Double(currentWeekSessions), previous: Double(previousWeekSessions)),
                sparkline: trend.map { Double($0.sessionCount) }
            ),
            KPITileData(
                label: "acceptance %",
                value: acceptance.map { "\($0)" } ?? "--",
                unit: "%",
                delta: .flat(""),
                sparkline: recent.compactMap(\.compositeScore)
            ),
            KPITileData(
                label: "avg score",
                value: averageScore.map { "\($0)" } ?? "--",
                unit: nil,
                delta: scoreDelta(current: currentWeekScore, previous: previousWeekScore),
                sparkline: trend.map { $0.compositeScore ?? 0 }
            )
        ]
    }

    static func sessionItems(from dashboard: DashboardMeResponse) -> [SessionListItem] {
        dashboard.recentSessions.map { session in
            let score = IterScoreValue.fromCompositeScore(session.compositeScore)
            let status = status(for: score, hasScore: session.compositeScore != nil)
            let task = taskTitle(from: session.redactedPromptPreview)
            return SessionListItem(
                id: session.id,
                when: startedLabel(for: session.startedAt),
                relativeTime: relativeTime(from: session.startedAt),
                repo: repoLabel(from: session.redactedPromptPreview),
                task: task,
                authorInitials: initials(from: dashboard.user.displayName),
                avatarSeed: dashboard.user.id,
                harness: harnessID(from: session.harness),
                duration: "--",
                score: score,
                status: status,
                accepted: acceptedLabel(for: score, hasScore: session.compositeScore != nil),
                tools: "-- tools",
                sparkline: session.compositeScore.map { [$0] } ?? []
            )
        }
    }

    static func isEmptyDashboard(_ dashboard: DashboardMeResponse) -> Bool {
        dashboard.recentSessions.isEmpty && dashboard.trend.allSatisfy { $0.compositeScore == nil }
    }

    private static func meanScore(_ points: [DashboardTrendPoint]) -> Int? {
        let scores = points.compactMap(\.compositeScore)
        guard !scores.isEmpty else { return nil }
        let average = scores.reduce(0, +) / Double(scores.count)
        return IterScoreValue.fromCompositeScore(average)
    }

    private static func acceptanceRate(_ sessions: [DashboardRecentSession]) -> Int? {
        let scored = sessions.compactMap(\.compositeScore)
        guard !scored.isEmpty else { return nil }
        let accepted = scored.filter { $0 >= 0.7 }.count
        return Int((Double(accepted) / Double(scored.count) * 100).rounded())
    }

    private static func percentDelta(current: Double, previous: Double) -> Delta {
        guard previous > 0 else {
            return current > 0 ? .increase("+new") : .flat("0%")
        }
        let change = ((current - previous) / previous * 100).rounded()
        if change > 0 { return .increase("+\(Int(change))%") }
        if change < 0 { return .decrease("\(Int(change))%") }
        return .flat("0%")
    }

    private static func scoreDelta(current: Int?, previous: Int?) -> Delta {
        guard let current, let previous else { return .flat("") }
        let change = current - previous
        if change > 0 { return .increase("+\(change)") }
        if change < 0 { return .decrease("\(change)") }
        return .flat("0")
    }

    private static func harnessID(from rawValue: String) -> HarnessID {
        HarnessID(rawValue: rawValue) ?? .codex
    }

    private static func status(for score: Int, hasScore: Bool) -> OutcomeStatus {
        guard hasScore else { return .review }
        switch score {
        case 80...100:
            return .landed
        case 60..<80:
            return .review
        default:
            return .dropped
        }
    }

    private static func acceptedLabel(for score: Int, hasScore: Bool) -> String {
        guard hasScore else { return "--" }
        return score >= 70 ? "1/1" : "0/1"
    }

    private static func repoLabel(from prompt: String) -> String {
        guard let token = prompt.split(whereSeparator: \.isWhitespace).first(where: { $0.hasPrefix("@") }) else {
            return "workspace"
        }
        let cleaned = token.dropFirst()
            .split(separator: "/")
            .prefix(2)
            .map(String.init)
            .joined(separator: "/")
        return cleaned.isEmpty ? "workspace" : cleaned
    }

    private static func taskTitle(from prompt: String) -> String {
        let trimmed = prompt.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return "Captured coding session" }
        return trimmed
    }

    private static func initials(from displayName: String) -> String {
        let parts = displayName
            .split(whereSeparator: \.isWhitespace)
            .prefix(2)
            .compactMap(\.first)
        let value = parts.map(String.init).joined().uppercased()
        return value.isEmpty ? "IT" : value
    }

    private static func startedLabel(for date: Date) -> String {
        if Calendar.current.isDateInToday(date) {
            return timeFormatter.string(from: date)
        }
        if Calendar.current.isDateInYesterday(date) {
            return "Yesterday"
        }
        return weekdayFormatter.string(from: date)
    }

    private static func relativeTime(from date: Date) -> String {
        let seconds = max(0, Int(Date().timeIntervalSince(date)))
        if seconds < 60 { return "\(seconds)s ago" }
        let minutes = seconds / 60
        if minutes < 60 { return "\(minutes)m ago" }
        let hours = minutes / 60
        if hours < 24 { return "\(hours)h ago" }
        return "\(hours / 24)d ago"
    }

    private static let timeFormatter: DateFormatter = {
        let formatter = DateFormatter()
        formatter.dateFormat = "HH:mm"
        return formatter
    }()

    private static let weekdayFormatter: DateFormatter = {
        let formatter = DateFormatter()
        formatter.dateFormat = "EEE"
        return formatter
    }()
}
