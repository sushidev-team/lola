// What a session did to the code: the diff against its base and each turn, one place.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor } from "@testing-library/svelte";

const svc = vi.hoisted(() => ({
  Checkpoints: vi.fn(),
  CheckpointDiff: vi.fn(),
  Diff: vi.fn(),
  RestoreCheckpoint: vi.fn(),
  ForkCheckpoint: vi.fn(),
  SendFeedback: vi.fn(),
  Alive: vi.fn().mockResolvedValue(true),
  // A restore asks for a refresh, and an answer would replace this test's session list.
  Sessions: vi.fn(() => new Promise(() => {})),
  Projects: vi.fn(() => new Promise(() => {})),
  Status: vi.fn(() => new Promise(() => {})),
}));
vi.mock("@bindings/desktop", () => ({
  DaemonService: svc,
  ConfigService: {},
  SSHService: { Timeline: vi.fn().mockResolvedValue({ connections: null, items: [], lastSeq: 0 }), Hosts: vi.fn().mockResolvedValue({ hosts: [] }) },
  TermService: { Shells: vi.fn(async () => []) },
  TalkService: { State: vi.fn(async () => ({ caps: {}, composer: "unknown", feedSeq: 0, health: "ok" })) },
}));

const { default: ChangesView } = await import("./ChangesView.svelte");
const { default: ViewStrip } = await import("./ViewStrip.svelte");
const { default: SessionFooter } = await import("./SessionFooter.svelte");
const { default: Receipt } = await import("./Receipt.svelte");
const { store } = await import("$lib/store.svelte");
const { terms, DIFF, CHECKPOINTS, SSH } = await import("$lib/terms.svelte");
const { checkpoints } = await import("$lib/checkpoints.svelte");
const { confirm } = await import("$lib/confirm.svelte");
const { feedback } = await import("$lib/feedback.svelte");
const { changes } = await import("$lib/changes.svelte");

const LIST = {
  session: "s",
  restorable: true,
  checkpoints: [
    { seq: 1, label: "start", sha: "aaa", created: "2026-01-01T00:00:00Z" },
    { seq: 2, label: "turn 1 · Fix the redirect", sha: "bbb", created: "2026-01-01T00:05:00Z" },
    { seq: 3, label: "before restore #1", sha: "ccc", created: "2026-01-01T00:06:00Z" },
  ],
};
const data = (add: number, name = "a.go") => ({
  session: "s",
  base: "origin/main",
  mergeBase: "abcdef0123",
  files: [{ path: name, status: "modified", additions: add, deletions: 1, patch: "@@ -1 +1 @@\n-old\n+new\n" }],
});

let n = 0;
let SID = "";
beforeEach(() => {
  cleanup();
  confirm.cancel();
  SID = `chg${++n}`;
  store.alive = true;
  store.sessions = [{ id: SID, agent: "claude", agentState: "idle", worktree: "/w", tmuxName: SID, firstSeen: "2026-01-01T00:00:00Z" }] as never;
  svc.Checkpoints.mockReset().mockResolvedValue(LIST);
  svc.CheckpointDiff.mockReset().mockResolvedValue(data(7, "turn.go"));
  svc.Diff.mockReset().mockResolvedValue(data(42, "base.go"));
  svc.RestoreCheckpoint.mockReset().mockResolvedValue({ seq: 2, safety: 4, message: "restored" });
  svc.ForkCheckpoint.mockReset().mockResolvedValue({ sessionId: "fork-1", message: "forked" });
  svc.SendFeedback.mockReset().mockResolvedValue({ delivered: true, queued: false });
});

