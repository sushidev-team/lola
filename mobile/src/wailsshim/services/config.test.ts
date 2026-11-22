import { vi } from "vitest";

vi.mock("../bridge", () => ({ bridge: { request: vi.fn() } }));

import * as Service from "./config";
import { describeRoutes } from "./testkit";

describeRoutes("ConfigService", Service, Service.ROUTES);

import { describe, expect, it } from "vitest";

describe("ConfigService locals", () => {
  it("answers ConfigExists true so a phone never meets the setup wizard", async () => {
    await expect(Service.ConfigExists()).resolves.toBe(true);
  });

  it("skips the mobile login prompt for sandbox sessions and tabs", async () => {
    await expect(Service.SandboxLoginNeeded("p", "claude")).resolves.toBe("");
    await expect(Service.SandboxTabLoginNeeded("p", "claude")).resolves.toBe("");
  });

  it("leaves the appearance methods unexported, because shared code probes them", () => {
    const mod = Service as Record<string, unknown>;
    for (const name of ["GetAppearance", "SetAppearance", "GetTheme", "SetTheme", "PreviewTheme", "Themes"]) {
      expect(mod[name], name).toBeUndefined();
    }
  });
});
