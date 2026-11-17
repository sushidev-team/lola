import { describe, expect, it } from "vitest";
import { summarizeRelay } from "./phonerelay";

const st = (relay: Record<string, unknown> | null) =>
  ({ enabled: true, joined: true, claimsToday: 0, maxClaimsPerDay: 0, pendingOutcomes: 0, relay }) as never;

describe("summarizeRelay", () => {
  it("covers every state and reason the daemon writes with a sentence", () => {
    const cases: [Record<string, unknown> | null, string, string][] = [
      [null, "Off", ""],
      [{ state: "connected", activeConns: 0 }, "Available", ""],
      [{ state: "connected", activeConns: 1 }, "Available", ""],
      [{ state: "connecting" }, "Connecting", ""],
      [{ state: "syncing" }, "Connecting", ""],
      [{ state: "unsupported", reason: "no_relay" }, "Team hub needs an update", ""],
      [{ state: "unsupported", reason: "version" }, "Team hub needs an update", ""],
      [{ state: "refused", reason: "revoked" }, "Refused", "join"],
      [{ state: "backoff", reason: "error" }, "Retrying", ""],
      [{ state: "backoff", reason: "rate_limited" }, "Retrying", ""],
      [{ state: "off", reason: "not_joined" }, "Needs a team hub", "join"],
      [{ state: "off", reason: "hub_off" }, "Team hub is off", "join"],
      [{ state: "off", reason: "revoked" }, "Refused", "join"],
      [{ state: "off", reason: "no_phones" }, "No phone paired yet", ""],
      [{ state: "off", reason: "disabled" }, "Turned off", ""],
      [{ state: "off", reason: "config_error" }, "Settings error", ""],
      [{ state: "off", reason: "remote_off" }, "Off", ""],
      [{ state: "off", reason: "stopped" }, "Off", ""],
    ];
    for (const [relay, title, action] of cases) {
      const s = summarizeRelay(st(relay));
      expect(s.title, JSON.stringify(relay)).toBe(title);
      expect(s.action, JSON.stringify(relay)).toBe(action);
      expect(s.detail).toMatch(/^[A-Z0-9].*\.$/);
    }
  });

  it("is good only while connected", () => {
    expect(summarizeRelay(st({ state: "connected", activeConns: 0 })).ok).toBe(true);
    expect(summarizeRelay(st({ state: "off", reason: "no_phones" })).ok).toBe(false);
  });

  it("names the phones connected through the hub", () => {
    expect(summarizeRelay(st({ state: "connected", activeConns: 1 })).detail).toBe("1 phone is connected through the team hub.");
    expect(summarizeRelay(st({ state: "connected", activeConns: 3 })).detail).toBe("3 phones are connected through the team hub.");
  });

  it("never shows the daemon's free-text error", () => {
    const s = summarizeRelay(st({ state: "backoff", reason: "error", lastError: "hub: HTTP 502 secret-ish" }));
    expect(JSON.stringify(s)).not.toContain("502");
  });

  it("waits for the first status", () => {
    expect(summarizeRelay(null).title).toBe("Unknown");
  });
});
