import Foundation
import SwiftUI
// swiftlint:disable file_length

struct SessionDetailView: View {
    @Environment(\.colorScheme) private var colorScheme
    @State private var viewModel = SessionDetailViewModel()

    let sessionID: String

    var body: some View {
        Group {
            switch viewModel.state {
            case .idle, .loading:
                SessionDetailSkeleton()
            case .notFound:
                SessionDetailMessage(text: "Session not found or you don't have access.")
            case .failed(let message):
                SessionDetailMessage(text: message)
            case .loaded(let loaded):
                SessionDetailContent(loaded: loaded)
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .background(Color.iterPanel(for: colorScheme))
        .task(id: sessionID) {
            await viewModel.load(sessionID: sessionID)
        }
    }
}

@MainActor
@Observable
private final class SessionDetailViewModel {
    enum State {
        case idle
        case loading
        case loaded(LoadedSessionDetail)
        case notFound
        case failed(String)
    }

    var state: State = .idle

    private let client = SessionDetailClient()

    func load(sessionID: String) async {
        state = .loading
        do {
            state = .loaded(try await client.load(sessionID: sessionID))
        } catch SessionDetailError.notFound {
            state = .notFound
        } catch {
            state = .failed(error.localizedDescription)
        }
    }
}

private struct SessionDetailContent: View {
    @Environment(\.colorScheme) private var colorScheme

    let loaded: LoadedSessionDetail

    private var session: SessionDetailRow { loaded.detail.session }
    private var latestScore: SessionScoreDetail? { loaded.scores.scores.first }
    private var timelineRows: [TraceTimelineRow] {
        TraceTimelineRow.rows(from: loaded.detail)
    }
    private var outcomeGrid: OutcomeGridData {
        OutcomeGridData.from(detail: loaded.detail)
    }
    private var scoreRows: [ScoreSignalRowData] {
        ScoreSignalRowData.rows(from: latestScore)
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: IterSpacing.gapLarge) {
                SessionDetailHeader(session: session, score: latestScore)

                if loaded.loadedFromArchive {
                    ArchiveNotice()
                }

                HStack(alignment: .top, spacing: IterSpacing.gapLarge) {
                    TraceTimelineSection(rows: timelineRows)

                    VStack(alignment: .leading, spacing: IterSpacing.gapLarge) {
                        OutcomeGrid(
                            tests: outcomeGrid.tests,
                            commits: outcomeGrid.commits,
                            files: outcomeGrid.files,
                            toolsUsed: outcomeGrid.toolsUsed
                        )

                        ScoreBreakdownSection(score: latestScore, rows: scoreRows)
                    }
                    .frame(width: 324, alignment: .topLeading)
                }
            }
            .padding(IterSpacing.mainPanePadding)
        }
        .background(Color.iterPanel(for: colorScheme))
    }
}

private struct SessionDetailHeader: View {
    @Environment(\.colorScheme) private var colorScheme

    let session: SessionDetailRow
    let score: SessionScoreDetail?

    var body: some View {
        VStack(alignment: .leading, spacing: IterSpacing.gapMedium) {
            HStack(alignment: .top, spacing: IterSpacing.gapMedium) {
                VStack(alignment: .leading, spacing: IterSpacing.gapSmall) {
                    Text(verbatim: session.redactedPrompt)
                        .font(IterFont.monoLabel)
                        .foregroundStyle(Color.iterTextPrimary(for: colorScheme))
                        .lineLimit(4)
                        .fixedSize(horizontal: false, vertical: true)

                    LazyVGrid(
                        columns: [GridItem(.adaptive(minimum: 72), spacing: 6)],
                        alignment: .leading,
                        spacing: 6
                    ) {
                        ForEach(Array(session.tools.enumerated()), id: \.offset) { _, tool in
                            DetailChip(text: tool)
                        }
                    }
                }

                Spacer(minLength: IterSpacing.gapLarge)

                VStack(alignment: .trailing, spacing: IterSpacing.gapSmall) {
                    if let score {
                        Score(value: IterScoreValue.fromCompositeScore(score.compositeScore))
                        DetailChip(text: score.scorerVersion)
                    } else {
                        DetailChip(text: "unscored")
                    }
                }
            }

            HStack(spacing: IterSpacing.gapSmall) {
                harnessBadge
                DetailChip(text: session.model)
                DetailChip(text: session.effort ?? "effort n/a")
                DetailMetric(label: "wall", value: session.wallTimeLabel)
                DetailMetric(label: "turns", value: session.turnCountLabel)
            }
        }
        .padding(IterSpacing.cardPadding)
        .background(Color.iterSidebar(for: colorScheme))
        .clipShape(.rect(cornerRadius: IterRadius.standard))
        .overlay {
            RoundedRectangle(cornerRadius: IterRadius.standard)
                .stroke(Color.iterBorder(for: colorScheme), lineWidth: 1)
        }
    }

