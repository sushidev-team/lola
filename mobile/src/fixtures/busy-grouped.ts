import { fixture as busy } from "./busy";
import type { Fixture } from "./types";

// The busy roster with the Inbox grouped by project: the preference is seeded
// into storage, so the grouped list is reached by route alone.
export const fixture: Fixture = {
  ...busy,
  name: "busy-grouped",
  routes: ["/inbox", "/inbox?filter=needs", "/inbox?sheet=filter"],
  storage: { ...busy.storage, "lola.mobile.inboxGroup": "project" },
};
