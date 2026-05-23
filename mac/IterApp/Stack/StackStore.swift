import Foundation
import Observation

enum StackLoadState: Equatable {
    case idle
    case loading
    case draft
    case loaded
    case offlineDraft(String)
}

enum StackToastKind: Equatable {
    case info
    case warning
}

struct StackToast: Identifiable, Equatable {
    var id = UUID()
    var message: String
    var kind: StackToastKind
}

enum StackShareTarget {
    case team
    case user(StackTeamMember)
}

@MainActor
@Observable
final class StackStore {
    var stack = EditableStack.detectedDraft()
    var loadState: StackLoadState = .idle
    var isSaving = false
    var isSharing = false
    var toast: StackToast?
    var pendingSkillName = ""
    var pendingSkillSource = ""
    var pendingDocReference = ""
    var teamMembers: [StackTeamMember] = StackStore.sampleTeamMembers
    var sharedWithMe: [SharedStackSummary] = []

    private let api: StackAPIClient

    init(api: StackAPIClient = StackAPIClient()) {
        self.api = api
    }

    var statusLabel: String {
        switch loadState {
        case .idle, .loading:
            return "loading"
        case .draft:
            return "draft"
        case .loaded:
            return stack.isDraft ? "draft" : "saved"
        case .offlineDraft:
            return "local draft"
        }
    }

    var sidebarHarnessLabels: [String] {
        let labels = stack.harnesses.map(\.shortCode)
        return labels.isEmpty ? ["cx"] : Array(labels.prefix(4))
    }

    func load() async {
        guard loadState != .loading else { return }
        loadState = .loading

        if let teamResponse = try? await api.fetchTeam() {
            teamMembers = teamResponse.members.map(Self.teamMember)
        }

        do {
            let stacks = try await api.fetchMyStacks()
            if let current = stacks.first {
                stack = EditableStack.from(current, members: teamMembers)
                loadState = .loaded
            } else {
                stack = EditableStack.detectedDraft()
                loadState = .draft
            }
        } catch StackAPIError.notFound {
            stack = EditableStack.detectedDraft()
            loadState = .draft
        } catch {
            if stack.id == nil {
                stack = EditableStack.detectedDraft()
            }
            loadState = .offlineDraft(error.localizedDescription)
        }

        await loadSharedWithMe()
    }

    func addSkill() {
        let name = pendingSkillName.trimmingCharacters(in: .whitespacesAndNewlines)
        let source = pendingSkillSource.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !name.isEmpty else { return }
        stack.skills.append(StackSkill(name: name, sourcePath: source))
        pendingSkillName = ""
        pendingSkillSource = ""
    }

    func removeSkill(_ skill: StackSkill) {
        stack.skills.removeAll { $0.id == skill.id }
    }

    func addDocReference() {
        let value = pendingDocReference.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !value.isEmpty else { return }
        guard !SecretPathPolicy.isBlocked(value) else {
            showToast("blocked: secrets-shaped path", kind: .warning)
            return
        }
        stack.docs.append(StackDocReference(value: value))
        pendingDocReference = ""
    }

    func removeDocReference(_ reference: StackDocReference) {
        stack.docs.removeAll { $0.id == reference.id }
    }

    func save() async {
        guard !isSaving else { return }
        guard !stack.payload.name.isEmpty else {
            showToast("Stack name required", kind: .warning)
            return
        }
        guard !stack.payload.harnesses.isEmpty else {
            showToast("At least one harness required", kind: .warning)
            return
        }
        guard stack.payload.docs.allSatisfy({ !SecretPathPolicy.isBlocked($0) }) else {
            showToast("blocked: secrets-shaped path", kind: .warning)
            return
        }

        isSaving = true
        defer { isSaving = false }

        do {
            let saved = try await api.saveStack(stack.payload)
            stack = EditableStack.from(saved, members: teamMembers)
            loadState = .loaded
            showToast("Stack saved", kind: .info)
        } catch {
            showToast(error.localizedDescription, kind: .warning)
        }
    }

