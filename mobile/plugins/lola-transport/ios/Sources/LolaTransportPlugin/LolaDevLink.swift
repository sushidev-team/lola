import Foundation

// The development link: a way to put the Simulator (no camera, no taps from a
// script) in front of a live daemon and on a given screen.
//
// It is fenced on three sides, and none of them may be loosened:
//
//   1. The scheme is `lola-dev`, which names itself as development-only and is
//      not the pairing format. The QR the desktop draws is an opaque `lola2.`
//      token that only Lola's own scanner reads.
//   2. Everything that turns a URL into an action is compiled out unless
//      `LOLA_DEV_LINK` is defined, which Package.swift does for the debug
//      configuration only. It is the ONLY condition: CocoaPods defines DEBUG
//      for its own Debug configuration, so a `|| DEBUG` arm would arm a release
//      path. Even the constant names sit inside the gate: a release binary
//      should not carry the name of the variable it does not read.
//   3. The event is stamped with HOW the link arrived. `dev-url` (the OS URL
//      router, on behalf of any app) may only fill the Connect screen and wait
//      for a human. `dev-launch` (this process's own launch environment or
//      argv, which only whoever starts the process can set) may also pair.
//      The two must never be reported as one door.

#if LOLA_DEV_LINK

    enum LolaDevLink {
        /// Registered in `mobile/ios/App/App/Info.plist` under `CFBundleURLTypes`.
        static let scheme = "lola-dev"
        /// `lola-dev://open?route=<path>&sheet=<name>&pair=<lola2 payload>`.
        static let action = "open"
        /// The same URL as a launch environment variable: from iOS 26 the system
        /// interposes a confirmation on custom-scheme opens from `simctl`, which a
        /// script cannot tap.
        static let environmentVariable = "LOLA_DEV_LINK"
        /// The argv form. Two dashes: a single-dash argument is swallowed by
        /// `UserDefaults`' own argument parsing.
        static let argumentFlag = "--lola-dev-link"

        enum Arrival: String {
            case url = "dev-url"
            case launch = "dev-launch"
        }

        /// What a link may say: a destination, and optionally a pairing payload.
        /// Once the phone is paired, links carry no credentials at all.
        struct Payload: Equatable {
            var route: String?
            var sheet: String?
            var pair: String?
            var arrival: Arrival = .url
        }

        static func parse(_ url: URL) -> Payload? {
            guard url.scheme?.lowercased() == scheme,
                let components = URLComponents(url: url, resolvingAgainstBaseURL: false)
            else { return nil }
            // `lola-dev://open?...` puts the action in the host slot,
            // `lola-dev:open?...` in the path; the difference is invisible in a shell.
            let name = (components.host ?? components.path).trimmingCharacters(in: CharacterSet(charactersIn: "/"))
            guard name.lowercased() == action else { return nil }

            let items = components.queryItems ?? []
            func value(_ key: String) -> String? {
                items.first { $0.name == key }?.value.flatMap { $0.isEmpty ? nil : $0 }
            }
            let payload = Payload(
                route: value("route").flatMap(route), sheet: value("sheet").flatMap(sheet),
                pair: value("pair").flatMap(pair))
            // A present-but-unusable part rejects the whole link: a typo must not
            // quietly turn into a different destination.
            if value("route") != nil, payload.route == nil { return nil }
            if value("sheet") != nil, payload.sheet == nil { return nil }
            if value("pair") != nil, payload.pair == nil { return nil }
            return payload
        }

        /// The link from this process's own environment or argv, if any.
        static func launchURL(
            environment: [String: String] = ProcessInfo.processInfo.environment,
            arguments: [String] = ProcessInfo.processInfo.arguments
        ) -> URL? {
            if let text = environment[environmentVariable], let url = URL(string: text) { return url }
            guard let index = arguments.firstIndex(of: argumentFlag), arguments.indices.contains(index + 1) else {
                return nil
            }
            return URL(string: arguments[index + 1])
        }

        /// An app route: absolute, short, path characters only.
        static func route(_ raw: String) -> String? {
            guard raw.hasPrefix("/"), raw.count <= 200,
                raw.range(of: "^[A-Za-z0-9/_.:~-]+$", options: .regularExpression) != nil
            else { return nil }
            return raw
        }

        static func sheet(_ raw: String) -> String? {
            raw.range(of: "^[A-Za-z0-9_-]{1,40}$", options: .regularExpression) != nil ? raw : nil
        }

        static func pair(_ raw: String) -> String? {
            raw.hasPrefix("lola2.") && raw.count <= 1024 ? raw : nil
        }
    }

#endif
