import Foundation

public struct OCROptions: Codable {
    public var recognitionLevel: String?
    public var languages: [String]?
    public var automaticallyDetectsLanguage: Bool?
    public var usesLanguageCorrection: Bool?
    public var customWords: [String]?
    public var minimumTextHeight: Float?

    public init(
        recognitionLevel: String? = nil,
        languages: [String]? = nil,
        automaticallyDetectsLanguage: Bool? = nil,
        usesLanguageCorrection: Bool? = nil,
        customWords: [String]? = nil,
        minimumTextHeight: Float? = nil
    ) {
        self.recognitionLevel = recognitionLevel
        self.languages = languages
        self.automaticallyDetectsLanguage = automaticallyDetectsLanguage
        self.usesLanguageCorrection = usesLanguageCorrection
        self.customWords = customWords
        self.minimumTextHeight = minimumTextHeight
    }
}

public struct NativeInputRef: Codable {
    public var url: String
    public var mediaType: String?
    public var sha256: String?

    public init(url: String, mediaType: String? = nil, sha256: String? = nil) {
        self.url = url
        self.mediaType = mediaType
        self.sha256 = sha256
    }
}

public struct NativeCallbackRef: Codable {
    public var url: String

    public init(url: String) {
        self.url = url
    }
}

public struct NativeOCRRequest: Codable {
    public var documentId: String
    public var pageId: String?
    public var attemptId: String
    public var input: NativeInputRef
    public var options: OCROptions?
    public var callback: NativeCallbackRef

    public init(
        documentId: String,
        pageId: String? = nil,
        attemptId: String,
        input: NativeInputRef,
        options: OCROptions? = nil,
        callback: NativeCallbackRef
    ) {
        self.documentId = documentId
        self.pageId = pageId
        self.attemptId = attemptId
        self.input = input
        self.options = options
        self.callback = callback
    }
}

public struct AcceptedResponse: Codable {
    public var attemptId: String
    public var status: String

    public init(attemptId: String, status: String = "accepted") {
        self.attemptId = attemptId
        self.status = status
    }
}

public struct OCRBlock: Codable {
    public var text: String
    public var confidence: Float
    public var bbox: [Double]

    public init(text: String, confidence: Float, bbox: [Double]) {
        self.text = text
        self.confidence = confidence
        self.bbox = bbox
    }
}

public struct OCRPageResult: Codable {
    public var pageNumber: Int
    public var text: String
    public var blocks: [OCRBlock]

    public init(pageNumber: Int, text: String, blocks: [OCRBlock]) {
        self.pageNumber = pageNumber
        self.text = text
        self.blocks = blocks
    }
}

public struct OCRResult: Codable {
    public var text: String
    public var pageCount: Int
    public var pages: [OCRPageResult]

    public init(text: String, pageCount: Int, pages: [OCRPageResult]) {
        self.text = text
        self.pageCount = pageCount
        self.pages = pages
    }
}

public struct NativeCapacity: Codable {
    public var configVersion: UInt64
    public var state: String
    public var operatorLimit: Int
    public var effectiveLimit: Int
    public var active: Int
    public var available: Int

    public init(
        configVersion: UInt64,
        state: String,
        operatorLimit: Int,
        effectiveLimit: Int,
        active: Int,
        available: Int
    ) {
        self.configVersion = configVersion
        self.state = state
        self.operatorLimit = operatorLimit
        self.effectiveLimit = effectiveLimit
        self.active = active
        self.available = available
    }
}

public struct CapacityUnavailableResponse: Codable {
    public var error: String
    public var capacity: NativeCapacity

    public init(error: String, capacity: NativeCapacity) {
        self.error = error
        self.capacity = capacity
    }
}

public struct NativeEvent: Codable {
    public var eventId: String
    public var type: String
    public var nodeId: String
    public var bootId: String
    public var sequence: UInt64
    public var attemptId: String
    public var documentId: String
    public var result: OCRResult?
    public var error: String?
    public var capacity: NativeCapacity
    public var occurredAt: String

    public init(
        eventId: String,
        type: String,
        nodeId: String,
        bootId: String,
        sequence: UInt64,
        attemptId: String,
        documentId: String,
        result: OCRResult? = nil,
        error: String? = nil,
        capacity: NativeCapacity,
        occurredAt: String
    ) {
        self.eventId = eventId
        self.type = type
        self.nodeId = nodeId
        self.bootId = bootId
        self.sequence = sequence
        self.attemptId = attemptId
        self.documentId = documentId
        self.result = result
        self.error = error
        self.capacity = capacity
        self.occurredAt = occurredAt
    }
}
