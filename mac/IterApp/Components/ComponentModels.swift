import SwiftUI

enum OutcomeStatus: String, CaseIterable, Identifiable {
    case landed
    case review
    case dropped

    var id: String { rawValue }

    func tint(for scheme: ColorScheme) -> Color {
        switch self {
        case .landed: return .iterGood(for: scheme)
        case .review: return .iterWarn(for: scheme)
        case .dropped: return .iterBad(for: scheme)
        }
    }
}

enum HarnessID: String, CaseIterable, Identifiable {
    case claudeCode = "claude_code"
    case codex
    case geminiCLI = "gemini_cli"
    case opencode
    case piHarness = "pi"

    var id: String { rawValue }

    var tint: IterHarnessTint {
        switch self {
        case .claudeCode: return .claudeCode
        case .codex: return .codex
        case .geminiCLI: return .gemini
        case .opencode: return .opencode
        case .piHarness: return .piHarness
        }
    }
}

enum Delta: Equatable {
    case increase(String)
    case decrease(String)
    case flat(String)

    var label: String {
        switch self {
        case .increase(let value), .decrease(let value), .flat(let value):
            return value
        }
    }

    func color(for scheme: ColorScheme) -> Color {
        switch self {
        case .increase: return .iterGood(for: scheme)
        case .decrease: return .iterBad(for: scheme)
        case .flat: return .iterTextTertiary(for: scheme)
        }
    }
}

enum IterScoreValue {
    static func fromCompositeScore(_ compositeScore: Double?) -> Int {
        guard let compositeScore else { return 0 }
        let clamped = min(max(compositeScore, 0), 1)
        return Int((clamped * 100).rounded())
    }
}

struct KPITileData: Identifiable {
    let id = UUID()
    let label: String
    let value: String
    let unit: String?
    let delta: Delta
    let sparkline: [Double]
}

struct SessionListItem: Identifiable {
    let id: String
    let when: String
    let relativeTime: String
    let repo: String
    let task: String
    let authorInitials: String
    let avatarSeed: String
    let harness: HarnessID
    let duration: String
    let score: Int
    let status: OutcomeStatus
    let accepted: String
    let tools: String
    let sparkline: [Double]
    var isSelected = false
}

enum TraceKind: String, CaseIterable, Identifiable {
    case start
    case prompt
    case suggest
    case tool
    case subagent
    case commit
    case extract
    case end

    var id: String { rawValue }

    func foreground(for scheme: ColorScheme) -> Color {
        switch self {
        case .start, .commit, .end:
            return .iterGood(for: scheme)
        case .prompt, .suggest, .extract:
            return .iterAccent(for: scheme)
        case .tool:
            return .iterTextSecondary(for: scheme)
        case .subagent:
            return .iterWarn(for: scheme)
        }
    }

    func background(for scheme: ColorScheme) -> Color {
        switch self {
        case .start, .commit, .end:
            return .iterGoodSoft(for: scheme)
        case .prompt, .suggest, .extract:
            return .iterAccentSoft(for: scheme)
        case .tool:
            return .iterSelected(for: scheme)
        case .subagent:
            return .iterWarnSoft(for: scheme)
        }
    }
}

struct TraceChild: Identifiable {
    let id = UUID()
    let label: String
    let detail: String
}

struct OutcomeGridData {
    let tests: String
    let commits: String
    let files: String
    let toolsUsed: String
}

struct RailItem: Identifiable {
    let id = UUID()
    let title: String
    let metadata: String
    let primaryAction: String?
    let secondaryAction: String?
}

enum ComponentPreviewData {
    static let sessions = [
        SessionListItem(
            id: "s_8f21",
            when: "09:42",
            relativeTime: "3m ago",
            repo: "iter/mac",
            task: "Build core component library",
            authorInitials: "PS",
            avatarSeed: "priya",
            harness: .codex,
            duration: "14m",
            score: 92,
            status: .landed,
            accepted: "4/5",
            tools: "9 tools",
            sparkline: [0.2, 0.7, 0.4, 0.9, 0.6],
            isSelected: true
        ),
        SessionListItem(
            id: "s_42ac",
            when: "08:18",
            relativeTime: "1h ago",
            repo: "api/gateway",
            task: "Review webhook HMAC verifier",
            authorInitials: "MC",
            avatarSeed: "mchen",
            harness: .claudeCode,
            duration: "31m",
            score: 74,
            status: .review,
            accepted: "2/4",
            tools: "12 tools",
            sparkline: [0.5, 0.4, 0.6, 0.5, 0.7]
        ),
        SessionListItem(
            id: "s_19bf",
            when: "Mon",
            relativeTime: "Yesterday",
            repo: "infra/rls",
            task: "Tighten tenant policy verifier",
            authorInitials: "AY",
            avatarSeed: "ana",
            harness: .opencode,
            duration: "8m",
            score: 49,
            status: .dropped,
            accepted: "0/3",
            tools: "5 tools",
            sparkline: [0.9, 0.3, 0.2, 0.5, 0.2]
        )
    ]
}
