import SwiftUI

struct MenubarActions {
    let openDashboard: () -> Void
    let shareStack: () -> Void
    let openSettings: () -> Void
    let statusChanged: () -> Void
    let quit: () -> Void
}

struct MenubarDropdownView: View {
    @Environment(\.colorScheme) private var colorScheme
    @Bindable var daemonClient: DaemonClient

    let actions: MenubarActions

    var body: some View {
        VStack(spacing: 0) {
            header

            VStack(alignment: .leading, spacing: IterSpacing.gapSmall) {
                MenubarStatusRow(
                    title: activityTitle,
                    value: activityValue,
                    systemImage: activitySystemImage
                )
                MenubarStatusRow(
                    title: "Last session captured",
                    value: lastSessionValue,
                    systemImage: "clock.arrow.circlepath"
                )
            }
            .padding(.horizontal, IterSpacing.gapMedium)
            .padding(.bottom, IterSpacing.gapMedium)

            MenubarDividerLine()

            VStack(spacing: 2) {
                MenubarActionButton(title: "Open Dashboard", systemImage: "rectangle.grid.2x2") {
                    actions.openDashboard()
                }
                MenubarActionButton(title: "Share my stack", systemImage: "square.and.arrow.up") {
                    actions.shareStack()
                }
                MenubarActionButton(
                    title: daemonClient.status.paused ? "Resume capture" : "Pause capture",
                    systemImage: daemonClient.status.paused ? "play.fill" : "pause.fill",
                    isDisabled: !daemonClient.connected
                ) {
                    Task {
                        if daemonClient.status.paused {
                            await daemonClient.resume()
                        } else {
                            await daemonClient.pause()
                        }
                        actions.statusChanged()
                    }
                }
            }
            .padding(IterSpacing.gapSmall)

            MenubarDividerLine()

            MenubarActionButton(title: "Quit Iter", systemImage: "power", role: .destructive) {
                actions.quit()
            }
            .padding(.horizontal, IterSpacing.gapSmall)
            .padding(.vertical, 6)

            MenubarDividerLine()

            Button {
                actions.openSettings()
            } label: {
                HStack(spacing: IterSpacing.gapSmall) {
                    Image(systemName: "gearshape")
                        .font(.system(size: 12, weight: .medium))
                        .frame(width: 16)
                        .accessibilityHidden(true)

                    Text(verbatim: "Settings")
                        .font(IterFont.sansBody)

                    Spacer()

                    Image(systemName: "chevron.right")
                        .font(.system(size: 10, weight: .semibold))
                        .accessibilityHidden(true)
                }
                .frame(height: 34)
                .padding(.horizontal, IterSpacing.gapMedium)
                .contentShape(.rect)
            }
            .buttonStyle(.plain)
            .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
            .help("Open Settings")
        }
        .frame(width: 304)
        .background(Color.iterPanel(for: colorScheme))
    }

    private var header: some View {
        HStack(spacing: IterSpacing.gapSmall) {
            MenubarStatusDot(state: statusDotState)

            Text(verbatim: "Iter — \(statusTitle)")
                .font(IterFont.sansCardTitle)
                .foregroundStyle(Color.iterTextPrimary(for: colorScheme))

            Spacer()

            Text(verbatim: daemonClient.daemonVersion.isEmpty ? "--" : daemonClient.daemonVersion)
                .font(IterFont.monoTiny)
                .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
                .padding(.horizontal, 6)
                .frame(height: 18)
                .background(Color.iterSelected(for: colorScheme))
                .clipShape(.rect(cornerRadius: IterRadius.scoreChip))
        }
        .padding(IterSpacing.gapMedium)
        .accessibilityElement(children: .combine)
    }

    private var statusTitle: String {
        if !daemonClient.connected { return "disconnected" }
        return daemonClient.status.paused ? "paused" : "running"
    }

    private var statusDotState: MenubarStatusDot.State {
        if !daemonClient.connected { return .bad }
        return daemonClient.status.paused ? .warn : .good
    }

    private var activityTitle: String {
        activeTask == nil ? "Idle since" : "Currently ingesting"
    }

    private var activityValue: String {
        if !daemonClient.connected {
            return "daemon unavailable"
        }
        if let activeTask {
            return activeTask
        }
        if let idleSince = daemonClient.status.idleSince {
            return DaemonClient.relativeTime(from: idleSince)
        }
        return "launch"
    }

