import { describe, it, expect, beforeEach } from "vitest";
import { nav, paneNameFor } from "./nav.svelte";

beforeEach(() => {
  nav.toConnect();
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

describe("paneNameFor", () => {
  it("prefers the tmux name, as the daemon's own paneTarget does", () => {
    expect(paneNameFor({ id: "lola-fe-42", tmuxName: "lola-fe-42-x" })).toBe("lola-fe-42-x");
  });

  it("falls back to the id when no tmux session correlates", () => {
    expect(paneNameFor({ id: "lola-fe-42", tmuxName: "" })).toBe("lola-fe-42");
    expect(paneNameFor({ id: "lola-fe-42" })).toBe("lola-fe-42");
  });
});
