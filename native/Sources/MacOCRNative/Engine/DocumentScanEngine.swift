#if canImport(Vision) && canImport(AppKit) && canImport(CoreImage)
import AppKit
import CoreGraphics
import CoreImage
import Foundation
import ImageIO
import Vision

/// Produces a clean document image. This is intentionally separate from OCR:
/// callers can submit the resulting image to OCR only when they need text.
public enum DocumentScanEngine {
    private static let context = CIContext()

    public static func scan(data: Data) throws -> Data {
        guard let source = CGImageSourceCreateWithData(data as CFData, nil),
              let image = CGImageSourceCreateImageAtIndex(source, 0, nil) else {
            throw NSError(domain: "DocumentScanEngine", code: 400, userInfo: [NSLocalizedDescriptionKey: "Failed to decode image data"])
        }

        let corrected = try perspectiveCorrectedImage(from: image) ?? image
        let enhanced = enhancedImage(from: corrected)
        guard let jpeg = jpegData(from: enhanced) else {
            throw NSError(domain: "DocumentScanEngine", code: 422, userInfo: [NSLocalizedDescriptionKey: "Failed to encode scanned image"])
        }
        return jpeg
    }

    private static func perspectiveCorrectedImage(from image: CGImage) throws -> CGImage? {
        let request = VNDetectRectanglesRequest()
        request.maximumObservations = 1
        request.minimumConfidence = 0.65
        request.minimumSize = 0.20
        request.minimumAspectRatio = 0.15
        request.maximumAspectRatio = 1.0
        request.quadratureTolerance = 30

        let handler = VNImageRequestHandler(cgImage: image, options: [:])
        try handler.perform([request])
        guard let rectangle = request.results?.first else { return nil }

        let ciImage = CIImage(cgImage: image)
        let width = ciImage.extent.width
        let height = ciImage.extent.height
        func point(_ normalized: CGPoint) -> CGPoint {
            CGPoint(x: normalized.x * width, y: normalized.y * height)
        }

        guard let filter = CIFilter(name: "CIPerspectiveCorrection") else { return nil }
        filter.setValue(ciImage, forKey: kCIInputImageKey)
        filter.setValue(CIVector(cgPoint: point(rectangle.topLeft)), forKey: "inputTopLeft")
        filter.setValue(CIVector(cgPoint: point(rectangle.topRight)), forKey: "inputTopRight")
        filter.setValue(CIVector(cgPoint: point(rectangle.bottomLeft)), forKey: "inputBottomLeft")
        filter.setValue(CIVector(cgPoint: point(rectangle.bottomRight)), forKey: "inputBottomRight")
        filter.setValue(true, forKey: "inputCrop")
        guard let output = filter.outputImage else { return nil }
        return context.createCGImage(output, from: output.extent)
    }

    private static func enhancedImage(from image: CGImage) -> CGImage {
        guard let color = CIFilter(name: "CIColorControls"),
              let sharpen = CIFilter(name: "CIUnsharpMask") else { return image }
        color.setValue(CIImage(cgImage: image), forKey: kCIInputImageKey)
        color.setValue(0, forKey: kCIInputSaturationKey)
        color.setValue(1.12, forKey: kCIInputContrastKey)
        color.setValue(0, forKey: kCIInputBrightnessKey)

        sharpen.setValue(color.outputImage, forKey: kCIInputImageKey)
        sharpen.setValue(1.5, forKey: kCIInputRadiusKey)
        sharpen.setValue(0.45, forKey: kCIInputIntensityKey)
        guard let output = sharpen.outputImage,
              let rendered = context.createCGImage(output, from: output.extent) else {
            return image
        }
        return rendered
    }

    private static func jpegData(from image: CGImage) -> Data? {
        let bitmap = NSBitmapImageRep(cgImage: image)
        return bitmap.representation(using: .jpeg, properties: [.compressionFactor: 0.92])
    }
}
#else
import Foundation

public enum DocumentScanEngine {
    public static func scan(data: Data) throws -> Data {
        throw NSError(domain: "DocumentScanEngine", code: 501, userInfo: [NSLocalizedDescriptionKey: "Document scan requires macOS Vision and Core Image"])
    }
}
#endif
