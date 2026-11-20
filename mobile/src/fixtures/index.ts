// The fixture registry. Each fixture is its own chunk, so the preview page loads
// only the one it was asked for.

import { FIXTURE_NAMES, type Fixture, type FixtureName } from "./types";

export { FIXTURE_NAMES, type Fixture, type FixtureName } from "./types";

const LOADERS: Record<FixtureName, () => Promise<{ fixture: Fixture }>> = {
  empty: () => import("./empty"),
  busy: () => import("./busy"),
  graph: () => import("./graph"),
  "busy-grouped": () => import("./busy-grouped"),
  permission: () => import("./permission"),
  dialog: () => import("./dialog"),
  question: () => import("./question"),
  "long-story": () => import("./long-story"),
  composer: () => import("./composer"),
  diff: () => import("./diff"),
  "merge-ready": () => import("./merge-ready"),
  "ci-red": () => import("./ci-red"),
  "review-package": () => import("./review-package"),
  terminal: () => import("./terminal"),
  offline: () => import("./offline"),
  "key-changed": () => import("./key-changed"),
  pairing: () => import("./pairing"),
  settings: () => import("./settings"),
};

export function isFixtureName(v: string): v is FixtureName {
  return (FIXTURE_NAMES as readonly string[]).includes(v);
}

export async function loadFixture(name: FixtureName): Promise<Fixture> {
  return (await LOADERS[name]()).fixture;
}

/** The fixture that serves a route, for a preview opened with `route=` and no `fixture=`. */
export async function fixtureFor(path: string): Promise<FixtureName | null> {
  const want = path.split("?")[0];
  // The richest roster answers a shared route first.
  for (const name of ["busy", ...FIXTURE_NAMES.filter((n) => n !== "busy")] as const) {
    const fx = await loadFixture(name);
    if (fx.routes.some((r) => r.split("?")[0] === want)) return name;
  }
  return null;
}
