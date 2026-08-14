// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "mac-ocr-native",
    platforms: [
        .macOS(.v13)
    ],
    products: [
        .executable(name: "mac-ocr-native", targets: ["MacOCRNative"])
    ],
    targets: [
        .executableTarget(
            name: "MacOCRNative",
            path: "Sources/MacOCRNative"
        ),
        .testTarget(
            name: "MacOCRNativeTests",
            dependencies: ["MacOCRNative"],
            path: "Tests/MacOCRNativeTests"
        )
    ]
)