    func share(target: StackShareTarget, includedDocs: [String]) async {
        guard !isSharing else { return }
        guard let stackID = stack.id else {
            showToast("Save stack before sharing", kind: .warning)
            return
        }
        guard includedDocs.allSatisfy({ !SecretPathPolicy.isBlocked($0) }) else {
            showToast("blocked: secrets-shaped path", kind: .warning)
            return
        }

        isSharing = true
        defer { isSharing = false }

        do {
            switch target {
            case .team:
                let targets = teamMembers.filter { $0.userID != stack.userID }
                for member in targets {
                    try await api.share(stackID: stackID, with: member.userID, includedDocs: includedDocs)
                }
                upsertGrant(StackShareGrant(
                    scope: .team,
                    displayName: "Whole team",
                    initials: "TM",
                    avatarSeed: "team"
                ))
            case .user(let member):
                try await api.share(stackID: stackID, with: member.userID, includedDocs: includedDocs)
                upsertGrant(StackShareGrant(
                    scope: .user(member.userID),
                    displayName: member.displayName,
                    initials: member.initials,
                    avatarSeed: member.avatarSeed
                ))
            }
            showToast("Share saved", kind: .info)
        } catch {
            showToast(error.localizedDescription, kind: .warning)
        }
    }

    func revoke(_ grant: StackShareGrant) async {
        guard let stackID = stack.id else { return }
        do {
            switch grant.scope {
            case .team:
                for member in teamMembers where member.userID != stack.userID {
                    try? await api.revoke(stackID: stackID, userID: member.userID)
                }
            case .user(let userID):
                try await api.revoke(stackID: stackID, userID: userID)
            }
            stack.shareGrants.removeAll { $0.id == grant.id }
            showToast("Share revoked", kind: .info)
        } catch {
            showToast(error.localizedDescription, kind: .warning)
        }
    }

    func showToast(_ message: String, kind: StackToastKind) {
        toast = StackToast(message: message, kind: kind)
    }

    private func loadSharedWithMe() async {
        var summaries: [SharedStackSummary] = []
        for member in teamMembers where member.userID != stack.userID {
            guard let shared = try? await api.fetchStacks(sharedBy: member.userID),
                  let first = shared.first else {
                continue
            }
            summaries.append(SharedStackSummary(
                userID: member.userID,
                displayName: member.displayName,
                stackName: first.payload.name,
                initials: member.initials,
                avatarSeed: member.avatarSeed
            ))
        }
        sharedWithMe = summaries.isEmpty ? Self.sampleSharedWithMe : summaries
    }

    private func upsertGrant(_ grant: StackShareGrant) {
        stack.shareGrants.removeAll { $0.id == grant.id }
        stack.shareGrants.insert(grant, at: 0)
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

    static let sampleTeamMembers = [
        StackTeamMember(
            userID: UUID(uuidString: "11111111-1111-4111-8111-111111111111")!,
            displayName: "Mina Chen",
            initials: "MC",
            avatarSeed: "mchen"
        ),
        StackTeamMember(
            userID: UUID(uuidString: "22222222-2222-4222-8222-222222222222")!,
            displayName: "Ana Yusuf",
            initials: "AY",
            avatarSeed: "ana"
        ),
        StackTeamMember(
            userID: UUID(uuidString: "33333333-3333-4333-8333-333333333333")!,
            displayName: "Lena Ito",
            initials: "LI",
            avatarSeed: "lena"
        )
    ]

    private static let sampleSharedWithMe = [
        SharedStackSummary(
            userID: UUID(uuidString: "44444444-4444-4444-8444-444444444444")!,
            displayName: "Mina Chen",
            stackName: "Webhook verifier",
            initials: "MC",
            avatarSeed: "mchen"
        ),
        SharedStackSummary(
            userID: UUID(uuidString: "55555555-5555-4555-8555-555555555555")!,
            displayName: "Lena Ito",
            stackName: "SwiftUI polish",
            initials: "LI",
            avatarSeed: "lena"
        )
    ]
}
