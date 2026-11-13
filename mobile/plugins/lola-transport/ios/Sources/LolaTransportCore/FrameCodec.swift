import Foundation

/// The wire framing, and nothing else.
///
/// Every frame on this transport is a four-byte big-endian `uint32` giving the
/// byte count of a UTF-8 JSON body, followed by exactly that many bytes. There
/// is no magic number, no type byte and no trailing newline; the frame's kind
/// lives inside the JSON. This mirrors `internal/protocol/framecodec.go` byte
/// for byte, and the golden vectors in `mobile/src/wire/testdata/frames.json`
/// are what hold the two honest.
///
/// The single most important property here is that an unacceptable length is
/// refused from the HEADER, before a byte of the body is waited for. A decoder
/// that accumulates first and checks afterwards will happily buffer toward a
/// four-gigabyte promise made by a peer, which is the whole reason the protocol
/// carries a prefix instead of a delimiter.
public enum LolaFrameLimits {
    /// Mirrors `protocol.FrameHeaderBytes`.
    public static let headerBytes = 4

    /// Mirrors `protocol.MaxFrameBytes` (1 MiB). It bounds the BODY, not the
    /// framed size, in both directions.
    public static let maxFrameBytes = 1 << 20
}

public enum FrameCodecError: Error, Equatable {
    /// A zero length prefix. There is no empty frame in this protocol — an
    /// envelope always carries at least `v` and `type` — so a zero is a bug or
    /// a probe. Fatal: nothing after it can be trusted to be a frame boundary.
    case emptyFrame

    /// A length prefix over `maxFrameBytes`. Also fatal, and for a sharper
    /// reason: skipping the frame would mean reading the very bytes the
    /// decoder just refused to read, so the stream cannot be resynchronised.
    case frameTooLarge(size: Int, max: Int)

    /// An outbound body over `maxFrameBytes`. The caller's bug; nothing is
    /// written, because a truncated frame is worse than no frame.
    case bodyTooLarge(size: Int, max: Int)

    /// The decoder already failed fatally and was not reset.
    case decoderPoisoned

    /// A body that is not valid UTF-8. The daemon writes JSON produced by
    /// `encoding/json`, which is always valid UTF-8, so this is a corrupt
    /// stream rather than a protocol variation.
    case invalidUTF8
}

/// Encodes frame bodies onto the wire.
public enum FrameEncoder {
    /// Prefixes one body. Throws `bodyTooLarge` rather than truncating.
    public static func encode(body: Data) throws -> Data {
        try encode(bodies: [body])
    }

    /// Prefixes several bodies into one contiguous buffer.
    ///
    /// The daemon writes one frame per socket write so a four-byte header never
    /// lands in its own TCP segment next to Nagle. Concatenating several
    /// COMPLETE frames into one write preserves that property and improves on
    /// it: the reader still sees well-formed frames back to back, and the
    /// keystroke path pays one syscall instead of several.
    public static func encode(bodies: [Data]) throws -> Data {
        var total = 0
        for body in bodies {
            guard body.count <= LolaFrameLimits.maxFrameBytes else {
                throw FrameCodecError.bodyTooLarge(size: body.count, max: LolaFrameLimits.maxFrameBytes)
            }
            guard !body.isEmpty else {
                // A zero-length body would encode as a zero prefix, which the
                // peer treats as fatal. Refuse it here, where there is still a
                // caller to blame.
                throw FrameCodecError.emptyFrame
            }
            total += LolaFrameLimits.headerBytes + body.count
        }

        var out = Data(capacity: total)
        for body in bodies {
            let n = UInt32(body.count)
            out.append(UInt8(truncatingIfNeeded: n >> 24))
            out.append(UInt8(truncatingIfNeeded: n >> 16))
            out.append(UInt8(truncatingIfNeeded: n >> 8))
            out.append(UInt8(truncatingIfNeeded: n))
            out.append(body)
        }
        return out
    }

    /// Convenience for a body that is already a `String` of JSON.
    public static func encode(json: String) throws -> Data {
        guard let body = json.data(using: .utf8) else { throw FrameCodecError.invalidUTF8 }
        return try encode(body: body)
    }

