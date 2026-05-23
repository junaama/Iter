import Foundation
import Security

struct StoredTokens: Equatable {
    let accessToken: String
    let refreshToken: String
    let idToken: String?
}

struct TokenKeychainStore {
    static let service = "dev.iter.IterApp"

    private enum Account {
        static let accessToken = "access_token"
        static let refreshToken = "refresh_token"
        static let idToken = "id_token"
    }

    func save(_ tokens: StoredTokens) throws {
        try save(tokens.accessToken, account: Account.accessToken, accessible: kSecAttrAccessibleAfterFirstUnlock)
        try save(
            tokens.refreshToken,
            account: Account.refreshToken,
            accessible: kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        )

        if let idToken = tokens.idToken, !idToken.isEmpty {
            try save(idToken, account: Account.idToken, accessible: kSecAttrAccessibleAfterFirstUnlock)
        } else {
            try delete(account: Account.idToken)
        }
    }

    func load() throws -> StoredTokens? {
        guard let accessToken = try read(account: Account.accessToken),
              let refreshToken = try read(account: Account.refreshToken) else {
            return nil
        }
        return StoredTokens(
            accessToken: accessToken,
            refreshToken: refreshToken,
            idToken: try read(account: Account.idToken)
        )
    }

    func clear() throws {
        try delete(account: Account.accessToken)
        try delete(account: Account.refreshToken)
        try delete(account: Account.idToken)
    }

    private func save(_ value: String, account: String, accessible: CFString) throws {
        let data = Data(value.utf8)
        let baseQuery = query(account: account)
        SecItemDelete(baseQuery as CFDictionary)

        var addQuery = baseQuery
        addQuery[kSecValueData as String] = data
        addQuery[kSecAttrAccessible as String] = accessible

        let status = SecItemAdd(addQuery as CFDictionary, nil)
        guard status == errSecSuccess else {
            throw KeychainError.unexpectedStatus(status)
        }
    }

    private func read(account: String) throws -> String? {
        var readQuery = query(account: account)
        readQuery[kSecReturnData as String] = true
        readQuery[kSecMatchLimit as String] = kSecMatchLimitOne

        var result: AnyObject?
        let status = SecItemCopyMatching(readQuery as CFDictionary, &result)
        if status == errSecItemNotFound { return nil }
        guard status == errSecSuccess else {
            throw KeychainError.unexpectedStatus(status)
        }
        guard let data = result as? Data, let value = String(data: data, encoding: .utf8) else {
            throw KeychainError.invalidData
        }
        return value
    }

    private func delete(account: String) throws {
        let status = SecItemDelete(query(account: account) as CFDictionary)
        guard status == errSecSuccess || status == errSecItemNotFound else {
            throw KeychainError.unexpectedStatus(status)
        }
    }

    private func query(account: String) -> [String: Any] {
        [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: Self.service,
            kSecAttrAccount as String: account
        ]
    }
}

enum KeychainError: LocalizedError {
    case invalidData
    case unexpectedStatus(OSStatus)

    var errorDescription: String? {
        switch self {
        case .invalidData:
            return "Stored Keychain token data is invalid."
        case .unexpectedStatus(let status):
            return "Keychain operation failed with status \(status)."
        }
    }
}
