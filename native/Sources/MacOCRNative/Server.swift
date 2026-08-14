import Foundation
import Network
import CryptoKit

private let maxRequestBytes = 1 * 1024 * 1024
private let maxHeaderBytes = 64 * 1024
private let maxInputBytes = 100 * 1024 * 1024
private let maxCallbackBytes = 1 * 1024 * 1024

public actor NativeWorkerState {
    public var operatorLimit: Int
    public var configVersion: UInt64
    public var sequence: UInt64
    public let nodeId: String
    public let bootId: String
    public let authSecret: String
    public let session: URLSession
    private let logger: NativeLogHandler
    private let adaptivePolicy: NativeAdaptiveConcurrencyPolicy
    private let resourceProbe: any NativeResourceProbing
    private var effectiveLimit: Int
    private var activeUnits: Int
    private var allocations: [String: Int]
    private var resourceReason: String
    private var growthCandidate: Int?
    private var growthSamples: Int

    public init(
        operatorLimit: Int,
        authSecret: String,
        nodeId: String,
        adaptivePolicy: NativeAdaptiveConcurrencyPolicy = .configured(),
        resourceProbe: any NativeResourceProbing = NativeSystemResourceProbe(),
        logger: @escaping NativeLogHandler = { _, _ in }
    ) {
        self.operatorLimit = max(0, operatorLimit)
        self.configVersion = 1
        self.sequence = 0
        self.nodeId = nodeId
        self.bootId = "boot_\(UInt64(Date().timeIntervalSince1970 * 1000))"
        self.authSecret = authSecret
        self.logger = logger
        self.adaptivePolicy = adaptivePolicy
        self.resourceProbe = resourceProbe
        let initialDecision = adaptivePolicy.decision(operatorLimit: max(0, operatorLimit), snapshot: resourceProbe.snapshot())
        self.effectiveLimit = initialDecision.limit
        self.activeUnits = 0
        self.allocations = [:]
        self.resourceReason = initialDecision.reason
        self.growthCandidate = nil
        self.growthSamples = 0

        let config = URLSessionConfiguration.default
        config.timeoutIntervalForRequest = 30
        config.timeoutIntervalForResource = 60
        self.session = URLSession(configuration: config)
    }

    public func tryAcquireSlot(attemptID: String, mediaType: String?) -> (accepted: Bool, capacity: NativeCapacity) {
        refreshAdaptiveLimit()
        let units = adaptivePolicy.units(for: mediaType)
        if operatorLimit == 0 || allocations[attemptID] != nil || activeUnits + units > effectiveLimit {
            return (false, capacity())
        }
        allocations[attemptID] = units
        activeUnits += units
        return (true, capacity())
    }

    public func releaseSlot(attemptID: String) -> (sequence: UInt64, capacity: NativeCapacity) {
        if let units = allocations.removeValue(forKey: attemptID) {
            activeUnits = max(0, activeUnits - units)
        }
        sequence += 1
        refreshAdaptiveLimit()
        return (sequence, capacity())
    }

    public func updateOperatorLimit(_ newLimit: Int) -> NativeCapacity {
        operatorLimit = max(0, newLimit)
        configVersion += 1
        refreshAdaptiveLimit()
        logger(.info, "Runtime concurrency ceiling changed to \(operatorLimit)")
        return capacity()
    }

    public func getCapacity() -> NativeCapacity {
        refreshAdaptiveLimit()
        return capacity()
    }

    private func refreshAdaptiveLimit() {
        let decision = adaptivePolicy.decision(operatorLimit: operatorLimit, snapshot: resourceProbe.snapshot())
        let oldLimit = effectiveLimit
        if decision.limit < effectiveLimit {
            effectiveLimit = decision.limit
            growthCandidate = nil
            growthSamples = 0
        } else if decision.limit > effectiveLimit {
            if growthCandidate == decision.limit {
                growthSamples += 1
            } else {
                growthCandidate = decision.limit
                growthSamples = 1
            }
            if growthSamples >= adaptivePolicy.recoverySamples {
                effectiveLimit = decision.limit
                growthCandidate = nil
                growthSamples = 0
            }
        } else {
            growthCandidate = nil
            growthSamples = 0
        }
        resourceReason = decision.reason
        if effectiveLimit != oldLimit {
            configVersion += 1
            logger(.info, "Adaptive capacity changed effective=\(effectiveLimit) ceiling=\(operatorLimit) reason=\(resourceReason)")
        }
    }

    private func capacity() -> NativeCapacity {
        let active = allocations.count
        let availableUnits = max(0, effectiveLimit - activeUnits)
        let available = availableUnits / max(1, adaptivePolicy.imageJobUnits)
        let state: String
        if operatorLimit == 0 {
            state = "paused"
        } else if availableUnits == 0 {
            state = "busy"
        } else {
            state = "ready"
        }
        return NativeCapacity(
            configVersion: configVersion,
            state: state,
            operatorLimit: operatorLimit,
            effectiveLimit: effectiveLimit,
            active: active,
            available: available,
            activeUnits: activeUnits,
            availableUnits: availableUnits,
            imageJobUnits: adaptivePolicy.imageJobUnits,
            pdfJobUnits: adaptivePolicy.pdfJobUnits,
            adaptive: adaptivePolicy.enabled,
            resourceReason: resourceReason
        )
    }

    public func processOCR(request: NativeOCRRequest) async {
        var processError: Error?
        var ocrResult: OCRResult?
        logger(.info, "OCR started document=\(request.documentId) attempt=\(request.attemptId)")

        do {
            guard let inputURL = URL(string: request.input.url) else {
                throw NSError(domain: "NativeWorker", code: 400, userInfo: [NSLocalizedDescriptionKey: "Invalid input URL"])
            }
            let (stream, response) = try await session.bytes(from: inputURL)
            guard let httpResponse = response as? HTTPURLResponse, (200...299).contains(httpResponse.statusCode) else {
                let status = (response as? HTTPURLResponse)?.statusCode ?? 0
                throw NSError(domain: "NativeWorker", code: status, userInfo: [NSLocalizedDescriptionKey: "Failed to download input object (HTTP \(status))"])
            }

            if httpResponse.expectedContentLength > Int64(maxInputBytes) {
                throw NSError(domain: "NativeWorker", code: 413, userInfo: [NSLocalizedDescriptionKey: "Input exceeds 100 MiB"])
            }
            var data = Data()
            data.reserveCapacity(min(maxInputBytes, max(0, Int(httpResponse.expectedContentLength))))
            for try await byte in stream {
                if data.count >= maxInputBytes {
                    throw NSError(domain: "NativeWorker", code: 413, userInfo: [NSLocalizedDescriptionKey: "Input exceeds 100 MiB"])
                }
                data.append(byte)
            }
            guard !data.isEmpty else {
                throw NSError(domain: "NativeWorker", code: 400, userInfo: [NSLocalizedDescriptionKey: "Downloaded input is empty"])
            }
            logger(.debug, "Input downloaded document=\(request.documentId) bytes=\(data.count) mediaType=\(request.input.mediaType ?? "auto")")
            if let expectedHash = request.input.sha256, !expectedHash.isEmpty {
                let actualHash = SHA256.hash(data: data).map { String(format: "%02x", $0) }.joined()
                guard constantTimeEqual(actualHash.lowercased(), expectedHash.lowercased()) else {
                    throw NSError(domain: "NativeWorker", code: 400, userInfo: [NSLocalizedDescriptionKey: "Input SHA-256 mismatch"])
                }
            }

            ocrResult = try VisionEngine.recognize(data: data, mediaType: request.input.mediaType, options: request.options)

            if let result = ocrResult,
               let encoded = try? JSONEncoder().encode(result),
               encoded.count > maxCallbackBytes - 16_384 {
                ocrResult = nil
                throw NSError(domain: "NativeWorker", code: 413, userInfo: [NSLocalizedDescriptionKey: "OCR result exceeds callback payload limit"])
            }
            if let result = ocrResult {
                logger(.info, "OCR completed document=\(request.documentId) pages=\(result.pageCount) characters=\(result.text.count)")
            }
        } catch {
            processError = error
            logger(.error, "OCR failed document=\(request.documentId) attempt=\(request.attemptId): \(error.localizedDescription)")
        }

        let (seq, cap) = releaseSlot(attemptID: request.attemptId)

        let isoFormatter = ISO8601DateFormatter()
        isoFormatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        let occurredAt = isoFormatter.string(from: Date())

        let eventId = "evt_native_\(UInt64(Date().timeIntervalSince1970 * 1000))_\(seq)"
        let event = NativeEvent(
            eventId: eventId,
            type: processError != nil ? "attempt.failed" : "attempt.completed",
            nodeId: nodeId,
            bootId: bootId,
            sequence: seq,
            attemptId: request.attemptId,
            documentId: request.documentId,
            result: ocrResult,
            error: processError?.localizedDescription,
            capacity: cap,
            occurredAt: occurredAt
        )

        await deliverCallback(urlStr: request.callback.url, event: event)
    }

    private func deliverCallback(urlStr: String, event: NativeEvent) async {
        guard let url = URL(string: urlStr) else {
            logger(.error, "Callback URL is invalid event=\(event.eventId)")
            return
        }

        guard let bodyData = try? JSONEncoder().encode(event) else {
            logger(.error, "Callback payload encoding failed event=\(event.eventId)")
            return
        }

        let timestamp = "\(Int64(Date().timeIntervalSince1970))"
        let signature = Signer.sign(
            secret: authSecret,
            nodeId: nodeId,
            timestamp: timestamp,
            eventId: event.eventId,
            body: bodyData
        )

        for attempt in 0..<5 {
            var req = URLRequest(url: url)
            req.httpMethod = "POST"
            req.setValue("application/json", forHTTPHeaderField: "Content-Type")
            req.setValue(nodeId, forHTTPHeaderField: "X-Native-Node-Id")
            req.setValue(timestamp, forHTTPHeaderField: "X-Native-Timestamp")
            req.setValue(event.eventId, forHTTPHeaderField: "X-Native-Event-Id")
            req.setValue(signature, forHTTPHeaderField: "X-Native-Signature")
            req.httpBody = bodyData

            do {
                let (_, resp) = try await session.data(for: req)
                if let httpResp = resp as? HTTPURLResponse, (200...299).contains(httpResp.statusCode) {
                    logger(.info, "Callback delivered event=\(event.eventId) document=\(event.documentId) status=\(httpResp.statusCode) attempt=\(attempt + 1)")
                    return
                }
            } catch {
                logger(.warning, "Callback attempt failed event=\(event.eventId) attempt=\(attempt + 1): \(error.localizedDescription)")
            }

            try? await Task.sleep(nanoseconds: UInt64((attempt + 1) * 250_000_000))
        }
        logger(.error, "Callback exhausted retries event=\(event.eventId) document=\(event.documentId)")
    }
}

