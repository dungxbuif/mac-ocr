import AppKit
import Combine
import CryptoKit
import Foundation
import ServiceManagement

@MainActor
public final class WorkerController: ObservableObject {
    public enum Status: String {
        case offline
        case starting
        case online
        case stopping
        case error
    }

    public enum ConnectionStatus: Equatable {
        case notTested
        case testing
        case verified
        case failed(String)
    }

    @Published public private(set) var status: Status = .offline
    @Published public private(set) var statusDetail = "Configure and test the proxy connection"
    @Published public private(set) var capacity: NativeCapacity?
    @Published public private(set) var logs: [NativeLogEntry] = []
    @Published public private(set) var launchAtLogin = false
    @Published public private(set) var connectionStatus: ConnectionStatus = .notTested
    @Published public private(set) var startRequested = false

    @Published public private(set) var proxyURL: String
    @Published public private(set) var port: Int
    @Published public private(set) var operatorLimit: Int
    public let nodeID: String
    @Published public private(set) var authSecret: String
    public let mode: String

    public let logRecorder = NativeLogRecorder.shared
    public var showLogs: (() -> Void)?
    public var requestQuit: (() -> Void)?

    private var server: NativeHTTPServer?
    private var workerState: NativeWorkerState?
    private var monitorTask: Task<Void, Never>?
    private var connectionTask: Task<Void, Never>?
    private var generation = UUID()
    private let defaults = UserDefaults.standard
    private let verifiedFingerprintKey = "MacOCRNative.verifiedConfigFingerprint"
    private let secretAccount = "native-auth-secret"

    public init() {
        let bundle = Bundle.main
        let environment = ProcessInfo.processInfo.environment
        func buildDefault(_ key: String, fallback: String) -> String {
            (bundle.object(forInfoDictionaryKey: key) as? String).flatMap { $0.isEmpty ? nil : $0 }
                ?? environment[key]
                ?? fallback
        }

        let defaultProxy = buildDefault("MacOCRDefaultProxyURL", fallback: "http://localhost:8080")
        let defaultMode = environment["MACOCR_MODE"]
            ?? environment["APP_ENV"]
            ?? buildDefault("MacOCRDefaultMode", fallback: "development")
        let defaultPort = Int(buildDefault("MacOCRDefaultPort", fallback: environment["NATIVE_PORT"] ?? "8787")) ?? 8787
        let defaultLimit = Int(buildDefault("MacOCRDefaultLimit", fallback: environment["NATIVE_LIMIT"] ?? "6")) ?? 6
        let requestedNode = buildDefault("MacOCRDefaultNodeID", fallback: environment["NATIVE_NODE_ID"] ?? "ocr-native-01")
        let defaultNode = requestedNode.utf8.count <= 128 && !requestedNode.isEmpty && requestedNode.allSatisfy({ $0.isLetter || $0.isNumber || $0 == "_" || $0 == "-" })
            ? requestedNode
            : "ocr-native-01"
        let defaultSecret = environment["NATIVE_AUTH_SECRET"]
            ?? buildDefault("MacOCRDefaultAuthSecret", fallback: "change-me-in-production")

        self.proxyURL = defaults.string(forKey: "MacOCRNative.proxyURL") ?? defaultProxy
        self.mode = defaultMode.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        self.port = defaults.object(forKey: "MacOCRNative.port") == nil ? defaultPort : defaults.integer(forKey: "MacOCRNative.port")
        self.operatorLimit = defaults.object(forKey: "MacOCRNative.operatorLimit") == nil ? defaultLimit : defaults.integer(forKey: "MacOCRNative.operatorLimit")
        self.nodeID = defaultNode
        self.authSecret = KeychainStore.read(account: secretAccount) ?? defaultSecret
        self.logs = logRecorder.snapshot()
        self.launchAtLogin = SMAppService.mainApp.status == .enabled
        self.logRecorder.setDebugEnabled(self.mode == "debug")

        logRecorder.setEntryHandler { [weak self] entry in
            DispatchQueue.main.async {
                guard let self else { return }
                self.logs.append(entry)
                if self.logs.count > 2_000 { self.logs.removeFirst(self.logs.count - 2_000) }
            }
        }

        refreshVerificationState()
        let diagnostics = debugLoggingEnabled ? "debug" : "standard"
        log(.info, "Menu-bar app launched in Offline mode; manual Start is required diagnostics=" + diagnostics)
    }

