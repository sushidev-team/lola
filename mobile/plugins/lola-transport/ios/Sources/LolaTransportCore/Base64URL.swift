import Foundation

/// base64url without padding, the encoding of every secret on the pairing
/// wire. Decoding is strict (canonical form only), like Go's
/// `base64.RawURLEncoding.Strict()`, so two encodings of one value cannot exist.
public enum Base64URL {
    public static func encode(_ data: Data) -> String {
        data.base64EncodedString()
            .replacingOccurrences(of: "+", with: "-")
            .replacingOccurrences(of: "/", with: "_")
            .replacingOccurrences(of: "=", with: "")
    }

    public static func decode(_ text: String) -> Data? {
        guard !text.isEmpty, text.utf8.count % 4 != 1 else { return nil }
        for byte in text.utf8 {
            switch byte {
            case UInt8(ascii: "A")...UInt8(ascii: "Z"), UInt8(ascii: "a")...UInt8(ascii: "z"),
                UInt8(ascii: "0")...UInt8(ascii: "9"), UInt8(ascii: "-"), UInt8(ascii: "_"):
                continue
            default:
                return nil
            }
        }
        var std = text.replacingOccurrences(of: "-", with: "+").replacingOccurrences(of: "_", with: "/")
        while std.utf8.count % 4 != 0 { std += "=" }
        guard let data = Data(base64Encoded: std), encode(data) == text else { return nil }
        return data
    }
}
