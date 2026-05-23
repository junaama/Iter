import Foundation
import SwiftUI

struct LoadedSessionDetail {
    let detail: SessionDetailResponse
    let scores: SessionScoresResponse
    let loadedFromArchive: Bool
}

struct SessionDetailResponse: Decodable {
    let session: SessionDetailRow
    let events: SessionEventsPage
    let subagents: SubagentTree
    let outcomes: [SessionOutcomeDetail]
}

struct SessionDetailRow: Decodable {
    let id: String
    let tenantId: String
    let userId: String
    let parentSessionId: String?
    let harness: String
    let model: String
    let effort: String?
    let tools: [String]
    let repoHash: String?
    let gitBranch: String?
    let startedAt: Date
    let endedAt: Date?
    let wallTimeMs: Int?
    let turnCount: Int?
    let totalTokensIn: Int?
    let totalTokensOut: Int?
    let redactedPrompt: String
    let redactedSystem: String?
    let classification: String
    let ingestedAt: Date
    let archivedAt: Date?
}

struct SessionEventsPage: Decodable {
    let items: [SessionEventDetail]
    let nextCursor: String?
}

struct SessionEventDetail: Decodable {
    let id: Int
    let sessionId: String
    let tenantId: String
    let eventType: String
    let payload: [String: JSONValue]
    let occurredAt: Date
}

struct SubagentTree: Decodable {
    let items: [SubagentSessionNode]
    let truncated: Bool
}

struct SubagentSessionNode: Decodable {
    let session: SessionDetailRow
    let depth: Int
    let children: [SubagentSessionNode]
}

struct SessionOutcomeDetail: Decodable {
    let id: String
    let sessionId: String
    let tenantId: String
    let outcomeType: String
    let externalRef: String?
    let details: [String: JSONValue]
    let observedAt: Date
}

struct SessionScoresResponse: Decodable {
    let sessionId: String
    let scores: [SessionScoreDetail]
}

struct SessionScoreDetail: Decodable {
    let id: String
    let sessionId: String
    let tenantId: String
    let scorerVersion: String
    let compositeScore: Double
    let signals: [String: JSONValue]
    let contributorWeight: Double
    let scoredAt: Date
    let rationale: String?
}

enum JSONValue: Decodable, Equatable {
    case string(String)
    case number(Double)
    case bool(Bool)
    case object([String: JSONValue])
    case array([JSONValue])
    case null

    init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        if container.decodeNil() {
            self = .null
        } else if let value = try? container.decode(Bool.self) {
            self = .bool(value)
        } else if let value = try? container.decode(Double.self) {
            self = .number(value)
        } else if let value = try? container.decode(String.self) {
            self = .string(value)
        } else if let value = try? container.decode([JSONValue].self) {
            self = .array(value)
        } else {
            self = .object(try container.decode([String: JSONValue].self))
        }
    }

    var stringValue: String? {
        if case .string(let value) = self { return value }
        return nil
    }

    var doubleValue: Double? {
        switch self {
        case .number(let value):
            return value
        case .string(let value):
            return Double(value)
        default:
            return nil
        }
    }

    var stringArrayValue: [String]? {
        if case .array(let values) = self {
            return values.compactMap(\.stringValue)
        }
        return nil
    }

    var displayValue: String {
        switch self {
        case .string(let value):
            return value
        case .number(let value):
            if value.rounded() == value {
                return "\(Int(value))"
            }
            return String(format: "%.2f", value)
        case .bool(let value):
            return value ? "true" : "false"
        case .array(let values):
            let preview = values.prefix(3).map(\.displayValue).joined(separator: ", ")
            return values.count > 3 ? "\(preview), ..." : preview
        case .object:
            return "object"
        case .null:
            return "--"
        }
    }
}

struct SessionDetailClient {
    private let baseURL: URL
    private let token: String?
    private let urlSession: URLSession

    init(
        baseURL: URL = DashboardAPIConfig.current.baseURL,
        token: String? = DashboardAPIConfig.current.token,
        urlSession: URLSession = .shared
    ) {
        self.baseURL = baseURL
        self.token = token
        self.urlSession = urlSession
    }

    func load(sessionID: String) async throws -> LoadedSessionDetail {
        async let detail: SessionDetailResponse = get(SessionDetailResponse.self, path: ["v1", "sessions", sessionID])
        async let scores: SessionScoresResponse = get(SessionScoresResponse.self, path: ["v1", "scores", sessionID])
        let (detailResponse, scoresResponse) = try await (detail, scores)
        return LoadedSessionDetail(
            detail: detailResponse,
            scores: scoresResponse,
            loadedFromArchive: detailResponse.session.archivedAt != nil
        )
    }

    private func get<T: Decodable>(_ type: T.Type, path: [String]) async throws -> T {
        var url = baseURL
        for part in path {
            url.appendPathComponent(part)
        }

        var request = URLRequest(url: url)
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        if let token, !token.isEmpty {
            request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        }

        let (data, response) = try await urlSession.data(for: request)
        guard let http = response as? HTTPURLResponse else {
            throw SessionDetailError.invalidResponse
        }

        switch http.statusCode {
        case 200..<300:
            return try Self.decoder.decode(type, from: data)
        case 401, 403:
            throw SessionDetailError.unauthorized
        case 404:
            throw SessionDetailError.notFound
        default:
            throw SessionDetailError.http(status: http.statusCode)
        }
    }

    private static let decoder: JSONDecoder = {
        let decoder = JSONDecoder()
        decoder.keyDecodingStrategy = .convertFromSnakeCase
        decoder.dateDecodingStrategy = .custom { decoder in
            let container = try decoder.singleValueContainer()
            let value = try container.decode(String.self)
            if let date = IterAPIDateParser.parse(value) {
                return date
            }
            throw DecodingError.dataCorruptedError(
                in: container,
                debugDescription: "Invalid ISO8601 date: \(value)"
            )
        }
        return decoder
    }()
}

struct DashboardAPIConfig {
    let baseURL: URL
    let token: String?

    static var current: DashboardAPIConfig {
        let env = ProcessInfo.processInfo.environment
        let defaults = UserDefaults.standard
        let rawBaseURL = env["ITER_API_BASE_URL"]
            ?? defaults.string(forKey: "iter.apiBaseURL")
            ?? "https://staging.iter.dev"
        let url = URL(string: rawBaseURL) ?? URL(string: "https://staging.iter.dev")!
        return DashboardAPIConfig(
            baseURL: url,
            token: env["ITER_AUTH_TOKEN"]
                ?? env["ITER_API_TOKEN"]
                ?? defaults.string(forKey: "iter.authToken")
        )
    }
}

enum SessionDetailError: LocalizedError, Equatable {
    case invalidResponse
    case unauthorized
    case notFound
    case http(status: Int)

    var errorDescription: String? {
        switch self {
        case .invalidResponse:
            return "The server returned an invalid response."
        case .unauthorized:
            return "Sign in to load this session."
        case .notFound:
            return "Session not found or you don't have access."
        case .http(let status):
            return "Session detail failed with HTTP \(status)."
        }
    }
}

private enum IterAPIDateParser {
    static func parse(_ value: String) -> Date? {
        fractional.date(from: value) ?? plain.date(from: value)
    }

    private static let fractional: ISO8601DateFormatter = {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return formatter
    }()

    private static let plain: ISO8601DateFormatter = {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime]
        return formatter
    }()
}
