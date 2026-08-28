import XCTest

@testable import LolaTransportCore

/// The pin, checked against real certificates.
///
/// The fixtures below are throwaway self-signed certificates generated with
/// OpenSSL. The EC one has the same shape as the daemon's own identity
/// (`internal/remote/identity.go`: ECDSA P-256, `CN=lola`, a `DNSNames` entry
/// and loopback IP SANs, ten years of validity); the RSA one exists only to
/// prove the extraction is key-agnostic, because the alternative implementation
/// — asking Security for the public key and rebuilding an SPKI header around it
/// — needs a different hard-coded ASN.1 prefix per key type and silently
/// produces a wrong hash for any type it does not know.
///
/// The expected pins were produced independently of this code:
///
///     openssl x509 -in cert.pem -pubkey -noout |
///       openssl pkey -pubin -outform DER |
///       openssl dgst -sha256 -binary | openssl base64
///
/// which is the same computation `DeviceKey.SPKIPin` performs in Go. A test
/// whose expected value came from the implementation under test would prove
/// only that the implementation is consistent with itself.
final class SPKIPinTests: XCTestCase {

    /// ECDSA P-256, CN=lola, SAN DNS:lola + IP:127.0.0.1 — the daemon's shape.
    static let ecCertificateBase64 = """
        MIIBiTCCATCgAwIBAgIUDoDJ/t1d7Z0dgGNuRUzjLJf9VqUwCgYIKoZIzj0EAwIwDzENMAsGA1UEA\
        wwEbG9sYTAeFw0yNjA4MjgxNzUwMTNaFw0zNjA4MjUxNzUwMTNaMA8xDTALBgNVBAMMBGxvbGEwWT\
        ATBgcqhkjOPQIBBggqhkjOPQMBBwNCAATcIWJ3dgX8NVSNNIkQonVpdOS77GLlGp/by7CV39poR8b\
        LNZJTbQ8kj+5LIqqakSHJiIoGg8glHxgG1ux452vDo2owaDAdBgNVHQ4EFgQUdWyxgndS6phLnXu5\
        l0LwmWfmSmQwHwYDVR0jBBgwFoAUdWyxgndS6phLnXu5l0LwmWfmSmQwDwYDVR0TAQH/BAUwAwEB/\
        zAVBgNVHREEDjAMggRsb2xhhwR/AAABMAoGCCqGSM49BAMCA0cAMEQCIFEvEHhC+Rem9Al00gtUFe\
        Op9HasKFLDnl0KXvAb9UqGAiBvAMadwHYdwwAAs/qG1F+dSZcVAfqzbqZgy4n19auyPQ==
        """

    static let ecPin = "r3NLB1UKu3NDu+F6VuMU4kPWMWohy7c3hCfi6TkcMTw="

    /// The SPKI itself, so a failure says whether the walk or the hash is wrong.
    static let ecSPKIBase64 = """
        MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAE3CFid3YF/DVUjTSJEKJ1aXTku+xi5Rqf28uwld/aa\
        EfGyzWSU20PJI/uSyKqmpEhyYiKBoPIJR8YBtbseOdrww==
        """

    /// RSA 2048. A different key type, a different SPKI size, and long-form DER
    /// lengths throughout — which is the case a short-form-only length reader
    /// gets wrong.
    static let rsaCertificateBase64 = """
        MIIDBzCCAe+gAwIBAgIUdqULHLj5vkmmt3EcYn4JqtnCorAwDQYJKoZIhvcNAQELBQAwEzERMA8GA\
        1UEAwwIbG9sYS1yc2EwHhcNMjYwODI4MTc1MDM4WhcNMzYwODI1MTc1MDM4WjATMREwDwYDVQQDDA\
        hsb2xhLXJzYTCCASIwDQYJKoZIhvcNAQEBBQADggEPADCCAQoCggEBAKyEwY9AWqEqz/nTnLNV4II\
        QBa1dETuA93cENlVJzvd4VKTUsQpxHtsRfzCoFoOLFyqrWPGj51SugQ51NTeFrQ0t/aJinDQVLeMM\
        0C/+C1G2ulOTQQVKTPjBDTTXtnNXIIiNyhloC5eT5WluECFA49mWtM2lig09dLdX/nkLLpDHh9K/z\
        6ToaxoCaK0MlUzlYOna07SOKn2mGccJXYGroN+TzBEfFr9uqJBEtkfEFnQ6FPamaMNfyLoGW4ryYF\
        wPtx/g/e0KV1Bzb+Ko1DfSCr1IjNwFYyQecB/Oa0ZQ3Jn783G+Buuk9KEfGXBBUOyh5Ik3VhzaWEj\
        C/Ma0aqwY4jUCAwEAAaNTMFEwHQYDVR0OBBYEFM4DcJ6HTTGJhByp7PSBZOEcKPTqMB8GA1UdIwQY\
        MBaAFM4DcJ6HTTGJhByp7PSBZOEcKPTqMA8GA1UdEwEB/wQFMAMBAf8wDQYJKoZIhvcNAQELBQADg\
        gEBAI3eckvDGW7w4OHZUkQVitXLF/NxhNl8uCCs3rIP74niUz9giTK3ATzBb3S7xPXpeZEYlFBJ9h\
        2EhjbldStQm5K6VP6Ao1o6aDccZ7R26Y0T3/cXnL+tkJzbENu7L/koFdtc+S06LGClH4uzD+hLuzD\
        LLqQSe1OFYUD0RwtoaBMPsqxbAPctD8bdNTqf/8lXuybWAEy7nHBKY5E5BgKTh6KbMXrq55BulBk7\
        a6YszsvGPc+QhYGYOzM0qBYMv+obp9evce7b6b2qvoGR6rhVlePpClpOxcKbecftYePalavqFg/5S\
        WoCvC51brBzE7q/6O2hSVWYYxkvISvvYi7CvtM=
        """

