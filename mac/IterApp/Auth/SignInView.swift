import AppKit
import ApplicationServices
import SwiftUI

@Observable
final class OnboardingStore {
    private enum Storage {
        static let completed = "dev.iter.onboarding.completed"
        static let degraded = "dev.iter.onboarding.degraded"
    }

    private let defaults: UserDefaults

    var completed = false
    var degraded = false

    init(defaults: UserDefaults = .standard) {
        self.defaults = defaults
    }

    func load() {
        completed = defaults.bool(forKey: Storage.completed)
        degraded = defaults.bool(forKey: Storage.degraded)
    }

    func finish(degraded: Bool = false) {
        self.completed = true
        self.degraded = degraded
        defaults.set(true, forKey: Storage.completed)
        defaults.set(degraded, forKey: Storage.degraded)
    }

    func reset() {
        completed = false
        degraded = false
        defaults.removeObject(forKey: Storage.completed)
        defaults.removeObject(forKey: Storage.degraded)
    }
}

struct RootSessionView: View {
    @Environment(SessionStore.self) private var sessionStore
    @Environment(OnboardingStore.self) private var onboardingStore

    var body: some View {
        switch sessionStore.status {
        case .loading:
            LoadingSessionView()
        case .signedIn:
            if onboardingStore.completed {
                WorkspaceView()
            } else {
                OnboardingView()
            }
        case .signedOut, .signingIn, .polling, .expired, .failed:
            SignInView()
        }
    }
}

private struct LoadingSessionView: View {
    @Environment(\.colorScheme) private var colorScheme

    var body: some View {
        ZStack {
            Color.iterStageBackdrop(for: colorScheme)
                .ignoresSafeArea()
            ProgressView()
                .controlSize(.small)
        }
    }
}

struct SignInView: View {
    @Environment(\.colorScheme) private var colorScheme
    @Environment(SessionStore.self) private var sessionStore

    var body: some View {
        ZStack {
            Color.iterStageBackdrop(for: colorScheme)
                .ignoresSafeArea()

            VStack(alignment: .leading, spacing: IterSpacing.gapLarge) {
                SignInHeader()

                VStack(alignment: .leading, spacing: IterSpacing.gapMedium) {
                    HStack(alignment: .top) {
                        VStack(alignment: .leading, spacing: 5) {
                            Text(verbatim: "WorkOS device sign-in")
                                .font(IterFont.sansSectionTitle)
                                .foregroundStyle(Color.iterTextPrimary(for: colorScheme))

                            Text(verbatim: statusCopy)
                                .font(IterFont.sansSmall)
                                .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
                                .fixedSize(horizontal: false, vertical: true)
                        }

                        Spacer()

                        if sessionStore.status == .signingIn || sessionStore.status == .polling {
                            ProgressView()
                                .controlSize(.small)
                        }
                    }

                    if let authorization = sessionStore.deviceAuthorization {
                        DeviceCodeBlock(authorization: authorization)

                        HStack(spacing: IterSpacing.gapSmall) {
                            ButtonPrimary(title: "Open in browser") {
                                sessionStore.openVerificationURL()
                            }
                            IterButton(title: "Cancel") {
                                sessionStore.signOut()
                            }
                        }
                    } else {
                        ButtonPrimary(title: "Sign in") {
                            sessionStore.startDeviceAuthorization()
                        }
                    }

                    if let message = sessionStore.lastError {
                        Text(verbatim: message)
                            .font(IterFont.monoSmall)
                            .foregroundStyle(Color.iterBad(for: colorScheme))
                            .fixedSize(horizontal: false, vertical: true)
                    }
                }
                .padding(IterSpacing.gapLarge)
                .frame(width: 420, alignment: .leading)
                .background(Color.iterPanel(for: colorScheme))
                .clipShape(.rect(cornerRadius: IterRadius.card))
                .overlay {
                    RoundedRectangle(cornerRadius: IterRadius.card)
                        .stroke(Color.iterBorder(for: colorScheme), lineWidth: 1)
                }
            }
            .padding(IterSpacing.gapLarge)
        }
        .frame(minWidth: 680, minHeight: 520)
    }

    private var statusCopy: String {
        switch sessionStore.status {
        case .expired:
            return "Session expired. Sign in again to continue."
        case .failed:
            return "The previous sign-in attempt failed."
        case .polling:
            return "Complete the browser step. Iter will continue automatically."
        case .signingIn:
            return "Requesting a device code."
        default:
            return "Sign in to sync traces, scores, and prompt refinements with your team."
        }
    }
}

private struct SignInHeader: View {
    @Environment(\.colorScheme) private var colorScheme

