import XCTest

@testable import LolaTransportCore

/// Reassembly, which is where naive implementations of this protocol break.
///
/// TCP delivers segments, not messages. Every one of these tests describes a
/// segmentation a real network produces and a decoder that assumed one read
/// equals one frame would fail: a length prefix split down the middle, a body
/// arriving in pieces, several frames in one read, and a frame straddling two.
final class FrameDecoderTests: XCTestCase {

    func testAFrameArrivingOneByteAtATimeIsReassembled() throws {
        let vectors = try GoldenVectors.load()
        for vector in vectors {
            var decoder = FrameDecoder()
            var delivered: [Data] = []

            for byte in vector.framed {
                decoder.push(Data([byte]))
                while let body = try decoder.next() { delivered.append(body) }
            }

            XCTAssertEqual(delivered.count, 1, vector.name)
            XCTAssertEqual(delivered.first, vector.body, vector.name)
        }
    }

    func testTheLengthPrefixMaySplitAcrossReads() throws {
        let vectors = try GoldenVectors.load()
        let vector = try XCTUnwrap(GoldenVectors.named("resync_cursor_visible", in: vectors))

        // Every split of the four-byte header, plus one inside the body.
        for cut in [1, 2, 3, 4, 5, 40] {
            var decoder = FrameDecoder()
            decoder.push(vector.framed.prefix(cut))
            XCTAssertNil(
                try decoder.next(),
                "a frame cut at \(cut) bytes must not decode before the rest arrives")

            decoder.push(vector.framed.dropFirst(cut))
            let body = try decoder.next()
            XCTAssertEqual(body, vector.body, "cut at \(cut)")
        }
    }

    func testSeveralFramesInOneReadAreDeliveredInOrder() throws {
        let vectors = try GoldenVectors.load()
        var stream = Data()
        for vector in vectors { stream.append(vector.framed) }

        var decoder = FrameDecoder()
        decoder.push(stream)

        var delivered: [Data] = []
        while let body = try decoder.next() { delivered.append(body) }

        XCTAssertEqual(delivered.count, vectors.count)
        for (index, vector) in vectors.enumerated() {
            XCTAssertEqual(delivered[index], vector.body, vector.name)
        }
        XCTAssertEqual(decoder.buffered, 0)
    }

    func testAFrameStraddlingTwoReadsIsReassembled() throws {
        let vectors = try GoldenVectors.load()
        var stream = Data()
        for vector in vectors { stream.append(vector.framed) }

        // Cut in a place guaranteed to fall inside a frame rather than on a
        // boundary: the middle of the whole stream.
        let cut = stream.count / 2
        var decoder = FrameDecoder()
        var delivered: [Data] = []

        decoder.push(stream.prefix(cut))
        while let body = try decoder.next() { delivered.append(body) }
        XCTAssertGreaterThan(delivered.count, 0, "the complete frames before the cut should arrive")
        XCTAssertLessThan(delivered.count, vectors.count, "the cut should leave a partial frame")

        decoder.push(stream.dropFirst(cut))
        while let body = try decoder.next() { delivered.append(body) }

        XCTAssertEqual(delivered.count, vectors.count)
        for (index, vector) in vectors.enumerated() {
            XCTAssertEqual(delivered[index], vector.body, vector.name)
        }
    }

    func testAnOversizedLengthPrefixIsRefusedFromTheHeaderAlone() throws {
        // 1 MiB + 1, announced but never sent. The refusal must happen on the
        // four header bytes: a decoder that waited for the body would sit
        // buffering toward a promise the peer never has to keep, which is the
        // whole reason this protocol carries a prefix instead of a delimiter.
        let announced = LolaFrameLimits.maxFrameBytes + 1
        var header = Data()
        header.append(UInt8(truncatingIfNeeded: announced >> 24))
        header.append(UInt8(truncatingIfNeeded: announced >> 16))
        header.append(UInt8(truncatingIfNeeded: announced >> 8))
        header.append(UInt8(truncatingIfNeeded: announced))

        var decoder = FrameDecoder()
        decoder.push(header)
        XCTAssertThrowsError(try decoder.next()) { error in
            guard case FrameCodecError.frameTooLarge(let size, let max) = error else {
                return XCTFail("expected frameTooLarge, got \(error)")
            }
            XCTAssertEqual(size, announced)
            XCTAssertEqual(max, LolaFrameLimits.maxFrameBytes)
        }
        XCTAssertEqual(decoder.buffered, 4, "not one byte of the announced body was waited for")
    }

