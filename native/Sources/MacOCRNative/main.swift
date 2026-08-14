import Foundation
import Darwin

let portStr = ProcessInfo.processInfo.environment["NATIVE_PORT"] ?? "8787"
guard let port = UInt16(portStr), port > 0 else {
    fputs("Fatal: NATIVE_PORT must be an integer between 1 and 65535\n", stderr)
    exit(1)
}

let limitStr = ProcessInfo.processInfo.environment["NATIVE_LIMIT"] ?? "2"
guard let limit = Int(limitStr), (0...256).contains(limit) else {
    fputs("Fatal: NATIVE_LIMIT must be an integer between 0 and 256\n", stderr)
    exit(1)
}

let secret = ProcessInfo.processInfo.environment["NATIVE_AUTH_SECRET"] ?? "change-me-in-production"
let environment = ProcessInfo.processInfo.environment["APP_ENV"] ?? "development"
if environment == "production" && (secret == "change-me-in-production" || secret.utf8.count < 32) {
    fputs("Fatal: production NATIVE_AUTH_SECRET must be a non-default value of at least 32 bytes\n", stderr)
    exit(1)
}
let nodeId = ProcessInfo.processInfo.environment["NATIVE_NODE_ID"] ?? "ocr-native-01"
guard !nodeId.isEmpty, nodeId.utf8.count <= 128 else {
    fputs("Fatal: NATIVE_NODE_ID must contain 1 to 128 bytes\n", stderr)
    exit(1)
}

let state = NativeWorkerState(operatorLimit: limit, authSecret: secret, nodeId: nodeId)
let server = NativeHTTPServer(port: port, state: state)

do {
    try server.start()
    signal(SIGINT, SIG_IGN)
    signal(SIGTERM, SIG_IGN)
    let interruptSource = DispatchSource.makeSignalSource(signal: SIGINT, queue: .main)
    let terminateSource = DispatchSource.makeSignalSource(signal: SIGTERM, queue: .main)
    let stop: () -> Void = {
        server.stop()
        exit(0)
    }
    interruptSource.setEventHandler(handler: stop)
    terminateSource.setEventHandler(handler: stop)
    interruptSource.resume()
    terminateSource.resume()
    print("OCR Native service running on port \(port) (concurrency limit: \(limit), node: \(nodeId))")
    dispatchMain()
} catch {
    print("Fatal: failed to start native server: \(error)")
    exit(1)
}
