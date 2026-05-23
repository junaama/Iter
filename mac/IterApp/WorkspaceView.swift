import AppKit
import SwiftUI
// swiftlint:disable file_length

enum Route: Hashable {
    case me // swiftlint:disable:this identifier_name
    case team
    case sessions
    case stack
    case stackSimulate(userId: UUID)
    case settings
    case sessionDetail(id: String)

    var title: String {
        switch self {
        case .me:
            return "Me"
        case .team:
            return "Team"
        case .sessions:
            return "Sessions"
        case .stack:
            return "Stack"
        case .stackSimulate:
            return "Stack"
        case .settings:
            return "Settings"
        case .sessionDetail:
            return "Session"
        }
    }

    var breadcrumb: String {
        switch self {
        case .stackSimulate(let userID):
            return "Workspace / Stack / \(userID.uuidString.prefix(8))"
        case .sessionDetail(let id):
            return "Me / sessions / \(id)"
        case .settings:
            return "Workspace / Settings"
        default:
            return "Workspace / \(title)"
        }
    }

    var showsRail: Bool {
        switch self {
        case .sessionDetail, .stackSimulate, .settings:
            return false
        default:
            return true
        }
    }

    var railWidth: CGFloat {
        switch self {
        case .team:
            return IterSpacing.railWidthTeam
        case .sessionDetail:
            return IterSpacing.railWidthSession
        default:
            return IterSpacing.railWidthMe
        }
    }

    var detailBackRoute: Route {
        switch self {
        case .me, .team, .sessions:
            return self
        default:
            return .sessions
        }
    }

    func matchesTopLevel(_ candidate: Route) -> Bool {
        switch (self, candidate) {
        case (.me, .me), (.team, .team), (.sessions, .sessions), (.stack, .stack), (.stackSimulate, .stack),
            (.settings, .settings):
            return true
        default:
            return false
        }
    }
}

enum LayoutVariant: String, CaseIterable, Identifiable {
    case table
    case cards
    case feed

    var id: String { rawValue }

    var title: String {
        switch self {
        case .table:
            return "Table"
        case .cards:
            return "Cards"
        case .feed:
            return "Feed"
        }
    }
}

@Observable
final class LayoutVariantStore {
    private enum Storage {
        static let key = "dev.iter.dashboard.layoutVariant"
    }

    private let defaults: UserDefaults

    var selected: LayoutVariant {
        didSet {
            defaults.set(selected.rawValue, forKey: Storage.key)
        }
    }

    init(defaults: UserDefaults = .standard) {
        self.defaults = defaults
        selected = defaults.string(forKey: Storage.key).flatMap(LayoutVariant.init(rawValue:)) ?? .table
    }
}

struct WorkspaceView: View {
    @Environment(\.colorScheme) private var colorScheme
    @Environment(DaemonClient.self) private var daemonClient
    @Environment(WorkspaceRouter.self) private var router

    @State private var layoutStore = LayoutVariantStore()
    @State private var dashboardMeStore = DashboardMeStore()
    @State private var dashboardTeamStore = DashboardTeamStore()
    @State private var stackStore = StackStore()
    @State private var previousListRoute: Route = .me
    @State private var searchText = ""
    @State private var showsSearchPopover = false
    @FocusState private var searchFocused: Bool