    public var isEnabled: Bool { startRequested || status == .online || status == .starting || status == .stopping }
    public var toggleDisabled: Bool { status == .starting || status == .stopping }
    public var configurationLocked: Bool { status != .offline && status != .error }
    public var endpoint: String { "http://localhost:\(port)" }
    public var debugLoggingEnabled: Bool { mode == "debug" }
    public var configurationVerified: Bool {
        if case .verified = connectionStatus { return true }
        return false
    }

    public func startOffline() {
        startRequested = false
        status = .offline
        statusDetail = configurationVerified ? "Connection verified; ready for manual Start" : "Configure and test the proxy connection"
    }

    public func updateProxyURL(_ value: String) { proxyURL = value; configurationChanged() }
    public func updatePort(_ value: Int) { port = value; configurationChanged() }
    public func updateOperatorLimit(_ value: Int) { operatorLimit = value; configurationChanged() }
    public func updateAuthSecret(_ value: String) { authSecret = value; configurationChanged() }

    public func testConnection() { testConnection(startAfterSuccess: false) }

    private func testConnection(startAfterSuccess: Bool) {
        guard !configurationLocked else { return }
        connectionTask?.cancel()
        do {
            try validateConfiguration()
            try persistConfiguration()
        } catch {
            connectionStatus = .failed(error.localizedDescription)
            if startAfterSuccess { startRequested = false; status = .error }
            statusDetail = error.localizedDescription
            log(.error, "Configuration validation failed: \(error.localizedDescription)")
            return
        }

        connectionStatus = .testing
        statusDetail = "Testing signed connection to proxy…"
        let baseURL = proxyURL.trimmingCharacters(in: .whitespacesAndNewlines).trimmingCharacters(in: CharacterSet(charactersIn: "/"))
        let node = nodeID
        let secret = authSecret
        connectionTask = Task { [weak self] in
            do {
                guard let url = URL(string: baseURL + "/webhooks/native/verify") else { throw configurationError("Proxy URL is invalid") }
                let nonce = UUID().uuidString.lowercased()
                let body = try JSONSerialization.data(withJSONObject: ["nodeId": node, "nonce": nonce], options: [.sortedKeys])
                let timestamp = "\(Int64(Date().timeIntervalSince1970))"
                let eventID = "verify_\(UUID().uuidString.replacingOccurrences(of: "-", with: "").lowercased())"
                let signature = Signer.sign(secret: secret, nodeId: node, timestamp: timestamp, eventId: eventID, body: body)

                var request = URLRequest(url: url)
                request.httpMethod = "POST"
                request.timeoutInterval = 6
                request.httpBody = body
                request.setValue("application/json", forHTTPHeaderField: "Content-Type")
                request.setValue(node, forHTTPHeaderField: "X-Native-Node-Id")
                request.setValue(timestamp, forHTTPHeaderField: "X-Native-Timestamp")
                request.setValue(eventID, forHTTPHeaderField: "X-Native-Event-Id")
                request.setValue(signature, forHTTPHeaderField: "X-Native-Signature")

                let (_, response) = try await URLSession.shared.data(for: request)
                guard let http = response as? HTTPURLResponse else { throw configurationError("Proxy returned an invalid response") }
                if http.statusCode == 401 { throw configurationError("Native shared key does not match the proxy") }
                guard (200...299).contains(http.statusCode) else { throw configurationError("Proxy connection test failed with HTTP \(http.statusCode)") }

                guard let self, !Task.isCancelled else { return }
                self.defaults.set(self.configurationFingerprint(), forKey: self.verifiedFingerprintKey)
                self.connectionStatus = .verified
                self.statusDetail = "Connection verified; ready for manual Start"
                self.log(.info, "Signed proxy connection verified origin=\(self.safeProxyOrigin())")
                if startAfterSuccess, self.startRequested { self.startService() }
            } catch {
                guard let self, !Task.isCancelled else { return }
                self.connectionStatus = .failed(error.localizedDescription)
                if startAfterSuccess { self.startRequested = false; self.status = .error }
                self.statusDetail = error.localizedDescription
                self.log(.error, "Proxy connection test failed: \(error.localizedDescription)")
            }
        }
    }