    var body: some View {
        HStack(spacing: IterSpacing.gapSmall) {
            Text(verbatim: "i")
                .font(IterFont.monoAvatar)
                .foregroundStyle(Color.white)
                .frame(width: 26, height: 26)
                .background(Color.iterAccent(for: colorScheme))
                .clipShape(.rect(cornerRadius: IterRadius.avatar))

            Text(verbatim: "iter")
                .font(IterFont.monoTitle)
                .foregroundStyle(Color.iterTextPrimary(for: colorScheme))
        }
    }
}

private struct DeviceCodeBlock: View {
    @Environment(\.colorScheme) private var colorScheme

    let authorization: DeviceAuthorization

    var body: some View {
        VStack(alignment: .leading, spacing: IterSpacing.gapSmall) {
            Text(verbatim: authorization.userCode)
                .font(IterFont.mono(size: 24, weight: .semibold))
                .foregroundStyle(Color.iterTextPrimary(for: colorScheme))
                .tracking(1)

            Text(verbatim: authorization.verificationURI.absoluteString)
                .font(IterFont.monoSmall)
                .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
                .lineLimit(1)
                .truncationMode(.middle)
        }
        .padding(IterSpacing.gapMedium)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color.iterSidebar(for: colorScheme))
        .clipShape(.rect(cornerRadius: IterRadius.card))
        .overlay {
            RoundedRectangle(cornerRadius: IterRadius.card)
                .stroke(Color.iterBorder(for: colorScheme), lineWidth: 1)
        }
        .accessibilityElement(children: .combine)
        .accessibilityLabel("Device code \(authorization.userCode)")
    }
}

private enum OnboardingStep {
    case permissions
    case tenant
    case waiting
}

private struct MacPermissionSnapshot: Equatable {
    var accessibilityTrusted: Bool
    var fullDiskReady: Bool
    var checkedPaths: [String]
    var unreadablePaths: [String]

    var ready: Bool {
        accessibilityTrusted && fullDiskReady
    }
}

private enum MacPermissionProbe {
    static let harnessPaths = [
        "~/.claude",
        "~/.codex",
        "~/.gemini",
        "~/.config/opencode",
        "~/Library/Application Support/Pi"
    ]

    static func snapshot() -> MacPermissionSnapshot {
        let accessibility = AXIsProcessTrusted()
        let expanded = harnessPaths.map(expandHome)
        let existing = expanded.filter { FileManager.default.fileExists(atPath: $0) }
        let checked = existing.isEmpty ? expanded : existing
        let unreadable = existing.filter { !FileManager.default.isReadableFile(atPath: $0) }
        return MacPermissionSnapshot(
            accessibilityTrusted: accessibility,
            fullDiskReady: unreadable.isEmpty,
            checkedPaths: checked,
            unreadablePaths: unreadable
        )
    }

    static func requestAccessibilityPrompt() {
        let key = kAXTrustedCheckOptionPrompt.takeUnretainedValue() as String
        let options = [key: true] as CFDictionary
        _ = AXIsProcessTrustedWithOptions(options)
    }

    static func openFullDiskAccessSettings() {
        let url = URL(string: "x-apple.systempreferences:com.apple.preference.security?Privacy_AllFiles")
            ?? URL(fileURLWithPath: "/System/Applications/System Settings.app")
        NSWorkspace.shared.open(url)
    }

    private static func expandHome(_ path: String) -> String {
        (path as NSString).expandingTildeInPath
    }
}

struct OnboardingView: View {
    @Environment(\.colorScheme) private var colorScheme
    @Environment(DaemonClient.self) private var daemonClient
    @Environment(OnboardingStore.self) private var onboardingStore

    @State private var step: OnboardingStep = .permissions
    @State private var permissions = MacPermissionProbe.snapshot()
    @State private var domain = ""
    @State private var workspaceName = ""
    @State private var tenantMatch: OnboardingTenantMatch?
    @State private var isLoading = false
    @State private var message: String?

