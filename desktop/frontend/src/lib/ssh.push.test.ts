import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { sshSvc, talkSvc, freshSession, ssh, store } from "./components/session/talk.testkit";
import { setPushSource, type PushError } from "./pushsource";

let on: (d: never, reset: boolean) => void;
let onError: (e: PushError) => void;
const stop = vi.fn();

beforeEach(() => {
  vi.clearAllMocks();
  setPushSource({
    timeline: (_s: string, a: typeof on, b: typeof onError) => {
      on = a;
      onError = b;
      return stop;
    },
  } as never);
});
afterEach(() => setPushSource(null));

const row = (id: string, seq: number) => ({ id, seq, kind: "human", human: { text: id } }) as never;

describe("a pushed timeline", () => {
  it("follow is a no-op without a source", () => {
    setPushSource(null);
    const v = ssh.view(freshSession());
    v.follow()();
    expect(v.pushed).toBe(false);
  });

  it("absorbs a snapshot and then a delta", () => {
    const id = freshSession();
    const v = ssh.view(id);
    v.follow();
    expect(v.pushed).toBe(true);
    on({ session: id, items: [row("a", 1)], lastSeq: 1, connections: [{ id: "c1", state: "connected" }] } as never, true);
    expect(v.items.map((i) => i.id)).toEqual(["a"]);
    expect(v.loaded).toBe(true);
    on({ session: id, items: [row("b", 2)], lastSeq: 2 } as never, false);
    expect(v.items.map((i) => i.id)).toEqual(["a", "b"]);
    expect(v.connections.map((c) => c.id)).toEqual(["c1"]);
  });

  it("a reset drops what the view held", () => {
    const id = freshSession();
    const v = ssh.view(id);
    v.follow();
    on({ session: id, items: [row("a", 5)], lastSeq: 5 } as never, true);
    on({ session: id, items: [row("z", 1)], lastSeq: 1 } as never, true);
    expect(v.items.map((i) => i.id)).toEqual(["z"]);
  });

  it("speak makes one agentSay and reads nothing back", async () => {
    const id = freshSession();
    store.alive = true;
    const v = ssh.view(id);
    v.follow();
    await ssh.speak(id, "hello");
    expect(talkSvc.Say).toHaveBeenCalledTimes(1);
    expect(sshSvc.Timeline).not.toHaveBeenCalled();
    expect(talkSvc.State).not.toHaveBeenCalled();
  });

  it("a refused topic returns to fetch", async () => {
    const id = freshSession();
    const v = ssh.view(id);
    v.follow();
    onError("denied");
    expect(v.pushed).toBe(false);
    await vi.waitFor(() => expect(sshSvc.Timeline).toHaveBeenCalledTimes(1));
  });
});

describe("retention", () => {
  it("trims an unfollowed timeline past the bound but keeps the draft", async () => {
    const { IDLE_VIEWS_KEPT } = await import("./viewretention");
    const id = freshSession();
    const v = ssh.view(id);
    const stopFollow = v.follow();
    on({ session: id, items: [row("a", 1)], lastSeq: 1 } as never, true);
    v.agentDraft = "half a thought";
    stopFollow();
    for (let i = 0; i < IDLE_VIEWS_KEPT; i++) ssh.view(freshSession()).follow()();
    expect(ssh.view(id)).toBe(v);
    expect(v.items).toEqual([]);
    expect(v.loaded).toBe(false);
    expect(v.agentDraft).toBe("half a thought");
  });
});
