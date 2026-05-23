import Foundation

struct TeamMemberAggregate: Decodable, Identifiable, Sendable {
    let userID: UUID
    let displayName: String
    let sessionCount30d: Int
    let meanCompositeScore30d: Double?

    var id: UUID { userID }

    enum CodingKeys: String, CodingKey {
        case userID = "user_id"
        case displayName = "display_name"
        case sessionCount30d = "session_count_30d"
        case meanCompositeScore30d = "mean_composite_score_30d"
    }
}

struct TeamPatternAggregate: Decodable, Identifiable, Sendable {
    let patternID: UUID
    let preview: String
    let usesCount: Int
    let tenantsUsed: Int
    let avgScore: Double

    var id: UUID { patternID }

    enum CodingKeys: String, CodingKey {
        case patternID = "pattern_id"
        case preview
        case usesCount = "uses_count"
        case tenantsUsed = "tenants_used"
        case avgScore = "avg_score"
    }
}

struct TeamInvite: Decodable, Sendable {
    let enabled: Bool
    let inviteLinkTemplate: String

    enum CodingKeys: String, CodingKey {
        case enabled
        case inviteLinkTemplate = "invite_link_template"
    }
}

struct DashboardTeamResponse: Decodable, Sendable {
    let members: [TeamMemberAggregate]
    let topPatterns: [TeamPatternAggregate]
    let invite: TeamInvite?

    enum CodingKeys: String, CodingKey {
        case members
        case topPatterns = "top_patterns"
        case invite
    }
}

struct SessionScoreSummary: Decodable, Sendable {
    let sessionID: UUID
    let compositeScore: Double
    let contributorWeight: Double

    enum CodingKeys: String, CodingKey {
        case sessionID = "session_id"
        case compositeScore = "composite_score"
        case contributorWeight = "contributor_weight"
    }
}

struct SessionSummary: Decodable, Identifiable, Sendable {
    let id: UUID
    let userID: UUID
    let harness: String
    let tools: [String]
    let startedAt: Date
    let endedAt: Date?
    let wallTimeMs: Int?
    let redactedPrompt: String
    let latestScore: SessionScoreSummary?

    enum CodingKeys: String, CodingKey {
        case id
        case userID = "user_id"
        case harness
        case tools
        case startedAt = "started_at"
        case endedAt = "ended_at"
        case wallTimeMs = "wall_time_ms"
        case redactedPrompt = "redacted_prompt"
        case latestScore = "latest_score"
    }
}

struct ListSessionsResponse: Decodable, Sendable {
    let sessions: [SessionSummary]
    let nextCursor: String?

    enum CodingKeys: String, CodingKey {
        case sessions
        case nextCursor = "next_cursor"
    }
}

enum DashboardTeamDisplay {
    static func kpiTiles(from dashboard: DashboardTeamResponse) -> [KPITileData] {
        let sessions = dashboard.members.reduce(0) { $0 + $1.sessionCount30d }
        let scores = dashboard.members.compactMap(\.meanCompositeScore30d)
        let averageScore = meanScore(scores)

        return [
            KPITileData(
                label: "team sessions",
                value: "\(sessions)",
                unit: nil,
                delta: .flat("30d"),
                sparkline: dashboard.members.map { Double($0.sessionCount30d) }
            ),
            KPITileData(
                label: "team acceptance %",
                value: "--",
                unit: "%",
                delta: .flat("pending"),
                sparkline: []
            ),
            KPITileData(
                label: "team avg score",
                value: averageScore.map { "\($0)" } ?? "--",
                unit: nil,
                delta: .flat("30d"),
                sparkline: scores
            ),
            KPITileData(
                label: "team time saved",
                value: "--",
                unit: "h",
                delta: .flat("pending"),
                sparkline: []
            )
        ]
    }

    static func sessionItems(from sessions: [SessionSummary], members: [TeamMemberAggregate]) -> [SessionListItem] {
        let membersByID = Dictionary(uniqueKeysWithValues: members.map { ($0.userID, $0) })
        return sessions.map { session in
            let member = membersByID[session.userID]
            let displayName = member?.displayName ?? "Teammate"
            let score = IterScoreValue.fromCompositeScore(session.latestScore?.compositeScore)
            let hasScore = session.latestScore != nil
            return SessionListItem(
                id: session.id.uuidString,
                when: startedLabel(for: session.startedAt),
                relativeTime: relativeTime(from: session.startedAt),
                repo: repoLabel(from: session.redactedPrompt),
                task: taskTitle(from: session.redactedPrompt),
                authorInitials: initials(from: displayName),
                avatarSeed: session.userID.uuidString,
                harness: harnessID(from: session.harness),
                duration: durationLabel(
                    startedAt: session.startedAt,
                    endedAt: session.endedAt,
                    wallTimeMs: session.wallTimeMs
                ),
                score: score,
                status: status(for: score, hasScore: hasScore),
                accepted: acceptedLabel(for: score, hasScore: hasScore),
                tools: "\(session.tools.count) tools",
                sparkline: session.latestScore.map { [$0.compositeScore] } ?? []
            )
        }
    }

    static func patternItems(from dashboard: DashboardTeamResponse) -> [RailItem] {
        dashboard.topPatterns.map { pattern in
            RailItem(
                title: trimmed(pattern.preview, fallback: "Prompt pattern"),
                metadata: "\(Int((pattern.avgScore * 100).rounded())) avg score · \(pattern.usesCount) uses",
                primaryAction: nil,
                secondaryAction: nil
            )
        }
    }

    static func isSolo(_ dashboard: DashboardTeamResponse) -> Bool {
        dashboard.members.count <= 1
    }

    private static func meanScore(_ scores: [Double]) -> Int? {
        guard !scores.isEmpty else { return nil }
        return IterScoreValue.fromCompositeScore(scores.reduce(0, +) / Double(scores.count))
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
        trimmed(prompt, fallback: "Captured coding session")
    }

    private static func initials(from displayName: String) -> String {
        let parts = displayName
            .split(whereSeparator: \.isWhitespace)
            .prefix(2)
            .compactMap(\.first)
        let value = parts.map(String.init).joined().uppercased()
        return value.isEmpty ? "IT" : value
    }

    private static func durationLabel(startedAt: Date, endedAt: Date?, wallTimeMs: Int?) -> String {
        let seconds: Int
        if let wallTimeMs {
            seconds = max(0, wallTimeMs / 1_000)
        } else if let endedAt {
            seconds = max(0, Int(endedAt.timeIntervalSince(startedAt)))
        } else {
            return "--"
        }
        let minutes = max(1, seconds / 60)
        if minutes < 60 { return "\(minutes)m" }
        let hours = Double(minutes) / 60.0
        return String(format: "%.1fh", hours)
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

    private static func trimmed(_ value: String, fallback: String) -> String {
        let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
        return trimmed.isEmpty ? fallback : trimmed
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
