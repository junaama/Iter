import SwiftUI

struct SessionTable: View {
    @Environment(\.colorScheme) private var colorScheme

    let sessions: [SessionListItem]

    var body: some View {
        VStack(spacing: 0) {
            header

            if sessions.isEmpty {
                emptyRow
            } else {
                ForEach(sessions) { session in
                    SessionTableRow(session: session)
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
        Text(verbatim: "No sessions")
            .font(IterFont.monoLabel)
            .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
            .frame(maxWidth: .infinity, minHeight: IterSpacing.rowHeight)
    }
}

private struct SessionTableRow: View {
    @Environment(\.colorScheme) private var colorScheme

    let session: SessionListItem

    var body: some View {
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
        .background(session.isSelected ? Color.iterAccentSoft(for: colorScheme) : Color.clear)
        .overlay(alignment: .bottom) {
            Rectangle()
                .fill(Color.iterBorder(for: colorScheme))
                .frame(height: 1)
        }
        .contentShape(.rect)
    }
}

#Preview("Session Table States") {
    VStack(spacing: IterSpacing.gapLarge) {
        SessionTable(sessions: ComponentPreviewData.sessions)
        SessionTable(sessions: [])
        SessionTable(sessions: [ComponentPreviewData.sessions[2]])
    }
    .padding()
}