    var body: some View {
        ZStack {
            Color.iterStageBackdrop(for: colorScheme)
                .ignoresSafeArea()

            VStack(alignment: .leading, spacing: IterSpacing.gapLarge) {
                SignInHeader()

                VStack(alignment: .leading, spacing: IterSpacing.gapLarge) {
                    OnboardingProgress(step: step)

                    switch step {
                    case .permissions:
                        permissionsPane
                    case .tenant:
                        tenantPane
                    case .waiting:
                        waitingPane
                    }

                    if let message {
                        Text(verbatim: message)
                            .font(IterFont.monoSmall)
                            .foregroundStyle(Color.iterBad(for: colorScheme))
                            .fixedSize(horizontal: false, vertical: true)
                    }
                }
                .padding(IterSpacing.gapLarge)
                .frame(width: 560, alignment: .leading)
                .background(Color.iterPanel(for: colorScheme))
                .clipShape(.rect(cornerRadius: IterRadius.card))
                .overlay {
                    RoundedRectangle(cornerRadius: IterRadius.card)
                        .stroke(Color.iterBorder(for: colorScheme), lineWidth: 1)
                }
            }
            .padding(IterSpacing.gapLarge)
        }
        .frame(minWidth: 760, minHeight: 560)
        .task {
            await pollPermissions()
        }
    }

    private var permissionsPane: some View {
        VStack(alignment: .leading, spacing: IterSpacing.gapMedium) {
            Text(verbatim: "Grant local capture access")
                .font(IterFont.sansSectionTitle)
                .foregroundStyle(Color.iterTextPrimary(for: colorScheme))

            PermissionRow(
                title: "Accessibility",
                status: permissions.accessibilityTrusted ? "Ready" : "Needs approval",
                isReady: permissions.accessibilityTrusted
            ) {
                MacPermissionProbe.requestAccessibilityPrompt()
                permissions = MacPermissionProbe.snapshot()
            }

            PermissionRow(
                title: "Full Disk Access",
                status: permissions.fullDiskReady ? "Harness folders readable" : "Needs approval",
                isReady: permissions.fullDiskReady
            ) {
                MacPermissionProbe.openFullDiskAccessSettings()
            }

            VStack(alignment: .leading, spacing: IterSpacing.gapTiny) {
                ForEach(permissions.checkedPaths, id: \.self) { path in
                    Text(verbatim: path)
                        .font(IterFont.monoTiny)
                        .foregroundStyle(permissionPathColor(path))
                        .lineLimit(1)
                        .truncationMode(.middle)
                }
            }

            HStack(spacing: IterSpacing.gapSmall) {
                ButtonPrimary(title: "Continue") {
                    step = .tenant
                }
                .disabled(!permissions.ready)

                IterButton(title: "Recheck") {
                    permissions = MacPermissionProbe.snapshot()
                }

                IterButton(title: "Set up later") {
                    Task {
                        await daemonClient.disableAllCapture()
                        onboardingStore.finish(degraded: true)
                    }
                }
            }
        }
    }

    private var tenantPane: some View {
        VStack(alignment: .leading, spacing: IterSpacing.gapMedium) {
            Text(verbatim: "Confirm workspace")
                .font(IterFont.sansSectionTitle)
                .foregroundStyle(Color.iterTextPrimary(for: colorScheme))

            TextField("company.com", text: $domain)
                .textFieldStyle(.roundedBorder)
                .font(IterFont.monoSmall)
                .onChange(of: domain) { _, newValue in
                    if workspaceName.isEmpty {
                        workspaceName = workspaceNameFromDomain(newValue)
                    }
                }

            HStack(spacing: IterSpacing.gapSmall) {
                ButtonPrimary(title: "Check domain") {
                    Task { await lookupDomain() }
                }
                .disabled(normalizedDomain.isEmpty || isLoading)

                IterButton(title: "Skip") {
                    onboardingStore.finish()
                }
            }

            if let match = tenantMatch {
                VStack(alignment: .leading, spacing: IterSpacing.gapSmall) {
                    Text(verbatim: match.name)
                        .font(IterFont.sansSectionTitle)
                        .foregroundStyle(Color.iterTextPrimary(for: colorScheme))
                    Text(verbatim: "\(match.memberCount) members")
                        .font(IterFont.sansSmall)
                        .foregroundStyle(Color.iterTextSecondary(for: colorScheme))

                    HStack(spacing: IterSpacing.gapSmall) {
                        ButtonPrimary(title: "Join") {
                            Task { await requestJoin(match) }
                        }
                        IterButton(title: "Create instead") {
                            tenantMatch = nil
                        }
                    }
                }
                .padding(IterSpacing.gapMedium)
                .background(Color.iterSidebar(for: colorScheme))
                .clipShape(.rect(cornerRadius: IterRadius.card))
            } else {
                VStack(alignment: .leading, spacing: IterSpacing.gapSmall) {
                    TextField("Workspace name", text: $workspaceName)
                        .textFieldStyle(.roundedBorder)
                        .font(IterFont.sansSmall)

                    ButtonPrimary(title: "Create workspace") {
                        Task { await createWorkspace() }
                    }
                    .disabled(workspaceName.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || isLoading)
                }
            }
        }
    }

