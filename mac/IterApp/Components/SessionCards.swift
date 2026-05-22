import SwiftUI

struct SessionCards: View {
    let sessions: [SessionListItem]

    private let columns = [
        GridItem(.adaptive(minimum: IterSpacing.cardGridMin), spacing: 10)
    ]

    var body: some View {
        LazyVGrid(columns: columns, spacing: 10) {
            if sessions.isEmpty {
                SessionCardEmpty()
            } else {
                ForEach(sessions) { session in
                    SessionCard(session: session)
                }
            }
        }
    }
}

private struct SessionCard: View {
    @Environment(\.colorScheme) private var colorScheme

    let session: SessionListItem

    var body: some View {
        VStack(alignment: .leading, spacing: IterSpacing.gapSmall) {
            HStack(spacing: IterSpacing.gapSmall) {
                Text(verbatim: session.repo)
                    .font(IterFont.monoLabel)
                    .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
                    .lineLimit(1)

                Spacer()
                Score(value: session.score)
            }

            Text(verbatim: session.task)
                .font(IterFont.sansCardTitle)
                .foregroundStyle(Color.iterTextPrimary(for: colorScheme))
                .lineLimit(2)

            HStack(spacing: 10) {
                Harness(id: session.harness)
                Text(verbatim: session.duration)
                Text(verbatim: session.tools)
                Text(verbatim: session.accepted)
            }
            .font(IterFont.monoLabel)
            .foregroundStyle(Color.iterTextTertiary(for: colorScheme))

            Sparkline(values: session.sparkline, tint: session.harness.tint.color, height: 14, barWidth: 4)

            HStack {
                StatusChip(status: session.status)
                Spacer()
                Text(verbatim: session.relativeTime)
                    .font(IterFont.monoSmall)
                    .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
            }
        }
        .padding(IterSpacing.cardPadding)
        .frame(maxWidth: .infinity, minHeight: 122, alignment: .topLeading)
        .background(Color.iterPanel(for: colorScheme))
        .clipShape(.rect(cornerRadius: IterRadius.card))
        .overlay {
            RoundedRectangle(cornerRadius: IterRadius.card)
                .stroke(
                    session.isSelected ? Color.iterAccent(for: colorScheme) : Color.iterBorder(for: colorScheme),
                    lineWidth: 1
                )
        }
    }
}

private struct SessionCardEmpty: View {
    @Environment(\.colorScheme) private var colorScheme

    var body: some View {
        Text(verbatim: "No sessions")
            .font(IterFont.monoLabel)
            .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
            .frame(maxWidth: .infinity, minHeight: 122)
            .background(Color.iterPanel(for: colorScheme))
            .clipShape(.rect(cornerRadius: IterRadius.card))
            .overlay {
                RoundedRectangle(cornerRadius: IterRadius.card)
                    .stroke(Color.iterBorder(for: colorScheme), lineWidth: 1)
            }
    }
}

#Preview("Session Card States") {
    VStack(spacing: IterSpacing.gapLarge) {
        SessionCards(sessions: ComponentPreviewData.sessions)
        SessionCards(sessions: [])
    }
    .padding()
}