    @ViewBuilder
    private var harnessBadge: some View {
        if let harnessID = HarnessID(rawValue: session.harness) {
            Harness(id: harnessID)
                .padding(.horizontal, 8)
                .frame(height: 24)
                .background(Color.iterPanel(for: colorScheme))
                .clipShape(.rect(cornerRadius: IterRadius.pill))
                .overlay {
                    RoundedRectangle(cornerRadius: IterRadius.pill)
                        .stroke(Color.iterBorder(for: colorScheme), lineWidth: 1)
                }
        } else {
            DetailChip(text: session.harness)
        }
    }
}

private struct TraceTimelineSection: View {
    @Environment(\.colorScheme) private var colorScheme

    let rows: [TraceTimelineRow]

    var body: some View {
        VStack(alignment: .leading, spacing: IterSpacing.gapSmall) {
            SectionHeader(title: "Trace timeline")

            VStack(spacing: 0) {
                if rows.isEmpty {
                    EmptyPanelText(text: "No trace events captured.")
                } else {
                    ForEach(rows) { row in
                        TraceRow(
                            time: row.time,
                            kind: row.kind,
                            label: row.label,
                            detail: row.detail,
                            children: row.children
                        )
                    }
                }
            }
            .background(Color.iterPanel(for: colorScheme))
            .clipShape(.rect(cornerRadius: IterRadius.standard))
            .overlay {
                RoundedRectangle(cornerRadius: IterRadius.standard)
                    .stroke(Color.iterBorder(for: colorScheme), lineWidth: 1)
            }
        }
        .frame(maxWidth: .infinity, alignment: .topLeading)
    }
}

private struct ScoreBreakdownSection: View {
    @Environment(\.colorScheme) private var colorScheme

    let score: SessionScoreDetail?
    let rows: [ScoreSignalRowData]

    var body: some View {
        VStack(alignment: .leading, spacing: IterSpacing.gapSmall) {
            SectionHeader(title: "Score breakdown")

            VStack(alignment: .leading, spacing: 0) {
                if rows.isEmpty {
                    EmptyPanelText(text: "No score signals yet.")
                } else {
                    ForEach(rows) { row in
                        ScoreSignalRow(row: row)
                    }
                }

                if let rationale = score?.rationale, !rationale.isEmpty {
                    Text(verbatim: rationale)
                        .font(IterFont.monoSmall)
                        .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
                        .fixedSize(horizontal: false, vertical: true)
                        .padding(IterSpacing.gapMedium)
                        .overlay(alignment: .top) {
                            Rectangle()
                                .fill(Color.iterBorder(for: colorScheme))
                                .frame(height: 1)
                        }
                }
            }
            .background(Color.iterPanel(for: colorScheme))
            .clipShape(.rect(cornerRadius: IterRadius.standard))
            .overlay {
                RoundedRectangle(cornerRadius: IterRadius.standard)
                    .stroke(Color.iterBorder(for: colorScheme), lineWidth: 1)
            }
        }
    }
}

private struct ScoreSignalRow: View {
    @Environment(\.colorScheme) private var colorScheme

    let row: ScoreSignalRowData

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(alignment: .firstTextBaseline, spacing: IterSpacing.gapSmall) {
                Text(verbatim: row.name)
                    .font(IterFont.sansSmall)
                    .foregroundStyle(Color.iterTextPrimary(for: colorScheme))
                    .lineLimit(1)

                Spacer()

                Text(verbatim: row.weightLabel)
                    .font(IterFont.monoSmall)
                    .foregroundStyle(Color.iterTextTertiary(for: colorScheme))

                Text(verbatim: row.contributionLabel)
                    .font(IterFont.monoSmall)
                    .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
            }

            HStack(spacing: IterSpacing.gapSmall) {
                GeometryReader { geometry in
                    ZStack(alignment: .leading) {
                        Capsule()
                            .fill(Color.iterSelected(for: colorScheme))
                        Capsule()
                            .fill(Color.iterAccent(for: colorScheme))
                            .frame(width: geometry.size.width * row.barValue)
                    }
                }
                .frame(height: 6)

                Text(verbatim: row.valueLabel)
                    .font(IterFont.monoSmall)
                    .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
                    .frame(width: 42, alignment: .trailing)
            }
        }
        .padding(.horizontal, IterSpacing.gapMedium)
        .padding(.vertical, 9)
        .overlay(alignment: .bottom) {
            Rectangle()
                .fill(Color.iterBorder(for: colorScheme))
                .frame(height: 1)
        }
    }
}

