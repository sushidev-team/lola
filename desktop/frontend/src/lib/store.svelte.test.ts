import { describe, it, expect, vi, beforeEach } from "vitest";

// The bindings are the process boundary; stub them so the store's own logic —
// specifically that destructive actions ASK before they act — is what's tested.
const { Kill, StopDaemon, CloseSessionShells, Dev, SwitchAgent, OpenManual, OpenTicket } = vi.hoisted(() => ({
  Kill: vi.fn(),
  StopDaemon: vi.fn(),
  CloseSessionShells: vi.fn(),
  Dev: vi.fn(),
  SwitchAgent: vi.fn(),
  OpenManual: vi.fn(),
  OpenTicket: vi.fn(),
}));

vi.mock("@bindings/desktop", () => ({
  DaemonService: {
    Kill: (...a: unknown[]) => Kill(...a),
    StopDaemon: () => StopDaemon(),
    Dev: (...a: unknown[]) => Dev(...a),
    SwitchAgent: (...a: unknown[]) => SwitchAgent(...a),
    OpenManual: (...a: unknown[]) => OpenManual(...a),
    OpenTicket: (...a: unknown[]) => OpenTicket(...a),
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
  Dev.mockResolvedValue(undefined);
  SwitchAgent.mockResolvedValue({ agent: "codex", message: "switched to codex" });
  OpenManual.mockResolvedValue({ sessionID: "sess-new", branch: "feat/test" });
  OpenTicket.mockResolvedValue({ sessionID: "sess-ticket", identifier: "ENG-10" });
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

  it("askSwitchAgent opens a confirmation and does NOT switch yet", () => {
    store.sessions = [{ id: "sess-1", issue: "ENG-42", agent: "claude" } as never];
    store.askSwitchAgent("sess-1", "codex");
    expect(SwitchAgent).not.toHaveBeenCalled();
    expect(confirm.request?.title).toBe("Switch agent?");
    expect(confirm.request?.body).toBe("Switch ENG-42 from claude to codex?");
    expect(confirm.request?.detail).toBe("The pane is replaced on the same worktree.");
    expect(confirm.request?.confirmLabel).toBe("Switch");
  });

  it("accepting the switch agent confirmation calls switchAgent", () => {
    store.sessions = [{ id: "sess-1", issue: "ENG-42", agent: "claude" } as never];
    store.askSwitchAgent("sess-1", "codex");
    confirm.accept();
    expect(SwitchAgent).toHaveBeenCalledWith({ session: "sess-1", agent: "codex" });
  });

  it("cancelling the switch agent confirmation does nothing", () => {
    store.sessions = [{ id: "sess-1", issue: "ENG-42", agent: "claude" } as never];
    store.askSwitchAgent("sess-1", "codex");
    confirm.cancel();
    expect(SwitchAgent).not.toHaveBeenCalled();
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

// The dev toggle is the app's slowest control: the daemon stops the previous
// holder's tabs and reclaims any port still held in the project's worktrees,
// each with its own SIGTERM grace, before it answers. The store owns the
// in-flight flag because the toggle has three triggers (the row's button, the
// context menu, the `D` shortcut) and only one of them is the button.
describe("the dev toggle reports that it is in flight", () => {
  beforeEach(() => {
    store.devPending = {};
  });

  it("marks the session pending while the call travels, and clears it after", async () => {
    let release!: () => void;
    Dev.mockReturnValue(new Promise<void>((r) => (release = r)));

    const call = store.dev("sess-1", true);
    expect(store.devPending["sess-1"]).toBe(true);
    // Only that session: another row's button must not spin.
    expect(store.devPending["sess-2"]).toBeUndefined();

    release();
    await call;
    expect(store.devPending["sess-1"]).toBeUndefined();
  });

  it("clears the flag when the daemon refuses", async () => {
    Dev.mockRejectedValue(new Error("project configures no dev_commands"));
    await store.dev("sess-1", true);
    expect(store.devPending["sess-1"]).toBeUndefined();
    expect(store.flash?.kind).toBe("bad");
  });

  // Activating is a MOVE — it stops another session's dev servers — so a second
  // click while the first is still travelling is never what was meant.
  it("ignores a second toggle while the first is still in flight", async () => {
    let release!: () => void;
    Dev.mockReturnValue(new Promise<void>((r) => (release = r)));

    const first = store.dev("sess-1", true);
    await store.dev("sess-1", false);
    expect(Dev).toHaveBeenCalledTimes(1);

    release();
    await first;
  });
});

describe("agent launch and switch actions", () => {
  it("openManual carries agentKind into the daemon request", async () => {
    await store.openManual({
      project: "acme",
      branch: "feat/login",
      agent: true,
      agentKind: "codex",
    });
    expect(OpenManual).toHaveBeenCalledWith({
      project: "acme",
      branch: "feat/login",
      agent: true,
      agentKind: "codex",
    });
  });

  it("openTicket carries agentKind into the daemon request", async () => {
    await store.openTicket({
      project: "acme",
      identifier: "ENG-10",
      uuid: "uuid-10",
      agentKind: "opencode",
    });
    expect(OpenTicket).toHaveBeenCalledWith({
      project: "acme",
      identifier: "ENG-10",
      uuid: "uuid-10",
      agentKind: "opencode",
    });
  });

  it("switchAgent invokes DaemonService.SwitchAgent and flashes", async () => {
    await store.switchAgent("sess-1", "codex");
    expect(SwitchAgent).toHaveBeenCalledWith({ session: "sess-1", agent: "codex" });
    expect(store.flash?.text).toBe("switched sess-1 to codex");
  });
});
