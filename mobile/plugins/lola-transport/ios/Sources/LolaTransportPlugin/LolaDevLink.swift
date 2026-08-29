import Foundation

// The development URL hand-off, and the two things it is not.
//
// IT IS NOT THE PAIRING MECHANISM. `mobile/PLAN.md`, under "Pairing", settles
// that the enrolment payload is `lola1.<base64url ...>` — an opaque token,
// deliberately not a URI — and gives the reason: a custom URL scheme cannot be
// claimed exclusively on either platform, resolution is roughly
// first-installed-wins, and people routinely scan QR codes with the SYSTEM
// camera rather than an in-app scanner, at which point the OS hands the secret
// to whichever app registered the scheme. That argument holds here with more
// force rather than less, because M1's bearer key is a longer-lived credential
// than M2's `qr_secret`: it has no 90-second window, it is not single-use, and
// it is not zeroed after enrolment. So the QR the desktop draws stays an opaque
// token that only Lola's own scanner reads, and nothing in this file is ever
// what a human scans.
//
// IT IS NOT AVAILABLE IN A SHIPPED BUILD. What this is, is the answer to a
// narrow problem: the iOS Simulator has no camera and cannot be given frames,
// so an agent or a CI job that has to prove the connect path works has no way
// to hand the app an endpoint. The URL is that way, and it is fenced on three
// sides:
//
//   1. The scheme is `lola-dev`, not `lola`. It is not the app's own scheme,
//      it names itself as development-only wherever it appears, and it does not
//      squat the name a real pairing URL would want if one is ever built on a
//      Universal Link.
//   2. Everything that turns a URL into a connection is compiled out unless
//      `LOLA_DEV_LINK` is defined, which `Package.swift` does only for the
//      debug configuration. A release build contains no parser and no observer.
//      (The CocoaPods fallback in `LolaTransport.podspec` defines nothing, so
//      the path is absent there too; CocoaPods is not the primary integration
//      and does not need a debug story.)
//
//      `LOLA_DEV_LINK` IS THE ONLY CONDITION, and it used to not be. The gate
//      read `#if LOLA_DEV_LINK || DEBUG`, which was described in three places
//      as a free second chance at the same answer — but CocoaPods sets
//      `SWIFT_ACTIVE_COMPILATION_CONDITIONS = DEBUG` for its Debug
//      configuration by default, so the podspec's claim that the affordance is
//      "simply absent under this path" was false the day it was written. A
//      fence whose documentation has already drifted from its implementation is
//      how the next reader concludes the wrong thing about a release build, so
//      the second condition is gone rather than the paragraph explaining it.
//   3. The delivered event is stamped `source: "dev-url"`, and the app is
//      expected to keep a persistent banner on screen for as long as a
//      connection that arrived this way is up. A hidden back door is a problem;
//      a labelled test fixture is a tool.
//
// The daemon side is fenced independently and was never weakened for this: the
// bearer key only exists in a daemon built with `-tags lola_insecure`, and such
// a daemon forces its listener onto loopback. A URL cannot conjure an endpoint
// that would otherwise refuse the connection.

// Everything below is inside the gate, down to the string constants. A release
// build should not carry so much as the NAME of the environment variable it
// does not read: the first thing anyone auditing a shipped binary does is run
// `strings` over it, and a hit for LOLA_DEV_LINK there costs them an hour of
// proving that the code behind it is absent. The `#else` half of
// LolaTransportPlugin+DevLink.swift supplies the no-op the plugin calls.

#if LOLA_DEV_LINK

public enum LolaDevLink {
    /// Registered in `mobile/ios/App/App/Info.plist` under `CFBundleURLTypes`.
    /// Registration alone grants nothing: without the handler below, an
    /// arriving URL is dropped.
    public static let scheme = "lola-dev"

    /// The action this build understands. `lola-dev://connect?...`.
    public static let connectAction = "connect"

