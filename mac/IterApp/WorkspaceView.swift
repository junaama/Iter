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
                                    teamSessions: dashboardTeamStore.recentSessions,
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
                Color.clear
                    .frame(width: 54, height: 12)
                    .accessibilityHidden(true)
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
    @Environment(WorkspaceRouter.self) private var router

    var body: some View {
        Menu {
            Section {
                Text(identityLabel)
            }

            Button {
                router.route = .settings
            } label: {
                Label("Settings", systemImage: "gearshape")
            }

            Divider()

            Button {
                sessionStore.signOut()
            } label: {
                Label("Sign out", systemImage: "rectangle.portrait.and.arrow.right")
            }
        } label: {
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

                    Text(verbatim: identityLabel)
                        .font(IterFont.monoSmall)
                        .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
                        .lineLimit(1)
                }

                Spacer()

                Image(systemName: "chevron.down")
                    .font(.system(size: 9, weight: .semibold))
                    .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
                    .accessibilityHidden(true)
            }
            .frame(height: 36)
            .contentShape(.rect)
        }
        .menuStyle(.borderlessButton)
        .buttonStyle(.plain)
        .accessibilityLabel("Profile menu")
    }

    private var identityLabel: String {
        guard let displayName = sessionStore.displayName?.trimmingCharacters(in: .whitespacesAndNewlines),
              !displayName.isEmpty else {
            return "signed in"
        }
        return displayName
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
                    TabButton(title: "Me", isActive: route.matchesTopLevel(.me)) {
                        route = .me
                    }
                    TabButton(title: "Team", isActive: route.matchesTopLevel(.team)) {
                        route = .team
                    }
                    TabButton(title: "Sessions", isActive: route.matchesTopLevel(.sessions)) {
                        route = .sessions
                    }
                }

                Spacer()

                if route.matchesTopLevel(.me) || route.matchesTopLevel(.team) || route.matchesTopLevel(.sessions) {
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
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            Text(verbatim: title)
                .font(IterFont.sans(size: 12, weight: isActive ? .medium : .regular))
                .foregroundStyle(
                    isActive ? Color.iterTextPrimary(for: colorScheme) : Color.iterTextTertiary(for: colorScheme)
                )
                .padding(.horizontal, 8)
                .frame(height: 24)
                .background(isActive ? Color.iterSelected(for: colorScheme) : Color.clear)
                .clipShape(.rect(cornerRadius: IterRadius.segment))
                .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .accessibilityAddTraits(isActive ? .isSelected : [])
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
        } else if route.matchesTopLevel(.sessions) {
            DashboardSessionsView(
                store: dashboardTeamStore,
                layoutVariant: layoutVariant,
                onSelectSession: { id in onNavigate(.sessionDetail(id: id)) }
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

            Text(verbatim: "This workspace view is unavailable.")
                .font(IterFont.sansBody)
                .foregroundStyle(Color.iterTextSecondary(for: colorScheme))

            Spacer()
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .padding(IterSpacing.mainPanePadding)
        .background(Color.iterPanel(for: colorScheme))
    }
}

private struct DashboardSessionsView: View {
    @Environment(\.colorScheme) private var colorScheme
    @Bindable var store: DashboardTeamStore