    var body: some View {
        @Bindable var router = router

        ZStack {
            Color.iterStageBackdrop(for: colorScheme)
                .ignoresSafeArea()

            VStack(spacing: 0) {
                TitlebarView(
                    route: router.route,
                    searchText: $searchText,
                    showsSearchPopover: $showsSearchPopover,
                    searchFocused: $searchFocused
                )

                HStack(spacing: 0) {
                    SidebarView(
                        route: $router.route,
                        stackStore: stackStore,
                        dashboardMeStore: dashboardMeStore
                    ) { route in
                        if case .sessionDetail = route {
                            previousListRoute = router.route.detailBackRoute
                        }
                        router.route = route
                    }

                    VStack(spacing: 0) {
                        SubbarView(
                            layoutStore: layoutStore,
                            route: $router.route,
                            previousListRoute: previousListRoute,
                            isDashboardRefreshing: dashboardRefreshing
                        ) {
                            Task { await refreshDashboard() }
                        }

                        HStack(spacing: 0) {
                            MainPaneView(
                                route: router.route,
                                layoutVariant: layoutStore.selected,
                                dashboardMeStore: dashboardMeStore,
                                dashboardTeamStore: dashboardTeamStore,
                                stackStore: stackStore
                            ) { route in
                                if case .sessionDetail = route {
                                    previousListRoute = router.route.detailBackRoute
                                }
                                router.route = route
                            }

                            if router.route.showsRail {
                                RightRailView(
                                    route: router.route,
                                    dashboard: dashboardMeStore.dashboard,
                                    teamDashboard: dashboardTeamStore.dashboard,
                                    stackStore: stackStore
                                ) { route in
                                    router.route = route
                                }
                                    .frame(width: router.route.railWidth)
                            }
                        }
                    }
                    .background(Color.iterPanel(for: colorScheme))
                }
            }
            .frame(
                maxWidth: IterSpacing.windowMaxWidth,
                maxHeight: IterSpacing.windowMaxHeight
            )
            .clipShape(.rect(cornerRadius: IterRadius.standard))
            .shadow(color: .rgba(20, 18, 14, colorScheme == .dark ? 0.32 : 0.18), radius: 32, x: 0, y: 24)
            .shadow(color: .rgba(20, 18, 14, colorScheme == .dark ? 0.22 : 0.08), radius: 4, x: 0, y: 2)

            Button("Focus Search") {
                searchFocused = true
                showsSearchPopover = true
            }
            .keyboardShortcut("k", modifiers: .command)
            .frame(width: 0, height: 0)
            .opacity(0)
            .accessibilityHidden(true)
        }
        .frame(minWidth: 980, minHeight: 620)
        .onChange(of: searchFocused) { _, focused in
            if focused {
                showsSearchPopover = true
            }
        }
        .sheet(isPresented: $router.showsStackShareSheet) {
            StackShareSheet(stackStore: stackStore)
        }
        .alert("Iter daemon out of date", isPresented: Bindable(daemonClient).versionMismatch) {
            Button("Quit and relaunch") {
                NSWorkspace.shared.open(Bundle.main.bundleURL)
                NSApp.terminate(nil)
            }
            Button("Dismiss", role: .cancel) {}
        } message: {
            Text("Install update to continue.")
        }
    }

    private var dashboardRefreshing: Bool {
        router.route.matchesTopLevel(.team) ? dashboardTeamStore.isLoading : dashboardMeStore.isLoading
    }

    private func refreshDashboard() async {
        if router.route.matchesTopLevel(.team) {
            await dashboardTeamStore.load(forceRefresh: true)
        } else {
            await dashboardMeStore.load(forceRefresh: true)
        }
    }
}

private struct TitlebarView: View {
    @Environment(\.colorScheme) private var colorScheme

    let route: Route
    @Binding var searchText: String
    @Binding var showsSearchPopover: Bool
    var searchFocused: FocusState<Bool>.Binding

    var body: some View {
        ZStack {
            HStack {
                TrafficLightsView()
                Spacer()
                TitlebarActionsView(
                    searchText: $searchText,
                    showsSearchPopover: $showsSearchPopover,
                    searchFocused: searchFocused
                )
            }

            Text(verbatim: "iter — \(route.title)")
                .font(IterFont.monoTitle)
                .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
                .accessibilityLabel(Text("iter \(route.title)"))
        }
        .frame(height: IterSpacing.titlebarHeight)
        .padding(.horizontal, IterSpacing.gapMedium)
        .background(Color.iterSidebar(for: colorScheme))
        .overlay(alignment: .bottom) {
            DividerLine()
        }
    }
}

private struct TrafficLightsView: View {
    var body: some View {
        HStack(spacing: 7) {
            Circle()
                .fill(Color.hex(0xFF5F57))
            Circle()
                .fill(Color.hex(0xFEBC2E))
            Circle()
                .fill(Color.hex(0x28C840))
        }
        .frame(width: 54, height: 12)
        .accessibilityHidden(true)
    }
}

private struct TitlebarActionsView: View {
    @Environment(\.colorScheme) private var colorScheme
    @Environment(ThemeStore.self) private var themeStore

    @Binding var searchText: String
    @Binding var showsSearchPopover: Bool
    var searchFocused: FocusState<Bool>.Binding

    var body: some View {
        HStack(spacing: IterSpacing.gapSmall) {
            StatusPillView(label: "daemon idle")

            TitlebarSearchField(
                searchText: $searchText,
                showsSearchPopover: $showsSearchPopover,
                searchFocused: searchFocused
            )

            Button {
                themeStore.toggleTheme()
            } label: {
                Image(systemName: themeStore.theme == .light ? "moon" : "sun.max")
                    .font(.system(size: 12, weight: .medium))
                    .frame(width: 26, height: 26)
                    .contentShape(.rect)
            }
            .buttonStyle(.plain)
            .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
            .background(Color.iterPanel(for: colorScheme))
            .clipShape(.rect(cornerRadius: IterRadius.button))
            .overlay {
                RoundedRectangle(cornerRadius: IterRadius.button)
                    .stroke(Color.iterBorder(for: colorScheme), lineWidth: 1)
            }
            .accessibilityLabel(themeStore.toggleTitle)
        }
    }
}

