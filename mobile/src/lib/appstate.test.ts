import { describe, it, expect, vi, afterEach } from "vitest";
import { installAppState } from "./appstate";

type Cb = (e: { active?: boolean }) => void;

function installPlugin() {
  let cb: Cb | undefined;
  const remove = vi.fn();
  const p = {
    addListener: vi.fn(async (_event: string, fn: Cb) => {
      cb = fn;
      return { remove };
    }),
  };
  (globalThis as { Capacitor?: unknown }).Capacitor = { Plugins: { LolaTransport: p } };
  return { p, remove, fire: (active: boolean) => cb?.({ active }) };
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
