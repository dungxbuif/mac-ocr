import XCTest
@testable import MacOCRNative

final class WorkerControllerTests: XCTestCase {
    @MainActor
    func testOfflineToggleIsAvailableBeforeConnectionVerification() {
        let controller = WorkerController()

        controller.startOffline()

        XCTAssertEqual(controller.status, .offline)
        XCTAssertFalse(controller.isEnabled)
        XCTAssertFalse(controller.toggleDisabled, "The first Start must be allowed to run the connection test automatically")
    }

    @MainActor
    func testConfigurationModeIsReadOnlyDiagnosticState() {
        let controller = WorkerController()

        XCTAssertEqual(controller.debugLoggingEnabled, controller.mode == "debug")
    }
    @MainActor
    func testSharedProductionIgnoresSavedAndAmbientSettingsAndCanStopWhileConnecting() {
        let suite = "production-toggle-\(UUID().uuidString)"
        let defaults = UserDefaults(suiteName: suite)!
        defer { defaults.removePersistentDomain(forName: suite) }
        defaults.set("http://stale.invalid", forKey: "MacOCRNative.proxyURL")
        defaults.set(0, forKey: "MacOCRNative.operatorLimit")
        let controller = WorkerController(defaults: defaults, bundleSettings: [
            "MacOCRDefaultMode": "production", "MacOCRDefaultAuthMode": "shared", "MacOCRDefaultTransport": "websocket",
            "MacOCRDefaultProxyURL": "https://127.0.0.1:1", "MacOCRDefaultLimit": "6",
            "MacOCRDefaultAuthSecret": String(repeating: "k", count: 32)
        ], environment: ["MACOCR_MODE": "debug", "MACOCR_TRANSPORT": "http", "NATIVE_AUTH_SECRET": "stale-runtime-key"])
        XCTAssertTrue(controller.usesBuildConfiguration)
        XCTAssertEqual(controller.proxyURL, "https://127.0.0.1:1")
        XCTAssertEqual(controller.operatorLimit, 6)
        XCTAssertEqual(controller.mode, "production")
        XCTAssertEqual(controller.transport, "websocket")
        XCTAssertEqual(controller.authSecret, String(repeating: "k", count: 32))
        controller.setEnabled(true)
        XCTAssertEqual(controller.status, .starting)
        XCTAssertFalse(controller.toggleDisabled)
        controller.setEnabled(false)
        XCTAssertFalse(controller.startRequested)
        XCTAssertTrue(controller.status == .offline || controller.status == .stopping)
    }

}
