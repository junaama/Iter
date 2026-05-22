import SwiftUI

struct KPITile: View {
    @Environment(\.colorScheme) private var colorScheme

    let label: String
    let value: String
    let unit: String?
    let delta: Delta
    let sparkline: [Double]

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(verbatim: label)
                .font(IterFont.sansLabel)
                .foregroundStyle(Color.iterTextSecondary(for: colorScheme))

            HStack(alignment: .firstTextBaseline, spacing: 6) {
                Text(verbatim: value)
                    .font(IterFont.mono(size: 22, weight: .medium))
                    .foregroundStyle(Color.iterTextPrimary(for: colorScheme))

                if let unit {
                    Text(verbatim: unit)
                        .font(IterFont.mono(size: 12))
                        .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
                }
            }

            Text(verbatim: delta.label)
                .font(IterFont.monoSmall)
                .foregroundStyle(delta.color(for: colorScheme))

            Sparkline(values: sparkline, tint: Color.iterAccent(for: colorScheme))
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color.iterPanel(for: colorScheme))
    }
}

struct KPIRow: View {
    @Environment(\.colorScheme) private var colorScheme

    let tiles: [KPITileData]

    var body: some View {
        Grid(horizontalSpacing: 1, verticalSpacing: 1) {
            GridRow {
                ForEach(tiles.prefix(4)) { tile in
                    KPITile(
                        label: tile.label,
                        value: tile.value,
                        unit: tile.unit,
                        delta: tile.delta,
                        sparkline: tile.sparkline
                    )
                }
            }
        }
        .background(Color.iterBorder(for: colorScheme))
        .clipShape(.rect(cornerRadius: IterRadius.standard))
        .overlay {
            RoundedRectangle(cornerRadius: IterRadius.standard)
                .stroke(Color.iterBorder(for: colorScheme), lineWidth: 1)
        }
    }
}

#Preview("KPI States") {
    KPIRow(tiles: [
        KPITileData(label: "sessions", value: "47", unit: nil, delta: .increase("+12%"), sparkline: [1, 4, 2, 6]),
        KPITileData(label: "acceptance", value: "68", unit: "%", delta: .flat("0%"), sparkline: [3, 3, 3, 3]),
        KPITileData(label: "avg score", value: "84", unit: nil, delta: .increase("+4"), sparkline: [2, 4, 5, 7]),
        KPITileData(label: "time saved", value: "12.5", unit: "h", delta: .decrease("-2%"), sparkline: [])
    ])
    .padding()
}
