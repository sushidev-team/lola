import { describe, it, expect, beforeEach } from "vitest";
import { render, screen, cleanup } from "@testing-library/svelte";
import SessionsKanban from "./SessionsKanban.svelte";
import { store, type SessionInfo } from "$lib/store.svelte";
import { nav } from "$lib/nav.svelte";

function fakeSession(over: Partial<SessionInfo> = {}): SessionInfo {
  return {
    id: "acme-eng-1",
    issue: "ENG-1",
    title: "fix login",
    project: "acme",
    status: "",
    reacting: "",
    age: "2m",
    prNumber: 0,
    agentState: "working",
    delivery: "none",
    ...over,
  } as SessionInfo;
}

// The board buckets by BOTH axes now (state.KanbanKeyFor), not by a list of
// collapsed status words. A status list could not express "working agent, red
// CI" landing in Fixing while its agent axis stays visible on the card.
describe("SessionsKanban bucketing", () => {
  beforeEach(() => {
    cleanup();
    store.connected = true;
    store.alive = true;
    store.sessions = [];
    nav.scoped = false;
    nav.project = "";
    nav.selectedId = "";
    nav.triage = "";
  });

  // Each column head is its title beside a count; the count is the assertion.
  function countIn(container: HTMLElement, title: string): number {
    const head = [...container.querySelectorAll("span")].find((el) => el.textContent === title);
    const n = head?.nextElementSibling?.textContent ?? "";
    return Number(n);
  }

  it("files a blocked agent under Needs You whatever its PR is doing", () => {
    store.sessions = [fakeSession({ agentState: "waiting_input", delivery: "review_pending" })];
    const { container } = render(SessionsKanban);
    expect(countIn(container, "Needs You")).toBe(1);
    expect(countIn(container, "In Review")).toBe(0);
  });

  it("files a working agent with a red build under Fixing", () => {
    store.sessions = [fakeSession({ agentState: "working", delivery: "ci_failed" })];
    const { container } = render(SessionsKanban);
    expect(countIn(container, "Fixing")).toBe(1);
    expect(countIn(container, "Working")).toBe(0);
  });

  it("lets a terminal agent beat an open PR", () => {
    store.sessions = [fakeSession({ agentState: "exited", delivery: "ci_failed" })];
    const { container } = render(SessionsKanban);
    expect(countIn(container, "Done")).toBe(1);
    expect(countIn(container, "Fixing")).toBe(0);
  });

  it("still buckets a session that carries no axes, by its rolled-up word", () => {
    // Both axes are optional on the wire; a pre-split push must not pile every
    // row into Working.
    store.sessions = [
      { id: "x", issue: "ENG-9", project: "acme", status: "merged", prNumber: 0 } as SessionInfo,
    ];
    const { container } = render(SessionsKanban);
    expect(countIn(container, "Done")).toBe(1);
  });
});

describe("SessionsKanban card", () => {
  beforeEach(() => {
    cleanup();
    store.connected = true;
    store.alive = true;
    store.sessions = [];
    nav.scoped = false;
    nav.project = "";
    nav.selectedId = "";
    nav.triage = "";
  });

  it("names the agent axis in words rather than a two-letter glyph", () => {
    store.sessions = [fakeSession({ agentState: "idle", delivery: "review_pending" })];
    render(SessionsKanban);
    expect(screen.getByText("idle")).toBeInTheDocument();
    expect(screen.queryByText("rv")).not.toBeInTheDocument();
  });

  it("marks a delivery regression, which used to get no marker at all", () => {
    store.sessions = [fakeSession({ agentState: "working", delivery: "merge_conflict" })];
    const { container } = render(SessionsKanban);
    const marks = [...container.querySelectorAll("span")].filter((el) => el.textContent === "!");
    expect(marks).toHaveLength(1);
  });

  it("never nests a button inside the card — the card IS one", () => {
    // A nested <button> is not parseable: the parser closes the outer one and
    // the card stops being clickable. So PrBadge is rendered here WITHOUT its
    // open action.
    store.sessions = [fakeSession({ prNumber: 42, prUrl: "u", delivery: "draft" })];
    const { container } = render(SessionsKanban);
    expect(container.querySelector("button button")).toBeNull();
    expect(screen.getByText("#42")).toBeInTheDocument();
  });
});