private struct ArchiveNotice: View {
    @Environment(\.colorScheme) private var colorScheme

    var body: some View {
        Text(verbatim: "Loaded from archive.")
            .font(IterFont.monoLabel)
            .foregroundStyle(Color.iterWarn(for: colorScheme))
            .padding(.horizontal, IterSpacing.gapMedium)
            .frame(height: 28, alignment: .leading)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(Color.iterWarnSoft(for: colorScheme))
            .clipShape(.rect(cornerRadius: IterRadius.standard))
    }
}

private struct DetailChip: View {
    @Environment(\.colorScheme) private var colorScheme

    let text: String

    var body: some View {
        Text(verbatim: text)
            .font(IterFont.monoLabel)
            .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
            .lineLimit(1)
            .padding(.horizontal, 8)
            .frame(height: 24)
            .background(Color.iterPanel(for: colorScheme))
            .clipShape(.rect(cornerRadius: IterRadius.pill))
            .overlay {
                RoundedRectangle(cornerRadius: IterRadius.pill)
                    .stroke(Color.iterBorder(for: colorScheme), lineWidth: 1)
            }
    }
}

private struct DetailMetric: View {
    @Environment(\.colorScheme) private var colorScheme

    let label: String
    let value: String

    var body: some View {
        HStack(spacing: 5) {
            Text(verbatim: label)
                .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
            Text(verbatim: value)
                .foregroundStyle(Color.iterTextPrimary(for: colorScheme))
        }
        .font(IterFont.monoLabel)
        .padding(.horizontal, 8)
        .frame(height: 24)
        .background(Color.iterPanel(for: colorScheme))
        .clipShape(.rect(cornerRadius: IterRadius.pill))
        .overlay {
            RoundedRectangle(cornerRadius: IterRadius.pill)
                .stroke(Color.iterBorder(for: colorScheme), lineWidth: 1)
        }
    }
}

private struct SectionHeader: View {
    @Environment(\.colorScheme) private var colorScheme

    let title: String

    var body: some View {
        Text(verbatim: title)
            .font(IterFont.sansSectionTitle)
            .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
            .textCase(.uppercase)
    }
}

private struct EmptyPanelText: View {
    @Environment(\.colorScheme) private var colorScheme

    let text: String

    var body: some View {
        Text(verbatim: text)
            .font(IterFont.monoLabel)
            .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
            .frame(maxWidth: .infinity, minHeight: 48, alignment: .center)
    }
}

private struct SessionDetailMessage: View {
    @Environment(\.colorScheme) private var colorScheme

    let text: String

    var body: some View {
        Text(verbatim: text)
            .font(IterFont.sansBody)
            .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .center)
            .padding(IterSpacing.mainPanePadding)
    }
}

private struct SessionDetailSkeleton: View {
    @Environment(\.colorScheme) private var colorScheme

    var body: some View {
        VStack(alignment: .leading, spacing: IterSpacing.gapLarge) {
            VStack(alignment: .leading, spacing: IterSpacing.gapSmall) {
                SkeletonBar(width: 420)
                SkeletonBar(width: 280)
                HStack(spacing: IterSpacing.gapSmall) {
                    SkeletonBar(width: 62)
                    SkeletonBar(width: 78)
                    SkeletonBar(width: 90)
                }
            }
            .padding(IterSpacing.cardPadding)
            .background(Color.iterSidebar(for: colorScheme))
            .clipShape(.rect(cornerRadius: IterRadius.standard))

            HStack(alignment: .top, spacing: IterSpacing.gapLarge) {
                VStack(spacing: 8) {
                    ForEach(0..<9, id: \.self) { _ in
                        SkeletonBar(width: nil)
                            .frame(height: 30)
                    }
                }
                VStack(spacing: IterSpacing.gapLarge) {
                    SkeletonBar(width: nil)
                        .frame(height: 98)
                    SkeletonBar(width: nil)
                        .frame(height: 240)
                }
                .frame(width: 324)
            }
        }
        .padding(IterSpacing.mainPanePadding)
    }
}

private struct SkeletonBar: View {
    @Environment(\.colorScheme) private var colorScheme

    let width: CGFloat?

    var body: some View {
        RoundedRectangle(cornerRadius: 4)
            .fill(Color.iterSelected(for: colorScheme))
            .frame(width: width, height: 12)
    }
}

private struct TraceTimelineRow: Identifiable {
    let id: String
    let time: String
    let kind: TraceKind
    let label: String
    let detail: String
    let children: [TraceChild]

