import Foundation
import XCTest

/// The cross-language wire contract.
///
/// `mobile/src/wire/testdata/frames.json` holds twenty frames, each with its
/// decoded JSON value and the EXACT bytes that value has on the wire. Three
/// suites read that one file: `internal/protocol/goldenvectors_test.go` writes
/// each frame with `protocol.FrameWriter` and asserts the bytes,
/// `mobile/src/wire/codec.test.ts` does the same with the TypeScript encoder,
/// and this one does it with the Swift codec. The Go side is the source of
/// truth; when Swift and the vector disagree, Swift is wrong.
///
/// The file is read by RELATIVE PATH from this source file rather than copied
/// into the test bundle as a resource. Copying it would create a second copy of
/// the contract, which is the exact failure mode the shared file exists to
/// prevent — a stale copy passes its own tests forever. The cost is that these
/// tests only run from a checkout, which is the only place they ever run
/// anyway. `LOLA_FRAME_VECTORS` overrides the path for a build that relocates
/// sources.
struct GoldenVector {
    let name: String
    let why: String
    let frame: [String: Any]

    /// The complete wire bytes: four-byte big-endian prefix, then the body.
    let framed: Data

    /// The body alone, which is what the decoder must hand back.
    var body: Data { framed.dropFirst(4) }
}

enum GoldenVectors {
    static func load(file: StaticString = #filePath, line: UInt = #line) throws -> [GoldenVector] {
        let url = try vectorsURL(file: file, line: line)
        let data = try Data(contentsOf: url)
        guard
            let root = try JSONSerialization.jsonObject(with: data) as? [String: Any],
            let cases = root["cases"] as? [[String: Any]]
        else {
            XCTFail("frames.json does not have the expected shape", file: file, line: line)
            return []
        }

        return try cases.map { entry in
            guard
                let name = entry["name"] as? String,
                let hex = entry["hex"] as? String,
                let frame = entry["frame"] as? [String: Any]
            else {
                throw VectorError.malformedCase
            }
            guard let framed = Data(hexEncoded: hex) else {
                throw VectorError.badHex(name)
            }
            return GoldenVector(
                name: name,
                why: entry["why"] as? String ?? "",
                frame: frame,
                framed: framed)
        }
    }

    static func named(_ name: String, in vectors: [GoldenVector]) -> GoldenVector? {
        vectors.first { $0.name == name }
    }

    enum VectorError: Error {
        case malformedCase
        case badHex(String)
        case notFound(String)
    }

    private static func vectorsURL(file: StaticString, line: UInt) throws -> URL {
        if let override = ProcessInfo.processInfo.environment["LOLA_FRAME_VECTORS"] {
            return URL(fileURLWithPath: override)
        }
        // .../mobile/plugins/lola-transport/ios/Tests/LolaTransportCoreTests/GoldenVectors.swift
        var url = URL(fileURLWithPath: "\(file)")
        for _ in 0..<6 { url.deleteLastPathComponent() }  // -> .../mobile
        let vectors = url.appendingPathComponent("src/wire/testdata/frames.json")
        guard FileManager.default.fileExists(atPath: vectors.path) else {
            throw VectorError.notFound(vectors.path)
        }
        return vectors
    }
}

extension Data {
    init?(hexEncoded hex: String) {
        guard hex.count % 2 == 0 else { return nil }
        var out = Data(capacity: hex.count / 2)
        var index = hex.startIndex
        while index < hex.endIndex {
            let next = hex.index(index, offsetBy: 2)
            guard let byte = UInt8(hex[index..<next], radix: 16) else { return nil }
            out.append(byte)
            index = next
        }
        self = out
    }

    var hexEncoded: String {
        map { String(format: "%02x", $0) }.joined()
    }
}
