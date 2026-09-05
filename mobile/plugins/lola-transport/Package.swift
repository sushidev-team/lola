// swift-tools-version: 5.9

// Swift Package Manager is the primary integration, and CocoaPods is only kept
// as a fallback, because Capacitor 8 changed the default. CocoaPods was the iOS
// dependency manager in Capacitor 7 and earlier; since Capacitor 8 `npx cap add
// ios` copies the `ios-spm-template`, writes `ios/App/CapApp-SPM/Package.swift`
// and produces an `App.xcodeproj` with no workspace and no Podfile. `cap sync`
// then scans the app's dependencies for a package carrying a top-level
// `capacitor` key and adds THIS package to `CapApp-SPM/Package.swift` by path.
// So this file is the one an ordinary `npx cap sync ios` reads;
// `LolaTransport.podspec` beside it only matters for an app that deliberately
// opted out with `npx cap add ios --packagemanager CocoaPods`, and the two must
// be kept describing the same source files.
//
// There are two targets and exactly ONE product, and the split is deliberate.
// `LolaTransportCore` is the part with no Capacitor and no Network.framework in
// it — the frame codec, the SPKI extraction, the bearer handshake, the
// connection-state vocabulary — which is to say everything a test can exercise
// without a device. `LolaTransportPlugin` is the bridge and the socket. Only
// the plugin is a product: `cap sync` adds a package's library product to the
// app target, and exporting two would make which one it picks a question rather
// than a fact.

import PackageDescription

// The package name and the library product name must BOTH be "LolaTransport",
// and neither is a free choice. `cap sync` derives that identifier from the npm
// package name ("lola-transport") and writes
//   .package(name: "LolaTransport", path: "../../../node_modules/lola-transport")
//   .product(name: "LolaTransport", package: "LolaTransport")
// into ios/App/CapApp-SPM/Package.swift, which is generated and must not be
// hand-edited. A mismatch does not fail with "no such product": SwiftPM cannot
// resolve the graph at all, so Xcode reports the APP's own product missing —
// "Missing package product 'CapApp-SPM'" — which points at the wrong file
// entirely. @capacitor/keyboard shows the same convention: package
// CapacitorKeyboard, product CapacitorKeyboard, target KeyboardPlugin. The
// TARGET names below are ours and stay descriptive.
let package = Package(
    name: "LolaTransport",
    platforms: [.iOS(.v15)],
    products: [
        .library(
            name: "LolaTransport",
            targets: ["LolaTransportPlugin"]
        )
    ],
    dependencies: [
        // The version window must agree with the Capacitor the app resolved in
        // `ios/App/CapApp-SPM/Package.swift`. Capacitor ships this package as
        // binary xcframeworks, so a mismatch is a link error rather than a
        // subtle one.
        .package(url: "https://github.com/ionic-team/capacitor-swift-pm.git", from: "8.0.0")
    ],
    targets: [
        .target(
            name: "LolaTransportCore",
            path: "ios/Sources/LolaTransportCore"
        ),
        .target(
            name: "LolaTransportPlugin",
            dependencies: [
                "LolaTransportCore",
                .product(name: "Capacitor", package: "capacitor-swift-pm"),
                .product(name: "Cordova", package: "capacitor-swift-pm")
            ],
            path: "ios/Sources/LolaTransportPlugin",
            // The development URL hand-off (`lola-dev://connect?...`) exists
            // only where this is defined, and it is defined only for the debug
            // configuration. See the header of LolaDevLink.swift for what it is
            // and why it is fenced.
            //
            // It is an OWN condition rather than a reliance on `DEBUG` because
            // Xcode's package-target settings are not the app target's: whether
            // `SWIFT_ACTIVE_COMPILATION_CONDITIONS` reaches a dependency is a
            // property of the integration, and a feature that silently vanishes
            // from a debug build is worse than one that is absent on purpose.
            // `.when(configuration: .debug)` is a fact of the manifest, so it
            // travels with the package.
            //
            // It is also the ONLY condition. The gate used to read
            // `#if LOLA_DEV_LINK || DEBUG`, on the theory that accepting DEBUG
            // as well cost nothing — but CocoaPods defines DEBUG for its own
            // Debug configuration, so that spare condition quietly armed the
            // path under exactly the integration the podspec said it was absent
            // from. One switch, named here, is what makes both comments true.
            swiftSettings: [
                .define("LOLA_DEV_LINK", .when(configuration: .debug))
            ]
        ),
        .testTarget(
            name: "LolaTransportCoreTests",
            dependencies: ["LolaTransportCore"],
            path: "ios/Tests/LolaTransportCoreTests"
        )
    ]
)
