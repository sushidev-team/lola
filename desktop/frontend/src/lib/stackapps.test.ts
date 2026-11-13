import { describe, it, expect } from "vitest";
import { appState, focusLabel, otherActive, appPr } from "./stackapps";

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
});
