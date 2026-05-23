import AppKit
import SwiftUI
// swiftlint:disable file_length

struct DashboardTeamView: View {
    @Environment(\.colorScheme) private var colorScheme
    @Environment(SessionStore.self) private var sessionStore
    @Bindable var store: DashboardTeamStore

    let layoutVariant: LayoutVariant
    let onSelectSession: (String) -> Void
    let onViewAll: () -> Void

    @State private var showsInviteSheet = false

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: IterSpacing.gapLarge) {
                KPIRow(tiles: kpiTiles)

                if let errorMessage = store.errorMessage {
                    TeamInlineRetryBanner(message: errorMessage) {
                        Task { await store.load(forceRefresh: true) }
                    }
                }

                TeamMembersSection(
                    members: members,
                    isSolo: store.dashboard.map(DashboardTeamDisplay.isSolo) ?? false,
                    canInvite: canInvite,
                    isLoading: store.dashboard == nil && store.isLoading
                ) {
                    showsInviteSheet = true
                }

                TeamSessionsSection(
                    count: store.sessionCountLabel,
                    sessions: sessionItems,
                    layoutVariant: layoutVariant,
                    isLoading: store.dashboard == nil && store.isLoading,
                    onViewAll: onViewAll,
                    onSelectSession: onSelectSession
                )
            }
            .padding(IterSpacing.mainPanePadding)
        }
        .background(Color.iterPanel(for: colorScheme))
        .task {
            await store.load()
        }
        .onReceive(NotificationCenter.default.publisher(for: NSApplication.didBecomeActiveNotification)) { _ in
            Task { await store.load() }
        }
        .sheet(isPresented: $showsInviteSheet) {
            TeamInviteSheet(store: store, isPresented: $showsInviteSheet)
        }
    }

    private var kpiTiles: [KPITileData] {
        if let dashboard = store.dashboard {
            return DashboardTeamDisplay.kpiTiles(from: dashboard)
        }
        return [
            KPITileData(label: "team sessions", value: "--", unit: nil, delta: .flat(""), sparkline: []),
            KPITileData(label: "team acceptance %", value: "--", unit: "%", delta: .flat(""), sparkline: []),
            KPITileData(label: "team avg score", value: "--", unit: nil, delta: .flat(""), sparkline: []),
            KPITileData(label: "team time saved", value: "--", unit: "h", delta: .flat(""), sparkline: [])
        ]
    }

    private var members: [TeamMemberAggregate] {
        store.dashboard?.members ?? []
    }

    private var sessionItems: [SessionListItem] {
        guard let dashboard = store.dashboard else { return [] }
        return DashboardTeamDisplay.sessionItems(from: store.recentSessions, members: dashboard.members)
    }

    private var canInvite: Bool {
        sessionStore.role == "admin" || sessionStore.role == "owner"
    }
}

private struct TeamMembersSection: View {
    @Environment(\.colorScheme) private var colorScheme

    let members: [TeamMemberAggregate]
    let isSolo: Bool
    let canInvite: Bool
    let isLoading: Bool
    let onInvite: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: IterSpacing.gapSmall) {
            HStack(spacing: IterSpacing.gapSmall) {
                Text(verbatim: "Members")
                    .font(IterFont.sansSectionTitle)
                    .foregroundStyle(Color.iterTextPrimary(for: colorScheme))

                Text(verbatim: "\(members.count)")
                    .font(IterFont.monoTiny)
                    .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
                    .padding(.horizontal, 5)
                    .frame(height: 18)
                    .background(Color.iterSelected(for: colorScheme))
                    .clipShape(.rect(cornerRadius: IterRadius.scoreChip))

                Spacer()

                if canInvite {
                    IterButton(title: isSolo ? "Invite teammate" : "Invite", action: onInvite)
                }
            }

            TeamMembersTable(
                members: members,
                isSolo: isSolo,
                canInvite: canInvite,
                isLoading: isLoading,
                onInvite: onInvite
            )
        }
    }
}

