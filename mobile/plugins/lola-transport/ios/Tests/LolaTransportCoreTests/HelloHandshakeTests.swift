import XCTest

@testable import LolaTransportCore

/// M1's bearer handshake, on both sides of the exchange.
final class HelloHandshakeTests: XCTestCase {

    func testTheHelloFrameMatchesTheGoldenVectorByteForByte() throws {
        let vectors = try GoldenVectors.load()
        let vector = try XCTUnwrap(GoldenVectors.named("req_hello_bearer", in: vectors))

        // The id and the key are read out of the vector rather than repeated
        // here, so that a change to the vector cannot be satisfied by an
        // equal-and-opposite change to this test.
        let id = try XCTUnwrap(vector.frame["id"] as? String)
        let payload = try XCTUnwrap(vector.frame["payload"] as? [String: Any])
        let key = try XCTUnwrap(payload["key"] as? String)

        let body = HelloHandshake.body(id: id, key: key)
        XCTAssertEqual(
            body.hexEncoded, vector.body.hexEncoded,
            "the handshake frame must be byte-identical to what the daemon's own encoder produces")
    }

    func testTheHelloFrameCarriesFieldsInProtocolFrameDeclarationOrder() {
        let body = HelloHandshake.body(id: "x", key: "0123456789abcdef")
        let text = String(data: body, encoding: .utf8) ?? ""
        // `encoding/json` emits struct fields in declaration order, and the
        // golden vectors record that order. Reproducing it is what makes a
        // byte comparison against Go possible at all.
        XCTAssertTrue(text.hasPrefix("{\"v\":1,\"type\":\"req\",\"id\":"))
        XCTAssertTrue(text.contains("\"cmd\":\"remote.hello\""))
        XCTAssertTrue(text.hasSuffix("}}"))
    }

    func testTheKeyIsEscapedTheWayGoEscapesIt() {
        // A key is whatever the human typed. Anything that needs escaping must
        // survive the trip, and it must be escaped the way the rest of this
        // protocol's encoders escape it, or a byte comparison against Go
        // becomes meaningless the moment somebody uses an ampersand.
        let body = HelloHandshake.body(id: "x", key: "a\"b\\c<d>e&f\ng")
        let text = String(data: body, encoding: .utf8) ?? ""
        XCTAssertTrue(text.contains("\"key\":\"a\\\"b\\\\c\\u003cd\\u003ee\\u0026f\\ng\""), text)

        // And it must still parse as the key that went in.
        let parsed =
            (try? JSONSerialization.jsonObject(with: body)) as? [String: Any]
        let payload = parsed?["payload"] as? [String: Any]
        XCTAssertEqual(payload?["key"] as? String, "a\"b\\c<d>e&f\ng")
    }

    func testAnAcceptedHandshakeIsAnOrdinaryResponse() {
        // The daemon acknowledges with a plain `resp` on the hello's id, which
        // is why nothing downstream needed a frame type invented for a path
        // that M2 deletes.
        let reply = Data(
            #"{"v":1,"type":"resp","id":"lola-hello","payload":{"ok":true}}"#.utf8)
        XCTAssertEqual(HelloHandshake.classify(reply: reply), .authenticated)
    }

    func testEveryRefusalLooksTheSame() {
        // `internal/remote/insecure.go` answers a wrong type, a wrong command,
        // an undecodable payload and a wrong key with the identical frame. The
        // client must not invent a finer diagnosis than the daemon gave.
        let reply = Data(
            #"{"v":1,"type":"err","id":"lola-hello","payload":{"code":"denied","message":"authenticate first"}}"#
                .utf8)
        XCTAssertEqual(
            HelloHandshake.classify(reply: reply),
            .refused(code: "denied", message: "authenticate first", minV: nil, maxV: nil))
    }

    func testAVersionRefusalCarriesBothBounds() {
        // The one refusal that says something actionable: the bounds are what
        // let the app name which side is behind instead of showing a connect
        // error.
        let reply = Data(
            #"{"v":1,"type":"err","id":"lola-hello","payload":{"code":"unsupported_version","message":"daemon speaks envelope v1","minV":1,"maxV":1}}"#
                .utf8)
        XCTAssertEqual(
            HelloHandshake.classify(reply: reply),
            .refused(
                code: "unsupported_version", message: "daemon speaks envelope v1", minV: 1,
                maxV: 1))
    }

    func testAnOkFalseResponseIsARefusal() {
        // `Response.OK` carries no omitempty, so `ok` is always on the wire. A
        // false one is an application failure arriving as an ordinary resp, and
        // it is still a refusal of the handshake.
        let reply = Data(
            #"{"v":1,"type":"resp","id":"lola-hello","payload":{"ok":false,"error":"nope"}}"#.utf8)
        XCTAssertEqual(
            HelloHandshake.classify(reply: reply),
            .refused(code: "denied", message: "nope", minV: nil, maxV: nil))
    }

    func testAReplyOnTheWrongCorrelationIdIsNotAccepted() {
        // The daemon echoes the hello's id. A different one means the
        // connection is not in the state this code believes it is in, which is
        // the moment to stop rather than to assume.
        let reply = Data(#"{"v":1,"type":"resp","id":"r7","payload":{"ok":true}}"#.utf8)
        guard case .unexpected = HelloHandshake.classify(reply: reply) else {
            return XCTFail("a reply on a foreign id must not authenticate the connection")
        }
    }

    func testANonReplyFrameIsUnexpected() {
        let pty = Data(#"{"v":1,"type":"pty","pane":"lola-fe-42","payload":{"data":""}}"#.utf8)
        guard case .unexpected = HelloHandshake.classify(reply: pty) else {
            return XCTFail("a pty frame is not a handshake reply")
        }
        guard case .unexpected = HelloHandshake.classify(reply: Data("not json".utf8)) else {
            return XCTFail("garbage is not a handshake reply")
        }
    }

    func testTheMinimumKeyLengthMirrorsTheDaemon() {
        // `remote.insecureMinKeyLen`. The daemon's listener refuses to start
        // below it, so a shorter key can only ever be denied.
        XCTAssertEqual(HelloHandshake.minimumKeyLength, 16)
        XCTAssertEqual(HelloHandshake.command, "remote.hello")
    }
}
