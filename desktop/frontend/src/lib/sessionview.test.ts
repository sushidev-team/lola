import { describe, it, expect } from "vitest";
import { branchFromPrompt, CONVERSATION_FOR_ALL, conversationOffered, defaultView, feedKnownFrom, rosterGroups, rosterLine, stateGlyph, stripCells, stripShown } from "./sessionview";
import type { SessionInfo } from "@bindings/internal/protocol";

const claude = { id: "a", agent: "claude", agentState: "idle" };
const conn = { id: "c1", host: "h", state: "connected" };
const withHost = { ...claude, ssh: { connections: [conn], lastSeq: 1 } } as never;
const ALL = true;

describe("defaultView", () => {
  it("agentless opens on terminal", () => {
    expect(defaultView({ location: "temporary" }, "conversation", "", undefined, ALL)).toBe("terminal");
    expect(defaultView({ status: "shell" }, null, "", undefined, ALL)).toBe("terminal");
    expect(defaultView({ agentState: "orphaned" }, null, "", undefined, ALL)).toBe("terminal");
  });
  it("goal opens on terminal", () => {
    expect(defaultView({ ...claude, goalId: "g" }, "conversation", "conversation", true, ALL)).toBe("terminal");
  });
  it("sticky beats global", () => {
    expect(defaultView(claude, "terminal", "conversation", true, ALL)).toBe("terminal");
    expect(defaultView(claude, "conversation", "terminal", true, ALL)).toBe("conversation");
  });
  it("no feed reader opens on terminal", () => {
    expect(defaultView({ ...claude, agent: "codex" }, null, "", undefined, ALL)).toBe("terminal");
    expect(defaultView({ ...claude, agent: "opencode" }, null, "", undefined, ALL)).toBe("terminal");
  });
  it("a direct ssh session opens on conversation whatever its agent", () => {
    expect(defaultView({ agent: "codex", location: "ssh" }, null, "", undefined, false)).toBe("conversation");
    expect(defaultView({ ...(withHost as object), agent: "opencode" }, null, "", undefined, false)).toBe("conversation");
  });
  it("the default global preference decides nothing", () => {
    expect(defaultView({ ...claude, agent: "codex" }, null, "conversation", undefined, ALL)).toBe("terminal");
    expect(defaultView(claude, null, "conversation", false, ALL)).toBe("terminal");
    expect(defaultView(claude, null, "terminal", true, ALL)).toBe("terminal");
  });
  it("bad last health opens on terminal", () => {
    expect(defaultView(claude, null, "", feedKnownFrom("unreadable"), ALL)).toBe("terminal");
    expect(defaultView(claude, null, "", feedKnownFrom("unsupported"), ALL)).toBe("terminal");
    expect(defaultView(claude, null, "", feedKnownFrom("partial"), ALL)).toBe("conversation");
    expect(defaultView(claude, null, "", feedKnownFrom(undefined), ALL)).toBe("conversation");
  });
  it("conversation is default for a claude session", () => {
    expect(CONVERSATION_FOR_ALL).toBe(true);
    expect(defaultView(claude, null, "", true)).toBe("conversation");
    expect(defaultView(claude, "conversation", "", true)).toBe("conversation");
    expect(conversationOffered(claude)).toBe(true);
  });
  it("a codex session is offered the conversation and still opens on its terminal", () => {
    const codex = { ...claude, agent: "codex" };
    expect(conversationOffered(codex)).toBe(true);
    expect(defaultView(codex, null, "", undefined)).toBe("terminal");
    expect(defaultView(codex, "conversation", "", undefined)).toBe("conversation");
  });
});

describe("branchFromPrompt", () => {
  it("takes the first six words, as a ref git accepts", () => {
    expect(branchFromPrompt("Fix the login redirect after logout, please and thanks", "feat/")).toBe("feat/fix-the-login-redirect-after-logout");
    expect(branchFromPrompt("a..b trailing.", "x/")).toBe("x/a-b-trailing");
  });
  it("names nothing when nothing usable is left", () => {
    expect(branchFromPrompt("  ")).toBe("");
    expect(branchFromPrompt("日本語")).toBe("");
  });
});