    private var activitySystemImage: String {
        activeTask == nil ? "pause.circle" : "arrow.down.circle"
    }

    private var activeTask: String? {
        guard let task = daemonClient.status.currentTask?.trimmingCharacters(in: .whitespacesAndNewlines),
              !task.isEmpty
        else {
            return nil
        }
        return task
    }

    private var lastSessionValue: String {
        guard daemonClient.connected else {
            return "daemon unavailable"
        }
        guard let lastSessionAt = daemonClient.status.lastSessionAt else {
            return "none yet"
        }
        return DaemonClient.relativeTime(from: lastSessionAt)
    }
}

private struct MenubarStatusRow: View {
    @Environment(\.colorScheme) private var colorScheme

    let title: String
    let value: String
    let systemImage: String

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: IterSpacing.gapSmall) {
            Image(systemName: systemImage)
                .font(.system(size: 12, weight: .medium))
                .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
                .frame(width: 16)
                .accessibilityHidden(true)

            VStack(alignment: .leading, spacing: 2) {
                Text(verbatim: title)
                    .font(IterFont.monoTiny)
                    .foregroundStyle(Color.iterTextTertiary(for: colorScheme))

                Text(verbatim: value)
                    .font(IterFont.sansBody)
                    .foregroundStyle(Color.iterTextPrimary(for: colorScheme))
                    .lineLimit(1)
            }

            Spacer(minLength: 0)
        }
        .frame(height: 42)
        .accessibilityElement(children: .combine)
    }
}

private struct MenubarActionButton: View {
    @Environment(\.colorScheme) private var colorScheme

    let title: String
    let systemImage: String
    var role: ButtonRole?
    var isDisabled = false
    let action: () -> Void

    var body: some View {
        Button(role: role, action: action) {
            HStack(spacing: IterSpacing.gapSmall) {
                Image(systemName: systemImage)
                    .font(.system(size: 12, weight: .medium))
                    .frame(width: 16)
                    .accessibilityHidden(true)

                Text(verbatim: title)
                    .font(IterFont.sansBody)

                Spacer()
            }
            .frame(height: 32)
            .padding(.horizontal, IterSpacing.gapSmall)
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .foregroundStyle(foregroundColor)
        .background(Color.clear)
        .clipShape(.rect(cornerRadius: IterRadius.navItem))
        .disabled(isDisabled)
        .opacity(isDisabled ? 0.45 : 1)
        .help(title)
    }

    private var foregroundColor: Color {
        if role == .destructive {
            return Color.iterBad(for: colorScheme)
        }
        return Color.iterTextSecondary(for: colorScheme)
    }
}

private struct MenubarStatusDot: View {
    @Environment(\.colorScheme) private var colorScheme

    enum State {
        case good
        case warn
        case bad
    }

    let state: State

    var body: some View {
        Circle()
            .fill(fillColor)
            .frame(width: 8, height: 8)
            .overlay {
                Circle()
                    .stroke(haloColor, lineWidth: 5)
            }
            .accessibilityHidden(true)
    }

    private var fillColor: Color {
        switch state {
        case .good:
            return Color.iterGood(for: colorScheme)
        case .warn:
            return Color.iterWarn(for: colorScheme)
        case .bad:
            return Color.iterBad(for: colorScheme)
        }
    }

    private var haloColor: Color {
        switch state {
        case .good:
            return Color.iterGoodSoft(for: colorScheme)
        case .warn:
            return Color.iterWarnSoft(for: colorScheme)
        case .bad:
            return Color.iterBadSoft(for: colorScheme)
        }
    }
}

private struct MenubarDividerLine: View {
    @Environment(\.colorScheme) private var colorScheme

    var body: some View {
        Rectangle()
            .fill(Color.iterBorder(for: colorScheme))
            .frame(height: 1)
    }
}

#Preview("Menubar") {
    MenubarDropdownView(
        daemonClient: DaemonClient(),
        actions: MenubarActions(
            openDashboard: {},
            shareStack: {},
            openSettings: {},
            statusChanged: {},
            quit: {}
        )
    )
    .environment(WorkspaceRouter())
    .preferredColorScheme(.light)
}
