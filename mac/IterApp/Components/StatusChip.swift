import SwiftUI

struct StatusChip: View {
    @Environment(\.colorScheme) private var colorScheme

    let status: OutcomeStatus

    var body: some View {
        HStack(spacing: 5) {
            Circle()
                .fill(status.tint(for: colorScheme))
                .frame(width: 6, height: 6)
                .accessibilityHidden(true)

            Text(verbatim: status.rawValue)
                .font(IterFont.monoLabel)
                .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
        }
        .accessibilityElement(children: .combine)
        .accessibilityLabel("Status \(status.rawValue)")
    }
}

#Preview("Status States") {
    HStack(spacing: IterSpacing.gapMedium) {
        ForEach(OutcomeStatus.allCases) { status in
            StatusChip(status: status)
        }
    }
    .padding()
}
