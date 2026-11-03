import { describe, expect, it, vi, beforeEach } from "vitest";

const calls: string[] = [];
let release: () => void = () => {};
const DetachAll = vi.fn(
  () =>
    new Promise<void>((resolve) => {
      calls.push("detachAll");
      release = resolve;
    }),
);
vi.mock("@bindings/desktop", () => ({ TermService: { DetachAll: () => DetachAll() } }));

describe("termBoot", () => {
  beforeEach(() => {
    vi.resetModules();
    DetachAll.mockClear();
    calls.length = 0;
  });

  it("sweeps once per page load, however many terminals ask", async () => {
    const { termBoot } = await import("./termboot");
    const a = termBoot();
    const b = termBoot();
    await vi.waitFor(() => expect(DetachAll).toHaveBeenCalledTimes(1));
    release();
    await Promise.all([a, b]);
    await termBoot();
    expect(DetachAll).toHaveBeenCalledTimes(1);
  });

  it("does not resolve before the sweep finishes, so an attach cannot be overtaken", async () => {
    const { termBoot } = await import("./termboot");
    let done = false;
    const p = termBoot().then(() => {
      done = true;
    });
    await vi.waitFor(() => expect(DetachAll).toHaveBeenCalled());
    await Promise.resolve();
    expect(done).toBe(false);
    release();
    await p;
    expect(done).toBe(true);
  });

  it("swallows a failing or missing binding", async () => {
    DetachAll.mockImplementationOnce(() => {
      throw new Error("no such method");
    });
    const { termBoot } = await import("./termboot");
    await expect(termBoot()).resolves.toBeUndefined();
  });
});