    static let rsaPin = "cFoF8mCWS7odRNQD4ESM3GQF2PeGniyHAtG5R6laOxQ="

    private func der(_ base64: String) throws -> Data {
        try XCTUnwrap(Data(base64Encoded: base64.replacingOccurrences(of: "\n", with: "")))
    }

    func testPinMatchesOpenSSLForAnECCertificate() throws {
        let pin = try SPKIPin.pin(forCertificateDER: der(Self.ecCertificateBase64))
        XCTAssertEqual(pin, Self.ecPin)
    }

    func testPinMatchesOpenSSLForAnRSACertificate() throws {
        let pin = try SPKIPin.pin(forCertificateDER: der(Self.rsaCertificateBase64))
        XCTAssertEqual(pin, Self.rsaPin)
    }

    func testTheExtractedSPKIIsTheWholeStructureIncludingItsHeader() throws {
        let spki = try SPKIPin.subjectPublicKeyInfo(
            inCertificateDER: der(Self.ecCertificateBase64))
        let expected = try der(Self.ecSPKIBase64)
        XCTAssertEqual(spki, expected)
        // The hash is over SubjectPublicKeyInfo, not over the bare key bits.
        // Hashing the wrong one produces a stable, plausible, useless pin.
        XCTAssertEqual(spki.first, 0x30, "an SPKI is a SEQUENCE")
    }

    func testPinIsPaddedStandardBase64() throws {
        let pin = try SPKIPin.pin(forCertificateDER: der(Self.ecCertificateBase64))
        // Go's base64.StdEncoding, the same encoding HPKP used, so a value
        // pasted between tools means one thing. A URL-safe or unpadded variant
        // would never match a pin copied out of the daemon's log.
        XCTAssertEqual(pin.count, 44)
        XCTAssertTrue(pin.hasSuffix("="))
        XCTAssertFalse(pin.contains("-"))
        XCTAssertFalse(pin.contains("_"))
    }

    func testMatchesIsExact() {
        XCTAssertTrue(SPKIPin.matches(Self.ecPin, expected: Self.ecPin))
        XCTAssertFalse(SPKIPin.matches(Self.ecPin, expected: Self.rsaPin))
        // No trimming, no case folding, no tolerance for a missing pad. A pin
        // that "nearly" matches is a pin that does not match.
        XCTAssertFalse(SPKIPin.matches(Self.ecPin, expected: Self.ecPin + " "))
        XCTAssertFalse(
            SPKIPin.matches(Self.ecPin, expected: String(Self.ecPin.dropLast())))
    }

    func testGarbageIsRefusedRatherThanHashed() {
        // Fails closed. Hashing whatever bytes arrived would produce a pin that
        // is stable and wrong, which is the worst possible outcome for a value
        // whose entire job is to be compared.
        XCTAssertThrowsError(try SPKIPin.pin(forCertificateDER: Data()))
        XCTAssertThrowsError(try SPKIPin.pin(forCertificateDER: Data([0x30])))
        XCTAssertThrowsError(try SPKIPin.pin(forCertificateDER: Data([0x30, 0x82, 0xFF])))
        XCTAssertThrowsError(
            try SPKIPin.pin(forCertificateDER: Data(repeating: 0x41, count: 64)))
    }

    func testATruncatedCertificateDoesNotWalkPastItsEnd() throws {
        // A crafted length inside a truncated certificate is the way a DER
        // walker gets talked into reading unrelated bytes. Every prefix of a
        // real certificate must either parse or fail, and never return
        // something.
        let full = try der(Self.ecCertificateBase64)
        for length in stride(from: 1, to: full.count, by: 7) {
            let truncated = full.prefix(length)
            if let spki = try? SPKIPin.subjectPublicKeyInfo(inCertificateDER: truncated) {
                XCTAssertTrue(
                    full.range(of: spki) != nil,
                    "a prefix of \(length) bytes produced an SPKI that is not in the certificate")
            }
        }
    }

    func testIndefiniteLengthIsRefused() {
        // Legal in BER, forbidden in DER, and accepting it would mean guessing
        // where a field ends.
        let indefinite = Data([0x30, 0x80, 0x30, 0x80, 0x00, 0x00, 0x00, 0x00])
        XCTAssertThrowsError(try SPKIPin.subjectPublicKeyInfo(inCertificateDER: indefinite))
    }
}
