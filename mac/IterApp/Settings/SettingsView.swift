import AppKit
import SwiftUI
// swiftlint:disable file_length

enum SettingsPolicy {
    static let retentionSummary = "Hot in Postgres for 90 days, then archived to R2. "
        + "Scored summaries kept indefinitely."
}

struct SettingsView: View {
    @Environment(\.colorScheme) private var colorScheme
    @Environment(DaemonClient.self) private var daemonClient
    @Environment(SessionStore.self) private var sessionStore

    let dashboard: DashboardMeResponse?

    @State private var unavailableNotice: String?
    @State private var destructiveRequest: DestructiveRequest?

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: IterSpacing.sectionGap) {
                header
                accountSection
                tenantSection
                captureSection
                retentionSection
                redactionSection
                dataExportSection
                notificationsSection
            }
            .padding(IterSpacing.mainPanePadding)
            .frame(maxWidth: 920, alignment: .leading)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .background(Color.iterPanel(for: colorScheme))
        .task {
            await daemonClient.refreshCaptureSettings()
        }
        .alert("Endpoint not yet available", isPresented: unavailableNoticeBinding) {
            Button("OK", role: .cancel) {}
        } message: {
            Text(unavailableNotice ?? "")
        }
        .sheet(item: $destructiveRequest) { request in
            ConfirmDestructiveSheet(request: request) {
                unavailableNotice = """
                \(request.endpoint) is not implemented yet. Follow-up issue 079 tracks the server endpoint.
                """
                destructiveRequest = nil
            } onCancel: {
                destructiveRequest = nil
            }
        }
    }

    private var header: some View {
        VStack(alignment: .leading, spacing: 3) {
            Text(verbatim: "Settings")
                .font(IterFont.sansKPIValue)
                .foregroundStyle(Color.iterTextPrimary(for: colorScheme))

            Text(verbatim: "Account, tenant, capture, retention, redaction, export, and notifications.")
                .font(IterFont.sansBody)
                .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
        }
    }

    private var accountSection: some View {
        SettingsPanel(title: "Account") {
            SettingsKeyValueRow(label: "display name", value: displayName, isMonospaced: true)
            SettingsKeyValueRow(label: "email", value: email, isMonospaced: true)
            SettingsKeyValueRow(label: "sign-in provider", value: "WorkOS device code")

            HStack(spacing: IterSpacing.gapSmall) {
                IterButton(title: "Sign out") {
                    sessionStore.signOut()
                }

                Button(role: .destructive) {
                    destructiveRequest = DestructiveRequest(
                        verb: "Delete",
                        target: "account",
                        endpoint: "POST /v1/account/delete"
                    )
                } label: {
                    Text(verbatim: "Delete account")
                        .font(IterFont.sansLabel)
                        .foregroundStyle(Color.iterBad(for: colorScheme))
                        .padding(.horizontal, 8)
                        .frame(height: 22)
                        .background(Color.iterPanel(for: colorScheme))
                        .clipShape(.rect(cornerRadius: IterRadius.button))
                        .overlay {
                            RoundedRectangle(cornerRadius: IterRadius.button)
                                .stroke(Color.iterBadSoft(for: colorScheme), lineWidth: 1)
                        }
                }
                .buttonStyle(.plain)
                .help("Requires typing DELETE to confirm")
            }
        }
    }

    private var tenantSection: some View {
        SettingsPanel(title: "Tenant") {
            SettingsKeyValueRow(label: "tenant", value: tenantName)
            SettingsKeyValueRow(label: "role", value: "member", source: "tenant membership")
            SettingsKeyValueRow(label: "join date", value: "inherited", source: "tenant membership")

            SettingsNotice(
                title: "Web admin",
                message: "iter.dev admin opens for tenant admins only. Your current local claim is member."
            )
        }
    }

    private var captureSection: some View {
        SettingsPanel(title: "Capture toggles") {
            ForEach(daemonClient.captureSettings) { setting in
                CaptureToggleRow(setting: setting) { enabled in
                    Task {
                        await daemonClient.setCapture(setting.id, enabled: enabled)
                    }
                }
            }

            if let lastError = daemonClient.lastError, !daemonClient.connected {
                SettingsNotice(title: "Daemon unavailable", message: lastError)
            }
        }
    }

    private var retentionSection: some View {
        SettingsPanel(title: "Retention info") {
            Text(verbatim: SettingsPolicy.retentionSummary)
                .font(IterFont.sansBody)
                .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
                .fixedSize(horizontal: false, vertical: true)

            SettingsKeyValueRow(
                label: "source",
                value: "SettingsPolicy.retentionSummary",
                isMonospaced: true
            )
        }
    }

    private var redactionSection: some View {
        SettingsPanel(title: "Redaction rules preview") {
            VStack(alignment: .leading, spacing: IterSpacing.gapSmall) {
                ClassificationRow(tier: "clean", detail: "No trufflehog or local PII detector findings.")
                ClassificationRow(
                    tier: "strippable",
                    detail: "Secrets or PII can be replaced with redaction markers before sync."
                )
                ClassificationRow(
                    tier: "dirty",
                    detail: "Unredactable findings stay on device and only contribute to local scoring."
                )
            }

            VStack(alignment: .leading, spacing: 6) {
                DetectorCategoryRow(name: "Secrets", detail: "API keys, cloud tokens, private keys")
                DetectorCategoryRow(name: "PII", detail: "Email addresses, phone numbers, street addresses")
                DetectorCategoryRow(
                    name: "Source references",
                    detail: "Repo hashes, file paths, and opaque tool payloads"
                )
            }

            SettingsNotice(
                title: "What stays on device",
                message: "Dirty records never reach cloud sync; clean and redacted strippable records can sync."
            )
        }
    }

    private var dataExportSection: some View {
        SettingsPanel(title: "Data export") {
            SettingsNotice(
                title: "Endpoint unavailable",
                message: "POST /v1/account/export is not implemented yet, so export cannot start from the Mac app."
            )

            IterButton(title: "Export my data") {
                unavailableNotice = """
                POST /v1/account/export is not implemented yet. Follow-up issue 079 tracks the server endpoint \
                and archive polling flow.
                """
            }
        }
    }

    private var notificationsSection: some View {
        SettingsPanel(title: "Notifications") {
            HStack(spacing: IterSpacing.gapMedium) {
                Toggle(
                    "Suggestion popover",
                    isOn: Binding(
                        get: { daemonClient.suggestionNotificationsEnabled },
                        set: { daemonClient.setSuggestionNotificationsEnabled($0) }
                    )
                )
                .toggleStyle(.switch)
                .font(IterFont.sansBody)
                .foregroundStyle(Color.iterTextPrimary(for: colorScheme))

                Spacer()

                Text(verbatim: daemonClient.suggestionNotificationsEnabled ? "on" : "off")
                    .font(IterFont.monoLabel)
                    .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
            }

            SettingsKeyValueRow(
                label: "source",
                value: "this Mac",
                source: "local preference"
            )
        }
    }

    private var displayName: String {
        dashboard?.user.displayName ?? sessionStore.displayName ?? "signed out"
    }

    private var email: String {
        dashboard?.user.email ?? sessionStore.displayName ?? "unavailable"
    }

    private var tenantName: String {
        sessionStore.tenantId.map { "tenant \($0.prefix(8))" } ?? "unavailable"
    }

    private var unavailableNoticeBinding: Binding<Bool> {
        Binding(
            get: { unavailableNotice != nil },
            set: { if !$0 { unavailableNotice = nil } }
        )
    }
}

