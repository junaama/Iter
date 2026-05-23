import AppKit
import Observation
import SwiftUI
// swiftlint:disable file_length

struct StackSimulateView: View {
    @Environment(\.colorScheme) private var colorScheme
    @Environment(DaemonClient.self) private var daemonClient

    let userID: UUID

    @State private var model: StackSimulationModel
    @State private var toast: StackToast?

    init(userID: UUID) {
        self.userID = userID
        _model = State(initialValue: StackSimulationModel(userID: userID))
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: IterSpacing.sectionGap) {
                header

                switch model.state {
                case .idle, .loading:
                    ProgressView()
                        .controlSize(.small)
                        .frame(maxWidth: .infinity, alignment: .leading)
                case .forbidden(let message), .unavailable(let message):
                    StackSimulateEmptyState(message: message)
                case .loaded(let stack):
                    StackSimulateReadOnlyStack(stack: stack)
                    simulationAction
                }
            }
            .padding(IterSpacing.mainPanePadding)
        }
        .background(Color.iterPanel(for: colorScheme))
        .safeAreaInset(edge: .bottom) {
            StackSimulateToastView(toast: toast)
                .padding(.bottom, IterSpacing.gapMedium)
        }
        .task(id: userID) {
            await model.load()
        }
    }

    private var header: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(verbatim: "Viewing \(model.teammateName)'s stack.")
                .font(IterFont.sansKPIValue)
                .foregroundStyle(Color.iterTextPrimary(for: colorScheme))

            Text(verbatim: "Read-only stack composition shared by a teammate.")
                .font(IterFont.monoSmall)
                .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
        }
    }

    private var simulationAction: some View {
        VStack(alignment: .leading, spacing: IterSpacing.gapSmall) {
            ButtonPrimary(title: model.isCreatingWorktree ? "Creating worktree" : "Use stack in directory") {
                Task { await simulateInDirectory() }
            }
            .disabled(model.isCreatingWorktree)

            Text(verbatim: "Creates a git worktree simulation sandbox. This does not switch your runtime environment.")
                .font(IterFont.monoSmall)
                .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
        }
    }

    private func simulateInDirectory() async {
        guard let destinationRoot = StackWorktreePicker.chooseDirectory(
            title: "Choose simulation parent directory"
        ) else {
            return
        }

        let repoURL: URL
        if let knownRepo = model.knownRepoURL {
            repoURL = knownRepo
        } else if let selectedRepo = StackWorktreePicker.chooseDirectory(title: "Choose repository for git worktree") {
            repoURL = selectedRepo
            model.rememberRepo(selectedRepo)
        } else {
            showToast("Repository path required", kind: .warning)
            return
        }

        do {
            let worktreeURL = try await model.createWorktree(repoURL: repoURL, destinationRoot: destinationRoot)
            NSPasteboard.general.clearContents()
            NSPasteboard.general.setString(worktreeURL.path, forType: .string)
            NSWorkspace.shared.activateFileViewerSelecting([worktreeURL])

            do {
                try await daemonClient.recordStackSimulated(userID: userID, worktreePath: worktreeURL.path)
                showToast("Worktree created and copied", kind: .info)
            } catch {
                showToast("Worktree created; audit unavailable: \(error.localizedDescription)", kind: .warning)
            }
        } catch {
            showToast(error.localizedDescription, kind: .warning)
        }
    }

    private func showToast(_ message: String, kind: StackToastKind) {
        toast = StackToast(message: message, kind: kind)
    }
}

@MainActor
@Observable
private final class StackSimulationModel {
    enum State: Equatable {
        case idle
        case loading
        case loaded(EditableStack)
        case forbidden(String)
        case unavailable(String)
    }

    private enum Storage {
        static let repoPath = "dev.iter.stack.simulationRepoPath"
    }

    var state: State = .idle
    var teammateName = "teammate"
    var isCreatingWorktree = false

    private let userID: UUID
    private let api: StackAPIClient
    private let defaults: UserDefaults

    init(userID: UUID, api: StackAPIClient = StackAPIClient(), defaults: UserDefaults = .standard) {
        self.userID = userID
        self.api = api
        self.defaults = defaults
    }

    var knownRepoURL: URL? {
        guard let path = defaults.string(forKey: Storage.repoPath), !path.isEmpty else {
            return nil
        }
        return URL(fileURLWithPath: path)
    }