    /// Convenience for several JSON strings.
    public static func encode(jsonBodies: [String]) throws -> Data {
        var bodies: [Data] = []
        bodies.reserveCapacity(jsonBodies.count)
        for s in jsonBodies {
            guard let d = s.data(using: .utf8) else { throw FrameCodecError.invalidUTF8 }
            bodies.append(d)
        }
        return try encode(bodies: bodies)
    }
}

/// Reassembles frames from a byte stream.
///
/// This is the half naive implementations get wrong. TCP delivers segments, not
/// messages: a single `receive` may hand over half a length prefix, two and a
/// half frames, or one byte. So the decoder buffers, and `next()` returns `nil`
/// to mean "not enough yet" rather than treating a short read as an error.
///
/// It is a value type with `mutating` methods and is NOT thread-safe. One
/// decoder per connection, touched only from that connection's serial queue.
public struct FrameDecoder {
    private var buffer: [UInt8] = []
    private var offset: Int = 0
    private var failed = false

    /// Bytes held but not yet delivered as a frame.
    public var buffered: Int { buffer.count - offset }

    /// True once a fatal framing error has been thrown. Every later `next()`
    /// throws `decoderPoisoned` until `reset()` is called, because a stream
    /// whose length prefix could not be honoured has no recoverable boundary.
    public var poisoned: Bool { failed }

    /// Compaction is amortised: bytes are dropped from the front only once
    /// enough of them have accumulated to be worth a copy, so a stream of small
    /// frames does not turn into a quadratic memmove.
    private static let compactThreshold = 64 * 1024

    public init() {}

    /// Appends bytes read from the socket.
    public mutating func push(_ chunk: Data) {
        guard !chunk.isEmpty else { return }
        buffer.append(contentsOf: chunk)
    }

    public mutating func push(_ chunk: [UInt8]) {
        guard !chunk.isEmpty else { return }
        buffer.append(contentsOf: chunk)
    }

    /// Returns the next complete frame body, or `nil` when more bytes are
    /// needed. Throws only on a FATAL framing violation, which the caller must
    /// answer by closing the connection.
    public mutating func next() throws -> Data? {
        if failed { throw FrameCodecError.decoderPoisoned }

        let available = buffer.count - offset
        if available < LolaFrameLimits.headerBytes {
            compact()
            return nil
        }

        let n =
            Int(buffer[offset]) << 24 | Int(buffer[offset + 1]) << 16 | Int(buffer[offset + 2]) << 8
            | Int(buffer[offset + 3])

        // Both checks happen on the HEADER alone. Neither waits for the body,
        // which is exactly the property a delimiter-based reader cannot have.
        if n == 0 {
            failed = true
            throw FrameCodecError.emptyFrame
        }
        if n > LolaFrameLimits.maxFrameBytes {
            failed = true
            throw FrameCodecError.frameTooLarge(size: n, max: LolaFrameLimits.maxFrameBytes)
        }

        if available - LolaFrameLimits.headerBytes < n {
            compact()
            return nil
        }

        let start = offset + LolaFrameLimits.headerBytes
        let body = Data(buffer[start..<(start + n)])
        offset = start + n
        compact()
        return body
    }

    /// Drains every complete frame currently buffered.
    ///
    /// On a fatal error the frames decoded BEFORE it are still handed to
    /// `sink`, then the error is thrown. The daemon does the same thing in the
    /// other direction — it answers a bad prefix with a best-effort refusal
    /// frame and then closes — so a client that discarded the good frames it
    /// already had would be throwing away the very message that explains the
    /// close.
    public mutating func drain(into sink: (Data) -> Void) throws {
        while let body = try next() {
            sink(body)
        }
    }

    /// Clears everything, including the poisoned flag. Only meaningful when the
    /// underlying socket has been replaced.
    public mutating func reset() {
        buffer.removeAll(keepingCapacity: false)
        offset = 0
        failed = false
    }

    private mutating func compact() {
        if offset == buffer.count {
            buffer.removeAll(keepingCapacity: true)
            offset = 0
            return
        }
        if offset >= Self.compactThreshold {
            buffer.removeFirst(offset)
            offset = 0
        }
    }
}
