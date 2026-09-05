import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/svelte";
import SessionRow from "./SessionRow.svelte";
import type { SessionInfo } from "$lib/store.svelte";

// The row's WEIGHT, as behaviour rather than as a screenshot.
//
// Most of what is asserted here is NEGATIVE — the issue key must not lead the
// row, the status must not be a filled pill, an attention row must not draw a
// rail of its own — and a negative is exactly what a screenshot cannot pin. The
// one positive is the one that matters most: the words and the colours still
// come from `$lib/theme`, the port of Go's internal/state vocabulary. A
// phone-side re-spelling would be a third mirror of a list
// desktop/state_parity_test.go keeps in exactly two, so these tests name the
// shared classes literally.

function session(over: Partial<SessionInfo> = {}): SessionInfo {
  return {
    id: "nori-app-nor-401",
    project: "nori-app",
    issue: "NOR-401",
    title: "Email ingest: Add feature flag to hide it",
    status: "needs_input",
    agentState: "waiting_input",
    delivery: "",
    interpretedState: "",
    age: "2h31m",
    prNumber: 0,
    prUrl: "",
    checks: "",
    review: "",
    reacting: "",
    devActive: false,
    ...over,
  } as unknown as SessionInfo;
}

function mount(over: Partial<SessionInfo> = {}) {
  return render(SessionRow, {
    props: { session: session(over), projectLabel: "Nori App", onopen: () => {} },
  });
}

