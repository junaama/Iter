import SwiftUI

struct SearchInput: View {
    @Environment(\.colorScheme) private var colorScheme
    @FocusState private var isFocused: Bool

    @Binding var text: String
    var placeholder = "Search"

    var body: some View {
        HStack(spacing: IterSpacing.gapSmall) {
            Image(systemName: "magnifyingglass")
                .font(.system(size: 11, weight: .medium))
                .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
                .accessibilityHidden(true)

            TextField(placeholder, text: $text, prompt: Text(placeholder))
                .textFieldStyle(.plain)
                .font(IterFont.monoLabel)
                .foregroundStyle(Color.iterTextPrimary(for: colorScheme))
                .focused($isFocused)
                .onSubmit {}

            KBD(text: "⌘K")
        }
        .padding(.horizontal, IterSpacing.gapSmall)
        .frame(width: 220, height: 26)
        .background(Color.iterPanel(for: colorScheme))
        .clipShape(.rect(cornerRadius: IterRadius.button))
        .overlay {
            RoundedRectangle(cornerRadius: IterRadius.button)
                .stroke(
                    isFocused ? Color.iterBorderStrong(for: colorScheme) : Color.iterBorder(for: colorScheme),
                    lineWidth: 1
                )
        }
    }
}

#Preview("Search States") {
    @Previewable @State var query = ""
    VStack(alignment: .leading, spacing: IterSpacing.gapLarge) {
        SearchInput(text: $query)
        SearchInput(text: .constant("workspace:iter"))
        SearchInput(text: .constant(""), placeholder: "No results")
    }
    .padding()
}
