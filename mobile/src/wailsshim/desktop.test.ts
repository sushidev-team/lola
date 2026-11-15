import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it, vi } from "vitest";

vi.mock("./bridge", () => ({ bridge: { request: vi.fn(async () => ({})), alive: vi.fn(() => true) } }));

import * as Desktop from "./desktop";
import { UnsupportedOnMobileError } from "./errors";
import { bridge } from "./bridge";

const GENERATED_INDEX = resolve(
  __dirname,
  "../../../desktop/frontend/bindings/github.com/sushidev-team/lola/desktop/index.ts",
);

describe("the barrel", () => {
  it("exports exactly the services the generated index exports", () => {
    const src = readFileSync(GENERATED_INDEX, "utf8");
    const names = [...src.matchAll(/import \* as (\w+Service) from/g)].map((m) => m[1]).sort();
    expect(Object.keys(Desktop).sort()).toEqual(names);
  });

  it("gives every service module its ROUTES table", () => {
    for (const svc of Object.values(Desktop)) {
      expect(typeof (svc as { ROUTES: unknown }).ROUTES).toBe("object");
    }
  });
});

describe("a call the phone cannot perform", () => {
  it("rejects with a named error and puts nothing on the wire", async () => {
    const err = await Desktop.DaemonService.StartDaemon().then(
      () => null,
      (e: unknown) => e,
    );
    expect(err).toBeInstanceOf(UnsupportedOnMobileError);
    expect(String(err)).toContain("DaemonService.StartDaemon is not available on mobile");
    expect(bridge.request).not.toHaveBeenCalled();
  });
});

describe("a forwarded call", () => {
  it("names the shim method and the daemon command", async () => {
    await Desktop.DaemonService.Sessions();
    expect(vi.mocked(bridge.request).mock.calls[0]).toEqual(["DaemonService.Sessions", "sessions"]);
  });
});
