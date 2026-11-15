import { vi } from "vitest";

vi.mock("../bridge", () => ({ bridge: { request: vi.fn() } }));

import * as Service from "./sandboxlogin";
import { describeRoutes } from "./testkit";

describeRoutes("SandboxLoginService", Service, Service.ROUTES);
