import { describe, expect, it } from "vitest";
import { diagnose } from "./diagnose";

const words = (s: string) => s.trim().split(/\s+/).length;

describe("diagnose", () => {
  it("reports a live connection", () => {
    expect(diagnose({ phase: "ready", label: "Mars Mac" })).toMatchObject({ kind: "ok", retryable: false });
  });

  it("says nothing is connected before and after a deliberate close", () => {
    expect(diagnose({ phase: "idle" }).kind).toBe("idle");
    expect(diagnose({ phase: "closed" }).kind).toBe("idle");
    expect(diagnose({ phase: "closed", failure: "client_closed" }).kind).toBe("idle");
  });

  it("reports reconnecting while a path is being raced and after a background close", () => {
    expect(diagnose({ phase: "connecting" }).kind).toBe("racing");
    expect(diagnose({ phase: "connecting", failure: "backgrounded" }).kind).toBe("racing");
  });

  it("separates offline from unreachable and names the Mac", () => {
    expect(diagnose({ phase: "connecting", failure: "offline" })).toMatchObject({ kind: "offline", detail: "No network." });
    for (const failure of ["unreachable", "timeout", "relay_unavailable", "peer_closed"] as const) {
      const d = diagnose({ phase: "connecting", failure, label: "Mars Mac" });
      expect(d.kind).toBe("unreachable");
      expect(d.detail).toBe("Can't reach Mars Mac.");
      expect(d.retryable).toBe(true);
    }
  });

  it("says whether the team hub relay could have helped", () => {
    const d = (relay: "none" | "ok" | "backoff" | "credential_dead" | "hub_too_old") =>
      diagnose({ phase: "closed", failure: "unreachable", label: "Mars Mac", relay });
    expect(d("none").detail).toBe("Can't reach Mars Mac, and remote access isn't set up.");
    expect(d("hub_too_old").detail).toBe("Can't reach Mars Mac; its team hub needs an update.");
    expect(d("credential_dead").detail).toBe("Can't reach Mars Mac, and remote access has expired.");
    expect(d("ok").detail).toBe("Can't reach Mars Mac here or through its team hub.");
    expect(d("backoff").detail).toBe("Can't reach Mars Mac here or through its team hub.");
    for (const relay of ["none", "ok", "backoff", "credential_dead", "hub_too_old"] as const) {
      const x = d(relay);
      expect(x).toMatchObject({ kind: "unreachable", title: "Unreachable", retryable: true });
      expect(words(x.detail)).toBeLessThanOrEqual(12);
      expect(x.detail.match(/[.…]/g)?.length ?? 0).toBe(1);
    }
    // Offline is offline whatever the relay.
    expect(diagnose({ phase: "closed", failure: "offline", relay: "none" }).detail).toBe("No network.");
  });

  it("falls back to a phrase when the Mac has no name", () => {
    expect(diagnose({ phase: "connecting", failure: "unreachable" }).detail).toBe("Can't reach your Mac.");
  });

  it("keeps retrying a changed key but says so", () => {
    expect(diagnose({ phase: "connecting", failure: "key_changed" })).toMatchObject({ kind: "key_changed", retryable: true });
  });

  it("treats not_paired as removal, from the failure code or the daemon's refusal", () => {
    expect(diagnose({ phase: "closed", failure: "not_paired" })).toMatchObject({ kind: "not_paired", retryable: false });
    expect(diagnose({ phase: "closed", refusal: { code: "not_paired" } }).kind).toBe("not_paired");
  });

  it("names which side is behind on a version skew", () => {
    expect(diagnose({ phase: "closed", refusal: { code: "unsupported_version", minV: 1, maxV: 1 } }).detail).toBe(
      "Update Lola on your Mac.",
    );
    expect(diagnose({ phase: "closed", refusal: { code: "unsupported_version", minV: 3, maxV: 4 } }).detail).toBe(
      "Update this app.",
    );
    expect(diagnose({ phase: "closed", failure: "unsupported_version" }).retryable).toBe(false);
  });

  it("outranks a transport failure with the daemon's refusal", () => {
    expect(diagnose({ phase: "closed", failure: "unreachable", refusal: { code: "not_paired" } }).kind).toBe("not_paired");
  });

  it("never interprets a refusal code it does not know", () => {
    const d = diagnose({ phase: "closed", refusal: { code: "from_the_future", message: "nope" } });
    expect(d).toMatchObject({ kind: "failed", retryable: true });
    expect(d.detail).not.toContain("nope");
  });

  it("keeps every title to three words and every body to one short sentence", () => {
    const all = [
      diagnose({ phase: "ready", label: "Mac" }),
      diagnose({ phase: "idle" }),
      diagnose({ phase: "connecting" }),
      diagnose({ phase: "connecting", failure: "offline" }),
      diagnose({ phase: "connecting", failure: "unreachable" }),
      diagnose({ phase: "connecting", failure: "key_changed" }),
      diagnose({ phase: "closed", failure: "not_paired" }),
      diagnose({ phase: "closed", failure: "unsupported_version" }),
      diagnose({ phase: "closed", refusal: { code: "x" } }),
      diagnose({ phase: "closed", failure: "internal" }),
    ];
    for (const d of all) {
      expect(words(d.title)).toBeLessThanOrEqual(3);
      expect(words(d.detail)).toBeLessThanOrEqual(12);
      expect(d.detail.match(/[.…]/g)?.length ?? 0).toBe(1);
    }
  });
});