    func testAZeroLengthPrefixIsFatal() {
        var decoder = FrameDecoder()
        decoder.push(Data([0x00, 0x00, 0x00, 0x00]))
        XCTAssertThrowsError(try decoder.next()) { error in
            XCTAssertEqual(error as? FrameCodecError, .emptyFrame)
        }
    }

    func testAFatalErrorPoisonsTheDecoderUntilItIsReset() throws {
        var decoder = FrameDecoder()
        decoder.push(Data([0x00, 0x00, 0x00, 0x00]))
        XCTAssertThrowsError(try decoder.next())
        XCTAssertTrue(decoder.poisoned)

        // A stream whose length prefix could not be honoured has no recoverable
        // frame boundary: the bytes that would tell you where the next frame
        // starts are the ones the decoder just refused to read. So it stays
        // refused rather than guessing.
        let vectors = try GoldenVectors.load()
        decoder.push(vectors[0].framed)
        XCTAssertThrowsError(try decoder.next()) { error in
            XCTAssertEqual(error as? FrameCodecError, .decoderPoisoned)
        }

        decoder.reset()
        XCTAssertFalse(decoder.poisoned)
        decoder.push(vectors[0].framed)
        XCTAssertEqual(try decoder.next(), vectors[0].body)
    }

    func testDrainDeliversTheFramesItDecodedBeforeAFatalError() throws {
        // The daemon answers a bad prefix with a best-effort refusal frame and
        // then closes, so the frame immediately before a violation is routinely
        // the one that explains it. Discarding it would throw away the message.
        let vectors = try GoldenVectors.load()
        var stream = vectors[0].framed
        stream.append(Data([0x00, 0x00, 0x00, 0x00]))

        var decoder = FrameDecoder()
        decoder.push(stream)

        var delivered: [Data] = []
        XCTAssertThrowsError(try decoder.drain { delivered.append($0) })
        XCTAssertEqual(delivered, [vectors[0].body])
    }

    func testARandomlySegmentedStreamReassemblesExactly() throws {
        let vectors = try GoldenVectors.load()
        var stream = Data()
        for vector in vectors { stream.append(vector.framed) }

        // A fixed seed rather than a random one: a reassembly bug that only
        // reproduces on some runs is worse than no test.
        var seed: UInt64 = 0x5DEE_CE66_D123_4567
        func nextChunk() -> Int {
            seed = seed &* 6_364_136_223_846_793_005 &+ 1_442_695_040_888_963_407
            return Int((seed >> 33) % 37) + 1
        }

        var decoder = FrameDecoder()
        var delivered: [Data] = []
        var offset = 0
        while offset < stream.count {
            let size = min(nextChunk(), stream.count - offset)
            decoder.push(stream.subdata(in: offset..<(offset + size)))
            offset += size
            while let body = try decoder.next() { delivered.append(body) }
        }

        XCTAssertEqual(delivered.count, vectors.count)
        for (index, vector) in vectors.enumerated() {
            XCTAssertEqual(delivered[index], vector.body, vector.name)
        }
    }

    func testATruncatedTailStaysBufferedRatherThanFailing() throws {
        let vectors = try GoldenVectors.load()
        let vector = vectors[0]
        var decoder = FrameDecoder()
        decoder.push(vector.framed.dropLast(3))

        XCTAssertNil(try decoder.next())
        XCTAssertEqual(decoder.buffered, vector.framed.count - 3)
        XCTAssertFalse(decoder.poisoned, "a short read is not an error")
    }
}
