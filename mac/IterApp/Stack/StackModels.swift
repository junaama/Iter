import Foundation

enum StackHarnessSource: String {
    case detected
    case explicit
}

struct StackHarness: Identifiable, Equatable {
    var code: String
    var source: StackHarnessSource

    var id: String { code }

    var harnessID: HarnessID? {
        HarnessID(rawValue: code)
    }

    var shortCode: String {
        harnessID?.tint.shortCode ?? String(code.prefix(2)).lowercased()
    }
}

struct StackSkill: Identifiable, Equatable {
    var id = UUID()
    var name: String
    var sourcePath: String

    var encodedValue: String {
        let trimmedName = name.trimmingCharacters(in: .whitespacesAndNewlines)
        let trimmedSource = sourcePath.trimmingCharacters(in: .whitespacesAndNewlines)
        if trimmedSource.isEmpty {
            return trimmedName
        }
        return "\(trimmedName) :: \(trimmedSource)"
    }

    static func decode(_ value: String) -> StackSkill {
        let parts = value.components(separatedBy: " :: ")
        if parts.count >= 2 {
            return StackSkill(name: parts[0], sourcePath: parts.dropFirst().joined(separator: " :: "))
        }
        return StackSkill(name: value, sourcePath: "")
    }
}

struct StackDocReference: Identifiable, Equatable {
    var id = UUID()
    var value: String
}

enum StackShareScope: Equatable {
    case team
    case user(UUID)
}

struct StackShareGrant: Identifiable, Equatable {
    var scope: StackShareScope
    var displayName: String
    var initials: String
    var avatarSeed: String

    var id: String {
        switch scope {
        case .team:
            return "team"
        case .user(let userID):
            return userID.uuidString
        }
    }
}

struct StackTeamMember: Identifiable, Equatable {
    var userID: UUID
    var displayName: String
    var initials: String
    var avatarSeed: String

    var id: UUID { userID }
}

struct SharedStackSummary: Identifiable, Equatable {
    var userID: UUID
    var displayName: String
    var stackName: String
    var initials: String
    var avatarSeed: String

    var id: UUID { userID }
}

struct EditableStack: Equatable {
    var id: UUID?
    var userID: UUID?
    var name: String
    var harnesses: [StackHarness]
    var skills: [StackSkill]
    var docs: [StackDocReference]
    var notes: String
    var classification: String
    var shareGrants: [StackShareGrant]
    var isDraft: Bool

    var payload: StackPayloadDTO {
        StackPayloadDTO(
            name: name.trimmingCharacters(in: .whitespacesAndNewlines),
            harnesses: harnesses.map(\.code),
            skills: skills.map(\.encodedValue).filter { !$0.isEmpty },
            docs: docs.map { $0.value.trimmingCharacters(in: .whitespacesAndNewlines) }.filter { !$0.isEmpty },
            notes: notes.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? nil : notes
        )
    }

    var fileReferenceValues: [String] {
        docs.map(\.value).filter { !$0.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }
    }

    static func empty() -> EditableStack {
        EditableStack(
            id: nil,
            userID: nil,
            name: "",
            harnesses: [],
            skills: [],
            docs: [],
            notes: "",
            classification: "clean",
            shareGrants: [],
            isDraft: true
        )
    }

    static func from(_ response: StackResponseDTO, members: [StackTeamMember]) -> EditableStack {
        let memberByID = Dictionary(uniqueKeysWithValues: members.map { ($0.userID, $0) })
        return EditableStack(
            id: response.id,
            userID: response.userId,
            name: response.payload.name,
            harnesses: response.payload.harnesses.map { StackHarness(code: $0, source: .explicit) },
            skills: response.payload.skills.map(StackSkill.decode),
            docs: response.payload.docs.map { StackDocReference(value: $0) },
            notes: response.payload.notes ?? "",
            classification: response.classification,
            shareGrants: response.shares.map { share in
                if let member = memberByID[share.sharedWithUserId] {
                    return StackShareGrant(
                        scope: .user(member.userID),
                        displayName: member.displayName,
                        initials: member.initials,
                        avatarSeed: member.avatarSeed
                    )
                }
                let suffix = String(share.sharedWithUserId.uuidString.prefix(8))
                return StackShareGrant(
                    scope: .user(share.sharedWithUserId),
                    displayName: "User \(suffix)",
                    initials: "U",
                    avatarSeed: suffix
                )
            },
            isDraft: false
        )
    }
}

enum SecretPathPolicy {
    static func isBlocked(_ rawValue: String) -> Bool {
        let trimmed = rawValue.trimmingCharacters(in: .whitespacesAndNewlines)
            .trimmingCharacters(in: CharacterSet(charactersIn: "/"))
            .lowercased()
        guard !trimmed.isEmpty else { return false }
        let base = URL(fileURLWithPath: trimmed).lastPathComponent
        return base.hasPrefix(".env") ||
            base.hasSuffix(".key") ||
            base.hasSuffix(".pem") ||
            base.hasPrefix("credentials")
    }
}
