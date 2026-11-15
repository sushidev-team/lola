import XCTest

@testable import LolaTransportCore

final class PairHandshakeTests: XCTestCase {
    func testProofEqualsTheGoldenVectorAndIsUnpaddedURLSafe() throws {
        let v = try PairingVectors.load()
        let proof = PairHandshake.proof(secret: v.secret, exporter: v.exporterProof)
        XCTAssertEqual(proof, v.proof)
        XCTAssertFalse(proof.contains("="))
        XCTAssertFalse(proof.contains("+") || proof.contains("/"))
    }

    func testCodeEqualsTheGoldenVector() throws {
        let v = try PairingVectors.load()
        XCTAssertEqual(
            PairHandshake.code(exporterCode: v.exporterCode, daemonSPKI: v.daemonSPKIHash, clientSPKI: v.phoneSPKIHash),
            v.code)
    }

    func testTheCodeBindsBothKeys() throws {
        let v = try PairingVectors.load()
        let swapped = PairHandshake.code(exporterCode: v.exporterCode, daemonSPKI: v.phoneSPKIHash, clientSPKI: v.daemonSPKIHash)
        XCTAssertNotEqual(swapped, v.code)
        XCTAssertEqual(v.code.count, 6)
    }

    func testBodyIsTheOnlyFrameAnUnpairedCertificateMaySend() throws {
        let wire = try wireVector("req_remote_pair")
        let proof = (wire.frame["payload"] as! [String: Any])["proof"] as! String
        let name = (wire.frame["payload"] as! [String: Any])["name"] as! String
        let body = String(decoding: PairHandshake.body(name: name, proof: proof), as: UTF8.self)
        // Same bytes as Go's encoding of the vector, with the native request id.
        let expected = String(decoding: wire.body, as: UTF8.self).replacingOccurrences(of: "\"id\":\"p1\"", with: "\"id\":\"lola-pair\"")
        XCTAssertEqual(body, expected)
    }

    func testBodyEscapesTheDeviceNameLikeGo() {
        let body = String(decoding: PairHandshake.body(name: "A\"<&>\n", proof: "x"), as: UTF8.self)
        XCTAssertTrue(body.contains("\"name\":\"A\\\"\\u003c\\u0026\\u003e\\n\""))
    }

    // MARK: Reply classification (04 §1.5)

    func testPairedReplyCarriesThePairResult() throws {
        let plain = PairHandshake.classify(try frame("resp_pair_result", id: "lola-pair"))
        guard case .paired(let result) = plain else { return XCTFail("\(plain)") }
        XCTAssertEqual(result.deviceId, "dd3f0a91c2b84e57")
        XCTAssertEqual(result.tiers, ["read", "answer"])
        XCTAssertNil(result.relay)

        let relayed = PairHandshake.classify(try frame("resp_pair_result_relay", id: "lola-pair"))
        guard case .paired(let withRelay) = relayed else { return XCTFail("\(relayed)") }
        XCTAssertEqual(withRelay.relay?.rv, "rv-placeholder-0000000000000000")
        XCTAssertFalse("\(withRelay)".contains("bearer-placeholder"))
    }

    func testErrorRepliesMapToOutcomes() throws {
        XCTAssertEqual(PairHandshake.classify(try frame("err_pair_rejected", id: "lola-pair")), .rejected)
        XCTAssertEqual(PairHandshake.classify(try frame("err_pair_expired", id: "lola-pair")), .expired)
        XCTAssertEqual(PairHandshake.classify(try frame("err_pair_failed", id: "lola-pair")), .failed(reason: "pairing failed"))
        // `not_paired` carries no id.
        XCTAssertEqual(PairHandshake.classify(try wireVector("err_not_paired").body), .notPaired)
    }

    func testAnAnswerOnAnotherIdOrOfAnotherTypeIsUnrecognized() throws {
        XCTAssertEqual(PairHandshake.classify(try wireVector("resp_pair_result").body), .unrecognized)  // id p1
        XCTAssertEqual(PairHandshake.classify(try wireVector("ready").body), .unrecognized)
        XCTAssertEqual(PairHandshake.classify(Data("nope".utf8)), .unrecognized)
        XCTAssertEqual(PairHandshake.classify(Data("{\"v\":2,\"type\":\"err\",\"id\":\"lola-pair\",\"payload\":{\"code\":\"weird\"}}".utf8)), .unrecognized)
    }

    func testAnApplicationErrorResponseFailsThePairing() {
        let body = Data("{\"v\":2,\"type\":\"resp\",\"id\":\"lola-pair\",\"payload\":{\"ok\":false,\"error\":\"nope\"}}".utf8)
        XCTAssertEqual(PairHandshake.classify(body), .failed(reason: "nope"))
    }

    // MARK: helpers

    private enum File { case pairing, frames }

    private func wireVector(_ name: String, in file: File = .pairing) throws -> GoldenVector {
        let vectors = try loadVectors(file == .pairing ? "pairing.json" : "frames.json")
        return try XCTUnwrap(GoldenVectors.named(name, in: vectors))
    }

    /// A pairing.json frame, re-addressed to the native request id.
    private func frame(_ name: String, id: String) throws -> Data {
        let body = String(decoding: try wireVector(name).body, as: UTF8.self)
        return Data(body.replacingOccurrences(of: "\"id\":\"p1\"", with: "\"id\":\"\(id)\"").utf8)
    }

    private func loadVectors(_ file: String, path: StaticString = #filePath) throws -> [GoldenVector] {
        var url = URL(fileURLWithPath: "\(path)")
        for _ in 0..<6 { url.deleteLastPathComponent() }
        let data = try Data(contentsOf: url.appendingPathComponent("src/wire/testdata/\(file)"))
        let root = try JSONSerialization.jsonObject(with: data) as! [String: Any]
        return (root["cases"] as! [[String: Any]]).map {
            GoldenVector(name: $0["name"] as! String, why: "", frame: $0["frame"] as! [String: Any], framed: Data(hexEncoded: $0["hex"] as! String)!)
        }
    }
}