private struct SettingsPanel<Content: View>: View {
    @Environment(\.colorScheme) private var colorScheme

    let title: String
    @ViewBuilder let content: Content

    var body: some View {
        VStack(alignment: .leading, spacing: IterSpacing.gapMedium) {
            Text(verbatim: title)
                .font(IterFont.sansSectionTitle)
                .foregroundStyle(Color.iterTextPrimary(for: colorScheme))

            content
        }
        .padding(IterSpacing.gapMedium)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color.iterPanel(for: colorScheme))
        .clipShape(.rect(cornerRadius: IterRadius.card))
        .overlay {
            RoundedRectangle(cornerRadius: IterRadius.card)
                .stroke(Color.iterBorder(for: colorScheme), lineWidth: 1)
        }
    }
}

private struct SettingsKeyValueRow: View {
    @Environment(\.colorScheme) private var colorScheme

    let label: String
    let value: String
    var source: String?
    var isMonospaced = false

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: IterSpacing.gapMedium) {
            Text(verbatim: label)
                .font(IterFont.monoLabel)
                .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
                .frame(width: 124, alignment: .leading)

            Text(verbatim: value)
                .font(isMonospaced ? IterFont.monoBody : IterFont.sansBody)
                .foregroundStyle(Color.iterTextPrimary(for: colorScheme))
                .lineLimit(1)

            Spacer()

            if let source {
                SourceBadge(text: source)
            }
        }
        .frame(minHeight: IterSpacing.rowHeight)
    }
}

private struct CaptureToggleRow: View {
    @Environment(\.colorScheme) private var colorScheme

    let setting: CaptureHarnessSetting
    let onChange: (Bool) -> Void

    var body: some View {
        HStack(spacing: IterSpacing.gapMedium) {
            Harness(id: setting.id)
                .frame(width: 48, alignment: .leading)

            VStack(alignment: .leading, spacing: 2) {
                Text(verbatim: setting.id.displayName)
                    .font(IterFont.sansBody)
                    .foregroundStyle(Color.iterTextPrimary(for: colorScheme))

                Text(verbatim: setting.enabled ? "Capture enabled" : "Capture paused for this harness")
                    .font(IterFont.monoTiny)
                    .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
            }

            Spacer()

            if setting.inherited {
                SourceBadge(text: setting.source)
            } else {
                SourceBadge(text: setting.source)
            }

            Toggle(
                setting.id.displayName,
                isOn: Binding(
                    get: { setting.enabled },
                    set: onChange
                )
            )
            .labelsHidden()
            .toggleStyle(.switch)
        }
        .frame(minHeight: 34)
    }
}

