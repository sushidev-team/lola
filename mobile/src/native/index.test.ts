import { afterEach, describe, expect, it, vi } from "vitest";

import { NO_NATIVE_TRANSPORT, webTransport } from "./web";

// `Capacitor.Plugins` is a Proxy that invents a function for every property, so
// a probe through it finds a plugin everywhere. The spy proves it is never read.
async function load(opts: { available: string[] }) {
  vi.resetModules();
  const plugins = vi.fn();
  const registered = { name: "registered-proxy" };
  const registerPlugin = vi.fn(() => registered);
  vi.doMock("@capacitor/core", () => ({
    Capacitor: {
      isPluginAvailable: (n: string) => opts.available.includes(n),
      get Plugins() {
        plugins();
        return {};
      },
    },
    registerPlugin,
  }));
  const keyboardPlugin = { name: "keyboard" };
  const browserPlugin = { name: "browser" };
  vi.doMock("@capacitor/keyboard", () => ({ Keyboard: keyboardPlugin }));
  vi.doMock("@capacitor/browser", () => ({ Browser: browserPlugin }));
  const mod = await import("./index");
  return { mod, plugins, registerPlugin, registered, keyboardPlugin, browserPlugin };
}

afterEach(() => {
  vi.doUnmock("@capacitor/core");
  vi.doUnmock("@capacitor/keyboard");
  vi.doUnmock("@capacitor/browser");
});

describe("native availability", () => {
  it("is null off a device and falls back to the web transport", async () => {
    const { mod, plugins, registerPlugin } = await load({ available: [] });
    expect(mod.native).toBeNull();
    expect(mod.keyboard).toBeNull();
    expect(mod.browser).toBeNull();
    // Same registry as `mod`: load() resets modules.
    expect(mod.transport).toBe((await import("./web")).webTransport);
    expect(registerPlugin).not.toHaveBeenCalled();
    expect(plugins).not.toHaveBeenCalled();
  });

  it("registers the plugin when Capacitor says it is available", async () => {
    const { mod, plugins, registerPlugin, registered, keyboardPlugin, browserPlugin } = await load({
      available: ["LolaTransport", "Keyboard", "Browser"],
    });
    expect(registerPlugin).toHaveBeenCalledWith("LolaTransport");
    expect(mod.native).toBe(registered);
    expect(mod.transport).toBe(registered);
    expect(mod.keyboard).toBe(keyboardPlugin);
    expect(mod.browser).toBe(browserPlugin);
    expect(plugins).not.toHaveBeenCalled();
  });

  it("treats each plugin independently", async () => {
    const { mod } = await load({ available: ["Keyboard"] });
    expect(mod.native).toBeNull();
    expect(mod.keyboard).not.toBeNull();
    expect(mod.browser).toBeNull();
  });
});

describe("the web transport", () => {
  it("answers capability probes as unsupported and holds nothing", async () => {
    expect(await webTransport.scanCapability()).toEqual({
      available: false,
      authorization: "denied",
      reason: "unsupported",
    });
    expect(await webTransport.daemons()).toEqual({ daemons: [] });
    expect(await webTransport.readClipboardImage()).toEqual({ present: false });
    expect((await webTransport.status()).phase).toBe("closed");
  });

  it("rejects every method that needs the device", async () => {
    const calls = [
      () => webTransport.scanPairing(),
      () => webTransport.pairBegin({ pairRef: "r", deviceName: "n" }),
      () => webTransport.rename({ daemonId: "d", name: "n" }),
      () => webTransport.connect({ daemonId: "d" }),
      () => webTransport.send({ epoch: 1, frames: [] }),
      () => webTransport.adopt({ epoch: 1 }),
      () => webTransport.pickImage({ source: "library" }),
      () => webTransport.openURL({ url: "https://example.com" }),
    ];
    for (const call of calls) {
      await expect(call()).rejects.toThrow(NO_NATIVE_TRANSPORT);
    }
  });

  it("treats haptics, disconnect, forget and cancel as no-ops", async () => {
    await expect(webTransport.haptic({ kind: "light" })).resolves.toBeUndefined();
    await expect(webTransport.disconnect()).resolves.toBeUndefined();
    await expect(webTransport.forget({ daemonId: "d" })).resolves.toBeUndefined();
    await expect(webTransport.pairCancel({ pairRef: "r" })).resolves.toBeUndefined();
    await expect(webTransport.setIdleTimerDisabled({ disabled: true })).resolves.toBeUndefined();
    const h = await webTransport.addListener("state", () => {});
    await expect(h.remove()).resolves.toBeUndefined();
  });
});
