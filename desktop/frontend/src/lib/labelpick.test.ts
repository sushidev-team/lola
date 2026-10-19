import { describe, it, expect } from "vitest";
import { labelIdsFor } from "./labelpick";

const opts = [
  { id: "u-web", label: "apps / website" },
  { id: "u-app", label: "apps / app · workspace" },
  { id: "u-bug", label: "Bug" },
];

describe("labelIdsFor", () => {
  it("maps names and group/name to the picker's ids, keeps the rest", () => {
    expect(labelIdsFor(["u-bug", "Apps/Website", "app", "bug", "nope"], opts)).toEqual(["u-bug", "u-web", "u-app", "u-bug", "nope"]);
    expect(labelIdsFor(null, opts)).toEqual([]);
  });
});
