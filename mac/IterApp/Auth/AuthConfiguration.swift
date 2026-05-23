import Foundation

struct AuthConfiguration {
    let clientID: String
    let deviceAuthorizationURL: URL
    let tokenURL: URL
    let apiBaseURL: URL

    var isConfigured: Bool {
        !clientID.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    static func fromEnvironment(
        _ environment: [String: String] = ProcessInfo.processInfo.environment
    ) -> AuthConfiguration {
        let clientID = environment["ITER_WORKOS_CLIENT_ID"] ?? environment["WORKOS_CLIENT_ID"] ?? ""
        let workOSBase = URL(string: environment["ITER_WORKOS_BASE_URL"] ?? "https://api.workos.com")
            ?? URL(string: "https://api.workos.com")!
        let deviceURL = URL(string: environment["ITER_WORKOS_DEVICE_AUTH_URL"] ?? "", relativeTo: nil)
            ?? workOSBase.appending(path: "user_management/authorize/device")
        let tokenURL = URL(string: environment["ITER_WORKOS_TOKEN_URL"] ?? "", relativeTo: nil)
            ?? workOSBase.appending(path: "user_management/authenticate")
        let apiBaseURL = URL(string: environment["ITER_API_BASE_URL"] ?? "https://staging.iter.dev")
            ?? URL(string: "https://staging.iter.dev")!

        return AuthConfiguration(
            clientID: clientID,
            deviceAuthorizationURL: deviceURL,
            tokenURL: tokenURL,
            apiBaseURL: apiBaseURL
        )
    }
}
