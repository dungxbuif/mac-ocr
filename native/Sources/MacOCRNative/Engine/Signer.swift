import Foundation
import CryptoKit

public enum Signer {
    public static func sign(secret: String, nodeId: String, timestamp: String, eventId: String, body: Data) -> String {
        let key = SymmetricKey(data: Data(secret.utf8))
        let prefix = "\(nodeId).\(timestamp).\(eventId)."
        var dataToSign = Data(prefix.utf8)
        dataToSign.append(body)

        let signature = HMAC<SHA256>.authenticationCode(for: dataToSign, using: key)
        return signature.map { String(format: "%02x", $0) }.joined()
    }
}
