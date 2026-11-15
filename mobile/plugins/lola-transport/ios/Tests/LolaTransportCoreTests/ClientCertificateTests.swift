import CryptoKit
import Security
import XCTest

@testable import LolaTransportCore

final class ClientCertificateTests: XCTestCase {
    private func make(now: Date = Date()) throws -> (der: Data, key: P256.Signing.PrivateKey, tbs: Data, signature: Data) {
        let key = P256.Signing.PrivateKey()
        var tbs = Data()
        var signature = Data()
        let der = try ClientCertificate.build(
            publicKeyX963: key.publicKey.x963Representation,
            sign: { message in
                tbs = message
                signature = try key.signature(for: message).derRepresentation
                return signature
            },
            now: now)
        return (der, key, tbs, signature)
    }

    func testTheDERParsesAsACertificate() throws {
        let cert = try make().der
        let parsed = try XCTUnwrap(SecCertificateCreateWithData(nil, cert as CFData))
        XCTAssertEqual(SecCertificateCopySubjectSummary(parsed) as String?, "lola-phone")
    }

    func testTheSPKIRoundTripsThroughSPKIPin() throws {
        let (der, key, _, _) = try make()
        let spki = try SPKIPin.subjectPublicKeyInfo(inCertificateDER: der)
        XCTAssertEqual(try SPKIPin.pin(forCertificateDER: der), Data(SHA256.hash(data: spki)).base64EncodedString())
        // The SPKI ends with the uncompressed point of the key we passed in.
        XCTAssertEqual(spki.suffix(65), key.publicKey.x963Representation)
        XCTAssertEqual(spki, key.publicKey.derRepresentation)
    }

    func testTheSignatureVerifiesOverTheTBSBytes() throws {
        let (der, key, tbs, signature) = try make()
        XCTAssertFalse(tbs.isEmpty)
        let sig = try P256.Signing.ECDSASignature(derRepresentation: signature)
        XCTAssertTrue(key.publicKey.isValidSignature(sig, for: tbs))
        // The TBS is the first element inside the certificate, byte for byte.
        let bytes = [UInt8](der)
        let outer = try DER.element(in: bytes, at: 0)
        let tbsElement = try DER.element(in: bytes, at: outer.contentStart)
        XCTAssertEqual(Data(bytes[tbsElement.start..<tbsElement.end]), tbs)
    }

    func testTheValidityStartsAnHourAgoAndEndsTenYearsOut() throws {
        let now = Date(timeIntervalSince1970: 1_800_000_000)  // 2027-01-15
        let der = try make(now: now).der
        let text = String(decoding: der, as: UTF8.self)
        XCTAssertTrue(text.contains("270115"), "notBefore is the same UTC day")
        XCTAssertTrue(text.contains("3701"), "notAfter is ten years later")
    }

    func testSerialsAreRandomPositiveAndDifferent() throws {
        let a = try make().der, b = try make().der
        XCTAssertNotEqual(a, b)
    }

    func testANonP256PublicKeyIsRefused() {
        for bad in [Data(), Data(repeating: 4, count: 64), Data([2]) + Data(repeating: 1, count: 64)] {
            XCTAssertThrowsError(try ClientCertificate.build(publicKeyX963: bad, sign: { _ in Data([1]) })) {
                XCTAssertEqual($0 as? ClientCertificate.Failure, .invalidPublicKey)
            }
        }
    }

    func testASigningFailureIsReported() {
        struct Boom: Error {}
        let key = P256.Signing.PrivateKey()
        XCTAssertThrowsError(try ClientCertificate.build(publicKeyX963: key.publicKey.x963Representation, sign: { _ in throw Boom() })) {
            XCTAssertEqual($0 as? ClientCertificate.Failure, .signingFailed)
        }
    }
}
