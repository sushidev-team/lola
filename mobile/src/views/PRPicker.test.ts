import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/svelte";

import PRPicker from "./PRPicker.svelte";
import { store } from "$lib/store.svelte";
import { nav } from "@mobile/lib/nav.svelte";
import { bridge } from "@mobile/wailsshim/bridge";
import { ChannelTransport } from "@mobile/wailsshim/channeltransport";
import { FRAME_RESP, FakeChannel, type Frame } from "@mobile/wire";
import type { ProjectInfo } from "@bindings/internal/protocol";

// The PR picker, driven over the ORDINARY request path: the same bridge, the
// same ChannelTransport and the same correlator the app uses on a device, with
// only the bytes faked. This is PaneTabs.test.ts's shape and it is chosen for
// the same reason — a test that stubbed `DaemonService` would pass with a shim
// that sends the wrong command or the wrong argument envelope, which is exactly
// the class of bug a picker can have without anything looking wrong on screen.
//
// Nothing here is a screenshot. What the list LOOKS like is checked on the
// Simulator; what it DOES — which command goes out, what a tap sends, what a
// refusal says, and when the staleness notice appears — is checked here, where
// there is no device and no pointer.

let ch: FakeChannel;

/** What the scripted daemon answers, per command. */
type Reply = { ok: true; data: unknown } | { ok: false; error: string };
let replies: Record<string, Reply>;

/** Every `req` frame this test produced, in order. */
function reqs(): Frame[] {
  return ch.sent.filter((f) => f.type === "req");
}

/** The payload of the first request for a command, as a plain object. */
function payloadOf(cmd: string): Record<string, unknown> | undefined {
  const f = reqs().find((r) => r.cmd === cmd);
  return f?.payload as Record<string, unknown> | undefined;
}

function pr(over: Record<string, unknown> = {}) {
  return {
    number: 12,
    title: "Fix the flaky dispatch test",
    author: "alice",
    branch: "fix/dispatch",
    isDraft: false,
    isFork: false,
    checks: "pass",
    review: "APPROVED",
    url: "https://example.test/pr/12",
    status: "approved",
    alreadyOpen: false,
    ...over,
  };
}

/** A PrsData reply carrying the given rows. */
function prsData(prs: unknown[], over: Record<string, unknown> = {}) {
  return { repo: "sushidev-team/nori", prs, ageSeconds: 5, stale: false, ...over };
}

function project(over: Partial<ProjectInfo> = {}): ProjectInfo {
  return {
    name: "nori-app",
    label: "Nori",
    group: "",
    path: "/Volumes/Git/nori",
    repo: "sushidev-team/nori",
    defaultBranch: "main",
    agent: "claude",
    agentBin: "claude",
    agentOk: true,
    pathOk: true,
    repoConfigured: true,
    pollCount: 1,
    pollsEnabled: 1,
    lastRun: "",
    sessions: 0,
    liveCounted: 0,
    needsYou: 0,
    ciRed: 0,
    openPrs: 1,
    ...over,
  } as unknown as ProjectInfo;
}

beforeEach(async () => {
  replies = {};
  ch = new FakeChannel();
  ch.onSend = (f: Frame) => {
    if (f.type !== "req") return;
    const r = replies[String(f.cmd)] ?? { ok: true, data: {} };
    ch.deliver({ v: 1, type: FRAME_RESP, id: f.id, payload: r });
  };
  const t = new ChannelTransport({ open: async () => ch });
  await t.connect({ host: "127.0.0.1", spkiPin: "pin" });
  bridge.installTransport(t);

  store.projects = [project()];
  nav.project = "nori-app";
  nav.pick = "prs";
  nav.tab = "projects";
  nav.query = "";
  nav.triage = "";
});

afterEach(() => bridge.installTransport(null));

