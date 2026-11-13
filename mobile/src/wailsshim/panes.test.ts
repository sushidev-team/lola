import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { ChannelTransport } from "./channeltransport";
import { bridge } from "./bridge";
import { DaemonService, TermService } from "./desktop";
import { UnsupportedOnMobileError } from "./errors";
import { FRAME_RESP, FakeChannel, type Frame } from "../wire";

// cmd=panes and cmd=shellCreate: the two commands the daemon grew for the phone,
// because the desktop answers both in-process through tmux and a phone has no
// tmux to ask. Everything here is the ORDINARY request path — same bridge, same
// transport, same correlator — which is the point: a second path would be a
// second set of failure modes to debug on a device.

let ch: FakeChannel;

/** Reply to the next req with `ok: true` and this data. */
function answerWith(data: unknown): void {
  ch.onSend = (f: Frame) => {
    if (f.type === "req") {
      ch.deliver({ v: 1, type: FRAME_RESP, id: f.id, payload: { ok: true, data } });
    }
  };
}

/** Reply to the next req with an application refusal carrying `reason`. */
function refuseWith(reason: string): void {
  ch.onSend = (f: Frame) => {
    if (f.type === "req") {
      ch.deliver({ v: 1, type: FRAME_RESP, id: f.id, payload: { ok: false, error: reason } });
    }
  };
}

/** The single req frame this test produced. */
function frameOf(): Frame {
  const reqs = ch.sent.filter((f) => f.type === "req");
  expect(reqs).toHaveLength(1);
  return reqs[0];
}

beforeEach(async () => {
  ch = new FakeChannel();
  answerWith({});
  const t = new ChannelTransport({ open: async () => ch });
  await t.connect({ host: "127.0.0.1", spkiPin: "pin" });
  bridge.installTransport(t);
});

afterEach(() => bridge.installTransport(null));

describe("DaemonService.Panes", () => {
  it("sends cmd=panes with the session on the Request, not in an args blob", async () => {
    await DaemonService.Panes("lola-fe-42");
    expect(frameOf()).toMatchObject({ cmd: "panes" });
    expect(frameOf().payload).toEqual({ cmd: "panes", session: "lola-fe-42" });
  });

  it("decodes the inventory and keeps the daemon's strip order", async () => {
    // The order IS the contract: agent, then shells and dev tabs by index, then
    // review. A client that re-sorts is a client that disagrees with the Mac.
    answerWith({
      session: "lola-fe-42",
      panes: [
        { name: "lola-fe-42", kind: "agent", label: "agent" },
        { name: "lola-fe-42-shell-1", kind: "shell", label: "shell 1", index: 1 },
        { name: "lola-fe-42-dev-1", kind: "dev", label: "dev 1", index: 1 },
        { name: "lola-fe-42-review", kind: "review", label: "review" },
      ],
      review: { name: "lola-fe-42-review", kind: "review", label: "review" },
      canCreateShell: true,
    });
    const d = await DaemonService.Panes("lola-fe-42");
    expect(d.session).toBe("lola-fe-42");
    expect(d.panes.map((p) => p.name)).toEqual([
      "lola-fe-42",
      "lola-fe-42-shell-1",
      "lola-fe-42-dev-1",
      "lola-fe-42-review",
    ]);
    expect(d.panes.map((p) => p.kind)).toEqual(["agent", "shell", "dev", "review"]);
    expect(d.panes[1].index).toBe(1);
    expect(d.review?.name).toBe("lola-fe-42-review");
    expect(d.canCreateShell).toBe(true);
  });

  it("normalizes the two shapes the Go encoder produces for 'nothing there'", async () => {
    // A nil slice with no omitempty is `null`, and omitempty does nothing to a
    // struct, so an absent review pane is a zero-valued one. There is no
    // generated createFrom on this type to smooth either over.
    answerWith({
      session: "lola-fe-42",
      panes: null,
      review: { name: "", kind: "", label: "" },
      canCreateShell: false,
    });
    const d = await DaemonService.Panes("lola-fe-42");
    expect(d.panes).toEqual([]);
    expect(d.review).toBeUndefined();
    expect(d.canCreateShell).toBe(false);
  });

  it("surfaces an unknown session as the daemon's own sentence", async () => {
    refuseWith('unknown session "lola-fe-99"');
    await expect(DaemonService.Panes("lola-fe-99")).rejects.toMatchObject({
      name: "DaemonError",
      cmd: "panes",
      message: 'unknown session "lola-fe-99"',
    });
  });
});

