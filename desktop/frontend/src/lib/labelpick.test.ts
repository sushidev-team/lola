import { describe, it, expect } from "vitest";
import { labelIdsFor } from "./labelpick";

const opts = [
  { id: "u-web", label: "apps / website" },
  { id: "u-app", label: "apps / app · workspace" },
  { id: "u-bug", label: "Bug" },
];

describe("labelIdsFor", () => {
  it("maps names and group/name to the picker's ids, keeps the rest", () => {
    expect(labelIdsFor(["u-bug", "Apps/Website", "app", "bug", "nope"], opts)).toEqual(["u-bug", "u-web", "u-app", "nope"]);
    expect(labelIdsFor(null, opts)).toEqual([]);
  });

  it("expands pickup patterns and deduplicates overlapping references", () => {
    expect(labelIdsFor(["apps/*", "u-web", "*/missing"], opts, true)).toEqual(["u-web", "u-app", "*/missing"]);
    expect(labelIdsFor(["apps/*"], opts)).toEqual(["apps/*"]);
    expect(labelIdsFor(["apps/.*"], opts, true)).toEqual(["apps/.*"]); // . is literal
  });

  it("leaves ambiguous bare names unresolved", () => {
    const labels = [...opts, { id: "other-web", label: "other / website" }];
    expect(labelIdsFor(["website", "apps/website"], labels, true)).toEqual(["website", "u-web"]);
  });
});