private struct TeamMembersTable: View {
    @Environment(\.colorScheme) private var colorScheme

    let members: [TeamMemberAggregate]
    let isSolo: Bool
    let canInvite: Bool
    let isLoading: Bool
    let onInvite: () -> Void

    var body: some View {
        VStack(spacing: 0) {
            header

            if isLoading {
                ForEach(0..<5, id: \.self) { _ in
                    TeamMemberSkeletonRow()
                }
            } else if members.isEmpty {
                emptyRow
            } else {
                ForEach(members) { member in
                    TeamMemberRow(member: member)
                }

                if isSolo && canInvite {
                    soloInviteRow
                }
            }
        }
        .background(Color.iterPanel(for: colorScheme))
        .clipShape(.rect(cornerRadius: IterRadius.table))
        .overlay {
            RoundedRectangle(cornerRadius: IterRadius.table)
                .stroke(Color.iterBorder(for: colorScheme), lineWidth: 1)
        }
    }

    private var header: some View {
        Grid(horizontalSpacing: 10, verticalSpacing: 0) {
            GridRow {
                ForEach(["Teammate", "Sessions", "Acceptance", "Avg score", "Weight"], id: \.self) { label in
                    Text(verbatim: label)
                        .font(IterFont.monoSmall)
                        .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
                        .textCase(.uppercase)
                        .frame(maxWidth: .infinity, alignment: .leading)
                }
            }
        }
        .padding(.horizontal, 12)
        .frame(height: IterSpacing.rowHeight)
        .background(Color.iterSidebar(for: colorScheme))
        .overlay(alignment: .bottom) {
            Rectangle()
                .fill(Color.iterBorder(for: colorScheme))
                .frame(height: 1)
        }
    }

    private var emptyRow: some View {
        Text(verbatim: "No team members yet")
            .font(IterFont.monoLabel)
            .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
            .frame(maxWidth: .infinity, minHeight: IterSpacing.rowHeight)
    }

    private var soloInviteRow: some View {
        Button(action: onInvite) {
            HStack(spacing: IterSpacing.gapSmall) {
                Image(systemName: "person.badge.plus")
                    .font(.system(size: 11, weight: .medium))
                    .accessibilityHidden(true)
                Text(verbatim: "Invite teammate")
                    .font(IterFont.sansLabel)
                Spacer()
            }
            .foregroundStyle(Color.iterAccent(for: colorScheme))
            .padding(.horizontal, 12)
            .frame(height: IterSpacing.rowHeight)
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .overlay(alignment: .top) {
            Rectangle()
                .fill(Color.iterBorder(for: colorScheme))
                .frame(height: 1)
        }
    }
}

private struct TeamMemberRow: View {
    @Environment(\.colorScheme) private var colorScheme

    let member: TeamMemberAggregate

    var body: some View {
        Grid(horizontalSpacing: 10, verticalSpacing: 0) {
            GridRow {
                HStack(spacing: IterSpacing.gapSmall) {
                    Avatar(initials: initials, seed: member.userID.uuidString)
                    Text(verbatim: member.displayName)
                        .font(IterFont.sansSmall)
                        .foregroundStyle(Color.iterTextPrimary(for: colorScheme))
                        .lineLimit(1)
                }

                Text(verbatim: "\(member.sessionCount30d)")
                    .font(IterFont.monoLabel)
                    .foregroundStyle(Color.iterTextSecondary(for: colorScheme))

                Text(verbatim: "--")
                    .font(IterFont.monoLabel)
                    .foregroundStyle(Color.iterTextTertiary(for: colorScheme))

                Score(value: IterScoreValue.fromCompositeScore(member.meanCompositeScore30d))

                ContributorWeightBar(value: member.meanCompositeScore30d ?? 0)
            }
        }
        .padding(.horizontal, 12)
        .frame(height: IterSpacing.rowHeight)
        .overlay(alignment: .bottom) {
            Rectangle()
                .fill(Color.iterBorder(for: colorScheme))
                .frame(height: 1)
        }
    }

