import { beforeEach, expect, it, vi } from "vitest";

vi.mock("../bridge", () => ({ bridge: { request: vi.fn() } }));
vi.mock("../../native", () => ({ native: null }));

import { bridge } from "../bridge";
import * as Service from "./talk";
import { describeRoutes } from "./testkit";

const request = vi.mocked(bridge.request);
beforeEach(() => {
  request.mockReset();
  request.mockResolvedValue({});
});

describeRoutes("TalkService", Service, Service.ROUTES, {
  skipSend: ["AttachClipboardImage", "ReadAttachmentImage"],
});

it("passes each Conversation args object as args", async () => {
  const a = { session: "s1", text: "hi" } as never;
  await Service.Say(a);
  expect(request.mock.calls[0].slice(1)).toEqual(["agentSay", { args: a }]);
});

it("reads ended sessions without arguments", async () => {
  await Service.Ended();
  expect(request.mock.calls[0].slice(1)).toEqual(["endedSessions"]);
});
