import Darwin
import Foundation

public enum NativeThermalPressure: String, Codable, Sendable {
    case nominal
    case fair
    case serious
    case critical
}

public struct NativeResourceSnapshot: Sendable {
    public var processorCount: Int
    public var physicalMemoryBytes: UInt64
    public var availableMemoryBytes: UInt64
    public var thermalPressure: NativeThermalPressure

    public init(processorCount: Int, physicalMemoryBytes: UInt64, availableMemoryBytes: UInt64, thermalPressure: NativeThermalPressure) {
        self.processorCount = processorCount
        self.physicalMemoryBytes = physicalMemoryBytes
        self.availableMemoryBytes = availableMemoryBytes
        self.thermalPressure = thermalPressure
    }
}

public protocol NativeResourceProbing: Sendable {
    func snapshot() -> NativeResourceSnapshot
}

public struct NativeSystemResourceProbe: NativeResourceProbing {
    public init() {}

    public func snapshot() -> NativeResourceSnapshot {
        let info = ProcessInfo.processInfo
        let thermal: NativeThermalPressure
        switch info.thermalState {
        case .nominal: thermal = .nominal
        case .fair: thermal = .fair
        case .serious: thermal = .serious
        case .critical: thermal = .critical
        @unknown default: thermal = .serious
        }
        return NativeResourceSnapshot(
            processorCount: max(1, info.activeProcessorCount),
            physicalMemoryBytes: info.physicalMemory,
            availableMemoryBytes: availableMemory(),
            thermalPressure: thermal
        )
    }

    private func availableMemory() -> UInt64 {
        var statistics = vm_statistics64()
        var count = mach_msg_type_number_t(MemoryLayout<vm_statistics64_data_t>.stride / MemoryLayout<integer_t>.stride)
        let status = withUnsafeMutablePointer(to: &statistics) { pointer in
            pointer.withMemoryRebound(to: integer_t.self, capacity: Int(count)) {
                host_statistics64(mach_host_self(), HOST_VM_INFO64, $0, &count)
            }
        }
        guard status == KERN_SUCCESS else {
            return ProcessInfo.processInfo.physicalMemory / 4
        }
        let pages = UInt64(statistics.free_count)
            + UInt64(statistics.inactive_count)
            + UInt64(statistics.speculative_count)
        return pages * UInt64(vm_kernel_page_size)
    }
}

public struct NativeCapacityDecision: Sendable {
    public var limit: Int
    public var reason: String
}

public struct NativeAdaptiveConcurrencyPolicy: Sendable {
    public var enabled: Bool
    public var reserveCores: Int
    public var reserveMemoryBytes: UInt64
    public var memoryPerUnitBytes: UInt64
    public var imageJobUnits: Int
    public var pdfJobUnits: Int
    public var recoverySamples: Int

    public init(
        enabled: Bool = true,
        reserveCores: Int = 2,
        reserveMemoryBytes: UInt64 = 10 * 1024 * 1024 * 1024,
        memoryPerUnitBytes: UInt64 = 2 * 1024 * 1024 * 1024,
        imageJobUnits: Int = 1,
        pdfJobUnits: Int = 3,
        recoverySamples: Int = 5
    ) {
        self.enabled = enabled
        self.reserveCores = max(0, reserveCores)
        self.reserveMemoryBytes = reserveMemoryBytes
        self.memoryPerUnitBytes = max(256 * 1024 * 1024, memoryPerUnitBytes)
        self.imageJobUnits = max(1, imageJobUnits)
        self.pdfJobUnits = max(1, pdfJobUnits)
        self.recoverySamples = max(1, recoverySamples)
    }

    public static func configured(bundle: Bundle = .main, environment: [String: String] = ProcessInfo.processInfo.environment) -> NativeAdaptiveConcurrencyPolicy {
        func value(_ environmentKey: String, _ bundleKey: String) -> String? {
            environment[environmentKey] ?? (bundle.object(forInfoDictionaryKey: bundleKey) as? String)
        }
        func intValue(_ environmentKey: String, _ bundleKey: String, _ fallback: Int) -> Int {
            Int(value(environmentKey, bundleKey) ?? "") ?? fallback
        }
        func boolValue(_ environmentKey: String, _ bundleKey: String, _ fallback: Bool) -> Bool {
            guard let raw = value(environmentKey, bundleKey)?.lowercased() else { return fallback }
            if ["1", "true", "yes", "on"].contains(raw) { return true }
            if ["0", "false", "no", "off"].contains(raw) { return false }
            return fallback
        }
        let gib = UInt64(1024 * 1024 * 1024)
        return NativeAdaptiveConcurrencyPolicy(
            enabled: boolValue("MACOCR_ADAPTIVE_CONCURRENCY", "MacOCRAdaptiveConcurrency", true),
            reserveCores: intValue("MACOCR_RESERVE_CORES", "MacOCRReserveCores", 2),
            reserveMemoryBytes: UInt64(max(0, intValue("MACOCR_RESERVE_MEMORY_GB", "MacOCRReserveMemoryGB", 10))) * gib,
            memoryPerUnitBytes: UInt64(max(1, intValue("MACOCR_MEMORY_PER_UNIT_GB", "MacOCRMemoryPerUnitGB", 2))) * gib,
            imageJobUnits: intValue("MACOCR_IMAGE_JOB_UNITS", "MacOCRImageJobUnits", 1),
            pdfJobUnits: intValue("MACOCR_PDF_JOB_UNITS", "MacOCRPDFJobUnits", 3),
            recoverySamples: intValue("MACOCR_CAPACITY_RECOVERY_SAMPLES", "MacOCRCapacityRecoverySamples", 5)
        )
    }

    public func decision(operatorLimit: Int, snapshot: NativeResourceSnapshot) -> NativeCapacityDecision {
        let ceiling = max(0, operatorLimit)
        guard ceiling > 0 else { return NativeCapacityDecision(limit: 0, reason: "operator-paused") }
        guard enabled else { return NativeCapacityDecision(limit: ceiling, reason: "operator-ceiling") }
        if snapshot.thermalPressure == .critical {
            return NativeCapacityDecision(limit: 0, reason: "thermal-critical")
        }

        let coreUnits = max(0, snapshot.processorCount - reserveCores)
        let memoryUnits: Int
        if snapshot.availableMemoryBytes <= reserveMemoryBytes {
            memoryUnits = 0
        } else {
            memoryUnits = Int((snapshot.availableMemoryBytes - reserveMemoryBytes) / memoryPerUnitBytes)
        }
        var target = min(ceiling, min(coreUnits, memoryUnits))
        if snapshot.thermalPressure == .serious {
            target = min(target, 1)
        } else if snapshot.thermalPressure == .fair {
            target = min(target, max(1, coreUnits / 2))
        }
        let reason = target == 0 ? "memory-safety-reserve" : "adaptive"
        return NativeCapacityDecision(limit: max(0, target), reason: reason)
    }

    public func units(for mediaType: String?) -> Int {
        guard let mediaType = mediaType?.lowercased(), !mediaType.isEmpty else { return pdfJobUnits }
        return mediaType == "application/pdf" ? pdfJobUnits : imageJobUnits
    }
}
