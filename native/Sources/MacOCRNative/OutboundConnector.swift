import Foundation
import CryptoKit
import IOKit

struct WorkerMessage: Codable {
    var type: String
    var requestId: String?
    var capacity: NativeCapacity?
    var ocr: NativeOCRRequest?
    var scan: NativeScanRequest?
    var accepted: Bool?
    var action: String?
    var name: String?
    var version: String?
    var capabilities: [String]?
}

enum OutboundProtocol {
    static func sharedSecret(configured: String, cached: String?) -> String {
        // A rebuilt release is the authority for key rotation. Keychain is
        // only a fallback for a build without a supplied production key.
        if !configured.isEmpty && configured != "change-me-in-production" { return configured }
        guard let cached, !cached.isEmpty, cached != "change-me-in-production" else { return "" }
        return cached
    }
    static func origin(_ raw: String) throws -> URL {
        guard let url = URL(string: raw.trimmingCharacters(in: .whitespacesAndNewlines)),
              ["http", "https"].contains(url.scheme), url.host != nil,
              url.user == nil, url.password == nil, url.query == nil, url.fragment == nil,
              url.path.isEmpty || url.path == "/" else {
            throw NSError(domain: "Outbound", code: 1, userInfo: [NSLocalizedDescriptionKey: "Use an HTTP(S) origin without path or credentials"])
        }
        return url
    }
    static func connectRequest(origin raw: String, nodeID: String, secret: String) throws -> URLRequest {
        var components = URLComponents(url: try origin(raw), resolvingAgainstBaseURL: false)!
        components.scheme = components.scheme == "https" ? "wss" : "ws"
        components.path = "/v1/workers/connect"
        var request = URLRequest(url: components.url!)
        request.setValue("Bearer \(secret)", forHTTPHeaderField: "Authorization")
        request.setValue(nodeID, forHTTPHeaderField: "X-Native-Node-Id")
        return request
    }
    static func machineNodeID(hardwareID: String?, defaults: UserDefaults) -> String {
        guard let hardwareID, !hardwareID.isEmpty else { return installationID(defaults: defaults) }
        let digest = SHA256.hash(data: Data(hardwareID.utf8)).map { String(format: "%02x", $0) }.joined()
        return "mac_" + digest.prefix(32)
    }
    static func hostHardwareID() -> String? {
        let service = IOServiceGetMatchingService(kIOMainPortDefault, IOServiceMatching("IOPlatformExpertDevice"))
        guard service != 0 else { return nil }
        defer { IOObjectRelease(service) }
        return IORegistryEntryCreateCFProperty(service, "IOPlatformUUID" as CFString, kCFAllocatorDefault, 0)?.takeRetainedValue() as? String
    }
    static func installationID(defaults: UserDefaults) -> String {
        let key = "MacOCRNative.installationID"
        if let value = defaults.string(forKey: key), UUID(uuidString: value) != nil { return value }
        let value = UUID().uuidString.lowercased()
        defaults.set(value, forKey: key)
        return value
    }
}

final class SameOriginRedirectDelegate: NSObject, URLSessionTaskDelegate {
    private let origin: URL
    init(origin: URL) { self.origin = origin }
    func urlSession(_ session: URLSession, task: URLSessionTask, willPerformHTTPRedirection response: HTTPURLResponse, newRequest request: URLRequest, completionHandler: @escaping (URLRequest?) -> Void) {
        guard let target = request.url, target.host == origin.host, target.port == origin.port,
              target.user == nil, target.password == nil,
              ((["https", "wss"].contains(origin.scheme) && ["https", "wss"].contains(target.scheme)) || target.scheme == origin.scheme) else { completionHandler(nil); return }
        completionHandler(request)
    }
}

final class CredentialRedirectDelegate: NSObject, URLSessionTaskDelegate {
    func urlSession(_ session: URLSession, task: URLSessionTask, willPerformHTTPRedirection response: HTTPURLResponse, newRequest request: URLRequest, completionHandler: @escaping (URLRequest?) -> Void) {
        guard let original = task.originalRequest, original.value(forHTTPHeaderField: "Authorization") != nil else { completionHandler(request); return }
        guard let source = original.url, let target = request.url,
              source.scheme == target.scheme, source.host == target.host, source.port == target.port,
              target.user == nil, target.password == nil else { completionHandler(nil); return }
        completionHandler(request)
    }
}

@MainActor
final class OutboundConnector {
    private let state: NativeWorkerState
    private let request: URLRequest
    private let name: String
    private let limit: Int
    private let status: (Bool, String) -> Void
    private var task: Task<Void, Never>?
    private var socket: URLSessionWebSocketTask?
    private var heartbeat: Task<Void, Never>?
    private var stopped = false
    private let session: URLSession