private struct TitlebarSearchField: View {
    @Environment(\.colorScheme) private var colorScheme

    @Binding var searchText: String
    @Binding var showsSearchPopover: Bool
    var searchFocused: FocusState<Bool>.Binding

    var body: some View {
        HStack(spacing: IterSpacing.gapSmall) {
            Image(systemName: "magnifyingglass")
                .font(.system(size: 11, weight: .medium))
                .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
                .accessibilityHidden(true)

            TextField("Search", text: $searchText, prompt: Text("Search"))
                .textFieldStyle(.plain)
                .font(IterFont.monoLabel)
                .foregroundStyle(Color.iterTextPrimary(for: colorScheme))
                .focused(searchFocused)
                .onSubmit {
                    showsSearchPopover = true
                }

            Text(verbatim: "⌘K")
                .font(IterFont.monoTiny)
                .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
                .padding(.horizontal, 4)
                .frame(height: 16)
                .background(Color.iterSelected(for: colorScheme))
                .clipShape(.rect(cornerRadius: IterRadius.searchHint))
                .accessibilityHidden(true)
        }
        .frame(width: 220, height: 26)
        .padding(.horizontal, IterSpacing.gapSmall)
        .background(Color.iterPanel(for: colorScheme))
        .clipShape(.rect(cornerRadius: IterRadius.button))
        .overlay {
            RoundedRectangle(cornerRadius: IterRadius.button)
                .stroke(
                    searchFocused.wrappedValue
                        ? Color.iterBorderStrong(for: colorScheme)
                        : Color.iterBorder(for: colorScheme),
                    lineWidth: 1
                )
        }
        .popover(isPresented: $showsSearchPopover, arrowEdge: .top) {
            EmptySearchPopover()
        }
    }
}

private struct EmptySearchPopover: View {
    @Environment(\.colorScheme) private var colorScheme

    var body: some View {
        VStack(alignment: .leading, spacing: IterSpacing.gapSmall) {
            Text(verbatim: "Command palette")
                .font(IterFont.sansSectionTitle)
                .foregroundStyle(Color.iterTextPrimary(for: colorScheme))

            Text(verbatim: "No commands wired yet")
                .font(IterFont.monoSmall)
                .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
        }
        .padding(IterSpacing.gapMedium)
        .frame(width: 220, alignment: .leading)
        .background(Color.iterPanel(for: colorScheme))
    }
}

private struct SidebarView: View {
    @Environment(\.colorScheme) private var colorScheme
    @Environment(DaemonClient.self) private var daemonClient
    @Binding var route: Route
    let stackStore: StackStore
    let dashboardMeStore: DashboardMeStore
    let onNavigate: (Route) -> Void

    private let navItems: [SidebarNavItem] = [
        SidebarNavItem(route: .me, title: "Me", symbol: "person", count: nil),
        SidebarNavItem(route: .team, title: "Team", symbol: "person.2", count: nil),
        SidebarNavItem(route: .sessions, title: "Sessions", symbol: "rectangle.stack", count: nil),
        SidebarNavItem(route: .stack, title: "Stack", symbol: "square.stack.3d.up", count: nil),
        SidebarNavItem(route: .settings, title: "Settings", symbol: "gearshape", count: nil)
    ]

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            WorkspaceSwitcherView()
                .padding(.horizontal, IterSpacing.gapSmall)
                .padding(.top, IterSpacing.gapSmall)
                .padding(.bottom, IterSpacing.gapMedium)

            VStack(spacing: 2) {
                ForEach(navItems) { item in
                    SidebarNavButton(item: item, isActive: route.matchesTopLevel(item.route)) {
                        onNavigate(item.route)
                    }
                }
            }
            .padding(.horizontal, 6)

            SidebarSectionTitle(title: "Active stack", action: "edit")
                .padding(.top, IterSpacing.gapLarge)

            FlowPillsView(titles: stackStore.sidebarHarnessLabels)
                .padding(.horizontal, IterSpacing.gapSmall)
                .padding(.top, IterSpacing.gapSmall)

