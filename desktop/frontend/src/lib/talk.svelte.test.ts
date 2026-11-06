import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";

const svc = vi.hoisted(() => ({
  State: vi.fn(),
  Stop: vi.fn(),
  Say: vi.fn(),
  Choose: vi.fn(),
  QueueRelease: vi.fn(),
  Feed: vi.fn(),
  FeedOutput: vi.fn(),
  Ended: vi.fn(),
}));
vi.mock("@bindings/desktop", () => ({ TalkService: svc }));

const { talk, TALK_POLL_MS, isTalkUnsupported, unsupportedState } = await import("./talk.svelte");

let n = 0;
let id = "";
const answer = { session: "x", caps: { feed: true, send: true, stop: true, cards: true }, composer: "resting", feedSeq: 7, health: "ok" };
const flush = async () => {
  for (let i = 0; i < 5; i++) await Promise.resolve();
};

beforeEach(() => {
  n++;
  id = `t${n}`;
  for (const f of Object.values(svc)) f.mockReset();
  svc.State.mockResolvedValue(answer);
  vi.useFakeTimers();
  vi.spyOn(document, "hasFocus").mockReturnValue(true);
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe("state", () => {
  it("is null before the first answer, and reading it writes nothing", () => {
    expect(talk.state(id)).toBeNull();
    expect(talk.unsupported(id)).toBe(false);
    expect(svc.State).not.toHaveBeenCalled();
  });

  it("holds the daemon's answer, feedSeq included", async () => {
    await talk.refresh(id);
    expect(svc.State).toHaveBeenCalledWith({ session: id });
    expect(talk.state(id)?.feedSeq).toBe(7);
    expect(talk.error(id)).toBe("");
  });

  it("does not share one session's answer with another", async () => {
    await talk.refresh(id);
    expect(talk.state(`${id}-other`)).toBeNull();
  });
});

describe("a daemon without these commands", () => {
  it.each(['unknown cmd "talkState"', "not yet"])("reads %s as unsupported, with every capability off", async (msg) => {
    svc.State.mockRejectedValue(new Error(msg));
    await talk.refresh(id);
    expect(talk.unsupported(id)).toBe(true);
    expect(talk.error(id)).toBe("");
    const s = talk.state(id);
    expect(s?.health).toBe("unsupported");
    expect(s?.caps).toEqual({ feed: false, send: false, stop: false, cards: false });
  });

  it("recovers when a restarted daemon answers", async () => {
    svc.State.mockRejectedValueOnce(new Error('unknown cmd "talkState"'));
    await talk.refresh(id);
    expect(talk.unsupported(id)).toBe(true);
    await talk.refresh(id);
    expect(talk.unsupported(id)).toBe(false);
    expect(talk.state(id)?.health).toBe("ok");
  });

  it("does not read a sentence that merely mentions the words as unsupported", () => {
    expect(isTalkUnsupported(new Error("the agent has not yet started"))).toBe(false);
    expect(isTalkUnsupported(new Error("dial unix: connection refused"))).toBe(false);
    expect(unsupportedState("s").session).toBe("s");
  });
});

describe("a daemon that is down", () => {
  it("keeps the last answer and says why", async () => {
    await talk.refresh(id);
    svc.State.mockRejectedValue(new Error("connection refused"));
    await talk.refresh(id);
    expect(talk.state(id)?.composer).toBe("resting");
    expect(talk.unsupported(id)).toBe(false);
    expect(talk.error(id)).toBe("connection refused");
  });
});

describe("the poller", () => {
  it("asks at once and then once a second", async () => {
    const end = talk.poll(id);
    await flush();
    expect(svc.State).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(TALK_POLL_MS * 3);
    expect(svc.State).toHaveBeenCalledTimes(4);
    expect(TALK_POLL_MS).toBe(1000);
    end();
  });

  it("stops when it is ended", async () => {
    const end = talk.poll(id);
    await flush();
    end();
    await vi.advanceTimersByTimeAsync(TALK_POLL_MS * 5);
    expect(svc.State).toHaveBeenCalledTimes(1);
  });

  it("asks nothing while the window has no focus, and resumes", async () => {
    const end = talk.poll(id);
    await flush();
    vi.mocked(document.hasFocus).mockReturnValue(false);
    await vi.advanceTimersByTimeAsync(TALK_POLL_MS * 5);
    expect(svc.State).toHaveBeenCalledTimes(1);
    vi.mocked(document.hasFocus).mockReturnValue(true);
    await vi.advanceTimersByTimeAsync(TALK_POLL_MS);
    expect(svc.State).toHaveBeenCalledTimes(2);
    end();
  });

  it("never has two asks out: a slow daemon is not piled onto", async () => {
    let release = (_: unknown) => {};
    svc.State.mockReturnValue(new Promise((r) => (release = r)));
    const end = talk.poll(id);
    await vi.advanceTimersByTimeAsync(TALK_POLL_MS * 5);
    expect(svc.State).toHaveBeenCalledTimes(1);
    release(answer);
    await flush();
    end();
  });

  it("is shared by two views of one session and ends with the last", async () => {
    const a = talk.poll(id);
    const b = talk.poll(id);
    await vi.advanceTimersByTimeAsync(TALK_POLL_MS);
    expect(svc.State).toHaveBeenCalledTimes(2);
    a();
    a(); // ending twice must not end the other view's poll
    await vi.advanceTimersByTimeAsync(TALK_POLL_MS);
    expect(svc.State).toHaveBeenCalledTimes(3);
    b();
    await vi.advanceTimersByTimeAsync(TALK_POLL_MS * 3);
    expect(svc.State).toHaveBeenCalledTimes(3);
  });
});

describe("the wrappers", () => {
  it("name the session they were asked about", async () => {
    svc.Stop.mockResolvedValue({ stopped: true });
    await expect(talk.stop(id)).resolves.toEqual({ stopped: true });
    expect(svc.Stop).toHaveBeenCalledWith({ session: id });

    await talk.choose(id, "h", "1");
    expect(svc.Choose).toHaveBeenCalledWith({ session: id, hash: "h", key: "1" });

    await talk.queueRelease(id);
    expect(svc.QueueRelease).toHaveBeenCalledWith({ session: id });

    await talk.say({ session: id, text: "hi" } as never);
    expect(svc.Say).toHaveBeenCalledWith({ session: id, text: "hi" });

    await talk.feed({ session: id } as never);
    expect(svc.Feed).toHaveBeenCalledWith({ session: id });

    await talk.feedOutput(id, "i1");
    expect(svc.FeedOutput).toHaveBeenCalledWith({ session: id, item: "i1" });

    svc.Ended.mockResolvedValue({ sessions: [{ id: "gone", reason: "PR #1 merged" }] });
    await talk.loadEnded();
    expect(talk.ended("gone")?.reason).toBe("PR #1 merged");
    expect(talk.endedList()).toHaveLength(1);
  });

  it("lets a refusal reach the caller, who shows it", async () => {
    svc.Stop.mockRejectedValue(new Error("A goal runs this session"));
    await expect(talk.stop(id)).rejects.toThrow("A goal runs this session");
  });

  it("rejects a stop the daemon refused as data, with its sentence", async () => {
    svc.Stop.mockResolvedValue({ stopped: false, why: "Could not reach the terminal" });
    await expect(talk.stop(id)).rejects.toBe("Could not reach the terminal");
  });
});

// A phone reaches store.svelte.ts through a vite alias, and it has no TalkService.
describe("phone safety", () => {
  it("store.svelte.ts does not import the service or this file", async () => {
    const src = (await import("./store.svelte.ts?raw")).default;
    expect(src).not.toMatch(/import[^;]*(TalkService|SSHService|talk\.svelte|ssh\.svelte)/);
  });
});
