import SwiftUI

struct Harness: View {
    @Environment(\.colorScheme) private var colorScheme

    let id: HarnessID

    var body: some View {
        HStack(spacing: 6) {
            RoundedRectangle(cornerRadius: IterRadius.harnessSwatch)
                .fill(id.tint.color)
                .frame(width: 6, height: 6)
                .accessibilityHidden(true)

            Text(verbatim: id.tint.shortCode)
                .font(IterFont.monoLabel)
                .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
        }
        .accessibilityElement(children: .combine)
        .accessibilityLabel("Harness \(id.tint.shortCode)")
    }
}

#Preview("Harness States") {
    HStack(spacing: IterSpacing.gapMedium) {
        ForEach(HarnessID.allCases) { id in
            Harness(id: id)
        }
    }
    .padding()
}
