// Development links (spec §5.7): `lola-dev://open?route=<path>&sheet=<name>`.
//
// Invariants:
//  - A link grants nothing a tap would not: it names a route and a sheet from
//    the router's own grammar, nothing else.
//  - A `dev-url` link never pairs. Only the plugin's `dev-launch` route may
//    carry a `pairRef`, and the plugin, not this module, runs the pairing.
//  - Anything outside the grammar is ignored; the current screen stays.

import type { DevLinkEvent } from "lola-transport";
import { transport } from "@mobile/native";
import { isSheetName, parseLocation, type Route, type Router, type SheetName } from "./router.svelte";

export interface DevLink {
  source: DevLinkEvent["source"];
  /** The route the link named, or null to stay on the current screen. */
  route: Route | null;
  /** The sheet to open, or "". */
  sheet: SheetName;
  /** The tab the sheet opens on, when the route named one. */
  tab?: string;
  /** Present only for `dev-launch`. */
  pairRef?: string;
}

/** Reads one plugin event. `paired` collapses `/connect/*` to the inbox. */
export function devLinkOf(e: DevLinkEvent, paired: boolean): DevLink {
  const base = typeof e.route === "string" ? parseLocation(e.route, { paired }) : null;
  const own = typeof e.sheet === "string" && isSheetName(e.sheet) ? e.sheet : "";
  const link: DevLink = {
    source: e.source,
    route: base?.route ?? null,
    sheet: base && base.sheet !== "" ? base.sheet : own,
  };
  if (base?.tab) link.tab = base.tab;
  if (e.source === "dev-launch" && typeof e.pairRef === "string" && e.pairRef !== "") {
    link.pairRef = e.pairRef;
  }
  return link;
}

/** Puts the router where the link asked. A session over a tab is pushed, anything else replaces. */
export function applyDevLink(
  router: Pick<Router, "top" | "push" | "replace" | "open">,
  link: Pick<DevLink, "route" | "sheet" | "tab">,
): void {
  const { route } = link;
  if (route) {
    if (route.name === "session" && router.top.name === "tab") router.push(route);
    else router.replace(route);
  }
  if (link.sheet !== "") router.open(link.sheet, link.tab);
}

/**
 * Listens for development links. Registered at startup because the plugin
 * retains a pending link until something consumes it. Returns the teardown.
 */
export function installDevLink(
  onLink: (link: DevLink) => void,
  paired: () => boolean,
): () => void {
  let live = true;
  let handle: { remove(): unknown } | undefined;
  void transport
    .addListener("devLink", (e) => {
      if (live) onLink(devLinkOf(e, paired()));
    })
    .then((h) => {
      if (live) handle = h;
      else void h.remove();
    })
    .catch(() => {
      /* release builds compile the event out */
    });
  return () => {
    live = false;
    void handle?.remove();
  };
}
