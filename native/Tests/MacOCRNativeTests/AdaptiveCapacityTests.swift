import Foundation
import XCTest
@testable import MacOCRNative

private final class MutableResourceProbe: NativeResourceProbing, @unchecked Sendable {
    private let lock = NSLock()
    private var value: NativeResourceSnapshot

    init(_ value: NativeResourceSnapshot) { self.value = value }

    func snapshot() -> NativeResourceSnapshot {
        lock.lock()
        defer { lock.unlock() }
        return value
    }

    func update(_ value: NativeResourceSnapshot) {
        lock.lock()
        self.value = value
        lock.unlock()
    }
}

final class AdaptiveCapacityTests: XCTestCase {
	func testCoreReserveCanPauseAResourceConstrainedNode() {
		let policy = NativeAdaptiveConcurrencyPolicy(reserveCores: 2)
		let snapshot = NativeResourceSnapshot(
			processorCount: 2,
			physicalMemoryBytes: 32 * 1024 * 1024 * 1024,
			availableMemoryBytes: 24 * 1024 * 1024 * 1024,
			thermalPressure: .nominal
		)

		let decision = policy.decision(operatorLimit: 6, snapshot: snapshot)
		XCTAssertEqual(decision.limit, 0)
	}

    private let gib = UInt64(1024 * 1024 * 1024)

    func testPDFUsesWeightedUnitsAndCeiling() async {
        let snapshot = NativeResourceSnapshot(processorCount: 12, physicalMemoryBytes: 48 * gib, availableMemoryBytes: 20 * gib, thermalPressure: .nominal)
        let policy = NativeAdaptiveConcurrencyPolicy(reserveCores: 2, reserveMemoryBytes: 10 * gib, memoryPerUnitBytes: 2 * gib, imageJobUnits: 1, pdfJobUnits: 3, recoverySamples: 3)
        let state = NativeWorkerState(operatorLimit: 6, authSecret: "secret", nodeId: "test", adaptivePolicy: policy, resourceProbe: MutableResourceProbe(snapshot))

        var capacity = await state.getCapacity()
        XCTAssertEqual(capacity.effectiveLimit, 5)
        let first = await state.tryAcquireSlot(attemptID: "pdf-1", mediaType: "application/pdf")
        XCTAssertTrue(first.accepted)
        XCTAssertEqual(first.capacity.activeUnits, 3)
        XCTAssertEqual(first.capacity.availableUnits, 2)
        let second = await state.tryAcquireSlot(attemptID: "pdf-2", mediaType: "application/pdf")
        XCTAssertFalse(second.accepted)

        _ = await state.releaseSlot(attemptID: "pdf-1")
        capacity = await state.getCapacity()
        XCTAssertEqual(capacity.active, 0)
        XCTAssertEqual(capacity.availableUnits, 5)
    }

    func testPressureDropsImmediatelyAndRecoveryUsesHysteresis() async {
        let healthy = NativeResourceSnapshot(processorCount: 12, physicalMemoryBytes: 48 * gib, availableMemoryBytes: 20 * gib, thermalPressure: .nominal)
        let probe = MutableResourceProbe(healthy)
        let policy = NativeAdaptiveConcurrencyPolicy(reserveCores: 2, reserveMemoryBytes: 10 * gib, memoryPerUnitBytes: 2 * gib, recoverySamples: 3)
        let state = NativeWorkerState(operatorLimit: 6, authSecret: "secret", nodeId: "test", adaptivePolicy: policy, resourceProbe: probe)
        var capacity = await state.getCapacity()
        XCTAssertEqual(capacity.effectiveLimit, 5)

        probe.update(NativeResourceSnapshot(processorCount: 12, physicalMemoryBytes: 48 * gib, availableMemoryBytes: 9 * gib, thermalPressure: .nominal))
        capacity = await state.getCapacity()
        XCTAssertEqual(capacity.effectiveLimit, 0)

        probe.update(healthy)
        capacity = await state.getCapacity()
        XCTAssertEqual(capacity.effectiveLimit, 0)
        capacity = await state.getCapacity()
        XCTAssertEqual(capacity.effectiveLimit, 0)
        capacity = await state.getCapacity()
        XCTAssertEqual(capacity.effectiveLimit, 5)
    }

    func testOperatorCeilingAndCriticalThermalPressureWin() {
        let policy = NativeAdaptiveConcurrencyPolicy(reserveCores: 2, reserveMemoryBytes: 10 * gib, memoryPerUnitBytes: 2 * gib)
        let nominal = NativeResourceSnapshot(processorCount: 12, physicalMemoryBytes: 48 * gib, availableMemoryBytes: 30 * gib, thermalPressure: .nominal)
        XCTAssertEqual(policy.decision(operatorLimit: 2, snapshot: nominal).limit, 2)
        let critical = NativeResourceSnapshot(processorCount: 12, physicalMemoryBytes: 48 * gib, availableMemoryBytes: 30 * gib, thermalPressure: .critical)
        XCTAssertEqual(policy.decision(operatorLimit: 6, snapshot: critical).limit, 0)
    }
}
