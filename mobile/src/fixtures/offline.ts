import { snapshot } from "./busy-snapshot";
import type { Fixture } from "./types";

// The last snapshot stays on screen while the Mac cannot be reached.
export const fixture: Fixture = {
  ...snapshot("offline"),
  routes: ["/inbox"],
  link: { kind: "down", code: "unreachable" },
};