    /// The launch environment variable carrying the same URL.
    ///
    /// It exists because the URL alone is not scriptable on a current iOS. From
    /// iOS 26 the system interposes an "Open in Lola?" confirmation on any
    /// custom-scheme open whose source it does not recognise, and `simctl
    /// openurl` is exactly such a source. A human taps through it in a second;
    /// an agent or a CI job cannot tap at all, and `simctl` has no gesture API.
    /// So the URL keeps working for a person, and this carries the identical
    /// string for a machine:
    ///
    ///   SIMCTL_CHILD_LOLA_DEV_LINK="lola-dev://connect?..." \
    ///     xcrun simctl launch <udid> dev.sushi.lola.mobile
    ///
    /// This is not a wider door than the URL. It is the same parser, the same
    /// gate and the same stamped event, and it is if anything narrower: a
    /// launch environment can only be set by whoever starts the process, which
    /// on a device means a debugger.
    public static let environmentVariable = "LOLA_DEV_LINK"

    /// The argv form of the same thing, for a launcher that passes arguments
    /// rather than an environment. Two dashes deliberately: a single-dash
    /// argument is swallowed by `UserDefaults`' own argument parsing and would
    /// silently become a preference.
    public static let argumentFlag = "--lola-dev-link"

    /// What a URL is allowed to say. Exactly the fields `connect` already takes
    /// from the form a human fills in, and nothing else — a development
    /// affordance that could reach settings the UI cannot would be a second,
    /// unreviewed way to configure the app.
    /// HOW a link arrived, which is the only thing that decides whether the app
    /// may act on it without being asked.
    ///
    /// The two are not the same door and must never be reported as one. A `url`
    /// is whatever some app asked iOS to open: anybody on the device can send
    /// one, which is precisely PLAN.md's objection to URL-routed pairing, so the
    /// app fills its form and waits for a human. A `launch` link came from this
    /// process's own environment or argv — and setting those requires being the
    /// thing that STARTED the process, which on a device means a debugger and in
    /// CI means already owning the machine. An attacker who can do that does not
    /// need this feature.
    ///
    /// That distinction is what makes the scriptable path actually scriptable.
    /// Collapsing them left an agent able to fill the form and unable to submit
    /// it, since `simctl` has no way to tap Connect.
    public enum Arrival: String, Sendable {
        /// The OS URL router, on behalf of some app. Fill only.
        case url = "dev-url"
        /// This process's own launch environment or argv. May connect.
        case launch = "dev-launch"
    }

    /// What a URL is allowed to say, plus how it got here.
    public struct Payload: Equatable, Sendable {
        public let host: String
        public let port: Int?
        public let spkiPin: String?
        public let insecureKey: String?
        /// The tmux pane to open once connected, or nil to land on the list.
        ///
        /// It exists because the terminal is the screen this whole project is a
        /// bet on and it was the one screen nobody could look at: it is reached
        /// only by tapping a row, `simctl` has no gesture API, and the
        /// Simulator's device window is absent from the accessibility tree, so
        /// no synthetic tap reaches it either. A pane name in the launch link
        /// makes the app's core surface screenshottable by a script — and it is
        /// strictly narrower than what the link already carries, since a name
        /// is only useful to somebody who is already connected.
        public let pane: String?
        /// The session the pane belongs to. Defaults to `pane` when absent,
        /// which is right for an agent's own pane (the daemon's `paneTarget`
        /// uses the tmux session name, which IS the session id in that case).
        public let session: String?
        /// Defaults to the RESTRICTIVE value, so a call site that forgets to
        /// say how a link arrived gets the door that only fills a form.
        public var arrival: Arrival = .url

        public init(
            host: String,
            port: Int?,
            spkiPin: String?,
            insecureKey: String?,
            pane: String? = nil,
            session: String? = nil
        ) {
            self.host = host
            self.port = port
            self.spkiPin = spkiPin
            self.insecureKey = insecureKey
            self.pane = pane
            self.session = session
        }
    }