    let layoutVariant: LayoutVariant
    let onSelectSession: (String) -> Void

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: IterSpacing.gapLarge) {
                KPIRow(tiles: kpiTiles)

                if let errorMessage = store.errorMessage {
                    SessionsInlineRetryBanner(message: errorMessage) {
                        Task { await store.load(forceRefresh: true) }
                    }
                }

                SessionsSection(
                    count: store.sessionCountLabel,
                    sessions: sessionItems,
                    layoutVariant: layoutVariant,
                    isLoading: store.recentSessions.isEmpty && store.isLoading,
                    onSelectSession: onSelectSession
                )
            }
            .padding(IterSpacing.mainPanePadding)
        }
        .background(Color.iterPanel(for: colorScheme))
        .task {
            await store.load()
        }
        .onReceive(NotificationCenter.default.publisher(for: NSApplication.didBecomeActiveNotification)) { _ in
            Task { await store.load() }
        }
    }

    private var sessionItems: [SessionListItem] {
        DashboardTeamDisplay.sessionItems(
            from: store.recentSessions,
            members: store.dashboard?.members ?? []
        )
    }

    private var kpiTiles: [KPITileData] {
        let sessions = store.recentSessions
        let scored = sessions.compactMap { $0.latestScore?.compositeScore }
        let averageScore = scored.isEmpty
            ? nil
            : IterScoreValue.fromCompositeScore(scored.reduce(0, +) / Double(scored.count))
        let toolCount = sessions.reduce(0) { $0 + $1.tools.count }

        return [
            KPITileData(
                label: "sessions",
                value: sessions.isEmpty && store.isLoading ? "--" : "\(sessions.count)",
                unit: nil,
                delta: .flat("latest"),
                sparkline: sessions.map { Double($0.tools.count) }
            ),
            KPITileData(
                label: "scored",
                value: sessions.isEmpty && store.isLoading ? "--" : "\(scored.count)",
                unit: nil,
                delta: .flat("outcomes"),
                sparkline: scored
            ),
            KPITileData(
                label: "avg score",
                value: averageScore.map(String.init) ?? "--",
                unit: nil,
                delta: .flat(scored.isEmpty ? "pending" : "latest"),
                sparkline: scored
            ),
            KPITileData(
                label: "tool calls",
                value: sessions.isEmpty && store.isLoading ? "--" : "\(toolCount)",
                unit: nil,
                delta: .flat("captured"),
                sparkline: sessions.map { Double($0.tools.count) }
            )
        ]
    }
}

private struct SessionsSection: View {
    @Environment(\.colorScheme) private var colorScheme

    let count: String
    let sessions: [SessionListItem]
    let layoutVariant: LayoutVariant
    let isLoading: Bool
    let onSelectSession: (String) -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: IterSpacing.gapSmall) {
            HStack(spacing: IterSpacing.gapSmall) {
                Text(verbatim: "Sessions")
                    .font(IterFont.sansSectionTitle)
                    .foregroundStyle(Color.iterTextPrimary(for: colorScheme))

                Text(verbatim: count)
                    .font(IterFont.monoTiny)
                    .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
                    .padding(.horizontal, 5)
                    .frame(height: 18)
                    .background(Color.iterSelected(for: colorScheme))
                    .clipShape(.rect(cornerRadius: IterRadius.scoreChip))

                Spacer()
            }

            Group {
                switch layoutVariant {
                case .table:
                    SessionTable(
                        sessions: sessions,
                        emptyMessage: "No sessions yet.",
                        isLoading: isLoading,
                        showsAuthorColumn: true
                    ) { session in
                        onSelectSession(session.id)
                    }
                case .cards:
                    SessionCards(sessions: sessions) { session in
                        onSelectSession(session.id)
                    }
                case .feed:
                    SessionFeed(groups: feedGroups) { session in
                        onSelectSession(session.id)
                    }
                }
            }
            .id(layoutVariant)
            .transition(.opacity)
        }
        .animation(.easeInOut(duration: 0.08), value: layoutVariant)
    }

    private var feedGroups: [(day: String, sessions: [SessionListItem])] {
        guard !sessions.isEmpty else {
            return [(day: "Today", sessions: [])]
        }

        return sessions.reduce(into: [(day: String, sessions: [SessionListItem])]()) { groups, session in
            let day = feedDay(for: session)
            if let index = groups.firstIndex(where: { $0.day == day }) {
                groups[index].sessions.append(session)
            } else {
                groups.append((day: day, sessions: [session]))
            }
        }
    }

    private func feedDay(for session: SessionListItem) -> String {
        switch session.when {
        case "Yesterday":
            return "Yesterday"
        case let value where value.count == 3:
            return value
        default:
            return "Today"
        }
    }
}

