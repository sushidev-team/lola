import { beforeEach, describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/svelte";
import Activity from "./Activity.svelte";
import { store } from "$lib/store.svelte";
import { statusText } from "$lib/theme";

// The event feed screen, which now draws its own rows rather than mounting the
// desktop's sidebar track. What is worth testing is what THIS file decides: what
// leads a row, what a row falls back to when the event carries no title, that an
// empty feed says so rather than showing a blank page, and that nothing here
// spells a status word of its own.
//
// The transition vocabulary is `$lib/theme`'s `eventPhrase` and `statusText` —
// the port of Go's internal/state that desktop/state_parity_test.go pins — so it
// is exercised, never re-spelled.

beforeEach(() => {
  store.activity = [];
});

describe("Activity", () => {
  it("takes the redesign's header: a large title over one line of subtitle", () => {
    const { container } = render(Activity);

    const h1 = screen.getByRole("heading", { name: "Activity" });
    expect(h1.tagName).toBe("H1");
    expect(h1.className).toContain("text-2xl");

    // The subtitle is a description rather than a count, and that is on
    // purpose: it costs a read of `store.activity` that this header does not
    // otherwise need, and the list below is the thing that counts.
    //
    // `text-body` and NOT the Sessions header's `text-base`, for that same
    // reason: the brief's scale puts a hint one step under a facts line, and
    // this is a hint. The three tab screens agree about it.
    const sub = container.querySelector("header span")!;
    expect(sub.className).toContain("text-body");
    expect(sub.textContent).toContain("newest first");
  });

  it("renders the daemon's events, newest first as the store holds them", () => {
    store.activity = [
      { id: "nori-app-nor-401", issue: "NOR-401", from: "working", to: "needs_input", ago: "2m" },
      { id: "nori-app-nor-329", issue: "NOR-329", from: "", to: "working", ago: "9m" },
    ];
    render(Activity);

    // The phrasing is theme.ts's `eventPhrase`, the shared vocabulary — this
    // screen names no transitions of its own.
    expect(screen.getByText("needs you")).toBeTruthy();
    expect(screen.getByText("spawned")).toBeTruthy();
    expect(screen.getByText("NOR-401")).toBeTruthy();
    expect(screen.getByText("2m")).toBeTruthy();
  });

  it("leads a row with the issue title, and the transition under it", () => {
    // The rail had no width for a title, so a row said which ticket changed and
    // not what changed. The event has carried one all along.
    store.activity = [
      {
        id: "nori-app-nor-401",
        issue: "NOR-401",
        title: "Email ingest: add a feature flag",
        from: "working",
        to: "needs_input",
        ago: "2m",
      },
    ];
    render(Activity);

    const title = screen.getByText("Email ingest: add a feature flag");
    expect(title.className).toContain("text-base");
    // The transition takes the colour of the state it ARRIVED at, from the
    // shared table — asserted through statusText rather than against a literal.
    expect(screen.getByText("needs you").className).toContain(statusText("needs_input"));
  });

  it("lets the transition carry a row that has no title", () => {
    // An adopted session, or one the daemon knew about before Linear answered.
    // The alternative is a row led by a blank line.
    store.activity = [
      { id: "nori-app-nor-401", issue: "NOR-401", from: "", to: "working", ago: "9m" },
    ];
    render(Activity);

    const heading = screen.getByText("spawned");
    expect(heading.className).toContain("text-base");
    // ...and it is not ALSO repeated on the facts line under itself.
    expect(screen.getAllByText("spawned")).toHaveLength(1);
  });

  it("falls back to a short id for an event with no issue key", () => {
    store.activity = [
      { id: "nori-app-nor-401", issue: "", title: "A session", from: "", to: "working", ago: "9m" },
    ];
    render(Activity);
    expect(screen.getByText("nori-app")).toBeTruthy();
  });

  it("shows every event the daemon sent, uncapped", () => {
    // The rail sliced to 30 because rows nobody can scroll to are retained
    // memory. A full-height scroller is the case where they can be.
    store.activity = Array.from({ length: 45 }, (_, i) => ({
      id: `s-${i}`,
      issue: `NOR-${i}`,
      title: `Event ${i}`,
      from: "working",
      to: "merged",
      ago: "1m",
    }));
    const { container } = render(Activity);
    expect(container.querySelectorAll("li")).toHaveLength(45);
  });

  it("says the feed is empty rather than showing nothing", () => {
    // A blank screen is indistinguishable from one that failed to load, and
    // this app has no recovery action to offer either way.
    render(Activity);
    expect(screen.getByText(/no activity yet/i)).toBeTruthy();
  });

  it("does not pay the bottom safe-area inset — the tab bar does", () => {
    const { container } = render(Activity);
    const html = container.innerHTML;
    // A screen that paid it too would leave a band of canvas between the last
    // event and a bar already clear of the home indicator.
    expect(html).not.toContain("safe-area-inset-bottom");
    expect(html).not.toContain("pb-safe-b");
  });
});
