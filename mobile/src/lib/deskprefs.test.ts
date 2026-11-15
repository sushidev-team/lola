import { describe, it, expect, beforeEach } from "vitest";
import { seedDeskPrefs } from "./deskprefs";

beforeEach(() => localStorage.clear());

describe("seedDeskPrefs", () => {
  it("seeds unified diff mode when absent", () => {
    seedDeskPrefs();
    expect(localStorage.getItem("lola.diff.mode")).toBe("unified");
  });
  it("keeps a stored choice", () => {
    localStorage.setItem("lola.diff.mode", "split");
    seedDeskPrefs();
    expect(localStorage.getItem("lola.diff.mode")).toBe("split");
  });
});
