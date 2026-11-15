import { snapshot } from "./busy-snapshot";
import type { Fixture } from "./types";

// The last snapshot stays on screen; the Mac's key is not the one that was pinned.
export const fixture: Fixture = {
  ...snapshot("key-changed"),
  routes: ["/inbox"],
  link: { kind: "down", code: "key_changed" },
};
