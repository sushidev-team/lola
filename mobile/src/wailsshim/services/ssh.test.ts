import { beforeEach, expect, it, vi } from "vitest";

vi.mock("../bridge", () => ({ bridge: { request: vi.fn() } }));

import { bridge } from "../bridge";
import * as Service from "./ssh";
import { describeRoutes } from "./testkit";

const request = vi.mocked(bridge.request);
beforeEach(() => {
  request.mockReset();
  request.mockResolvedValue({});
});

describeRoutes("SSHService", Service, Service.ROUTES);

it("reads the Conversation timeline through the phone-safe command", async () => {
  const a = { session: "s1", after: 3 } as never;
  await Service.Timeline(a);
  expect(request.mock.calls[0].slice(1)).toEqual(["sessionTimeline", { args: a }]);
});

it("recalls a queued message by session and item", async () => {
  await Service.Recall("s1", "q1");
  expect(request.mock.calls[0].slice(1)).toEqual(["sshRecall", { args: { session: "s1", item: "q1" } }]);
});

it("answers an ssh card through sshAnswer", async () => {
  const a = { session: "s1" } as never;
  await Service.Answer(a);
  expect(request.mock.calls[0].slice(1)).toEqual(["sshAnswer", { args: a }]);
});
