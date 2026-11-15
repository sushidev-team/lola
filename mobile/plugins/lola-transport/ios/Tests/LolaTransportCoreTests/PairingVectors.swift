import Foundation
import XCTest

@testable import LolaTransportCore

/// `internal/protocol/testdata/pairing_vectors.json`, written by Go. Read by
/// path from this source file so no second copy of the contract exists;
/// `LOLA_PAIRING_VECTORS` overrides the path.
struct PairingVectors {
    struct Payload {
        let name: String
        let encoded: String
        let decoded: [String: Any]
    }

    let secret: Data
    let exporterProof: Data
    let proof: String
    let exporterCode: Data
    let daemonSPKIHash: Data
    let phoneSPKIHash: Data
    let code: String
    let payloads: [Payload]

    static func load(file: StaticString = #filePath) throws -> PairingVectors {
        let url: URL
        if let override = ProcessInfo.processInfo.environment["LOLA_PAIRING_VECTORS"] {
            url = URL(fileURLWithPath: override)
        } else {
            // .../<repo>/mobile/plugins/lola-transport/ios/Tests/LolaTransportCoreTests/PairingVectors.swift
            var root = URL(fileURLWithPath: "\(file)")
            for _ in 0..<7 { root.deleteLastPathComponent() }
            url = root.appendingPathComponent("internal/protocol/testdata/pairing_vectors.json")
        }
        let raw = try JSONSerialization.jsonObject(with: Data(contentsOf: url)) as! [String: Any]
        let proof = raw["proof"] as! [String: String]
        let code = raw["code"] as! [String: String]
        let payloads = (raw["payloads"] as! [[String: Any]]).map {
            Payload(name: $0["name"] as! String, encoded: $0["encoded"] as! String, decoded: $0["decoded"] as! [String: Any])
        }
        return PairingVectors(
            secret: Data(hexEncoded: proof["secret"]!)!,
            exporterProof: Data(hexEncoded: proof["exporterProof"]!)!,
            proof: proof["proof"]!,
            exporterCode: Data(hexEncoded: code["exporterCode"]!)!,
            daemonSPKIHash: Data(hexEncoded: code["daemonSPKIHash"]!)!,
            phoneSPKIHash: Data(hexEncoded: code["phoneSPKIHash"]!)!,
            code: code["code"]!,
            payloads: payloads)
    }

    func payload(_ name: String) -> Payload { payloads.first { $0.name == name }! }
}

/// A fixed `lola2.` string built from a JSON object, for negative cases.
func lola2(_ object: [String: Any]) -> String {
    let data = try! JSONSerialization.data(withJSONObject: object)
    return "lola2." + Base64URL.encode(data)
}
