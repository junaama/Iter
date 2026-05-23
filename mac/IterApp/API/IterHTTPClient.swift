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
    private var dashboardMeCache: CacheEntry<DashboardMeResponse>?

    init(
        session: URLSession = .shared,
        baseURL: URL = IterHTTPClient.defaultBaseURL(),
        bearerToken: String? = IterHTTPClient.defaultBearerToken()
    ) {
        self.session = session
        self.baseURL = baseURL
        self.bearerToken = bearerToken
    }

    convenience init<SessionStoreType>(
        sessionStore _: SessionStoreType,
        session: URLSession = .shared,
        baseURL: URL = IterHTTPClient.defaultBaseURL(),
        bearerToken: String? = IterHTTPClient.defaultBearerToken()
    ) {
        self.init(session: session, baseURL: baseURL, bearerToken: bearerToken)
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
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        if let bearerToken, !bearerToken.isEmpty {
            request.setValue("Bearer \(bearerToken)", forHTTPHeaderField: "Authorization")
        }

        let (data, response) = try await session.data(for: request)
        guard let httpResponse = response as? HTTPURLResponse else {
            throw IterHTTPClientError.invalidResponse
        }
        guard (200..<300).contains(httpResponse.statusCode) else {
            let message = Self.decodeErrorMessage(from: data)
                ?? HTTPURLResponse.localizedString(forStatusCode: httpResponse.statusCode)
            throw IterHTTPClientError.http(status: httpResponse.statusCode, message: message)
        }

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
        ProcessInfo.processInfo.environment["ITER_API_TOKEN"]
            ?? UserDefaults.standard.string(forKey: "iter.api.token")
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
        }
    }
}
