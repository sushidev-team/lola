// ProjectGitService: one function per generated method, routed as ROUTES says.
import type * as Gen from "@bindings-generated/desktop";
import type * as M from "@bindings/desktop/models";
import type * as GD from "@bindings/internal/gitdiff/models";
import type * as PG from "@bindings/internal/projectgit/models";
import { bridge } from "../bridge";
import { unsupported } from "../errors";
import { MAC_ONLY } from "./reasons";
import type { Routes } from "./routes";

// One repository request. The phone holds no git credentials: the daemon runs it
// on the Mac, on a checkout the project's repository registers (docs/mobile/01 §8.2).
// The Mac may run hooks, signing helpers and the network for up to two minutes
// (projectgit.Context), so a write waits longer than that; the default 15 s
// would report failure for an action that then succeeds, unacknowledged.
const READ_TIMEOUT_MS = 45_000;
const WRITE_TIMEOUT_MS = 135_000;
function git<T>(method: string, cmd: string, args: Record<string, unknown>): Promise<T> {
  const timeoutMs = cmd === "projectGit" ? READ_TIMEOUT_MS : WRITE_TIMEOUT_MS;
  return bridge.request<T>(`ProjectGitService.${method}`, cmd, { args }, { timeoutMs });
}

/** PLATFORM. */
export function AICommitStatus(_id: string): Promise<M.AICommitProgress> {
  return unsupported("ProjectGitService.AICommitStatus", MAC_ONLY);
}

/** PLATFORM. */
export function Amend(_project: string, _worktree: string, _message: string, _expectedHead: string): Promise<void> {
  return unsupported("ProjectGitService.Amend", MAC_ONLY);
}

/** PLATFORM. */
export function AmendNeedsConfirmation(_project: string, _worktree: string, _expectedHead: string): Promise<boolean> {
  return unsupported("ProjectGitService.AmendNeedsConfirmation", MAC_ONLY);
}

/** PLATFORM. */
export function BranchAction(_project: string, _worktree: string, _request: PG.BranchRequest): Promise<PG.BranchResult> {
  return unsupported("ProjectGitService.BranchAction", MAC_ONLY);
}

/** PLATFORM. */
export function BranchPullRequests(_project: string, _worktree: string): Promise<PG.BranchPR[] | null> {
  return unsupported("ProjectGitService.BranchPullRequests", MAC_ONLY);
}

/** PLATFORM. */
export function CachedIdentities(_project: string, _shas: string[] | null): Promise<PG.Identities> {
  return unsupported("ProjectGitService.CachedIdentities", MAC_ONLY);
}

/** PLATFORM. */
export function CanFastForward(_project: string, _worktree: string, _request: PG.BranchRequest): Promise<boolean> {
  return unsupported("ProjectGitService.CanFastForward", MAC_ONLY);
}

/** PLATFORM. */
export function CancelAICommit(_id: string): Promise<void> {
  return unsupported("ProjectGitService.CancelAICommit", MAC_ONLY);
}

/** FORWARDED. */
export function Commit(project: string, worktree: string, message: string): Promise<void> {
  return git<void>("Commit", "projectGitAct", { op: "commit", project, worktree, message });
}

/** FORWARDED. */
export function CreateBranch(project: string, worktree: string, name: string, startSHA: string, checkout: boolean): Promise<PG.BranchResult> {
  return git<PG.BranchResult>("CreateBranch", "projectGitAct", { op: "createBranch", project, worktree, name, sha: startSHA, checkout });
}

/** PLATFORM. */
export function CreatePullRequest(_project: string, _worktree: string, _request: PG.PRCreateRequest): Promise<PG.PRResult> {
  return unsupported("ProjectGitService.CreatePullRequest", MAC_ONLY);
}

/** PLATFORM. */
export function CreateWorktree(_project: string, _worktree: string, _source: string, _sha: string, _path: string, _newBranch: string): Promise<PG.BranchResult> {
  return unsupported("ProjectGitService.CreateWorktree", MAC_ONLY);
}

/** FORWARDED. */
export function Diff(project: string, worktree: string, $from: string, to: string, area: string, path: string): Promise<GD.Result> {
  return git<GD.Result>("Diff", "projectGit", { op: "diff", project, worktree, from: $from, to, area, path });
}