describe("ChangesView", () => {
  it("a checkpoint diff lights the Changes tab", async () => {
    const session = store.sessionById(SID)!;
    checkpoints.select(SID, 2);
    terms.select(SID, CHECKPOINTS);
    render(ViewStrip, { session });
    expect(screen.getByRole("tab", { name: /^Changes/ })).toHaveAttribute("aria-selected", "true");
    terms.select(SID, DIFF);
    await waitFor(() => expect(screen.getByRole("tab", { name: /^Changes/ })).toHaveAttribute("aria-selected", "true"));
  });

  it("receipt link selects its turn", async () => {
    render(Receipt, { sessionId: SID, words: "Done · 2m", seq: 2 });
    await fireEvent.click(screen.getByRole("button", { name: "View changes" }));
    expect(terms.activeTab(SID)).toBe(CHECKPOINTS);
    expect(checkpoints.of(SID).selected).toBe(2);
    // A second click must not close what the first one opened.
    await fireEvent.click(screen.getByRole("button", { name: "View changes" }));
    expect(terms.activeTab(SID)).toBe(CHECKPOINTS);
  });

  it("a receipt with no checkpoint goes to everything against the base", async () => {
    render(Receipt, { sessionId: SID, words: "Done · 2m" });
    await fireEvent.click(screen.getByRole("button", { name: "View changes" }));
    expect(terms.activeTab(SID)).toBe(DIFF);
  });

  it("restore copy names files only", async () => {
    terms.select(SID, CHECKPOINTS);
    checkpoints.select(SID, 2);
    render(ChangesView, { sessionId: SID });
    await waitFor(() => expect(document.querySelector("[data-restore-note]")).toHaveTextContent("Files only. The conversation and git history do not move."));
    await fireEvent.click(screen.getByRole("button", { name: "Restore…" }));
    expect(confirm.request?.title).toBe("Restore checkpoint #2?");
    expect(confirm.request?.body).toContain("Files only. The conversation and git history do not move.");
    expect(svc.RestoreCheckpoint).not.toHaveBeenCalled();
    confirm.accept();
    await waitFor(() => expect(svc.RestoreCheckpoint).toHaveBeenCalledWith(SID, 2));
  });

  it("lists turns newest first with lola's own markers named, and shows the base first", async () => {
    terms.select(SID, DIFF);
    render(ChangesView, { sessionId: SID });
    const list = await screen.findByRole("list", { name: "Turns" });
    await waitFor(() => expect(list.querySelectorAll("button")).toHaveLength(3));
    const rows = [...list.querySelectorAll("button")].map((b) => b.textContent ?? "");
    expect(rows[0]).toContain("before restore");
    expect(rows[1]).toContain("#2 · Fix the redirect");
    expect(rows[2]).toContain("start");
    await waitFor(() => expect(svc.Diff).toHaveBeenCalledWith(SID));
    expect(svc.CheckpointDiff).not.toHaveBeenCalled();
    expect(screen.queryByRole("button", { name: "Restore…" })).toBeNull();
    expect(screen.getByRole("button", { name: /All vs base/ })).toHaveAttribute("aria-current", "true");
  });

  it("a turn row shows that turn's diff, and All vs base comes back", async () => {
    terms.select(SID, DIFF);
    render(ChangesView, { sessionId: SID });
    const row = await screen.findByRole("button", { name: /#2/ });
    await fireEvent.click(row);
    await waitFor(() => expect(svc.CheckpointDiff).toHaveBeenCalledWith(SID, 2));
    expect(terms.activeTab(SID)).toBe(CHECKPOINTS);
    await screen.findByLabelText(/turn\.go/);
    await fireEvent.click(screen.getByRole("button", { name: /All vs base/ }));
    expect(terms.activeTab(SID)).toBe(DIFF);
    await screen.findByLabelText(/base\.go/);
  });

  it("the column stays when a turn changed nothing, so another can be picked", async () => {
    terms.select(SID, CHECKPOINTS);
    checkpoints.select(SID, 2);
    svc.CheckpointDiff.mockResolvedValue({ ...data(0), files: [] });
    render(ChangesView, { sessionId: SID });
    await screen.findByText("This turn changed no files.");
    expect(screen.getByRole("list", { name: "Turns" })).toBeInTheDocument();
  });

  it("disables restore while the agent is mid-turn, and fork stays", async () => {
    svc.Checkpoints.mockResolvedValue({ ...LIST, restorable: false });
    terms.select(SID, CHECKPOINTS);
    checkpoints.select(SID, 2);
    render(ChangesView, { sessionId: SID });
    const restore = await screen.findByRole("button", { name: "Restore…" });
    await waitFor(() => expect(checkpoints.of(SID).restorable).toBe(false));
    expect(restore).toBeDisabled();
    expect(screen.getByRole("button", { name: "Fork…" })).toBeEnabled();
  });

  it("forks from the selected turn", async () => {
    terms.select(SID, CHECKPOINTS);
    checkpoints.select(SID, 2);
    render(ChangesView, { sessionId: SID });
    await fireEvent.click(await screen.findByRole("button", { name: "Fork…" }));
    confirm.accept();
    await waitFor(() => expect(svc.ForkCheckpoint).toHaveBeenCalledWith(SID, 2, ""));
  });

  it("explains an empty list of turns", async () => {
    svc.Checkpoints.mockResolvedValue({ session: SID, restorable: true, checkpoints: [] });
    terms.select(SID, DIFF);
    render(ChangesView, { sessionId: SID });
    expect(await screen.findByText(/One is recorded each time the agent ends a turn/)).toBeInTheDocument();
  });

  it("a main-checkout session has no turns to ask about", async () => {
    store.sessions = [{ id: SID, agent: "claude", agentState: "idle", worktree: "/w", location: "main" }] as never;
    terms.select(SID, DIFF);
    render(ChangesView, { sessionId: SID });
    await waitFor(() => expect(svc.Diff).toHaveBeenCalled());
    expect(svc.Checkpoints).not.toHaveBeenCalled();
    expect(screen.queryByRole("list", { name: "Turns" })).toBeNull();
  });

  it("the footer offers the send only with something drafted, and names the agent", async () => {
    terms.select(SID, DIFF);
    render(SessionFooter, { sessionId: SID });
    expect(screen.queryByRole("button", { name: /Send to/ })).toBeNull();
    feedback.add(SID, { path: "a.go", side: "new", line: 1, endLine: 1, quote: "", body: "why" });
    const send = await screen.findByRole("button", { name: "Send to Claude" });
    expect(screen.getByText(/1 comment/)).toBeInTheDocument();
    await fireEvent.click(send);
    await waitFor(() => expect(svc.SendFeedback).toHaveBeenCalled());
  });

  it("the footer says Conversation ⌘\\ on Changes, and goes there", async () => {
    terms.select(SID, SSH);
    terms.select(SID, DIFF);
    render(SessionFooter, { sessionId: SID });
    await fireEvent.click(screen.getByRole("button", { name: /Conversation/ }));
    expect(terms.activeTab(SID)).toBe(SSH);
  });

  it("leaving Changes returns to where it was opened from, whichever half it ended on", () => {
    terms.select(SID, SSH);
    terms.select(SID, DIFF);
    terms.select(SID, CHECKPOINTS);
    terms.toggleDiff(SID);
    expect(terms.activeTab(SID)).toBe(SSH);
  });

  it("an edit row lands on its file, and a second press stays on Changes", async () => {
    const { default: ToolRow } = await import("./ToolRow.svelte");
    const row = { kind: "tool", id: "t1", ids: ["t1"], at: 0, gap: 0, verb: "Edit", target: "/w/base.go", status: "ok", tool: { name: "Edit", category: "edit", summary: "/w/base.go", status: "ok" }, item: { id: "t1", kind: "tool" } };
    terms.select(SID, SSH);
    render(ToolRow, { sessionId: SID, row: row as never });
    await fireEvent.click(screen.getByRole("button", { name: /Edited base.go/ }));
    expect(terms.activeTab(SID)).toBe(DIFF);
    expect(changes.wantedFile(SID)).toBe("/w/base.go");
    await fireEvent.click(screen.getByRole("button", { name: /Edited base.go/ }));
    expect(terms.activeTab(SID)).toBe(DIFF);
    terms.select(SID, SSH);
    changes.taken(SID);
  });

  it("feeds the strip's figure from the diff it already loaded", async () => {
    terms.select(SID, DIFF);
    render(ChangesView, { sessionId: SID });
    await waitFor(() => expect(changes.of(SID)).toEqual({ add: 42, del: 1 }));
    expect(svc.Diff).toHaveBeenCalledTimes(1);
  });
});

it('reopens a closed dock tab from the Panels menu', async () => {
  const { ssh } = await import('$lib/ssh.svelte');
  const { CHANGES_PANEL } = await import('$lib/sessionpanels');
  const { groupOf } = await import('$lib/docklayout');
  const view = ssh.view(SID);
  view.closeWidget(CHANGES_PANEL);
  render(ViewStrip, { session: store.sessionById(SID)!, changesInSide: true });
  await fireEvent.click(screen.getByRole('button', { name: 'Panels' }));
  await fireEvent.click(screen.getByRole('menuitemcheckbox', { name: 'Changes' }));
  expect(groupOf(view.dock, CHANGES_PANEL)?.active).toBe(CHANGES_PANEL);
  expect(view.dockClosed.has(CHANGES_PANEL)).toBe(false);
  expect(screen.queryByRole('menu', { name: 'Panels' })).toBeNull();
});

it('lists individual widgets in the icon menu and toggles their tabs without running them', async () => {
  const { ssh } = await import('$lib/ssh.svelte');
  const { groupOf } = await import('$lib/docklayout');
  const view = ssh.view(SID);
  view.items = [{ id: 'chart', kind: 'element', element: { id: 'chart', kind: 'widget', title: 'Coverage', html: '<b>Chart</b>' } }] as never;
  view.sessionPanels = true;
  view.ensureSessionPanels(true);
  render(ViewStrip, { session: store.sessionById(SID)!, changesInSide: true });
  const control = screen.getByRole('button', { name: 'Panels' });
  expect(control.querySelector('svg')).not.toBeNull();
  await fireEvent.click(control);
  expect(screen.queryByRole('menuitemcheckbox', { name: 'Widgets' })).toBeNull();
  expect(screen.getByRole('menuitemcheckbox', { name: 'Coverage' })).toHaveAttribute('aria-checked', 'true');
  await fireEvent.click(screen.getByRole('menuitemcheckbox', { name: 'Coverage' }));
  expect(groupOf(view.dock, 'chart')).toBeUndefined();
  view.syncDock();
  expect(groupOf(view.dock, 'chart')).toBeUndefined();
  await fireEvent.click(control);
  expect(screen.getByRole('menuitemcheckbox', { name: 'Coverage' })).toHaveAttribute('aria-checked', 'false');
  await fireEvent.click(screen.getByRole('menuitemcheckbox', { name: 'Coverage' }));
  expect(groupOf(view.dock, 'chart')?.active).toBe('chart');
  expect(view.widgetsAllowed).toBe(false);
  expect(document.activeElement).toBe(control);
});
