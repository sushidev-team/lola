import { describe, it, expect } from "vitest";
import type { HubBoardItem, HubDeviceInfo } from "@bindings/internal/protocol";
import {
  boardSections,
  claimProjects,
  deliveryLabel,
  deviceOnline,
  hubChipText,
  itemTitle,
  leaseLeft,
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
    expect(leaseLeft("2026-11-11T12:30:00Z", now)).toBe("Lease ends in 30m");
    expect(leaseLeft("2026-11-11T11:00:00Z", now)).toBe("Lease expired");
    expect(hubChipText({ kind: "dep_advisory", label: "GHSA-x · lodash" }).state).toBe("");
    expect(hubChipText({ kind: "dep_advisory", leaseLost: true }).state).toBe("lost");
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
    expect(severityCounts(g[1].items)).toBe("1 high · 1 low");
    expect(groupByAdvisory(items).find((x) => x.key === "G1")?.items.length).toBe(2);
  });
});