/** FORWARDED. */
export function DiscardUnstaged(project: string, worktree: string, token: string): Promise<void> {
  return git<void>("DiscardUnstaged", "projectGitDiscard", { op: "discard", project, worktree, token });
}

/** FORWARDED. */
export function DiscardPaths(project: string, worktree: string, token: string, paths: string[] | null): Promise<void> {
  return git<void>("DiscardPaths", "projectGitDiscard", { op: "discard", project, worktree, token, paths });
}

/** PLATFORM. */
export function DraftCommitMessage(_project: string, _worktree: string, _draft: string): Promise<string> {
  return unsupported("ProjectGitService.DraftCommitMessage", MAC_ONLY);
}

/** PLATFORM. */
export function DraftPullRequest(_project: string, _worktree: string, _selection: PG.PRSelection, _title: string, _body: string): Promise<string> {
  return unsupported("ProjectGitService.DraftPullRequest", MAC_ONLY);
}

/** FORWARDED. */
export function Fetch(project: string, worktree: string): Promise<void> {
  return git<void>("Fetch", "projectGitAct", { op: "fetch", project, worktree });
}

/** PLATFORM. */
export function Identities(_project: string, _shas: string[] | null): Promise<PG.Identities> {
  return unsupported("ProjectGitService.Identities", MAC_ONLY);
}

/** PLATFORM. */
export function LastCommitMessage(_project: string, _worktree: string, _expectedHead: string): Promise<string> {
  return unsupported("ProjectGitService.LastCommitMessage", MAC_ONLY);
}

/** PLATFORM. */
export function PrepareAICommit(_project: string, _worktree: string): Promise<PG.AICommitPreparation> {
  return unsupported("ProjectGitService.PrepareAICommit", MAC_ONLY);
}

/** FORWARDED. */
export function PrepareDiscard(project: string, worktree: string): Promise<PG.DiscardPlan> {
  return git<PG.DiscardPlan>("PrepareDiscard", "projectGitDiscard", { op: "prepareDiscard", project, worktree });
}

/** FORWARDED. */
export function PrepareDiscardPaths(project: string, worktree: string, paths: string[] | null): Promise<PG.DiscardPlan> {
  return git<PG.DiscardPlan>("PrepareDiscardPaths", "projectGitDiscard", { op: "prepareDiscard", project, worktree, paths });
}

/** PLATFORM. */
export function PreviewPullRequest(_project: string, _worktree: string, _selection: PG.PRSelection): Promise<PG.PRPreview> {
  return unsupported("ProjectGitService.PreviewPullRequest", MAC_ONLY);
}

/** FORWARDED. */
export function Pull(project: string, worktree: string): Promise<void> {
  return git<void>("Pull", "projectGitAct", { op: "pull", project, worktree });
}

/** PLATFORM. */
export function PullRequestOptions(_project: string, _worktree: string, _remote: string, _baseRepo: string): Promise<PG.PROptions> {
  return unsupported("ProjectGitService.PullRequestOptions", MAC_ONLY);
}

/** FORWARDED. */
export function Push(project: string, worktree: string): Promise<void> {
  return git<void>("Push", "projectGitAct", { op: "push", project, worktree });
}

/** FORWARDED. */
export function Snapshot(project: string, worktree: string, ref: string, limit: number): Promise<PG.Snapshot> {
  return git<PG.Snapshot>("Snapshot", "projectGit", { op: "snapshot", project, worktree, ref, limit });
}

/** FORWARDED. */
export function Stage(project: string, worktree: string, paths: string[] | null): Promise<PG.File[] | null> {
  return git<PG.File[] | null>("Stage", "projectGitAct", { op: "stage", project, worktree, paths });
}

/** PLATFORM. */
export function StartAICommit(_project: string, _worktree: string, _token: string, _includeStaged: boolean): Promise<M.AICommitProgress> {
  return unsupported("ProjectGitService.StartAICommit", MAC_ONLY);
}