    public func setEnabled(_ enabled: Bool) {
        if enabled {
            startRequested = true
            if configurationVerified {
                startService()
            } else {
                statusDetail = "Verifying local proxy connection before Start…"
                testConnection(startAfterSuccess: true)
            }
        } else {
            startRequested = false
            if connectionStatus == .testing {
                connectionTask?.cancel()
                connectionStatus = .notTested
                status = .offline
                statusDetail = "Start cancelled"
            } else {
                stopService()
            }
        }
    }

    public func startService() {
        guard status == .offline || status == .error else { return }
        guard configurationVerified else {
            statusDetail = "Test the proxy connection before starting"
            startRequested = false
            return
        }
        do { try validateConfiguration(); try persistConfiguration() } catch {
            status = .error
            startRequested = false
            statusDetail = error.localizedDescription
            log(.error, "Worker failed configuration validation: \(error.localizedDescription)")
            return
        }

        status = .starting
        startRequested = true
        statusDetail = "Binding local port \(port)…"
        let currentGeneration = UUID()
        generation = currentGeneration
        let handler: NativeLogHandler = { [weak recorder = logRecorder] level, message in recorder?.emit(level, message) }
        let state = NativeWorkerState(operatorLimit: operatorLimit, authSecret: authSecret, nodeId: nodeID, logger: handler)
        let newServer = NativeHTTPServer(port: UInt16(port), state: state, logger: handler)
        newServer.lifecycleHandler = { [weak self] lifecycle, detail in
            DispatchQueue.main.async {
                guard let self, self.generation == currentGeneration else { return }
                self.handleLifecycle(lifecycle, detail: detail)
            }
        }
        workerState = state
        server = newServer
        do {
            try newServer.start()
            beginCapacityMonitoring(state: state, generation: currentGeneration)
        } catch {
            server = nil
            workerState = nil
            status = .error
            startRequested = false
            statusDetail = error.localizedDescription
            log(.error, "Worker failed to start: \(error.localizedDescription)")
        }
    }

    public func stopService() {
        guard status == .online || status == .starting || status == .error else { return }
        status = .stopping
        statusDetail = "Stopping listener; accepted jobs will finish"
        log(.info, "Stopping native listener")
        server?.stop()
        server = nil
        if workerState == nil || (capacity?.active ?? 0) == 0 { finishStopping() }
    }

    public func clearLogs() { logRecorder.clear(); logs.removeAll() }
    public func copyLogs(_ entries: [NativeLogEntry]) {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(entries.map(\.line).joined(separator: "\n"), forType: .string)
    }
    public func revealLogFile() {
        let url = logRecorder.fileURL
        if !FileManager.default.fileExists(atPath: url.path) { try? Data().write(to: url) }
        NSWorkspace.shared.activateFileViewerSelecting([url])
    }
    public func setLaunchAtLogin(_ enabled: Bool) {
        do {
            if enabled { try SMAppService.mainApp.register() } else { try SMAppService.mainApp.unregister() }
            launchAtLogin = SMAppService.mainApp.status == .enabled
            log(.info, "Launch at Login \(launchAtLogin ? "enabled" : "disabled")")
        } catch {
            launchAtLogin = SMAppService.mainApp.status == .enabled
            log(.error, "Could not update Launch at Login: \(error.localizedDescription)")
            statusDetail = "Install the app in Applications before enabling Launch at Login"
        }
    }

