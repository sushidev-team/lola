import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/svelte";
import SessionCard from "./SessionCard.svelte";
import type { SessionInfo } from "$lib/store.svelte";

// The card's JUDGEMENTS, rather than its pixels.
//
// Everything asserted here is something this component decides on its own and
// could therefore get wrong on its own: which status earns the rail, whether an
// absent PR draws anything, what leads the card when a title is missing, and
// that the agent's own prose reaches the DOM as text. The geometry — the
// paddings, the shadow, the 9px column gap — is transcribed from Figma and is
// not a decision, so it is not pinned; a test that spelled those out again
// would just be a second, staler copy of the mock.
//
// Two of the four are NEGATIVE (no rail, no badge), and a negative is exactly
// what a screenshot cannot catch.

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
  return render(SessionCard, {
    props: { session: session(over), projectLabel: "Nori App", onopen: () => {} },
  });
}

/** The 3px left rail. It is the only `bg-orange` the card ever draws. */
function rail(container: HTMLElement): Element | null {
  return container.querySelector(".bg-orange");
}

/** The card's heading — `text-lg` is the design's card-title step and this is
 *  the only element that takes it. */
function heading(container: HTMLElement): HTMLElement {
  return container.querySelector(".text-lg")!;
}

describe("SessionCard", () => {
  it("rails the card a human is blocked on, and only that one", () => {
    // The Figma draws the rail on the "waiting for you" card and leaves the
    // CI-failed card without one. That asymmetry is the whole assertion: it is
    // NARROWER than `$lib/theme`'s isAttention, which covers all four attention
    // statuses, so a future change that reached for the family predicate here
    // would put a rail on four cards in five and this test would catch it.
    expect(rail(mount({ status: "needs_input" }).container)).toBeTruthy();

    for (const status of ["ci_failed", "changes_requested", "merge_conflict"]) {
      const { container, unmount } = mount({ status });
      expect(rail(container)).toBeNull();
      unmount();
    }
    // ...and nothing quiet gets one either.
    for (const status of ["working", "review_pending", "merged", "dead"]) {
      const { container, unmount } = mount({ status });
      expect(rail(container)).toBeNull();
      unmount();
    }
  });

  it("draws nothing at all where there is no pull request", () => {
    // <PrBadge> prints a bare em-dash for a session with no PR, which is
    // unambiguous under a desktop table's column header and is just a mark on a
    // card that has none. The card must show absence as absence.
    const { container } = mount({ prNumber: 0 });
    expect(container.textContent).not.toContain("#");
    expect(container.textContent).not.toContain("—");
    expect(container.querySelector(".text-magenta")).toBeNull();
  });

  it("colours the badge from the checks rather than from the status word", () => {
    const passing = mount({ prNumber: 341, checks: "pass" });
    expect(screen.getByText("#341")).toBeTruthy();
    expect(passing.container.querySelector(".text-magenta")).toBeTruthy();
    passing.unmount();

    // A red number is the fastest way to see that the PR which exists is not
    // the PR you wanted — and the status here is deliberately NOT ci_failed, so
    // this can only be reading `checks`.
    const failing = mount({ prNumber: 341, checks: "fail", status: "needs_input" });
    expect(failing.container.querySelector(".text-bad")).toBeTruthy();
    expect(failing.container.querySelector(".text-magenta")).toBeNull();
  });

  it("renders the agent's headline as text, never as markup", () => {
    // `headline` and `lastNotification` are derived from pane text, which an
    // issue description or a dependency's README can write into (rule 6 of the
    // brief). Svelte's `{...}` escapes by construction — this test exists so
    // that a later "just let it render the bold bits" cannot land quietly.
    const hostile = '<img src=x onerror="alert(1)"> **bold**';
    const { container } = mount({ headline: hostile });

    expect(container.querySelector("img")).toBeNull();
    // The "≈" marks it as the interpreter's approximation, the same mark the
    // compact row and the TUI's statusPillFor use. The sentence itself survives
    // verbatim.
    expect(screen.getByText(`≈ ${hostile}`)).toBeTruthy();
  });

  it("falls back from the headline to the agent's own notification, unmarked", () => {
    // Same source and precedence as <AgentActivity>'s. A notification is the
    // agent's own words rather than an interpretation of them, so it carries no
    // "≈" — but it is just as untrusted, hence the same escaping.
    mount({ headline: "", lastNotification: "waiting for permission to run tests" });
    const line = screen.getByText("waiting for permission to run tests");
    expect(line.textContent).not.toContain("≈");
  });

  it("leads with the title and demotes the issue key below the rule", () => {
    const { container } = mount();

    expect(heading(container).textContent?.trim()).toBe(
      "Email ingest: Add feature flag to hide it",
    );
    expect(heading(container).textContent).not.toContain("NOR-401");
    // The key still exists, once, as the citation handle in the meta row.
    expect(container.textContent).toContain("NOR-401");
    expect(container.textContent).toContain("Nori App");
  });

  it("promotes the issue key to the heading when there is no title, and does not repeat it", () => {
    const { container } = mount({ title: "" });

    expect(heading(container).textContent?.trim()).toBe("NOR-401");
    // Exactly one occurrence: printing it twice with a rule between the copies
    // is the failure this guards.
    expect(container.textContent!.match(/NOR-401/g) ?? []).toHaveLength(1);
  });

  it("falls back to the session id when there is neither a title nor an issue", () => {
    // An adopted or manually created session has no Linear record at all. The
    // card still has to say which one it is, so the id's first twelve
    // characters lead — the same last resort the compact row takes.
    const { container } = mount({ title: "", issue: "" });
    expect(heading(container).textContent?.trim()).toBe("nori-app-nor");
  });

  it("states lola's remaining CI retry budget beside the status", () => {
    // The one fact on the card that is lola's own rather than the session's.
    // `$lib/reaction` filters the four outcomes that merely relabel the status;
    // this is one of the two that survive.
    const { container } = mount({ status: "ci_failed", reacting: "ci retry 1/2" });
    expect(screen.getByText("ci retry 1/2")).toBeTruthy();
    // Grey and dotless — progress, not news. Two loud chips side by side just
    // cancel each other out.
    expect(screen.getByText("ci retry 1/2").className).toContain("bg-pill-grey");
    expect(container.querySelector(".text-bad")).toBeNull();
  });

  it("spends the foreground, not a second loud ground, on an escalation", () => {
    // The retries are gone and it is a human's problem now. The chip keeps the
    // grey ground (the status chip beside it is already ci_failed and already
    // broken-toned) and says so in the bad colour instead. The trailing `!` is
    // what makes that override actually win — see the Button invariant.
    const chip = mount({ status: "ci_failed", reacting: "escalated" });
    const el = screen.getByText("escalated");
    expect(el.className).toContain("bg-pill-grey");
    expect(el.className).toContain("text-bad!");
    chip.unmount();
  });

  it("is one tap target, with no nested control inside it", () => {
    // A nested <button> does not parse: the parser closes the outer one and
    // takes the card's own tap with it. <MetaPill> becomes a button only when
    // given an `onclick`, so this asserts the card never passes one.
    const { container } = mount({ prNumber: 341, prUrl: "https://example.test/pr/341" });
    expect(container.querySelectorAll("button")).toHaveLength(1);
  });
});
