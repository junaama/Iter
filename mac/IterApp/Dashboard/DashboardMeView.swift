import AppKit
import SwiftUI

struct DashboardMeView: View {
    @Environment(\.colorScheme) private var colorScheme
    @Bindable var store: DashboardMeStore

    let onSelectSession: (String) -> Void
    let onViewAll: () -> Void

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: IterSpacing.gapLarge) {
                KPIRow(tiles: kpiTiles)

                if let errorMessage = store.errorMessage {
                    DashboardInlineRetryBanner(message: errorMessage) {
                        Task { await store.load(forceRefresh: true) }
                    }
                }

                if let dashboard = store.dashboard, DashboardMeDisplay.isEmptyDashboard(dashboard) {
                    FirstScoreEstimateView(hours: DashboardMeDisplay.firstScoreEstimateHours(dashboard))
                }

                RecentSessionsSection(
                    count: store.sessionCountLabel,
                    sessions: sessionItems,
                    isLoading: store.dashboard == nil && store.isLoading,
                    emptyMessage: emptyMessage,
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
    }

    private var kpiTiles: [KPITileData] {
        if let dashboard = store.dashboard {
            return DashboardMeDisplay.kpiTiles(from: dashboard)
        }
        return [
            KPITileData(label: "sessions", value: "--", unit: nil, delta: .flat("loading"), sparkline: []),
            KPITileData(label: "acceptance %", value: "--", unit: "%", delta: .flat("loading"), sparkline: []),
            KPITileData(label: "avg score", value: "--", unit: nil, delta: .flat("loading"), sparkline: []),
            KPITileData(label: "time saved", value: "--", unit: "h", delta: .flat("loading"), sparkline: [])
        ]
    }

    private var sessionItems: [SessionListItem] {
        guard let dashboard = store.dashboard else { return [] }
        return DashboardMeDisplay.sessionItems(from: dashboard)
    }

    private var emptyMessage: String {
        guard let dashboard = store.dashboard else {
            return "First scored session estimated in 4 hours"
        }
        return "First scored session estimated in \(DashboardMeDisplay.firstScoreEstimateHours(dashboard)) hours"
    }
}

private struct RecentSessionsSection: View {
    @Environment(\.colorScheme) private var colorScheme

    let count: String
    let sessions: [SessionListItem]
    let isLoading: Bool
    let emptyMessage: String
    let onViewAll: () -> Void
    let onSelectSession: (String) -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: IterSpacing.gapSmall) {
            HStack(spacing: IterSpacing.gapSmall) {
                Text(verbatim: "Recent sessions")
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

            SessionTable(
                sessions: sessions,
                emptyMessage: emptyMessage,
                isLoading: isLoading
            ) { session in
                onSelectSession(session.id)
            }
        }
    }
}

private struct DashboardInlineRetryBanner: View {
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

private struct FirstScoreEstimateView: View {
    @Environment(\.colorScheme) private var colorScheme

    let hours: Int

    var body: some View {
        HStack(spacing: IterSpacing.gapSmall) {
            Circle()
                .fill(Color.iterWarn(for: colorScheme))
                .frame(width: 7, height: 7)
                .overlay {
                    Circle()
                        .stroke(Color.iterWarnSoft(for: colorScheme), lineWidth: 5)
                }
                .accessibilityHidden(true)

            Text(verbatim: "First scored session estimated in \(hours) hours")
                .font(IterFont.monoLabel)
                .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
        }
        .padding(.horizontal, IterSpacing.gapMedium)
        .frame(height: 34)
        .background(Color.iterSelected(for: colorScheme))
        .clipShape(.rect(cornerRadius: IterRadius.card))
    }
}
