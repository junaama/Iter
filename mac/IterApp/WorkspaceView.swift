import SwiftUI

struct WorkspaceView: View {
    @Environment(\.colorScheme) private var colorScheme

    var body: some View {
        ZStack {
            Color.iterBackground(for: colorScheme)
                .ignoresSafeArea()

            VStack(spacing: IterSpacing.gapSmall) {
                Text(verbatim: "iter — Workspace")
                    .font(IterFont.monoTitle)
                    .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
                    .accessibilityLabel(Text("iter Workspace"))

                Text(verbatim: "IBM Plex Mono 0123456789")
                    .font(IterFont.monoBody)
                    .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
                    .accessibilityHidden(true)
            }
            .padding(IterSpacing.mainPanePadding)
        }
        .frame(
            maxWidth: IterSpacing.windowMaxWidth,
            maxHeight: IterSpacing.windowMaxHeight
        )
    }
}

#Preview("Light") {
    WorkspaceView()
        .preferredColorScheme(.light)
}

#Preview("Dark") {
    WorkspaceView()
        .preferredColorScheme(.dark)
}
