import { describe, expect, it, vi } from "vitest";
import { Emit, Off, OffAll, On, Once, emit, listenerCount } from "./events";

describe("the shim's event bus", () => {
  it("delivers an EVENT OBJECT whose data field carries the payload", () => {
    // Every call site in the shared frontend reads `e.data`, never `e` itself:
    // store.svelte.ts does `this.sessions = e.data?.sessions ?? []`, and
    // LiveTerminal does `term.write(b64ToBytes(e.data))`. Handing the payload
    // straight to the callback would leave both silently empty.
    OffAll();
    const seen: unknown[] = [];
    On<{ sessions: string[] }>("daemon:sessions", (e) => seen.push(e));
    emit("daemon:sessions", { sessions: ["a"] });
    expect(seen).toEqual([{ name: "daemon:sessions", data: { sessions: ["a"] } }]);
  });

  it("returns an unsubscribe function from On, which every caller assigns", () => {
    OffAll();
    const cb = vi.fn();
    const off = On("x", cb);
    emit("x", 1);
    off();
    emit("x", 2);
    expect(cb).toHaveBeenCalledTimes(1);
    expect(listenerCount("x")).toBe(0);
  });

  it("delivers a Once subscription exactly once and then forgets it", () => {
    OffAll();
    const cb = vi.fn();
    Once("x", cb);
    emit("x", 1);
    emit("x", 2);
    expect(cb).toHaveBeenCalledTimes(1);
    expect(listenerCount("x")).toBe(0);
  });

  it("contains a throwing listener so its siblings still get the event", () => {
    // On the desktop the Go-side fan-out gives this for free. Losing it here
    // would let one bad row freeze the whole session list.
    OffAll();
    const err = vi.spyOn(console, "error").mockImplementation(() => {});
    const good = vi.fn();
    On("x", () => {
      throw new Error("boom");
    });
    On("x", good);
    emit("x", 1);
    expect(good).toHaveBeenCalledTimes(1);
    err.mockRestore();
  });

  it("survives a listener that unsubscribes during delivery", () => {
    OffAll();
    const b = vi.fn();
    const offA = On("x", () => offB());
    const offB = On("x", b);
    emit("x", 1);
    offA();
    expect(b).not.toHaveBeenCalled();
  });

  it("drops listeners by name with Off and wholesale with OffAll", () => {
    OffAll();
    On("a", vi.fn());
    On("b", vi.fn());
    Off("a");
    expect(listenerCount("a")).toBe(0);
    expect(listenerCount("b")).toBe(1);
    OffAll();
    expect(listenerCount("b")).toBe(0);
  });

  it("keeps Wails' Promise<boolean> signature on Emit", async () => {
    OffAll();
    const cb = vi.fn();
    On("x", cb);
    await expect(Emit("x", 7)).resolves.toBe(false);
    expect(cb).toHaveBeenCalledWith({ name: "x", data: 7 });
  });
});
