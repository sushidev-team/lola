// DaemonService: one function per generated method, routed as ROUTES says.
import type * as Gen from "@bindings-generated/desktop";
import type * as P from "@bindings/internal/protocol";
import type * as M from "@bindings/desktop/models";
import type * as G from "@bindings/internal/goal/models";
import type * as GP from "@bindings/internal/goalplan/models";
import type * as K from "@bindings/internal/knowledge/models";
import { UnsupportedOnMobileError, unsupported } from "../errors";
import { OpenOnPhoneError, openOnPhone } from "../../lib/openurl";
import { WireError, normalizePanesData } from "../../wire";
import type { PaneCloseData, PaneResizeData, PanesData, RawPanesData, ShellCreateData } from "../../wire";
import { bridge } from "../bridge";
import { MAC_LINK, MAC_ONLY } from "./reasons";
import { ReadReviewImage } from "./reviewimage";
import type { Routes } from "./routes";

/** FORWARDED: cmd=addressPRFeedback. */
export function AddressPRFeedback(session: string, $number: number): Promise<P.AgentSayData> {
  return bridge.request<P.AgentSayData>("DaemonService.AddressPRFeedback", "addressPRFeedback", { session, args: { number: $number } });
}

/** LOCAL: the connection's own state; resolves false rather than throwing so an unpaired app shows its offline state. */
export function Alive(): Promise<boolean> {
  return Promise.resolve(bridge.alive());
}

/** FORWARDED: cmd=answer. */
export function Answer(session: string, text: string): Promise<void> {
  return bridge.request<void>("DaemonService.Answer", "answer", { session, text });
}

/** FORWARDED: cmd=bestOf. */
export function BestOf(a: P.BestOfArgs): Promise<P.BestOfData> {
  return bridge.request<P.BestOfData>("DaemonService.BestOf", "bestOf", { args: a });
}

/** FORWARDED: cmd=bestOfCompare. */
export function BestOfCompare(session: string): Promise<P.BestOfCompareData> {
  return bridge.request<P.BestOfCompareData>("DaemonService.BestOfCompare", "bestOfCompare", { args: { session } });
}

/** FORWARDED: cmd=bestOfPick. */
export function BestOfPick(session: string): Promise<P.BestOfPickData> {
  return bridge.request<P.BestOfPickData>("DaemonService.BestOfPick", "bestOfPick", { args: { session } });
}

/** FORWARDED: cmd=broadcast. */
export function Broadcast(args: P.BroadcastArgs): Promise<P.BroadcastData> {
  return bridge.request<P.BroadcastData>("DaemonService.Broadcast", "broadcast", { args: args });
}

/** PLATFORM. */
export function CLIInfo(): Promise<M.CLIInfoDTO> {
  return unsupported("DaemonService.CLIInfo", MAC_ONLY);
}

/** FORWARDED: cmd=checkpointDiff. */
export function CheckpointDiff(session: string, seq: number): Promise<P.DiffData> {
  return bridge.request<P.DiffData>("DaemonService.CheckpointDiff", "checkpointDiff", { args: { session, seq } });
}

/** FORWARDED: cmd=checkpoints. */
export function Checkpoints(session: string): Promise<P.CheckpointsData> {
  return bridge.request<P.CheckpointsData>("DaemonService.Checkpoints", "checkpoints", { session });
}

/** FORWARDED: cmd=closeExitedTemporary. */
export function CloseExitedTemporary(id: string): Promise<P.KillData> {
  return bridge.request<P.KillData>("DaemonService.CloseExitedTemporary", "closeExitedTemporary", { session: id });
}

/** FORWARDED: cmd=coderabbit. */
export function CodeRabbit(session: string): Promise<P.CodeRabbitData> {
  return bridge.request<P.CodeRabbitData>("DaemonService.CodeRabbit", "coderabbit", { session });
}

/** FORWARDED: cmd=dev. */
export function Dev(session: string, on: boolean): Promise<P.DevData> {
  return bridge.request<P.DevData>("DaemonService.Dev", "dev", { args: { session, on } });
}

