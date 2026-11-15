import { expect, it, vi } from "vitest";

vi.mock("../bridge", () => ({ bridge: { request: vi.fn(async () => ({})) } }));

import { bridge } from "../bridge";
import * as Service from "./hub";
import { describeRoutes } from "./testkit";

describeRoutes("HubService", Service, Service.ROUTES);

it("reads hub status as a plain hubStatus request", async () => {
  await Service.Status();
  expect(vi.mocked(bridge.request).mock.calls.at(-1)).toEqual(["HubService.Status", "hubStatus"]);
});
