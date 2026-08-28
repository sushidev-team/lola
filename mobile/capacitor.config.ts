import type { CapacitorConfig } from "@capacitor/cli";
import { KeyboardResize } from "@capacitor/keyboard";

// Capacitor 8 defaults to Swift Package Manager on iOS, so `npx cap add ios`
// produces an .xcodeproj and a CapApp-SPM package rather than a CocoaPods
// workspace. There is no `pod install` step and nothing to open but
// ios/App/App.xcodeproj.
const config: CapacitorConfig = {
  // Distinct from the desktop bundle id (dev.sushi.lola.desktop). Changing an
  // app id after the first TestFlight upload orphans the App Store Connect
  // record, so this is effectively permanent from the first build onward.
  appId: "dev.sushi.lola.mobile",
  appName: "Lola",

  // MUST equal vite.config.ts's build.outDir. `cap sync` copies this directory
  // into ios/App/App/public, which is gitignored precisely because it is a copy.
  webDir: "dist",

  ios: {
    // The Xcode target name from the ios-spm-template.
    scheme: "App",

    // The app draws its own safe areas (see the --spacing-safe-* tokens in
    // app.css), so WKWebView must not add automatic insets on top of them.
    contentInset: "never",

    // The document itself never scrolls: every scrollable region in the app is
    // an internal one, and leaving the WebView scrollable gives the whole page
    // a rubber-band bounce that reads as the terminal coming loose.
    scrollEnabled: false,

    // Safari's Web Inspector is the only debugger available on a device, and
    // the app ships to internal TestFlight testers rather than the App Store.
    // Turn this off if that ever changes.
    webContentsDebuggingEnabled: true,
  },

  plugins: {
    Keyboard: {
      // Body/native resize fights FitAddon and the terminal's ResizeObserver
      // into a resize storm: the keyboard shrinks the viewport, the observer
      // refits, the refit changes the layout, the keyboard reports again. The
      // terminal view pads its own host by the reported keyboardHeight instead,
      // which is one deliberate layout change rather than a feedback loop.
      resize: KeyboardResize.None,
    },
  },

  // NOTE: there is deliberately no `server` block. `server.url` and
  // `server.cleartext` are live-reload conveniences that make a shipped build
  // load its UI from a developer's laptop. Use `npx cap run ios --live-reload
  // --external` for that, which sets them for one run without writing them into
  // the config a release is built from.
};

export default config;
