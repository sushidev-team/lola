import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/svelte";

import TicketPicker from "./TicketPicker.svelte";
import { store } from "$lib/store.svelte";
import { nav } from "@mobile/lib/nav.svelte";
import { ChannelTransport } from "@mobile/wailsshim/channeltransport";
import { bridge } from "@mobile/wailsshim/bridge";
import { FRAME_RESP, FakeChannel, type Frame } from "@mobile/wire";

// The ticket picker, driven over the ORDINARY request path: the same bridge,
// the same ChannelTransport and the same correlator the app uses on a device,
// with only the bytes faked. The alternative — stubbing `DaemonService` — would
// pass happily against a shim that sent the wrong command or the wrong args,
// which is precisely the class of bug this screen can have: it is one `tickets`
// read and one `openTicket` write, and everything else is presentation.
//
// Nothing here is a screenshot. What the list LOOKS like is checked on the
// Simulator; what it DOES — which issue a tap starts, what a refusal leaves on
// screen, and whether "no issues" and "Linear is unreachable" are told apart —
// is checked here, where there is no device and no pointer.

let ch: FakeChannel;

/** What the scripted daemon answers, per command. */
type Reply = { ok: true; data: unknown } | { ok: false; error: string };
let replies: Record<string, Reply>;

/**
 * Commands the scripted daemon receives and never answers, so a test can hold
 * the screen in its in-flight state. A request with no reply is exactly what a
 * slow Linear looks like from here.
 */
let held: Set<string>;

/** One issue row, with the fields the picker actually reads. */
function issue(over: Record<string, unknown> = {}) {
  return {
    identifier: "FE-9",
    uuid: "11111111-1111-1111-1111-111111111111",
    title: "fix the oauth callback",
    branch: "mr/fe-9-fix-the-oauth-callback",
    priority: 1,
    state: "In Progress",
    stateType: "started",
    assignee: "",
    labels: [],
    estimate: 0,
    updated: "2h05m",
    alreadyLive: false,
    ...over,
  };
}

/** A `tickets` reply carrying the given rows. */
function tickets(rows: ReturnType<typeof issue>[]) {
  return {
    ok: true as const,
    data: {
      team: "22222222-2222-2222-2222-222222222222",
      teamName: "Frontend",
      teamKey: "FE",
      issues: rows,
    },
  };
}

/** Every `req` frame this test produced, in order. */
function reqs(): Frame[] {
  return ch.sent.filter((f) => f.type === "req");
}

/** The payload of the last `req` frame for one command. */
function lastArgs(cmd: string): Record<string, unknown> | undefined {
  const f = [...reqs()].reverse().find((x) => x.cmd === cmd);
  const p = f?.payload as { args?: Record<string, unknown> } | undefined;
  return p?.args;
}

beforeEach(async () => {
  replies = {};
  held = new Set();
  ch = new FakeChannel();
  ch.onSend = (f: Frame) => {
    if (f.type !== "req") return;
    const cmd = String(f.cmd);
    if (held.has(cmd)) return;
    // Everything unscripted answers with an empty success — that covers the
    // `sessions`/`projects`/`status` reads `store.refresh()` makes after a
    // successful start, which this screen does not assert about.
    const r = replies[cmd] ?? { ok: true, data: {} };
    ch.deliver({ v: 1, type: FRAME_RESP, id: f.id, payload: r });
  };
  const t = new ChannelTransport({ open: async () => ch });
  await t.connect({ host: "127.0.0.1", spkiPin: "pin" });
  bridge.installTransport(t);

  store.projects = [];
  nav.tab = "projects";
  nav.project = "nori-app";
  nav.pick = "tickets";
  nav.query = "";
  nav.triage = "";
});

afterEach(() => bridge.installTransport(null));

