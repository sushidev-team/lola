// ProjectGitService: one function per generated method, routed as ROUTES says.
import type * as Gen from "@bindings-generated/desktop";
import type * as M from "@bindings/desktop/models";
import type * as GD from "@bindings/internal/gitdiff/models";
import type * as PG from "@bindings/internal/projectgit/models";
import { unsupported } from "../errors";
import { MAC_ONLY } from "./reasons";
import type { Routes } from "./routes";

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

/** PLATFORM. */
export function Commit(_project: string, _worktree: string, _message: string): Promise<void> {
  return unsupported("ProjectGitService.Commit", MAC_ONLY);
}

/** PLATFORM. */
export function CreateBranch(_project: string, _worktree: string, _name: string, _startSHA: string, _checkout: boolean): Promise<PG.BranchResult> {
  return unsupported("ProjectGitService.CreateBranch", MAC_ONLY);
}

/** PLATFORM. */
export function CreatePullRequest(_project: string, _worktree: string, _request: PG.PRCreateRequest): Promise<PG.PRResult> {
  return unsupported("ProjectGitService.CreatePullRequest", MAC_ONLY);
}

/** PLATFORM. */
export function CreateWorktree(_project: string, _worktree: string, _source: string, _sha: string, _path: string, _newBranch: string): Promise<PG.BranchResult> {
  return unsupported("ProjectGitService.CreateWorktree", MAC_ONLY);
}

/** PLATFORM. */
export function Diff(_project: string, _worktree: string, _$from: string, _to: string, _area: string, _path: string): Promise<GD.Result> {
  return unsupported("ProjectGitService.Diff", MAC_ONLY);
}

/** PLATFORM. */
export function DiscardUnstaged(_project: string, _worktree: string, _token: string): Promise<void> {
  return unsupported("ProjectGitService.DiscardUnstaged", MAC_ONLY);
}

/** PLATFORM. */
export function DraftCommitMessage(_project: string, _worktree: string, _draft: string): Promise<string> {
  return unsupported("ProjectGitService.DraftCommitMessage", MAC_ONLY);
}

/** PLATFORM. */
export function DraftPullRequest(_project: string, _worktree: string, _selection: PG.PRSelection, _title: string, _body: string): Promise<string> {
  return unsupported("ProjectGitService.DraftPullRequest", MAC_ONLY);
}

/** PLATFORM. */
export function Fetch(_project: string, _worktree: string): Promise<void> {
  return unsupported("ProjectGitService.Fetch", MAC_ONLY);
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

/** PLATFORM. */
export function PrepareDiscard(_project: string, _worktree: string): Promise<PG.DiscardPlan> {
  return unsupported("ProjectGitService.PrepareDiscard", MAC_ONLY);
}

/** PLATFORM. */
export function PreviewPullRequest(_project: string, _worktree: string, _selection: PG.PRSelection): Promise<PG.PRPreview> {
  return unsupported("ProjectGitService.PreviewPullRequest", MAC_ONLY);
}

/** PLATFORM. */
export function Pull(_project: string, _worktree: string): Promise<void> {
  return unsupported("ProjectGitService.Pull", MAC_ONLY);
}

/** PLATFORM. */
export function PullRequestOptions(_project: string, _worktree: string, _remote: string, _baseRepo: string): Promise<PG.PROptions> {
  return unsupported("ProjectGitService.PullRequestOptions", MAC_ONLY);
}

/** PLATFORM. */
export function Push(_project: string, _worktree: string): Promise<void> {
  return unsupported("ProjectGitService.Push", MAC_ONLY);
}

/** PLATFORM. */
export function Snapshot(_project: string, _worktree: string, _ref: string, _limit: number): Promise<PG.Snapshot> {
  return unsupported("ProjectGitService.Snapshot", MAC_ONLY);
}

/** PLATFORM. */
export function Stage(_project: string, _worktree: string, _paths: string[] | null): Promise<PG.File[] | null> {
  return unsupported("ProjectGitService.Stage", MAC_ONLY);
}

/** PLATFORM. */
export function StartAICommit(_project: string, _worktree: string, _token: string, _includeStaged: boolean): Promise<M.AICommitProgress> {
  return unsupported("ProjectGitService.StartAICommit", MAC_ONLY);
}

/** PLATFORM. */
export function StashApply(_project: string, _worktree: string, _sha: string, _expectedHead: string, _pop: boolean): Promise<void> {
  return unsupported("ProjectGitService.StashApply", MAC_ONLY);
}

/** PLATFORM. */
export function StashDrop(_project: string, _worktree: string, _sha: string): Promise<void> {
  return unsupported("ProjectGitService.StashDrop", MAC_ONLY);
}

/** PLATFORM. */
export function StashPush(_project: string, _worktree: string, _message: string, _expectedHead: string): Promise<void> {
  return unsupported("ProjectGitService.StashPush", MAC_ONLY);
}

/** PLATFORM. */
export function Stashes(_project: string, _worktree: string): Promise<PG.Stash[] | null> {
  return unsupported("ProjectGitService.Stashes", MAC_ONLY);
}

/** PLATFORM. */
export function Unstage(_project: string, _worktree: string, _paths: string[] | null): Promise<PG.File[] | null> {
  return unsupported("ProjectGitService.Unstage", MAC_ONLY);
}

export const ROUTES: Routes<typeof Gen.ProjectGitService> = {
  AICommitStatus: { kind: "platform", reason: MAC_ONLY },
  Amend: { kind: "platform", reason: MAC_ONLY },
  AmendNeedsConfirmation: { kind: "platform", reason: MAC_ONLY },
  BranchAction: { kind: "platform", reason: MAC_ONLY },
  CachedIdentities: { kind: "platform", reason: MAC_ONLY },
  CanFastForward: { kind: "platform", reason: MAC_ONLY },
  CancelAICommit: { kind: "platform", reason: MAC_ONLY },
  Commit: { kind: "platform", reason: MAC_ONLY },
  CreateBranch: { kind: "platform", reason: MAC_ONLY },
  CreatePullRequest: { kind: "platform", reason: MAC_ONLY },
  CreateWorktree: { kind: "platform", reason: MAC_ONLY },
  Diff: { kind: "platform", reason: MAC_ONLY },
  DiscardUnstaged: { kind: "platform", reason: MAC_ONLY },
  DraftCommitMessage: { kind: "platform", reason: MAC_ONLY },
  DraftPullRequest: { kind: "platform", reason: MAC_ONLY },
  Fetch: { kind: "platform", reason: MAC_ONLY },
  Identities: { kind: "platform", reason: MAC_ONLY },
  LastCommitMessage: { kind: "platform", reason: MAC_ONLY },
  PrepareAICommit: { kind: "platform", reason: MAC_ONLY },
  PrepareDiscard: { kind: "platform", reason: MAC_ONLY },
  PreviewPullRequest: { kind: "platform", reason: MAC_ONLY },
  Pull: { kind: "platform", reason: MAC_ONLY },
  PullRequestOptions: { kind: "platform", reason: MAC_ONLY },
  Push: { kind: "platform", reason: MAC_ONLY },
  Snapshot: { kind: "platform", reason: MAC_ONLY },
  Stage: { kind: "platform", reason: MAC_ONLY },
  StartAICommit: { kind: "platform", reason: MAC_ONLY },
  StashApply: { kind: "platform", reason: MAC_ONLY },
  StashDrop: { kind: "platform", reason: MAC_ONLY },
  StashPush: { kind: "platform", reason: MAC_ONLY },
  Stashes: { kind: "platform", reason: MAC_ONLY },
  Unstage: { kind: "platform", reason: MAC_ONLY },
};
