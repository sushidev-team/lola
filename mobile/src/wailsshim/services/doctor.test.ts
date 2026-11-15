import { vi } from "vitest";

vi.mock("../bridge", () => ({ bridge: { request: vi.fn() } }));

import * as Service from "./doctor";
import { describeRoutes } from "./testkit";

describeRoutes("DoctorService", Service, Service.ROUTES);