    init(state: NativeWorkerState, request: URLRequest, name: String, limit: Int, status: @escaping (Bool, String) -> Void) {
        self.state = state; self.request = request; self.name = name; self.limit = limit; self.status = status
        let configuration = URLSessionConfiguration.default
        configuration.timeoutIntervalForRequest = 20
        session = URLSession(configuration: configuration, delegate: SameOriginRedirectDelegate(origin: request.url!), delegateQueue: nil)
    }
    func start() {
        task = Task { [weak self] in
            guard let self else { return }
            var delay: Double = 1
            while !Task.isCancelled && !stopped {
                let ws = session.webSocketTask(with: request)
                ws.maximumMessageSize = 1_048_576
                socket = ws; ws.resume()
                do {
                    try await send(WorkerMessage(type: "hello", capacity: await state.getCapacity(), name: name,
                        version: Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "2.0.0", capabilities: ["ocr", "scan"]), socket: ws)
                    heartbeat = Task { [weak self] in
                        guard let self else { return }
                        do {
                            while !Task.isCancelled {
                                try await Task.sleep(nanoseconds: 5_000_000_000)
                                try await send(WorkerMessage(type: "capacity", capacity: await state.getCapacity()), socket: ws)
                            }
                        } catch { ws.cancel(with: .goingAway, reason: nil) }
                    }
                    while !Task.isCancelled && !stopped {
                        let incoming = try await ws.receive()
                        let data: Data
                        switch incoming { case .data(let value): data = value; case .string(let value): data = Data(value.utf8); @unknown default: continue }
                        let message = try JSONDecoder().decode(WorkerMessage.self, from: data)
                        if message.type == "welcome" { status(true, "Connected to proxy · accepting work"); delay = 1 }
                        try await handle(message, socket: ws)
                    }
                } catch {
                    let code = (ws.response as? HTTPURLResponse)?.statusCode
                    if code == 401 || code == 403 || ws.closeCode == .policyViolation {
                        stopped = true
                        status(false, "Credential rejected or revoked; enroll again")
                    } else if !stopped { status(false, "Connection interrupted; reconnecting automatically") }
                }
                heartbeat?.cancel(); heartbeat = nil
                ws.cancel(with: .goingAway, reason: nil)
                if stopped || Task.isCancelled { break }
                do { try await Task.sleep(nanoseconds: UInt64((delay + Double.random(in: 0...1)) * 1_000_000_000)) } catch { break }
                delay = min(60, delay * 2)
            }
        }
    }
    func reconnect() { socket?.cancel(with: .goingAway, reason: nil) }
    func stop() {
        stopped = true
        task?.cancel(); heartbeat?.cancel()
        socket?.cancel(with: .normalClosure, reason: nil)
        session.invalidateAndCancel()
    }
    private func send(_ message: WorkerMessage, socket: URLSessionWebSocketTask) async throws {
        let data = try JSONEncoder().encode(message)
        try await socket.send(.string(String(decoding: data, as: UTF8.self)))
    }
    private func handle(_ message: WorkerMessage, socket: URLSessionWebSocketTask) async throws {
        if message.type == "control" {
            if message.action == "pause" || message.action == "drain" { _ = await state.updateOperatorLimit(0) }
            if message.action == "resume" { _ = await state.updateOperatorLimit(limit) }
            try await send(WorkerMessage(type: "capacity", capacity: await state.getCapacity()), socket: socket)
            return
        }
        guard let id = message.requestId else { return }
        let ocr = message.type == "ocr" ? message.ocr : nil
        let scan = message.type == "scan" ? message.scan : nil
        let validPayload = ocr.map { $0.attemptId == id && validOCRRequest($0) } ?? scan.map { $0.attemptId == id && validScanRequest($0) } ?? false
        let urls = ocr.map { [$0.input.url, $0.callback.url] } ?? scan.map { [$0.input.url, $0.output.url, $0.callback.url] } ?? []
        let secureURLs = request.url?.scheme != "wss" || urls.allSatisfy { URL(string: $0)?.scheme == "https" }
        let callbackURLs = ocr.map { [$0.callback.url] } ?? scan.map { [$0.callback.url, $0.output.url] } ?? []
        let sameOrigin = !state.bearerCallbacks || callbackURLs.allSatisfy {
            guard let target = URL(string: $0), let source = request.url else { return false }
            return target.host == source.host && target.port == source.port && (source.scheme == "wss" ? target.scheme == "https" : target.scheme == "http")
        }
        let valid = validPayload && secureURLs && sameOrigin
        let acquisition = valid ? await state.tryAcquireSlot(attemptID: id, mediaType: ocr?.input.mediaType ?? scan?.input.mediaType) : (false, await state.getCapacity())
        do { try await send(WorkerMessage(type: "ack", requestId: id, capacity: acquisition.1, accepted: acquisition.0), socket: socket) }
        catch { if acquisition.0 { _ = await state.releaseSlot(attemptID: id) }; throw error }
        guard acquisition.0 else { return }
        let worker = state
        Task.detached {
            if let ocr { await worker.processOCR(request: ocr) }
            if let scan { await worker.processScan(request: scan) }
        }
    }
}