            let recentSessions = dashboardMeStore.dashboard?.recentSessions ?? []
            if !recentSessions.isEmpty {
                SidebarSectionTitle(title: "Recent sessions", action: nil)
                    .padding(.top, IterSpacing.gapLarge)

                VStack(spacing: 2) {
                    ForEach(recentSessions.prefix(5), id: \.id) { session in
                        RecentSessionButton(
                            title: SidebarView.sessionTitle(for: session),
                            tint: SidebarView.tint(for: session.harness)
                        ) {
                            onNavigate(.sessionDetail(id: session.id))
                        }
                    }
                }
                .padding(.horizontal, 6)
                .padding(.top, 6)
            }

            Spacer(minLength: IterSpacing.gapLarge)

            SidebarFooterView(daemonClient: daemonClient)
        }
        .frame(width: IterSpacing.sidebarWidth)
        .background(Color.iterSidebar(for: colorScheme))
        .overlay(alignment: .trailing) {
            DividerLine(axis: .vertical)
        }
    }

    private static func sessionTitle(for session: DashboardRecentSession) -> String {
        let trimmed = session.redactedPromptPreview.trimmingCharacters(in: .whitespacesAndNewlines)
        if trimmed.isEmpty { return "Session \(session.id.prefix(8))" }
        let firstLine = trimmed.split(separator: "\n").first.map(String.init) ?? trimmed
        return firstLine.count > 48 ? String(firstLine.prefix(48)) + "…" : firstLine
    }

    private static func tint(for harness: String) -> IterHarnessTint {
        (HarnessID(rawValue: harness) ?? .codex).tint
    }
}

private struct WorkspaceSwitcherView: View {
    @Environment(\.colorScheme) private var colorScheme
    @Environment(SessionStore.self) private var sessionStore

    var body: some View {
        HStack(spacing: IterSpacing.gapSmall) {
            Text(verbatim: "i")
                .font(IterFont.monoAvatar)
                .foregroundStyle(Color.white)
                .frame(width: 22, height: 22)
                .background(Color.iterAccent(for: colorScheme))
                .clipShape(.rect(cornerRadius: IterRadius.avatar))

            VStack(alignment: .leading, spacing: 2) {
                Text(verbatim: "iter · core")
                    .font(IterFont.sansCardTitle)
                    .foregroundStyle(Color.iterTextPrimary(for: colorScheme))

                Text(verbatim: sessionStore.displayName ?? sessionStore.userId ?? "signed in")
                    .font(IterFont.monoSmall)
                    .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
                    .lineLimit(1)
            }

            Spacer()

            Button {
                sessionStore.signOut()
            } label: {
                Image(systemName: "rectangle.portrait.and.arrow.right")
                    .font(.system(size: 11, weight: .semibold))
                    .frame(width: 22, height: 22)
                    .contentShape(.rect)
            }
            .buttonStyle(.plain)
            .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
            .accessibilityLabel("Sign out")
        }
        .frame(height: 36)
    }
}

private struct SidebarNavItem: Identifiable {
    let route: Route
    let title: String
    let symbol: String
    let count: String?

    var id: String { title }
}

private struct SidebarNavButton: View {
    @Environment(\.colorScheme) private var colorScheme

    let item: SidebarNavItem
    let isActive: Bool
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: IterSpacing.gapSmall) {
                Image(systemName: item.symbol)
                    .font(.system(size: 12, weight: .medium))
                    .foregroundStyle(
                        isActive ? Color.iterAccent(for: colorScheme) : Color.iterTextTertiary(for: colorScheme)
                    )
                    .frame(width: 16)
                    .accessibilityHidden(true)

                Text(verbatim: item.title)
                    .font(IterFont.sans(size: 12.5, weight: isActive ? .medium : .regular))
                    .foregroundStyle(
                        isActive ? Color.iterTextPrimary(for: colorScheme) : Color.iterTextSecondary(for: colorScheme)
                    )

                Spacer()

                if let count = item.count {
                    Text(verbatim: count)
                        .font(IterFont.monoTiny)
                        .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
                }
            }
            .frame(height: IterSpacing.rowHeight)
            .padding(.horizontal, IterSpacing.gapSmall)
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .background(isActive ? Color.iterSelected(for: colorScheme) : Color.clear)
        .clipShape(.rect(cornerRadius: IterRadius.navItem))
        .accessibilityAddTraits(isActive ? .isSelected : [])
    }
}

private struct SidebarSectionTitle: View {
    @Environment(\.colorScheme) private var colorScheme

    let title: String
    let action: String?

