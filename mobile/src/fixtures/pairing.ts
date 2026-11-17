import { base } from "./build";
import type { Fixture } from "./types";

// A phone that was never paired: Connect in every state it has.
export const fixture: Fixture = {
  ...base("pairing"),
  daemons: [],
  routes: [
    "/connect",
    "/connect/code",
    "/connect/refused",
    "/connect/expired",
    "/connect/failed",
    "/connect/dropped",
    "/connect/not-paired",
    "/connect/no-camera",
  ],
  // The code is the plugin's to produce; the screen shows what it was given.
  async after() {
    const { pairing } = await import("@mobile/lib/pairing.svelte");
    const { router } = await import("@mobile/lib/router.svelte");
    const until = Date.now() + 3000;
    const fill = () => {
      if (pairing.view === "code") pairing.code = "482913";
      // The web layer has no camera, so a scan route settles on "no camera"; the route
      // that asked for the scan shows the scan's own line instead.
      else if (router.top.name === "connect" && router.top.state === "scan" && pairing.view === "no-camera") {
        pairing.view = "scan";
      } else if (Date.now() < until) setTimeout(fill, 50);
    };
    fill();
  },
};
