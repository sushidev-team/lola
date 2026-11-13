import { describe, it, expect } from "vitest";
import type { HubBoardItem, HubDeviceInfo } from "@bindings/internal/protocol";
import {
  boardSections,
  claimProjects,
  deliveryLabel,
  deviceOnline,
  hubChipText,
  itemTitle,
  localSession,
  packageLine,
  severityClass,
} from "./hubboard";

const item = (o: Partial<HubBoardItem>): HubBoardItem =>
  ({ id: "x", kind: "dep_advisory", repo: "acme/web", label: "", package: "", state: "open", attempts: 0, reporters: 1, firstSeen: "", lastSeen: "", local: { claimable: true }, ...o }) as HubBoardItem;

describe("hubboard", () => {
  it("splits the board and sorts available by severity, then age", () => {
    const s = boardSections([
      item({ id: "low", severity: "low", firstSeen: "2026-01-01T00:00:00Z" }),
      item({ id: "crit-new", severity: "critical", firstSeen: "2026-03-01T00:00:00Z" }),
      item({ id: "crit-old", severity: "critical", firstSeen: "2026-02-01T00:00:00Z" }),
      item({ id: "mine", state: "claimed", lease: { mine: true, deviceName: "me", expires: "" } }),
      item({ id: "theirs", state: "claimed", lease: { mine: false, deviceName: "bob", expires: "" } }),
      item({ id: "d", state: "done" }),
      item({ id: "f", state: "failed" }),
      item({ id: "e", state: "escalated" }),
    ]);
    expect(s.available.map((i) => i.id)).toEqual(["crit-old", "crit-new", "low"]);
    expect(s.mine.map((i) => i.id)).toEqual(["mine"]);
    expect(s.teammates.map((i) => i.id)).toEqual(["theirs"]);
    expect([s.done.length, s.failed.length, s.escalated.length]).toEqual([1, 1, 1]);
  });

  it("names rows and packages", () => {
    expect(itemTitle(item({ advisory: "GHSA-1" }))).toBe("GHSA-1");
    expect(itemTitle(item({ kind: "red_bump_pr", prNumber: 12 }))).toBe("PR #12");
    expect(packageLine(item({ package: "lodash", vulnerable: "4.17.20", fixed: "4.17.21" }))).toBe("lodash 4.17.20 → 4.17.21");
    expect(packageLine(item({}))).toBe("");
    expect(severityClass("critical")).toContain("text-bad");
    expect(deliveryLabel("ci_failed")).toBe("CI failed");
    expect(deliveryLabel("some_new_word")).toBe("Some new word");
  });

  it("asks for a project only when several could run it", () => {
    expect(claimProjects(item({ local: { claimable: true, project: "web" } }))).toEqual([]);
    expect(claimProjects(item({ local: { claimable: true, projects: ["a", "b"] } }))).toEqual(["a", "b"]);
  });

  it("finds the local session from the row, else from the status", () => {
    expect(localSession(item({ local: { claimable: false, session: "s1" } }), null)).toBe("s1");
    const st = { sessions: [{ session: "s2", itemId: "x" }] } as never;
    expect(localSession(item({}), st)).toBe("s2");
  });

  it("reads devices, leases and chips", () => {
    const now = Date.parse("2026-11-11T12:00:00Z");
    const d = (o: Partial<HubDeviceInfo>) => ({ name: "d", me: false, freeSlots: 0, agents: [], active: true, ...o }) as HubDeviceInfo;
    expect(deviceOnline(d({ me: true }), now)).toBe(true);
    expect(deviceOnline(d({ lastSeen: "2026-11-11T11:55:00Z" }), now)).toBe(true);
    expect(deviceOnline(d({ lastSeen: "2026-11-11T10:00:00Z" }), now)).toBe(false);
    expect(hubChipText({ kind: "dep_advisory", label: "GHSA-x · lodash" }).state).toBe("");
    expect(hubChipText({ kind: "dep_advisory", leaseLost: true }).state).toBe("lost");
    expect(hubChipText({ kind: "dep_advisory", leaseLost: true, leaseTaken: true }).note).toBe("Taken by another Mac");
    expect(hubChipText({ kind: "dep_advisory", inReview: true }).note).toBe("In review");
    // Plain words: the chip never says "lease".
    for (const h of [{ leaseLost: true }, { leaseTaken: true }, { released: true }, { inReview: true }]) {
      const c = hubChipText({ kind: "dep_advisory", ...h });
      expect(`${c.note} ${c.tip}`).not.toMatch(/lease/i);
    }
    expect(hubChipText({ kind: "dep_advisory", released: true, leaseLost: true }).state).toBe("released");
    expect(hubChipText({ kind: "red_bump_pr" }).label).toBe("Failing dependency bump");
  });
});

