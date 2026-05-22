import SwiftUI

struct SegmentItem: Identifiable, Hashable {
    let id: String
    let title: String
}

struct SegmentedControl: View {
    @Environment(\.colorScheme) private var colorScheme

    let items: [SegmentItem]
    @Binding var selection: SegmentItem

    var body: some View {
        HStack(spacing: 0) {
            ForEach(items) { item in
                Button {
                    selection = item
                } label: {
                    Text(verbatim: item.title)
                        .font(IterFont.monoLabel)
                        .foregroundStyle(
                            selection == item
                                ? Color.iterTextPrimary(for: colorScheme)
                                : Color.iterTextSecondary(for: colorScheme)
                        )
                        .padding(.horizontal, 10)
                        .frame(height: 26)
                        .background(selection == item ? Color.iterSelected(for: colorScheme) : Color.clear)
                }
                .buttonStyle(.plain)

                if item != items.last {
                    Rectangle()
                        .fill(Color.iterBorder(for: colorScheme))
                        .frame(width: 1)
                }
            }
        }
        .background(Color.iterPanel(for: colorScheme))
        .clipShape(.rect(cornerRadius: IterRadius.segment))
        .overlay {
            RoundedRectangle(cornerRadius: IterRadius.segment)
                .stroke(Color.iterBorder(for: colorScheme), lineWidth: 1)
        }
    }
}

#Preview("Segmented States") {
    @Previewable @State var selected = SegmentItem(id: "table", title: "Table")
    let items = [
        SegmentItem(id: "table", title: "Table"),
        SegmentItem(id: "cards", title: "Cards"),
        SegmentItem(id: "feed", title: "Feed")
    ]

    VStack(spacing: IterSpacing.gapLarge) {
        SegmentedControl(items: items, selection: $selected)
        SegmentedControl(items: [items[0]], selection: $selected)
        SegmentedControl(items: items, selection: .constant(items[2]))
    }
    .padding()
}
