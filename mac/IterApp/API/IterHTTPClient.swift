import Foundation
import Observation

@Observable
final class IterHTTPClient {
    static let dashboardCacheTTL: TimeInterval = 30

    private struct CacheEntry<Value> {
        let value: Value
        let fetchedAt: Date
    }

    private let session: URLSession
    private let baseURL: URL
    private let bearerToken: String?
    private let sessionStore: SessionStore?
    private var dashboardMeCache: CacheEntry<DashboardMeResponse>?

    init(
        session: URLSession = .shared,
        baseURL: URL = IterHTTPClient.defaultBaseURL(),
        bearerToken: String? = IterHTTPClient.defaultBearerToken(),
        sessionStore: SessionStore? = nil
    ) {
        self.session = session
        self.baseURL = baseURL
        self.bearerToken = bearerToken
        self.sessionStore = sessionStore
    }

    func dashboardMe(forceRefresh: Bool = false) async throws -> DashboardMeResponse {
        if !forceRefresh,
           let dashboardMeCache,
           Date().timeIntervalSince(dashboardMeCache.fetchedAt) < Self.dashboardCacheTTL {
            return dashboardMeCache.value
        }

        let response: DashboardMeResponse = try await get(
            path: "v1/dashboard/me",
            queryItems: [
                URLQueryItem(name: "days", value: "30"),
                URLQueryItem(name: "limit", value: "10")
            ]
        )
        dashboardMeCache = CacheEntry(value: response, fetchedAt: Date())
        return response
    }

    func data(
        for request: URLRequest,
        method: String? = nil,
        body: Data? = nil
    ) async throws -> (Data, HTTPURLResponse) {
        var request = request
        if let method {
            request.httpMethod = method
        }
        if let body {
            request.httpBody = body
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        request = try await authorize(request)

        let firstResponse = try await perform(request)
        if firstResponse.1.statusCode != 401 || sessionStore == nil {
            try validate(firstResponse)
            return firstResponse
        }

        guard let sessionStore, await sessionStore.refreshIfNeeded(force: true) else {
            throw IterHTTPClientError.sessionExpired
        }

        request = try await authorize(request)
        let retryResponse = try await perform(request)
        if retryResponse.1.statusCode == 401 {
            await sessionStore.signOut(expired: true)
            throw IterHTTPClientError.sessionExpired
        }
        try validate(retryResponse)
        return retryResponse
    }

    private func authorize(_ request: URLRequest) async throws -> URLRequest {
        var request = request
        guard request.url?.path.hasPrefix("/v1/") == true else {
            return request
        }

        let token: String?
        if let sessionStore {
            guard await sessionStore.refreshIfNeeded() else {
                throw IterHTTPClientError.sessionExpired
            }
            token = await sessionStore.accessToken
        } else {
            token = bearerToken
        }

        if let token, !token.isEmpty {
            request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        }
        return request
    }

    private func perform(_ request: URLRequest) async throws -> (Data, HTTPURLResponse) {
        let (data, response) = try await session.data(for: request)
        guard let httpResponse = response as? HTTPURLResponse else {
            throw IterHTTPClientError.invalidResponse
        }
        return (data, httpResponse)
    }

    private func validate(_ response: (Data, HTTPURLResponse)) throws {
        guard (200..<300).contains(response.1.statusCode) else {
            let message = Self.decodeErrorMessage(from: response.0)
                ?? HTTPURLResponse.localizedString(forStatusCode: response.1.statusCode)
            throw IterHTTPClientError.http(status: response.1.statusCode, message: message)
        }
    }

    private func get<Response: Decodable>(path: String, queryItems: [URLQueryItem] = []) async throws -> Response {
        let endpoint = baseURL.appendingPathComponent(path)
        guard var components = URLComponents(url: endpoint, resolvingAgainstBaseURL: false) else {
            throw IterHTTPClientError.invalidURL
        }
        components.queryItems = queryItems.isEmpty ? nil : queryItems

        guard let url = components.url else {
            throw IterHTTPClientError.invalidURL
        }

        var request = URLRequest(url: url)
        request.httpMethod = "GET"
        let (data, _) = try await data(for: request)
        do {
            return try Self.decoder.decode(Response.self, from: data)
        } catch {
            throw IterHTTPClientError.decode(error.localizedDescription)
        }
    }

    private static var decoder: JSONDecoder {
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .custom { decoder in
            let container = try decoder.singleValueContainer()
            let string = try container.decode(String.self)
            if let date = fractionalISO8601.date(from: string) ?? ISO8601DateFormatter().date(from: string) {
                return date
            }
            throw DecodingError.dataCorruptedError(in: container, debugDescription: "Invalid ISO8601 date")
        }
        return decoder
    }

    private static let fractionalISO8601: ISO8601DateFormatter = {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return formatter
    }()

    private static func decodeErrorMessage(from data: Data) -> String? {
        guard !data.isEmpty else { return nil }
        return try? decoder.decode(IterAPIErrorEnvelope.self, from: data).error
    }

    private static func defaultBaseURL() -> URL {
        if let configured = ProcessInfo.processInfo.environment["ITER_API_BASE_URL"].flatMap(URL.init(string:)) {
            return configured
        }
        if let configured = UserDefaults.standard.string(forKey: "iter.api.baseURL").flatMap(URL.init(string:)) {
            return configured
        }
        return URL(string: "http://127.0.0.1:8080")!
    }

    private static func defaultBearerToken() -> String? {
        if let env = ProcessInfo.processInfo.environment["ITER_API_TOKEN"], !env.isEmpty {
            return env
        }
        if let stored = UserDefaults.standard.string(forKey: "iter.api.token"), !stored.isEmpty {
            return stored
        }
        // Fall back to the Iter session JWT in the Keychain. SessionStore
        // persists it there after the WorkOS → Iter exchange completes,
        // so a client constructed without an explicit sessionStore still
        // authenticates the signed-in user.
        return try? TokenKeychainStore().load()?.accessToken
    }
}

private struct IterAPIErrorEnvelope: Decodable {
    let error: String?
}

enum IterHTTPClientError: LocalizedError {
    case invalidURL
    case invalidResponse
    case http(status: Int, message: String)
    case decode(String)
    case sessionExpired

    var errorDescription: String? {
        switch self {
        case .invalidURL:
            return "Invalid dashboard API URL"
        case .invalidResponse:
            return "Invalid dashboard API response"
        case .http(let status, let message):
            return "Dashboard API returned \(status): \(message)"
        case .decode(let message):
            return "Dashboard API response could not be decoded: \(message)"
        case .sessionExpired:
            return "Session expired, sign in again."
        }
    }
}