    private var initials: String {
        let parts = member.displayName
            .split(whereSeparator: \.isWhitespace)
            .prefix(2)
            .compactMap(\.first)
        let value = parts.map(String.init).joined().uppercased()
        return value.isEmpty ? "IT" : value
    }
}

private struct ContributorWeightBar: View {
    @Environment(\.colorScheme) private var colorScheme

    let value: Double

    var body: some View {
        GeometryReader { proxy in
            ZStack(alignment: .leading) {
                RoundedRectangle(cornerRadius: 3)
                    .fill(Color.iterSelected(for: colorScheme))
                RoundedRectangle(cornerRadius: 3)
                    .fill(Color.iterAccent(for: colorScheme))
                    .frame(width: proxy.size.width * min(max(value, 0), 1))
            }
        }
        .frame(height: 6)
        .accessibilityLabel("Contributor weight")
    }
}

private struct TeamMemberSkeletonRow: View {
    @Environment(\.colorScheme) private var colorScheme

    var body: some View {
        Grid(horizontalSpacing: 10, verticalSpacing: 0) {
            GridRow {
                ForEach([96, 42, 48, 36, 54], id: \.self) { width in
                    RoundedRectangle(cornerRadius: 3)
                        .fill(Color.iterSelected(for: colorScheme))
                        .frame(width: CGFloat(width), height: 10)
                        .frame(maxWidth: .infinity, alignment: .leading)
                }
            }
        }
        .padding(.horizontal, 12)
        .frame(height: IterSpacing.rowHeight)
        .overlay(alignment: .bottom) {
            Rectangle()
                .fill(Color.iterBorder(for: colorScheme))
                .frame(height: 1)
        }
        .accessibilityHidden(true)
    }
}

private struct TeamSessionsSection: View {
    @Environment(\.colorScheme) private var colorScheme

    let count: String
    let sessions: [SessionListItem]
    let layoutVariant: LayoutVariant
    let isLoading: Bool
    let onViewAll: () -> Void
    let onSelectSession: (String) -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: IterSpacing.gapSmall) {
            HStack(spacing: IterSpacing.gapSmall) {
                Text(verbatim: "Recent team sessions")
                    .font(IterFont.sansSectionTitle)
                    .foregroundStyle(Color.iterTextPrimary(for: colorScheme))

                Text(verbatim: count)
                    .font(IterFont.monoTiny)
                    .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
                    .padding(.horizontal, 5)
                    .frame(height: 18)
                    .background(Color.iterSelected(for: colorScheme))
                    .clipShape(.rect(cornerRadius: IterRadius.scoreChip))

                Spacer()

                Button(action: onViewAll) {
                    Text(verbatim: "View all")
                        .font(IterFont.sansLabel)
                        .foregroundStyle(Color.iterAccent(for: colorScheme))
                }
                .buttonStyle(.plain)
            }

            Group {
                switch layoutVariant {
                case .table:
                    SessionTable(
                        sessions: sessions,
                        emptyMessage: "No team sessions yet.",
                        isLoading: isLoading,
                        showsAuthorColumn: true
                    ) { session in
                        onSelectSession(session.id)
                    }
                case .cards:
                    SessionCards(sessions: sessions) { session in
                        onSelectSession(session.id)
                    }
                case .feed:
                    SessionFeed(groups: feedGroups) { session in
                        onSelectSession(session.id)
                    }
                }
            }
            .id(layoutVariant)
            .transition(.opacity)
        }
        .animation(.easeInOut(duration: 0.08), value: layoutVariant)
    }

    private var feedGroups: [(day: String, sessions: [SessionListItem])] {
        guard !sessions.isEmpty else {
            return [(day: "Today", sessions: [])]
        }

        return sessions.reduce(into: [(day: String, sessions: [SessionListItem])]()) { groups, session in
            let day = feedDay(for: session)
            if let index = groups.firstIndex(where: { $0.day == day }) {
                groups[index].sessions.append(session)
            } else {
                groups.append((day: day, sessions: [session]))
            }
        }
    }

    private func feedDay(for session: SessionListItem) -> String {
        switch session.when {
        case "Yesterday":
            return "Yesterday"
        case let value where value.count == 3:
            return value
        default:
            return "Today"
        }
    }
}

