import Darwin
import Foundation
import Observation

struct DaemonStatus: Equatable {
    var running: Bool = false
    var lastSessionAt: Date?
    var capturedToday: Int = 0
    var paused: Bool = false
}

@MainActor
@Observable
final class DaemonClient {
    var connected = false
    var status = DaemonStatus()
    var daemonVersion = ""
    var versionMismatch = false
    var lastError: String?

    private let socketPath: String
    private let appVersion: String
    private var monitorTask: Task<Void, Never>?

    init(
        socketPath: String = DaemonClient.defaultSocketPath(),
        appVersion: String = Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String ?? "0.1"
    ) {
        self.socketPath = socketPath
        self.appVersion = appVersion
    }

    func start() {
        guard monitorTask == nil else { return }
        monitorTask = Task { await monitor() }
    }

    func pause() {
        Task { await sendControl("pause") }
    }

    func resume() {
        Task { await sendControl("resume") }
    }

    var footerLabel: String {
        if !connected { return "daemon · offline" }
        if status.paused { return "daemon · paused" }
        return status.running ? "daemon · running" : "daemon · starting"
    }

    var footerDetail: String {
        if !connected { return "reconnecting" }
        if let lastSessionAt = status.lastSessionAt {
            return "last \(Self.relativeTime(from: lastSessionAt))"
        }
        return "\(status.capturedToday) captured today"
    }

    private func monitor() async {
        var backoff: TimeInterval = 0.25
        while !Task.isCancelled {
            do {
                let versionResult = try await request("version")
                let version = versionResult["version"] as? String ?? ""
                daemonVersion = version
                versionMismatch = Self.major(version) != Self.major(appVersion)

                let statusResult = try await request("status")
                status = Self.parseStatus(statusResult)
                connected = true
                lastError = nil
                backoff = 0.25
                try? await Task.sleep(nanoseconds: 5_000_000_000)
            } catch {
                connected = false
                status = DaemonStatus()
                lastError = error.localizedDescription
                try? await Task.sleep(nanoseconds: UInt64(backoff * 1_000_000_000))
                backoff = min(backoff * 2, 2)
            }
        }
    }

    private func sendControl(_ method: String) async {
        do {
            _ = try await request(method)
            let statusResult = try await request("status")
            status = Self.parseStatus(statusResult)
            connected = true
            lastError = nil
        } catch {
            connected = false
            lastError = error.localizedDescription
        }
    }

    private func request(_ method: String) async throws -> [String: Any] {
        let socketPath = socketPath
        return try await Task.detached(priority: .utility) {
            try Self.performRequest(method: method, socketPath: socketPath)
        }.value
    }

    nonisolated private static func performRequest(method: String, socketPath: String) throws -> [String: Any] {
        let descriptor = try openSocket(at: socketPath)
        defer { Darwin.close(descriptor) }
        let payload = try JSONSerialization.data(
            withJSONObject: ["id": UUID().uuidString, "method": method],
            options: []
        ) + Data([0x0A])
        try writePayload(payload, to: descriptor)

        let object = try JSONSerialization.jsonObject(with: readLine(from: descriptor), options: [])
        guard let response = object as? [String: Any] else { throw DaemonClientError.invalidResponse }
        if let error = response["error"] as? String, !error.isEmpty {
            throw DaemonClientError.daemon(error)
        }
        guard let result = response["result"] as? [String: Any] else {
            throw DaemonClientError.invalidResponse
        }
        return result
    }

    nonisolated private static func openSocket(at socketPath: String) throws -> Int32 {
        let descriptor = Darwin.socket(AF_UNIX, SOCK_STREAM, 0)
        guard descriptor >= 0 else { throw POSIXError(.EIO) }
        var address = sockaddr_un()
        address.sun_family = sa_family_t(AF_UNIX)
        let pathBytes = Array(socketPath.utf8)
        guard pathBytes.count < MemoryLayout.size(ofValue: address.sun_path) else {
            throw POSIXError(.ENAMETOOLONG)
        }
        withUnsafeMutableBytes(of: &address.sun_path) { rawBuffer in
            for index in pathBytes.indices {
                rawBuffer[index] = pathBytes[index]
            }
            rawBuffer[pathBytes.count] = 0
        }
        let addressLength = socklen_t(MemoryLayout<sa_family_t>.size + pathBytes.count + 1)
        let connectResult = withUnsafePointer(to: &address) { pointer in
            pointer.withMemoryRebound(to: sockaddr.self, capacity: 1) { sockaddrPointer in
                Darwin.connect(descriptor, sockaddrPointer, addressLength)
            }
        }
        guard connectResult == 0 else {
            Darwin.close(descriptor)
            throw POSIXError(POSIXErrorCode(rawValue: errno) ?? .ECONNREFUSED)
        }
        return descriptor
    }

    nonisolated private static func writePayload(_ payload: Data, to descriptor: Int32) throws {
        try payload.withUnsafeBytes { buffer in
            guard let baseAddress = buffer.baseAddress else { return }
            var sent = 0
            while sent < payload.count {
                let count = Darwin.write(descriptor, baseAddress.advanced(by: sent), payload.count - sent)
                guard count > 0 else { throw POSIXError(POSIXErrorCode(rawValue: errno) ?? .EPIPE) }
                sent += count
            }
        }
    }

    nonisolated private static func readLine(from descriptor: Int32) throws -> Data {
        var bytes = [UInt8]()
        var byte = UInt8(0)
        while true {
            let count = Darwin.read(descriptor, &byte, 1)
            guard count > 0 else { throw POSIXError(POSIXErrorCode(rawValue: errno) ?? .ECONNRESET) }
            if byte == 0x0A { break }
            bytes.append(byte)
        }
        return Data(bytes)
    }

    nonisolated private static func parseStatus(_ result: [String: Any]) -> DaemonStatus {
        DaemonStatus(
            running: result["running"] as? Bool ?? false,
            lastSessionAt: parseDate(result["last_session_at"]),
            capturedToday: result["captured_today"] as? Int ?? 0,
            paused: result["paused"] as? Bool ?? false
        )
    }

    nonisolated private static func parseDate(_ value: Any?) -> Date? {
        guard let string = value as? String else { return nil }
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return formatter.date(from: string) ?? ISO8601DateFormatter().date(from: string)
    }

    nonisolated private static func major(_ version: String) -> Int {
        let head = version.trimmingCharacters(in: CharacterSet(charactersIn: "v"))
            .split(separator: ".")
            .first
        return head.flatMap { Int($0) } ?? 0
    }

    nonisolated private static func relativeTime(from date: Date) -> String {
        let seconds = max(0, Int(Date().timeIntervalSince(date)))
        if seconds < 60 { return "\(seconds)s ago" }
        let minutes = seconds / 60
        if minutes < 60 { return "\(minutes)m ago" }
        return "\(minutes / 60)h ago"
    }

    nonisolated private static func defaultSocketPath() -> String {
        FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Library/Application Support/Iter/daemon.sock")
            .path
    }
}

enum DaemonClientError: LocalizedError {
    case invalidResponse
    case daemon(String)

    var errorDescription: String? {
        switch self {
        case .invalidResponse:
            return "Invalid daemon response"
        case .daemon(let message):
            return message
        }
    }
}