describe("PRPicker", () => {
  it("asks the daemon for the drilled-into project's open PRs and lists them", async () => {
    replies.prs = { ok: true, data: prsData([pr(), pr({ number: 34, title: "Rework the observer", author: "bob", branch: "chore/observer", status: "ci_failed" })]) };
    render(PRPicker);

    // The row's own facts, all four of them: a phone row carries the number,
    // the title, the author and the branch without the title giving way.
    expect(await screen.findByText("Fix the flaky dispatch test")).toBeTruthy();
    expect(screen.getByText("Rework the observer")).toBeTruthy();
    expect(screen.getByText("#12")).toBeTruthy();
    expect(screen.getByText("alice")).toBeTruthy();
    expect(screen.getByText("fix/dispatch")).toBeTruthy();
    expect(screen.getByText("bob")).toBeTruthy();
    expect(screen.getByText("chore/observer")).toBeTruthy();

    // The command, and the project it names. `refresh: false` on a first load —
    // the daemon's TTL cache is the point, and a picker that bypassed it on
    // every open would exec `gh` for each tap on the detail screen.
    expect(payloadOf("prs")).toEqual({ cmd: "prs", args: { project: "nori-app", refresh: false } });
  });

  it("says the status in the SHARED vocabulary, not a local one", async () => {
    // `status` is state.DeriveDelivery run on the daemon, and `statusLabel` is
    // the port of Go's internal/state that desktop/state_parity_test.go pins.
    // A second word list on the phone is the drift this asserts against.
    replies.prs = { ok: true, data: prsData([pr({ status: "ci_failed" })]) };
    render(PRPicker);
    expect(await screen.findByText("ci failed")).toBeTruthy();
  });

  it("marks the rows whose tap the daemon is likely to refuse", async () => {
    replies.prs = {
      ok: true,
      data: prsData([
        pr({ number: 12, isDraft: true }),
        pr({ number: 34, title: "From a fork", branch: "fork/thing", isFork: true }),
        pr({ number: 56, title: "Already running", branch: "held/branch", alreadyOpen: true }),
      ]),
    };
    render(PRPicker);

    expect(await screen.findByText("draft")).toBeTruthy();
    expect(screen.getByText("fork")).toBeTruthy();
    expect(screen.getByText("already open")).toBeTruthy();
  });

  it("launches an agent on the tapped row's own fields, then leaves for the filtered list", async () => {
    replies.prs = { ok: true, data: prsData([pr(), pr({ number: 34, title: "From a fork", branch: "feat/fork", isFork: true })]) };
    replies.openPr = { ok: true, data: { sessionId: "nori-app-12", message: "opened #12" } };
    render(PRPicker);

    await fireEvent.click(await screen.findByRole("button", { name: /From a fork/ }));

    // THE ROW'S OWN FIELDS, not the first row's and not a recomputed branch:
    // the branch is what the worktree tracks and `isFork` is what the daemon
    // refuses on, so a picker that sent either from the wrong row would put an
    // agent on someone else's work.
    await waitFor(() => expect(payloadOf("openPr")).toBeTruthy());
    expect(payloadOf("openPr")).toEqual({
      cmd: "openPr",
      args: { project: "nori-app", branch: "feat/fork", number: 34, isFork: true },
    });

    // The picker closes with the navigation — it belongs to the detail it was
    // opened over — and the sessions list is filtered to the project by NAME,
    // the identity every session carries in its `project` field.
    await waitFor(() => expect(nav.tab).toBe("sessions"));
    expect(nav.pick).toBe("");
    expect(nav.query).toBe("nori-app");
    expect(nav.triage).toBe("");
  });

  it("keeps the picker and shows the daemon's own sentence when a tap is refused", async () => {
    replies.prs = { ok: true, data: prsData([pr()]) };
    replies.openPr = {
      ok: false,
      error: "fix/dispatch is already open in nori-app (session nori-app-9) — kill it first",
    };
    render(PRPicker);

    await fireEvent.click(await screen.findByRole("button", { name: /Fix the flaky dispatch test/ }));

    // VERBATIM. The refusal names the session holding the branch, which is the
    // only half a person can act on; a composed "could not open this PR" throws
    // it away.
    expect(
      await screen.findByText(/already open in nori-app \(session nori-app-9\)/),
    ).toBeTruthy();
    // And the screen stays put: the list is still there and the picker is still
    // the place the app is in.
    expect(nav.pick).toBe("prs");
    expect(nav.tab).toBe("projects");
    expect(screen.getByText("Fix the flaky dispatch test")).toBeTruthy();
  });

  it("says a list is a cached snapshot ONLY when the daemon says it is stale", async () => {
    // A stale list drawn as a fresh one is the bug the flag exists to prevent:
    // the PR you tap may be merged and the one you wanted may not be listed.
    replies.prs = { ok: true, data: prsData([pr()], { stale: false, ageSeconds: 5 }) };
    const fresh = render(PRPicker);
    await screen.findByText("Fix the flaky dispatch test");
    expect(screen.queryByText(/Cached list from/)).toBeNull();
    // The age is still stated — freshness is a fact on every list, not only a
    // stale one.
    expect(screen.getByText(/5s ago/)).toBeTruthy();
    fresh.unmount();

    replies.prs = { ok: true, data: prsData([pr()], { stale: true, ageSeconds: 240 }) };
    render(PRPicker);
    expect(await screen.findByText(/Cached list from 4m ago/)).toBeTruthy();
  });

  it("re-asks with refresh set, which is what bypasses the TTL", async () => {
    replies.prs = { ok: true, data: prsData([pr()], { stale: true, ageSeconds: 240 }) };
    render(PRPicker);
    await screen.findByText("Fix the flaky dispatch test");

    const before = reqs().filter((f) => f.cmd === "prs").length;
    await fireEvent.click(screen.getAllByRole("button", { name: "Refresh" })[0]);

    await waitFor(() => expect(reqs().filter((f) => f.cmd === "prs").length).toBe(before + 1));
    const last = reqs().filter((f) => f.cmd === "prs").pop();
    expect((last?.payload as Record<string, unknown>).args).toEqual({
      project: "nori-app",
      refresh: true,
    });
  });

  it("tells the three emptinesses apart", async () => {
    // 1. Nothing open. Everything worked; the repository has no open PRs.
    replies.prs = { ok: true, data: prsData([]) };
    const none = render(PRPicker);
    expect(await screen.findByText("No open pull requests")).toBeTruthy();
    none.unmount();

    // 2. A gh failure. Nothing on the phone can fix it, so it is named exactly
    //    as the daemon worded it rather than summarised away.
    replies.prs = { ok: false, error: "gh: could not determine the current repository" };
    const broken = render(PRPicker);
    expect(await screen.findByText("Couldn't list the pull requests")).toBeTruthy();
    expect(screen.getByText(/could not determine the current repository/)).toBeTruthy();
    broken.unmount();

    // 3. No repository configured. A configuration fact, read from the
    //    project's own `repoConfigured` rather than from the wording of an
    //    error — and no request is sent at all, because none could succeed.
    store.projects = [project({ repoConfigured: false })];
    ch.sent.length = 0;
    render(PRPicker);
    expect(await screen.findByText("No repository configured")).toBeTruthy();
    expect(reqs().filter((f) => f.cmd === "prs")).toHaveLength(0);
  });

  it("shows a loading state rather than a blank screen while the Mac runs gh", () => {
    // The reply is withheld: a first load execs `gh pr list` on the Mac and can
    // take seconds, and an empty scroller for that long is indistinguishable
    // from a repository with no open PRs — which means something else entirely.
    ch.onSend = () => {};
    render(PRPicker);
    expect(screen.getByText("Loading pull requests…")).toBeTruthy();
  });

  it("gives every row the 44pt minimum height", async () => {
    replies.prs = { ok: true, data: prsData([pr()]) };
    render(PRPicker);
    const row = await screen.findByRole("button", { name: /Fix the flaky dispatch test/ });
    expect(row.className).toContain("tap-row");
  });
});
