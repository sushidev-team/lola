import Foundation

/// A self-signed X.509 v3 certificate over the phone's P-256 key (04 §1.5).
///
/// The daemon trusts the SPKI only (01 §4.1), so nothing here but the key
/// matters; the certificate exists because TLS client authentication needs one.
/// The signer is injected: the plugin signs with `SecKeyCreateSignature`
/// (`.ecdsaSignatureMessageX962SHA256`, Secure Enclave when available) and
/// this file never sees a private key.
public enum ClientCertificate {
    public enum Failure: Error, Equatable {
        /// Not an uncompressed P-256 point (65 bytes, leading 0x04).
        case invalidPublicKey
        case signingFailed
    }

    public static let commonName = "lola-phone"
    private static let backdate: TimeInterval = 3600
    private static let validity: TimeInterval = 10 * 365 * 24 * 3600

    /// - Parameters:
    ///   - publicKeyX963: `SecKeyCopyExternalRepresentation` of the public key.
    ///   - sign: signs the TBS bytes, returning a DER ECDSA-Sig-Value.
    ///   - now: injected for tests.
    public static func build(
        publicKeyX963: Data,
        sign: (Data) throws -> Data,
        now: Date = Date()
    ) throws -> Data {
        guard publicKeyX963.count == 65, publicKeyX963.first == 0x04 else { throw Failure.invalidPublicKey }

        let ecdsaSHA256 = ASN1.sequence([ASN1.oid([1, 2, 840, 10045, 4, 3, 2])])
        let name = ASN1.sequence([
            ASN1.set([ASN1.sequence([ASN1.oid([2, 5, 4, 3]), ASN1.utf8String(commonName)])])
        ])
        let validityPeriod = ASN1.sequence([
            ASN1.utcTime(now.addingTimeInterval(-backdate)),
            ASN1.utcTime(now.addingTimeInterval(validity)),
        ])
        let spki = ASN1.sequence([
            ASN1.sequence([ASN1.oid([1, 2, 840, 10045, 2, 1]), ASN1.oid([1, 2, 840, 10045, 3, 1, 7])]),
            ASN1.bitString(publicKeyX963),
        ])
        // basicConstraints CA:FALSE: the DEFAULT is omitted, leaving an empty SEQUENCE.
        let basicConstraints = ASN1.sequence([
            ASN1.oid([2, 5, 29, 19]),
            ASN1.boolean(true),
            ASN1.octetString(ASN1.sequence([])),
        ])

        let tbs = ASN1.sequence([
            ASN1.explicit(0, ASN1.integer(Data([2]))),
            ASN1.integer(randomSerial()),
            ecdsaSHA256,
            name,
            validityPeriod,
            name,
            spki,
            ASN1.explicit(3, ASN1.sequence([basicConstraints])),
        ])

        let signature: Data
        do { signature = try sign(tbs) } catch { throw Failure.signingFailed }
        guard !signature.isEmpty else { throw Failure.signingFailed }

        return ASN1.sequence([tbs, ecdsaSHA256, ASN1.bitString(signature)])
    }

    /// 16 random bytes, top bit cleared (a positive INTEGER), first byte
    /// non-zero so the encoding is minimal.
    private static func randomSerial() -> Data {
        var bytes = (0..<16).map { _ in UInt8.random(in: 0...255) }
        bytes[0] &= 0x7F
        if bytes[0] == 0 { bytes[0] = 1 }
        return Data(bytes)
    }
}

/// The few DER encoders a certificate needs. Definite lengths, minimal form.
enum ASN1 {
    static func sequence(_ items: [Data]) -> Data { tlv(0x30, items.reduce(Data(), +)) }
    static func set(_ items: [Data]) -> Data { tlv(0x31, items.reduce(Data(), +)) }
    static func explicit(_ n: UInt8, _ inner: Data) -> Data { tlv(0xA0 | n, inner) }
    static func octetString(_ inner: Data) -> Data { tlv(0x04, inner) }
    static func utf8String(_ s: String) -> Data { tlv(0x0C, Data(s.utf8)) }
    static func boolean(_ b: Bool) -> Data { tlv(0x01, Data([b ? 0xFF : 0x00])) }
    static func bitString(_ bytes: Data) -> Data { tlv(0x03, Data([0x00]) + bytes) }

    /// `bytes` is a big-endian magnitude of a non-negative number; a leading
    /// zero octet is added when the top bit is set.
    static func integer(_ bytes: Data) -> Data {
        var body = bytes
        if body.first.map({ $0 & 0x80 != 0 }) ?? true { body = Data([0]) + body }
        return tlv(0x02, body)
    }

    static func oid(_ arcs: [UInt]) -> Data {
        var body = Data([UInt8(arcs[0] * 40 + arcs[1])])
        for arc in arcs.dropFirst(2) {
            var chunk = [UInt8(arc & 0x7F)]
            var rest = arc >> 7
            while rest > 0 {
                chunk.insert(UInt8(rest & 0x7F) | 0x80, at: 0)
                rest >>= 7
            }
            body.append(contentsOf: chunk)
        }
        return tlv(0x06, body)
    }

    /// UTCTime `YYMMDDHHMMSSZ`; valid for years 1950-2049.
    static func utcTime(_ date: Date) -> Data {
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = TimeZone(identifier: "UTC")!
        let c = calendar.dateComponents([.year, .month, .day, .hour, .minute, .second], from: date)
        let text = String(
            format: "%02d%02d%02d%02d%02d%02dZ",
            (c.year ?? 1970) % 100, c.month ?? 1, c.day ?? 1, c.hour ?? 0, c.minute ?? 0, c.second ?? 0)
        return tlv(0x17, Data(text.utf8))
    }

    private static func tlv(_ tag: UInt8, _ content: Data) -> Data {
        var out = Data([tag])
        let n = content.count
        if n < 0x80 {
            out.append(UInt8(n))
        } else {
            var length: [UInt8] = []
            var rest = n
            while rest > 0 { length.insert(UInt8(rest & 0xFF), at: 0); rest >>= 8 }
            out.append(0x80 | UInt8(length.count))
            out.append(contentsOf: length)
        }
        return out + content
    }
}
