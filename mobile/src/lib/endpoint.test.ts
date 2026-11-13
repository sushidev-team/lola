import { describe, it, expect } from "vitest";
import {
  DEFAULT_REMOTE_PORT,
  classifyHost,
  endpointId,
  isPinShaped,
  needsLocalNetwork,
  parsePort,
  validateDraft,
} from "./endpoint";

describe("classifyHost", () => {
  it("recognises loopback, which prompts for nothing", () => {
    // An SSH forward lands here, and it is the shape M1's insecure build forces
    // the daemon to bind to.
    expect(classifyHost("localhost")).toBe("loopback");
    expect(classifyHost("127.0.0.1")).toBe("loopback");
    expect(classifyHost("127.3.2.1")).toBe("loopback");
    expect(classifyHost("::1")).toBe("loopback");
    expect(classifyHost("[::1]")).toBe("loopback");
  });

  it("recognises every private range the local-network permission covers", () => {
    expect(classifyHost("10.0.0.4")).toBe("private");
    expect(classifyHost("172.16.0.1")).toBe("private");
    expect(classifyHost("172.31.255.255")).toBe("private");
    expect(classifyHost("192.168.1.42")).toBe("private");
    expect(classifyHost("100.101.102.103")).toBe("private"); // CGNAT / tailnet
    expect(classifyHost("169.254.1.1")).toBe("private"); // link-local
    expect(classifyHost("fd00::1")).toBe("private"); // unique-local
    expect(classifyHost("fe80::1")).toBe("private"); // link-local
    expect(classifyHost("marsmac.local")).toBe("private"); // mDNS
  });

  it("does not mistake a neighbouring range for a private one", () => {
    expect(classifyHost("172.15.0.1")).toBe("other");
    expect(classifyHost("172.32.0.1")).toBe("other");
    expect(classifyHost("192.167.1.1")).toBe("other");
    expect(classifyHost("100.63.0.1")).toBe("other");
  });

  it("rejects what is not an address at all", () => {
    expect(classifyHost("")).toBe("invalid");
    expect(classifyHost("   ")).toBe("invalid");
    expect(classifyHost("256.1.1.1")).toBe("invalid");
    expect(classifyHost("my mac")).toBe("invalid");
    expect(classifyHost("http://10.0.0.1")).toBe("invalid");
  });

  it("only claims the permission may apply where it can", () => {
    expect(needsLocalNetwork("127.0.0.1")).toBe(false);
    expect(needsLocalNetwork("192.168.1.5")).toBe(true);
    expect(needsLocalNetwork("mac.example.com")).toBe(true);
  });
});

describe("parsePort", () => {
  it("defaults an empty port to the daemon's own", () => {
    expect(parsePort("")).toBe(DEFAULT_REMOTE_PORT);
    expect(parsePort("  ")).toBe(DEFAULT_REMOTE_PORT);
    expect(DEFAULT_REMOTE_PORT).toBe(7717);
  });

  it("accepts a real port and rejects the rest", () => {
    expect(parsePort("7717")).toBe(7717);
    expect(parsePort("1")).toBe(1);
    expect(parsePort("65535")).toBe(65535);
    expect(parsePort("0")).toBeNull();
    expect(parsePort("65536")).toBeNull();
    expect(parsePort("77 17")).toBeNull();
    expect(parsePort("-1")).toBeNull();
  });
});

describe("isPinShaped", () => {
  const good = "A".repeat(43) + "=";

  it("accepts the shape the daemon prints", () => {
    // base64 of a 32-byte SHA-256, standard alphabet with padding: always 44
    // characters ending in "=".
    expect(isPinShaped(good)).toBe(true);
    expect(isPinShaped(`  ${good}  `)).toBe(true);
  });

  it("rejects a truncated or unpadded copy-paste", () => {
    // The failure this catches would otherwise present as an opaque TLS error
    // long after the form was dismissed.
    expect(isPinShaped("A".repeat(43))).toBe(false);
    expect(isPinShaped("A".repeat(44))).toBe(false);
    expect(isPinShaped("")).toBe(false);
    expect(isPinShaped("not a pin")).toBe(false);
  });
});

describe("validateDraft", () => {
  const ok = { host: "192.168.1.5", port: "7717", spkiPin: "A".repeat(43) + "=" };

  it("passes a complete draft", () => {
    expect(validateDraft(ok, "0123456789abcdef", 16)).toEqual([]);
  });

  it("names each field that is wrong, in form order", () => {
    const problems = validateDraft({ host: "", port: "no", spkiPin: "" }, "", 16);
    expect(problems.map((p) => p.field)).toEqual(["host", "port", "pin", "key"]);
  });

  it("explains a too-short key as a daemon that never started listening", () => {
    // insecureMinKeyLen is enforced at LISTENER STARTUP, so a short key is not a
    // rejection — it is silence that looks exactly like a wrong host.
    const [p] = validateDraft(ok, "short", 16);
    expect(p.field).toBe("key");
    expect(p.message).toContain("16");
    expect(p.message).toContain("never starts listening");
  });
});

describe("endpointId", () => {
  it("is stable and case-insensitive", () => {
    expect(endpointId("MarsMac.local", 7717)).toBe("marsmac.local:7717");
    expect(endpointId(" 10.0.0.1 ", 7717)).toBe("10.0.0.1:7717");
  });

  it("separates two daemons so one's key is never tried against the other", () => {
    expect(endpointId("10.0.0.1", 7717)).not.toBe(endpointId("10.0.0.2", 7717));
    expect(endpointId("10.0.0.1", 7717)).not.toBe(endpointId("10.0.0.1", 7718));
  });
});
