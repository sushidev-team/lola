import XCTest

@testable import LolaTransportCore

final class PairPayloadTests: XCTestCase {
    private let pin = "vYMaGG1dwDptBdM/YlK+GhANzgrp66pRsZ/K22vrHto="
    private let secret = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8"
    private let tok = "XX4XJQ74NtekhCq2EFMdZTAkfluOKR1zYUtnfxqfecY"
    private let rv = "0123456789abcdef0123456789abcdef"

    func testGoldenPayloadsDecodeToTheirFields() throws {
        let vectors = try PairingVectors.load()
        XCTAssertEqual(vectors.payloads.count, 3)
        for v in vectors.payloads {
            let p = try PairPayload.decode(v.encoded)
            XCTAssertEqual(p.pin, v.decoded["pin"] as? String, v.name)
            XCTAssertEqual(p.addrs, v.decoded["addrs"] as? [String] ?? [], v.name)
            XCTAssertEqual(p.port, v.decoded["port"] as? Int ?? 0, v.name)
            XCTAssertEqual(Base64URL.encode(p.secret), v.decoded["secret"] as? String, v.name)
            let relay = v.decoded["relay"] as? [String: String]
            XCTAssertEqual(p.relay?.url, relay?["url"], v.name)
            XCTAssertEqual(p.relay?.rv, relay?["rv"], v.name)
            XCTAssertEqual(p.relay?.tok, relay?["tok"], v.name)
        }
    }

    func testDaemonIdIsTheUnpaddedURLSafePin() throws {
        let p = try PairPayload.decode(try PairingVectors.load().payload("direct").encoded)
        XCTAssertEqual(p.daemonId, "vYMaGG1dwDptBdM_YlK-GhANzgrp66pRsZ_K22vrHto")
    }

    func testAStringLongerThan1024CharactersIsMalformed() {
        let long = "lola2." + String(repeating: "A", count: 1019)
        XCTAssertEqual(long.count, 1025)
        XCTAssertThrowsError(try PairPayload.decode(long)) { XCTAssertEqual($0 as? PairPayload.Failure, .malformed) }
    }

    func testPinsThatAreNot32BytesAreMalformed() {
        for bad in ["", "AAAA", String(pin.dropLast(4)) + "====", pin + "AAAA"] {
            let s = lola2(["v": 2, "pin": bad, "addrs": ["10.0.0.2"], "port": 7717, "secret": secret])
            XCTAssertThrowsError(try PairPayload.decode(s), bad) { XCTAssertEqual($0 as? PairPayload.Failure, .malformed) }
        }
    }

    func testTheRetiredFormatAndStrangersAreTold() {
        XCTAssertThrowsError(try PairPayload.decode("lola1.abc")) { XCTAssertEqual($0 as? PairPayload.Failure, .oldFormat) }
        XCTAssertThrowsError(try PairPayload.decode("https://example.com")) { XCTAssertEqual($0 as? PairPayload.Failure, .notLola) }
        XCTAssertThrowsError(try PairPayload.decode("")) { XCTAssertEqual($0 as? PairPayload.Failure, .notLola) }
    }

    func testAnUnknownVersionIsMalformed() {
        for v in [1, 3] {
            let s = lola2(["v": v, "pin": pin, "addrs": ["10.0.0.2"], "port": 7717, "secret": secret])
            XCTAssertThrowsError(try PairPayload.decode(s)) { XCTAssertEqual($0 as? PairPayload.Failure, .malformed) }
        }
    }

    func testARelayWithoutATokIsMalformed() {
        let s = lola2(["v": 2, "pin": pin, "secret": secret, "relay": ["url": "https://hub.example.com", "rv": rv]])
        XCTAssertThrowsError(try PairPayload.decode(s)) { XCTAssertEqual($0 as? PairPayload.Failure, .malformed) }
    }

    func testRelayFieldsAreValidated() {
        let bad: [[String: String]] = [
            ["url": "http://hub.example.com", "rv": rv, "tok": tok],
            ["url": "https://hub.example.com", "rv": "ABC", "tok": tok],
            ["url": "https://hub.example.com", "rv": rv, "tok": "short"],
        ]
        for relay in bad {
            let s = lola2(["v": 2, "pin": pin, "secret": secret, "relay": relay])
            XCTAssertThrowsError(try PairPayload.decode(s), "\(relay)") { XCTAssertEqual($0 as? PairPayload.Failure, .malformed) }
        }
    }

    func testAddressRules() {
        func decode(_ addrs: [String], port: Int?) -> Error? {
            var o: [String: Any] = ["v": 2, "pin": pin, "addrs": addrs, "secret": secret]
            if let port { o["port"] = port }
            return (try? PairPayload.decode(lola2(o))) == nil ? PairPayload.Failure.malformed : nil
        }
        XCTAssertNil(decode(["10.0.0.2", "fd00::1"], port: 7717))
        XCTAssertNotNil(decode(["10.0.0.2"], port: nil), "addresses need a port")
        XCTAssertNotNil(decode(["10.0.0.2"], port: 70000))
        XCTAssertNotNil(decode(["fe80::1%en0"], port: 7717), "no zone ids")
        XCTAssertNotNil(decode(["host.local"], port: 7717), "IP literals only")
        XCTAssertNotNil(decode((1...9).map { "10.0.0.\($0)" }, port: 7717), "at most 8")
        XCTAssertNotNil(decode([], port: nil), "no address and no relay")
    }

    func testAPortWithoutAddressesIsMalformed() {
        let s = lola2(["v": 2, "pin": pin, "port": 7717, "secret": secret, "relay": ["url": "https://h.example", "rv": rv, "tok": tok]])
        XCTAssertThrowsError(try PairPayload.decode(s))
    }

    func testTheSecretMustBe32BytesInCanonicalBase64URL() {
        for bad in ["AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=", "AAECAw", String(secret.dropLast()) + "9"] {
            let s = lola2(["v": 2, "pin": pin, "addrs": ["10.0.0.2"], "port": 7717, "secret": bad])
            XCTAssertThrowsError(try PairPayload.decode(s), bad)
        }
    }

    func testUnknownFieldsAreIgnored() throws {
        let s = lola2(["v": 2, "pin": pin, "addrs": ["10.0.0.2"], "port": 7717, "secret": secret, "future": ["x": 1]])
        XCTAssertEqual(try PairPayload.decode(s).addrs, ["10.0.0.2"])
    }

    func testInvalidBase64URLOrJSONIsMalformed() {
        for s in ["lola2.", "lola2.@@@@", "lola2." + Base64URL.encode(Data("not json".utf8)), "lola2.AAAA=="] {
            XCTAssertThrowsError(try PairPayload.decode(s), s) { XCTAssertEqual($0 as? PairPayload.Failure, .malformed) }
        }
    }

    func testNeitherTheSecretNorTheTokenSurvivesIntoADescription() throws {
        let p = try PairPayload.decode(try PairingVectors.load().payload("both").encoded)
        XCTAssertFalse("\(p)".contains(secret))
        XCTAssertFalse("\(p)".contains(tok))
        XCTAssertFalse(String(reflecting: p).contains(tok))
    }
}