/** FORWARDED: cmd=devFreePort. */
export function DevFreePort(session: string, port: number, pid: number): Promise<P.DevFreePortData> {
  return bridge.request<P.DevFreePortData>("DaemonService.DevFreePort", "devFreePort", { args: { session, port, pid } });
}

/** FORWARDED: cmd=diff. */
export function Diff(session: string): Promise<P.DiffData> {
  return bridge.request<P.DiffData>("DaemonService.Diff", "diff", { session });
}

/** FORWARDED: cmd=disable. */
export function Disable(poll: string): Promise<void> {
  return bridge.request<void>("DaemonService.Disable", "disable", { poll });
}

/** FORWARDED: cmd=enable. */
export function Enable(poll: string): Promise<void> {
  return bridge.request<void>("DaemonService.Enable", "enable", { poll });
}

/** FORWARDED: cmd=forkCheckpoint. */
export function ForkCheckpoint(session: string, seq: number, agentKind: string): Promise<P.OpenData> {
  return bridge.request<P.OpenData>("DaemonService.ForkCheckpoint", "forkCheckpoint", { args: { session, seq, agent: agentKind } });
}

/** PLATFORM. */
export function GoalAcceptBrief(_request: G.BriefAcceptance): Promise<void> {
  return unsupported("DaemonService.GoalAcceptBrief", MAC_ONLY);
}

/** PLATFORM. */
export function GoalAcceptPlan(_id: string, _revision: number): Promise<string> {
  return unsupported("DaemonService.GoalAcceptPlan", MAC_ONLY);
}

/** PLATFORM. */
export function GoalControl(_id: string, _revision: number, _action: string, _reason: string): Promise<void> {
  return unsupported("DaemonService.GoalControl", MAC_ONLY);
}

/** PLATFORM. */
export function GoalDeliveryEvidence(_request: G.DeliveryEvidenceRequest): Promise<void> {
  return unsupported("DaemonService.GoalDeliveryEvidence", MAC_ONLY);
}

/** PLATFORM. */
export function GoalDismissProgress(_request: G.ProgressResolution): Promise<void> {
  return unsupported("DaemonService.GoalDismissProgress", MAC_ONLY);
}

/** PLATFORM. */
export function GoalEditPlan(_request: GP.EditRequest): Promise<GP.Record> {
  return unsupported("DaemonService.GoalEditPlan", MAC_ONLY);
}

/** PLATFORM. */
export function GoalOverview(_project: string): Promise<G.Overview[] | null> {
  return unsupported("DaemonService.GoalOverview", MAC_ONLY);
}

/** PLATFORM. */
export function GoalProposal(_id: string): Promise<GP.Record> {
  return unsupported("DaemonService.GoalProposal", MAC_ONLY);
}

/** PLATFORM. */
export function GoalProposals(_project: string): Promise<GP.Summary[] | null> {
  return unsupported("DaemonService.GoalProposals", MAC_ONLY);
}

/** PLATFORM. */
export function GoalPropose(_request: GP.Request): Promise<GP.Record> {
  return unsupported("DaemonService.GoalPropose", MAC_ONLY);
}

/** PLATFORM. */
export function GoalPublish(_id: string, _revision: number): Promise<void> {
  return unsupported("DaemonService.GoalPublish", MAC_ONLY);
}

/** PLATFORM. */
export function GoalRecovery(_id: string): Promise<G.RecoveryView> {
  return unsupported("DaemonService.GoalRecovery", MAC_ONLY);
}

/** PLATFORM. */
export function GoalStart(_authorization: G.Authorization): Promise<void> {
  return unsupported("DaemonService.GoalStart", MAC_ONLY);
}

/** PLATFORM. */
export function GoalStartOptions(_id: string): Promise<G.StartOptions> {
  return unsupported("DaemonService.GoalStartOptions", MAC_ONLY);
}

/** PLATFORM. */
export function GoalSyncBrief(_id: string): Promise<void> {
  return unsupported("DaemonService.GoalSyncBrief", MAC_ONLY);
}

/** PLATFORM. */
export function InstallCLI(): Promise<M.CLIInstallDTO> {
  return unsupported("DaemonService.InstallCLI", MAC_ONLY);
}