/** FORWARDED. */
export function StashApply(project: string, worktree: string, sha: string, expectedHead: string, pop: boolean): Promise<void> {
  return git<void>("StashApply", "projectGitAct", { op: "stashApply", project, worktree, sha, head: expectedHead, pop });
}

/** FORWARDED. */
export function StashDrop(project: string, worktree: string, sha: string): Promise<void> {
  return git<void>("StashDrop", "projectGitDiscard", { op: "stashDrop", project, worktree, sha });
}

/** PLATFORM. */
export function Uncommit(_project: string, _worktree: string, _expectedHead: string): Promise<void> {
  return unsupported("ProjectGitService.Uncommit", MAC_ONLY);
}

/** FORWARDED. */
export function StashPush(project: string, worktree: string, message: string, expectedHead: string): Promise<void> {
  return git<void>("StashPush", "projectGitAct", { op: "stashPush", project, worktree, message, head: expectedHead });
}

/** FORWARDED. */
export function Stashes(project: string, worktree: string): Promise<PG.Stash[] | null> {
  return git<PG.Stash[] | null>("Stashes", "projectGit", { op: "stashes", project, worktree });
}

/** FORWARDED. */
export function Unstage(project: string, worktree: string, paths: string[] | null): Promise<PG.File[] | null> {
  return git<PG.File[] | null>("Unstage", "projectGitAct", { op: "unstage", project, worktree, paths });
}

export const ROUTES: Routes<typeof Gen.ProjectGitService> = {
  AICommitStatus: { kind: "platform", reason: MAC_ONLY },
  Amend: { kind: "platform", reason: MAC_ONLY },
  AmendNeedsConfirmation: { kind: "platform", reason: MAC_ONLY },
  BranchAction: { kind: "platform", reason: MAC_ONLY },
  BranchPullRequests: { kind: "platform", reason: MAC_ONLY },
  CachedIdentities: { kind: "platform", reason: MAC_ONLY },
  CanFastForward: { kind: "platform", reason: MAC_ONLY },
  CancelAICommit: { kind: "platform", reason: MAC_ONLY },
  Commit: { kind: "fwd", cmd: "projectGitAct" },
  CreateBranch: { kind: "fwd", cmd: "projectGitAct" },
  CreatePullRequest: { kind: "platform", reason: MAC_ONLY },
  CreateWorktree: { kind: "platform", reason: MAC_ONLY },
  Diff: { kind: "fwd", cmd: "projectGit" },
  DiscardUnstaged: { kind: "fwd", cmd: "projectGitDiscard" },
  DiscardPaths: { kind: "fwd", cmd: "projectGitDiscard" },
  DraftCommitMessage: { kind: "platform", reason: MAC_ONLY },
  DraftPullRequest: { kind: "platform", reason: MAC_ONLY },
  Fetch: { kind: "fwd", cmd: "projectGitAct" },
  Identities: { kind: "platform", reason: MAC_ONLY },
  LastCommitMessage: { kind: "platform", reason: MAC_ONLY },
  PrepareAICommit: { kind: "platform", reason: MAC_ONLY },
  PrepareDiscard: { kind: "fwd", cmd: "projectGitDiscard" },
  PrepareDiscardPaths: { kind: "fwd", cmd: "projectGitDiscard" },
  PreviewPullRequest: { kind: "platform", reason: MAC_ONLY },
  Pull: { kind: "fwd", cmd: "projectGitAct" },
  PullRequestOptions: { kind: "platform", reason: MAC_ONLY },
  Push: { kind: "fwd", cmd: "projectGitAct" },
  Snapshot: { kind: "fwd", cmd: "projectGit" },
  Stage: { kind: "fwd", cmd: "projectGitAct" },
  StartAICommit: { kind: "platform", reason: MAC_ONLY },
  StashApply: { kind: "fwd", cmd: "projectGitAct" },
  StashDrop: { kind: "fwd", cmd: "projectGitDiscard" },
  StashPush: { kind: "fwd", cmd: "projectGitAct" },
  Stashes: { kind: "fwd", cmd: "projectGit" },
  Unstage: { kind: "fwd", cmd: "projectGitAct" },
  Uncommit: { kind: "platform", reason: MAC_ONLY },
};
