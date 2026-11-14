import { describe, it, expect } from "vitest";
import { hubURLError, joinErrorText, parseInvite, PUBLIC_KEY_RE } from "./hubinvite";

const PUB = "A".repeat(43);
const TOKEN = "tok_0123456789abcdef";

describe("parseInvite", () => {
  it("parses an invite with and without a policy key", () => {
    expect(parseInvite(`lola-hub:https://hub.example.com#${TOKEN}`)).toEqual({
      ok: true,
      invite: { url: "https://hub.example.com", token: TOKEN, keyId: "", publicKey: "" },
    });
    expect(parseInvite(`  lola-hub:https://hub.example.com/#${TOKEN}#kadmin1:${PUB}\n`)).toEqual({
      ok: true,
      invite: { url: "https://hub.example.com", token: TOKEN, keyId: "kadmin1", publicKey: PUB },
    });
  });

  it("takes a bare token, like the Go parser", () => {
    expect(parseInvite(TOKEN)).toEqual({ ok: true, invite: { url: "", token: TOKEN, keyId: "", publicKey: "" } });
  });

  it("refuses malformed invites with fixed words that echo nothing", () => {
    const bad = [
      "",
      "a b",
      `x${"y".repeat(5000)}`,
      `${TOKEN}#extra`,
      "lola-hub:https://hub.example.com",
      `lola-hub:https://hub.example.com#${TOKEN}#k:${PUB}#more`,
      `lola-hub:http://hub.example.com#${TOKEN}`,
      `lola-hub:https://hub.example.com/path#${TOKEN}`,
      "lola-hub:https://hub.example.com#",
      `lola-hub:https://hub.example.com#${TOKEN}#bad id:${PUB}`,
      `lola-hub:https://hub.example.com#${TOKEN}#kadmin1`,
      `lola-hub:https://hub.example.com#${TOKEN}#kadmin1:short`,
    ];
    for (const s of bad) {
      const r = parseInvite(s);
      expect(r.ok, s).toBe(false);
      if (!r.ok) {
        expect(r.error).not.toContain(TOKEN);
        expect(r.error).not.toContain("hub.example.com");
      }
    }
  });

  it("accepts loopback http for development", () => {
    expect(parseInvite(`lola-hub:http://127.0.0.1:8080#${TOKEN}`).ok).toBe(true);
    expect(parseInvite(`lola-hub:http://localhost:8080#${TOKEN}`).ok).toBe(true);
  });
});

describe("PUBLIC_KEY_RE", () => {
  it("accepts only strict 32-byte base64url (zero padding bits)", () => {
    expect(PUBLIC_KEY_RE.test(PUB)).toBe(true);
    expect(PUBLIC_KEY_RE.test("A".repeat(42) + "B")).toBe(false);
    expect(PUBLIC_KEY_RE.test("A".repeat(42))).toBe(false);
    expect(PUBLIC_KEY_RE.test("A".repeat(42) + "=")).toBe(false);
  });
});

describe("hubURLError", () => {
  it("mirrors config.ValidateHubURL", () => {
    expect(hubURLError("https://hub.example.com")).toBe("");
    expect(hubURLError("http://localhost:9000")).toBe("");
    expect(hubURLError("http://hub.example.com")).toMatch(/https/);
    expect(hubURLError("https://hub.example.com/v1")).toMatch(/without a path/);
    expect(hubURLError("https://u:p@hub.example.com")).toMatch(/login/);
    expect(hubURLError("https://hub.example.com?a=1")).toMatch(/query/);
    expect(hubURLError("nope")).toMatch(/full URL/);
  });
});

describe("joinErrorText", () => {
  it("maps the daemon's known refusals to plain words", () => {
    expect(joinErrorText("hub: join: enroll token rejected")).toMatch(/did not accept this invite/);
    expect(joinErrorText("hub: the hub accepted the one-time token (it is now used): save identity: disk full")).toMatch(/invite is now used/);
    expect(joinErrorText("hub: the config directory is not writable, so the join could not be saved (the token was not sent): x")).toMatch(/Nothing was used up/);
    expect(joinErrorText("hub: the invite names another hub url than the one given")).toMatch(/different hub/);
    expect(joinErrorText("Post https://hub: context deadline exceeded")).toMatch(/did not answer in time/);
    expect(joinErrorText("dial tcp: lookup hub: no such host")).toMatch(/could not reach/);
  });

  it("keeps an unknown error, without Go's prefix", () => {
    expect(joinErrorText("Error: something odd")).toBe("something odd");
  });
});
