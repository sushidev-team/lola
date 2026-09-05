import CryptoKit
import Foundation

/// Deriving a server's pin from its certificate.
///
/// The daemon publishes `base64(SHA-256(SubjectPublicKeyInfo))` — see
/// `DeviceKey.SPKIPin` in `internal/remote/identity.go`, which is what the
/// startup line `remote: ... (SPKI pin ...)` prints and what M2's pairing QR
/// will carry. Hashing the SPKI rather than the whole certificate is what lets
/// the daemon reissue a certificate over the same key without invalidating
/// every paired device, so the client must hash the same structure.
///
/// The SPKI is extracted by WALKING THE CERTIFICATE'S DER, not by asking
/// Security for the public key and rebuilding an SPKI header around it. The
/// rebuild approach needs a hard-coded ASN.1 prefix per key type — one for
/// P-256, another for RSA, another for anything later — and each of them is a
/// constant nobody notices is wrong until a certificate changes. Walking the
/// DER is key-agnostic, is about sixty lines, and, unlike anything that touches
/// `SecTrust`, can be tested on a laptop against a fixture certificate.
public enum SPKIPin {
    public enum Failure: Error, Equatable {
        case malformedDER
        case unexpectedStructure
    }

    /// The pin for a DER-encoded X.509 certificate: standard base64, padded,
    /// matching Go's `base64.StdEncoding`.
    public static func pin(forCertificateDER der: Data) throws -> String {
        let spki = try subjectPublicKeyInfo(inCertificateDER: der)
        let digest = SHA256.hash(data: spki)
        return Data(digest).base64EncodedString()
    }

    /// The raw SubjectPublicKeyInfo, as it appears inside the certificate.
    ///
    /// ```text
    /// Certificate     ::= SEQUENCE { tbsCertificate, signatureAlgorithm, signature }
    /// TBSCertificate  ::= SEQUENCE {
    ///     [0] version          OPTIONAL,
    ///     serialNumber,
    ///     signature,
    ///     issuer,
    ///     validity,
    ///     subject,
    ///     subjectPublicKeyInfo,          <- this one
    ///     ... }
    /// ```
    ///
    /// So: descend into the outer SEQUENCE, descend into the TBS SEQUENCE, skip
    /// the explicit `[0]` version if present, skip the five fields before the
    /// key, and take the next element whole.
    public static func subjectPublicKeyInfo(inCertificateDER der: Data) throws -> Data {
        let bytes = [UInt8](der)

        let certificate = try DER.element(in: bytes, at: 0)
        guard certificate.tag == DER.sequence else { throw Failure.unexpectedStructure }

        let tbs = try DER.element(in: bytes, at: certificate.contentStart)
        guard tbs.tag == DER.sequence else { throw Failure.unexpectedStructure }
        let tbsEnd = tbs.end

        var index = tbs.contentStart

        // [0] EXPLICIT version. A v1 certificate omits it and starts straight
        // at the serial number, which is why this is a test rather than an
        // unconditional skip.
        let first = try DER.element(in: bytes, at: index, limit: tbsEnd)
        if first.tag == DER.contextExplicitZero {
            index = first.end
        }

        // serialNumber, signature, issuer, validity, subject.
        for _ in 0..<5 {
            let field = try DER.element(in: bytes, at: index, limit: tbsEnd)
            index = field.end
        }

        let spki = try DER.element(in: bytes, at: index, limit: tbsEnd)
        guard spki.tag == DER.sequence else { throw Failure.unexpectedStructure }
        return Data(bytes[spki.start..<spki.end])
    }

    /// Compares two pins.
    ///
    /// A plain comparison, deliberately: the pin is a public value published in
    /// a log line and, from M2, printed in a QR code, so there is no secret here
    /// for a timing side channel to leak. Writing a constant-time compare would
    /// suggest otherwise, which is its own kind of misinformation.
    public static func matches(_ observed: String, expected: String) -> Bool {
        observed == expected
    }
}

/// The smallest DER reader that can find one field.
///
/// Only what an X.509 certificate's outer structure needs: low-tag-number
/// identifiers (every structural tag in a certificate is one) and definite
/// lengths. Indefinite length is invalid in DER and is refused rather than
/// tolerated, because tolerating it would mean guessing where a field ends.
enum DER {
    static let sequence: UInt8 = 0x30
    static let contextExplicitZero: UInt8 = 0xA0

    struct Element {
        /// Offset of the identifier octet.
        let start: Int
        /// Offset of the first content octet.
        let contentStart: Int
        /// Offset one past the last content octet.
        let end: Int
        let tag: UInt8
    }

    /// Reads the element beginning at `index`. `limit`, when given, is the end
    /// of the enclosing element: reading past it means the certificate is
    /// malformed, and catching that here is what stops a crafted length from
    /// walking the parser off into unrelated bytes.
    static func element(in bytes: [UInt8], at index: Int, limit: Int? = nil) throws -> Element {
        let end = limit ?? bytes.count
        guard index >= 0, index + 2 <= end else { throw SPKIPin.Failure.malformedDER }

        let tag = bytes[index]
        // A high-tag-number identifier (low five bits all set) never appears in
        // the structure this walker traverses.
        guard tag & 0x1F != 0x1F else { throw SPKIPin.Failure.malformedDER }

        let lengthByte = bytes[index + 1]
        var contentStart = index + 2
        var length = 0

        if lengthByte & 0x80 == 0 {
            length = Int(lengthByte)
        } else {
            let count = Int(lengthByte & 0x7F)
            // 0x80 is the indefinite form: legal in BER, forbidden in DER.
            // More than four length octets would exceed anything this protocol
            // can hold and is refused rather than accumulated into an overflow.
            guard count > 0, count <= 4, contentStart + count <= end else {
                throw SPKIPin.Failure.malformedDER
            }
            for i in 0..<count {
                length = (length << 8) | Int(bytes[contentStart + i])
            }
            contentStart += count
        }

        guard length >= 0, contentStart <= end, contentStart + length <= end else {
            throw SPKIPin.Failure.malformedDER
        }
        return Element(start: index, contentStart: contentStart, end: contentStart + length, tag: tag)
    }
}
