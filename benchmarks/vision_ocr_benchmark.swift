import CoreGraphics
import CoreText
import Foundation
import Vision

struct Fixture {
    let name: String
    let width: Int
    let height: Int
    let image: CGImage
}

struct Sample {
    let milliseconds: Double
    let observations: Int
    let characters: Int
}

struct Summary {
    let count: Int
    let mean: Double
    let minimum: Double
    let p50: Double
    let p95: Double
    let maximum: Double
}

func percentile(_ sorted: [Double], _ quantile: Double) -> Double {
    guard !sorted.isEmpty else { return 0 }
    let index = Int(ceil(quantile * Double(sorted.count))) - 1
    return sorted[max(0, min(index, sorted.count - 1))]
}

func summarize(_ samples: [Sample]) -> Summary {
    let values = samples.map(\.milliseconds).sorted()
    return Summary(
        count: values.count,
        mean: values.reduce(0, +) / Double(max(1, values.count)),
        minimum: values.first ?? 0,
        p50: percentile(values, 0.50),
        p95: percentile(values, 0.95),
        maximum: values.last ?? 0
    )
}

func makeFixture(name: String, width: Int, height: Int, fontSize: CGFloat, repetitions: Int) -> Fixture {
    let colorSpace = CGColorSpaceCreateDeviceRGB()
    let bitmapInfo = CGImageAlphaInfo.premultipliedLast.rawValue
    guard let context = CGContext(
        data: nil,
        width: width,
        height: height,
        bitsPerComponent: 8,
        bytesPerRow: 0,
        space: colorSpace,
        bitmapInfo: bitmapInfo
    ) else {
        fatalError("Unable to create bitmap context")
    }

    context.setFillColor(CGColor(gray: 1, alpha: 1))
    context.fill(CGRect(x: 0, y: 0, width: width, height: height))

    let paragraph = """
    HÓA ĐƠN GIÁ TRỊ GIA TĂNG — VAT INVOICE
    Mã số thuế: 0312345678    Số hóa đơn: HD-2026-0812
    Khách hàng: Công ty Công nghệ Việt Nam
    Địa chỉ: 123 Nguyễn Huệ, Thành phố Hồ Chí Minh
    Nội dung: Dịch vụ xử lý tài liệu và nhận dạng ký tự
    Số lượng: 12    Đơn giá: 125.000 đ    Thành tiền: 1.500.000 đ
    Thuế GTGT 10%: 150.000 đ    Tổng cộng: 1.650.000 đ
    Amount in words: One million six hundred fifty thousand đồng.
    Reference: OCR-BENCH-M4PRO-2026 / Page 01
    """
    let content = Array(repeating: paragraph, count: repetitions).joined(separator: "\n")
    let font = CTFontCreateWithName("Helvetica" as CFString, fontSize, nil)
    let attributes: [NSAttributedString.Key: Any] = [
        NSAttributedString.Key(kCTFontAttributeName as String): font,
        NSAttributedString.Key(kCTForegroundColorAttributeName as String): CGColor(gray: 0.05, alpha: 1)
    ]
    let attributed = NSAttributedString(string: content, attributes: attributes)
    let framesetter = CTFramesetterCreateWithAttributedString(attributed)
    let margin = CGFloat(max(40, width / 30))
    let path = CGPath(rect: CGRect(
        x: margin,
        y: margin,
        width: CGFloat(width) - 2 * margin,
        height: CGFloat(height) - 2 * margin
    ), transform: nil)
    let frame = CTFramesetterCreateFrame(framesetter, CFRange(), path, nil)
    CTFrameDraw(frame, context)

    guard let image = context.makeImage() else {
        fatalError("Unable to create fixture image")
    }
    return Fixture(name: name, width: width, height: height, image: image)
}

func recognize(_ image: CGImage, level: VNRequestTextRecognitionLevel) throws -> Sample {
    let request = VNRecognizeTextRequest()
    request.recognitionLevel = level
    request.recognitionLanguages = ["vi-VN", "en-US"]
    request.automaticallyDetectsLanguage = true
    request.usesLanguageCorrection = level == .accurate

    let start = DispatchTime.now().uptimeNanoseconds
    let handler = VNImageRequestHandler(cgImage: image, orientation: .up, options: [:])
    try handler.perform([request])
    let end = DispatchTime.now().uptimeNanoseconds

    let observations = request.results ?? []
    let characters = observations.compactMap { $0.topCandidates(1).first?.string.count }.reduce(0, +)
    return Sample(
        milliseconds: Double(end - start) / 1_000_000,
        observations: observations.count,
        characters: characters
    )
}

