// Pure helpers of the phone's Git screen (screens/Git.svelte). The repository
// logic itself is the desktop's `ProjectGitState`, reused whole; only what a
// 390pt-wide list needs to say differently lives here.

import type { GitFile, GitSnapshot } from "$lib/projectgit";

export type FileTone = "good" | "warn" | "bad" | "info" | "faint";

/** The one-letter status of a file in its group, and the colour that letter earns. */
export function fileStatus(file: GitFile, staged: boolean): { letter: string; tone: FileTone } {
  if (file.conflict) return { letter: "!", tone: "bad" };
  if (!staged && file.untracked) return { letter: "U", tone: "good" };
  const code = staged ? file.index : file.worktree;
  switch (code) {
    case "A":
      return { letter: "A", tone: "good" };
    case "D":
      return { letter: "D", tone: "bad" };
    case "R":
    case "C":
      return { letter: code, tone: "info" };
    case "M":
    case "T":
      return { letter: "M", tone: "warn" };
    default:
      return { letter: code && code !== " " ? code : "·", tone: "faint" };
  }
}

/** A path as a file name and the folder it sits in, so the name never truncates first. */
export function splitPath(path: string): { name: string; dir: string } {
  const at = path.lastIndexOf("/");
  return at < 0 ? { name: path, dir: "" } : { name: path.slice(at + 1), dir: path.slice(0, at) };
}

export function shortSha(sha: string): string {
  return sha.slice(0, 7);
}

/** How far the checked-out branch is from its upstream, from the snapshot's own ref list. */
export function aheadBehind(snapshot: GitSnapshot | null): { ahead: number; behind: number } {
  const current = snapshot?.refs.find((r) => r.current && !r.remote);
  return { ahead: current?.ahead ?? 0, behind: current?.behind ?? 0 };
}

/** The checkouts a phone may switch to: not sandboxed, not prunable. */
export function pickableWorktrees(snapshot: GitSnapshot | null): GitSnapshot["worktrees"] {
  return (snapshot?.worktrees ?? []).filter((w) => !w.blocked && !w.prunable);
}

/** History is asked for in pages of this many commits, never past MAX_HISTORY. */
export const HISTORY_PAGE = 50;
export const MAX_HISTORY = 300;
export function nextLimit(limit: number): number {
  return Math.min(limit + HISTORY_PAGE * 2, MAX_HISTORY);
}