    var body: some View {
        HStack {
            Text(verbatim: title)
                .font(IterFont.sansSectionTitle)
                .foregroundStyle(Color.iterTextTertiary(for: colorScheme))

            Spacer()

            if let action {
                Text(verbatim: action)
                    .font(IterFont.monoTiny)
                    .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
            }
        }
        .padding(.horizontal, IterSpacing.gapMedium)
    }
}

private struct FlowPillsView: View {
    @Environment(\.colorScheme) private var colorScheme

    let titles: [String]

    var body: some View {
        HStack(spacing: IterSpacing.gapSmall) {
            ForEach(titles, id: \.self) { title in
                Text(verbatim: title)
                    .font(IterFont.monoSmall)
                    .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
                    .padding(.horizontal, 7)
                    .frame(height: 24)
                    .background(Color.iterPanel(for: colorScheme))
                    .clipShape(.rect(cornerRadius: IterRadius.pill))
                    .overlay {
                        RoundedRectangle(cornerRadius: IterRadius.pill)
                            .stroke(Color.iterBorder(for: colorScheme), lineWidth: 1)
                    }
            }
        }
    }
}

private struct RecentSessionButton: View {
    @Environment(\.colorScheme) private var colorScheme

    let title: String
    let tint: IterHarnessTint
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: IterSpacing.gapSmall) {
                RoundedRectangle(cornerRadius: IterRadius.harnessSwatch)
                    .fill(tint.color)
                    .frame(width: 6, height: 6)

                Text(verbatim: title)
                    .font(IterFont.sansSmall)
                    .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
                    .lineLimit(1)

                Spacer()
            }
            .frame(height: 26)
            .padding(.horizontal, IterSpacing.gapSmall)
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .clipShape(.rect(cornerRadius: IterRadius.navItem))
    }
}

private struct SidebarFooterView: View {
    @Environment(\.colorScheme) private var colorScheme
    @Bindable var daemonClient: DaemonClient

    var body: some View {
        VStack(alignment: .leading, spacing: IterSpacing.gapSmall) {
            HStack {
                HStack(spacing: 6) {
                    PulseDotView(state: dotState)
                    Text(verbatim: daemonClient.footerLabel)
                }

                Spacer()

	                Button {
                    Task {
                        if daemonClient.status.paused {
                            await daemonClient.resume()
                        } else {
                            await daemonClient.pause()
                        }
                    }
                } label: {
                    Image(systemName: daemonClient.status.paused ? "play.fill" : "pause.fill")
                        .font(.system(size: 9, weight: .semibold))
                        .frame(width: 20, height: 20)
                        .contentShape(.rect)
                }
                .buttonStyle(.plain)
                .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
                .disabled(!daemonClient.connected)
                .accessibilityLabel(daemonClient.status.paused ? "Resume daemon" : "Pause daemon")
            }

            HStack {
                Text(verbatim: daemonClient.footerDetail)
                Spacer()
                Text(verbatim: daemonClient.daemonVersion.isEmpty ? "--" : daemonClient.daemonVersion)
            }
        }
        .font(IterFont.monoSmall)
        .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
        .padding(IterSpacing.gapMedium)
        .overlay(alignment: .top) {
            DividerLine()
        }
        .accessibilityElement(children: .combine)
    }

    private var dotState: PulseDotView.State {
        if !daemonClient.connected { return .bad }
        return daemonClient.status.paused ? .warn : .good
    }
}

private struct SubbarView: View {
    @Environment(\.colorScheme) private var colorScheme
    @Bindable var layoutStore: LayoutVariantStore

    @Binding var route: Route
    let previousListRoute: Route
    let isDashboardRefreshing: Bool
    let onRefreshDashboard: () -> Void