public enum NativeServerLifecycle: Sendable {
    case ready
    case failed
    case cancelled
}

public final class NativeHTTPServer {
    private let port: UInt16
    private let state: NativeWorkerState
    private let logger: NativeLogHandler
    private var listener: NWListener?
    public var lifecycleHandler: ((NativeServerLifecycle, String?) -> Void)?

    public init(port: UInt16, state: NativeWorkerState, logger: @escaping NativeLogHandler = { _, _ in }) {
        self.port = port
        self.state = state
        self.logger = logger
    }

    public func start() throws {
        let params = NWParameters.tcp
        guard let nwPort = NWEndpoint.Port(rawValue: port) else {
            throw NSError(domain: "Server", code: 1, userInfo: [NSLocalizedDescriptionKey: "Invalid port"])
        }
        listener = try NWListener(using: params, on: nwPort)
        listener?.stateUpdateHandler = { newState in
            switch newState {
            case .ready:
                self.logger(.info, "Native OCR listener ready on port \(self.port)")
                self.lifecycleHandler?(.ready, nil)
            case .failed(let err):
                self.logger(.error, "Native listener failed: \(err.localizedDescription)")
                self.lifecycleHandler?(.failed, err.localizedDescription)
            case .cancelled:
                self.logger(.info, "Native OCR listener stopped")
                self.lifecycleHandler?(.cancelled, nil)
            default:
                break
            }
        }
        listener?.newConnectionHandler = { [weak self] connection in
            self?.handleConnection(connection)
        }
        listener?.start(queue: .global(qos: .userInitiated))
    }