    static func rows(from detail: SessionDetailResponse) -> [TraceTimelineRow] {
        var rows = [
            TraceTimelineRow(
                id: "session-start",
                time: "00:00",
                kind: .start,
                label: "Session started",
                detail: detail.session.gitBranch ?? detail.session.id,
                children: []
            )
        ]

        rows.append(contentsOf: detail.events.items.map { event in
            eventRow(event, session: detail.session)
        })
        rows.append(contentsOf: subagentRows(detail.subagents.items, rootStart: detail.session.startedAt))

        if let endedAt = detail.session.endedAt {
            rows.append(
                TraceTimelineRow(
                    id: "session-end",
                    time: relativeTime(from: detail.session.startedAt, to: endedAt),
                    kind: .end,
                    label: "Session completed",
                    detail: detail.session.wallTimeLabel,
                    children: []
                )
            )
        }

        return rows.sorted { lhs, rhs in
            if lhs.time == rhs.time { return lhs.id < rhs.id }
            return lhs.time < rhs.time
        }
    }

    private static func eventRow(_ event: SessionEventDetail, session: SessionDetailRow) -> TraceTimelineRow {
        let kind = traceKind(for: event.eventType)
        return TraceTimelineRow(
            id: "event-\(event.id)",
            time: relativeTime(from: session.startedAt, to: event.occurredAt),
            kind: kind,
            label: eventLabel(event, kind: kind),
            detail: eventDetail(event, session: session),
            children: []
        )
    }

    private static func subagentRows(_ nodes: [SubagentSessionNode], rootStart: Date) -> [TraceTimelineRow] {
        nodes.flatMap { node -> [TraceTimelineRow] in
            let children = node.session.tools.map { TraceChild(label: $0, detail: node.session.model) }
            let row = TraceTimelineRow(
                id: "subagent-\(node.session.id)",
                time: relativeTime(from: rootStart, to: node.session.startedAt),
                kind: .subagent,
                label: "Subagent \(node.depth)",
                detail: node.session.redactedPrompt,
                children: children
            )
            return [row] + subagentRows(node.children, rootStart: rootStart)
        }
    }

    private static func traceKind(for eventType: String) -> TraceKind {
        switch eventType {
        case "prompt_sent":
            return .prompt
        case "tool_call":
            return .tool
        case "subagent_spawned":
            return .subagent
        case "git_commit", "git_revert", "pr_opened", "pr_merged", "pr_reverted":
            return .commit
        case "suggestion_accepted", "suggestion_rejected":
            return .suggest
        case "session_completed":
            return .end
        default:
            return .extract
        }
    }

    private static func eventLabel(_ event: SessionEventDetail, kind: TraceKind) -> String {
        switch kind {
        case .tool:
            return event.payload.firstString(for: ["tool", "tool_name", "name", "command"]) ?? "tool_call"
        case .subagent:
            return event.payload.firstString(for: ["agent", "name", "harness"]) ?? "Subagent spawned"
        case .commit:
            return event.payload.firstString(for: ["sha", "commit", "pull_request", "url"]) ?? event.eventType
        case .suggest:
            return event.eventType.replacingOccurrences(of: "_", with: " ")
        case .prompt:
            return "Prompt sent"
        case .start:
            return "Session started"
        case .end:
            return "Session completed"
        case .extract:
            return event.eventType.replacingOccurrences(of: "_", with: " ")
        }
    }

    private static func eventDetail(_ event: SessionEventDetail, session: SessionDetailRow) -> String {
        if event.eventType == "prompt_sent" {
            return event.payload.firstString(for: ["prompt", "redacted_prompt"]) ?? session.redactedPrompt
        }
        return event.payload.firstString(
            for: ["detail", "summary", "path", "file", "query", "command", "cmd", "result"]
        ) ?? event.payload.compactSummary
    }

    private static func relativeTime(from start: Date, to date: Date) -> String {
        let seconds = max(0, Int(date.timeIntervalSince(start)))
        if seconds < 3_600 {
            return String(format: "%02d:%02d", seconds / 60, seconds % 60)
        }
        return String(format: "%02d:%02d", seconds / 3_600, (seconds % 3_600) / 60)
    }
}

private struct ScoreSignalRowData: Identifiable {
    let id: String
    let name: String
    let valueLabel: String
    let weightLabel: String
    let contributionLabel: String
    let barValue: Double