/**
 * FORWARDED: cmd=kill, carrying `keepWorktree` and never `force`.
 *
 * The daemon clears `force` on every remote request, so a forced kill would
 * answer the dirty-worktree refusal again and `store.kill` would loop on it.
 */
export function Kill(session: string, force: boolean, keepWorktree: boolean): Promise<P.KillData> {
  if (force) {
    return Promise.reject(
      new WireError(
        "a forced kill is refused for remote clients: the daemon clears `force` on every remote request, so a worktree with uncommitted changes can only be removed from the Mac",
      ),
    );
  }
  return bridge.request<P.KillData>("DaemonService.Kill", "kill", keepWorktree ? { session, keepWorktree } : { session });
}

/** PLATFORM. */
export function KnowledgeAccept(_request: K.Acceptance): Promise<K.Lesson> {
  return unsupported("DaemonService.KnowledgeAccept", MAC_ONLY);
}

/** PLATFORM. */
export function KnowledgeCapture(_request: K.CaptureRequest, _correction: boolean): Promise<K.Lesson> {
  return unsupported("DaemonService.KnowledgeCapture", MAC_ONLY);
}

/** PLATFORM. */
export function KnowledgeDecideImprovement(_request: K.ImprovementTriage): Promise<K.Improvement> {
  return unsupported("DaemonService.KnowledgeDecideImprovement", MAC_ONLY);
}

/** PLATFORM. */
export function KnowledgeDispute(_scope: K.Scope, _id: string, _version: number, _reason: string): Promise<void> {
  return unsupported("DaemonService.KnowledgeDispute", MAC_ONLY);
}

/** PLATFORM. */
export function KnowledgeExport(_project: string): Promise<{ [_ in string]?: string } | null> {
  return unsupported("DaemonService.KnowledgeExport", MAC_ONLY);
}

/** PLATFORM. */
export function KnowledgeExportImprovement(_scope: K.Scope, _id: string, _revision: string): Promise<{ [_ in string]?: string } | null> {
  return unsupported("DaemonService.KnowledgeExportImprovement", MAC_ONLY);
}

/** PLATFORM. */
export function KnowledgeImprovement(_scope: K.Scope, _id: string): Promise<K.Improvement> {
  return unsupported("DaemonService.KnowledgeImprovement", MAC_ONLY);
}

/** PLATFORM. */
export function KnowledgeImprovementCatalog(_scope: K.Scope, _after: string): Promise<K.ImprovementCatalog> {
  return unsupported("DaemonService.KnowledgeImprovementCatalog", MAC_ONLY);
}

/** PLATFORM. */
export function KnowledgeImprovementDecisions(_scope: K.Scope, _id: string): Promise<K.ImprovementDecision[] | null> {
  return unsupported("DaemonService.KnowledgeImprovementDecisions", MAC_ONLY);
}

/** PLATFORM. */
export function KnowledgeImprovementSources(_scope: K.Scope, _id: string, _revision: string, _after: string): Promise<K.ImprovementSources> {
  return unsupported("DaemonService.KnowledgeImprovementSources", MAC_ONLY);
}

/** PLATFORM. */
export function KnowledgeList(_project: string): Promise<K.Lesson[] | null> {
  return unsupported("DaemonService.KnowledgeList", MAC_ONLY);
}

/** PLATFORM. */
export function KnowledgeVersion(_scope: K.Scope, _id: string, _version: number): Promise<K.Lesson> {
  return unsupported("DaemonService.KnowledgeVersion", MAC_ONLY);
}

/** FORWARDED: cmd=mergePR. */
export function MergePR(session: string, $number: number, repo: string, head: string, method: string, confirmUnready: boolean): Promise<void> {
  return bridge.request<void>("DaemonService.MergePR", "mergePR", { session, args: { number: $number, repo, head, method, confirmUnready } });
}

/** FORWARDED: cmd=open. */
export function Open(project: string, ref: string): Promise<P.OpenData> {
  return bridge.request<P.OpenData>("DaemonService.Open", "open", { project, ref });
}

/** PLATFORM. */
export function OpenFolder(_path: string): Promise<void> {
  return unsupported("DaemonService.OpenFolder", MAC_ONLY);
}

