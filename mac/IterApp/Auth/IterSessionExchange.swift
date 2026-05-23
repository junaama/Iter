import Foundation
import OSLog

private let exchangeLog = Logger(subsystem: "dev.iter.IterApp", category: "auth.exchange")

/// Wire shape returned by POST /v1/auth/session. Mirrors
/// `pkg/contracts/auth_session.go` AuthSessionResponse and
/// `contracts.py` AuthSessionResponse — the canonical compatibility
/// contract per CLAUDE.md.
struct IterSessionTokenResponse: Decodable, Equatable {
    let accessToken: String
    let expiresIn: TimeInterval
    let tokenType: String

    enum CodingKeys: String, CodingKey {
        case accessToken = "access_token"
        case expiresIn = "expires_in"
        case tokenType = "token_type"
    }
}

/// IterSessionExchangeClient trades a WorkOS access token for an
/// Iter-issued session JWT by POSTing to /v1/auth/session.
///
/// The Mac app calls this immediately after `WorkOSDeviceAuthClient`
/// returns a `WorkOSTokenResponse` (and after every successful refresh)
/// because the raw WorkOS token cannot be presented to the rest of the
/// Iter API: WorkOS `sub` is "user_01KS..." (a prefixed WorkOS id, not
/// a UUID) and the access token carries no `tenant_id` claim, both of
/// which the Iter auth middleware requires.
///
/// This client is intentionally separate from `IterHTTPClient` because
/// it must NOT be authorized — by definition the caller does not yet
/// hold an Iter JWT.
struct IterSessionExchangeClient {
    private let configuration: AuthConfiguration
    private let session: URLSession

    init(
        configuration: AuthConfiguration = .fromEnvironment(),
        session: URLSession = .shared
    ) {
        self.configuration = configuration
        self.session = session
    }

    /// Exchange the WorkOS access token for an Iter session JWT. Throws
    /// `IterSessionExchangeError` for every failure path.
    func exchange(workosAccessToken: String) async throws -> IterSessionTokenResponse {
        let endpoint = configuration.apiBaseURL.appendingPathComponent("v1/auth/session")
        var request = URLRequest(url: endpoint)
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.setValue("application/json", forHTTPHeaderField: "Accept")

        let body = ["workos_access_token": workosAccessToken]
        request.httpBody = try JSONSerialization.data(withJSONObject: body)

        let (data, response) = try await session.data(for: request)
        guard let http = response as? HTTPURLResponse else {
            throw IterSessionExchangeError.invalidResponse
        }

        guard (200..<300).contains(http.statusCode) else {
            let errorCode = Self.decodeErrorCode(from: data)
            exchangeLog.error("token exchange failed status=\(http.statusCode, privacy: .public) code=\(errorCode ?? "unknown", privacy: .public)")
            switch http.statusCode {
            case 401:
                throw IterSessionExchangeError.invalidToken
            case 503:
                throw IterSessionExchangeError.serverUnavailable
            default:
                throw IterSessionExchangeError.httpStatus(http.statusCode, errorCode)
            }
        }

        do {
            return try JSONDecoder().decode(IterSessionTokenResponse.self, from: data)
        } catch {
            exchangeLog.error("token exchange decode failed: \(String(describing: error), privacy: .public)")
            throw IterSessionExchangeError.decode(error.localizedDescription)
        }
    }

    private static func decodeErrorCode(from data: Data) -> String? {
        guard !data.isEmpty,
              let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            return nil
        }
        return object["error"] as? String
    }
}

enum IterSessionExchangeError: LocalizedError, Equatable {
    case invalidResponse
    case invalidToken
    case serverUnavailable
    case httpStatus(Int, String?)
    case decode(String)

    var errorDescription: String? {
        switch self {
        case .invalidResponse:
            return "Iter API returned an invalid response while exchanging tokens."
        case .invalidToken:
            return "Sign-in failed: the identity provider rejected your credentials."
        case .serverUnavailable:
            return "Iter API is not configured to accept sign-ins yet (HTTP 503)."
        case .httpStatus(let status, let code):
            if let code, !code.isEmpty {
                return "Iter API returned HTTP \(status) (\(code))."
            }
            return "Iter API returned HTTP \(status)."
        case .decode(let message):
            return "Iter API response was not the expected JSON shape: \(message)."
        }
    }
}
