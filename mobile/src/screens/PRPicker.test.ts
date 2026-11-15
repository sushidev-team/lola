import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/svelte";
import { store } from "$lib/store.svelte";
import { ScriptedDaemon, project } from "@mobile/lib/projects.testkit";
import { router } from "@mobile/lib/router.svelte";
import { middleEllipsis } from "@mobile/lib/projects.svelte";
import PRPicker from "./PRPicker.svelte";

const d = new ScriptedDaemon();
const pr = (over: Record<string, unknown> = {}) => ({
  number: 12, title: "Fix the flaky test", author: "alice", branch: "fix/x", isDraft: false,
  isFork: false, checks: "pass", review: "", url: "", status: "ci_failed", alreadyOpen: false, ...over,
});
const prs = (rows: unknown[], over: Record<string, unknown> = {}) => ({
  ok: true as const,
  data: { repo: "sushidev-team/nori", prs: rows, ageSeconds: 5, stale: false, ...over },
});

beforeEach(async () => {
  await d.install();
  d.replies = {};
  store.projects = [project()];
  router.stack = [{ name: "tab", tab: "projects", project: "nori-app", pick: "prs" }];
});
afterEach(() => d.uninstall());

describe("PRPicker", () => {
  it("lists the project's PRs with the shared status word", async () => {
    d.replies.prs = prs([pr(), pr({ number: 34, title: "Rework", author: "bob", branch: "chore/y" })]);
    render(PRPicker);
    expect(await screen.findByText("Fix the flaky test")).toBeTruthy();
    expect(screen.getByText("#34")).toBeTruthy();
    expect(screen.getAllByText("CI failed").length).toBeGreaterThan(0);
    // The title is the page title; the repo/age line is gone.
    expect(screen.getByRole("heading", { name: "Pick a PR" })).toBeTruthy();
    expect(screen.queryByText(/sushidev-team\/nori/)).toBeNull();
    expect(d.payloadOf("prs")).toEqual({ cmd: "prs", args: { project: "nori-app", refresh: false } });
  });

  it("shows a bot author without [bot], capitalised, with no chip", async () => {
    d.replies.prs = prs([pr({ author: "dependabot[bot]" })]);
    render(PRPicker);
    expect(await screen.findByText("Dependabot")).toBeTruthy();
    expect(screen.queryByText(/\[bot\]/)).toBeNull();
    expect(screen.queryByText("Fork")).toBeNull();
  });

  it("marks draft and fork with Sentence-case pills and draws no open badge", async () => {
    d.replies.prs = prs([pr({ isDraft: true, isFork: true, alreadyOpen: true })]);
    render(PRPicker);
    expect(await screen.findByText("Draft")).toBeTruthy();
    expect(screen.getByText("Fork")).toBeTruthy();
    expect(screen.queryByText("open")).toBeNull();
  });

  it("keeps the first and last twelve characters of a long branch", async () => {
    const long = "feature/very-long-branch-name-that-keeps-going-forever";
    d.replies.prs = prs([pr({ branch: long })]);
    render(PRPicker);
    expect(await screen.findByText(middleEllipsis(long))).toBeTruthy();
    expect(middleEllipsis(long)).toBe("feature/very…oing-forever");
    expect(middleEllipsis("fix/x")).toBe("fix/x");
  });

  it("refreshes from a 44pt icon button, not a text button", async () => {
    d.replies.prs = prs([pr()]);
    render(PRPicker);
    await screen.findByText("Fix the flaky test");
    const btn = screen.getByRole("button", { name: "Refresh" });
    expect(btn.textContent?.trim()).toBe("");
    await fireEvent.click(btn);
    await waitFor(() => expect(d.payloadOf("prs")).toBeTruthy());
  });

  it("launches the tapped row and lands on its session", async () => {
    d.replies.prs = prs([pr({ number: 34, title: "From a fork", branch: "feat/fork", isFork: true })]);
    d.replies.openPr = { ok: true, data: { sessionId: "nori-app-34" } };
    render(PRPicker);
    await fireEvent.click(await screen.findByRole("button", { name: /From a fork/ }));
    await waitFor(() => expect(router.top).toMatchObject({ name: "session", id: "nori-app-34" }));
    expect(d.payloadOf("openPr")).toEqual({
      cmd: "openPr",
      args: { project: "nori-app", branch: "feat/fork", number: 34, isFork: true },
    });
  });

  it("keeps the list and shows the refusal verbatim", async () => {
    d.replies.prs = prs([pr()]);
    d.replies.openPr = { ok: false, error: "already open (session nori-app-9)" };
    render(PRPicker);
    await fireEvent.click(await screen.findByRole("button", { name: /Fix the flaky test/ }));
    expect(await screen.findByText(/session nori-app-9/)).toBeTruthy();
    expect(screen.getByText("Fix the flaky test")).toBeTruthy();
  });

  it("says a list is cached when the daemon reports it stale", async () => {
    d.replies.prs = prs([pr()], { stale: true });
    render(PRPicker);
    expect(await screen.findByText("Cached list.")).toBeTruthy();
  });

  it("asks nothing when the project has no repository", async () => {
    store.projects = [project({ repoConfigured: false })];
    render(PRPicker);
    expect(screen.getByText("No repo set.")).toBeTruthy();
    expect(d.count("prs")).toBe(0);
  });

  it("shows the daemon's sentence when the list fails and retries bypassing the cache", async () => {
    d.replies.prs = { ok: false, error: "gh is not authenticated" };
    render(PRPicker);
    expect(await screen.findByText("gh is not authenticated")).toBeTruthy();
    d.replies.prs = prs([]);
    await fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    await waitFor(() => expect(screen.getByText("No open PRs.")).toBeTruthy());
  });
});