/** FORWARDED: cmd=openManual, or openExisting when the branch is reused (an older daemon must refuse, never ignore the field). */
export function OpenManual(a: P.OpenManualArgs): Promise<P.OpenData> {
  const cmd = a.existing ? "openExisting" : "openManual";
  return bridge.request<P.OpenData>("DaemonService.OpenManual", cmd, { args: a });
}

/** FORWARDED: cmd=openPr. */
export function OpenPR(a: P.OpenPrArgs): Promise<P.OpenData> {
  return bridge.request<P.OpenData>("DaemonService.OpenPR", "openPr", { args: a });
}

/** FORWARDED: cmd=openTicket. */
export function OpenTicket(a: P.OpenTicketArgs): Promise<P.OpenData> {
  return bridge.request<P.OpenData>("DaemonService.OpenTicket", "openTicket", { args: a });
}

/**
 * LOCAL: opens on the phone. http(s) only; a loopback address means a dev
 * server on the Mac, which the dev-links sheet reaches through its forwarded URL.
 */
export async function OpenURL(url: string): Promise<void> {
  try {
    await openOnPhone(url);
  } catch (err) {
    if (err instanceof OpenOnPhoneError && err.reason === "loopback") {
      throw new UnsupportedOnMobileError("DaemonService.OpenURL", MAC_LINK);
    }
    throw err;
  }
}

/** FORWARDED: cmd=prReadiness. */
export function PRReadiness(session: string, $number: number): Promise<P.PRReadinessData> {
  return bridge.request<P.PRReadinessData>("DaemonService.PRReadiness", "prReadiness", { session, args: { number: $number } });
}

/** FORWARDED: cmd=prs. */
export function PRs(project: string, refresh: boolean): Promise<P.PrsData> {
  return bridge.request<P.PrsData>("DaemonService.PRs", "prs", { args: { project, refresh } });
}

/** FORWARDED: cmd=pane. */
export function Pane(session: string, lines: number): Promise<P.PaneData> {
  return bridge.request<P.PaneData>("DaemonService.Pane", "pane", { session, lines });
}

/** FORWARDED: cmd=pollOnce. */
export function PollOnce(poll: string, dryRun: boolean): Promise<P.PollOnceData> {
  return bridge.request<P.PollOnceData>("DaemonService.PollOnce", "pollOnce", { poll, dryRun });
}

/** FORWARDED: cmd=projects. */
export function Projects(): Promise<P.ProjectsData> {
  return bridge.request<P.ProjectsData>("DaemonService.Projects", "projects");
}

/** PLATFORM. */
export function Reload(): Promise<void> {
  return unsupported("DaemonService.Reload", MAC_ONLY);
}

/** PLATFORM. */
export function RenameProject(_$from: string, _to: string): Promise<P.RenameProjectData> {
  return unsupported("DaemonService.RenameProject", MAC_ONLY);
}

/** FORWARDED: cmd=renameSession. */
export function RenameSession(id: string, name: string): Promise<void> {
  return bridge.request<void>("DaemonService.RenameSession", "renameSession", { session: id, text: name });
}

/** FORWARDED: cmd=resolveConflict. */
export function ResolveConflict(session: string): Promise<P.ResolveConflictData> {
  return bridge.request<P.ResolveConflictData>("DaemonService.ResolveConflict", "resolveConflict", { session });
}

/** PLATFORM. */
export function RestartDaemon(): Promise<void> {
  return unsupported("DaemonService.RestartDaemon", MAC_ONLY);
}

/** FORWARDED: cmd=restoreCheckpoint. */
export function RestoreCheckpoint(session: string, seq: number): Promise<P.RestoreCheckpointData> {
  return bridge.request<P.RestoreCheckpointData>("DaemonService.RestoreCheckpoint", "restoreCheckpoint", { args: { session, seq } });
}

/** FORWARDED: cmd=review. */
export function Review(session: string, provider: string): Promise<P.ReviewData> {
  return bridge.request<P.ReviewData>("DaemonService.Review", "review", { session, provider });
}

/** FORWARDED: cmd=reviewPackageCapture. */
export function ReviewPackageCapture(session: string, request: P.ReviewCaptureArgs): Promise<P.ReviewPackage> {
  return bridge.request<P.ReviewPackage>("DaemonService.ReviewPackageCapture", "reviewPackageCapture", { session, args: request });
}

