import SwiftUI

struct TraceRow: View {
    @Environment(\.colorScheme) private var colorScheme
    @State private var isExpanded = false

    let time: String
    let kind: TraceKind
    let label: String
    let detail: String
    var children: [TraceChild] = []

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Grid(horizontalSpacing: 12, verticalSpacing: 0) {
                GridRow(alignment: .top) {
                    Text(verbatim: time)
                        .font(IterFont.monoLabel)
                        .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
                        .frame(width: 52, alignment: .leading)

                    Text(verbatim: kind.rawValue)
                        .font(IterFont.mono(size: 9))
                        .foregroundStyle(kind.foreground(for: colorScheme))
                        .padding(.horizontal, 3)
                        .frame(height: 16)
                        .background(kind.background(for: colorScheme))
                        .clipShape(.rect(cornerRadius: 3))
                        .frame(width: 20, alignment: .leading)

                    VStack(alignment: .leading, spacing: 2) {
                        Text(verbatim: label)
                            .font(IterFont.sans(size: 12, weight: .medium))
                            .foregroundStyle(Color.iterTextPrimary(for: colorScheme))

                        if kind == .subagent && !isExpanded {
                            Button {
                                isExpanded = true
                            } label: {
                                Text(verbatim: "\(children.count) tool calls · expand")
                                    .font(IterFont.monoLabel)
                                    .foregroundStyle(Color.iterWarn(for: colorScheme))
                            }
                            .buttonStyle(.plain)
                        } else {
                            Text(verbatim: detail)
                                .font(IterFont.monoLabel)
                                .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
                                .fixedSize(horizontal: false, vertical: true)
                        }
                    }
                }
            }
            .padding(.horizontal, 10)
            .padding(.vertical, 6)

            if kind == .subagent && isExpanded {
                VStack(alignment: .leading, spacing: 3) {
                    ForEach(children) { child in
                        Text(verbatim: "\(child.label) · \(child.detail)")
                            .font(IterFont.monoSmall)
                            .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
                    }
                }
                .padding(.leading, 94)
                .padding(.bottom, 6)
            }
        }
        .overlay(alignment: .bottom) {
            DashedDivider()
        }
    }
}

private struct DashedDivider: View {
    @Environment(\.colorScheme) private var colorScheme

    var body: some View {
        Rectangle()
            .stroke(style: StrokeStyle(lineWidth: 1, dash: [4, 3]))
            .foregroundStyle(Color.iterBorder(for: colorScheme))
            .frame(height: 1)
    }
}

#Preview("Trace Row States") {
    VStack(spacing: 0) {
        TraceRow(time: "00:00", kind: .start, label: "Session started", detail: "iter/mac · main")
        TraceRow(time: "00:08", kind: .prompt, label: "Prompt sent", detail: "Build dashboard components")
        TraceRow(
            time: "03:42",
            kind: .subagent,
            label: "SwiftUI worker",
            detail: "read_file · edit_file · xcodebuild",
            children: [
                TraceChild(label: "read_file", detail: "DESIGN.md"),
                TraceChild(label: "edit_file", detail: "Components/Score.swift"),
                TraceChild(label: "run_tests", detail: "xcodebuild")
            ]
        )
    }
    .padding()
}