    func load() async {
        guard state != .loading else { return }
        state = .loading

        let members = (try? await api.fetchTeam().members.map(Self.teamMember)) ?? []
        if let member = members.first(where: { $0.userID == userID }) {
            teammateName = member.displayName
        }

        do {
            guard let response = try await api.fetchStacks(sharedBy: userID).first else {
                state = .unavailable("No shared stack is available for this teammate.")
                return
            }
            state = .loaded(EditableStack.from(response, members: members))
        } catch StackAPIError.forbidden {
            state = .forbidden("This teammate has not shared their stack with you.")
        } catch StackAPIError.unauthorized {
            state = .forbidden("Sign in to view this teammate's stack.")
        } catch StackAPIError.notFound {
            state = .unavailable("No shared stack is available for this teammate.")
        } catch {
            state = .unavailable(error.localizedDescription)
        }
    }

    func rememberRepo(_ url: URL) {
        defaults.set(url.path, forKey: Storage.repoPath)
    }

    func createWorktree(repoURL: URL, destinationRoot: URL) async throws -> URL {
        guard !isCreatingWorktree else { throw StackSimulationError.alreadyCreating }
        isCreatingWorktree = true
        defer { isCreatingWorktree = false }

        let timestamp = Self.timestamp()
        let worktreeURL = destinationRoot
            .appendingPathComponent("iter-sim-\(userID.uuidString.lowercased())-\(timestamp)", isDirectory: true)
        try await StackWorktreeRunner.addWorktree(repoURL: repoURL, worktreeURL: worktreeURL)
        return worktreeURL
    }

    private static func teamMember(_ dto: StackTeamMemberDTO) -> StackTeamMember {
        StackTeamMember(
            userID: dto.userId,
            displayName: dto.displayName,
            initials: initials(for: dto.displayName),
            avatarSeed: dto.displayName
        )
    }

    private static func initials(for displayName: String) -> String {
        let initials = displayName
            .split(separator: " ")
            .prefix(2)
            .compactMap(\.first)
            .map(String.init)
            .joined()
        return initials.isEmpty ? "U" : initials.uppercased()
    }

    private static func timestamp() -> String {
        let formatter = DateFormatter()
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.dateFormat = "yyyyMMddHHmmss"
        return formatter.string(from: Date())
    }
}

private struct StackSimulateReadOnlyStack: View {
    @Environment(\.colorScheme) private var colorScheme

    let stack: EditableStack

    var body: some View {
        VStack(alignment: .leading, spacing: IterSpacing.sectionGap) {
            readOnlyField(title: "Stack", value: stack.name.isEmpty ? "Untitled stack" : stack.name)

            VStack(alignment: .leading, spacing: IterSpacing.gapSmall) {
                StackSimulateSectionTitle(title: "Harnesses", detail: "\(stack.harnesses.count)")
                HStack(spacing: IterSpacing.gapSmall) {
                    ForEach(stack.harnesses) { harness in
                        StackSimulatePill(title: harness.shortCode, tint: harness.harnessID?.tint.color)
                    }
                }
            }

            StackSimulateList(title: "Skills", values: stack.skills.map(\.encodedValue))
            StackSimulateList(title: "Doc references", values: stack.docs.map(\.value), monospace: true)
            StackSimulateList(title: "Notes", values: stack.notes.isEmpty ? [] : [stack.notes])
        }
    }

    private func readOnlyField(title: String, value: String) -> some View {
        VStack(alignment: .leading, spacing: IterSpacing.gapSmall) {
            StackSimulateSectionTitle(title: title, detail: nil)
            Text(verbatim: value)
                .font(IterFont.sansBody)
                .foregroundStyle(Color.iterTextPrimary(for: colorScheme))
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(.horizontal, 10)
                .frame(minHeight: 34)
                .background(Color.iterPanel(for: colorScheme))
                .clipShape(.rect(cornerRadius: IterRadius.card))
                .overlay {
                    RoundedRectangle(cornerRadius: IterRadius.card)
                        .stroke(Color.iterBorder(for: colorScheme), lineWidth: 1)
                }
        }
    }
}

private struct StackSimulateList: View {
    @Environment(\.colorScheme) private var colorScheme

    let title: String
    let values: [String]
    var monospace = false

    var body: some View {
        VStack(alignment: .leading, spacing: IterSpacing.gapSmall) {
            StackSimulateSectionTitle(title: title, detail: "\(values.count)")

            if values.isEmpty {
                Text(verbatim: "None shared")
                    .font(IterFont.monoSmall)
                    .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(.vertical, 8)
            } else {
                VStack(spacing: 6) {
                    ForEach(values, id: \.self) { value in
                        Text(verbatim: value)
                            .font(monospace ? IterFont.monoSmall : IterFont.sansBody)
                            .foregroundStyle(Color.iterTextPrimary(for: colorScheme))
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .padding(.horizontal, 10)
                            .frame(minHeight: 34, alignment: .leading)
                            .background(Color.iterPanel(for: colorScheme))
                            .clipShape(.rect(cornerRadius: IterRadius.card))
                            .overlay {
                                RoundedRectangle(cornerRadius: IterRadius.card)
                                    .stroke(Color.iterBorder(for: colorScheme), lineWidth: 1)
                            }
                    }
                }
            }
        }
    }
}

