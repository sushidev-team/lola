import XCTest

@testable import LolaTransportCore

/// The framing, checked against the same bytes the Go and TypeScript codecs are
/// checked against.
final class FrameCodecTests: XCTestCase {

    func testEveryGoldenVectorEncodesToItsStatedBytes() throws {
        let vectors = try GoldenVectors.load()
        XCTAssertEqual(vectors.count, 20, "the vector file should carry twenty cases")

        for vector in vectors {
            let framed = try FrameEncoder.encode(body: vector.body)
            XCTAssertEqual(
                framed.hexEncoded, vector.framed.hexEncoded,
                "\(vector.name): \(vector.why)")
        }
    }

    func testEveryGoldenVectorDecodesBackToItsBody() throws {
        let vectors = try GoldenVectors.load()
        for vector in vectors {
            var decoder = FrameDecoder()
            decoder.push(vector.framed)
            let body = try decoder.next()
            XCTAssertEqual(body, vector.body, vector.name)
            XCTAssertNil(try decoder.next(), "\(vector.name) should hold exactly one frame")
            XCTAssertEqual(decoder.buffered, 0, "\(vector.name) should leave nothing buffered")
        }
    }

    func testTheLengthPrefixIsFourBytesBigEndian() throws {
        let vectors = try GoldenVectors.load()
        for vector in vectors {
            let prefix = [UInt8](vector.framed.prefix(4))
            let stated =
                Int(prefix[0]) << 24 | Int(prefix[1]) << 16 | Int(prefix[2]) << 8 | Int(prefix[3])
            XCTAssertEqual(
                stated, vector.framed.count - 4,
                "\(vector.name): the prefix must state the body length")
            // Big-endian, not little: every one of these bodies is well under
            // 256 bytes, so the first three prefix bytes are zero and the last
            // carries the length. A little-endian encoder would put it first.
            XCTAssertEqual(prefix[0], 0, vector.name)
            XCTAssertEqual(prefix[1], 0, vector.name)
        }
    }

    func testSeveralFramesEncodeIntoOneContiguousBuffer() throws {
        let vectors = try GoldenVectors.load()
        let bodies = vectors.map(\.body)
        let batched = try FrameEncoder.encode(bodies: bodies)

        let expected = vectors.map(\.framed).reduce(into: Data()) { $0.append($1) }
        XCTAssertEqual(batched.hexEncoded, expected.hexEncoded)
    }

    func testAnOversizedBodyIsRefusedRatherThanTruncated() {
        let body = Data(repeating: 0x61, count: LolaFrameLimits.maxFrameBytes + 1)
        XCTAssertThrowsError(try FrameEncoder.encode(body: body)) { error in
            guard case FrameCodecError.bodyTooLarge(let size, let max) = error else {
                return XCTFail("expected bodyTooLarge, got \(error)")
            }
            XCTAssertEqual(size, LolaFrameLimits.maxFrameBytes + 1)
            XCTAssertEqual(max, LolaFrameLimits.maxFrameBytes)
        }
    }

    func testABodyExactlyAtTheLimitIsAccepted() throws {
        let body = Data(repeating: 0x61, count: LolaFrameLimits.maxFrameBytes)
        let framed = try FrameEncoder.encode(body: body)
        XCTAssertEqual(framed.count, LolaFrameLimits.maxFrameBytes + 4)
        // The cap is on the BODY, so the prefix pushes the framed size past it.
        // Getting this off by four bytes at either end is how two peers end up
        // disagreeing about a frame that only one of them will send.
        XCTAssertEqual([UInt8](framed.prefix(4)), [0x00, 0x10, 0x00, 0x00])
    }

    func testAnEmptyBodyIsRefused() {
        // A zero-length body would encode as a zero prefix, which the daemon
        // treats as fatal. Refusing it here means the caller learns about it
        // where there is still a caller to blame.
        XCTAssertThrowsError(try FrameEncoder.encode(body: Data())) { error in
            XCTAssertEqual(error as? FrameCodecError, .emptyFrame)
        }
    }
}