describe("SessionRow", () => {
  it("leads with the title and demotes the issue key into the meta line", () => {
    const { container } = mount();

    const heading = container.querySelector(".line-clamp-2");
    expect(heading).toBeTruthy();
    expect(heading!.textContent?.trim()).toBe("Email ingest: Add feature flag to hide it");
    // The heading is the row's ONE medium weight...
    expect(heading!.className).toContain("font-medium");
    // ...and the issue key is not in it.
    expect(heading!.textContent).not.toContain("NOR-401");

    // The key still exists, inside the small secondary line beside the project
    // and the age.
    const meta = container.querySelector(".text-sm.text-faint");
    expect(meta).toBeTruthy();
    expect(meta!.textContent).toContain("NOR-401");
    expect(meta!.textContent).toContain("Nori App");
    expect(meta!.textContent).toContain("2h31m");
  });

  it("weaves the status into the meta line in the shared theme colour", () => {
    const { container } = mount({ status: "needs_input" });

    // statusLabel("needs_input") === "needs you", statusText === "text-orange".
    const word = [...container.querySelectorAll("span")].find(
      (s) => s.textContent?.trim() === "needs you",
    );
    expect(word).toBeTruthy();
    expect(word!.className).toContain("text-orange");

    // It sits in the meta line, not on a line of its own above it.
    expect(container.querySelector(".text-sm.text-faint")!.textContent).toContain("needs you");
  });

  it("draws no pill fill for any status, dead included", () => {
    for (const status of ["needs_input", "ci_failed", "working", "dead", "review_pending"]) {
      const { container, unmount } = mount({ status });
      // pillClasses' five fills plus the `dead` special case. Any of them means
      // the badge is back.
      expect(container.innerHTML).not.toMatch(/bg-pill-|bg-bad\b/);
      unmount();
    }
  });

  it("dims a terminal status rather than shouting it", () => {
    const { container } = mount({ status: "dead", title: "" });
    const word = [...container.querySelectorAll("span")].find(
      (s) => s.textContent?.trim() === "dead",
    );
    expect(word).toBeTruthy();
    // theme.ts's bad family, at reduced weight because nothing is waiting on it.
    expect(word!.className).toContain("text-bad");
    expect(word!.className).toContain("opacity-55");

    // The design dims the whole row, not just the word: the title steps from
    // `ink` down to the meta tier, which is what makes a settled row read as
    // finished rather than merely quiet.
    const heading = container.querySelector(".line-clamp-2")!;
    expect(heading.className).toContain("text-faint");
    expect(heading.className).not.toContain("text-ink");
  });

  it("keeps the title at the heading's ink while anything is still waiting", () => {
    const { container } = mount({ status: "working" });
    const heading = container.querySelector(".line-clamp-2")!;
    expect(heading.className).toContain("text-ink");
  });

  it("promotes the issue key to the heading when there is no title, and does not repeat it", () => {
    const { container } = mount({ title: "" });

    const heading = container.querySelector(".line-clamp-2");
    expect(heading!.textContent?.trim()).toBe("NOR-401");

    // Exactly one occurrence in the whole row.
    const hits = container.textContent!.match(/NOR-401/g) ?? [];
    expect(hits).toHaveLength(1);
  });

  it("draws no attention rail of its own, for any status", () => {
    // This row used to carry ONE emphasis — a two-point orange border down the
    // left edge of a `needs_input` session, with the left padding widened to
    // absorb it. A session that needs a human now renders as a <SessionCard>
    // instead, so the treatment moved wholesale; leaving the rail here would
    // give the same state two different shapes on one screen. It is asserted
    // over the whole attention family, because the rail's own comment argued
    // that only `needs_input` deserved one and a future edit could reasonably
    // reach for the family instead.
    for (const status of ["needs_input", "ci_failed", "changes_requested", "merge_conflict"]) {
      const { container, unmount } = mount({ status });
      const row = container.querySelector("button")!;
      expect(row.className).not.toContain("border-l");
      // The hairline underneath is the redesign's soft token, not an opacity of
      // the panel border approximating it by hand.
      expect(row.className).toContain("border-edge-soft");
      expect(row.className).not.toContain("border-edge/40");
      unmount();
    }
  });

  it("marks the interpreter's disagreement as an approximation", () => {
    mount({ interpretedState: "waiting_input" });
    // statusLabel("waiting_input") === "waiting".
    expect(screen.getByText("≈waiting")).toBeTruthy();
  });

  it("keeps the issue key whole and lets the project be the thing that gives way", () => {
    // AT AN ACCESSIBILITY TEXT SIZE this line is far wider than the screen, and
    // it used to be the KEY that truncated — it and the project were the only
    // `min-w-0 truncate` items while the age was `shrink-0` — so the row read
    // "needs you · NO… · No… · 2h31m". That is backwards: the key is the
    // citation handle the row deliberately kept, and "NO…" is not a citation.
    // A class assertion is the honest test here; the truncation itself only
    // happens under a real layout, and a screenshot cannot pin a negative.
    const { container } = mount();
    const spans = [...container.querySelectorAll("span")];
    const key = spans.find((el) => el.textContent?.trim() === "NOR-401")!;
    const age = spans.find((el) => el.textContent?.trim() === "2h31m")!;
    const project = spans.find((el) => el.textContent?.trim() === "Nori App")!;

    expect(key.className).toContain("shrink-0");
    expect(key.className).not.toContain("truncate");
    expect(age.className).toContain("shrink-0");
    // The one item that may give way is the free-text one.
    expect(project.className).toContain("truncate");
    // ...and the line wraps rather than crushing anything when even that is not
    // enough.
    expect(key.parentElement!.className).toContain("flex-wrap");
  });

  it("draws no line of its own for a working agent with nothing to say", () => {
    // <AgentActivity> is `{#if live || text}` — pulse first, prose after — so a
    // mid-turn agent with no headline and no notification produced a block
    // containing nothing but a six-point dot, hanging between the title and the
    // facts. That is the commonest state in the list, and it read as a
    // rendering artefact. The pulse moved into the facts line; the prose keeps a
    // line only when there is prose.
    const { container } = mount({ agentState: "working", headline: "", lastNotification: "" });

    const pulse = container.querySelector(".animate-ping");
    expect(pulse).toBeTruthy();
    // The dot lives beside the status word now, on the wrapping facts line.
    expect(pulse!.closest(".flex-wrap")).toBeTruthy();
    // And the row is exactly two blocks: the heading and that line.
    const blocks = [...container.querySelector("button")!.children];
    expect(blocks).toHaveLength(2);
  });

  it("keeps the agent's own prose on its own line when there is any", () => {
    const { container } = mount({
      agentState: "working",
      headline: "rewriting the ingest tests",
    });
    // The interpreter's headline is an approximation and is marked as one, in
    // the pulse's info blue — AgentActivity's rules, applied to the half of it
    // this row still draws.
    const line = screen.getByText(/rewriting the ingest tests/);
    expect(line.textContent).toContain("≈");
    expect(line.className).toContain("text-info");
    expect([...container.querySelector("button")!.children]).toHaveLength(3);
  });

  it("steps the unnamed status family off the heading's ink", () => {
    // theme.ts answers `text-ink` for review_pending, draft and ci_pending —
    // right inside a pill, wrong without one. With the fill gone the word
    // printed in exactly the heading's ink, one size smaller, and read as
    // emphasis rather than as a state.
    const { container } = mount({ status: "review_pending" });
    const word = [...container.querySelectorAll("span")].find(
      (el) => el.textContent?.trim() === "review",
    )!;
    expect(word.className).toContain("text-faint");
    expect(word.className).not.toContain("text-ink");
  });
});