    public func activeJobCount() -> Int { capacity?.active ?? 0 }

    private func configurationChanged() {
        connectionTask?.cancel()
        startRequested = false
        connectionStatus = .notTested
        statusDetail = "Configuration changed; test the proxy connection again"
        persistNonSecretConfiguration()
    }

    private func refreshVerificationState() {
        connectionStatus = defaults.string(forKey: verifiedFingerprintKey) == configurationFingerprint() ? .verified : .notTested
    }

    private func validateConfiguration() throws {
        let trimmedURL = proxyURL.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let url = URL(string: trimmedURL), let scheme = url.scheme?.lowercased(), ["http", "https"].contains(scheme), url.host != nil, url.user == nil, url.password == nil else {
            throw configurationError("Proxy URL must be an HTTP(S) origin without credentials")
        }
        guard (1...65_535).contains(port) else { throw configurationError("Local port must be between 1 and 65535") }
        guard (0...256).contains(operatorLimit) else { throw configurationError("Concurrency ceiling must be between 0 and 256") }
        guard mode == "debug" || mode == "development" || mode == "production" else { throw configurationError("Mode must be debug, development, or production") }
        guard !authSecret.isEmpty else { throw configurationError("Native shared key is required") }
        if mode == "production" && (authSecret == "change-me-in-production" || authSecret.utf8.count < 32) {
            throw configurationError("Production native shared key must be non-default and at least 32 bytes")
        }
    }

    private func persistConfiguration() throws {
        persistNonSecretConfiguration()
        try KeychainStore.write(authSecret, account: secretAccount)
    }

    private func persistNonSecretConfiguration() {
        defaults.set(proxyURL, forKey: "MacOCRNative.proxyURL")
        defaults.set(port, forKey: "MacOCRNative.port")
        defaults.set(operatorLimit, forKey: "MacOCRNative.operatorLimit")
    }

    private func configurationFingerprint() -> String {
        let raw = [proxyURL.trimmingCharacters(in: .whitespacesAndNewlines), "\(port)", "\(operatorLimit)", nodeID, authSecret].joined(separator: "\u{1f}")
        return SHA256.hash(data: Data(raw.utf8)).map { String(format: "%02x", $0) }.joined()
    }

    private func safeProxyOrigin() -> String {
        guard let url = URL(string: proxyURL), let scheme = url.scheme, let host = url.host else { return "invalid" }
        return "\(scheme)://\(host)\(url.port.map { ":\($0)" } ?? "")"
    }

    private func handleLifecycle(_ lifecycle: NativeServerLifecycle, detail: String?) {
        switch lifecycle {
        case .ready: status = .online; statusDetail = "Proxy connected · accepting OCR requests"
        case .failed: status = .error; statusDetail = detail ?? "Native listener failed"; server = nil
        case .cancelled:
            if status != .stopping { status = .offline; statusDetail = "Not accepting OCR requests" }
        }
    }

    private func beginCapacityMonitoring(state: NativeWorkerState, generation currentGeneration: UUID) {
        monitorTask?.cancel()
        monitorTask = Task { [weak self] in
            while !Task.isCancelled {
                let currentCapacity = await state.getCapacity()
                guard let self, self.generation == currentGeneration else { return }
                self.capacity = currentCapacity
                if self.status == .stopping && currentCapacity.active == 0 { self.finishStopping(); return }
                try? await Task.sleep(nanoseconds: 750_000_000)
            }
        }
    }

    private func finishStopping() {
        monitorTask?.cancel(); monitorTask = nil
        workerState = nil; capacity = nil; status = .offline; startRequested = false
        statusDetail = configurationVerified ? "Connection verified; ready for manual Start" : "Configure and test the proxy connection"
        log(.info, "Native worker is offline")
    }

    private func log(_ level: NativeLogLevel, _ message: String) { logRecorder.emit(level, message) }
}

private func configurationError(_ message: String) -> Error {
    NSError(domain: "MacOCRConfiguration", code: 1, userInfo: [NSLocalizedDescriptionKey: message])
}
