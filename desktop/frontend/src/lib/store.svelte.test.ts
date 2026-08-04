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

const { store, dirtyWorktreeRefusal } = await import("./store.svelte");
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

// A kill the daemon refuses because the worktree is dirty is a QUESTION, not a
// failure: the agent is already dead and only force can clear the worktree, so
// the store re-asks instead of flashing "rerun with --force" at a GUI user.
describe("dirty-worktree kill re-asks with force", () => {
  const dirty = (dir = "/Users/martin/.lola/worktrees/nori-app/lola-nori-app-nor-332") =>
    new Error(
      `RuntimeError: session lola-nori-app-nor-332 terminated; worktree kept (uncommitted changes) at ${dir} — rerun with --force to remove it`,
    );

  it("recognises the refusal and pulls out the worktree path", () => {
    expect(dirtyWorktreeRefusal(String(dirty()))).toBe(
      "/Users/martin/.lola/worktrees/nori-app/lola-nori-app-nor-332",
    );
    expect(dirtyWorktreeRefusal("/Volumes/My Disk/wt")).toBeNull();
    expect(dirtyWorktreeRefusal("RuntimeError: unknown session x")).toBeNull();
  });

  it("survives a path containing spaces", () => {
    expect(dirtyWorktreeRefusal(String(dirty("/Users/a b/.lola/worktrees/p/s")))).toBe(
      "/Users/a b/.lola/worktrees/p/s",
    );
  });

  it("opens a second dialog naming the worktree instead of flashing the error", async () => {
    Kill.mockRejectedValueOnce(dirty());
    await store.kill("lola-nori-app-nor-332");
    expect(confirm.request?.confirmLabel).toBe("Delete worktree");
    expect(confirm.request?.detail).toContain("/Users/martin/.lola/worktrees/nori-app");
    expect(store.flash?.kind).not.toBe("bad");
  });

  it("accepting it retries with force", async () => {
    Kill.mockRejectedValueOnce(dirty());
    await store.kill("sess-1");
    confirm.accept();
    expect(Kill).toHaveBeenLastCalledWith("sess-1", true);
  });

  it("declining leaves the worktree alone", async () => {
    Kill.mockRejectedValueOnce(dirty());
    await store.kill("sess-1");
    confirm.cancel();
    expect(Kill).toHaveBeenCalledTimes(1);
  });

  // Any other failure — and a forced kill that still failed — stays a plain
  // error; re-asking there would loop on something force cannot fix.
  it("flashes other failures and asks nothing", async () => {
    Kill.mockRejectedValueOnce(new Error("RuntimeError: unknown session sess-9"));
    await store.kill("sess-9");
    expect(confirm.request).toBeNull();
    expect(store.flash?.kind).toBe("bad");
  });

  it("does not re-ask when force was already set", async () => {
    Kill.mockRejectedValueOnce(dirty());
    await store.kill("sess-1", true);
    expect(confirm.request).toBeNull();
    expect(store.flash?.kind).toBe("bad");
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