/** FORWARDED: cmd=reviewPackageChecks. */
export function ReviewPackageChecks(session: string, packageID: string): Promise<P.ReviewChecksData> {
  return bridge.request<P.ReviewChecksData>("DaemonService.ReviewPackageChecks", "reviewPackageChecks", { session, args: { package: packageID } });
}

/** FORWARDED: cmd=reviewPackageDiff. */
export function ReviewPackageDiff(session: string, $from: string, to: string): Promise<P.DiffData> {
  return bridge.request<P.DiffData>("DaemonService.ReviewPackageDiff", "reviewPackageDiff", { session, args: { from: $from, to } });
}

/** FORWARDED: cmd=reviewPackageImage, read in chunks under the frame cap (reviewimage.ts). */
export function ReviewPackageImage(session: string, packageID: string, artifact: string): Promise<P.ReviewImageData> {
  return ReadReviewImage(session, packageID, artifact);
}

/** FORWARDED: cmd=reviewPackageMark. */
export function ReviewPackageMark(session: string, packageID: string, previous: string): Promise<P.ReviewRecord> {
  return bridge.request<P.ReviewRecord>("DaemonService.ReviewPackageMark", "reviewPackageMark", { session, args: { package: packageID, previous } });
}

/** FORWARDED: cmd=reviewPackageRecover. */
export function ReviewPackageRecover(session: string, packageID: string, id: string): Promise<P.ReviewCheck> {
  return bridge.request<P.ReviewCheck>("DaemonService.ReviewPackageRecover", "reviewPackageRecover", { session, args: { package: packageID, id } });
}

/** FORWARDED: cmd=reviewPackageVerify. */
export function ReviewPackageVerify(session: string, request: P.ReviewVerifyArgs): Promise<P.ReviewCheck> {
  return bridge.request<P.ReviewCheck>("DaemonService.ReviewPackageVerify", "reviewPackageVerify", { session, args: request });
}

/** FORWARDED: cmd=reviewPackages. */
export function ReviewPackages(session: string): Promise<P.ReviewPackagesData> {
  return bridge.request<P.ReviewPackagesData>("DaemonService.ReviewPackages", "reviewPackages", { session });
}

/** FORWARDED: cmd=revive. */
export function Revive(session: string): Promise<P.ReviveData> {
  return bridge.request<P.ReviveData>("DaemonService.Revive", "revive", { session });
}

/** FORWARDED: cmd=feedback. */
export function SendFeedback(args: P.FeedbackArgs): Promise<P.FeedbackData> {
  return bridge.request<P.FeedbackData>("DaemonService.SendFeedback", "feedback", { args: args });
}

/** FORWARDED: cmd=sessionTask. */
export function SessionTask(session: string, argv: string[] | null): Promise<P.SessionTask[] | null> {
  return bridge.request<P.SessionTask[] | null>("DaemonService.SessionTask", "sessionTask", { session, args: { argv } });
}

/** FORWARDED: cmd=sessions. */
export function Sessions(): Promise<P.SessionsData> {
  return bridge.request<P.SessionsData>("DaemonService.Sessions", "sessions");
}

/** PLATFORM. */
export function StartDaemon(): Promise<void> {
  return unsupported("DaemonService.StartDaemon", MAC_ONLY);
}

/** FORWARDED: cmd=status. */
export function Status(): Promise<P.StatusData> {
  return bridge.request<P.StatusData>("DaemonService.Status", "status");
}

/** PLATFORM. */
export function StopDaemon(): Promise<void> {
  return unsupported("DaemonService.StopDaemon", MAC_ONLY);
}

/** FORWARDED: cmd=switchAgent. */
export function SwitchAgent(a: P.SwitchAgentArgs): Promise<P.SwitchAgentData> {
  return bridge.request<P.SwitchAgentData>("DaemonService.SwitchAgent", "switchAgent", { args: a });
}

/** FORWARDED: cmd=tickets. */
export function Tickets(project: string, scope: string): Promise<P.TicketsData> {
  return bridge.request<P.TicketsData>("DaemonService.Tickets", "tickets", { args: { project, scope } });
}

