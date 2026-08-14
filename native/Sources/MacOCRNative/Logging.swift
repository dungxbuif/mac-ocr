import Foundation

public enum NativeLogLevel: String, Codable, CaseIterable, Sendable {
    case debug = "DEBUG"
    case info = "INFO"
    case warning = "WARN"
    case error = "ERROR"
}

public struct NativeLogEntry: Identifiable, Codable, Sendable {
    public let id: UUID
    public let timestamp: Date
    public let level: NativeLogLevel
    public let message: String

    public init(level: NativeLogLevel, message: String) {
        self.id = UUID()
        self.timestamp = Date()
        self.level = level
        self.message = message
    }

    public var line: String {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return "\(formatter.string(from: timestamp)) [\(level.rawValue)] \(message)"
    }
}

public typealias NativeLogHandler = @Sendable (NativeLogLevel, String) -> Void

public final class NativeLogRecorder: @unchecked Sendable {
    public static let shared = NativeLogRecorder()

    private let lock = NSLock()
    private let ioQueue = DispatchQueue(label: "com.macocr.native.log", qos: .utility)
    private var storedEntries: [NativeLogEntry] = []
    private var entryHandler: ((NativeLogEntry) -> Void)?
    private var includeDebug = false
    private let maxMemoryEntries = 2_000
    private let maxFileBytes: UInt64 = 5 * 1024 * 1024
    private let maxArchivedFiles = 10
    private let maxLogAge: TimeInterval
    private let cleanupInterval: TimeInterval = 60 * 60
    private var lastCleanupAt = Date.distantPast

    public let fileURL: URL

    private init() {
        let configuredDays = ProcessInfo.processInfo.environment["MACOCR_LOG_RETENTION_DAYS"]
            ?? Bundle.main.object(forInfoDictionaryKey: "MacOCRLogRetentionDays") as? String
        let retentionDays = min(max(Int(configuredDays ?? "") ?? 30, 1), 365)
        self.maxLogAge = TimeInterval(retentionDays * 24 * 60 * 60)
        let logsRoot = FileManager.default.urls(for: .libraryDirectory, in: .userDomainMask)[0]
            .appendingPathComponent("Logs/MacOCR", isDirectory: true)
        try? FileManager.default.createDirectory(at: logsRoot, withIntermediateDirectories: true)
        self.fileURL = logsRoot.appendingPathComponent("native.log")
        cleanupExpiredLogsIfNeeded(force: true)
    }

    public func setEntryHandler(_ handler: @escaping (NativeLogEntry) -> Void) {
        lock.lock()
        entryHandler = handler
        lock.unlock()
    }

    public func emit(_ level: NativeLogLevel, _ message: String) {
        lock.lock()
        let shouldRecord = level != .debug || includeDebug
        lock.unlock()
        guard shouldRecord else { return }

        let sanitized = sanitize(message)
        let entry = NativeLogEntry(level: level, message: sanitized)

        lock.lock()
        storedEntries.append(entry)
        if storedEntries.count > maxMemoryEntries {
            storedEntries.removeFirst(storedEntries.count - maxMemoryEntries)
        }
        let handler = entryHandler
        lock.unlock()

        handler?(entry)
        ioQueue.async { [weak self] in self?.appendToFile(entry) }
    }

    public func setDebugEnabled(_ enabled: Bool) {
        lock.lock()
        includeDebug = enabled
        lock.unlock()
    }

    public func snapshot() -> [NativeLogEntry] {
        lock.lock()
        defer { lock.unlock() }
        return storedEntries
    }

    public func clear() {
        lock.lock()
        storedEntries.removeAll()
        lock.unlock()
        let archiveLimit = maxArchivedFiles
        ioQueue.async { [fileURL] in
            try? Data().write(to: fileURL, options: .atomic)
            for index in 1...archiveLimit {
                try? FileManager.default.removeItem(at: fileURL.appendingPathExtension(String(index)))
            }
        }
    }

    private func appendToFile(_ entry: NativeLogEntry) {
        cleanupExpiredLogsIfNeeded()
        rotateIfNeeded()
        let data = Data((entry.line + "\n").utf8)
        if !FileManager.default.fileExists(atPath: fileURL.path) {
            try? data.write(to: fileURL, options: .atomic)
            return
        }
        guard let handle = try? FileHandle(forWritingTo: fileURL) else { return }
        defer { try? handle.close() }
        do {
            try handle.seekToEnd()
            try handle.write(contentsOf: data)
        } catch {
            // Logging must never crash or block OCR processing.
        }
    }

    private func rotateIfNeeded() {
        guard let attrs = try? FileManager.default.attributesOfItem(atPath: fileURL.path),
              let size = attrs[.size] as? NSNumber,
              size.uint64Value >= maxFileBytes else { return }
        for index in stride(from: maxArchivedFiles, through: 2, by: -1) {
            let destination = fileURL.appendingPathExtension(String(index))
            let source = fileURL.appendingPathExtension(String(index - 1))
            try? FileManager.default.removeItem(at: destination)
            if FileManager.default.fileExists(atPath: source.path) {
                try? FileManager.default.moveItem(at: source, to: destination)
            }
        }
        let firstArchive = fileURL.appendingPathExtension("1")
        try? FileManager.default.removeItem(at: firstArchive)
        try? FileManager.default.moveItem(at: fileURL, to: firstArchive)
    }

    private func sanitize(_ message: String) -> String {
        var value = message
            .replacingOccurrences(of: "\n", with: " ")
            .replacingOccurrences(of: "\r", with: " ")

        let redactions: [(pattern: String, replacement: String)] = [
            (#"(?i)(https?://[^\s?]+)\?[^\s]+"#, "$1?[REDACTED]"),
            (#"(?i)(authorization:\s*(?:bearer\s+)?)[^\s]+"#, "$1[REDACTED]")
        ]
        for redaction in redactions {
            guard let expression = try? NSRegularExpression(pattern: redaction.pattern) else { continue }
            let range = NSRange(value.startIndex..<value.endIndex, in: value)
            value = expression.stringByReplacingMatches(in: value, range: range, withTemplate: redaction.replacement)
        }
        return value
    }

    private func cleanupExpiredLogsIfNeeded(force: Bool = false) {
        let now = Date()
        guard force || now.timeIntervalSince(lastCleanupAt) >= cleanupInterval else { return }
        lastCleanupAt = now
        let cutoff = now.addingTimeInterval(-maxLogAge)
        let candidates = [fileURL] + (1...maxArchivedFiles).map { fileURL.appendingPathExtension(String($0)) }
        for candidate in candidates {
            guard let attributes = try? FileManager.default.attributesOfItem(atPath: candidate.path),
                  let modifiedAt = attributes[.modificationDate] as? Date,
                  modifiedAt < cutoff else { continue }
            try? FileManager.default.removeItem(at: candidate)
        }
    }
}