    /// Reads one URL, or refuses.
    ///
    /// It FAILS CLOSED on every ambiguity, and the pin is why. A URL whose pin
    /// is malformed must not become a connection with the pin quietly dropped:
    /// an unpinned connection accepts whatever certificate answers, which is
    /// the one genuinely dangerous state this transport has, and reaching it
    /// through a typo would make the pin decorative. So a present-but-unusable
    /// pin rejects the whole link and the app stays where it was.
    public static func parse(_ url: URL) -> Payload? {
        guard url.scheme?.lowercased() == scheme else { return nil }

        guard let components = URLComponents(url: url, resolvingAgainstBaseURL: false) else {
            return nil
        }
        // `lola-dev://connect?...` puts the action in the host slot;
        // `lola-dev:connect?...` puts it in the path. Accept both, because the
        // difference is invisible in a shell and produces the same intent.
        let action = (components.host ?? components.path)
            .trimmingCharacters(in: CharacterSet(charactersIn: "/"))
            .lowercased()
        guard action == connectAction else { return nil }

        let items = components.queryItems ?? []
        func value(_ names: String...) -> String? {
            for name in names {
                if let found = items.first(where: { $0.name == name })?.value,
                   !found.isEmpty {
                    return found
                }
            }
            return nil
        }

        guard let host = value("host", "addr"), isPlausibleHost(host) else { return nil }

        var port: Int?
        if let raw = value("port") {
            guard let parsed = Int(raw), parsed > 0, parsed <= 65535 else { return nil }
            port = parsed
        }

        // The pin is REQUIRED, and its absence is refused here rather than
        // downstream. An unpinned connection accepts whatever certificate
        // answers, which is the one genuinely dangerous state this transport
        // has; a malformed pin has always rejected the whole link, and letting
        // an ABSENT one through merely to be caught by the form's validator
        // meant the two spellings of the same mistake failed in two different
        // places. Both fail here now, which is also what the README's
        // fail-closed table says.
        guard let rawPin = value("pin", "spkiPin"),
              let pin = normalizedPin(rawPin)
        else { return nil }

        // An empty key is rejected above by `value`; a short one is left to the
        // app, which already refuses below `INSECURE_MIN_KEY_LEN` and can say so
        // in the field rather than swallowing the link with no explanation.
        //
        // `keyfile` is the form that keeps the credential OUT OF THE LOG. Every
        // way of delivering this URL — `simctl openurl`, the launch
        // environment, argv — is written to the device's unified log by the OS
        // before this process runs, so a key spelled into the query string is
        // disclosed whatever the plugin does with it afterwards. A file name is
        // not a credential, so the URL carries one and the bytes are read from
        // the app's own container. `key=` still works and is still logged; the
        // README says so in as many words.
        let key = value("key", "insecureKey") ?? value("keyfile").flatMap(readKeyFile)

        // The pane to open. Purely a destination — it names nothing secret and
        // grants nothing; a link without a connection cannot use it.
        let pane = value("pane").flatMap(sanitizedName)
        let session = value("session").flatMap(sanitizedName)

        return Payload(
            host: host, port: port, spkiPin: pin, insecureKey: key,
            pane: pane, session: session ?? pane)
    }

    /// Reads a bearer key out of a file in the app's own Documents directory.
    ///
    /// The parameter is a BARE NAME, never a path, and that is the whole of the
    /// safety argument: a name with a separator, a parent reference or a
    /// leading dot is refused, so the only thing this can ever open is a file
    /// somebody deliberately staged inside this app's container. On a Simulator
    /// that is one command:
    ///
    ///     printf %s "$KEY" > "$(xcrun simctl get_app_container \
    ///         "$UDID" dev.sushi.lola.mobile data)/Documents/lola-dev-key"
    ///
    /// The file is DELETED once read. It is a hand-off, not storage, and a
    /// bearer credential left in a container that `simctl` will happily copy
    /// out is exactly the disclosure this form exists to avoid.
    static func readKeyFile(_ name: String) -> String? {
        guard isBareName(name) else { return nil }
        guard let dir = FileManager.default.urls(
            for: .documentDirectory, in: .userDomainMask).first
        else { return nil }
        let url = dir.appendingPathComponent(name, isDirectory: false)
        guard let text = try? String(contentsOf: url, encoding: .utf8) else { return nil }
        try? FileManager.default.removeItem(at: url)
        let key = text.trimmingCharacters(in: .whitespacesAndNewlines)
        return key.isEmpty ? nil : key
    }

