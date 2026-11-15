import { vi } from "vitest";

vi.mock("../bridge", () => ({ bridge: { request: vi.fn() } }));

import * as Service from "./projectgit";
import { describeRoutes } from "./testkit";

describeRoutes("ProjectGitService", Service, Service.ROUTES);