    static func rows(from score: SessionScoreDetail?) -> [ScoreSignalRowData] {
        guard let score else { return [] }
        let orderedKeys = knownOrder.filter { score.signals[$0] != nil }
            + score.signals.keys.filter { !knownWeights.keys.contains($0) }.sorted()
        let denominator = orderedKeys.compactMap { knownWeights[$0] }.reduce(0, +)

        return orderedKeys.compactMap { key in
            guard let value = score.signals[key] else { return nil }
            let numeric = value.doubleValue
            let weight = knownWeights[key]
            let scoringValue = normalizedValue(for: key, raw: numeric)
            let contribution = weight.map { denominator > 0 ? ($0 * scoringValue / denominator) : 0 }
            return ScoreSignalRowData(
                id: key,
                name: key.replacingOccurrences(of: "_", with: " "),
                valueLabel: value.displayValue,
                weightLabel: weight.map { percent($0) } ?? "--",
                contributionLabel: contribution.map { "\(Int(($0 * 100).rounded())) pts" } ?? "--",
                barValue: min(max(scoringValue, 0), 1)
            )
        }
    }

    private static let knownOrder = [
        "durability_7d",
        "durability_30d",
        "peer_reuse_count",
        "self_reuse_count",
        "override_rate",
        "suggestion_acceptance"
    ]

    private static let knownWeights = [
        "durability_7d": 0.25,
        "durability_30d": 0.15,
        "peer_reuse_count": 0.20,
        "self_reuse_count": 0.10,
        "override_rate": 0.10,
        "suggestion_acceptance": 0.20
    ]

    private static func normalizedValue(for key: String, raw: Double?) -> Double {
        let value = raw ?? 0
        switch key {
        case "peer_reuse_count", "self_reuse_count":
            return value <= 0 ? 0 : 1 - exp(-value / 3)
        case "override_rate":
            return 1 - min(max(value, 0), 1)
        default:
            return min(max(value, 0), 1)
        }
    }

    private static func percent(_ value: Double) -> String {
        "\(Int((value * 100).rounded()))%"
    }
}

private extension OutcomeGridData {
    static func from(detail: SessionDetailResponse) -> OutcomeGridData {
        let testsPassed = detail.outcomes.filter { $0.outcomeType == "tests_passed" }.count
        let testsFailed = detail.outcomes.filter { $0.outcomeType == "tests_failed" }.count
        let commits = detail.outcomes.filter { $0.outcomeType == "commit_landed" }.count
        let files = detail.fileReferences.count
        let tools = detail.toolNames.count

        return OutcomeGridData(
            tests: testsFailed > 0 ? "\(testsFailed) failed" : "\(testsPassed)/\(testsPassed)",
            commits: "\(commits)",
            files: "\(files)",
            toolsUsed: "\(tools)"
        )
    }
}

private extension SessionDetailRow {
    var wallTimeLabel: String {
        guard let wallTimeMs else { return "--" }
        let seconds = max(0, wallTimeMs / 1_000)
        if seconds < 60 { return "\(seconds)s" }
        if seconds < 3_600 { return "\(seconds / 60)m" }
        return "\(seconds / 3_600)h \((seconds % 3_600) / 60)m"
    }

    var turnCountLabel: String {
        turnCount.map(String.init) ?? "--"
    }
}

private extension SessionDetailResponse {
    var fileReferences: Set<String> {
        var values = Set<String>()
        for event in events.items {
            event.payload.collectStrings(into: &values, matching: ["file", "path", "files", "changed_files"])
        }
        for outcome in outcomes {
            outcome.details.collectStrings(into: &values, matching: ["file", "path", "files", "changed_files"])
        }
        return values
    }

    var toolNames: Set<String> {
        var values = Set(session.tools)
        for event in events.items where event.eventType == "tool_call" {
            if let tool = event.payload.firstString(for: ["tool", "tool_name", "name", "command"]) {
                values.insert(tool)
            }
        }
        return values
    }
}

private extension Dictionary where Key == String, Value == JSONValue {
    func firstString(for keys: [String]) -> String? {
        for key in keys {
            if let string = self[key]?.stringValue, !string.isEmpty {
                return string
            }
            if let number = self[key]?.doubleValue {
                return JSONValue.number(number).displayValue
            }
        }
        return nil
    }

    var compactSummary: String {
        if isEmpty { return "--" }
        return keys.sorted().prefix(3).joined(separator: " / ")
    }

    func collectStrings(into output: inout Set<String>, matching keys: [String]) {
        for (key, value) in self where keys.contains(key) {
            if let string = value.stringValue {
                output.insert(string)
            } else if let strings = value.stringArrayValue {
                output.formUnion(strings)
            }
        }
    }
}

#Preview("Session Detail Loading") {
    SessionDetailView(sessionID: "11111111-1111-4111-8111-111111111111")
        .environment(ThemeStore())
}
