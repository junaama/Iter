import SwiftUI

struct SessionTable: View {
    @Environment(\.colorScheme) private var colorScheme

    let sessions: [SessionListItem]
    var emptyMessage = "No sessions"
    var isLoading = false
    var onSelect: (SessionListItem) -> Void = { _ in }

    var body: some View {
        VStack(spacing: 0) {
            header

            if isLoading {
                ForEach(0..<8, id: \.self) { _ in
                    SessionTableSkeletonRow()
                }
            } else if sessions.isEmpty {
                emptyRow
            } else {
                ForEach(sessions) { session in
                    SessionTableRow(session: session) {
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
        Grid(horizontalSpacing: 10, verticalSpacing: 0) {
            GridRow {
                ForEach(["Started", "Repo·Task", "Harness", "Dur", "Score", "Status", "Accepted"], id: \.self) {
                    Text(verbatim: $0)
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
        Text(verbatim: emptyMessage)
            .font(IterFont.monoLabel)
            .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
            .frame(maxWidth: .infinity, minHeight: IterSpacing.rowHeight)
    }
}

private struct SessionTableRow: View {
    @Environment(\.colorScheme) private var colorScheme

    let session: SessionListItem
    let onSelect: () -> Void

    var body: some View {
        Button(action: onSelect) {
            Grid(horizontalSpacing: 10, verticalSpacing: 0) {
                GridRow {
                    Text(verbatim: session.when)
                        .font(IterFont.monoLabel)
                        .foregroundStyle(Color.iterTextSecondary(for: colorScheme))

                    VStack(alignment: .leading, spacing: 0) {
                        Text(verbatim: session.repo)
                            .font(IterFont.monoLabel)
                            .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
                        Text(verbatim: session.task)
                            .font(IterFont.sansSmall)
                            .foregroundStyle(Color.iterTextPrimary(for: colorScheme))
                            .lineLimit(1)
                    }

                    Harness(id: session.harness)

                    Text(verbatim: session.duration)
                        .font(IterFont.monoLabel)
                        .foregroundStyle(Color.iterTextSecondary(for: colorScheme))

                    Score(value: session.score)

                    StatusChip(status: session.status)

                    Text(verbatim: session.accepted)
                        .font(IterFont.monoLabel)
                        .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
                }
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

    var body: some View {
        Grid(horizontalSpacing: 10, verticalSpacing: 0) {
            GridRow {
                ForEach([44, 132, 58, 36, 34, 58, 46], id: \.self) { width in
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

#Preview("Session Table States") {
    VStack(spacing: IterSpacing.gapLarge) {
        SessionTable(sessions: ComponentPreviewData.sessions)
        SessionTable(sessions: [])
        SessionTable(sessions: [], isLoading: true)
        SessionTable(sessions: [ComponentPreviewData.sessions[2]])
    }
    .padding()
}
