import { vi } from "vitest";

vi.mock("../bridge", () => ({ bridge: { request: vi.fn() } }));

import * as Service from "./linear";
import { describeRoutes } from "./testkit";

describeRoutes("LinearService", Service, Service.ROUTES);
