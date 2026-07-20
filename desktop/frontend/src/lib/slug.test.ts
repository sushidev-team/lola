import { describe, it, expect } from "vitest";
import { slug, slugTyping, isSlug, displayName } from "./slug";

// These cases mirror internal/config/slug_test.go one for one. When either side
// changes, both must — the Go implementation is the authority and this file is
// what catches the drift.
describe("slug", () => {
  it.each([
    ["Nori App", "nori-app"],
    ["nori-app", "nori-app"],
    ["  Nori   App  ", "nori-app"],
    ["Okane", "okane"],
    ["my_project.v2", "my_project.v2"],
    ["Ünïcødé Ãpp", "n-c-d-pp"],
    ["日本語", ""],
    ["a/b", "a-b"],
    ["../etc", "etc"],
    ["...", ""],
    ["", ""],
    ["   ", ""],
    ["---", ""],
  ])("slug(%o) === %o", (input, want) => {
    expect(slug(input)).toBe(want);
  });

  it("is idempotent and self-consistent", () => {
    for (const input of ["Nori App", "a/b", "../etc", "my_project.v2", "Okane"]) {
      const s = slug(input);
      if (s === "") continue;
      expect(slug(s)).toBe(s);
      expect(isSlug(s)).toBe(true);
    }
  });

  it("never lets a path separator survive", () => {
    // The id becomes a directory name; a "/" would escape the segment entirely.
    for (const input of ["a/b", "../../etc/passwd", "a\\b"]) {
      expect(slug(input)).not.toMatch(/[/\\]/);
    }
  });
});

describe("isSlug", () => {
  it.each(["", "Okane", "Nori App", "a/b", "-lead", "trail-", "..", "."])(
    "rejects %o",
    (input) => {
      expect(isSlug(input)).toBe(false);
    },
  );
});

describe("slugTyping", () => {
  it("keeps a trailing separator so a hyphen can be typed at all", () => {
    expect(slugTyping("nori-")).toBe("nori-");
  });

  it("converges on the slug when typed one keystroke at a time", () => {
    let typed = "";
    for (const ch of "Nori App") typed = slugTyping(typed + ch);
    expect(typed).toBe("nori-app");
  });
});

describe("displayName", () => {
  it("falls back to the id when there is no label", () => {
    expect(displayName({ name: "nori-app" })).toBe("nori-app");
    expect(displayName({ name: "nori-app", label: "" })).toBe("nori-app");
    expect(displayName({ name: "nori-app", label: "   " })).toBe("nori-app");
  });

  it("prefers the label", () => {
    expect(displayName({ name: "nori-app", label: "Nori App" })).toBe("Nori App");
  });
});