    public func stop() {
        listener?.cancel()
        listener = nil
    }

    private func handleConnection(_ connection: NWConnection) {
        connection.start(queue: .global(qos: .userInitiated))
        readFullHTTPRequest(connection: connection, accumulated: Data())
    }

    private func readFullHTTPRequest(connection: NWConnection, accumulated: Data) {
        connection.receive(minimumIncompleteLength: 1, maximumLength: 65536) { [weak self] content, _, isComplete, error in
            guard let self = self else { return }
            var current = accumulated
            if let content = content {
                current.append(content)
            }

            if current.count > maxRequestBytes + maxHeaderBytes {
                self.sendResponse(connection: connection, status: 413, body: "{\"error\":\"request is too large\"}")
                return
            }

            if let headerEndRange = current.range(of: Data("\r\n\r\n".utf8)) {
                let headerData = current.subdata(in: 0..<headerEndRange.lowerBound)
                if headerData.count > maxHeaderBytes {
                    self.sendResponse(connection: connection, status: 431, body: "{\"error\":\"request headers are too large\"}")
                    return
                }
                let headerStr = String(data: headerData, encoding: .utf8) ?? ""
                let bodyData = current.subdata(in: headerEndRange.upperBound..<current.count)

                var contentLength = 0
                var contentLengthSeen = false
                for line in headerStr.components(separatedBy: "\r\n") {
                    let parts = line.split(separator: ":", maxSplits: 1).map { $0.trimmingCharacters(in: .whitespaces) }
                    if parts.count == 2 && parts[0].lowercased() == "transfer-encoding" {
                        self.sendResponse(connection: connection, status: 400, body: "{\"error\":\"transfer encoding is not supported\"}")
                        return
                    }
                    if parts.count == 2 && parts[0].lowercased() == "content-length" {
                        guard !contentLengthSeen, let len = Int(parts[1]) else {
                            self.sendResponse(connection: connection, status: 400, body: "{\"error\":\"invalid content length\"}")
                            return
                        }
                        contentLengthSeen = true
                        contentLength = len
                    }
                }
                if contentLength < 0 || contentLength > maxRequestBytes {
                    self.sendResponse(connection: connection, status: 413, body: "{\"error\":\"request is too large\"}")
                    return
                }

                if bodyData.count >= contentLength {
                    self.dispatchRoute(connection: connection, headerStr: headerStr, body: bodyData.subdata(in: 0..<contentLength))
                    return
                }
            }

            if isComplete || error != nil {
                connection.cancel()
                return
            }

            self.readFullHTTPRequest(connection: connection, accumulated: current)
        }
    }

