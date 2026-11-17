import { describe, it, expect } from "vitest";
import { appState, focusLabel, otherActive, appPr, unrouted, missingMembers } from "./stackapps";

const apps = [
  { name: "web", label: "Web", branch: "b", number: 9, url: "u9", delivery: "ci_failed" },
  { name: "api", branch: "b", touched: true },
  { name: "docs", branch: "b" },
  { name: "site", branch: "b", number: 4 },
];

describe("stackapps", () => {
  it("names the focus app and counts the OTHER apps with news", () => {
    const s = { stack: true, apps, focusApp: "web" };
    expect(focusLabel(s)).toBe("Web");
    expect(otherActive(s)).toBe(2); // api (changes) + site (PR); docs is untouched
  });

  it("says nothing for a plain session", () => {
    expect(focusLabel({ apps, focusApp: "web" })).toBe("");
    expect(otherActive({ apps, focusApp: "web" })).toBe(0);
  });

  it("classifies an app and shapes its PR for the badge", () => {
    expect(apps.map(appState)).toEqual(["pr", "changes", "untouched", "pr"]);
    expect(appPr(apps[0])).toEqual({ prNumber: 9, prUrl: "u9", checks: "", review: "" });
  });

  it("calls an issue unrouted when no label selects a repository", () => {
    const members = [{ name: "web" }, { name: "api" }, { name: "docs", always: true }];
    expect(unrouted({ apps: ["docs"] }, members)).toBe(true); // only the always-included member
    expect(unrouted({ apps: [] }, members)).toBe(true);
    expect(unrouted({ apps: ["api", "docs"] }, members)).toBe(false);
    // A stack whose members all take every issue has no routing to miss; a repository has none at all.
    expect(unrouted({ apps: ["docs"] }, [{ name: "docs", always: true }])).toBe(false);
    expect(unrouted({}, undefined)).toBe(false);
  });

  it("lists the stack's members a session has not checked out", () => {
    const projects = [
      { name: "acme", kind: "stack" },
      { name: "acme-web", label: "Web", stack: "acme" },
      { name: "acme-api", stack: "acme" },
      { name: "other", stack: "elsewhere" },
    ];
    const s = { project: "acme", stack: true, apps: [{ name: "acme-web" }] };
    expect(missingMembers(s, projects)).toEqual([{ name: "acme-api", label: "acme-api" }]);
    expect(missingMembers({ ...s, stack: false }, projects)).toEqual([]);
  });
});
