import SwiftUI

struct Avatar: View {
    @Environment(\.colorScheme) private var colorScheme

    let initials: String
    let seed: String
    var size: CGFloat = 18

    var body: some View {
        Text(verbatim: initials)
            .font(size > 18 ? IterFont.mono(size: 11.5, weight: .semibold) : IterFont.monoAvatar)
            .foregroundStyle(Color.iterBackground(for: colorScheme))
            .frame(width: size, height: size)
            .background(tint.color)
            .clipShape(.rect(cornerRadius: size > 18 ? IterRadius.avatarLarge : IterRadius.avatar))
            .accessibilityLabel("Avatar \(initials)")
    }

    private var tint: IterAvatarTint {
        let palette = IterAvatarTint.allCases
        let total = seed.unicodeScalars.reduce(0) { $0 + Int($1.value) }
        return palette[abs(total) % palette.count]
    }
}

#Preview("Avatar States") {
    HStack(spacing: IterSpacing.gapSmall) {
        Avatar(initials: "PS", seed: "priya")
        Avatar(initials: "MC", seed: "mchen")
        Avatar(initials: "AY", seed: "ana", size: 28)
    }
    .padding()
}
