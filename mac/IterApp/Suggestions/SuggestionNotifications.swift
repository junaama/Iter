import AppKit
import SwiftUI
import UserNotifications

struct IterSuggestion: Equatable, Identifiable {
    let id: String
    let sessionID: String
    let action: String
    let suggestionID: String?
    let refinedPrompt: String
    let rationale: String?
    let confidence: Double
    let evidence: [IterSuggestionEvidence]

    init?(result: [String: Any]) {
        guard result["available"] as? Bool == true else { return nil }
        guard
            let id = result["id"] as? String,
            let sessionID = result["session_id"] as? String,
            let action = result["action"] as? String,
            let refinedPrompt = result["refined_prompt"] as? String
        else {
            return nil
        }
        self.id = id
        self.sessionID = sessionID
        self.action = action
        self.suggestionID = result["suggestion_id"] as? String
        self.refinedPrompt = refinedPrompt
        self.rationale = result["rationale"] as? String
        self.confidence = (result["confidence"] as? NSNumber)?.doubleValue ?? 0
        self.evidence = (result["evidence"] as? [[String: Any]] ?? []).compactMap(IterSuggestionEvidence.init)
    }

    var confidenceLabel: String {
        "\(Int((confidence * 100).rounded()))%"
    }
}

struct IterSuggestionEvidence: Equatable, Identifiable {
    let id: String
    let outcome: String
    let wallTimeMS: Int?
    let contributorDisplayName: String

    init?(result: [String: Any]) {
        guard
            let sessionID = result["session_id"] as? String,
            let outcome = result["outcome"] as? String,
            let contributorDisplayName = result["contributor_display_name"] as? String
        else {
            return nil
        }
        self.id = sessionID
        self.outcome = outcome
        self.wallTimeMS = (result["wall_time_ms"] as? NSNumber)?.intValue
        self.contributorDisplayName = contributorDisplayName
    }
}

final class SuggestionNotificationPresenter: NSObject, UNUserNotificationCenterDelegate {
    static let shared = SuggestionNotificationPresenter()

    private static let categoryIdentifier = "iter.suggestion"
    private static let copyActionIdentifier = "iter.suggestion.copy"
    private static let dismissActionIdentifier = "iter.suggestion.dismiss"
    private static let suppressActionIdentifier = "iter.suggestion.suppress"

    private var activeSuggestions: [String: IterSuggestion] = [:]
    private var detailPanel: NSPanel?
    private var suppressHandler: ((IterSuggestion) async -> Void)?
    private var isPrepared = false

    @MainActor
    func configure(suppressHandler: @escaping (IterSuggestion) async -> Void) {
        self.suppressHandler = suppressHandler
    }

    @MainActor
    func prepare() async {
        guard !isPrepared else { return }
        isPrepared = true

        let center = UNUserNotificationCenter.current()
        center.delegate = self
        center.setNotificationCategories([
            UNNotificationCategory(
                identifier: Self.categoryIdentifier,
                actions: [
                    UNNotificationAction(
                        identifier: Self.copyActionIdentifier,
                        title: "Copy to clipboard",
                        options: []
                    ),
                    UNNotificationAction(identifier: Self.dismissActionIdentifier, title: "Dismiss", options: []),
                    UNNotificationAction(
                        identifier: Self.suppressActionIdentifier,
                        title: "Suppress this pattern",
                        options: []
                    )
                ],
                intentIdentifiers: [],
                options: []
            )
        ])
        _ = try? await center.requestAuthorization(options: [.alert, .sound])
    }

    @MainActor
    func present(_ suggestion: IterSuggestion) async {
        guard suggestion.refinedPrompt.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty == false else { return }

        activeSuggestions[suggestion.id] = suggestion

        let content = UNMutableNotificationContent()
        content.title = "Iter suggestion"
        content.body = Self.notificationBody(for: suggestion.refinedPrompt)
        content.categoryIdentifier = Self.categoryIdentifier
        content.userInfo = ["suggestion_id": suggestion.id]

        let request = UNNotificationRequest(identifier: suggestion.id, content: content, trigger: nil)
        try? await UNUserNotificationCenter.current().add(request)

        Task { @MainActor [weak self] in
            try? await Task.sleep(nanoseconds: 8_000_000_000)
            UNUserNotificationCenter.current().removeDeliveredNotifications(withIdentifiers: [suggestion.id])
            self?.activeSuggestions.removeValue(forKey: suggestion.id)
        }
    }

    nonisolated func userNotificationCenter(
        _ center: UNUserNotificationCenter,
        willPresent notification: UNNotification,
        withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void
    ) {
        completionHandler([.banner, .list])
    }

    nonisolated func userNotificationCenter(
        _ center: UNUserNotificationCenter,
        didReceive response: UNNotificationResponse,
        withCompletionHandler completionHandler: @escaping () -> Void
    ) {
        Task { @MainActor [weak self] in
            self?.handle(response)
            completionHandler()
        }
    }