/**
 * FORWARDED: cmd=panes, the live inventory in tab-strip order. Normalized
 * because the daemon writes `"panes": null` for none and a zero-valued review
 * pane for an absent one (wire/panes.ts).
 */
export async function Panes(session: string): Promise<PanesData> {
  const d = await bridge.request<RawPanesData>("DaemonService.Panes", "panes", { session });
  return normalizePanesData(session, d);
}

/**
 * FORWARDED: cmd=shellCreate. The daemon allocates the shell's name, and its
 * refusal sentence is the error: nothing here rewraps it. A size <= 0 is
 * ignored daemon-side, so an unmeasured screen only loses the no-reflow nicety.
 */
export function ShellCreate(session: string, cols = 0, rows = 0): Promise<ShellCreateData> {
  const size = cols > 0 && rows > 0 ? { cols, rows } : {};
  return bridge.request<ShellCreateData>("DaemonService.ShellCreate", "shellCreate", { session, ...size });
}

/** FORWARDED: cmd=paneClose. The daemon refuses the agent pane; callers must not offer it there. */
export function PaneClose(session: string, pane: string): Promise<PaneCloseData> {
  return bridge.request<PaneCloseData>("DaemonService.PaneClose", "paneClose", { args: { session, pane } });
}

/**
 * FORWARDED: cmd=paneResize. `cols <= 0` releases the pin. This reshapes the
 * Mac's tmux window, so only `lib/panepin` calls it, and it owns the release.
 */
export function PaneResize(session: string, pane: string, cols: number, rows: number): Promise<PaneResizeData> {
  return bridge.request<PaneResizeData>("DaemonService.PaneResize", "paneResize", {
    args: { session, pane, cols, rows },
  });
}