    private func dispatchRoute(connection: NWConnection, headerStr: String, body: Data) {
        let lines = headerStr.components(separatedBy: "\r\n")
        guard let requestLine = lines.first else {
            sendResponse(connection: connection, status: 400, body: "{\"error\":\"bad request\"}")
            return
        }
        let parts = requestLine.components(separatedBy: " ")
        guard parts.count >= 2 else {
            sendResponse(connection: connection, status: 400, body: "{\"error\":\"bad request\"}")
            return
        }

        let method = parts[0].uppercased()
        let path = parts[1]
        if path != "/health" && path != "/capacity" {
            logger(.debug, "HTTP \(method) \(path)")
        }

        var headers: [String: String] = [:]
        for line in lines.dropFirst() {
            let p = line.split(separator: ":", maxSplits: 1).map { $0.trimmingCharacters(in: .whitespaces) }
            if p.count == 2 {
                headers[p[0].lowercased()] = p[1]
            }
        }

        Task {
            if method == "GET" && path == "/health" {
                sendResponse(connection: connection, status: 200, body: "{\"status\":\"ok\",\"service\":\"mac-ocr-native\"}")
                return
            }

            if method == "GET" && path == "/capacity" {
                let cap = await state.getCapacity()
                let json = (try? JSONEncoder().encode(cap)).flatMap { String(data: $0, encoding: .utf8) } ?? "{}"
                sendResponse(connection: connection, status: 200, body: json)
                return
            }

            if method == "PUT" && path == "/runtime/config" {
                struct ConfigUpdate: Codable {
                    var operatorLimit: Int?
                }
                guard authorized(headers: headers, secret: state.authSecret) else {
                    logger(.warning, "Rejected unauthorized runtime config request")
                    sendResponse(connection: connection, status: 401, body: "{\"error\":\"invalid bearer token\"}")
                    return
                }
                guard isJSON(headers: headers) else {
                    sendResponse(connection: connection, status: 415, body: "{\"error\":\"content type must be application/json\"}")
                    return
                }
                guard hasOnlyKeys(body: body, allowed: ["operatorLimit"]),
                      let update = try? JSONDecoder().decode(ConfigUpdate.self, from: body),
                      let limit = update.operatorLimit, limit >= 0 else {
                    sendResponse(connection: connection, status: 400, body: "{\"error\":\"operatorLimit must be non-negative integer\"}")
                    return
                }
                let cap = await state.updateOperatorLimit(limit)
                let json = (try? JSONEncoder().encode(cap)).flatMap { String(data: $0, encoding: .utf8) } ?? "{}"
                sendResponse(connection: connection, status: 200, body: json)
                return
            }

            if method == "POST" && path == "/ocr" {
                guard authorized(headers: headers, secret: state.authSecret) else {
                    logger(.warning, "Rejected unauthorized OCR request")
                    sendResponse(connection: connection, status: 401, body: "{\"error\":\"invalid bearer token\"}")
                    return
                }
                guard isJSON(headers: headers) else {
                    sendResponse(connection: connection, status: 415, body: "{\"error\":\"content type must be application/json\"}")
                    return
                }
                guard validateOCRJSON(body), let ocrReq = try? JSONDecoder().decode(NativeOCRRequest.self, from: body),
                      validOCRRequest(ocrReq) else {
                    sendResponse(connection: connection, status: 400, body: "{\"error\":\"invalid OCR request JSON\"}")
                    return
                }

                let (slotAccepted, cap) = await state.tryAcquireSlot(attemptID: ocrReq.attemptId, mediaType: ocrReq.input.mediaType)
                if !slotAccepted {
                    logger(.warning, "OCR request rejected because capacity is unavailable document=\(ocrReq.documentId)")
                    let payload = CapacityUnavailableResponse(error: "capacity unavailable", capacity: cap)
                    let json = (try? JSONEncoder().encode(payload)).flatMap { String(data: $0, encoding: .utf8) } ?? "{}"
                    sendResponse(connection: connection, status: 503, headers: ["Retry-After": "1"], body: json)
                    return
                }

                let response = AcceptedResponse(attemptId: ocrReq.attemptId)
                let acceptedJSON = (try? JSONEncoder().encode(response)).flatMap { String(data: $0, encoding: .utf8) } ?? "{}"
                logger(.info, "OCR accepted document=\(ocrReq.documentId) attempt=\(ocrReq.attemptId) active=\(cap.active)")

                // Flush the acknowledgement before starting synchronous Vision
                // work. Large PDFs can monopolize worker threads long enough for
                // the proxy's HTTP timeout to expire, creating an ambiguous
                // dispatch and duplicate OCR attempts.
                sendResponse(connection: connection, status: 202, body: acceptedJSON) { [self] sendError in
                    if let sendError {
                        logger(.error, "OCR acknowledgement failed document=\(ocrReq.documentId): \(sendError.localizedDescription)")
                        Task { _ = await state.releaseSlot(attemptID: ocrReq.attemptId) }
                        return
                    }
                    Task.detached { [self] in
                        await self.state.processOCR(request: ocrReq)
                    }
                }
                return
            }

            sendResponse(connection: connection, status: 404, body: "{\"error\":\"not found\"}")
        }
    }

