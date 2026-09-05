import { describe, it, expect, vi, afterEach } from "vitest";
import { installAppBackground, installAppState } from "./appstate";

type Cb = (e: { active?: boolean }) => void;

function installPlugin() {
  // EVERY listener, not the last one. The foreground and the background signals
  // are two independent installs over the same plugin event, so a stub that kept
  // one callback would let a real regression in either of them pass unseen.
  const cbs: Cb[] = [];
  const remove = vi.fn();
  const p = {
    addListener: vi.fn(async (_event: string, fn: Cb) => {
      cbs.push(fn);
      return { remove };
    }),
  };
  (globalThis as { Capacitor?: unknown }).Capacitor = { Plugins: { LolaTransport: p } };
  return {
    p,
    remove,
    fire: (active: boolean) => {
      for (const cb of [...cbs]) cb({ active });
    },
  };
}

afterEach(() => {
  delete (globalThis as { Capacitor?: unknown }).Capacitor;
  vi.restoreAllMocks();
});

describe("the foreground signal", () => {
  it("fires on the way in and stays silent on the way out", async () => {
    // Only one direction needs the app. The plugin has already closed the
    // socket on the way out and said so on the `state` event; a callback there
    // would only race the teardown it cannot undo.
    const plugin = installPlugin();
    const onForeground = vi.fn();
    const off = installAppState(onForeground);
    await vi.waitFor(() => expect(plugin.p.addListener).toHaveBeenCalled());

    plugin.fire(false);
    expect(onForeground).not.toHaveBeenCalled();

    plugin.fire(true);
    expect(onForeground).toHaveBeenCalledTimes(1);
    off();
  });

  it("stops after teardown", async () => {
    const plugin = installPlugin();
    const onForeground = vi.fn();
    const off = installAppState(onForeground);
    await vi.waitFor(() => expect(plugin.p.addListener).toHaveBeenCalled());
    off();

    plugin.fire(true);
    expect(onForeground).not.toHaveBeenCalled();
    expect(plugin.remove).toHaveBeenCalled();
  });

  it("still works with no plugin at all", () => {
    // A browser dev session, and equally a device build whose plugin predates
    // the `appState` event. `visibilitychange` is the reason this is a
    // degradation rather than a dead feature.
    const onForeground = vi.fn();
    const off = installAppState(onForeground);

    vi.spyOn(document, "visibilityState", "get").mockReturnValue("visible");
    document.dispatchEvent(new Event("visibilitychange"));
    expect(onForeground).toHaveBeenCalledTimes(1);
    off();
  });

  it("ignores a visibilitychange that is going away", () => {
    const onForeground = vi.fn();
    const off = installAppState(onForeground);

    vi.spyOn(document, "visibilityState", "get").mockReturnValue("hidden");
    document.dispatchEvent(new Event("visibilitychange"));
    expect(onForeground).not.toHaveBeenCalled();
    off();
  });

  it("survives a plugin whose addListener rejects", async () => {
    // An older plugin binary has no such event and the bridge refuses the
    // registration. That must not take the launch down, and it must not stop
    // the web trigger being wired.
    (globalThis as { Capacitor?: unknown }).Capacitor = {
      Plugins: { LolaTransport: { addListener: vi.fn().mockRejectedValue(new Error("nope")) } },
    };
    const onForeground = vi.fn();
    const off = installAppState(onForeground);
    await Promise.resolve();

    vi.spyOn(document, "visibilityState", "get").mockReturnValue("visible");
    document.dispatchEvent(new Event("visibilitychange"));
    expect(onForeground).toHaveBeenCalledTimes(1);
    off();
  });
});

// The other direction, which exists for exactly one caller: the pane size pin
// hands a resized window back on every way out, and a phone going into a pocket
// is one of them.
describe("the background signal", () => {
  it("fires on the way out and stays silent on the way in", async () => {
    const plugin = installPlugin();
    const onBackground = vi.fn();
    const off = installAppBackground(onBackground);
    await vi.waitFor(() => expect(plugin.p.addListener).toHaveBeenCalled());

    plugin.fire(true);
    expect(onBackground).not.toHaveBeenCalled();

    plugin.fire(false);
    expect(onBackground).toHaveBeenCalledTimes(1);
    off();
  });

  it("stops after teardown", async () => {
    const plugin = installPlugin();
    const onBackground = vi.fn();
    const off = installAppBackground(onBackground);
    await vi.waitFor(() => expect(plugin.p.addListener).toHaveBeenCalled());
    off();

    plugin.fire(false);
    expect(onBackground).not.toHaveBeenCalled();
    expect(plugin.remove).toHaveBeenCalled();
  });

  it("still works with no plugin at all", () => {
    // A browser dev session, or a plugin binary predating the event. Releasing
    // a pin twice costs one no-op request; not releasing it squashes a window.
    const onBackground = vi.fn();
    const off = installAppBackground(onBackground);

    vi.spyOn(document, "visibilityState", "get").mockReturnValue("hidden");
    document.dispatchEvent(new Event("visibilitychange"));
    expect(onBackground).toHaveBeenCalledTimes(1);
    off();
  });

  it("ignores a visibilitychange that is coming back", () => {
    const onBackground = vi.fn();
    const off = installAppBackground(onBackground);

    vi.spyOn(document, "visibilityState", "get").mockReturnValue("visible");
    document.dispatchEvent(new Event("visibilitychange"));
    expect(onBackground).not.toHaveBeenCalled();
    off();
  });

  it("does not disturb the foreground signal it sits beside", async () => {
    const plugin = installPlugin();
    const onForeground = vi.fn();
    const onBackground = vi.fn();
    const offFg = installAppState(onForeground);
    const offBg = installAppBackground(onBackground);
    await vi.waitFor(() => expect(plugin.p.addListener).toHaveBeenCalledTimes(2));

    plugin.fire(false);
    plugin.fire(true);
    expect(onBackground).toHaveBeenCalledTimes(1);
    expect(onForeground).toHaveBeenCalledTimes(1);
    offFg();
    offBg();
  });
});
