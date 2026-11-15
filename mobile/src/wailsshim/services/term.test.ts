import { beforeEach, expect, it, vi } from "vitest";

const sub = { write: vi.fn(async () => {}), resize: vi.fn(async (_cols: number, _rows: number) => {}), scroll: vi.fn(async () => {}) };
vi.mock("../bridge", () => ({
  bridge: {
    request: vi.fn(),
    attachPane: vi.fn(async (name: string) => name),
    detachPane: vi.fn(async () => {}),
    resizePane: vi.fn(async (name: string, cols: number, rows: number) => {
      if (name === "live") await sub.resize(cols, rows);
    }),
    paneSubscription: vi.fn((name: string) => (name === "live" ? sub : undefined)),
  },
}));

import { bridge } from "../bridge";
import * as Service from "./term";
import { describeRoutes } from "./testkit";

const request = vi.mocked(bridge.request);
beforeEach(() => {
  vi.clearAllMocks();
});

describeRoutes("TermService", Service, Service.ROUTES);

it("captures a pane through cmd=pane and returns its text", async () => {
  request.mockResolvedValueOnce({ text: "hi" });
  await expect(Service.Capture("s1", 40)).resolves.toBe("hi");
  expect(request.mock.calls[0].slice(1)).toEqual(["pane", { session: "s1", lines: 40 }]);
});

it("omits a pane that cannot be captured instead of failing the batch", async () => {
  request.mockImplementation(async (_m, _c, f) => {
    if ((f as { session: string }).session === "bad") throw new Error("gone");
    return { text: "ok" };
  });
  await expect(Service.CaptureMany(["a", "bad"], 10)).resolves.toEqual({ a: "ok" });
  await expect(Service.CaptureMany(null, 10)).resolves.toEqual({});
});

it("routes input to a live subscription and drops it for a detached pane", async () => {
  await Service.Write("live", "x");
  await Service.Resize("live", 80, 24);
  await Service.Scroll("live", -3);
  expect(sub.write).toHaveBeenCalledWith("x");
  expect(sub.resize).toHaveBeenCalledWith(80, 24);
  expect(sub.scroll).toHaveBeenCalledWith(-3);
  await expect(Service.Write("gone", "x")).resolves.toBeUndefined();
});

it("DetachAll detaches exactly the panes bound through Attach", async () => {
  await Service.Attach("a", 80, 24);
  await Service.Attach("b", 80, 24);
  await Service.Detach("a");
  vi.mocked(bridge.detachPane).mockClear();
  await Service.DetachAll();
  expect(bridge.detachPane).toHaveBeenCalledTimes(1);
  expect(bridge.detachPane).toHaveBeenCalledWith("b");
  vi.mocked(bridge.detachPane).mockClear();
  await Service.DetachAll();
  expect(bridge.detachPane).not.toHaveBeenCalled();
});
