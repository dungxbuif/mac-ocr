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
}
