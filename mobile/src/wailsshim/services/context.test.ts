import { vi } from "vitest";

vi.mock("../bridge", () => ({ bridge: { request: vi.fn() } }));

import * as Service from "./context";
import { describeRoutes } from "./testkit";

describeRoutes("ContextService", Service, Service.ROUTES);

import { expect, it } from "vitest";

it("publishes nothing and resolves", async () => {
  await expect(Service.Publish({} as never)).resolves.toBeUndefined();
});
