import { describe, expect, it } from "vitest";
import { BANG_NOTE, isBang, slashEnter, slashRows, slashTable, slashWord, unknownSlashNote } from "./slash";

describe("input rules", () => {
  it("prefix never sends", () => {
    expect(slashEnter("/comp", "claude")).toEqual({ act: "fill", text: "/compact" });
    expect(slashEnter("/co", "claude", "/config")).toEqual({ act: "fill", text: "/config" });
    // Whatever is typed, a prefix of a known command never answers with a send.
    for (const t of ["/c", "/co", "/com", "/compac", "/h", "/term"]) expect(slashEnter(t, "claude").act).toBe("fill");
  });

  it("screen command opens Terminal and types nothing", () => {
    for (const w of ["/config", "/usage", "/cost", "/help", "/model", "/vim", "/terminal-setup"]) {
      expect(slashEnter(w, "claude")).toEqual({ act: "terminal", word: w });
    }
    expect(slashEnter("/model opus", "claude")).toEqual({ act: "terminal", word: "/model" });
  });

  it("sends a known inline command exact", () => {
    expect(slashEnter("/compact", "claude")).toEqual({ act: "send" });
    expect(slashEnter("/rename login work", "claude")).toEqual({ act: "send" });
  });

  it("unknown warns then sends on second press", () => {
    expect(slashEnter("/nonsense do a thing", "claude")).toEqual({ act: "warn", word: "/nonsense" });
    expect(slashEnter("/nonsense do a thing", "claude", "", "/nonsense")).toEqual({ act: "send" });
    // A different word has not been warned about.
    expect(slashEnter("/other", "claude", "", "/nonsense")).toEqual({ act: "warn", word: "/other" });
    expect(unknownSlashNote("/nonsense", "Claude")).toBe("lola doesn't know /nonsense. Claude drops the text if it doesn't either.");
  });

  it("path is not flagged", () => {
    expect(slashWord("/tmp/foo.log is broken")).toBe("");
    expect(slashEnter("/tmp/foo.log is broken", "claude")).toEqual({ act: "send" });
    expect(slashEnter("/etc/hosts", "claude")).toEqual({ act: "send" });
    expect(slashRows("/tmp/foo", "claude")).toEqual([]);
  });

  it("codex table is empty", () => {
    for (const a of ["codex", "opencode"]) {
      expect(slashTable(a)).toEqual({ inline: [], screen: [] });
      expect(slashRows("/", a)).toEqual([]);
      expect(slashEnter("/config", a)).toEqual({ act: "warn", word: "/config" });
    }
  });

  it("only column 0 counts, and the list closes at the first space", () => {
    expect(slashRows(" /comp", "claude")).toEqual([]);
    expect(slashEnter("please run /compact", "claude")).toEqual({ act: "send" });
    expect(slashRows("/compact now", "claude")).toEqual([]);
    expect(slashRows("/co", "claude").map((r) => r.name)).toEqual(["/compact", "/context", "/config", "/cost"]);
    expect(slashRows("/config", "claude")).toEqual([{ name: "/config", screen: true }]);
  });

  it("a leading bang is named, and the daemon neutralizes it", () => {
    expect(isBang("!rm -rf /")).toBe(true);
    expect(isBang("  !x")).toBe(true);
    expect(isBang("wow!")).toBe(false);
    expect(BANG_NOTE).toContain("Sent as text");
  });
});
