import SwiftUI

struct Sparkline: View {
    let values: [Double]
    var tint: Color
    var height: CGFloat = 22
    var barWidth: CGFloat?

    var body: some View {
        HStack(alignment: .bottom, spacing: 2) {
            ForEach(Array(normalized.enumerated()), id: \.offset) { _, value in
                RoundedRectangle(cornerRadius: 1)
                    .fill(tint.opacity(0.55))
                    .frame(width: barWidth, height: max(3, value * height))
            }
        }
        .frame(height: height, alignment: .bottom)
        .accessibilityHidden(true)
    }

    private var normalized: [Double] {
        guard let maxValue = values.max(), maxValue > 0 else {
            return values.isEmpty ? [0.15, 0.15, 0.15] : values.map { _ in 0.15 }
        }
        return values.map { min(max($0 / maxValue, 0.15), 1) }
    }
}