    private var waitingPane: some View {
        VStack(alignment: .leading, spacing: IterSpacing.gapMedium) {
            Text(verbatim: "Waiting for admin approval")
                .font(IterFont.sansSectionTitle)
                .foregroundStyle(Color.iterTextPrimary(for: colorScheme))
            Text(verbatim: "The request is recorded. An admin approval queue is the remaining HITL follow-up for this slice.")
                .font(IterFont.sansSmall)
                .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
                .fixedSize(horizontal: false, vertical: true)
            IterButton(title: "Use personal workspace for now") {
                onboardingStore.finish()
            }
        }
    }

    private var normalizedDomain: String {
        domain.trimmingCharacters(in: .whitespacesAndNewlines)
            .trimmingCharacters(in: CharacterSet(charactersIn: "@"))
            .lowercased()
    }

    private func pollPermissions() async {
        while !Task.isCancelled, !onboardingStore.completed {
            permissions = MacPermissionProbe.snapshot()
            try? await Task.sleep(nanoseconds: 2_000_000_000)
        }
    }

    private func lookupDomain() async {
        isLoading = true
        message = nil
        defer { isLoading = false }
        do {
            let result = try await IterHTTPClient().lookupTenantDomain(domain: normalizedDomain)
            tenantMatch = result.match
            if result.match == nil, workspaceName.isEmpty {
                workspaceName = workspaceNameFromDomain(result.domain)
            }
        } catch {
            message = error.localizedDescription
        }
    }

    private func createWorkspace() async {
        isLoading = true
        message = nil
        defer { isLoading = false }
        do {
            _ = try await IterHTTPClient().createWorkspace(name: workspaceName)
            onboardingStore.finish()
        } catch {
            message = error.localizedDescription
        }
    }

    private func requestJoin(_ match: OnboardingTenantMatch) async {
        isLoading = true
        message = nil
        defer { isLoading = false }
        do {
            _ = try await IterHTTPClient().requestTenantJoin(tenantID: match.tenantId)
            step = .waiting
        } catch {
            message = error.localizedDescription
        }
    }

    private func workspaceNameFromDomain(_ value: String) -> String {
        let head = value.split(separator: ".").first.map(String.init) ?? ""
        guard !head.isEmpty else { return "" }
        return head.prefix(1).uppercased() + head.dropFirst()
    }

    private func permissionPathColor(_ path: String) -> Color {
        permissions.unreadablePaths.contains(path)
            ? Color.iterBad(for: colorScheme)
            : Color.iterTextTertiary(for: colorScheme)
    }
}

private struct OnboardingProgress: View {
    @Environment(\.colorScheme) private var colorScheme
    let step: OnboardingStep

    var body: some View {
        HStack(spacing: IterSpacing.gapSmall) {
            stepPill("1", "Permissions", active: step == .permissions)
            stepPill("2", "Workspace", active: step == .tenant)
        }
    }

    private func stepPill(_ number: String, _ title: String, active: Bool) -> some View {
        HStack(spacing: IterSpacing.gapTiny) {
            Text(verbatim: number)
                .font(IterFont.monoTiny)
            Text(verbatim: title)
                .font(IterFont.sansSmall)
        }
        .foregroundStyle(active ? Color.white : Color.iterTextSecondary(for: colorScheme))
        .padding(.horizontal, 8)
        .frame(height: 22)
        .background(active ? Color.iterAccent(for: colorScheme) : Color.iterSidebar(for: colorScheme))
        .clipShape(.rect(cornerRadius: IterRadius.button))
    }
}

private struct PermissionRow: View {
    @Environment(\.colorScheme) private var colorScheme

    let title: String
    let status: String
    let isReady: Bool
    let action: () -> Void

    var body: some View {
        HStack(spacing: IterSpacing.gapMedium) {
            Circle()
                .fill(isReady ? Color.iterGood(for: colorScheme) : Color.iterWarn(for: colorScheme))
                .frame(width: 8, height: 8)

            VStack(alignment: .leading, spacing: 2) {
                Text(verbatim: title)
                    .font(IterFont.sansLabel)
                    .foregroundStyle(Color.iterTextPrimary(for: colorScheme))
                Text(verbatim: status)
                    .font(IterFont.sansSmall)
                    .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
            }

            Spacer()

            IterButton(title: isReady ? "Open" : "Grant", action: action)
        }
        .padding(IterSpacing.gapMedium)
        .background(Color.iterSidebar(for: colorScheme))
        .clipShape(.rect(cornerRadius: IterRadius.card))
    }
}

#Preview("Sign In") {
    SignInView()
        .environment(SessionStore())
        .preferredColorScheme(.light)
}
