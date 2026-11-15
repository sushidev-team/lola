// SSHService: one function per generated method, routed as ROUTES says.
import type * as Gen from "@bindings-generated/desktop";
import type * as P from "@bindings/internal/protocol";
import { bridge } from "../bridge";
import { unsupported } from "../errors";
import { MAC_ONLY } from "./reasons";
import type { Routes } from "./routes";

/** FORWARDED: cmd=sshAnswer. */
export function Answer(a: P.SSHAnswerArgs): Promise<P.SSHSayData> {
  return bridge.request<P.SSHSayData>("SSHService.Answer", "sshAnswer", { args: a });
}

/** PLATFORM. */
export function Approve(_a: P.SSHApproveArgs): Promise<P.SSHBlock> {
  return unsupported("SSHService.Approve", MAC_ONLY);
}

/** PLATFORM. */
export function Close(_conn: string): Promise<P.SSHCloseData> {
  return unsupported("SSHService.Close", MAC_ONLY);
}

/** PLATFORM. */
export function Connect(_a: P.SSHConnectArgs): Promise<P.SSHConnectData> {
  return unsupported("SSHService.Connect", MAC_ONLY);
}

/** PLATFORM. */
export function Deny(_conn: string, _reason: string): Promise<P.SSHConnInfo> {
  return unsupported("SSHService.Deny", MAC_ONLY);
}

/** PLATFORM. */
export function Go(_conn: string): Promise<P.SSHConnInfo> {
  return unsupported("SSHService.Go", MAC_ONLY);
}

/** PLATFORM. */
export function Handback(_a: P.SSHHandbackArgs): Promise<P.SSHHandbackData> {
  return unsupported("SSHService.Handback", MAC_ONLY);
}

/** PLATFORM. */
export function HandbackPreview(_conn: string): Promise<P.SSHHandbackPreviewData> {
  return unsupported("SSHService.HandbackPreview", MAC_ONLY);
}

/** PLATFORM. */
export function Hosts(): Promise<P.SSHHostsData> {
  return unsupported("SSHService.Hosts", MAC_ONLY);
}

/** PLATFORM. */
export function Open(_a: P.SSHOpenArgs): Promise<P.SSHOpenData> {
  return unsupported("SSHService.Open", MAC_ONLY);
}

/** PLATFORM. */
export function Output(_a: P.SSHOutputArgs): Promise<P.SSHOutputData> {
  return unsupported("SSHService.Output", MAC_ONLY);
}

/** PLATFORM. */
export function PromptAnswer(_a: P.SSHPromptAnswerArgs): Promise<P.SSHPromptAnswerData> {
  return unsupported("SSHService.PromptAnswer", MAC_ONLY);
}

/** PLATFORM. */
export function Proposal(_a: P.SSHProposalArgs): Promise<P.SSHProposalData> {
  return unsupported("SSHService.Proposal", MAC_ONLY);
}

/** FORWARDED: cmd=sshRecall. */
export function Recall(session: string, item: string): Promise<P.SSHRecallData> {
  return bridge.request<P.SSHRecallData>("SSHService.Recall", "sshRecall", { args: { session, item } });
}

/** PLATFORM. */
export function Resolve(_alias: string, _user: string): Promise<P.SSHResolveData> {
  return unsupported("SSHService.Resolve", MAC_ONLY);
}

/** PLATFORM. */
export function RevokeGrant(_conn: string): Promise<P.SSHConnInfo> {
  return unsupported("SSHService.RevokeGrant", MAC_ONLY);
}

/** PLATFORM. */
export function RunCommand(_a: P.SSHHumanRunArgs): Promise<P.SSHBlock> {
  return unsupported("SSHService.RunCommand", MAC_ONLY);
}

/** PLATFORM. */
export function Say(_session: string, _conn: string, _text: string): Promise<P.SSHSayData> {
  return unsupported("SSHService.Say", MAC_ONLY);
}

/** PLATFORM. */
export function ShareBlock(_a: P.SSHShareBlockArgs): Promise<P.SSHBlock> {
  return unsupported("SSHService.ShareBlock", MAC_ONLY);
}

/** PLATFORM. */
export function Stop(_conn: string, _block: string): Promise<P.SSHStopData> {
  return unsupported("SSHService.Stop", MAC_ONLY);
}

/** PLATFORM. */
export function TakeWheel(_a: P.SSHTakeWheelArgs): Promise<P.SSHTakeWheelData> {
  return unsupported("SSHService.TakeWheel", MAC_ONLY);
}

/** PLATFORM. */
export function Terminal(_conn: string): Promise<P.SSHConnInfo> {
  return unsupported("SSHService.Terminal", MAC_ONLY);
}

/** PLATFORM. */
export function TerminalCd(_conn: string): Promise<P.SSHConnInfo> {
  return unsupported("SSHService.TerminalCd", MAC_ONLY);
}

/** FORWARDED: cmd=sessionTimeline. */
export function Timeline(a: P.SSHTimelineArgs): Promise<P.SSHTimelineData> {
  return bridge.request<P.SSHTimelineData>("SSHService.Timeline", "sessionTimeline", { args: a });
}

/** PLATFORM. */
export function Trash(_conn: string): Promise<P.SSHCloseData> {
  return unsupported("SSHService.Trash", MAC_ONLY);
}

/** PLATFORM. */
export function WidgetStatus(_a: P.SSHWidgetStatusArgs): Promise<P.SSHWidgetStatusData> {
  return unsupported("SSHService.WidgetStatus", MAC_ONLY);
}

export const ROUTES: Routes<typeof Gen.SSHService> = {
  Answer: { kind: "fwd", cmd: "sshAnswer" },
  Approve: { kind: "platform", reason: MAC_ONLY },
  Close: { kind: "platform", reason: MAC_ONLY },
  Connect: { kind: "platform", reason: MAC_ONLY },
  Deny: { kind: "platform", reason: MAC_ONLY },
  Go: { kind: "platform", reason: MAC_ONLY },
  Handback: { kind: "platform", reason: MAC_ONLY },
  HandbackPreview: { kind: "platform", reason: MAC_ONLY },
  Hosts: { kind: "platform", reason: MAC_ONLY },
  Open: { kind: "platform", reason: MAC_ONLY },
  Output: { kind: "platform", reason: MAC_ONLY },
  PromptAnswer: { kind: "platform", reason: MAC_ONLY },
  Proposal: { kind: "platform", reason: MAC_ONLY },
  Recall: { kind: "fwd", cmd: "sshRecall" },
  Resolve: { kind: "platform", reason: MAC_ONLY },
  RevokeGrant: { kind: "platform", reason: MAC_ONLY },
  RunCommand: { kind: "platform", reason: MAC_ONLY },
  Say: { kind: "platform", reason: MAC_ONLY },
  ShareBlock: { kind: "platform", reason: MAC_ONLY },
  Stop: { kind: "platform", reason: MAC_ONLY },
  TakeWheel: { kind: "platform", reason: MAC_ONLY },
  Terminal: { kind: "platform", reason: MAC_ONLY },
  TerminalCd: { kind: "platform", reason: MAC_ONLY },
  Timeline: { kind: "fwd", cmd: "sessionTimeline" },
  Trash: { kind: "platform", reason: MAC_ONLY },
  WidgetStatus: { kind: "platform", reason: MAC_ONLY },
};
