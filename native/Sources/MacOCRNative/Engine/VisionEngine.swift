#if canImport(Vision) && canImport(AppKit) && canImport(PDFKit)
import Foundation
import Vision
import AppKit
import PDFKit
import CoreGraphics
import ImageIO

public enum VisionEngine {
    public static func recognize(data: Data, mediaType: String?, options: OCROptions?) throws -> OCRResult {
        if let mediaType = mediaType, mediaType.lowercased() == "application/pdf" {
            return try recognizePDF(data: data, options: options)
        }
        if data.starts(with: [0x25, 0x50, 0x44, 0x46]) {
            return try recognizePDF(data: data, options: options)
        }
        return try recognizeImage(data: data, options: options)
    }

    private static func recognizeImage(data: Data, options: OCROptions?) throws -> OCRResult {
        guard let imageSource = CGImageSourceCreateWithData(data as CFData, nil),
              let cgImage = CGImageSourceCreateImageAtIndex(imageSource, 0, nil) else {
            throw NSError(domain: "VisionEngine", code: 400, userInfo: [NSLocalizedDescriptionKey: "Failed to decode image data"])
        }

        let pageResult = try recognizeCGImage(cgImage: cgImage, pageNumber: 1, options: options)
        return OCRResult(text: pageResult.text, pageCount: 1, pages: [pageResult])
    }

    private static func recognizePDF(data: Data, options: OCROptions?) throws -> OCRResult {
        guard let pdfDocument = PDFDocument(data: data) else {
            throw NSError(domain: "VisionEngine", code: 400, userInfo: [NSLocalizedDescriptionKey: "Failed to parse PDF document"])
        }

        let pageCount = pdfDocument.pageCount
        guard pageCount > 0 else {
            throw NSError(domain: "VisionEngine", code: 400, userInfo: [NSLocalizedDescriptionKey: "PDF document contains 0 pages"])
        }

        var allPages: [OCRPageResult] = []
        var fullTextParts: [String] = []

        for pageIndex in 0..<pageCount {
            guard let page = pdfDocument.page(at: pageIndex) else { continue }
            let pageRect = page.bounds(for: .mediaBox)
            let scale: CGFloat = 2.0
            let targetSize = CGSize(width: pageRect.width * scale, height: pageRect.height * scale)

            let image = NSImage(size: targetSize)
            image.lockFocus()
            if let context = NSGraphicsContext.current?.cgContext {
                context.setFillColor(NSColor.white.cgColor)
                context.fill(CGRect(origin: .zero, size: targetSize))
                context.scaleBy(x: scale, y: scale)
                page.draw(with: .mediaBox, to: context)
            }
            image.unlockFocus()

            guard let tiffData = image.tiffRepresentation,
                  let bitmap = NSBitmapImageRep(data: tiffData),
                  let cgImage = bitmap.cgImage else {
                continue
            }

            let pageResult = try recognizeCGImage(cgImage: cgImage, pageNumber: pageIndex + 1, options: options)
            allPages.append(pageResult)
            if !pageResult.text.isEmpty {
                fullTextParts.append(pageResult.text)
            }
        }

        let fullText = fullTextParts.joined(separator: "\n\n--- PAGE BREAK ---\n\n")
        return OCRResult(text: fullText, pageCount: pageCount, pages: allPages)
    }

    private static func recognizeCGImage(cgImage: CGImage, pageNumber: Int, options: OCROptions?) throws -> OCRPageResult {
        let request = VNRecognizeTextRequest()

        if let level = options?.recognitionLevel, level.lowercased() == "fast" {
            request.recognitionLevel = .fast
        } else {
            request.recognitionLevel = .accurate
        }

        if let languages = options?.languages, !languages.isEmpty {
            request.recognitionLanguages = languages
        } else {
            request.recognitionLanguages = ["vi-VN", "en-US"]
        }

        if let autoDetect = options?.automaticallyDetectsLanguage {
            if #available(macOS 13.0, *) {
                request.automaticallyDetectsLanguage = autoDetect
            }
        } else {
            if #available(macOS 13.0, *) {
                request.automaticallyDetectsLanguage = true
            }
        }

        if let languageCorrection = options?.usesLanguageCorrection {
            request.usesLanguageCorrection = languageCorrection
        } else {
            request.usesLanguageCorrection = true
        }

        if let customWords = options?.customWords, !customWords.isEmpty {
            request.customWords = customWords
        }

        if let minHeight = options?.minimumTextHeight, minHeight > 0 {
            request.minimumTextHeight = minHeight
        }

        let handler = VNImageRequestHandler(cgImage: cgImage, options: [:])
        try handler.perform([request])

        guard let observations = request.results else {
            return OCRPageResult(pageNumber: pageNumber, text: "", blocks: [])
        }

        var blocks: [OCRBlock] = []
        var pageLines: [String] = []

        for observation in observations {
            guard let topCandidate = observation.topCandidates(1).first else { continue }
            let boundingBox = observation.boundingBox
            let bboxArray = [
                Double(boundingBox.origin.x),
                Double(boundingBox.origin.y),
                Double(boundingBox.size.width),
                Double(boundingBox.size.height)
            ]
            let block = OCRBlock(
                text: topCandidate.string,
                confidence: topCandidate.confidence,
                bbox: bboxArray
            )
            blocks.append(block)
            pageLines.append(topCandidate.string)
        }

        let pageText = pageLines.joined(separator: "\n")
        return OCRPageResult(pageNumber: pageNumber, text: pageText, blocks: blocks)
    }
}
#else
import Foundation

public enum VisionEngine {
    public static func recognize(data: Data, mediaType: String?, options: OCROptions?) throws -> OCRResult {
        let text = "NON_MACOS_SIMULATED_RESULT: Vision framework requires macOS runtime."
        let block = OCRBlock(text: text, confidence: 1.0, bbox: [0, 0, 1, 1])
        return OCRResult(text: text, pageCount: 1, pages: [OCRPageResult(pageNumber: 1, text: text, blocks: [block])])
    }
}
#endif
