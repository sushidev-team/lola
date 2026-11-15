// UpdateService: one function per generated method, routed as ROUTES says.
import type * as Gen from "@bindings-generated/desktop";
import type * as M from "@bindings/desktop/models";
import { unsupported } from "../errors";
import { MAC_ONLY } from "./reasons";
import type { Routes } from "./routes";

/** PLATFORM. */
export function CheckForUpdates(_force: boolean): Promise<M.UpdateInfoDTO> {
  return unsupported("UpdateService.CheckForUpdates", MAC_ONLY);
}

/** PLATFORM. */
export function DownloadUpdate(_url: string): Promise<string> {
  return unsupported("UpdateService.DownloadUpdate", MAC_ONLY);
}

/** PLATFORM. */
export function GetRecentReleases(_count: number): Promise<M.ReleaseEntryDTO[] | null> {
  return unsupported("UpdateService.GetRecentReleases", MAC_ONLY);
}

/** PLATFORM. */
export function GetUpdateSettings(): Promise<M.UpdateSettingsDTO> {
  return unsupported("UpdateService.GetUpdateSettings", MAC_ONLY);
}

/** PLATFORM. */
export function GetVersion(): Promise<string> {
  return unsupported("UpdateService.GetVersion", MAC_ONLY);
}

/** PLATFORM. */
export function InstallAndRestart(_dmgPath: string): Promise<void> {
  return unsupported("UpdateService.InstallAndRestart", MAC_ONLY);
}

/** PLATFORM. */
export function IsVersionSkipped(_v: string): Promise<boolean> {
  return unsupported("UpdateService.IsVersionSkipped", MAC_ONLY);
}

/** PLATFORM. */
export function SetApp(_app: unknown | null): Promise<void> {
  return unsupported("UpdateService.SetApp", MAC_ONLY);
}

/** PLATFORM. */
export function SetUpdateSettings(_$in: M.UpdateSettingsDTO): Promise<void> {
  return unsupported("UpdateService.SetUpdateSettings", MAC_ONLY);
}

/** PLATFORM. */
export function ShouldAutoCheck(): Promise<boolean> {
  return unsupported("UpdateService.ShouldAutoCheck", MAC_ONLY);
}

/** PLATFORM. */
export function SkipVersion(_v: string): Promise<void> {
  return unsupported("UpdateService.SkipVersion", MAC_ONLY);
}

export const ROUTES: Routes<typeof Gen.UpdateService> = {
  CheckForUpdates: { kind: "platform", reason: MAC_ONLY },
  DownloadUpdate: { kind: "platform", reason: MAC_ONLY },
  GetRecentReleases: { kind: "platform", reason: MAC_ONLY },
  GetUpdateSettings: { kind: "platform", reason: MAC_ONLY },
  GetVersion: { kind: "platform", reason: MAC_ONLY },
  InstallAndRestart: { kind: "platform", reason: MAC_ONLY },
  IsVersionSkipped: { kind: "platform", reason: MAC_ONLY },
  SetApp: { kind: "platform", reason: MAC_ONLY },
  SetUpdateSettings: { kind: "platform", reason: MAC_ONLY },
  ShouldAutoCheck: { kind: "platform", reason: MAC_ONLY },
  SkipVersion: { kind: "platform", reason: MAC_ONLY },
};
