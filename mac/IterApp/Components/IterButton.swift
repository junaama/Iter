import SwiftUI

struct IterButton: View {
    @Environment(\.colorScheme) private var colorScheme
    @State private var isHovering = false

    let title: String
    var kbd: String?
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 4) {
                Text(verbatim: title)
                    .font(IterFont.sansLabel)
                if let kbd {
                    KBD(text: kbd)
                }
            }
            .foregroundStyle(Color.iterTextPrimary(for: colorScheme))
            .padding(.horizontal, 8)
            .frame(height: 22)
            .background(isHovering ? Color.iterHover(for: colorScheme) : Color.iterPanel(for: colorScheme))
            .clipShape(.rect(cornerRadius: IterRadius.button))
            .overlay {
                RoundedRectangle(cornerRadius: IterRadius.button)
                    .stroke(
                        isHovering ? Color.iterBorderStrong(for: colorScheme) : Color.iterBorder(for: colorScheme),
                        lineWidth: 1
                    )
            }
        }
        .buttonStyle(.plain)
        .onHover { isHovering = $0 }
    }
}

struct ButtonPrimary: View {
    @Environment(\.colorScheme) private var colorScheme
    @State private var isHovering = false

    let title: String
    var kbd: String?
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 4) {
                Text(verbatim: title)
                    .font(IterFont.sansLabel)
                if let kbd {
                    KBD(text: kbd, isPrimary: true)
                }
            }
            .foregroundStyle(Color.white)
            .padding(.horizontal, 8)
            .frame(height: 22)
            .background(
                isHovering
                    ? Color.oklch(lightness: 0.62, chroma: 0.16, hue: 38)
                    : Color.iterAccent(for: colorScheme)
            )
            .clipShape(.rect(cornerRadius: IterRadius.button))
        }
        .buttonStyle(.plain)
        .onHover { isHovering = $0 }
    }
}

struct KBD: View {
    @Environment(\.colorScheme) private var colorScheme

    let text: String
    var isPrimary = false

    var body: some View {
        Text(verbatim: text)
            .font(IterFont.monoTiny)
            .foregroundStyle(isPrimary ? Color.white.opacity(0.8) : Color.iterTextTertiary(for: colorScheme))
    }
}

#Preview("Button States") {
    HStack(spacing: IterSpacing.gapSmall) {
        IterButton(title: "Default", kbd: "⌘K") {}
        ButtonPrimary(title: "Copy to clipboard", kbd: "↵") {}
        IterButton(title: "Disabled") {}
            .disabled(true)
    }
    .padding()
}
