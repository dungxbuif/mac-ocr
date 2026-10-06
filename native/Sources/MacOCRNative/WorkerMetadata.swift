import Foundation
import Darwin

struct WorkerMetadata: Codable, Equatable {
    let model: String
    let user: String
    let hostname: String
    let os: String
    let architecture: String
    let cpuCores: Int
    let memoryBytes: UInt64

    init(model: String, user: String, hostname: String, os: String, architecture: String, cpuCores: Int, memoryBytes: UInt64) {
        func bounded(_ value: String) -> String {
            var result = ""
            for scalar in value.unicodeScalars {
                let part = String(scalar)
                if result.utf8.count + part.utf8.count > 256 { break }
                result += part
            }
            return result
        }
        self.model = bounded(model); self.user = bounded(user)
        self.hostname = bounded(hostname); self.os = bounded(os)
        self.architecture = bounded(architecture)
        self.cpuCores = max(1, cpuCores); self.memoryBytes = memoryBytes
    }

    static func collect() -> WorkerMetadata {
        let info = ProcessInfo.processInfo
        let translated = sysctlString("sysctl.proc_translated") == "1"
        let machine = sysctlString("hw.machine") ?? "unknown"
        let architecture = translated ? "arm64 (Rosetta x86_64)" : machine
        return WorkerMetadata(model: sysctlString("hw.model") ?? "Mac", user: NSUserName(), hostname: info.hostName,
            os: "macOS " + info.operatingSystemVersionString, architecture: architecture,
            cpuCores: info.activeProcessorCount, memoryBytes: info.physicalMemory)
    }

    private static func sysctlString(_ key: String) -> String? {
        var size = 0
        guard sysctlbyname(key, nil, &size, nil, 0) == 0, size > 0, size <= 4096 else { return nil }
        var bytes = [UInt8](repeating: 0, count: size)
        guard sysctlbyname(key, &bytes, &size, nil, 0) == 0 else { return nil }
        if key == "sysctl.proc_translated", bytes.count >= 4 {
            let value = bytes.withUnsafeBytes { $0.loadUnaligned(as: Int32.self) }
            return String(value)
        }
        return String(bytes: bytes.prefix { $0 != 0 }, encoding: .utf8)
    }
}
