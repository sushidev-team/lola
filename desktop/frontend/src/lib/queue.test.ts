import { describe, it, expect } from "vitest";
import { landingFor, nextWaiting, waitingQueue } from "./queue";
import type { SessionInfo } from "@bindings/internal/protocol";

const at = (m: number) => new Date(Date.UTC(2026, 0, 1, 0, m)).toISOString();
const s = (id: string, o: Record<string, unknown> = {}) => ({ id, agentState: "idle", ...o }) as unknown as SessionInfo;
const blocked = (id: string, m: number) => s(id, { agentState: "waiting_input", agentStateSince: at(m) });
const red = (id: string, m: number, o = {}) => s(id, { delivery: "ci_failed", statusSince: at(m), ...o });

describe("waitingQueue", () => {
  it("orders blocked before regressed, oldest first", () => {
    const q = waitingQueue([red("r2", 1), blocked("b2", 9), red("r1", 0), blocked("b1", 5)]);
    expect(q.map((i) => i.id)).toEqual(["b1", "b2", "r1", "r2"]);
  });
  it("counts a host waiting on a person as blocked", () => {
    const q = waitingQueue([s("h", { ssh: { needsHuman: true, connections: [] } })]);
    expect(q[0]).toMatchObject({ id: "h", kind: "blocked" });
  });
  it("excludes red CI with a working agent", () => {
    expect(waitingQueue([red("a", 0, { agentState: "working" }), red("b", 0, { agentState: "starting" })])).toEqual([]);
  });
  it("excludes resting agents", () => {
    expect(waitingQueue([s("a"), s("b", { delivery: "review_pending" }), s("c", { delivery: "approved" })])).toEqual([]);
  });
});

describe("nextWaiting", () => {
  const list = [blocked("a", 1), blocked("b", 2), blocked("c", 3)];
  it("wraps from the current session", () => {
    expect(nextWaiting(list, "a")?.id).toBe("b");
    expect(nextWaiting(list, "c")?.id).toBe("a");
    expect(nextWaiting(list, "a", -1)?.id).toBe("c");
  });
  it("starts at the head from a session not in the queue", () => {
    expect(nextWaiting(list, "zzz")?.id).toBe("a");
    expect(nextWaiting([], "a")).toBeUndefined();
  });
});

describe("landingFor", () => {
  const item = { id: "a", kind: "blocked" as const, since: 0 };
  it("lands on the card for a prompt, and on the terminal for a modal", () => {
    expect(landingFor(s("a", { inputReason: "permission_prompt" }), item, false)).toBe("card");
    expect(landingFor(s("a", { inputReason: "dialog" }), item, false)).toBe("terminal");
  });
  it("respects a session that opens on its terminal, except for an approval no terminal can answer", () => {
    expect(landingFor(s("a"), item, true)).toBe("terminal");
    expect(landingFor(s("a", { ssh: { needsHuman: true, connections: [] } }), item, true)).toBe("card");
  });
  it("lands a regressed session on the last moment", () => {
    expect(landingFor(s("a"), { ...item, kind: "regressed" }, false)).toBe("moment");
  });
});