import { groupByRepo, groupByAdvisory, fixAllItems, severityCounts } from "./hubboard";

describe("board groups", () => {
  const mk = (id: string, repo: string, advisory: string, severity: string, claimable = true, reason = "") =>
    ({ id, kind: "dep_advisory", repo, advisory, severity, state: "open", firstSeen: "", lastSeen: "", reporters: 1, attempts: 0, label: "", local: { claimable, reason } }) as never;

  it("groups per repository, worst first, with a shared reason only when nothing is claimable", () => {
    const items = [mk("a", "o/web", "G1", "low"), mk("b", "o/api", "G2", "critical", false, "not sandboxed"), mk("c", "o/web", "G3", "high"), mk("d", "o/api", "G1", "high", false, "not sandboxed")];
    const g = groupByRepo(items);
    expect(g.map((x) => x.key)).toEqual(["o/api", "o/web"]);
    expect(g[0].reason).toBe("not sandboxed");
    expect(g[1].items.map((i: { id: string }) => i.id)).toEqual(["c", "a"]);
    expect(fixAllItems(g[1]).length).toBe(2);
    expect(fixAllItems(g[0]).length).toBe(0);
    expect(severityCounts(g[1].items)).toBe("1 high · 1 low");
    expect(groupByAdvisory(items).find((x) => x.key === "G1")?.items.length).toBe(2);
  });
});

import { workGroups, severityTally } from "./hubboard";

describe("in-progress groups", () => {
  const chore = (id: string, mine: boolean, device: string, repo: string, severity: string) =>
    ({ id, kind: "dep_advisory", repo, severity, state: "claimed", lease: { mine, deviceName: device, expires: "2026-11-12T12:00:00Z" } }) as never;

  it("groups this Mac's chores per session and a teammate's per device and repository", () => {
    const status = { sessions: [{ session: "s1", itemId: "a" }, { session: "s1", itemId: "b" }] } as never;
    const g = workGroups([chore("a", true, "me", "o/web", "low"), chore("b", true, "me", "o/web", "critical")], [chore("c", false, "bob", "o/api", "high")], status);
    expect(g.map((x) => [x.key, x.items.length])).toEqual([["s:s1", 2], ["d:bob|o/api", 1]]);
    expect(g[0].items[0].id).toBe("b"); // worst first
    expect(severityTally(g[0].items)).toEqual([{ severity: "critical", count: 1 }, { severity: "low", count: 1 }]);
  });
});

import {
  attemptLine,
  failWords,
  healthLine,
  hubNotices,
  hubReason,
  needsYou,
  REVIEW_STALE_MS,
  severityFill,
  skipLine,
  splitFix,
  statsLine,
  whoOf,
  whyLine,
  workingFor,
} from "./hubboard";

describe("group fix", () => {
  it("counts claimable failing bumps too, and splits them from the advisories", () => {
    const rows = [
      item({ id: "a1", kind: "dep_advisory", advisory: "G1" }),
      item({ id: "p1", kind: "red_bump_pr", prNumber: 4 }),
      item({ id: "a2", kind: "dep_advisory", advisory: "G2", local: { claimable: false, reason: "capped" } }),
    ];
    const g = groupByRepo(rows)[0];
    const fix = fixAllItems(g);
    expect(fix.map((i) => i.id)).toEqual(["a1", "p1"]);
    expect(splitFix(fix)).toEqual({ advisories: [fix[0]], prs: [fix[1]] });
  });

  it("caps the grouped advisories but never the bumps", () => {
    const rows = [...Array.from({ length: 14 }, (_, i) => item({ id: `a${i}`, advisory: `G${i}` })), item({ id: "p", kind: "red_bump_pr", prNumber: 1 })];
    const fix = fixAllItems(groupByRepo(rows)[0]);
    expect(splitFix(fix).advisories.length).toBe(12);
    expect(splitFix(fix).prs.length).toBe(1);
  });
});

