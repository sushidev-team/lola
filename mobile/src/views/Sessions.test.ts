import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/svelte";
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

  it("offers exactly two header controls, each naming its own subject", () => {
    render(Sessions);
    // "Disconnect" is no longer a bare word in the header; the settings icon is
    // what stands there, and it says which kind of settings.
    expect(screen.queryByRole("button", { name: "Disconnect" })).toBeNull();
    expect(
      screen.getByRole("button", { name: /^Connection settings/ }),
    ).toBeTruthy();
    expect(screen.getByRole("button", { name: "Filters" })).toBeTruthy();
  });

  it("names the Mac in the settings control, before it is opened", () => {
    // A gear beside a funnel reads as list or display settings, so the subject
    // of this control only became visible once the sheet was open — "Connected
    // to <host>" is in there and nowhere else. The name carries it now, which
    // is the half a VoiceOver user got from nothing at all.
    render(Sessions);
    expect(
      screen.getByRole("button", {
        name: "Connection settings — connected to 192.168.1.20",
      }),
    ).toBeTruthy();
  });

  it("keeps the header's filter summary to one line", () => {
    // The subtitle concatenates a count, a bucket title and the raw search term
    // in quotes. It had no truncation while the title above it did, so a long
    // search term wrapped and GREW the header — pushing down the list that
    // moving the filters behind a button was supposed to give room to.
    nav.query =
      "a search term long enough to wrap the header on any phone ever sold";
    render(Sessions);
    const summary = screen.getByText(/of 3 sessions/);
    expect(summary.className).toContain("truncate");
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
    await fireEvent.click(screen.getByRole("button", { name: /^Needs You/ }));

    expect(nav.triage).toBe("Needs You");
    expect(screen.getByText("Email ingest")).toBeTruthy();
    expect(screen.queryByText("Template library")).toBeNull();
  });

  it("shows that a filter is active, in the button's name and in the count", async () => {
    render(Sessions);
    // Unfiltered: the plain total, and a plain button name.
    expect(screen.getByText(/3 sessions/)).toBeTruthy();

    await fireEvent.click(screen.getByRole("button", { name: "Filters" }));
    await fireEvent.click(screen.getByRole("button", { name: /^Needs You/ }));
    await fireEvent.click(screen.getByRole("button", { name: "Done" }));

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

  it("opens the connection settings when nav says so", () => {
    nav.sheet = "connection";
    render(Sessions);
    expect(
      screen.getByRole("dialog", { name: "Connection settings" }),
    ).toBeTruthy();
  });
});

describe("Sessions settings menu", () => {
  it("reaches Disconnect, and the control names the Mac it leaves", async () => {
    render(Sessions);
    await fireEvent.click(
      screen.getByRole("button", { name: /^Connection settings/ }),
    );

    const sheet = screen.getByRole("dialog", { name: "Connection settings" });
    expect(sheet).toBeTruthy();
    // Not a bare "Disconnect": the ambiguity about what these controls act on is
    // the bug this screen was fixing, and a menu does not cure it on its own.
    expect(
      screen.getByRole("button", { name: "Disconnect from 192.168.1.20" }),
    ).toBeTruthy();
    expect(
      screen.getByRole("button", { name: "Forget this Mac" }),
    ).toBeTruthy();
  });

  it("disconnects and returns to the pairing screen", async () => {
    const disconnect = vi
      .spyOn(connection, "disconnect")
      .mockResolvedValue(undefined);
    const forget = vi.spyOn(connection, "forget").mockResolvedValue(undefined);

    render(Sessions);
    await fireEvent.click(
      screen.getByRole("button", { name: /^Connection settings/ }),
    );
    await fireEvent.click(
      screen.getByRole("button", { name: "Disconnect from 192.168.1.20" }),
    );

    expect(disconnect).toHaveBeenCalledTimes(1);
    expect(forget).not.toHaveBeenCalled();
    expect(nav.screen).toBe("connect");
  });

  it("forgetting removes the key before it disconnects", async () => {
    const calls: string[] = [];
    vi.spyOn(connection, "disconnect").mockImplementation(async () => {
      calls.push("disconnect");
    });
    vi.spyOn(connection, "forget").mockImplementation(async () => {
      calls.push("forget");
    });

    render(Sessions);
    await fireEvent.click(
      screen.getByRole("button", { name: /^Connection settings/ }),
    );
    await fireEvent.click(
      screen.getByRole("button", { name: "Forget this Mac" }),
    );

    expect(calls).toEqual(["forget", "disconnect"]);
  });

  it("closes without leaving", async () => {
    const disconnect = vi
      .spyOn(connection, "disconnect")
      .mockResolvedValue(undefined);
    render(Sessions);

    await fireEvent.click(
      screen.getByRole("button", { name: /^Connection settings/ }),
    );
    await fireEvent.click(screen.getByRole("button", { name: "Cancel" }));

    expect(
      screen.queryByRole("dialog", { name: "Connection settings" }),
    ).toBeNull();
    expect(disconnect).not.toHaveBeenCalled();
  });
});

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
    await screen.findByRole("button", { name: /Connection settings/ });
    expect(screen.queryByRole("button", { name: /reconnect/i })).toBeNull();
  });
});