    private func sendResponse(
        connection: NWConnection,
        status: Int,
        headers: [String: String] = [:],
        body: String,
        completion: ((NWError?) -> Void)? = nil
    ) {
        let statusText: String
        switch status {
        case 200: statusText = "OK"
        case 202: statusText = "Accepted"
        case 400: statusText = "Bad Request"
        case 401: statusText = "Unauthorized"
        case 404: statusText = "Not Found"
        case 413: statusText = "Payload Too Large"
        case 415: statusText = "Unsupported Media Type"
        case 431: statusText = "Request Header Fields Too Large"
        case 503: statusText = "Service Unavailable"
        default: statusText = "Status \(status)"
        }

        let bodyData = Data(body.utf8)
        var responseHeaders = headers
        responseHeaders["Content-Type"] = "application/json"
        responseHeaders["Content-Length"] = "\(bodyData.count)"
        responseHeaders["Connection"] = "close"

        var headerLines = ["HTTP/1.1 \(status) \(statusText)"]
        for (k, v) in responseHeaders {
            headerLines.append("\(k): \(v)")
        }
        headerLines.append("\r\n")

        var responseData = Data(headerLines.joined(separator: "\r\n").utf8)
        responseData.append(bodyData)

        connection.send(content: responseData, completion: .contentProcessed({ error in
            completion?(error)
            connection.cancel()
        }))
    }
}

