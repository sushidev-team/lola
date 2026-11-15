// Adapter between the phone router and the desktop `$lib/nav.svelte` singleton
// that the shared components read. Spec: docs/mobile/04-client.md §4.2.
//
// Pure functions over a structural nav type: the host component calls them from
// its own `$effect`s, so this file needs no runes and tests need no singleton.

import type { Route, SheetName } from "./router.svelte";

/** The slice of the desktop nav the phone reads or writes. */
export interface DeskNav {
  view: string;
  selectedId: string;
  focusedTerm: string;
  overlay: string | null;
  markSeen(id: string): void;
  closeOverlay(): void;
}

/** Route to desktop nav: a session route focuses it, leaving clears the focus. */
export function applyRoute(route: Route, nav: DeskNav): void {
  if (route.name === "session") {
    nav.view = "cockpit";
    nav.selectedId = route.id;
    nav.focusedTerm = route.id;
  } else if (nav.focusedTerm !== "") {
    nav.focusedTerm = "";
  }
}

/** The sheet a desktop overlay stands for, or "" when it has none. */
export function sheetForOverlay(nav: DeskNav): SheetName {
  return nav.overlay === "pr-review" ? "reviewPackage" : "";
}

/** Closing the review-package sheet closes the overlay behind it. */
export function sheetClosed(nav: DeskNav, closed: SheetName): void {
  if (closed === "reviewPackage" && nav.overlay === "pr-review") nav.closeOverlay();
}

/** The route a component-driven focus change (Fork, openSession) asks for, or null. */
export function followFocus(route: Route, nav: DeskNav): Route | null {
  if (route.name !== "session") return null;
  if (nav.focusedTerm === "" || nav.focusedTerm === route.id) return null;
  return { name: "session", id: nav.focusedTerm, seg: "conversation" };
}

/** Marks the session seen while its Conversation segment is on screen. */
export function markConversationSeen(route: Route, nav: DeskNav): void {
  if (route.name === "session" && route.seg === "conversation") nav.markSeen(route.id);
}
