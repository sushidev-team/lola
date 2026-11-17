import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import path from "node:path";
import { describe, expect, it } from "vitest";

// The bridge guard replaces Capacitor's `bridge` script handler and was read
// against exactly this release (docs/mobile/04-client.md §1.9).
const REVIEWED_CAPACITOR_IOS = "8.6.2";

const require_ = createRequire(import.meta.url);
const pkgJSON = require_.resolve("@capacitor/ios/package.json");
const root = path.dirname(pkgJSON);

describe("capacitor bridge guard pin", () => {
  it("runs against the reviewed Capacitor iOS release", () => {
    const { version } = JSON.parse(readFileSync(pkgJSON, "utf8")) as { version: string };
    expect(
      version,
      `Capacitor iOS is ${version}; the bridge guard was read against ${REVIEWED_CAPACITOR_IOS}. ` +
        "Re-read WebViewDelegationHandler.swift and CapacitorBridge.registerPlugins: is the `bridge` handler " +
        "still added under that name, is `webViewDelegationHandler` still public, does `load()` still run " +
        "before the page? Then update REVIEWED_CAPACITOR_IOS.",
    ).toBe(REVIEWED_CAPACITOR_IOS);
  });

  it("still registers the handler under the name the guard replaces", () => {
    const file = path.join(root, "Capacitor/Capacitor/WebViewDelegationHandler.swift");
    const src = readFileSync(file, "utf8");
    expect(src).toContain("name: handlerName");
    expect(src).toContain('handlerName = "bridge"');
  });

  it("still exposes the delegation handler on the bridge", () => {
    const file = path.join(root, "Capacitor/Capacitor/CapacitorBridge.swift");
    expect(readFileSync(file, "utf8")).toMatch(/public private\(set\) var webViewDelegationHandler/);
  });
});