    var body: some View {
        HStack(spacing: IterSpacing.gapLarge) {
            if case .sessionDetail = route {
                Button {
                    route = previousListRoute
                } label: {
                    HStack(spacing: 5) {
                        Image(systemName: "chevron.left")
                            .font(.system(size: 10, weight: .semibold))
                            .accessibilityHidden(true)
                        Text(verbatim: previousListRoute.title)
                            .font(IterFont.monoSmall)
                    }
                    .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
                }
                .buttonStyle(.plain)

                Text(verbatim: route.breadcrumb)
                    .font(IterFont.monoSmall)
                    .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
                    .lineLimit(1)

                Spacer()
            } else if route.matchesTopLevel(.settings) {
                Text(verbatim: route.breadcrumb)
                    .font(IterFont.monoSmall)
                    .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
                    .lineLimit(1)

                Spacer()
            } else {
                Text(verbatim: route.breadcrumb)
                    .font(IterFont.monoSmall)
                    .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
                    .lineLimit(1)

                HStack(spacing: 2) {
                    TabButton(title: "Me", isActive: route.matchesTopLevel(.me))
                    TabButton(title: "Team", isActive: route.matchesTopLevel(.team))
                    TabButton(title: "Sessions", isActive: route.matchesTopLevel(.sessions))
                }

                Spacer()

                if route.matchesTopLevel(.me) {
                    DashboardRefreshButton(isRefreshing: isDashboardRefreshing, action: onRefreshDashboard)
                }

                LayoutSegmentedControl(selection: $layoutStore.selected)
            }
        }
        .frame(height: IterSpacing.subbarHeight)
        .padding(.horizontal, IterSpacing.gapMedium)
        .background(Color.iterPanel(for: colorScheme))
        .overlay(alignment: .bottom) {
            DividerLine()
        }
    }
}

private struct DashboardRefreshButton: View {
    @Environment(\.colorScheme) private var colorScheme

    let isRefreshing: Bool
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 5) {
                Image(systemName: "arrow.clockwise")
                    .font(.system(size: 10.5, weight: .semibold))
                    .accessibilityHidden(true)

                Text(verbatim: isRefreshing ? "Refreshing" : "Refresh")
                    .font(IterFont.sansLabel)

                KBD(text: "⌘R")
            }
            .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
            .padding(.horizontal, 8)
            .frame(height: 22)
            .background(Color.iterPanel(for: colorScheme))
            .clipShape(.rect(cornerRadius: IterRadius.button))
            .overlay {
                RoundedRectangle(cornerRadius: IterRadius.button)
                    .stroke(Color.iterBorder(for: colorScheme), lineWidth: 1)
            }
        }
        .buttonStyle(.plain)
        .disabled(isRefreshing)
        .keyboardShortcut("r", modifiers: .command)
        .accessibilityLabel("Refresh dashboard")
    }
}

private struct TabButton: View {
    @Environment(\.colorScheme) private var colorScheme

    let title: String
    let isActive: Bool

    var body: some View {
        Text(verbatim: title)
            .font(IterFont.sans(size: 12, weight: isActive ? .medium : .regular))
            .foregroundStyle(
                isActive ? Color.iterTextPrimary(for: colorScheme) : Color.iterTextTertiary(for: colorScheme)
            )
            .padding(.horizontal, 8)
            .frame(height: 24)
            .background(isActive ? Color.iterSelected(for: colorScheme) : Color.clear)
            .clipShape(.rect(cornerRadius: IterRadius.segment))
    }
}

private struct LayoutSegmentedControl: View {
    @Environment(\.colorScheme) private var colorScheme
    @Binding var selection: LayoutVariant

    var body: some View {
        HStack(spacing: 0) {
            ForEach(LayoutVariant.allCases) { variant in
                Button {
                    selection = variant
                } label: {
                    Text(verbatim: variant.title)
                        .font(IterFont.monoLabel)
                        .foregroundStyle(
                            selection == variant
                                ? Color.iterTextPrimary(for: colorScheme)
                                : Color.iterTextTertiary(for: colorScheme)
                        )
                        .frame(width: 56, height: 26)
                        .background(selection == variant ? Color.iterSelected(for: colorScheme) : Color.clear)
                }
                .buttonStyle(.plain)

                if variant != LayoutVariant.allCases.last {
                    DividerLine(axis: .vertical)
                }
            }
        }
        .clipShape(.rect(cornerRadius: IterRadius.segment))
        .overlay {
            RoundedRectangle(cornerRadius: IterRadius.segment)
                .stroke(Color.iterBorder(for: colorScheme), lineWidth: 1)
        }
        .accessibilityElement(children: .contain)
        .accessibilityLabel("Layout variant")
    }
}

private struct MainPaneView: View {
    @Environment(\.colorScheme) private var colorScheme

    let route: Route
    let layoutVariant: LayoutVariant
    let dashboardMeStore: DashboardMeStore
    let dashboardTeamStore: DashboardTeamStore
    let stackStore: StackStore
    let onNavigate: (Route) -> Void

