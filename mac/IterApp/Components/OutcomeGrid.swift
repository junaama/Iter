import SwiftUI

struct OutcomeGrid: View {
    @Environment(\.colorScheme) private var colorScheme

    let tests: String
    let commits: String
    let files: String
    let toolsUsed: String

    var body: some View {
        Grid(horizontalSpacing: 1, verticalSpacing: 1) {
            GridRow {
                panel(label: "tests", value: tests)
                panel(label: "commits", value: commits)
            }
            GridRow {
                panel(label: "files", value: files)
                panel(label: "tools used", value: toolsUsed)
            }
        }
        .background(Color.iterBorder(for: colorScheme))
        .clipShape(.rect(cornerRadius: IterRadius.standard))
        .overlay {
            RoundedRectangle(cornerRadius: IterRadius.standard)
                .stroke(Color.iterBorder(for: colorScheme), lineWidth: 1)
        }
    }

    private func panel(label: String, value: String) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(verbatim: value)
                .font(IterFont.monoOutcomeValue)
                .foregroundStyle(Color.iterTextPrimary(for: colorScheme))
            Text(verbatim: label)
                .font(IterFont.sansLabel)
                .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 10)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color.iterPanel(for: colorScheme))
    }
}

#Preview("Outcome Grid States") {
    VStack(spacing: IterSpacing.gapLarge) {
        OutcomeGrid(tests: "18/18", commits: "2", files: "11", toolsUsed: "9")
        OutcomeGrid(tests: "0/0", commits: "0", files: "0", toolsUsed: "0")
        OutcomeGrid(tests: "3 failed", commits: "0", files: "7", toolsUsed: "14")
    }
    .padding()
}