describe("TicketPicker", () => {
  it("asks for the drilled-into project, in the daemon's default scope", async () => {
    // `nav.project` is a project NAME — identity in this repository, the thing
    // config and every session key by — so it is what goes on the wire. "mine"
    // is the daemon's own default and the app agrees with it explicitly rather
    // than sending "" and hoping.
    replies.tickets = tickets([issue()]);
    render(TicketPicker);

    await screen.findByText("fix the oauth callback");
    expect(lastArgs("tickets")).toEqual({ project: "nori-app", scope: "mine" });
  });

  it("lists what the daemon returned: the title, the identifier and the state", async () => {
    replies.tickets = tickets([
      issue({ identifier: "FE-9", title: "fix the oauth callback" }),
      issue({
        identifier: "FE-12",
        uuid: "33333333-3333-3333-3333-333333333333",
        title: "audit the session cookie",
        state: "Todo",
        stateType: "unstarted",
      }),
    ]);
    render(TicketPicker);

    expect(await screen.findByText("fix the oauth callback")).toBeTruthy();
    expect(screen.getByText("audit the session cookie")).toBeTruthy();
    expect(screen.getByText("FE-9")).toBeTruthy();
    expect(screen.getByText("FE-12")).toBeTruthy();
    // The workflow-state name is the TEAM's own text and is rendered as it came.
    expect(screen.getByText("In Progress")).toBeTruthy();
    expect(screen.getByText("Todo")).toBeTruthy();
  });

  it("re-asks in the team scope when the Team chip is tapped", async () => {
    // Both scopes are offered because a board that plans its backlog unassigned
    // would otherwise show an empty picker and describe a full board as empty.
    replies.tickets = tickets([issue()]);
    render(TicketPicker);
    await screen.findByText("fix the oauth callback");

    replies.tickets = tickets([
      issue({
        identifier: "FE-40",
        uuid: "44444444-4444-4444-4444-444444444444",
        title: "unassigned backlog item",
        assignee: "kim",
      }),
    ]);
    await fireEvent.click(screen.getByRole("button", { name: "Team" }));

    await screen.findByText("unassigned backlog item");
    expect(lastArgs("tickets")).toEqual({ project: "nori-app", scope: "team" });
    // The assignee only appears in the team scope: in "mine" every row would
    // name the same person.
    expect(screen.getByText("kim")).toBeTruthy();
  });

  it("starts the row's OWN issue, then leaves for the sessions list", async () => {
    // THE POINT OF THIS TEST is the pairing of identifier and uuid. They come
    // off the tapped row and nowhere else — the daemon dedups on the UUID and
    // names the session by the identifier, so a picker that sent the first row's
    // ids for the second row's tap would start the wrong issue and look right.
    replies.tickets = tickets([
      issue(),
      issue({
        identifier: "FE-12",
        uuid: "33333333-3333-3333-3333-333333333333",
        title: "audit the session cookie",
        branch: "mr/fe-12-audit-the-session-cookie",
      }),
    ]);
    replies.openTicket = {
      ok: true,
      data: { sessionId: "nori-app-fe-12", worktree: "/w", branch: "b", message: "started FE-12" },
    };
    render(TicketPicker);

    await screen.findByText("audit the session cookie");
    await fireEvent.click(screen.getByRole("button", { name: /FE-12/ }));

    await waitFor(() => expect(lastArgs("openTicket")).toBeDefined());
    expect(lastArgs("openTicket")).toEqual({
      project: "nori-app",
      identifier: "FE-12",
      uuid: "33333333-3333-3333-3333-333333333333",
      // Linear's own suggested branch rides along, so a phone-started session
      // lands on the same branch a desktop-started one would.
      branch: "mr/fe-12-audit-the-session-cookie",
      title: "audit the session cookie",
    });

    // Where it goes afterwards: the sessions list, narrowed to this project by
    // the same free-text query a project row sets — and with the picker closed,
    // so coming back to the Projects tab lands on the project's detail rather
    // than on a picker still listing the issue just started.
    await waitFor(() => expect(nav.tab).toBe("sessions"));
    expect(nav.query).toBe("nori-app");
    expect(nav.pick).toBe("");
  });

  it("keeps the picker and shows the daemon's sentence when a start is refused", async () => {
    replies.tickets = tickets([issue()]);
    replies.openTicket = {
      ok: false,
      error: "FE-9 is already being worked on — check sessions",
    };
    render(TicketPicker);

    await screen.findByText("fix the oauth callback");
    await fireEvent.click(screen.getByRole("button", { name: /FE-9/ }));

    // The daemon's own words, verbatim: they carry the only half a person can
    // act on, and a generic "could not start" would throw it away.
    expect(
      await screen.findByText(/already being worked on/),
    ).toBeTruthy();
    // STAYS. The list is still correct and the person now has to pick something
    // else, so taking it away would be the wrong answer to a refusal.
    expect(screen.getByText("fix the oauth callback")).toBeTruthy();
    expect(nav.tab).toBe("projects");
    expect(nav.pick).toBe("tickets");
  });

  it("does not spawn an issue lola already holds — it shows it instead", async () => {
    // `alreadyLive` is the daemon's own answer, and sending `openTicket` for one
    // earns a refusal telling you to check sessions. Following that advice is
    // better than printing it.
    replies.tickets = tickets([issue({ alreadyLive: true })]);
    render(TicketPicker);

    await screen.findByText("fix the oauth callback");
    expect(screen.getByText("Running")).toBeTruthy();

    await fireEvent.click(screen.getByRole("button", { name: /FE-9/ }));
    await waitFor(() => expect(nav.tab).toBe("sessions"));
    expect(reqs().some((f) => f.cmd === "openTicket")).toBe(false);
  });

  // -------------------------------------------------------------------------
  // The three states that must never be mistaken for each other
  // -------------------------------------------------------------------------
  //
  // "still asking", "asked and the answer is none" and "asked and could not be
  // answered" are three different facts and only the last one is a problem. A
  // screen that renders them alike turns a healthy empty backlog into a bug
  // report, and an unreachable Linear into a shrug.

  it("says it is still asking while the request is in flight", async () => {
    held.add("tickets");
    render(TicketPicker);

    expect(await screen.findByText("Loading issues…")).toBeTruthy();
    expect(screen.queryByText("Nothing to start")).toBeNull();
    expect(screen.queryByText("Could not list issues")).toBeNull();
  });

  it("says an empty answer is an empty answer, and offers the other scope", async () => {
    replies.tickets = tickets([]);
    render(TicketPicker);

    expect(await screen.findByText("Nothing to start")).toBeTruthy();
    expect(screen.queryByText("Could not list issues")).toBeNull();
    // Not a fault, so no retry: retrying a correct answer is how a working app
    // looks broken. The offer is the other scope, which is the actual next move
    // when a team plans its backlog unassigned.
    expect(screen.queryByRole("button", { name: "Try again" })).toBeNull();

    replies.tickets = tickets([issue({ title: "unassigned backlog item" })]);
    await fireEvent.click(screen.getByRole("button", { name: "Show the whole team" }));
    expect(await screen.findByText("unassigned backlog item")).toBeTruthy();
    expect(lastArgs("tickets")).toEqual({ project: "nori-app", scope: "team" });
  });

  it("says a failure is a failure, in the daemon's words, with a retry", async () => {
    replies.tickets = {
      ok: false,
      error: 'project "nori-app" has no Linear team — set team_id to browse issues',
    };
    render(TicketPicker);

    expect(await screen.findByText("Could not list issues")).toBeTruthy();
    // The sentence, not a classification of it. The daemon words "no team
    // configured" and "Linear is unreachable" differently already; matching on
    // that wording here would mis-label the day it rephrases one.
    expect(screen.getByText(/set team_id to browse issues/)).toBeTruthy();
    expect(screen.queryByText("Nothing to start")).toBeNull();
    expect(screen.queryByText("Loading issues…")).toBeNull();

    replies.tickets = tickets([issue()]);
    await fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByText("fix the oauth callback")).toBeTruthy();
  });
});