describe("who and when", () => {
  const now = Date.parse("2026-11-12T12:00:00Z");

  it("names the person first and the Mac second, from the holder or the owner", () => {
    expect(whoOf(item({ state: "claimed", lease: { mine: false, deviceName: "bobs-mac", member: "Bob", expires: "" } }))).toEqual({ name: "Bob", device: "bobs-mac", mine: false });
    expect(whoOf(item({ state: "in_review", owner: { mine: true, deviceName: "me-mac" } }))).toEqual({ name: "me-mac", device: "me-mac", mine: true });
    expect(whoOf(item({ state: "failed", history: [{ action: "claimed", deviceName: "x", member: "Ana", at: "" }] }))?.name).toBe("Ana");
    expect(whoOf(item({}))).toBeNull();
  });

  it("says how long a chore has been worked, and how long it sat in review", () => {
    expect(workingFor(item({ state: "claimed", claimedAt: "2026-11-12T10:00:00Z" }), now)).toBe("Working for 2h");
    expect(workingFor(item({ state: "claimed", claimedAt: "2026-11-12T11:37:00Z" }), now)).toBe("Working for 23m");
    expect(workingFor(item({ state: "claimed" }), now)).toBe("");
    expect(workingFor(item({ state: "in_review", claimedAt: "2026-11-01T00:00:00Z", history: [{ action: "in_review", at: "2026-11-09T12:00:00Z" }] }), now)).toBe("In review for 3d");
  });

  it("puts in-review rows with whoever opened the pull request", () => {
    const s = boardSections([
      item({ id: "r-mine", state: "in_review", owner: { mine: true, deviceName: "me" } }),
      item({ id: "r-theirs", state: "in_review", owner: { mine: false, deviceName: "bob" } }),
    ]);
    expect(s.mine.map((i) => i.id)).toEqual(["r-mine"]);
    expect(s.teammates.map((i) => i.id)).toEqual(["r-theirs"]);
  });
});

describe("plain words", () => {
  it("drops error tags and turns the daemon's reasons into sentences", () => {
    expect(hubReason("Error: hub: not claimed: dispatch held: load")).toBe("New sessions are on hold on this Mac: load");
    expect(hubReason("hub: not claimed: capped")).toBe("This Mac already runs as many sessions as allowed.");
    expect(hubReason("project is not sandboxed (hub.require_sandbox)")).toMatch(/^The project is not sandboxed/);
    expect(hubReason("Error: Error: something new")).toBe("Something new");
    expect(hubReason("")).toBe("");
    expect(hubReason(undefined)).toBe("");
  });

  it("never shows lease, heartbeat, offer or device key", () => {
    for (const m of ["hub: heartbeat: 503", "lease lost", "renew lease: 409", "offers: hub not reachable", "device key missing"]) {
      expect(hubReason(m)).not.toMatch(/lease|heartbeat|offer|device key/i);
    }
  });

  it("names Docker being down so a person can fix it", () => {
    expect(hubReason("hub: not claimed: Cannot connect to the Docker daemon. Is the docker daemon running?")).toBe("Docker is not running. Start Docker, then try again.");
  });

  it("explains failures and history in words", () => {
    expect(failWords("agent_gave_up")).toBe("the agent stopped without a pull request");
    expect(failWords("brand_new")).toBe("brand new");
    expect(whyLine(item({ attempts: 2, lastFail: "ci_failed", reopened: 1 }))).toBe("2 attempts · last: the pull request's checks stayed red · back on the list once");
    expect(attemptLine({ action: "failed", reason: "timeout", deviceName: "bobs-mac", member: "Bob", at: "" })).toEqual({ who: "Bob · bobs-mac", what: "Failed: it ran out of time" });
    expect(attemptLine({ action: "reopened", at: "" }).who).toBe("The hub");
  });

  it("gives repository health and unclaimed chores a fix in words", () => {
    expect(healthLine({ repo: "o/web", at: "", alerts: "no_access", pullRequests: "ok" })?.text).toBe("No access to Dependabot alerts");
    expect(healthLine({ repo: "o/web", at: "", alerts: "ok", pullRequests: "ok" })).toBeNull();
    expect(healthLine({ repo: "o/web", at: "", alerts: "ok", pullRequests: "error" })?.text).toBe("Could not read pull requests");
    const sk = skipLine({ itemId: "x", kind: "dep_advisory", reason: "the sandboxed agent has no login yet", at: "" });
    expect(sk.text).toBe("The sandboxed agent is not logged in yet.");
    expect(sk.fix).toMatch(/Log the agent in/);
    expect(skipLine({ itemId: "x", kind: "dep_advisory", reason: "ended here recently", at: "" }).fix).toBe("");
  });

  it("gives unrated severity a visible fill", () => {
    expect(severityFill("")).not.toBe("bg-edge");
  });

  it("reads the stats line", () => {
    expect(statsLine(null)).toBe("");
    expect(statsLine({ available: false, fixedThisWeek: 0, avgFixSeconds: 0 })).toBe("");
    expect(statsLine({ available: true, fixedThisWeek: 0, avgFixSeconds: 0 })).toBe("0 fixed this week");
    expect(statsLine({ available: true, fixedThisWeek: 4, avgFixSeconds: 7200 })).toBe("4 fixed this week · 2h to fix on average");
  });
});