private struct ClassificationRow: View {
    @Environment(\.colorScheme) private var colorScheme

    let tier: String
    let detail: String

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: IterSpacing.gapMedium) {
            Text(verbatim: tier)
                .font(IterFont.monoLabel)
                .foregroundStyle(tint)
                .frame(width: 74, alignment: .leading)

            Text(verbatim: detail)
                .font(IterFont.sansBody)
                .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
                .fixedSize(horizontal: false, vertical: true)
        }
    }

    private var tint: Color {
        switch tier {
        case "clean":
            return Color.iterGood(for: colorScheme)
        case "strippable":
            return Color.iterWarn(for: colorScheme)
        default:
            return Color.iterBad(for: colorScheme)
        }
    }
}

private struct DetectorCategoryRow: View {
    @Environment(\.colorScheme) private var colorScheme

    let name: String
    let detail: String

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: IterSpacing.gapMedium) {
            Text(verbatim: name)
                .font(IterFont.sansSectionTitle)
                .foregroundStyle(Color.iterTextPrimary(for: colorScheme))
                .frame(width: 124, alignment: .leading)

            Text(verbatim: detail)
                .font(IterFont.monoSmall)
                .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
        }
    }
}

private struct SettingsNotice: View {
    @Environment(\.colorScheme) private var colorScheme

    let title: String
    let message: String

    var body: some View {
        VStack(alignment: .leading, spacing: 3) {
            Text(verbatim: title)
                .font(IterFont.sansSectionTitle)
                .foregroundStyle(Color.iterTextPrimary(for: colorScheme))

            Text(verbatim: message)
                .font(IterFont.sansSmall)
                .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
                .fixedSize(horizontal: false, vertical: true)
        }
        .padding(IterSpacing.gapSmall)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color.iterSelected(for: colorScheme))
        .clipShape(.rect(cornerRadius: IterRadius.button))
    }
}

private struct SourceBadge: View {
    @Environment(\.colorScheme) private var colorScheme

    let text: String

    var body: some View {
        Text(verbatim: text)
            .font(IterFont.monoTiny)
            .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
            .padding(.horizontal, 6)
            .frame(height: 18)
            .background(Color.iterSelected(for: colorScheme))
            .clipShape(.rect(cornerRadius: IterRadius.scoreChip))
    }
}

private struct DestructiveRequest: Identifiable {
    let id = UUID()
    let verb: String
    let target: String
    let endpoint: String
}

private struct ConfirmDestructiveSheet: View {
    @Environment(\.colorScheme) private var colorScheme
    @State private var confirmation = ""

    let request: DestructiveRequest
    let onConfirm: () -> Void
    let onCancel: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: IterSpacing.gapMedium) {
            Text(verbatim: "\(request.verb) \(request.target)")
                .font(IterFont.sansKPIValue)
                .foregroundStyle(Color.iterTextPrimary(for: colorScheme))

            Text(verbatim: "Type DELETE to confirm. The cloud endpoint is required before this action can complete.")
                .font(IterFont.sansBody)
                .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
                .fixedSize(horizontal: false, vertical: true)

            TextField("DELETE", text: $confirmation)
                .textFieldStyle(.plain)
                .font(IterFont.monoBody)
                .padding(.horizontal, IterSpacing.gapSmall)
                .frame(height: 30)
                .background(Color.iterSidebar(for: colorScheme))
                .clipShape(.rect(cornerRadius: IterRadius.button))
                .overlay {
                    RoundedRectangle(cornerRadius: IterRadius.button)
                        .stroke(Color.iterBorder(for: colorScheme), lineWidth: 1)
                }

            HStack {
                Spacer()
                IterButton(title: "Cancel", action: onCancel)
                Button(role: .destructive, action: onConfirm) {
                    Text(verbatim: request.verb)
                        .font(IterFont.sansLabel)
                        .foregroundStyle(Color.white)
                        .padding(.horizontal, 8)
                        .frame(height: 22)
                        .background(Color.iterBad(for: colorScheme))
                        .clipShape(.rect(cornerRadius: IterRadius.button))
                }
                .buttonStyle(.plain)
                .disabled(confirmation != "DELETE")
            }
        }
        .padding(IterSpacing.gapLarge)
        .frame(width: 420, alignment: .leading)
        .background(Color.iterPanel(for: colorScheme))
    }
}

private extension HarnessID {
    var displayName: String {
        switch self {
        case .claudeCode:
            return "claude-code"
        case .codex:
            return "codex"
        case .geminiCLI:
            return "gemini"
        case .opencode:
            return "opencode"
        case .piHarness:
            return "pi"
        }
    }
}

#Preview("Settings") {
    SettingsView(dashboard: nil)
        .environment(DaemonClient())
        .environment(SessionStore())
}
