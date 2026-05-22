import SwiftUI

struct SessionFeed: View {
    let groups: [(day: String, sessions: [SessionListItem])]

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            ForEach(Array(groups.enumerated()), id: \.offset) { _, group in
                Text(verbatim: group.day)
                    .font(IterFont.monoSmall)
                    .foregroundStyle(.secondary)
                    .textCase(.uppercase)
                    .padding(.top, 10)
                    .padding(.horizontal, 4)

                if group.sessions.isEmpty {
                    SessionFeedEmpty()
                } else {
                    ForEach(group.sessions) { session in
                        SessionFeedRow(session: session)
                    }
                }
            }
        }
    }
}

private struct SessionFeedRow: View {
    @Environment(\.colorScheme) private var colorScheme

    let session: SessionListItem

    var body: some View {
        Grid(horizontalSpacing: 12, verticalSpacing: 0) {
            GridRow(alignment: .top) {
                Text(verbatim: session.when)
                    .font(IterFont.monoLabel)
                    .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
                    .frame(width: 58, alignment: .leading)

                Avatar(initials: session.authorInitials, seed: session.avatarSeed)

                VStack(alignment: .leading, spacing: 4) {
                    HStack(spacing: IterSpacing.gapSmall) {
                        Text(verbatim: session.task)
                            .font(IterFont.sans(size: 12.5, weight: .medium))
                            .foregroundStyle(Color.iterTextPrimary(for: colorScheme))
                        Text(verbatim: session.repo)
                            .font(IterFont.monoLabel)
                            .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
                    }

                    HStack(spacing: 10) {
                        Harness(id: session.harness)
                        Text(verbatim: session.duration)
                        Text(verbatim: session.tools)
                    }
                    .font(IterFont.monoLabel)
                    .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
                }

                VStack(alignment: .trailing, spacing: 4) {
                    Score(value: session.score)
                    StatusChip(status: session.status)
                }
            }
        }
        .padding(8)
        .background(session.isSelected ? Color.iterAccentSoft(for: colorScheme) : Color.clear)
        .clipShape(.rect(cornerRadius: IterRadius.navItem))
    }
}

private struct SessionFeedEmpty: View {
    @Environment(\.colorScheme) private var colorScheme

    var body: some View {
        Text(verbatim: "No activity")
            .font(IterFont.monoLabel)
            .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
            .padding(8)
    }
}

#Preview("Session Feed States") {
    SessionFeed(groups: [
        ("Today", Array(ComponentPreviewData.sessions.prefix(2))),
        ("Yesterday", [ComponentPreviewData.sessions[2]]),
        ("Mon", [])
    ])
    .padding()
}
