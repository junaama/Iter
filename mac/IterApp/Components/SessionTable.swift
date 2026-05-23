import SwiftUI

struct SessionTable: View {
    @Environment(\.colorScheme) private var colorScheme

    let sessions: [SessionListItem]
    var emptyMessage = "No sessions"
    var isLoading = false
    var showsAuthorColumn = false
    var onSelect: (SessionListItem) -> Void = { _ in }

    var body: some View {
        VStack(spacing: 0) {
            header

            if isLoading {
                ForEach(0..<8, id: \.self) { _ in
                    SessionTableSkeletonRow(showsAuthorColumn: showsAuthorColumn)
                }
            } else if sessions.isEmpty {
                emptyRow
            } else {
                ForEach(sessions) { session in
                    SessionTableRow(session: session, showsAuthorColumn: showsAuthorColumn) {
                        onSelect(session)
                    }
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
        HStack(spacing: SessionTableColumns.spacing) {
            headerText("Started")
                .frame(width: SessionTableColumns.started, alignment: .leading)

            if showsAuthorColumn {
                headerText("Author")
                    .frame(width: SessionTableColumns.author, alignment: .leading)
            }

            headerText("Repo·Task")
                .frame(maxWidth: .infinity, alignment: .leading)

            headerText("Harness")
                .frame(width: SessionTableColumns.harness, alignment: .leading)

            headerText("Dur")
                .frame(width: SessionTableColumns.duration, alignment: .leading)

            headerText("Score")
                .frame(width: SessionTableColumns.score, alignment: .leading)

            headerText("Status")
                .frame(width: SessionTableColumns.status, alignment: .leading)

            headerText("Accepted")
                .frame(width: SessionTableColumns.accepted, alignment: .leading)
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
        Text(verbatim: emptyMessage)
            .font(IterFont.monoLabel)
            .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
            .frame(maxWidth: .infinity, minHeight: IterSpacing.rowHeight)
    }

    private func headerText(_ value: String) -> some View {
        Text(verbatim: value)
            .font(IterFont.monoSmall)
            .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
            .textCase(.uppercase)
            .lineLimit(1)
    }
}

private struct SessionTableRow: View {
    @Environment(\.colorScheme) private var colorScheme

    let session: SessionListItem
    var showsAuthorColumn = false
    let onSelect: () -> Void

    var body: some View {
        Button(action: onSelect) {
            HStack(spacing: SessionTableColumns.spacing) {
                Text(verbatim: session.when)
                    .font(IterFont.monoLabel)
                    .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
                    .lineLimit(1)
                    .frame(width: SessionTableColumns.started, alignment: .leading)

                if showsAuthorColumn {
                    HStack(spacing: 6) {
                        Avatar(initials: session.authorInitials, seed: session.avatarSeed)
                        Text(verbatim: session.authorInitials)
                            .font(IterFont.monoLabel)
                            .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
                            .lineLimit(1)
                    }
                    .frame(width: SessionTableColumns.author, alignment: .leading)
                }

                VStack(alignment: .leading, spacing: 0) {
                    Text(verbatim: session.repo)
                        .font(IterFont.monoLabel)
                        .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
                        .lineLimit(1)
                        .truncationMode(.tail)
                    Text(verbatim: session.task)
                        .font(IterFont.sansSmall)
                        .foregroundStyle(Color.iterTextPrimary(for: colorScheme))
                        .lineLimit(1)
                        .truncationMode(.tail)
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                .clipped()
                .layoutPriority(1)

                Harness(id: session.harness)
                    .frame(width: SessionTableColumns.harness, alignment: .leading)

                Text(verbatim: session.duration)
                    .font(IterFont.monoLabel)
                    .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
                    .lineLimit(1)
                    .frame(width: SessionTableColumns.duration, alignment: .leading)

                Score(value: session.score)
                    .frame(width: SessionTableColumns.score, alignment: .leading)

                StatusChip(status: session.status)
                    .frame(width: SessionTableColumns.status, alignment: .leading)

                Text(verbatim: session.accepted)
                    .font(IterFont.monoLabel)
                    .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
                    .lineLimit(1)
                    .frame(width: SessionTableColumns.accepted, alignment: .leading)
            }
            .padding(.horizontal, 12)
            .frame(height: IterSpacing.rowHeight)
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .background(session.isSelected ? Color.iterAccentSoft(for: colorScheme) : Color.clear)
        .overlay(alignment: .bottom) {
            Rectangle()
                .fill(Color.iterBorder(for: colorScheme))
                .frame(height: 1)
        }
    }
}

private struct SessionTableSkeletonRow: View {
    @Environment(\.colorScheme) private var colorScheme

    let showsAuthorColumn: Bool

    var body: some View {
        HStack(spacing: SessionTableColumns.spacing) {
            skeletonBar(width: 44)
                .frame(width: SessionTableColumns.started, alignment: .leading)

            if showsAuthorColumn {
                skeletonBar(width: 34)
                    .frame(width: SessionTableColumns.author, alignment: .leading)
            }

            skeletonBar(width: 132)
                .frame(maxWidth: .infinity, alignment: .leading)

            skeletonBar(width: 42)
                .frame(width: SessionTableColumns.harness, alignment: .leading)

            skeletonBar(width: 28)
                .frame(width: SessionTableColumns.duration, alignment: .leading)

            skeletonBar(width: 30)
                .frame(width: SessionTableColumns.score, alignment: .leading)

            skeletonBar(width: 54)
                .frame(width: SessionTableColumns.status, alignment: .leading)

            skeletonBar(width: 28)
                .frame(width: SessionTableColumns.accepted, alignment: .leading)
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

    private func skeletonBar(width: CGFloat) -> some View {
        RoundedRectangle(cornerRadius: 3)
            .fill(Color.iterSelected(for: colorScheme))
            .frame(width: width, height: 10)
    }
}

private enum SessionTableColumns {
    static let spacing: CGFloat = 10
    static let started: CGFloat = 86
    static let author: CGFloat = 72
    static let harness: CGFloat = 70
    static let duration: CGFloat = 44
    static let score: CGFloat = 56
    static let status: CGFloat = 82
    static let accepted: CGFloat = 66
}

#Preview("Session Table States") {
    VStack(spacing: IterSpacing.gapLarge) {
        SessionTable(sessions: ComponentPreviewData.sessions)
        SessionTable(sessions: [])
        SessionTable(sessions: [], isLoading: true)
        SessionTable(sessions: [ComponentPreviewData.sessions[2]])
    }
    .padding()
}
