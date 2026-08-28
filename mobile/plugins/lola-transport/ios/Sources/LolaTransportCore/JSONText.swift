import Foundation

/// A JSON string writer that reproduces Go's `encoding/json` byte for byte.
///
/// The plugin serializes exactly one thing, the bearer handshake frame, and
/// could have used `JSONEncoder` for it. It does not, for two reasons.
/// `JSONEncoder` gives no guarantee about key order, so the handshake frame
/// could not be checked against the golden wire vectors that keep every other
/// implementation of this protocol honest. And Go escapes several characters
/// that `JSONEncoder` leaves alone, so a key containing an ampersand would
/// produce bytes no other encoder in this repository produces: harmless on the
/// wire, since the daemon parses JSON either way, but it would make the one
/// cross-language check available here useless.
///
/// The rules, taken from `encoding/json`'s `encodeState.string`:
///
///   - the quote and the backslash take their short escapes;
///   - newline, carriage return and tab take theirs, but backspace and form
///     feed do NOT: Go sends those down the generic path as the six-character
///     forms u0008 and u000c;
///   - every other byte below 0x20 takes the same six-character form;
///   - the three HTML characters less-than, greater-than and ampersand become
///     u003c, u003e and u0026, because Go escapes HTML by default;
///   - U+2028 and U+2029 become u2028 and u2029.
public enum JSONText {
    /// Renders `s` as a quoted JSON string literal, quotes included.
    public static func quote(_ s: String) -> String {
        var out = "\""
        out.reserveCapacity(s.utf8.count + 2)
        for scalar in s.unicodeScalars {
            switch scalar {
            case "\"":
                out += "\\\""
            case "\\":
                out += "\\\\"
            case "\n":
                out += "\\n"
            case "\r":
                out += "\\r"
            case "\t":
                out += "\\t"
            case "<":
                out += "\\u003c"
            case ">":
                out += "\\u003e"
            case "&":
                out += "\\u0026"
            case "\u{2028}":
                out += "\\u2028"
            case "\u{2029}":
                out += "\\u2029"
            default:
                if scalar.value < 0x20 {
                    out += String(format: "\\u%04x", scalar.value)
                } else {
                    out.unicodeScalars.append(scalar)
                }
            }
        }
        out += "\""
        return out
    }
}
