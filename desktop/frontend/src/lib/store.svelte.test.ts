import { describe, it, expect, vi, beforeEach } from "vitest";

// The bindings are the process boundary; stub them so the store's own logic —
// specifically that destructive actions ASK before they act — is what's tested.
const { Kill, StopDaemon, CloseSessionShells } = vi.hoisted(() => ({
  Kill: vi.fn(),
  StopDaemon: vi.fn(),
  CloseSessionShells: vi.fn(),
}));

vi.mock("@bindings/desktop", () => ({
  DaemonService: {
    Kill: (...a: unknown[]) => Kill(...a),
    StopDaemon: () => StopDaemon(),
    Alive: vi.fn().mockResolvedValue(false),
    Sessions: vi.fn(),
    Projects: vi.fn(),
    Status: vi.fn(),
  },
  ConfigService: { ConfigExists: vi.fn() },
  TermService: { CloseSessionShells: (...a: unknown[]) => CloseSessionShells(...a) },
}));

const { store } = await import("./store.svelte");
const { confirm } = await import("./confirm.svelte");

beforeEach(() => {
  vi.clearAllMocks();
  Kill.mockResolvedValue(undefined);
  StopDaemon.mockResolvedValue(undefined);
  CloseSessionShells.mockResolvedValue(undefined);
  confirm.cancel();
  store.sessions = [];
  store.pushErrors = {};
});

describe("destructive actions ask first", () => {
  it("askKill opens a confirmation and does NOT kill yet", () => {
    store.askKill("sess-1");
    expect(Kill).not.toHaveBeenCalled();
    expect(confirm.request?.confirmLabel).toBe("Kill");
  });

  it("accepting the confirmation kills the session", () => {
    store.askKill("sess-1");
    confirm.accept();
    expect(Kill).toHaveBeenCalledWith("sess-1", false);
  });

  it("cancelling the confirmation kills nothing", () => {
    store.askKill("sess-1");
    confirm.cancel();
    expect(Kill).not.toHaveBeenCalled();
  });

  // The daemon-stop button lives in the footer next to "restart"; a misclick used
  // to halt every poll outright.
  it("askStopDaemon confirms before stopping", () => {
    store.askStopDaemon();
    expect(StopDaemon).not.toHaveBeenCalled();
    confirm.accept();
    expect(StopDaemon).toHaveBeenCalledOnce();
  });

  it("names the session in the prompt when it is known", () => {
    store.sessions = [{ id: "sess-1", issue: "ENG-42", title: "fix login" } as never];
    store.askKill("sess-1");
    expect(confirm.request?.body).toContain("ENG-42");
    expect(confirm.request?.body).toContain("fix login");
  });

  // An id with no matching session (removed from the snapshot between the
  // keypress and the dialog) still has to render something the user can read.
  it("falls back to the id when the session is unknown", () => {
    store.askKill("abcdef1234567890");
    expect(confirm.request?.body).toContain("abcdef12");
  });
});

// The push loop swallowed per-command errors; the store now holds them so an
// out-of-date daemon can be explained instead of silently blanking a read.
describe("push errors", () => {
  it("pushError surfaces the first non-empty entry and ignores recovered ones", () => {
    store.pushErrors = { sessions: "", projects: 'unknown cmd "projects"' };
    expect(store.pushError).toEqual({ cmd: "projects", msg: 'unknown cmd "projects"' });
  });

  it("pushError is null when every entry is empty", () => {
    store.pushErrors = { sessions: "", projects: "" };
    expect(store.pushError).toBeNull();
  });

  it("dismissPushError clears the set", () => {
    store.pushErrors = { projects: "boom" };
    store.dismissPushError();
    expect(store.pushErrors).toEqual({});
    expect(store.pushError).toBeNull();
  });
});