export const ROUTES: Routes<typeof Gen.DaemonService, "Panes" | "ShellCreate" | "PaneClose" | "PaneResize"> = {
  AddressPRFeedback: { kind: "fwd", cmd: "addressPRFeedback" },
  Alive: { kind: "local" },
  Answer: { kind: "fwd", cmd: "answer" },
  BestOf: { kind: "fwd", cmd: "bestOf" },
  BestOfCompare: { kind: "fwd", cmd: "bestOfCompare" },
  BestOfPick: { kind: "fwd", cmd: "bestOfPick" },
  Broadcast: { kind: "fwd", cmd: "broadcast" },
  CLIInfo: { kind: "platform", reason: MAC_ONLY },
  CheckpointDiff: { kind: "fwd", cmd: "checkpointDiff" },
  Checkpoints: { kind: "fwd", cmd: "checkpoints" },
  CloseExitedTemporary: { kind: "fwd", cmd: "closeExitedTemporary" },
  CodeRabbit: { kind: "fwd", cmd: "coderabbit" },
  Dev: { kind: "fwd", cmd: "dev" },
  DevFreePort: { kind: "fwd", cmd: "devFreePort" },
  Diff: { kind: "fwd", cmd: "diff" },
  Disable: { kind: "fwd", cmd: "disable" },
  Enable: { kind: "fwd", cmd: "enable" },
  ForkCheckpoint: { kind: "fwd", cmd: "forkCheckpoint" },
  GoalAcceptBrief: { kind: "platform", reason: MAC_ONLY },
  GoalAcceptPlan: { kind: "platform", reason: MAC_ONLY },
  GoalControl: { kind: "platform", reason: MAC_ONLY },
  GoalDeliveryEvidence: { kind: "platform", reason: MAC_ONLY },
  GoalDismissProgress: { kind: "platform", reason: MAC_ONLY },
  GoalEditPlan: { kind: "platform", reason: MAC_ONLY },
  GoalOverview: { kind: "platform", reason: MAC_ONLY },
  GoalProposal: { kind: "platform", reason: MAC_ONLY },
  GoalProposals: { kind: "platform", reason: MAC_ONLY },
  GoalPropose: { kind: "platform", reason: MAC_ONLY },
  GoalPublish: { kind: "platform", reason: MAC_ONLY },
  GoalRecovery: { kind: "platform", reason: MAC_ONLY },
  GoalStart: { kind: "platform", reason: MAC_ONLY },
  GoalStartOptions: { kind: "platform", reason: MAC_ONLY },
  GoalSyncBrief: { kind: "platform", reason: MAC_ONLY },
  InstallCLI: { kind: "platform", reason: MAC_ONLY },
  Kill: { kind: "fwd", cmd: "kill" },
  KnowledgeAccept: { kind: "platform", reason: MAC_ONLY },
  KnowledgeCapture: { kind: "platform", reason: MAC_ONLY },
  KnowledgeDecideImprovement: { kind: "platform", reason: MAC_ONLY },
  KnowledgeDispute: { kind: "platform", reason: MAC_ONLY },
  KnowledgeExport: { kind: "platform", reason: MAC_ONLY },
  KnowledgeExportImprovement: { kind: "platform", reason: MAC_ONLY },
  KnowledgeImprovement: { kind: "platform", reason: MAC_ONLY },
  KnowledgeImprovementCatalog: { kind: "platform", reason: MAC_ONLY },
  KnowledgeImprovementDecisions: { kind: "platform", reason: MAC_ONLY },
  KnowledgeImprovementSources: { kind: "platform", reason: MAC_ONLY },
  KnowledgeList: { kind: "platform", reason: MAC_ONLY },
  KnowledgeVersion: { kind: "platform", reason: MAC_ONLY },
  MergePR: { kind: "fwd", cmd: "mergePR" },
  Open: { kind: "fwd", cmd: "open" },
  OpenFolder: { kind: "platform", reason: MAC_ONLY },
  OpenManual: { kind: "fwd", cmd: "openManual" },
  OpenPR: { kind: "fwd", cmd: "openPr" },
  OpenTicket: { kind: "fwd", cmd: "openTicket" },
  OpenURL: { kind: "local" },
  PRReadiness: { kind: "fwd", cmd: "prReadiness" },
  PRs: { kind: "fwd", cmd: "prs" },
  Pane: { kind: "fwd", cmd: "pane" },
  PollOnce: { kind: "fwd", cmd: "pollOnce" },
  Projects: { kind: "fwd", cmd: "projects" },
  Reload: { kind: "platform", reason: MAC_ONLY },
  RenameProject: { kind: "platform", reason: MAC_ONLY },
  RenameSession: { kind: "fwd", cmd: "renameSession" },
  ResolveConflict: { kind: "fwd", cmd: "resolveConflict" },
  RestartDaemon: { kind: "platform", reason: MAC_ONLY },
  RestoreCheckpoint: { kind: "fwd", cmd: "restoreCheckpoint" },
  Review: { kind: "fwd", cmd: "review" },
  ReviewPackageCapture: { kind: "fwd", cmd: "reviewPackageCapture" },
  ReviewPackageChecks: { kind: "fwd", cmd: "reviewPackageChecks" },
  ReviewPackageDiff: { kind: "fwd", cmd: "reviewPackageDiff" },
  ReviewPackageImage: { kind: "fwd", cmd: "reviewPackageImage" },
  ReviewPackageMark: { kind: "fwd", cmd: "reviewPackageMark" },
  ReviewPackageRecover: { kind: "fwd", cmd: "reviewPackageRecover" },
  ReviewPackageVerify: { kind: "fwd", cmd: "reviewPackageVerify" },
  ReviewPackages: { kind: "fwd", cmd: "reviewPackages" },
  Revive: { kind: "fwd", cmd: "revive" },
  SendFeedback: { kind: "fwd", cmd: "feedback" },
  SessionTask: { kind: "fwd", cmd: "sessionTask" },
  Sessions: { kind: "fwd", cmd: "sessions" },
  StartDaemon: { kind: "platform", reason: MAC_ONLY },
  Status: { kind: "fwd", cmd: "status" },
  StopDaemon: { kind: "platform", reason: MAC_ONLY },
  SwitchAgent: { kind: "fwd", cmd: "switchAgent" },
  Tickets: { kind: "fwd", cmd: "tickets" },
  Panes: { kind: "fwd", cmd: "panes" },
  ShellCreate: { kind: "fwd", cmd: "shellCreate" },
  PaneClose: { kind: "fwd", cmd: "paneClose" },
  PaneResize: { kind: "fwd", cmd: "paneResize" },
};
