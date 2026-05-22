import SwiftUI

struct Score: View {
    @Environment(\.colorScheme) private var colorScheme

    let value: Int

    private var clampedValue: Int { min(max(value, 0), 100) }

    var body: some View {
        Text(verbatim: "\(clampedValue)")
            .font(IterFont.monoScore)
            .foregroundStyle(foreground)
            .padding(.horizontal, 6)
            .padding(.vertical, 1)
            .frame(minWidth: 30, minHeight: 18)
            .background(background)
            .clipShape(.rect(cornerRadius: IterRadius.scoreChip))
            .accessibilityLabel("Score \(clampedValue)")
    }

    private var foreground: Color {
        switch clampedValue {
        case 80...100: return .iterGood(for: colorScheme)
        case 60..<80: return .iterWarn(for: colorScheme)
        default: return .iterBad(for: colorScheme)
        }
    }

    private var background: Color {
        switch clampedValue {
        case 80...100: return .iterGoodSoft(for: colorScheme)
        case 60..<80: return .iterWarnSoft(for: colorScheme)
        default: return .iterBadSoft(for: colorScheme)
        }
    }
}

#Preview("Score States") {
    HStack(spacing: IterSpacing.gapSmall) {
        Score(value: IterScoreValue.fromCompositeScore(0.92))
        Score(value: IterScoreValue.fromCompositeScore(0.74))
        Score(value: IterScoreValue.fromCompositeScore(0.49))
    }
    .padding()
}
