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

    // SAFARI'S WEB INSPECTOR IS OPT-IN NOW, and the bearer key is why.
    //
    // It is the only debugger available on a device, so it stays easy to get:
    // `LOLA_WEB_INSPECTOR=1 npx cap sync ios` (the dev scripts set it). It is no
    // longer the default, because what an attached inspector can reach changed:
    // the key is now durable in the Keychain, so somebody with a stolen
    // UNLOCKED phone, a Mac and a cable could previously attach the inspector
    // and evaluate a plugin call to print the credential. The plugin no longer
    // has a method that returns it (see LolaTransportPlugin+Secrets.swift), so
    // that particular path is closed either way — but a debugger enabled by
    // default on a build carrying a durable credential is not a default worth
    // keeping for the convenience it buys.
    //
    // `cap sync` runs in Node, so this is read at sync time and baked into
    // ios/App/App/capacitor.config.json. Changing it requires a re-sync, which
    // is exactly the deliberate act it should be.
    webContentsDebuggingEnabled: process.env.LOLA_WEB_INSPECTOR === "1",
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
