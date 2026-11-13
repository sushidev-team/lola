import Foundation

/// M1's in-band bearer handshake.
///
/// The client sends exactly ONE frame first, and it must be a `req` naming
/// `remote.hello`, whose payload is NOT a `protocol.Request` but a bare
/// `{"key": ...}`. The daemon reads that one frame, compares the key in
/// constant time, and answers with an ordinary `resp` carrying `{"ok": true}`
/// on the same correlation id. Any failure at all, whatever its cause, is the
/// identical `err` frame with code `denied` and message "authenticate first",
/// followed by a close. See `internal/remote/insecure.go`.
///
/// Two consequences shape this file. The reply is an ORDINARY response, so
/// nothing downstream needs a special frame type for a path that M2 deletes.
/// And a refusal says nothing about which part was wrong, so the client must
/// not try to explain more than "the daemon refused the key" — inventing a
/// finer diagnosis here would be a guess dressed as a fact.
///
/// This whole file goes away with the daemon's `lola_insecure` build tag. It is
/// kept separate from the connection for exactly that reason: deleting it in M2
/// should be a file removal and a branch removal, not an archaeology exercise.
public enum HelloHandshake {
    /// Mirrors `remote.helloCmd`.
    public static let command = "remote.hello"

    /// Mirrors `remote.insecureMinKeyLen`. The daemon's listener refuses to
    /// start below this, so a shorter key can only ever fail; saying so before
    /// a socket is opened is a better error than "denied".
    public static let minimumKeyLength = 16

    /// The correlation id the plugin uses for its own handshake.
    ///
    /// It is deliberately not of the shape the JavaScript correlator generates
    /// (`r1`, `s1`, ...). The plugin consumes the reply and never forwards it,
    /// so a collision could not actually mis-deliver anything, but a distinct
    /// id makes a packet capture unambiguous about which side sent what.
    public static let correlationID = "lola-hello"

    public enum Outcome: Equatable {
        /// The daemon accepted the key. Frames may now flow.
        case authenticated

        /// The daemon refused. `code` is `ErrPayload.Code`; `message` is its
        /// short human line. `minV`/`maxV` are present only on
        /// `unsupported_version`, and they are what lets the app say which side
        /// is behind rather than showing a connect error.
        case refused(code: String, message: String?, minV: Int?, maxV: Int?)

        /// A frame that is neither. The daemon writes exactly one reply before
        /// anything else can happen on the connection, so this is a protocol
        /// violation rather than an out-of-order arrival.
        case unexpected(String)
    }

    /// Builds the handshake frame's JSON body.
    ///
    /// Written by hand rather than encoded from a struct so that the field
    /// order matches `protocol.Frame`'s declaration order — `v`, `type`, `id`,
    /// `cmd`, `payload` — which is what the golden vectors record.
    public static func body(id: String = correlationID, key: String) -> Data {
        let json =
            "{\"v\":1,\"type\":\"req\",\"id\":" + JSONText.quote(id)
            + ",\"cmd\":" + JSONText.quote(command)
            + ",\"payload\":{\"key\":" + JSONText.quote(key) + "}}"
        // The string is built from UTF-8-representable pieces, so this cannot
        // fail; the fallback keeps the signature non-throwing at the call site,
        // where a throw would only ever be dead code.
        return json.data(using: .utf8) ?? Data()
    }

    /// Classifies the daemon's reply.
    ///
    /// `expectedID` is checked because the daemon echoes the hello's id, and a
    /// reply on a different id would mean the connection is not in the state
    /// this code believes it is in.
    public static func classify(reply body: Data, expectedID: String = correlationID) -> Outcome {
        guard
            let any = try? JSONSerialization.jsonObject(with: body),
            let frame = any as? [String: Any]
        else {
            return .unexpected("the daemon's handshake reply was not a JSON object")
        }

        let type = frame["type"] as? String ?? ""
        let id = frame["id"] as? String ?? ""
        let payload = frame["payload"] as? [String: Any]

        if !id.isEmpty && id != expectedID {
            return .unexpected("the daemon answered the handshake on an unexpected correlation id")
        }

        switch type {
        case "resp":
            // `Response.OK` carries no omitempty, so `ok` is always present. A
            // missing one is a frame this code does not understand, and an
            // `ok:false` is a refusal like any other.
            if let ok = payload?["ok"] as? Bool, ok {
                return .authenticated
            }
            let message = payload?["error"] as? String
            return .refused(code: "denied", message: message, minV: nil, maxV: nil)

        case "err":
            let code = payload?["code"] as? String ?? "denied"
            let message = payload?["message"] as? String
            let minV = payload?["minV"] as? Int
            let maxV = payload?["maxV"] as? Int
            return .refused(code: code, message: message, minV: minV, maxV: maxV)

        default:
            return .unexpected("the daemon answered the handshake with a \"" + type + "\" frame")
        }
    }
}
