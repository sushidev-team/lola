import { describe, it, expect, beforeEach } from "vitest";
import { isTab, nav, paneNameFor, TABS } from "./nav.svelte";

beforeEach(() => {
  nav.toConnect();
  nav.tab = "sessions";
  nav.triage = "";
  nav.query = "";
});

describe("navigation", () => {
  it("starts at the connect screen", () => {
    expect(nav.screen).toBe("connect");
  });

  it("opens a terminal for one pane of one session", () => {
    nav.toTerminal("lola-fe-42", "lola-fe-42");
    expect(nav.screen).toBe("terminal");
    expect(nav.paneSession).toBe("lola-fe-42");
    expect(nav.pane).toBe("lola-fe-42");
  });

  it("goes back from the terminal to the list, dropping the pane", () => {
    // Dropping it matters: the terminal component is keyed on the pane name, so
    // a stale one would let a re-entry attach before the new selection lands.
    nav.toTerminal("lola-fe-42", "lola-fe-42");
    nav.back();
    expect(nav.screen).toBe("sessions");
    expect(nav.pane).toBe("");
  });

  it("goes back from the list to connect", () => {
    nav.toSessions();
    nav.back();
    expect(nav.screen).toBe("connect");
  });
});

describe("nav sheets", () => {
  it("opens and closes a sheet by name", () => {
    nav.openSheet("filter");
    expect(nav.sheet).toBe("filter");
    nav.closeSheet();
    expect(nav.sheet).toBe("");
  });

  it("closes whatever is open on every navigation", () => {
    // A sheet belongs to the screen it was opened over. Carried across, the
    // terminal's session menu would pop open on the list and the list's filter
    // sheet over a terminal — and only the screen that draws one can close it,
    // so the user would be stuck behind a modal nothing on screen owns.
    nav.openSheet("menu");
    nav.toSessions();
    expect(nav.sheet).toBe("");

    nav.openSheet("filter");
    nav.toTerminal("lola-fe-42", "lola-fe-42");
    expect(nav.sheet).toBe("");

    nav.openSheet("pane");
    nav.toConnect();
    expect(nav.sheet).toBe("");
  });
});

describe("nav tabs", () => {
  it("starts on the sessions tab", () => {
    expect(nav.tab).toBe("sessions");
  });

  it("switches to a tab and shows the tab shell", () => {
    nav.toTab("projects");
    expect(nav.tab).toBe("projects");
    expect(nav.screen).toBe("sessions");
  });

  it("closes a sheet on the way, like every other navigation", () => {
    nav.openSheet("filter");
    nav.toTab("settings");
    expect(nav.sheet).toBe("");
  });

  it("keeps the tab when a terminal is opened over it and closed again", () => {
    // The terminal is a SCREEN pushed over a tab, not a fifth tab: it is
    // full-screen, it belongs to one session rather than to a section of the
    // app, and leaving it has to put you back where you came from.
    nav.toTab("activity");
    nav.toTerminal("lola-fe-42", "lola-fe-42");
    expect(nav.screen).toBe("terminal");
    expect(nav.tab).toBe("activity");

    nav.back();
    expect(nav.screen).toBe("sessions");
    expect(nav.tab).toBe("activity");
  });

  it("keeps the tab across a disconnect and a return", () => {
    // `toSessions` deliberately does not reset it — see its comment. A reset
    // would drop somebody who opened a pane from Projects onto the list.
    nav.toTab("settings");
    nav.toConnect();
    nav.toSessions();
    expect(nav.tab).toBe("settings");
  });

  it("leaves the pane attached, because moving tabs is not leaving a terminal", () => {
    nav.toTerminal("lola-fe-42", "lola-fe-42");
    nav.toTab("projects");
    expect(nav.pane).toBe("lola-fe-42");
    expect(nav.paneSession).toBe("lola-fe-42");
  });
});

describe("isTab", () => {
  it("accepts every name the bar draws", () => {
    for (const t of TABS) expect(isTab(t)).toBe(true);
  });

  it("fails closed on anything else", () => {
    // A typo, a tab a later build added, a value from a link written against a
    // different version of the app: none of them is a tab, and the caller
    // leaves the current one alone rather than landing somewhere unexpected.
    expect(isTab("terminal")).toBe(false);
    expect(isTab("Sessions")).toBe(false);
    expect(isTab("")).toBe(false);
  });
});

describe("paneNameFor", () => {
  it("prefers the tmux name, as the daemon's own paneTarget does", () => {
    expect(paneNameFor({ id: "lola-fe-42", tmuxName: "lola-fe-42-x" })).toBe("lola-fe-42-x");
  });

  it("falls back to the id when no tmux session correlates", () => {
    expect(paneNameFor({ id: "lola-fe-42", tmuxName: "" })).toBe("lola-fe-42");
    expect(paneNameFor({ id: "lola-fe-42" })).toBe("lola-fe-42");
  });
});
