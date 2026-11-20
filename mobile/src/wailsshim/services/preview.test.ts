import { vi } from "vitest";

vi.mock("../bridge", () => ({ bridge: { request: vi.fn() } }));

import * as Service from "./preview";
import { describeRoutes } from "./testkit";

describeRoutes("PreviewService", Service, Service.ROUTES);