describe("needs you", () => {
  const now = Date.parse("2026-11-12T12:00:00Z");
  const status = { joined: true, deviceName: "me-mac", sessions: [
    { session: "s-taken", itemId: "taken", kind: "dep_advisory", leaseTaken: true, summary: "GHSA-t · tar" },
    { session: "s-fail", itemId: "fail-local", kind: "dep_advisory" },
  ] } as never;

  it("lists what needs a person, most urgent first, once per chore", () => {
    const rows = needsYou([
      item({ id: "crit", state: "open", severity: "critical", advisory: "G-C" }),
      item({ id: "esc", state: "escalated", attempts: 3, escalation: { pending: false, identifier: "SEC-1", url: "https://linear.app/a/issue/SEC-1" } }),
      item({ id: "fail-owner", state: "failed", owner: { mine: true, deviceName: "me-mac" }, attempts: 1, lastFail: "agent_gave_up" }),
      item({ id: "fail-local", state: "failed" }),
      item({ id: "fail-hist", state: "failed", history: [{ action: "failed", deviceName: "me-mac", at: "" }] }),
      item({ id: "fail-other", state: "failed", owner: { mine: false, deviceName: "bob" } }),
      item({ id: "review-old", state: "in_review", owner: { mine: true, deviceName: "me-mac" }, claimedAt: new Date(now - REVIEW_STALE_MS - 60_000).toISOString(), outcome: { prUrl: "https://github.com/o/w/pull/2", delivery: "review_pending" } }),
      item({ id: "review-new", state: "in_review", owner: { mine: true, deviceName: "me-mac" }, claimedAt: new Date(now - 3_600_000).toISOString() }),
      item({ id: "taken", state: "claimed", lease: { mine: false, deviceName: "bob", expires: "" } }),
    ], status, now);
    expect(rows.map((r) => [r.kind, r.id])).toEqual([
      ["taken", "taken"],
      ["failed", "fail-owner"],
      ["failed", "fail-local"],
      ["failed", "fail-hist"],
      ["escalated", "esc"],
      ["review", "review-old"],
      ["critical", "crit"],
    ]);
    const fail = rows.find((r) => r.id === "fail-owner")!;
    expect(fail.why).toBe("Failed · 1 attempt · last: the agent stopped without a pull request");
    expect(rows.find((r) => r.id === "esc")!.issue).toBe("https://linear.app/a/issue/SEC-1");
    expect(rows.find((r) => r.id === "review-old")!.pr).toBe("https://github.com/o/w/pull/2");
    expect(rows.find((r) => r.id === "taken")!.session).toBe("s-taken");
  });

  it("names a taken chore from its session when the board has no row", () => {
    const rows = needsYou([], status, now);
    expect(rows).toHaveLength(1);
    expect(rows[0].title).toBe("GHSA-t · tar");
  });
});

describe("notices", () => {
  const st = (sessions: object[]) => ({ joined: true, sessions }) as never;

  it("says nothing on the first read, then reports auto-claims, failures and takeovers", () => {
    expect(hubNotices(null, st([{ itemId: "a", kind: "dep_advisory" }]))).toEqual([]);
    const prev = st([{ itemId: "b", kind: "dep_advisory", summary: "GHSA-b" }, { itemId: "c", kind: "red_bump_pr", summary: "PR #3" }]);
    const next = st([
      { itemId: "a", kind: "dep_advisory", summary: "GHSA-a" },
      { itemId: "mine", kind: "dep_advisory", summary: "GHSA-m" },
      { itemId: "b", kind: "dep_advisory", summary: "GHSA-b", reported: "fail:agent_gave_up" },
      { itemId: "c", kind: "red_bump_pr", summary: "PR #3", leaseLost: true, leaseTaken: true },
    ]);
    const n = hubNotices(prev, next, new Set(["mine"]));
    expect(n.map((x) => [x.kind, x.itemId])).toEqual([["good", "a"], ["bad", "b"], ["warn", "c"]]);
    expect(n[0].text).toBe("This Mac took a team chore: GHSA-a");
    expect(n[1].text).toBe("Team chore failed: GHSA-b (the agent stopped without a pull request)");
    expect(n[2].text).toMatch(/^Another Mac took PR #3/);
    // Once said, not again.
    expect(hubNotices(next, next)).toEqual([]);
  });
});
