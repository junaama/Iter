import Foundation

struct DeviceAuthorization: Equatable {
    let deviceCode: String
    let userCode: String
    let verificationURI: URL
    let verificationURIComplete: URL
    let expiresIn: TimeInterval
    let interval: TimeInterval
    let issuedAt: Date

    var expiresAt: Date {
        issuedAt.addingTimeInterval(expiresIn)
    }
}

struct WorkOSTokenResponse: Equatable {
    let accessToken: String
    let refreshToken: String
    let idToken: String?
    let tokenType: String
    let expiresIn: TimeInterval
}

struct WorkOSDeviceAuthClient {
    private let configuration: AuthConfiguration
    private let session: URLSession
    private let now: () -> Date

    init(
        configuration: AuthConfiguration = .fromEnvironment(),
        session: URLSession = .shared,
        now: @escaping () -> Date = Date.init
    ) {
        self.configuration = configuration
        self.session = session
        self.now = now
    }

    func authorizeDevice() async throws -> DeviceAuthorization {
        guard configuration.isConfigured else { throw WorkOSAuthError.missingClientID }

        let data = try await postForm(
            to: configuration.deviceAuthorizationURL,
            fields: ["client_id": configuration.clientID]
        )
        let payload = try JSONDecoder().decode(DeviceAuthorizationPayload.self, from: data)
        guard let verificationURI = URL(string: payload.verificationURI),
              let verificationURIComplete = URL(string: payload.verificationURIComplete) else {
            throw WorkOSAuthError.invalidResponse
        }

        return DeviceAuthorization(
            deviceCode: payload.deviceCode,
            userCode: payload.userCode,
            verificationURI: verificationURI,
            verificationURIComplete: verificationURIComplete,
            expiresIn: TimeInterval(payload.expiresIn),
            interval: TimeInterval(payload.interval),
            issuedAt: now()
        )
    }

    func pollForTokens(_ authorization: DeviceAuthorization) async throws -> WorkOSTokenResponse {
        var delay = max(authorization.interval, 1)

        while now() < authorization.expiresAt {
            do {
                return try await requestToken(fields: [
                    "grant_type": "urn:ietf:params:oauth:grant-type:device_code",
                    "device_code": authorization.deviceCode,
                    "client_id": configuration.clientID
                ])
            } catch WorkOSAuthError.authorizationPending {
                try await sleep(seconds: delay)
                delay = min(delay * 1.5, 15)
            } catch WorkOSAuthError.slowDown {
                delay = min(delay + 5, 20)
                try await sleep(seconds: delay)
            }
        }

        throw WorkOSAuthError.authorizationTimedOut
    }

    func refresh(refreshToken: String) async throws -> WorkOSTokenResponse {
        guard configuration.isConfigured else { throw WorkOSAuthError.missingClientID }
        return try await requestToken(fields: [
            "grant_type": "refresh_token",
            "refresh_token": refreshToken,
            "client_id": configuration.clientID
        ])
    }

    private func requestToken(fields: [String: String]) async throws -> WorkOSTokenResponse {
        let data = try await postForm(to: configuration.tokenURL, fields: fields)
        let payload = try JSONDecoder().decode(TokenPayload.self, from: data)
        return WorkOSTokenResponse(
            accessToken: payload.accessToken,
            refreshToken: payload.refreshToken,
            idToken: payload.idToken,
            tokenType: payload.tokenType,
            expiresIn: TimeInterval(payload.expiresIn)
        )
    }

    private func postForm(to url: URL, fields: [String: String]) async throws -> Data {
        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.setValue("application/x-www-form-urlencoded", forHTTPHeaderField: "Content-Type")
        request.httpBody = fields.formEncodedData()

        let (data, response) = try await session.data(for: request)
        guard let httpResponse = response as? HTTPURLResponse else {
            throw WorkOSAuthError.invalidResponse
        }
        if (200..<300).contains(httpResponse.statusCode) {
            return data
        }
        if let payload = try? JSONDecoder().decode(WorkOSErrorPayload.self, from: data) {
            throw WorkOSAuthError.fromRemote(payload.error)
        }
        throw WorkOSAuthError.httpStatus(httpResponse.statusCode)
    }

    private func sleep(seconds: TimeInterval) async throws {
        try await Task.sleep(nanoseconds: UInt64(seconds * 1_000_000_000))
    }
}

private struct DeviceAuthorizationPayload: Decodable {
    let deviceCode: String
    let userCode: String
    let verificationURI: String
    let verificationURIComplete: String
    let expiresIn: Int
    let interval: Int

    enum CodingKeys: String, CodingKey {
        case deviceCode = "device_code"
        case userCode = "user_code"
        case verificationURI = "verification_uri"
        case verificationURIComplete = "verification_uri_complete"
        case expiresIn = "expires_in"
        case interval
    }
}

private struct TokenPayload: Decodable {
    let accessToken: String
    let refreshToken: String
    let idToken: String?
    let tokenType: String
    let expiresIn: Int

    enum CodingKeys: String, CodingKey {
        case accessToken = "access_token"
        case refreshToken = "refresh_token"
        case idToken = "id_token"
        case tokenType = "token_type"
        case expiresIn = "expires_in"
    }
}

private struct WorkOSErrorPayload: Decodable {
    let error: String
}

enum WorkOSAuthError: LocalizedError, Equatable {
    case authorizationPending
    case authorizationTimedOut
    case invalidResponse
    case missingClientID
    case slowDown
    case accessDenied
    case expiredToken
    case httpStatus(Int)
    case provider(String)

    static func fromRemote(_ error: String) -> WorkOSAuthError {
        switch error {
        case "authorization_pending":
            return .authorizationPending
        case "slow_down":
            return .slowDown
        case "access_denied":
            return .accessDenied
        case "expired_token":
            return .expiredToken
        default:
            return .provider(error)
        }
    }

    var errorDescription: String? {
        switch self {
        case .authorizationPending:
            return "Authorization is still pending."
        case .authorizationTimedOut:
            return "Authorization timed out."
        case .invalidResponse:
            return "WorkOS returned an invalid response."
        case .missingClientID:
            return "Set ITER_WORKOS_CLIENT_ID before signing in."
        case .slowDown:
            return "WorkOS asked this device to slow down polling."
        case .accessDenied:
            return "Authorization was denied."
        case .expiredToken:
            return "The device code expired."
        case .httpStatus(let status):
            return "WorkOS returned HTTP \(status)."
        case .provider(let error):
            return "WorkOS authorization failed: \(error)."
        }
    }
}

private extension Dictionary where Key == String, Value == String {
    func formEncodedData() -> Data {
        let body = map { key, value in
            "\(key.urlFormEncoded)=\(value.urlFormEncoded)"
        }
        .sorted()
        .joined(separator: "&")
        return Data(body.utf8)
    }
}

private extension String {
    var urlFormEncoded: String {
        addingPercentEncoding(withAllowedCharacters: .urlFormAllowed) ?? self
    }
}

private extension CharacterSet {
    static let urlFormAllowed: CharacterSet = {
        var set = CharacterSet.urlQueryAllowed
        set.remove(charactersIn: "&+=?")
        return set
    }()
}
