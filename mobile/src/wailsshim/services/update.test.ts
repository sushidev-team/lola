import { vi } from "vitest";

vi.mock("../bridge", () => ({ bridge: { request: vi.fn() } }));

import * as Service from "./update";
import { describeRoutes } from "./testkit";

describeRoutes("UpdateService", Service, Service.ROUTES);