    var body: some View {
        if case .sessionDetail(let id) = route {
            SessionDetailView(sessionID: id)
        } else if case .stackSimulate(let userID) = route {
            StackSimulateView(userID: userID)
        } else if route.matchesTopLevel(.me) {
            DashboardMeView(
                store: dashboardMeStore,
                layoutVariant: layoutVariant,
                onSelectSession: { id in onNavigate(.sessionDetail(id: id)) },
                onViewAll: { onNavigate(.sessions) }
            )
        } else if route.matchesTopLevel(.team) {
            DashboardTeamView(
                store: dashboardTeamStore,
                layoutVariant: layoutVariant,
                onSelectSession: { id in onNavigate(.sessionDetail(id: id)) },
                onViewAll: { onNavigate(.sessions) }
            )
        } else if route.matchesTopLevel(.stack) {
            StackMeView(store: stackStore)
        } else if route.matchesTopLevel(.settings) {
            SettingsView(dashboard: dashboardMeStore.dashboard)
        } else {
            notImplementedView
        }
    }

    private var notImplementedView: some View {
        VStack(alignment: .leading, spacing: IterSpacing.gapMedium) {
            Text(verbatim: route.title)
                .font(IterFont.sansKPIValue)
                .foregroundStyle(Color.iterTextPrimary(for: colorScheme))

            Text(verbatim: "Not implemented yet.")
                .font(IterFont.sansBody)
                .foregroundStyle(Color.iterTextSecondary(for: colorScheme))

            Spacer()
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .padding(IterSpacing.mainPanePadding)
        .background(Color.iterPanel(for: colorScheme))
    }
}

private struct StackShareSheet: View {
    @Environment(\.colorScheme) private var colorScheme
    @Environment(WorkspaceRouter.self) private var router

    let stackStore: StackStore

    var body: some View {
        let stack = stackStore.stack
        let harnesses = stack.harnesses.map(\.code).joined(separator: ", ")
        let skills = stack.skills.map(\.name).joined(separator: ", ")
        let docs = stack.docs.map(\.value).joined(separator: ", ")

        return VStack(alignment: .leading, spacing: IterSpacing.gapMedium) {
            HStack {
                Text(verbatim: "Share stack")
                    .font(IterFont.sansKPIValue)
                    .foregroundStyle(Color.iterTextPrimary(for: colorScheme))

                Spacer()

                Button {
                    router.showsStackShareSheet = false
                } label: {
                    Image(systemName: "xmark")
                        .font(.system(size: 11, weight: .semibold))
                        .frame(width: 28, height: 28)
                        .contentShape(.rect)
                }
                .buttonStyle(.plain)
                .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
                .help("Close")
                .accessibilityLabel("Close share stack")
            }

            if harnesses.isEmpty && skills.isEmpty && docs.isEmpty {
                Text(verbatim: "Your stack is empty. Add harnesses, skills, or docs first.")
                    .font(IterFont.sansBody)
                    .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
            } else {
                VStack(alignment: .leading, spacing: IterSpacing.gapSmall) {
                    if !harnesses.isEmpty {
                        StackShareRow(label: "Harnesses", value: harnesses)
                    }
                    if !skills.isEmpty {
                        StackShareRow(label: "Skills", value: skills)
                    }
                    if !docs.isEmpty {
                        StackShareRow(label: "Docs", value: docs)
                    }
                }

                HStack {
                    Spacer()

                    ShareLink(item: "Iter stack: \(harnesses)") {
                        Label("Share", systemImage: "square.and.arrow.up")
                    }
                    .buttonStyle(.borderedProminent)
                }
            }
        }
        .padding(IterSpacing.gapLarge)
        .frame(width: 420)
        .background(Color.iterPanel(for: colorScheme))
    }
}

private struct StackShareRow: View {
    @Environment(\.colorScheme) private var colorScheme

    let label: String
    let value: String

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: IterSpacing.gapMedium) {
            Text(verbatim: label)
                .font(IterFont.monoLabel)
                .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
                .frame(width: 72, alignment: .leading)

            Text(verbatim: value)
                .font(IterFont.sansBody)
                .foregroundStyle(Color.iterTextPrimary(for: colorScheme))
        }
    }
}

private struct RightRailView: View {
    @Environment(\.colorScheme) private var colorScheme

    let route: Route
    let dashboard: DashboardMeResponse?
    let stackStore: StackStore
    let onNavigate: (Route) -> Void

    var body: some View {
        if route.matchesTopLevel(.stack) {
            StackRightRailView(store: stackStore) { userID in
                onNavigate(.stackSimulate(userId: userID))
            }
        } else {
            VStack(alignment: .leading, spacing: IterSpacing.gapMedium) {
                if route.matchesTopLevel(.me) {
                    MeRailCards(dashboard: dashboard, stackStore: stackStore)
                }
                Spacer()
            }
            .padding(IterSpacing.gapMedium)
            .frame(maxHeight: .infinity, alignment: .topLeading)
            .background(Color.iterRail(for: colorScheme))
            .overlay(alignment: .leading) {
                DividerLine(axis: .vertical)
            }
        }
    }
}