private struct SessionsInlineRetryBanner: View {
    @Environment(\.colorScheme) private var colorScheme

    let message: String
    let retry: () -> Void

    var body: some View {
        HStack(spacing: IterSpacing.gapSmall) {
            Image(systemName: "exclamationmark.triangle")
                .font(.system(size: 11, weight: .medium))
                .foregroundStyle(Color.iterWarn(for: colorScheme))
                .accessibilityHidden(true)

            Text(verbatim: message)
                .font(IterFont.sansSmall)
                .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
                .lineLimit(2)

            Spacer()

            IterButton(title: "Retry", action: retry)
        }
        .padding(.horizontal, IterSpacing.gapMedium)
        .frame(minHeight: 36)
        .background(Color.iterWarnSoft(for: colorScheme))
        .clipShape(.rect(cornerRadius: IterRadius.card))
        .overlay {
            RoundedRectangle(cornerRadius: IterRadius.card)
                .stroke(Color.iterBorder(for: colorScheme), lineWidth: 1)
        }
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

private struct SessionsRailCards: View {
    let sessions: [SessionSummary]
    let members: [TeamMemberAggregate]

    var body: some View {
        VStack(alignment: .leading, spacing: IterSpacing.gapMedium) {
            RailCard(
                title: "Recent captures",
                count: "\(sessions.count)",
                items: recentItems
            )

            RailCard(
                title: "Harness mix",
                count: "\(harnessItems.count)",
                items: harnessItems
            )
        }
    }

    private var recentItems: [RailItem] {
        sessions.prefix(5).map { session in
            let metadata = [
                memberName(for: session.userID),
                harnessLabel(session.harness),
                durationLabel(for: session)
            ].joined(separator: " · ")

            return RailItem(
                title: title(from: session.redactedPrompt),
                metadata: metadata,
                primaryAction: nil,
                secondaryAction: nil
            )
        }
    }

    private var harnessItems: [RailItem] {
        let counts = Dictionary(grouping: sessions, by: \.harness)
            .mapValues(\.count)
            .sorted { lhs, rhs in
                lhs.value == rhs.value ? lhs.key < rhs.key : lhs.value > rhs.value
            }

        return counts.map { harness, count in
            RailItem(
                title: harnessLabel(harness),
                metadata: "\(count) session\(count == 1 ? "" : "s")",
                primaryAction: nil,
                secondaryAction: nil
            )
        }
    }

    private func memberName(for userID: UUID) -> String {
        members.first(where: { $0.userID == userID })?.displayName ?? "Teammate"
    }

    private func title(from prompt: String) -> String {
        let trimmed = prompt.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return "Captured coding session" }
        let firstLine = trimmed.split(separator: "\n").first.map(String.init) ?? trimmed
        return firstLine.count > 64 ? String(firstLine.prefix(64)) + "..." : firstLine
    }

    private func harnessLabel(_ rawValue: String) -> String {
        (HarnessID(rawValue: rawValue) ?? .codex).rawValue.replacingOccurrences(of: "_", with: " ")
    }

    private func durationLabel(for session: SessionSummary) -> String {
        let seconds: Int
        if let wallTimeMs = session.wallTimeMs {
            seconds = max(0, wallTimeMs / 1_000)
        } else if let endedAt = session.endedAt {
            seconds = max(0, Int(endedAt.timeIntervalSince(session.startedAt)))
        } else {
            return "--"
        }

        let minutes = max(1, seconds / 60)
        if minutes < 60 { return "\(minutes)m" }
        return String(format: "%.1fh", Double(minutes) / 60.0)
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
    let teamDashboard: DashboardTeamResponse?
    let teamSessions: [SessionSummary]
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
                } else if route.matchesTopLevel(.team) {
                    TeamRailCards(dashboard: teamDashboard)
                } else if route.matchesTopLevel(.sessions) {
                    SessionsRailCards(sessions: teamSessions, members: teamDashboard?.members ?? [])
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
