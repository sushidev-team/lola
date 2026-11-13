import { describe, it, expect } from "vitest";
import { diagnose } from "./diagnose";

// The four confusions this module exists to separate. Each has a different fix,
// and all four look identical at the socket.
describe("diagnose", () => {
  it("reports a live connection", () => {
    const d = diagnose({ phase: "ready", label: "marsmac" });
    expect(d.kind).toBe("ok");
  });

  it("separates a WRONG KEY from a network problem", () => {
    // `denied` means the daemon answered: the address and the pin are both
    // right, and retrying without changing the key can never help.
    const d = diagnose({
      phase: "closed",
      refusal: { code: "denied", message: "authenticate first" },
      host: "192.168.1.5",
      label: "marsmac",
    });
    expect(d.kind).toBe("rejected");
    expect(d.title).toContain("refused this key");
    expect(d.detail).toContain("answered");
    expect(d.retryable).toBe(false);
  });

  it("names which side is behind on a version skew", () => {
    const d = diagnose({
      phase: "closed",
      refusal: { code: "unsupported_version", minV: 2, maxV: 3 },
    });
    expect(d.kind).toBe("version");
    expect(d.detail).toContain("2");
    expect(d.detail).toContain("3");
    expect(d.retryable).toBe(false);
  });

  it("blames the app, not the user, for a denied command", () => {
    const d = diagnose({ phase: "closed", refusal: { code: "unknown_cmd" } });
    expect(d.kind).toBe("client");
    expect(d.hint).toContain("bug in the app");
  });

  it("refuses to interpret a refusal code it does not know", () => {
    // Saying something specific about an unrecognised code is how a confidently
    // wrong fix gets suggested.
    const d = diagnose({ phase: "closed", refusal: { code: "some_future_code", message: "nope" } });
    expect(d.kind).toBe("rejected");
    expect(d.detail).toContain("nope");
  });

  it("separates a PIN MISMATCH from an unreachable host", () => {
    const d = diagnose({
      phase: "closed",
      error: { message: "TLS handshake failed: errSSLBadCert (-9807)" },
      host: "192.168.1.5",
      label: "marsmac",
    });
    expect(d.kind).toBe("identity");
    expect(d.hint).toContain("device.key");
    expect(d.retryable).toBe(false);
  });

  it("names the local-network permission on a private address", () => {
    // THE load-bearing case. iOS reports a denied permission as an ordinary
    // unreachable host, never asks again, and offers no API to check — so if the
    // app does not say this, the user checks their WiFi forever.
    const d = diagnose({
      phase: "closed",
      error: { message: "connection failed" },
      host: "192.168.1.5",
      label: "marsmac",
    });
    expect(d.kind).toBe("unreachable");
    expect(d.title).toBe("Not on marsmac's network");
    expect(d.hint).toContain("Local Network");
    expect(d.hint).toContain("only asks once");
    expect(d.retryable).toBe(true);
  });

  it("does NOT blame the permission for a loopback address", () => {
    // A loopback connection — an SSH forward, which is M1's own shape — prompts
    // for nothing, so pointing at Settings there would be a wrong lead.
    const d = diagnose({
      phase: "closed",
      error: { message: "connection failed" },
      host: "127.0.0.1",
    });
    expect(d.kind).toBe("unreachable");
    expect(d.hint).toBeUndefined();
  });

  it("prefers a refusal over an error, since a refusal means the daemon spoke", () => {
    const d = diagnose({
      phase: "closed",
      error: { message: "connection reset" },
      refusal: { code: "denied" },
      host: "192.168.1.5",
    });
    expect(d.kind).toBe("rejected");
  });

  it("never suggests re-pairing for an unreachable daemon", () => {
    // PLAN.md: an off-network phone must stay distinguishable from a REVOKED
    // one, and revocation is what the pairing screen means.
    for (const host of ["192.168.1.5", "127.0.0.1", "mac.example.com"]) {
      const d = diagnose({ phase: "closed", error: { message: "timeout" }, host });
      expect(`${d.title} ${d.detail} ${d.hint ?? ""}`.toLowerCase()).not.toContain("pair");
    }
  });
});
