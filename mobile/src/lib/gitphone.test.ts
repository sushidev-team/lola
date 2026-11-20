import { describe, expect, it } from "vitest";
import type { GitFile, GitSnapshot } from "$lib/projectgit";
import { aheadBehind, fileStatus, nextLimit, pickableWorktrees, splitPath, MAX_HISTORY } from "./gitphone";

const file = (over: Partial<GitFile>): GitFile => ({ path: "a", oldPath: "", index: " ", worktree: " ", untracked: false, conflict: false, ...over });

describe("fileStatus", () => {
  it("reads the index column for staged and the worktree column for unstaged", () => {
    expect(fileStatus(file({ index: "A" }), true)).toEqual({ letter: "A", tone: "good" });
    expect(fileStatus(file({ index: "M", worktree: "D" }), false)).toEqual({ letter: "D", tone: "bad" });
    expect(fileStatus(file({ index: "R" }), true).tone).toBe("info");
  });
  it("calls an untracked file U and a conflict !", () => {
    expect(fileStatus(file({ untracked: true, worktree: "?" }), false).letter).toBe("U");
    expect(fileStatus(file({ conflict: true }), false)).toEqual({ letter: "!", tone: "bad" });
  });
});

describe("paths and refs", () => {
  it("splits a path into name and folder", () => {
    expect(splitPath("a/b/c.ts")).toEqual({ name: "c.ts", dir: "a/b" });
    expect(splitPath("c.ts")).toEqual({ name: "c.ts", dir: "" });
  });
  it("reads ahead and behind from the current local branch", () => {
    const s = { refs: [{ current: true, remote: false, ahead: 2, behind: 1 }] } as unknown as GitSnapshot;
    expect(aheadBehind(s)).toEqual({ ahead: 2, behind: 1 });
    expect(aheadBehind(null)).toEqual({ ahead: 0, behind: 0 });
  });
  it("never offers a sandboxed or prunable checkout", () => {
    const s = { worktrees: [{ path: "/a", blocked: false, prunable: false }, { path: "/b", blocked: true, prunable: false }, { path: "/c", blocked: false, prunable: true }] } as unknown as GitSnapshot;
    expect(pickableWorktrees(s).map((w) => w.path)).toEqual(["/a"]);
  });
  it("caps history paging", () => {
    expect(nextLimit(50)).toBe(150);
    expect(nextLimit(MAX_HISTORY)).toBe(MAX_HISTORY);
  });
});