describe("DaemonService.ShellCreate", () => {
  it("sends cmd=shellCreate carrying only the session, never a pane name", async () => {
    // The index is the daemon's to allocate: two phones and a desktop can be
    // racing for "-shell-2" and only the daemon sees all of them.
    answerWith({ session: "lola-fe-42", pane: "lola-fe-42-shell-2", index: 2 });
    await DaemonService.ShellCreate("lola-fe-42");
    expect(frameOf().payload).toEqual({ cmd: "shellCreate", session: "lola-fe-42" });
    expect(JSON.stringify(frameOf())).not.toContain("shell-2");
  });

  it("returns the pane to subscribe to, with the index the daemon chose", async () => {
    answerWith({ session: "lola-fe-42", pane: "lola-fe-42-shell-2", index: 2 });
    await expect(DaemonService.ShellCreate("lola-fe-42")).resolves.toEqual({
      session: "lola-fe-42",
      pane: "lola-fe-42-shell-2",
      index: 2,
    });
  });

  // A refusal's REASON is the whole value of the error path: a "+" that stops
  // working for no stated reason reads as a broken button. Each of these is a
  // sentence internal/daemon/panes.go actually produces.
  const refusals: [string, string][] = [
    ["no worktree", 'session "lola-fe-42" has no worktree'],
    ["a worktree removed underneath the session", "worktree unavailable: /Users/x/.lola/worktrees/lola/fe-42"],
    ["the shell cap", 'session "lola-fe-42" already has 16 shells, which is the cap'],
    ["an unknown session", 'unknown session "lola-fe-99"'],
  ];

  for (const [what, reason] of refusals) {
    it(`surfaces ${what} verbatim rather than as a generic failure`, async () => {
      refuseWith(reason);
      const err = await DaemonService.ShellCreate("lola-fe-42").then(
        () => null,
        (e: unknown) => e,
      );
      expect(err).toMatchObject({ name: "DaemonError", cmd: "shellCreate" });
      expect((err as Error).message).toBe(reason);
    });
  }

  it("does not rewrap a refusal into a message of its own", async () => {
    // The shim adds nothing on this path on purpose. If it ever starts
    // prefixing, this catches it before a user reads a sentence twice.
    refuseWith("worktree unavailable: /tmp/gone");
    await expect(DaemonService.ShellCreate("lola-fe-42")).rejects.toThrow(/^worktree unavailable: \/tmp\/gone$/);
  });
});

describe("the neighbouring TermService methods stay platform rejections", () => {
  // Both now have a daemon-side answer, and neither may quietly become it: the
  // shared terms.svelte.ts asks a differently shaped question (bare shell names,
  // a client-allocated name plus a worktree path), and answering it from the new
  // commands would put the naming back in the client.
  it.each([
    ["TermService.Shells", () => TermService.Shells("lola-fe-42")],
    ["TermService.Shell", () => TermService.Shell("lola-fe-42-shell-1", "/tmp")],
  ])("%s rejects and names the method to call instead", async (_name, call) => {
    const err = await call().then(
      () => null,
      (e: unknown) => e,
    );
    expect(err).toBeInstanceOf(UnsupportedOnMobileError);
    expect(ch.sent.filter((f) => f.type === "req")).toHaveLength(0);
  });

  it("points TermService.Shell at DaemonService.ShellCreate in its reason", async () => {
    const err = (await TermService.Shell("lola-fe-42-shell-1", "/tmp").catch(
      (e: unknown) => e,
    )) as UnsupportedOnMobileError;
    expect(err.reason).toContain("DaemonService.ShellCreate");
  });
});