private struct TeamInviteSheet: View {
    @Environment(\.colorScheme) private var colorScheme
    @Bindable var store: DashboardTeamStore
    @Binding var isPresented: Bool

    @State private var email = ""

    var body: some View {
        VStack(alignment: .leading, spacing: IterSpacing.gapMedium) {
            HStack {
                Text(verbatim: "Invite teammate")
                    .font(IterFont.sansKPIValue)
                    .foregroundStyle(Color.iterTextPrimary(for: colorScheme))
                Spacer()
                Button {
                    isPresented = false
                } label: {
                    Image(systemName: "xmark")
                        .font(.system(size: 11, weight: .semibold))
                        .frame(width: 28, height: 28)
                        .contentShape(.rect)
                }
                .buttonStyle(.plain)
                .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
            }

            TextField("name@company.com", text: $email)
                .textFieldStyle(.plain)
                .font(IterFont.monoLabel)
                .padding(.horizontal, IterSpacing.gapSmall)
                .frame(height: 30)
                .background(Color.iterSidebar(for: colorScheme))
                .clipShape(.rect(cornerRadius: IterRadius.button))
                .overlay {
                    RoundedRectangle(cornerRadius: IterRadius.button)
                        .stroke(Color.iterBorder(for: colorScheme), lineWidth: 1)
                }

            if let message = store.inviteErrorMessage {
                Text(verbatim: message)
                    .font(IterFont.sansSmall)
                    .foregroundStyle(Color.iterBad(for: colorScheme))
                    .fixedSize(horizontal: false, vertical: true)
            } else if let message = store.inviteSuccessMessage {
                Text(verbatim: message)
                    .font(IterFont.sansSmall)
                    .foregroundStyle(Color.iterGood(for: colorScheme))
            }

            HStack {
                Spacer()
                IterButton(title: "Cancel") {
                    isPresented = false
                }
                ButtonPrimary(title: store.isInviting ? "Sending" : "Send invite", kbd: "↵") {
                    Task {
                        if await store.invite(email: email) {
                            isPresented = false
                        }
                    }
                }
                .disabled(store.isInviting || !email.contains("@"))
            }
        }
        .padding(IterSpacing.gapLarge)
        .frame(width: 380)
        .background(Color.iterPanel(for: colorScheme))
    }
}

private struct TeamInlineRetryBanner: View {
    @Environment(\.colorScheme) private var colorScheme

    let message: String
    let retry: () -> Void

    var body: some View {
        HStack(spacing: IterSpacing.gapSmall) {
            Image(systemName: "exclamationmark.triangle")
                .font(.system(size: 11, weight: .medium))
                .foregroundStyle(Color.iterWarn(for: colorScheme))
                .accessibilityHidden(true)

            Text(verbatim: message)
                .font(IterFont.sansSmall)
                .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
                .lineLimit(2)

            Spacer()

            IterButton(title: "Retry", action: retry)
        }
        .padding(.horizontal, IterSpacing.gapMedium)
        .frame(minHeight: 36)
        .background(Color.iterWarnSoft(for: colorScheme))
        .clipShape(.rect(cornerRadius: IterRadius.card))
        .overlay {
            RoundedRectangle(cornerRadius: IterRadius.card)
                .stroke(Color.iterBorder(for: colorScheme), lineWidth: 1)
        }
    }
}

struct TeamRailCards: View {
    let dashboard: DashboardTeamResponse?

    var body: some View {
        VStack(alignment: .leading, spacing: IterSpacing.gapMedium) {
            RailCard(
                title: "Top patterns this week",
                count: "\(dashboard?.topPatterns.count ?? 0)",
                items: dashboard.map(DashboardTeamDisplay.patternItems) ?? []
            )

            RailCard(
                title: "Active now",
                count: "0",
                items: []
            )
        }
    }
}
