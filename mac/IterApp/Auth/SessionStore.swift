import AppKit
import Foundation
import Observation

enum SessionStatus: Equatable {
    case loading
    case signedOut
    case signingIn
    case polling
    case signedIn
    case expired
    case failed(String)
}

@MainActor
@Observable
final class SessionStore {
    var accessToken: String?
    var tenantId: String?
    var userId: String?
    var expiresAt: Date?
    var displayName: String?
    var status: SessionStatus = .loading
    var deviceAuthorization: DeviceAuthorization?
    var lastError: String?

    private let keychain: TokenKeychainStore
    private let authClient: WorkOSDeviceAuthClient
    private var refreshTask: Task<Void, Never>?
    private var pollingTask: Task<Void, Never>?
    private var refreshToken: String?

    init(
        keychain: TokenKeychainStore = TokenKeychainStore(),
        authClient: WorkOSDeviceAuthClient = WorkOSDeviceAuthClient()
    ) {
        self.keychain = keychain
        self.authClient = authClient
    }

    func load() {
        do {
            guard let tokens = try keychain.load() else {
                clearInMemory(status: .signedOut)
                return
            }
            try apply(tokens: tokens)
        } catch {
            lastError = error.localizedDescription
            clearInMemory(status: .signedOut)
        }
    }

    func startDeviceAuthorization() {
        guard pollingTask == nil else { return }
        status = .signingIn
        lastError = nil

        pollingTask = Task { [weak self] in
            guard let self else { return }
            do {
                let authorization = try await authClient.authorizeDevice()
                self.deviceAuthorization = authorization
                self.status = .polling
                let tokens = try await authClient.pollForTokens(authorization)
                try self.persistAndApply(tokens)
                self.deviceAuthorization = nil
            } catch {
                self.lastError = error.localizedDescription
                self.status = .failed(error.localizedDescription)
            }
            self.pollingTask = nil
        }
    }

    func openVerificationURL() {
        guard let url = deviceAuthorization?.verificationURIComplete ?? deviceAuthorization?.verificationURI else {
            return
        }
        NSWorkspace.shared.open(url)
    }

    func refreshIfNeeded(force: Bool = false) async -> Bool {
        guard let refreshToken else {
            signOut(expired: true)
            return false
        }
        if !force, let expiresAt, expiresAt.timeIntervalSinceNow > 60 {
            return true
        }

        do {
            let tokens = try await authClient.refresh(refreshToken: refreshToken)
            try persistAndApply(tokens)
            return true
        } catch {
            lastError = "Session expired, sign in again."
            signOut(expired: true)
            return false
        }
    }

    func signOut(expired: Bool = false) {
        pollingTask?.cancel()
        pollingTask = nil
        refreshTask?.cancel()
        refreshTask = nil
        do {
            try keychain.clear()
        } catch {
            lastError = error.localizedDescription
        }
        clearInMemory(status: expired ? .expired : .signedOut)
    }

    private func persistAndApply(_ response: WorkOSTokenResponse) throws {
        let tokens = StoredTokens(
            accessToken: response.accessToken,
            refreshToken: response.refreshToken,
            idToken: response.idToken
        )
        try keychain.save(tokens)
        try apply(tokens: tokens)
    }

    private func apply(tokens: StoredTokens) throws {
        let accessClaims = try JWTClaims.decode(tokens.accessToken)
        let idClaims = try tokens.idToken.map(JWTClaims.decode)

        accessToken = tokens.accessToken
        refreshToken = tokens.refreshToken
        tenantId = accessClaims.tenantId
        userId = accessClaims.subject
        expiresAt = accessClaims.expiresAt
        displayName = accessClaims.displayName ?? idClaims?.displayName ?? accessClaims.subject
        status = .signedIn
        lastError = nil
        scheduleRefresh()
    }

    private func scheduleRefresh() {
        refreshTask?.cancel()
        guard let expiresAt else { return }
        let delay = max(0, expiresAt.timeIntervalSinceNow - 60)
        refreshTask = Task { [weak self] in
            try? await Task.sleep(nanoseconds: UInt64(delay * 1_000_000_000))
            guard !Task.isCancelled else { return }
            _ = await self?.refreshIfNeeded(force: true)
        }
    }

    private func clearInMemory(status: SessionStatus) {
        accessToken = nil
        refreshToken = nil
        tenantId = nil
        userId = nil
        expiresAt = nil
        displayName = nil
        deviceAuthorization = nil
        self.status = status
    }
}

struct JWTClaims: Equatable {
    let subject: String
    let tenantId: String?
    let expiresAt: Date
    let displayName: String?

    static func decode(_ jwt: String) throws -> JWTClaims {
        let parts = jwt.split(separator: ".")
        guard parts.count >= 2,
              let payloadData = Data(base64URLEncoded: String(parts[1])),
              let object = try JSONSerialization.jsonObject(with: payloadData) as? [String: Any] else {
            throw JWTError.invalidToken
        }

        guard let subject = object["sub"] as? String, !subject.isEmpty else {
            throw JWTError.missingSubject
        }
        guard let expiresAt = expirationDate(from: object["exp"]) else {
            throw JWTError.missingExpiration
        }

        let displayName = object["name"] as? String
            ?? object["email"] as? String
            ?? object["preferred_username"] as? String

        return JWTClaims(
            subject: subject,
            tenantId: object["tenant_id"] as? String,
            expiresAt: expiresAt,
            displayName: displayName
        )
    }

    private static func expirationDate(from value: Any?) -> Date? {
        if let number = value as? NSNumber {
            return Date(timeIntervalSince1970: number.doubleValue)
        }
        if let int = value as? Int {
            return Date(timeIntervalSince1970: TimeInterval(int))
        }
        if let double = value as? Double {
            return Date(timeIntervalSince1970: double)
        }
        return nil
    }
}

enum JWTError: LocalizedError {
    case invalidToken
    case missingExpiration
    case missingSubject

    var errorDescription: String? {
        switch self {
        case .invalidToken:
            return "Token is not a valid JWT."
        case .missingExpiration:
            return "Token is missing an expiration claim."
        case .missingSubject:
            return "Token is missing a subject claim."
        }
    }
}

private extension Data {
    init?(base64URLEncoded value: String) {
        var base64 = value
            .replacingOccurrences(of: "-", with: "+")
            .replacingOccurrences(of: "_", with: "/")
        while base64.count % 4 != 0 {
            base64.append("=")
        }
        self.init(base64Encoded: base64)
    }
}