func runConcurrent(image: CGImage, level: VNRequestTextRecognitionLevel, concurrency: Int, jobs: Int) -> (samples: [Sample], wallMs: Double) {
    let queue = DispatchQueue(label: "vision-benchmark", attributes: .concurrent)
    let group = DispatchGroup()
    let lock = NSLock()
    var samples: [Sample] = []
    var nextJob = 0
    var firstError: Error?

    let wallStart = DispatchTime.now().uptimeNanoseconds
    for _ in 0..<concurrency {
        group.enter()
        queue.async {
            defer { group.leave() }
            while true {
                lock.lock()
                guard nextJob < jobs else {
                    lock.unlock()
                    break
                }
                nextJob += 1
                lock.unlock()

                do {
                    let sample = try recognize(image, level: level)
                    lock.lock()
                    samples.append(sample)
                    lock.unlock()
                } catch {
                    lock.lock()
                    if firstError == nil { firstError = error }
                    lock.unlock()
                }
            }
        }
    }
    group.wait()
    if let firstError {
        fputs("CONCURRENCY_ERROR\t\(firstError)\n", stderr)
    }
    let wallEnd = DispatchTime.now().uptimeNanoseconds
    return (samples, Double(wallEnd - wallStart) / 1_000_000)
}

let arguments = CommandLine.arguments
let iterations = arguments.count > 1 ? max(3, Int(arguments[1]) ?? 8) : 8
let concurrentJobs = arguments.count > 2 ? max(8, Int(arguments[2]) ?? 16) : 16

let fixtures = [
    makeFixture(name: "receipt-1.8mp", width: 1200, height: 1500, fontSize: 31, repetitions: 2),
    makeFixture(name: "a4-8.7mp", width: 2480, height: 3508, fontSize: 42, repetitions: 8),
    makeFixture(name: "large-12mp", width: 4000, height: 3000, fontSize: 48, repetitions: 9)
]

let process = ProcessInfo.processInfo
print("META\tos=\(process.operatingSystemVersionString)\tcores=\(process.activeProcessorCount)\tmemory_bytes=\(process.physicalMemory)\tthermal=\(process.thermalState.rawValue)")
print("SERIAL\tfixture\twidth\theight\tmegapixels\tmode\tn\tmean_ms\tmin_ms\tp50_ms\tp95_ms\tmax_ms\tobservations\tcharacters")

for fixture in fixtures {
    for (modeName, level) in [("fast", VNRequestTextRecognitionLevel.fast), ("accurate", VNRequestTextRecognitionLevel.accurate)] {
        do {
            _ = try recognize(fixture.image, level: level)
        } catch {
            fputs("WARMUP_ERROR\t\(fixture.name)\t\(modeName)\t\(error)\n", stderr)
        }
        var samples: [Sample] = []
        for _ in 0..<iterations {
            do {
                let sample = try recognize(fixture.image, level: level)
                samples.append(sample)
            } catch {
                fputs("SERIAL_ERROR\t\(fixture.name)\t\(modeName)\t\(error)\n", stderr)
                break
            }
        }
        let summary = summarize(samples)
        let last = samples.last
        let megapixels = Double(fixture.width * fixture.height) / 1_000_000
        print(String(format: "SERIAL\t%@\t%d\t%d\t%.2f\t%@\t%d\t%.2f\t%.2f\t%.2f\t%.2f\t%.2f\t%d\t%d",
                     fixture.name, fixture.width, fixture.height, megapixels, modeName, summary.count,
                     summary.mean, summary.minimum, summary.p50, summary.p95, summary.maximum,
                     last?.observations ?? 0, last?.characters ?? 0))
    }
}

print("CONCURRENCY\tfixture\tmode\tconcurrency\tjobs\twall_ms\tthroughput_jobs_s\tmean_ms\tp50_ms\tp95_ms\tmax_ms")
let concurrencyFixture = fixtures[1]
for (modeName, level) in [("fast", VNRequestTextRecognitionLevel.fast), ("accurate", VNRequestTextRecognitionLevel.accurate)] {
    _ = try? recognize(concurrencyFixture.image, level: level)
    for concurrency in [1, 2, 4] {
        let result = runConcurrent(
            image: concurrencyFixture.image,
            level: level,
            concurrency: concurrency,
            jobs: concurrentJobs
        )
        let summary = summarize(result.samples)
        let throughput = Double(result.samples.count) / (result.wallMs / 1000)
        print(String(format: "CONCURRENCY\t%@\t%@\t%d\t%d\t%.2f\t%.3f\t%.2f\t%.2f\t%.2f\t%.2f",
                     concurrencyFixture.name, modeName, concurrency, result.samples.count, result.wallMs, throughput,
                     summary.mean, summary.p50, summary.p95, summary.maximum))
    }
}