describe("conversationOffered", () => {
  it("wants connections, not a store: a posted narration line makes s.ssh exist", () => {
    expect(conversationOffered({ ...claude, ssh: { connections: [], lastSeq: 3 } } as never, false)).toBe(false);
    expect(conversationOffered(withHost, false)).toBe(true);
    expect(conversationOffered({ location: "ssh" }, false)).toBe(true);
  });
  it("never offers one to a session with no agent", () => {
    expect(conversationOffered({ status: "shell" }, ALL)).toBe(false);
    expect(conversationOffered({ location: "temporary" }, ALL)).toBe(false);
  });
});

describe("stripCells", () => {
  it("puts views first and machines after, in strip order, with no cell for an ended host", () => {
    const s = { ...claude, worktree: "/w", ssh: { connections: [conn, { ...conn, id: "c2", state: "closed" }], lastSeq: 1 } } as never;
    const { views, machines } = stripCells(s, ["a-shell-1", "a-review", "a-dev-1"]);
    expect(views.map((c) => c.key)).toEqual(["conversation", "terminal", "changes"]);
    expect(machines.map((c) => c.kind)).toEqual(["host", "shell", "review", "dev"]);
  });
  it("gives a goal and a direct session no Changes, and hides an agentless strip with one terminal", () => {
    expect(stripCells({ ...claude, worktree: "/w", goalId: "g" }, []).views.map((c) => c.key)).toEqual(["conversation", "terminal"]);
    expect(stripCells({ location: "ssh", worktree: "/w" }, []).views.map((c) => c.key)).toEqual(["conversation", "terminal"]);
    expect(stripShown({ status: "shell" }, 0)).toBe(false);
    expect(stripShown({ status: "shell" }, 1)).toBe(true);
  });
});

describe("the state vocabulary", () => {
  it("names each state with its own glyph", () => {
    const seen = (o: object) => stateGlyph(o).glyph;
    expect(seen({ agentState: "waiting_input" })).toBe("◉");
    expect(seen({ agentState: "working" })).toBe("◐");
    expect(seen({ agentState: "dead" })).toBe("✕");
    expect(seen({ agentState: "shell" })).toBe("$");
    expect(seen({ agentState: "idle", agentStateSince: "2026-01-01T00:00:00Z" })).toBe("●");
    expect(stateGlyph({ agentState: "idle", agentStateSince: "2026-01-01T00:00:00Z" }, Date.UTC(2026, 0, 2)).glyph).toBe("○");
  });
  it("prefers lola's own ask, then a red PR, then the tool", () => {
    const now = Date.UTC(2026, 0, 1, 1);
    expect(rosterLine({ agentState: "waiting_input", inputReason: "permission_prompt", delivery: "ci_failed" }, now)).toEqual({ text: "Wants permission", tone: "orange" });
    expect(rosterLine({ ssh: { nextApproval: { host: "prod-api" } } } as never, now)).toEqual({ text: "Approve on prod-api", tone: "orange" });
    expect(rosterLine({ agentState: "working", delivery: "ci_failed", currentTool: "Bash" }, now)).toEqual({ text: "CI failed", tone: "bad" });
    expect(rosterLine({ agentState: "working", currentTool: "Bash", agentStateSince: "2026-01-01T00:58:00Z" } as never, now).text).toBe("Bash · 2m");
    expect(rosterLine({ agentState: "idle", board: { blocked: "needs a key" } } as never, now).text).toBe("Says blocked: needs a key");
    expect(rosterLine({ agentState: "idle", prNumber: 412, agentStateSince: "2026-01-01T00:56:00Z" } as never, now).text).toBe("Done 4m ago · #412");
  });
});

describe("rosterGroups", () => {
  const row = (id: string, project: string, firstSeen: string, o = {}) => ({ id, project, firstSeen, ...o }) as unknown as SessionInfo;
  it("follows config order, then first seen, then id, with ssh and temporary after", () => {
    const groups = rosterGroups(
      [row("b", "web", "2026-01-02"), row("c", "lola", "2026-01-03"), row("a", "lola", "2026-01-01"), row("t", "", "2026-01-01", { location: "temporary" }), row("s", "", "2026-01-01", { location: "ssh" }), row("x", "gone", "2026-01-01")],
      ["lola", "web"],
      (n) => n.toUpperCase(),
    );
    expect(groups.map((g) => g.key)).toEqual(["p:lola", "p:web", "p:gone", "ssh", "temporary"]);
    expect(groups[0].rows.map((r) => r.id)).toEqual(["a", "c"]);
  });
});