private struct StackSimulatePill: View {
    @Environment(\.colorScheme) private var colorScheme

    let title: String
    let tint: Color?

    var body: some View {
        HStack(spacing: 6) {
            RoundedRectangle(cornerRadius: IterRadius.harnessSwatch)
                .fill(tint ?? Color.iterTextTertiary(for: colorScheme))
                .frame(width: 7, height: 7)
                .accessibilityHidden(true)

            Text(verbatim: title)
                .font(IterFont.monoLabel)
                .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
        }
        .padding(.horizontal, 8)
        .frame(height: 26)
        .background(Color.iterPanel(for: colorScheme))
        .clipShape(.rect(cornerRadius: IterRadius.pill))
        .overlay {
            RoundedRectangle(cornerRadius: IterRadius.pill)
                .stroke(tint ?? Color.iterBorder(for: colorScheme), lineWidth: 1)
        }
    }
}

private struct StackSimulateSectionTitle: View {
    @Environment(\.colorScheme) private var colorScheme

    let title: String
    let detail: String?

    var body: some View {
        HStack(spacing: 6) {
            Text(verbatim: title)
                .font(IterFont.sansSectionTitle)
                .foregroundStyle(Color.iterTextPrimary(for: colorScheme))
            if let detail {
                Text(verbatim: detail)
                    .font(IterFont.monoSmall)
                    .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
            }
        }
    }
}

private struct StackSimulateEmptyState: View {
    @Environment(\.colorScheme) private var colorScheme

    let message: String

    var body: some View {
        Text(verbatim: message)
            .font(IterFont.sansBody)
            .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(12)
            .background(Color.iterPanel(for: colorScheme))
            .clipShape(.rect(cornerRadius: IterRadius.card))
            .overlay {
                RoundedRectangle(cornerRadius: IterRadius.card)
                    .stroke(Color.iterBorder(for: colorScheme), lineWidth: 1)
            }
    }
}

private struct StackSimulateToastView: View {
    @Environment(\.colorScheme) private var colorScheme
    let toast: StackToast?

    var body: some View {
        if let toast {
            Text(verbatim: toast.message)
                .font(IterFont.monoLabel)
                .foregroundStyle(
                    toast.kind == .warning ? Color.iterBad(for: colorScheme) : Color.iterTextPrimary(for: colorScheme)
                )
                .padding(.horizontal, 10)
                .frame(height: 28)
                .background(Color.iterPanel(for: colorScheme))
                .clipShape(.rect(cornerRadius: IterRadius.pill))
                .overlay {
                    RoundedRectangle(cornerRadius: IterRadius.pill)
                        .stroke(
                            toast.kind == .warning
                                ? Color.iterBad(for: colorScheme)
                                : Color.iterBorder(for: colorScheme)
                        )
                }
        }
    }
}

private enum StackWorktreePicker {
    @MainActor
    static func chooseDirectory(title: String) -> URL? {
        let panel = NSOpenPanel()
        panel.title = title
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.allowsMultipleSelection = false
        panel.canCreateDirectories = true
        return panel.runModal() == .OK ? panel.url : nil
    }
}

private enum StackWorktreeRunner {
    static func addWorktree(repoURL: URL, worktreeURL: URL) async throws {
        try await Task.detached(priority: .userInitiated) {
            let process = Process()
            process.executableURL = URL(fileURLWithPath: "/usr/bin/git")
            process.currentDirectoryURL = repoURL
            process.arguments = ["worktree", "add", worktreeURL.path, "HEAD"]

            let stderr = Pipe()
            process.standardError = stderr
            try process.run()
            process.waitUntilExit()

            guard process.terminationStatus == 0 else {
                let data = stderr.fileHandleForReading.readDataToEndOfFile()
                let message = String(data: data, encoding: .utf8)?
                    .trimmingCharacters(in: .whitespacesAndNewlines)
                throw StackSimulationError.gitFailed(message ?? "git worktree add failed")
            }
        }.value
    }
}

private enum StackSimulationError: LocalizedError {
    case alreadyCreating
    case gitFailed(String)

    var errorDescription: String? {
        switch self {
        case .alreadyCreating:
            return "Worktree creation already in progress"
        case .gitFailed(let message):
            return message
        }
    }
}
