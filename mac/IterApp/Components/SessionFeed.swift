import SwiftUI

struct SessionFeed: View {
    @Environment(\.colorScheme) private var colorScheme

    let groups: [(day: String, sessions: [SessionListItem])]
    var onSelect: (SessionListItem) -> Void = { _ in }

    var body: some View {
        LazyVStack(alignment: .leading, spacing: 2) {
            ForEach(Array(groups.enumerated()), id: \.offset) { _, group in
                Text(verbatim: group.day)
                    .font(IterFont.monoSmall)
                    .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
                    .textCase(.uppercase)
                    .padding(.top, 10)
                    .padding(.horizontal, 4)

                if group.sessions.isEmpty {
                    SessionFeedEmpty()
                } else {
                    ForEach(group.sessions) { session in
                        SessionFeedRow(session: session) {
                            onSelect(session)
                        }
                    }
                }
            }
        }
    }
}

private struct SessionFeedRow: View {
    @Environment(\.colorScheme) private var colorScheme

    let session: SessionListItem
    let onSelect: () -> Void

    var body: some View {
        Button(action: onSelect) {
            HStack(alignment: .top, spacing: 12) {
                Text(verbatim: session.when)
                    .font(IterFont.monoLabel)
                    .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
                    .lineLimit(1)
                    .frame(width: 58, alignment: .leading)

                Avatar(initials: session.authorInitials, seed: session.avatarSeed)

                VStack(alignment: .leading, spacing: 4) {
                    HStack(spacing: IterSpacing.gapSmall) {
                        Text(verbatim: session.task)
                            .font(IterFont.sans(size: 12.5, weight: .medium))
                            .foregroundStyle(Color.iterTextPrimary(for: colorScheme))
                            .lineLimit(1)
                            .truncationMode(.tail)
                            .layoutPriority(1)

                        Text(verbatim: session.repo)
                            .font(IterFont.monoLabel)
                            .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
                            .lineLimit(1)
                            .truncationMode(.middle)
                    }

                    HStack(spacing: 10) {
                        Harness(id: session.harness)
                        Text(verbatim: session.duration)
                            .lineLimit(1)
                        Text(verbatim: session.tools)
                            .lineLimit(1)
                    }
                    .font(IterFont.monoLabel)
                    .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                .clipped()

                VStack(alignment: .trailing, spacing: 4) {
                    Score(value: session.score)
                    StatusChip(status: session.status)
                }
                .frame(width: 76, alignment: .trailing)
            }
            .padding(8)
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
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
