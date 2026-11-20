// HubService: one function per generated method, routed as ROUTES says.
import type * as Gen from "@bindings-generated/desktop";
import type * as P from "@bindings/internal/protocol";
import type * as M from "@bindings/desktop/models";
import { bridge } from "../bridge";
import { unsupported } from "../errors";
import { MAC_ONLY } from "./reasons";
import type { Routes } from "./routes";

/** PLATFORM. */
export function Board(_a: P.HubBoardArgs): Promise<P.HubBoardData> {
  return unsupported("HubService.Board", MAC_ONLY);
}

/** PLATFORM. */
export function Claim(_a: P.HubClaimArgs): Promise<P.HubClaimData> {
  return unsupported("HubService.Claim", MAC_ONLY);
}

/** PLATFORM. */
export function CreateTeam(_a: P.HubCreateTeamArgs): Promise<P.HubCreateTeamData> {
  return unsupported("HubService.CreateTeam", MAC_ONLY);
}

/** PLATFORM. */
export function DefaultDeviceName(): Promise<string> {
  return unsupported("HubService.DefaultDeviceName", MAC_ONLY);
}

/** PLATFORM. */
export function Invite(_a: P.HubInviteArgs): Promise<P.HubInviteData> {
  return unsupported("HubService.Invite", MAC_ONLY);
}

/** PLATFORM. */
export function Join(_a: P.HubJoinArgs): Promise<P.HubJoinData> {
  return unsupported("HubService.Join", MAC_ONLY);
}

/** PLATFORM. */
export function Kinds(): Promise<M.HubKindDTO[] | null> {
  return unsupported("HubService.Kinds", MAC_ONLY);
}

/** PLATFORM. */
export function Leave(_forgetKey: boolean): Promise<P.HubLeaveData> {
  return unsupported("HubService.Leave", MAC_ONLY);
}

/** PLATFORM. */
export function Org(): Promise<P.HubOrgData> {
  return unsupported("HubService.Org", MAC_ONLY);
}

/** PLATFORM. */
export function ProbeHub(_url: string): Promise<P.HubProbeData> {
  return unsupported("HubService.ProbeHub", MAC_ONLY);
}

/** PLATFORM. */
export function Release(_itemID: string): Promise<P.HubReleaseData> {
  return unsupported("HubService.Release", MAC_ONLY);
}

/** PLATFORM. */
export function RevokeDevice(_name: string): Promise<P.HubRevokeData> {
  return unsupported("HubService.RevokeDevice", MAC_ONLY);
}

/** PLATFORM: sharing a session is a person's act on the Mac. */
export function SetShared(_a: P.ShareSessionArgs): Promise<P.ShareSessionData> {
  return unsupported("HubService.SetShared", MAC_ONLY);
}

/** PLATFORM. */
export function ShareMembers(): Promise<P.ShareMembersData> {
  return unsupported("HubService.ShareMembers", MAC_ONLY);
}

/** PLATFORM. */
export function ShareOffered(): Promise<P.ShareOfferedData> {
  return unsupported("HubService.ShareOffered", MAC_ONLY);
}

/** PLATFORM. */
export function ShareTrust(_a: P.ShareTrustArgs): Promise<P.ShareMembersData> {
  return unsupported("HubService.ShareTrust", MAC_ONLY);
}

/** PLATFORM. */
export function ShareUntrust(_a: P.ShareUntrustArgs): Promise<P.ShareMembersData> {
  return unsupported("HubService.ShareUntrust", MAC_ONLY);
}

/** PLATFORM. */
export function Stats(): Promise<M.HubStatsDTO> {
  return unsupported("HubService.Stats", MAC_ONLY);
}

/** FORWARDED: cmd=hubStatus. */
export function Status(): Promise<P.HubStatusData> {
  return bridge.request<P.HubStatusData>("HubService.Status", "hubStatus");
}

/** PLATFORM. */
export function Sync(): Promise<P.HubSyncData> {
  return unsupported("HubService.Sync", MAC_ONLY);
}

/** PLATFORM. */
export function TeamSettings(_a: P.HubTeamSettingsArgs): Promise<P.HubTeamSettingsData> {
  return unsupported("HubService.TeamSettings", MAC_ONLY);
}

export const ROUTES: Routes<typeof Gen.HubService> = {
  Board: { kind: "platform", reason: MAC_ONLY },
  Claim: { kind: "platform", reason: MAC_ONLY },
  CreateTeam: { kind: "platform", reason: MAC_ONLY },
  DefaultDeviceName: { kind: "platform", reason: MAC_ONLY },
  Invite: { kind: "platform", reason: MAC_ONLY },
  Join: { kind: "platform", reason: MAC_ONLY },
  Kinds: { kind: "platform", reason: MAC_ONLY },
  Leave: { kind: "platform", reason: MAC_ONLY },
  Org: { kind: "platform", reason: MAC_ONLY },
  ProbeHub: { kind: "platform", reason: MAC_ONLY },
  Release: { kind: "platform", reason: MAC_ONLY },
  RevokeDevice: { kind: "platform", reason: MAC_ONLY },
  SetShared: { kind: "platform", reason: MAC_ONLY },
  ShareMembers: { kind: "platform", reason: MAC_ONLY },
  ShareOffered: { kind: "platform", reason: MAC_ONLY },
  ShareTrust: { kind: "platform", reason: MAC_ONLY },
  ShareUntrust: { kind: "platform", reason: MAC_ONLY },
  Stats: { kind: "platform", reason: MAC_ONLY },
  Status: { kind: "fwd", cmd: "hubStatus" },
  Sync: { kind: "platform", reason: MAC_ONLY },
  TeamSettings: { kind: "platform", reason: MAC_ONLY },
};
