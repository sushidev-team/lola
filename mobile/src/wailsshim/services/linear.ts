// LinearService: one function per generated method, routed as ROUTES says.
import type * as Gen from "@bindings-generated/desktop";
import type * as M from "@bindings/desktop/models";
import { unsupported } from "../errors";
import { MAC_ONLY } from "./reasons";
import type { Routes } from "./routes";

/** PLATFORM. */
export function IssueBrief(_identifier: string): Promise<M.LinearIssueBrief> {
  return unsupported("LinearService.IssueBrief", MAC_ONLY);
}

/** PLATFORM. */
export function TeamMeta(_teamID: string, _refresh: boolean): Promise<M.LinearTeamMeta> {
  return unsupported("LinearService.TeamMeta", MAC_ONLY);
}

/** PLATFORM. */
export function Teams(): Promise<M.LinearTeam[] | null> {
  return unsupported("LinearService.Teams", MAC_ONLY);
}

/** PLATFORM. */
export function WorkspaceLabels(): Promise<M.LinearOption[] | null> {
  return unsupported("LinearService.WorkspaceLabels", MAC_ONLY);
}

export const ROUTES: Routes<typeof Gen.LinearService> = {
  IssueBrief: { kind: "platform", reason: MAC_ONLY },
  TeamMeta: { kind: "platform", reason: MAC_ONLY },
  Teams: { kind: "platform", reason: MAC_ONLY },
  WorkspaceLabels: { kind: "platform", reason: MAC_ONLY },
};