    @MainActor
    private func handle(_ response: UNNotificationResponse) {
        let identifier = response.notification.request.identifier
        guard let suggestion = activeSuggestions[identifier] else { return }

        switch response.actionIdentifier {
        case Self.copyActionIdentifier:
            copyToPasteboard(suggestion.refinedPrompt)
            activeSuggestions.removeValue(forKey: identifier)
            UNUserNotificationCenter.current().removeDeliveredNotifications(withIdentifiers: [identifier])
        case Self.suppressActionIdentifier:
            activeSuggestions.removeValue(forKey: identifier)
            UNUserNotificationCenter.current().removeDeliveredNotifications(withIdentifiers: [identifier])
            Task {
                await suppressHandler?(suggestion)
            }
        case UNNotificationDefaultActionIdentifier:
            showDetailPanel(for: suggestion)
        default:
            activeSuggestions.removeValue(forKey: identifier)
            UNUserNotificationCenter.current().removeDeliveredNotifications(withIdentifiers: [identifier])
        }
    }

    @MainActor
    private func copyToPasteboard(_ text: String) {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(text, forType: .string)
    }

    @MainActor
    private func showDetailPanel(for suggestion: IterSuggestion) {
        let panel = detailPanel ?? NSPanel(
            contentRect: NSRect(x: 0, y: 0, width: 380, height: 280),
            styleMask: [.titled, .fullSizeContentView, .nonactivatingPanel],
            backing: .buffered,
            defer: false
        )
        panel.title = "Iter suggestion"
        panel.isFloatingPanel = true
        panel.level = .floating
        panel.collectionBehavior = [.transient, .moveToActiveSpace]
        panel.contentViewController = NSHostingController(rootView: SuggestionDetailPanel(suggestion: suggestion))
        panel.center()
        panel.makeKeyAndOrderFront(nil)
        detailPanel = panel
    }

    private static func notificationBody(for prompt: String) -> String {
        let collapsed = prompt
            .replacingOccurrences(of: "\n", with: " ")
            .replacingOccurrences(of: "\t", with: " ")
        guard collapsed.count > 120 else { return collapsed }
        let end = collapsed.index(collapsed.startIndex, offsetBy: 120)
        return "\(collapsed[..<end])..."
    }
}

private struct SuggestionDetailPanel: View {
    @Environment(\.colorScheme) private var colorScheme

    let suggestion: IterSuggestion

    var body: some View {
        VStack(alignment: .leading, spacing: IterSpacing.gapMedium) {
            HStack(spacing: IterSpacing.gapSmall) {
                Text(verbatim: "Suggestion")
                    .font(IterFont.sansSectionTitle)
                    .foregroundStyle(Color.iterTextPrimary(for: colorScheme))

                Text(verbatim: suggestion.action)
                    .font(IterFont.monoTiny)
                    .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
                    .padding(.horizontal, 6)
                    .frame(height: 18)
                    .background(Color.iterAccentSoft(for: colorScheme))
                    .clipShape(.rect(cornerRadius: IterRadius.scoreChip))

                Spacer()

                Text(verbatim: suggestion.confidenceLabel)
                    .font(IterFont.monoLabel)
                    .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
            }

            Text(verbatim: suggestion.refinedPrompt)
                .font(IterFont.monoSmall)
                .foregroundStyle(Color.iterTextPrimary(for: colorScheme))
                .lineLimit(5)
                .textSelection(.enabled)
                .frame(maxWidth: .infinity, alignment: .leading)

            if let rationale = suggestion.rationale, !rationale.isEmpty {
                SuggestionDividerLine()

                Text(verbatim: rationale)
                    .font(IterFont.sansBody)
                    .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
                    .lineLimit(4)
            }

            if !suggestion.evidence.isEmpty {
                SuggestionDividerLine()

                VStack(alignment: .leading, spacing: IterSpacing.gapSmall) {
                    ForEach(suggestion.evidence.prefix(3)) { item in
                        HStack(spacing: IterSpacing.gapSmall) {
                            Text(verbatim: item.contributorDisplayName)
                                .font(IterFont.sansLabel)
                                .foregroundStyle(Color.iterTextPrimary(for: colorScheme))
                            Text(verbatim: item.outcome)
                                .font(IterFont.monoTiny)
                                .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
                            Spacer()
                            if let wallTimeMS = item.wallTimeMS {
                                Text(verbatim: "\(wallTimeMS)ms")
                                    .font(IterFont.monoTiny)
                                    .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
                            }
                        }
                    }
                }
            }
        }
        .padding(IterSpacing.gapLarge)
        .frame(width: 380, alignment: .topLeading)
        .background(Color.iterPanel(for: colorScheme))
    }
}

private struct SuggestionDividerLine: View {
    @Environment(\.colorScheme) private var colorScheme

    var body: some View {
        Rectangle()
            .fill(Color.iterBorder(for: colorScheme))
            .frame(height: 1)
    }
}