private func constantTimeEqual(_ lhs: String, _ rhs: String) -> Bool {
    let left = Array(lhs.utf8)
    let right = Array(rhs.utf8)
    guard left.count == right.count else { return false }
    var difference: UInt8 = 0
    for index in left.indices { difference |= left[index] ^ right[index] }
    return difference == 0
}

private func authorized(headers: [String: String], secret: String) -> Bool {
    constantTimeEqual(headers["authorization"] ?? "", "Bearer \(secret)")
}

private func isJSON(headers: [String: String]) -> Bool {
    (headers["content-type"] ?? "").split(separator: ";", maxSplits: 1).first?.lowercased() == "application/json"
}

private func hasOnlyKeys(body: Data, allowed: Set<String>) -> Bool {
    guard let object = try? JSONSerialization.jsonObject(with: body) as? [String: Any] else { return false }
    return Set(object.keys).isSubset(of: allowed)
}

private func validateOCRJSON(_ body: Data) -> Bool {
    guard let object = try? JSONSerialization.jsonObject(with: body) as? [String: Any],
          Set(object.keys).isSubset(of: ["documentId", "pageId", "attemptId", "input", "options", "callback"]),
          let input = object["input"] as? [String: Any], Set(input.keys).isSubset(of: ["url", "mediaType", "sha256"]),
          let callback = object["callback"] as? [String: Any], Set(callback.keys).isSubset(of: ["url"]) else { return false }
    if let options = object["options"] as? [String: Any],
       !Set(options.keys).isSubset(of: ["recognitionLevel", "languages", "automaticallyDetectsLanguage", "usesLanguageCorrection", "customWords", "minimumTextHeight"]) {
        return false
    }
    return true
}

private func validOCRRequest(_ request: NativeOCRRequest) -> Bool {
    let identifierCharacters = CharacterSet.alphanumerics.union(CharacterSet(charactersIn: "_-"))
    func validIdentifier(_ value: String) -> Bool {
        !value.isEmpty && value.utf8.count <= 128 && value.unicodeScalars.allSatisfy { identifierCharacters.contains($0) }
    }
    func validHTTPURL(_ raw: String) -> Bool {
        guard raw.utf8.count <= 2048, let url = URL(string: raw), let scheme = url.scheme?.lowercased(), url.host != nil else { return false }
        return scheme == "http" || scheme == "https"
    }

    guard validIdentifier(request.documentId), validIdentifier(request.attemptId),
          validHTTPURL(request.input.url), validHTTPURL(request.callback.url) else { return false }
    if let pageId = request.pageId, !validIdentifier(pageId) { return false }
    if let hash = request.input.sha256,
       (hash.utf8.count != 64 || !hash.unicodeScalars.allSatisfy({ CharacterSet(charactersIn: "0123456789abcdefABCDEF").contains($0) })) { return false }
    if let mediaType = request.input.mediaType,
       !["image/jpeg", "image/png", "image/tiff", "image/webp", "application/pdf"].contains(mediaType.lowercased()) { return false }
    if let options = request.options {
        if let level = options.recognitionLevel, level != "fast" && level != "accurate" { return false }
        if let languages = options.languages, languages.count > 20 || languages.contains(where: { $0.isEmpty || $0.utf8.count > 35 }) { return false }
        if let words = options.customWords, words.count > 500 || words.contains(where: { $0.isEmpty || $0.utf8.count > 128 }) { return false }
        if let height = options.minimumTextHeight, !height.isFinite || height < 0 || height > 1 { return false }
    }
    return true
}
