import Foundation

struct StackPayloadDTO: Codable, Equatable {
    var name: String
    var harnesses: [String]
    var skills: [String]
    var docs: [String]
    var notes: String?
}

struct StackShareResponseDTO: Codable, Equatable {
    var sharedWithUserId: UUID
    var sharedAt: String
}

struct StackResponseDTO: Codable, Equatable {
    var id: UUID
    var userId: UUID
    var payload: StackPayloadDTO
    var classification: String
    var createdAt: String
    var updatedAt: String
    var shares: [StackShareResponseDTO]

    enum CodingKeys: String, CodingKey {
        case id
        case userId
        case payload
        case classification
        case createdAt
        case updatedAt
        case shares
    }

    init(
        id: UUID,
        userId: UUID,
        payload: StackPayloadDTO,
        classification: String,
        createdAt: String,
        updatedAt: String,
        shares: [StackShareResponseDTO] = []
    ) {
        self.id = id
        self.userId = userId
        self.payload = payload
        self.classification = classification
        self.createdAt = createdAt
        self.updatedAt = updatedAt
        self.shares = shares
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        id = try container.decode(UUID.self, forKey: .id)
        userId = try container.decode(UUID.self, forKey: .userId)
        payload = try container.decode(StackPayloadDTO.self, forKey: .payload)
        classification = try container.decode(String.self, forKey: .classification)
        createdAt = try container.decode(String.self, forKey: .createdAt)
        updatedAt = try container.decode(String.self, forKey: .updatedAt)
        shares = try container.decodeIfPresent([StackShareResponseDTO].self, forKey: .shares) ?? []
    }
}

struct StackShareRequestDTO: Codable {
    var sharedWithUserId: UUID
    var includedDocs: [String]
}

struct StackTeamResponseDTO: Codable {
    var members: [StackTeamMemberDTO]
}

struct StackTeamMemberDTO: Codable {
    var userId: UUID
    var displayName: String
    var sessionCount30d: Int
    var meanCompositeScore30d: Double?
}

enum StackAPIError: LocalizedError {
    case invalidURL
    case notFound
    case unauthorized
    case forbidden
    case serverStatus(Int)
    case emptyResponse

    var errorDescription: String? {
        switch self {
        case .invalidURL:
            return "Invalid API URL"
        case .notFound:
            return "Stack not saved"
        case .unauthorized:
            return "Sign in required"
        case .forbidden:
            return "This teammate has not shared their stack with you."
        case .serverStatus(let status):
            return "Server returned \(status)"
        case .emptyResponse:
            return "Empty server response"
        }
    }
}

struct StackAPIClient {
    var baseURL: URL = StackAPIClient.defaultBaseURL()
    var session: URLSession = .shared
    var tokenProvider: () -> String? = StackAPIClient.defaultToken

    func fetchMyStacks() async throws -> [StackResponseDTO] {
        let request = try makeRequest(path: "/v1/stack/me", method: "GET")
        let data = try await data(for: request, acceptedStatuses: [200])
        return try decodeStackList(data)
    }

    func fetchStacks(sharedBy userID: UUID) async throws -> [StackResponseDTO] {
        let request = try makeRequest(path: "/v1/stack/\(userID.uuidString)", method: "GET")
        let data = try await data(for: request, acceptedStatuses: [200])
        return try decodeStackList(data)
    }

    func fetchTeam() async throws -> StackTeamResponseDTO {
        let request = try makeRequest(path: "/v1/dashboard/team?member_limit=20&pattern_limit=1", method: "GET")
        let data = try await data(for: request, acceptedStatuses: [200])
        return try decoder.decode(StackTeamResponseDTO.self, from: data)
    }

    func saveStack(_ payload: StackPayloadDTO) async throws -> StackResponseDTO {
        var request = try makeRequest(path: "/v1/stack", method: "POST")
        request.setValue(UUID().uuidString, forHTTPHeaderField: "Idempotency-Key")
        request.httpBody = try encoder.encode(payload)
        let data = try await data(for: request, acceptedStatuses: [200, 201])
        return try decoder.decode(StackResponseDTO.self, from: data)
    }

    func share(stackID: UUID, with userID: UUID, includedDocs: [String]) async throws {
        var request = try makeRequest(path: "/v1/stack/\(stackID.uuidString)/share", method: "POST")
        request.setValue(UUID().uuidString, forHTTPHeaderField: "Idempotency-Key")
        request.httpBody = try encoder.encode(StackShareRequestDTO(
            sharedWithUserId: userID,
            includedDocs: includedDocs
        ))
        _ = try await data(for: request, acceptedStatuses: [200])
    }

    func revoke(stackID: UUID, userID: UUID) async throws {
        let path = "/v1/stack/\(stackID.uuidString)/share/\(userID.uuidString)"
        let request = try makeRequest(path: path, method: "DELETE")
        _ = try await data(for: request, acceptedStatuses: [204])
    }

    private func makeRequest(path: String, method: String) throws -> URLRequest {
        guard let url = URL(string: path, relativeTo: baseURL) else {
            throw StackAPIError.invalidURL
        }
        var request = URLRequest(url: url)
        request.httpMethod = method
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        if let token = tokenProvider(), !token.isEmpty {
            request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        }
        return request
    }

    private func data(for request: URLRequest, acceptedStatuses: Set<Int>) async throws -> Data {
        let (data, response) = try await session.data(for: request)
        guard let httpResponse = response as? HTTPURLResponse else {
            throw StackAPIError.emptyResponse
        }
        if httpResponse.statusCode == 404 {
            throw StackAPIError.notFound
        }
        if httpResponse.statusCode == 401 {
            throw StackAPIError.unauthorized
        }
        if httpResponse.statusCode == 403 {
            throw StackAPIError.forbidden
        }
        guard acceptedStatuses.contains(httpResponse.statusCode) else {
            throw StackAPIError.serverStatus(httpResponse.statusCode)
        }
        return data
    }

    private func decodeStackList(_ data: Data) throws -> [StackResponseDTO] {
        if let list = try? decoder.decode([StackResponseDTO].self, from: data) {
            return list
        }
        return [try decoder.decode(StackResponseDTO.self, from: data)]
    }

    private var decoder: JSONDecoder {
        let decoder = JSONDecoder()
        decoder.keyDecodingStrategy = .convertFromSnakeCase
        return decoder
    }

    private var encoder: JSONEncoder {
        let encoder = JSONEncoder()
        encoder.keyEncodingStrategy = .convertToSnakeCase
        return encoder
    }

    static func defaultBaseURL() -> URL {
        if let rawValue = ProcessInfo.processInfo.environment["ITER_API_BASE_URL"],
           let url = URL(string: rawValue) {
            return url
        }
        return URL(string: "http://127.0.0.1:8080")!
    }

    static func defaultToken() -> String? {
        if let env = ProcessInfo.processInfo.environment["ITER_AUTH_TOKEN"], !env.isEmpty {
            return env
        }
        if let stored = UserDefaults.standard.string(forKey: "iter.authToken"), !stored.isEmpty {
            return stored
        }
        // Fall back to the Keychain — that's where SessionStore persists
        // the WorkOS access token after device-code sign-in. Without this
        // every Stack request goes out without an Authorization header.
        return try? TokenKeychainStore().load()?.accessToken
    }
}
