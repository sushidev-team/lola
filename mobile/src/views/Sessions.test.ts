import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, within } from "@testing-library/svelte";
import Sessions from "./Sessions.svelte";
import { store } from "$lib/store.svelte";
import { connection } from "@mobile/lib/connection.svelte";
import { nav } from "@mobile/lib/nav.svelte";
import type { SessionInfo } from "$lib/store.svelte";

// The header, as behaviour.
//
// Everything this screen reads is a module singleton with `$state` fields —
// `store`, `connection`, `nav` — so the fixture is assignment rather than a mock
// layer. That is deliberate: a mocked store would let the header pass while the
// real one has renamed a field underneath it.

function s(over: Partial<SessionInfo> = {}): SessionInfo {
  return {
    id: "nori-app-nor-401",
    project: "nori-app",
    issue: "NOR-401",
    title: "Email ingest",
    status: "needs_input",
    agentState: "",
    delivery: "",
    interpretedState: "",
    age: "2h",
    prNumber: 0,
    ...over,
  } as unknown as SessionInfo;
}

beforeEach(() => {
  store.sessions = [
    s({ id: "a", issue: "NOR-401", status: "needs_input" }),
    s({
      id: "b",
      issue: "NOR-329",
      status: "review_pending",
      title: "Template library",
    }),
    s({ id: "c", issue: "NOR-311", status: "dead", title: "" }),
  ];
  store.alive = true;
  store.connected = true;
  // The header names the Mac from `cmd=status`, falling back to
  // connection.label. Null here, so the fallback is what the header tests see.
  store.status = null;
  connection.phase = "ready";
  connection.host = "192.168.1.20";
  connection.hasStoredKey = true;
  nav.triage = "";
  nav.query = "";
  nav.screen = "sessions";
  nav.sheet = "";
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("Sessions header", () => {
  it("has no Refresh control at all", () => {
    render(Sessions);
    expect(screen.queryByRole("button", { name: /refresh/i })).toBeNull();
  });

  it("offers exactly one header control, and it names its own subject", () => {
    render(Sessions);
    // Two, until the Mac button went to the Settings tab. "Disconnect" was a
    // bare word in this header before that, which is the original bug both
    // moves were undoing: a control whose subject a reader has to infer.
    expect(screen.queryByRole("button", { name: "Disconnect" })).toBeNull();
    expect(screen.queryByRole("button", { name: /^Connection settings/ })).toBeNull();
    expect(screen.getByRole("button", { name: "Filters" })).toBeTruthy();
  });

  it("keeps the header's summary to one line", () => {
    // A header that GROWS pushes down the list it heads, which is the opposite
    // of what the redesign gave this screen the room for. The free text on this
    // line used to be the search term; the rail took the bucket name and the
    // sheet kept the term, so what is left that can be arbitrarily long is the
    // name the daemon reports for itself — which crosses a network and is only
    // bounded at 40 characters.
    store.status = {
      runtimeOk: true,
      linearOk: true,
      polls: null,
      host: "a-machine-name-long-enough-to-wrap-any-phone-header",
    } as unknown as (typeof store)["status"];
    render(Sessions);
    const summary = screen.getByText(/3 sessions/);
    expect(summary.className).toContain("truncate");
  });

  it("names the daemon, preferring what it calls itself", () => {
    // Two sources and a documented order: the daemon's own answer to
    // `cmd=status` first, the connection's label — the address, or a name typed
    // on this phone — while that answer has not arrived.
    store.status = {
      runtimeOk: true,
      linearOk: true,
      polls: null,
      host: "marvin",
    } as unknown as (typeof store)["status"];
    render(Sessions);
    expect(screen.getByText("marvin")).toBeTruthy();
  });

  it("falls back to the connection's label, with no dangling separator", () => {
    render(Sessions);
    // `connection.host` is the fixture's address, so that is what a daemon too
    // old to name itself leaves on the line.
    expect(screen.getByText("192.168.1.20")).toBeTruthy();
  });

  it("opens and closes the filter overlay", async () => {
    render(Sessions);
    expect(screen.queryByRole("dialog", { name: "Filters" })).toBeNull();

    await fireEvent.click(screen.getByRole("button", { name: "Filters" }));
    expect(screen.getByRole("dialog", { name: "Filters" })).toBeTruthy();

    await fireEvent.click(screen.getByRole("button", { name: "Done" }));
    expect(screen.queryByRole("dialog", { name: "Filters" })).toBeNull();
  });

  it("filters the list from a chip in the overlay", async () => {
    render(Sessions);
    expect(screen.getByText("Email ingest")).toBeTruthy();
    expect(screen.getByText("Template library")).toBeTruthy();

    await fireEvent.click(screen.getByRole("button", { name: "Filters" }));
    // SCOPED TO THE SHEET, because the same bucket chip now exists twice on
    // screen: once in the always-visible <FilterRail> under the header, and
    // once inside this overlay, which still renders <TriageChips>. Both are
    // bound to `nav.triage` so they can never disagree, but an unscoped query
    // matches two buttons and fails. See the note in Sessions.svelte: the
    // sheet's copy is the redundant one.
    await fireEvent.click(
      within(screen.getByRole("dialog", { name: "Filters" })).getByRole(
        "button",
        { name: /^Needs You/ },
      ),
    );

    expect(nav.triage).toBe("Needs You");
    expect(screen.getByText("Email ingest")).toBeTruthy();
    expect(screen.queryByText("Template library")).toBeNull();
  });

  it("shows that a filter is active, in the button's name and in the count", async () => {
    render(Sessions);
    // Unfiltered: the plain total, and a plain button name.
    expect(screen.getByText(/3 sessions/)).toBeTruthy();

    await fireEvent.click(screen.getByRole("button", { name: "Filters" }));
    const sheet = screen.getByRole("dialog", { name: "Filters" });
    await fireEvent.click(
      within(sheet).getByRole("button", { name: /^Needs You/ }),
    );
    await fireEvent.click(within(sheet).getByRole("button", { name: "Done" }));

    // The name carries the state for anyone who cannot see the dot...
    expect(
      screen.getByRole("button", {
        name: "Filters active — showing Needs You",
      }),
    ).toBeTruthy();
    // ...and the subtitle names BOTH numbers, which is the guarantee: one row
    // under "1 of 3 sessions" can never be mistaken for a quiet morning.
    expect(screen.getByText(/1 of 3 sessions/)).toBeTruthy();
  });

  it("names a text search in the button too", async () => {
    render(Sessions);
    await fireEvent.click(screen.getByRole("button", { name: "Filters" }));
    await fireEvent.input(screen.getByLabelText("Search sessions"), {
      target: { value: "NOR-329" },
    });
    await fireEvent.click(screen.getByRole("button", { name: "Done" }));

    expect(
      screen.getByRole("button", {
        name: "Filters active — searching NOR-329",
      }),
    ).toBeTruthy();
    expect(screen.getByText(/1 of 3 sessions/)).toBeTruthy();
  });

  it("clears both filters in one tap from inside the overlay", async () => {
    nav.triage = "Needs You";
    nav.query = "NOR";
    render(Sessions);

    await fireEvent.click(
      screen.getByRole("button", { name: /^Filters active/ }),
    );
    await fireEvent.click(
      screen.getByRole("button", { name: "Clear filters" }),
    );

    expect(nav.triage).toBe("");
    expect(nav.query).toBe("");
    expect(screen.getByRole("button", { name: "Filters" })).toBeTruthy();
  });
});

describe("Sessions sheets are addressable", () => {
  // The filter overlay and the connection settings were reachable only by a
  // tap, and a tap is the one thing the Simulator cannot be asked to perform —
  // so a review of either had to be conducted from unit tests, with no picture
  // of the thing being reviewed. Naming the open sheet in `nav` is what lets a
  // development link land on one. See lib/sheets.ts and lib/devlink.ts.

  it("opens the filter overlay when nav says so", () => {
    nav.sheet = "filter";
    render(Sessions);
    expect(screen.getByRole("dialog", { name: "Filters" })).toBeTruthy();
  });

  it("has no connection control of its own any more", () => {
    // The header used to carry a Mac button opening a sheet with the
    // connected-to line, disconnect, forget and the nickname — a second door
    // onto what the Settings tab already held, kept in step by hand. Both are
    // gone; the tab is the one place a machine is managed.
    render(Sessions);
    expect(screen.queryByRole("button", { name: /^Connection settings/ })).toBeNull();
    expect(screen.queryByRole("dialog", { name: "Connection settings" })).toBeNull();
  });
});

// The Mac's own controls — disconnect, forget, the nickname — used to live in a
// sheet behind this header and are asserted in Settings.test.ts now. The header
// button and the sheet were both removed rather than shared: a Settings TAB is a
// place, and a place does not need a shortcut from another screen's header.

describe("the offline banner", () => {
  // A phone off the daemon's network sits behind this banner with the last
  // snapshot. The retry ladder runs on its own and on every foreground, but its
  // next attempt can be a minute away — and until this button existed the only
  // way to say "now" was to force-quit the app.
  it("offers a reconnect while the connection is down", async () => {
    connection.phase = "closed";
    connection.busy = false;
    connection.reconnecting = false;
    const spy = vi.spyOn(connection, "reconnect").mockResolvedValue(true);

    render(Sessions);
    const btn = await screen.findByRole("button", { name: "Reconnect" });
    await fireEvent.click(btn);

    expect(spy).toHaveBeenCalled();
    spy.mockRestore();
  });

  it("says it is working, and is disabled, while an attempt is in flight", async () => {
    // DISABLED rather than "the click does nothing": jsdom dispatches a click on
    // a disabled button, which a real browser suppresses, so asserting the
    // handler was not called would pass for the wrong reason on a device and
    // fail here.
    connection.phase = "closed";
    connection.reconnecting = true;

    render(Sessions);
    const btn = await screen.findByRole("button", { name: "Connecting…" });
    expect(btn).toBeDisabled();

    connection.reconnecting = false;
  });

  it("is absent once the connection is up", async () => {
    connection.phase = "ready";
    connection.reconnecting = false;
    render(Sessions);
    // Waits on the FILTER button, which is the header's only control now that
    // the Mac's moved to the Settings tab — it is a proxy for "the header has
    // rendered", nothing more.
    await screen.findByRole("button", { name: "Filters" });
    expect(screen.queryByRole("button", { name: /reconnect/i })).toBeNull();
  });
});

describe("the list is partitioned", () => {
  // The screen's one structural change: a flat, attention-first run became a
  // run of sections. What is asserted here is only what THIS file decides —
  // which sections exist, in what order, and which shape a row takes. The
  // membership rule itself belongs to `$lib/filters` and is pinned against Go
  // by desktop/state_parity_test.go; re-asserting it here would be a third,
  // staler copy of a partition the repository deliberately keeps in two.

  /** Every section heading on screen, in document order, whitespace-collapsed. */
  function headings(): string[] {
    return screen
      .getAllByRole("heading", { level: 2 })
      .map((h) => h.textContent!.replace(/\s+/g, " ").trim());
  }

  /**
   * Which shape a session was drawn as.
   *
   * `rounded-xl` is the hero card's panel and the only element on this screen
   * that takes it; the compact row is a bare button with a bottom hairline. The
   * classes are the discriminator because neither component exposes anything
   * else to ask — and both are transcribed geometry, so a test that spelled out
   * their paddings would just be a staler copy of the mock.
   */
  function shapeOf(name: RegExp): "card" | "row" {
    const btn = screen.getByRole("button", { name });
    return btn.querySelector(".rounded-xl") ? "card" : "row";
  }

  it("draws a section per non-empty bucket, in the design's order", () => {
    // Fixing before Working, which is NOT the kanban board's left-to-right: a
    // session whose delivered work regressed is nearer to needing a person than
    // one quietly mid-turn, and that is the order sortRank already puts them in.
    store.sessions = [
      s({ id: "a", issue: "NOR-401", status: "needs_input" }),
      s({ id: "b", issue: "NOR-402", status: "ci_failed", title: "Broken" }),
      s({ id: "c", issue: "NOR-403", status: "working", title: "Busy" }),
      s({ id: "d", issue: "NOR-404", status: "review_pending", title: "Parked" }),
      s({ id: "e", issue: "NOR-405", status: "merged", title: "Shipped" }),
    ];
    render(Sessions);

    expect(headings()).toEqual([
      "Needs You 1",
      "Fixing 1",
      "Working 1",
      "In Review 1",
      "Done 1",
    ]);
  });

  it("draws no heading for a bucket that holds nothing", () => {
    // A section for an empty bucket is a heading over a gap, and with five
    // buckets that is most of a phone screen spent on absence. The rail's chips
    // are where a zero is worth stating.
    store.sessions = [
      s({ id: "a", issue: "NOR-401", status: "needs_input" }),
      s({ id: "b", issue: "NOR-402", status: "needs_input", title: "Second" }),
    ];
    render(Sessions);

    expect(headings()).toEqual(["Needs You 2"]);
  });

  it("shows one bucket and its heading when the rail selects one", () => {
    nav.triage = "In Review";
    render(Sessions);

    expect(headings()).toEqual(["In Review 1"]);
    expect(screen.getByText("Template library")).toBeTruthy();
    expect(screen.queryByText("Email ingest")).toBeNull();
  });

  it("gives a session a human is blocked on the hero card", () => {
    render(Sessions);
    expect(shapeOf(/Email ingest/)).toBe("card");
  });

  it("gives everything else the compact row", () => {
    // Two rows rather than one: `review_pending` is parked on somebody else and
    // `dead` is over, and neither is a state a card's worth of screen buys
    // anything for.
    render(Sessions);
    expect(shapeOf(/Template library/)).toBe("row");
    expect(shapeOf(/NOR-311/)).toBe("row");
  });

  it("cards the broken family too, not just needs_input", () => {
    // `attention` spans both reasons a human is needed — blocked on a person,
    // or the delivered work regressed. Only the RAIL on the card narrows to
    // needs_input; the shape does not.
    store.sessions = [
      s({ id: "a", issue: "NOR-402", status: "ci_failed", title: "Broken" }),
    ];
    render(Sessions);
    expect(shapeOf(/Broken/)).toBe("card");
  });

  it("reads the axes when the daemon sends them", () => {
    // The rolled-up status word is the FALLBACK. A daemon that sends both axes
    // is answered from those, which is the whole point of the split: a working
    // agent over a red build is `working` in one word and "needs a person" in
    // two.
    store.sessions = [
      s({
        id: "a",
        issue: "NOR-402",
        status: "working",
        title: "Red build",
        agentState: "working",
        delivery: "ci_failed",
      }),
    ];
    render(Sessions);

    expect(headings()).toEqual(["Fixing 1"]);
    expect(shapeOf(/Red build/)).toBe("card");
  });
});
