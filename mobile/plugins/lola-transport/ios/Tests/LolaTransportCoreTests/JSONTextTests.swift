import XCTest

@testable import LolaTransportCore

/// The JSON string escaper, pinned against Go's rules.
///
/// The expectations here were taken from `encoding/json`'s behaviour, and the
/// same rules are implemented in `mobile/src/wire/codec.ts` as `goJSONString`.
/// Three of them are genuinely surprising and each one silently breaks a byte
/// comparison against the daemon: the HTML escaping, the missing short forms
/// for backspace and form feed, and the two line separators.
final class JSONTextTests: XCTestCase {

    func testOrdinaryTextIsUntouched() {
        XCTAssertEqual(JSONText.quote("hello"), "\"hello\"")
        XCTAssertEqual(JSONText.quote(""), "\"\"")
    }

    func testShortEscapes() {
        XCTAssertEqual(JSONText.quote("a\"b"), "\"a\\\"b\"")
        XCTAssertEqual(JSONText.quote("a\\b"), "\"a\\\\b\"")
        XCTAssertEqual(JSONText.quote("a\nb"), "\"a\\nb\"")
        XCTAssertEqual(JSONText.quote("a\rb"), "\"a\\rb\"")
        XCTAssertEqual(JSONText.quote("a\tb"), "\"a\\tb\"")
    }

    func testGoEscapesHTMLAndMostEncodersDoNot() {
        // `encoding/json` escapes these by default, and does it twice on this
        // protocol: once when a payload is marshalled and again when the
        // envelope compacts the raw payload into itself. An agent echoing
        // something like grep 'a && b' produces all three.
        XCTAssertEqual(JSONText.quote("<"), "\"\\u003c\"")
        XCTAssertEqual(JSONText.quote(">"), "\"\\u003e\"")
        XCTAssertEqual(JSONText.quote("&"), "\"\\u0026\"")
    }

    func testBackspaceAndFormFeedUseTheLongForm() {
        // JSON has short escapes for both and every other encoder emits them.
        // Go does not, so neither does this.
        XCTAssertEqual(JSONText.quote("\u{08}"), "\"\\u0008\"")
        XCTAssertEqual(JSONText.quote("\u{0C}"), "\"\\u000c\"")
        XCTAssertEqual(JSONText.quote("\u{00}"), "\"\\u0000\"")
        XCTAssertEqual(JSONText.quote("\u{1F}"), "\"\\u001f\"")
    }

    func testEscapeIsTheOneControlCharacterThatMatters() {
        // Terminal output is full of it, and it is what a resync frame's lines
        // are made of.
        XCTAssertEqual(JSONText.quote("\u{1B}[1mready"), "\"\\u001b[1mready\"")
    }

    func testLineSeparatorsAreEscaped() {
        XCTAssertEqual(JSONText.quote("\u{2028}"), "\"\\u2028\"")
        XCTAssertEqual(JSONText.quote("\u{2029}"), "\"\\u2029\"")
    }

    func testHexIsLowercase() {
        // Go writes lowercase hex in the six-character form. Uppercase parses
        // identically and compares differently, which is exactly the kind of
        // difference that only shows up in a golden-vector diff.
        XCTAssertFalse(JSONText.quote("\u{1B}").contains("B"))
        XCTAssertTrue(JSONText.quote("\u{1B}").contains("1b"))
    }

    func testNonASCIIPassesThroughAsUTF8() {
        // Go does not escape non-ASCII, and neither does this: the body is
        // UTF-8 on the wire either way, and escaping would change the bytes.
        XCTAssertEqual(JSONText.quote("héllo"), "\"héllo\"")
        XCTAssertEqual(JSONText.quote("日本"), "\"日本\"")
        XCTAssertEqual(JSONText.quote("🐟"), "\"🐟\"")
    }

    func testEverythingItProducesStillParses() throws {
        let nasty = "a\"b\\c<d>e&f\ng\th\u{1B}i\u{00}j\u{2028}k🐟l"
        let quoted = JSONText.quote(nasty)
        let document = Data(("{\"k\":" + quoted + "}").utf8)
        let parsed = try JSONSerialization.jsonObject(with: document) as? [String: Any]
        XCTAssertEqual(parsed?["k"] as? String, nasty)
    }
}
