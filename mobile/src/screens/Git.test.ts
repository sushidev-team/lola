import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/svelte";
import { store } from "$lib/store.svelte";
import { confirm } from "$lib/confirm.svelte";
import { ScriptedDaemon, project } from "@mobile/lib/projects.testkit";
import { router } from "@mobile/lib/router.svelte";
import { tiers } from "@mobile/lib/tiers.svelte";
import { FRAME_RESP, type Frame } from "@mobile/wire";
import Git from "./Git.svelte";

const d = new ScriptedDaemon();

const snapshot = (over: Record<string, unknown> = {}) => ({
  operation: "",
  remotes: [],
  path: "/Volumes/Git/nori",
  branch: "main",
  head: "abcdef0123456789",
  upstream: "origin/main",
  commits: [{ sha: "abcdef0123456789", parents: [], subject: "Add a thing", author: "Alice", email: "", date: "2026-01-01T00:00:00Z", refs: [] }],
  refs: [{ name: "main", fullName: "refs/heads/main", sha: "abcdef0123456789", remote: false, current: true, ahead: 2, behind: 0 }],
  worktrees: [{ path: "/Volumes/Git/nori", branch: "main", head: "abcdef0123456789", locked: false, prunable: false, blocked: false }],
  files: [
    { path: "src/a.ts", oldPath: "", index: "M", worktree: " ", untracked: false, conflict: false },
    { path: "src/b.ts", oldPath: "", index: " ", worktree: "M", untracked: false, conflict: false },
  ],
  signing: { enabled: false, format: "", key: "" },
  hasMore: false,
  ...over,
});

/** Answers by (cmd, op), because the three Git commands each carry several ops. */
function script(replies: Record<string, unknown>) {
  d.ch.onSend = (f: Frame) => {
    if (f.type !== "req") return;
    const args = ((f.payload as { args?: { op?: string } } | undefined)?.args ?? {}) as { op?: string };
    const data = replies[`${String(f.cmd)}:${args.op}`] ?? {};
    d.ch.deliver({ v: 2, type: FRAME_RESP, id: f.id, payload: { ok: true, data } });
  };
}
const ops = (cmd: string): Record<string, unknown>[] =>
  d.ch.sent.filter((f) => f.type === "req" && f.cmd === cmd).map((f) => (f.payload as { args: Record<string, unknown> }).args);

beforeEach(async () => {
  await d.install();
  tiers.set(["read", "control", "destructive"]);
  store.projects = [project()];
  router.stack = [{ name: "tab", tab: "projects", project: "nori-app", pick: "git" }];
  confirm.cancel();
});
afterEach(() => {
  tiers.clear();
  d.uninstall();
});

describe("Git screen", () => {
  it("loads the snapshot through projectGit and groups files as staged and unstaged", async () => {
    script({ "projectGit:snapshot": snapshot(), "projectGit:stashes": [] });
    render(Git);
    expect(await screen.findByText("main")).toBeTruthy();
    expect(await screen.findByText("a.ts")).toBeTruthy();
    expect(screen.getByText("b.ts")).toBeTruthy();
    expect(screen.getByText("2 ahead")).toBeTruthy();
    expect(ops("projectGit")[0]).toMatchObject({ op: "snapshot", project: "nori-app" });
  });

  it("stages a file with projectGitAct and never sends a credential", async () => {
    script({ "projectGit:snapshot": snapshot(), "projectGitAct:stage": [] });
    render(Git);
    await screen.findByText("b.ts");
    await fireEvent.click(screen.getByRole("button", { name: "Stage src/b.ts" }));
    await waitFor(() => expect(ops("projectGitAct")).toHaveLength(1));
    expect(ops("projectGitAct")[0]).toMatchObject({ op: "stage", project: "nori-app", paths: ["src/b.ts"] });
  });

  it("commits the staged files with the typed message", async () => {
    script({ "projectGit:snapshot": snapshot(), "projectGitAct:commit": {} });
    render(Git);
    await screen.findByText("a.ts");
    const commit = screen.getByRole("button", { name: "Commit" }) as HTMLButtonElement;
    expect(commit.disabled).toBe(true);
    await fireEvent.input(screen.getByLabelText("Commit message"), { target: { value: "Ship it" } });
    await waitFor(() => expect(commit.disabled).toBe(false));
    await fireEvent.click(commit);
    await waitFor(() => expect(ops("projectGitAct").some((a) => a.op === "commit")).toBe(true));
    expect(ops("projectGitAct").find((a) => a.op === "commit")).toMatchObject({ message: "Ship it" });
  });

  it("hides every write control from a device that holds only read", async () => {
    tiers.set(["read"]);
    script({ "projectGit:snapshot": snapshot() });
    render(Git);
    await screen.findByText("a.ts");
    expect(screen.queryByRole("button", { name: "Pull" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Stage all" })).toBeNull();
    expect(screen.queryByLabelText("Commit message")).toBeNull();
  });

  it("asks before dropping a stash, through projectGitDiscard", async () => {
    script({
      "projectGit:snapshot": snapshot(),
      "projectGit:stashes": [{ sha: "1111111111111111", selector: "stash@{0}", subject: "WIP", date: "2026-01-01T00:00:00Z" }],
      "projectGitDiscard:stashDrop": {},
    });
    render(Git);
    await screen.findByText("a.ts");
    await fireEvent.click(screen.getByRole("tab", { name: /Stashes/ }));
    await fireEvent.click(await screen.findByRole("button", { name: "Drop" }));
    expect(confirm.request?.title).toBe("Drop stash?");
    expect(ops("projectGitDiscard")).toHaveLength(0);
    confirm.accept();
    await waitFor(() => expect(ops("projectGitDiscard")).toHaveLength(1));
    expect(ops("projectGitDiscard")[0]).toMatchObject({ op: "stashDrop", sha: "1111111111111111" });
  });

  it("opens a commit's diff with area commit", async () => {
    script({
      "projectGit:snapshot": snapshot(),
      "projectGit:diff": { files: [{ path: "x.ts", additions: 1, deletions: 0, patch: "@@ -0,0 +1 @@\n+hi\n" }] },
    });
    render(Git);
    await screen.findByText("a.ts");
    await fireEvent.click(screen.getByRole("tab", { name: "History" }));
    await fireEvent.click(await screen.findByText("Add a thing"));
    await waitFor(() => expect(ops("projectGit").some((a) => a.op === "diff")).toBe(true));
    expect(ops("projectGit").find((a) => a.op === "diff")).toMatchObject({ area: "commit", to: "abcdef0123456789" });
  });
});
