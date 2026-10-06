import XCTest
@testable import MacOCRNative

final class OutboundTests: XCTestCase {
    func testProductionURLUsesWSSWithoutCredentials() throws {
        let request = try OutboundProtocol.connectRequest(origin: "https://ocr.dungxbuif.com", nodeID: "node", secret: "private")
        XCTAssertEqual(request.url?.absoluteString, "wss://ocr.dungxbuif.com/v1/workers/connect")
        XCTAssertEqual(request.value(forHTTPHeaderField: "Authorization"), "Bearer private")
        XCTAssertFalse(request.url!.absoluteString.contains("private"))
    }
    func testRejectsCredentialAndPathOrigins() {
        XCTAssertThrowsError(try OutboundProtocol.connectRequest(origin: "https://user:pass@example.com", nodeID: "node", secret: "s"))
        XCTAssertThrowsError(try OutboundProtocol.connectRequest(origin: "https://example.com/path", nodeID: "node", secret: "s"))
    }
    func testIdentityPersistsOutsideBundle() {
        let suite = "outbound-tests-\(UUID().uuidString)"
        let defaults = UserDefaults(suiteName: suite)!
        defer { defaults.removePersistentDomain(forName: suite) }
        let first = OutboundProtocol.installationID(defaults: defaults)
        XCTAssertEqual(first, OutboundProtocol.installationID(defaults: defaults))
        XCTAssertNotNil(UUID(uuidString: first))
    }
    func testDuplicateAndDrainRejectReservationsWithoutLosingActiveWork() async {
        let state = NativeWorkerState(operatorLimit: 6, authSecret: "test", nodeId: "test", adaptivePolicy: NativeAdaptiveConcurrencyPolicy(enabled: false, recoverySamples: 1))
        let first = await state.tryAcquireSlot(attemptID: "attempt", mediaType: "image/jpeg")
        XCTAssertTrue(first.accepted)
        let duplicate = await state.tryAcquireSlot(attemptID: "attempt", mediaType: "image/jpeg")
        XCTAssertFalse(duplicate.accepted)
        XCTAssertEqual(duplicate.capacity.active, 1)
        let paused = await state.updateOperatorLimit(0)
        XCTAssertEqual(paused.active, 1)
        let drained = await state.tryAcquireSlot(attemptID: "second", mediaType: "image/jpeg")
        XCTAssertFalse(drained.accepted)
        let released = await state.releaseSlot(attemptID: "attempt")
        XCTAssertEqual(released.capacity.active, 0)
        XCTAssertEqual(released.capacity.available, 0)
        _ = await state.updateOperatorLimit(6)
        let resumed = await state.tryAcquireSlot(attemptID: "second", mediaType: "image/jpeg")
        XCTAssertTrue(resumed.accepted)
    }
    @MainActor
    func testRealSocketHelloRejectedDispatchDrainAndResume() async throws {
        let fixture = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent().appendingPathComponent("Fixtures/socket-fixture.py")
        let portFile = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: portFile) }
        let process = Process()
        process.executableURL = URL(fileURLWithPath: "/usr/bin/python3")
        process.arguments = [fixture.path, portFile.path]
        let output = Pipe(); process.standardError = output
        try process.run()
        defer { if process.isRunning { process.terminate() } }
        var port: Int?
        for _ in 0..<200 {
            if let text = try? String(contentsOf: portFile), let value = Int(text) { port = value; break }
            try await Task.sleep(nanoseconds: 10_000_000)
        }
        let address = try XCTUnwrap(port)
        let worker = NativeWorkerState(operatorLimit: 6, authSecret: "fixture-secret", nodeId: "fixture-node", adaptivePolicy: NativeAdaptiveConcurrencyPolicy(enabled: false, recoverySamples: 1))
        let request = try OutboundProtocol.connectRequest(origin: "http://127.0.0.1:\(address)", nodeID: "fixture-node", secret: "fixture-secret")
        var welcomed = false
        let connector = OutboundConnector(state: worker, request: request, name: "Fixture Mac", limit: 6) { connected, _ in if connected { welcomed = true } }
        connector.start()
        defer { connector.stop() }
        for _ in 0..<1_000 {
            if !process.isRunning { break }
            try await Task.sleep(nanoseconds: 10_000_000)
        }
        XCTAssertFalse(process.isRunning, "Local socket exchange timed out")
        if !process.isRunning {
            XCTAssertEqual(process.terminationStatus, 0, String(decoding: output.fileHandleForReading.readDataToEndOfFile(), as: UTF8.self))
        }
        XCTAssertTrue(welcomed)
        let capacity = await worker.getCapacity()
        XCTAssertEqual(capacity.active, 0)
    }

    func testSharedIdentityHashesHostAndNeverIncludesRawHardwareID() {
        let defaults = UserDefaults(suiteName: "host-id-test")!
        let hardware = "private-platform-uuid"
        let first = OutboundProtocol.machineNodeID(hardwareID: hardware, defaults: defaults)
        XCTAssertEqual(first, OutboundProtocol.machineNodeID(hardwareID: hardware, defaults: defaults))
        XCTAssertEqual(first.count, 36)
        XCTAssertTrue(first.hasPrefix("mac_"))
        XCTAssertFalse(first.contains(hardware))
        XCTAssertNotEqual(first, OutboundProtocol.machineNodeID(hardwareID: "another-machine", defaults: defaults))
    }

    func testNewSharedBuildKeyWinsOverCachedOldKey() {
        XCTAssertEqual(OutboundProtocol.sharedSecret(configured: "new-build-key", cached: "old-key"), "new-build-key")
        XCTAssertEqual(OutboundProtocol.sharedSecret(configured: "", cached: "old-key"), "old-key")
        XCTAssertEqual(OutboundProtocol.sharedSecret(configured: "change-me-in-production", cached: nil), "")
        XCTAssertEqual(OutboundProtocol.sharedSecret(configured: "", cached: "change-me-in-production"), "")
    }

}
