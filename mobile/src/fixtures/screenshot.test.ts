import { describe, expect, it } from "vitest";
import { WireRefusalError } from "../wire";
import { previewTransport } from "./daemon";
import { loadFixture } from "./index";
import { SCREENSHOT_PNG_BASE64 } from "./screenshot";

describe("PreviewDaemon reviewPackageImage", () => {
  async function connected() {
    const fx = await loadFixture("review-package");
    const t = previewTransport(fx);
    await t.connect({ daemonId: fx.daemons[0]?.daemonId ?? "none" });
    return t;
  }

  it("serves the bundled screenshot for a package's artifact, chunked or whole", async () => {
    const t = await connected();
    const part = await t.request<{ mime: string; size: number; data: string; eof: boolean }>("reviewPackageImage", {
      session: "s5",
      args: { package: "pkg-1", artifact: "after.png", chunked: true },
    });
    expect(part.mime).toBe("image/png");
    expect(part.eof).toBe(true);
    expect(part.data).toBe(SCREENSHOT_PNG_BASE64);
    expect(part.size).toBe(atob(SCREENSHOT_PNG_BASE64).length);
    // The PNG signature: the bundled bytes are a real image.
    expect(Array.from(atob(part.data).slice(0, 4), (c) => c.charCodeAt(0))).toEqual([0x89, 0x50, 0x4e, 0x47]);

    const whole = await t.request<{ dataURL: string }>("reviewPackageImage", { session: "s5", args: { package: "pkg-1", artifact: "after.png" } });
    expect(whole.dataURL).toBe(`data:image/png;base64,${SCREENSHOT_PNG_BASE64}`);
  });

  it("refuses an artifact no package names", async () => {
    const t = await connected();
    await expect(t.request("reviewPackageImage", { session: "s5", args: { package: "pkg-1", artifact: "other.png", chunked: true } })).rejects.toBeInstanceOf(WireRefusalError);
    await expect(t.request("reviewPackageImage", { session: "s5", args: { package: "nope", artifact: "after.png" } })).rejects.toBeInstanceOf(WireRefusalError);
  });
});
