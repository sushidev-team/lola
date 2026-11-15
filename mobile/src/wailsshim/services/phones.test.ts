import { vi } from "vitest";

vi.mock("../bridge", () => ({ bridge: { request: vi.fn() } }));

import * as Service from "./phones";
import { describeRoutes } from "./testkit";

describeRoutes("PhonesService", Service, Service.ROUTES);