    /// A single path component that cannot escape the directory it is joined to.
    static func isBareName(_ name: String) -> Bool {
        guard !name.isEmpty, name.utf8.count <= 128 else { return false }
        if name.hasPrefix(".") { return false }
        let forbidden = CharacterSet(charactersIn: "/\\:\u{0}").union(.whitespacesAndNewlines)
        return name.rangeOfCharacter(from: forbidden) == nil
    }

    /// A tmux target as the daemon spells one. The daemon re-resolves whatever
    /// arrives against its own session store before anything is exec'd, so this
    /// only has to be a plausible name rather than a trusted one.
    static func sanitizedName(_ raw: String) -> String? {
        let text = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty, text.utf8.count <= 128 else { return nil }
        let allowed = CharacterSet.alphanumerics.union(CharacterSet(charactersIn: "-_."))
        return text.unicodeScalars.allSatisfy(allowed.contains) ? text : nil
    }

    /// The URL this process was launched with, from its environment or its
    /// arguments, or nil.
    ///
    /// Read once at startup. Nothing re-reads it, because a launch environment
    /// does not change while the process lives and re-applying the same link on
    /// every foreground would fight whatever the user did in between.
    public static func launchURL(
        environment: [String: String] = ProcessInfo.processInfo.environment,
        arguments: [String] = ProcessInfo.processInfo.arguments
    ) -> URL? {
        if let text = environment[environmentVariable], !text.isEmpty,
           let url = URL(string: text) {
            return url
        }
        if let index = arguments.firstIndex(of: argumentFlag),
           index + 1 < arguments.count,
           let url = URL(string: arguments[index + 1]) {
            return url
        }
        return nil
    }

    /// A host has to survive being handed to `NWEndpoint.Host`, so the bar is
    /// low but not absent: no whitespace, no scheme or path punctuation that
    /// would mean the caller pasted a URL into the field, and a sane length.
    static func isPlausibleHost(_ host: String) -> Bool {
        guard !host.isEmpty, host.utf8.count <= 255 else { return false }
        let forbidden = CharacterSet.whitespacesAndNewlines
            .union(CharacterSet(charactersIn: "/\\?#@\"'<>"))
        return host.rangeOfCharacter(from: forbidden) == nil
    }

    /// Accepts the pin in either alphabet and returns the one the transport
    /// compares against.
    ///
    /// `DeviceKey.SPKIPin()` on the daemon is `base64.StdEncoding`, so the live
    /// value carries `+`, `/` and a trailing `=` — and `SPKIPin.matches` is a
    /// plain string comparison, so that exact spelling is what has to arrive.
    /// PLAN.md's M2 payload specifies base64url instead. Those are two
    /// spellings of the same 32 bytes, a URL is exactly where the difference
    /// would bite, and a pin that silently fails to match reads on screen as
    /// "this is not the daemon it claims to be" — the most alarming message in
    /// the app, for a punctuation mismatch. So both are accepted here and the
    /// standard form is what leaves.
    ///
    /// Length is checked by DECODING rather than by counting characters: the
    /// pin is a SHA-256 digest, so the only correct answer is 32 bytes, and
    /// `Data(base64Encoded:)` rejects a wrong alphabet for free.
    static func normalizedPin(_ raw: String) -> String? {
        var text = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        text = text.replacingOccurrences(of: "-", with: "+")
        text = text.replacingOccurrences(of: "_", with: "/")
        while text.count % 4 != 0 { text += "=" }

        guard let bytes = Data(base64Encoded: text), bytes.count == 32 else { return nil }
        return bytes.base64EncodedString()
    }
}

#endif