private struct MeRailCards: View {
    let dashboard: DashboardMeResponse?
    let stackStore: StackStore

    var body: some View {
        let stack = stackStore.stack
        let harnesses = stack.harnesses.map(\.code)
        if !harnesses.isEmpty {
            RailCard(
                title: "Active stack",
                count: "\(harnesses.count)",
                items: [
                    RailItem(
                        title: harnesses.joined(separator: " · "),
                        metadata: stackMetadata(skillsCount: stack.skills.count, docsCount: stack.docs.count),
                        primaryAction: nil,
                        secondaryAction: nil
                    )
                ]
            )
        }
    }

    private func stackMetadata(skillsCount: Int, docsCount: Int) -> String {
        var parts: [String] = []
        if skillsCount > 0 { parts.append("\(skillsCount) skill\(skillsCount == 1 ? "" : "s")") }
        if docsCount > 0 { parts.append("\(docsCount) doc\(docsCount == 1 ? "" : "s")") }
        return parts.joined(separator: " · ")
    }
}

private struct RailCardView: View {
    @Environment(\.colorScheme) private var colorScheme

    let title: String
    let count: String
    let bodyText: String

    var body: some View {
        VStack(alignment: .leading, spacing: IterSpacing.gapSmall) {
            HStack {
                Text(verbatim: title)
                    .font(IterFont.sansSectionTitle)
                    .foregroundStyle(Color.iterTextPrimary(for: colorScheme))

                Spacer()

                Text(verbatim: count)
                    .font(IterFont.monoTiny)
                    .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
                    .padding(.horizontal, 5)
                    .frame(height: 18)
                    .background(Color.iterSelected(for: colorScheme))
                    .clipShape(.rect(cornerRadius: IterRadius.scoreChip))
            }

            Text(verbatim: bodyText)
                .font(IterFont.sansSmall)
                .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
                .fixedSize(horizontal: false, vertical: true)
        }
        .padding(IterSpacing.gapMedium)
        .background(Color.iterPanel(for: colorScheme))
        .clipShape(.rect(cornerRadius: IterRadius.card))
        .overlay {
            RoundedRectangle(cornerRadius: IterRadius.card)
                .stroke(Color.iterBorder(for: colorScheme), lineWidth: 1)
        }
    }
}

private struct StatusPillView: View {
    @Environment(\.colorScheme) private var colorScheme

    let label: String

    var body: some View {
        HStack(spacing: 6) {
            Circle()
                .fill(Color.iterGood(for: colorScheme))
                .frame(width: 6, height: 6)
                .accessibilityHidden(true)

            Text(verbatim: label)
                .font(IterFont.monoLabel)
                .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
        }
        .padding(.horizontal, 8)
        .frame(height: 26)
        .background(Color.iterPanel(for: colorScheme))
        .clipShape(.rect(cornerRadius: IterRadius.pill))
        .overlay {
            RoundedRectangle(cornerRadius: IterRadius.pill)
                .stroke(Color.iterBorder(for: colorScheme), lineWidth: 1)
        }
        .accessibilityElement(children: .combine)
    }
}

private struct PulseDotView: View {
    @Environment(\.colorScheme) private var colorScheme

    enum State {
        case good
        case warn
        case bad
    }

    var state: State = .good

    var body: some View {
        Circle()
            .fill(fillColor)
            .frame(width: 7, height: 7)
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

private struct DividerLine: View {
    @Environment(\.colorScheme) private var colorScheme

    enum Axis {
        case horizontal
        case vertical
    }

    var axis: Axis = .horizontal

    var body: some View {
        Rectangle()
            .fill(Color.iterBorder(for: colorScheme))
            .frame(
                width: axis == .vertical ? 1 : nil,
                height: axis == .horizontal ? 1 : nil
            )
    }
}

#Preview("Light") {
    WorkspaceView()
        .environment(ThemeStore())
        .environment(DaemonClient())
        .environment(WorkspaceRouter())
        .environment(SessionStore())
        .preferredColorScheme(.light)
}

#Preview("Dark") {
    WorkspaceView()
        .environment(ThemeStore())
        .environment(DaemonClient())
        .environment(WorkspaceRouter())
        .environment(SessionStore())
        .preferredColorScheme(.dark)
}
