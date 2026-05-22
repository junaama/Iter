import SwiftUI

struct RailCard: View {
    @Environment(\.colorScheme) private var colorScheme

    let title: String
    let count: String
    let items: [RailItem]

    var body: some View {
        VStack(spacing: 0) {
            HStack {
                Text(verbatim: title)
                    .font(IterFont.sansSectionTitle)
                    .foregroundStyle(Color.iterTextPrimary(for: colorScheme))
                Spacer()
                Text(verbatim: count)
                    .font(IterFont.monoSmall)
                    .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
            }
            .padding(.horizontal, 12)
            .padding(.top, 10)
            .padding(.bottom, 6)

            if items.isEmpty {
                RailItemEmpty()
            } else {
                ForEach(items) { item in
                    RailItemRow(item: item)
                }
            }
        }
        .background(Color.iterPanel(for: colorScheme))
        .clipShape(.rect(cornerRadius: IterRadius.card))
        .overlay {
            RoundedRectangle(cornerRadius: IterRadius.card)
                .stroke(Color.iterBorder(for: colorScheme), lineWidth: 1)
        }
    }
}

private struct RailItemRow: View {
    @Environment(\.colorScheme) private var colorScheme

    let item: RailItem

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(verbatim: item.title)
                .font(IterFont.sans(size: 12))
                .foregroundStyle(Color.iterTextPrimary(for: colorScheme))

            Text(verbatim: item.metadata)
                .font(IterFont.monoSmall)
                .foregroundStyle(Color.iterTextTertiary(for: colorScheme))

            if item.primaryAction != nil || item.secondaryAction != nil {
                HStack(spacing: 6) {
                    if let primaryAction = item.primaryAction {
                        ButtonPrimary(title: primaryAction, kbd: "↵") {}
                    }
                    if let secondaryAction = item.secondaryAction {
                        IterButton(title: secondaryAction) {}
                    }
                }
                .padding(.top, 4)
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
        .overlay(alignment: .top) {
            Rectangle()
                .fill(Color.iterBorder(for: colorScheme))
                .frame(height: 1)
        }
    }
}

private struct RailItemEmpty: View {
    @Environment(\.colorScheme) private var colorScheme

    var body: some View {
        Text(verbatim: "No items")
            .font(IterFont.monoLabel)
            .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.horizontal, 12)
            .padding(.vertical, 8)
    }
}

#Preview("Rail Card States") {
    VStack(spacing: IterSpacing.gapLarge) {
        RailCard(
            title: "Suggestions waiting",
            count: "2",
            items: [
                RailItem(
                    title: "Tighten migration verifier prompt",
                    metadata: "iter/mac · 8m ago",
                    primaryAction: "Copy to clipboard",
                    secondaryAction: "Dismiss"
                ),
                RailItem(
                    title: "Use session replay fixture",
                    metadata: "api · 1h ago",
                    primaryAction: nil,
                    secondaryAction: nil
                )
            ]
        )
        RailCard(title: "Active now", count: "0", items: [])
    }
    .padding()
}
