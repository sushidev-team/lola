// ConfigService: one function per generated method, routed as ROUTES says.
import type * as Gen from "@bindings-generated/desktop";
import type * as P from "@bindings/internal/protocol";
import type * as M from "@bindings/desktop/models";
import { unsupported } from "../errors";
import { CONFIG } from "./reasons";
import type { Routes } from "./routes";

/** PLATFORM. */
export function AddGroup(_label: string): Promise<string> {
  return unsupported("ConfigService.AddGroup", CONFIG);
}

/** PLATFORM. */
export function ClearSandboxToken(): Promise<void> {
  return unsupported("ConfigService.ClearSandboxToken", CONFIG);
}

/** LOCAL: a connected daemon has a config, and `false` would start a wizard that configures a Mac. */
export function ConfigExists(): Promise<boolean> {
  return Promise.resolve(true);
}

/** PLATFORM. */
export function CreateStack(_label: string): Promise<string> {
  return unsupported("ConfigService.CreateStack", CONFIG);
}

/** PLATFORM. */
export function CreateStackFromFolder(_label: string, _apps: M.StackFolderAppDTO[] | null): Promise<string> {
  return unsupported("ConfigService.CreateStackFromFolder", CONFIG);
}

/** PLATFORM. */
export function DetectToolchains(_path: string): Promise<M.ToolchainInfoDTO> {
  return unsupported("ConfigService.DetectToolchains", CONFIG);
}

/** PLATFORM. */
export function GetProject(_name: string): Promise<M.ProjectFormDTO> {
  return unsupported("ConfigService.GetProject", CONFIG);
}

/** PLATFORM. */
export function GetSettings(): Promise<M.SettingsDTO> {
  return unsupported("ConfigService.GetSettings", CONFIG);
}

/** PLATFORM. */
export function HubProjects(): Promise<M.HubProjectDTO[] | null> {
  return unsupported("ConfigService.HubProjects", CONFIG);
}

/** PLATFORM. */
export function HubReadiness(): Promise<M.HubReadinessRowDTO[] | null> {
  return unsupported("ConfigService.HubReadiness", CONFIG);
}

/** PLATFORM. */
export function InspectPath(_path: string): Promise<M.PathInfoDTO> {
  return unsupported("ConfigService.InspectPath", CONFIG);
}

/** PLATFORM. */
export function LinearKeyStatus(): Promise<M.LinearKeyStatusDTO> {
  return unsupported("ConfigService.LinearKeyStatus", CONFIG);
}

/** PLATFORM. */
export function MigrateReview(): Promise<void> {
  return unsupported("ConfigService.MigrateReview", CONFIG);
}

/** PLATFORM. */
export function OpenLinearKeyHelp(): Promise<void> {
  return unsupported("ConfigService.OpenLinearKeyHelp", CONFIG);
}

/** PLATFORM. */
export function PickFile(_start: string): Promise<string> {
  return unsupported("ConfigService.PickFile", CONFIG);
}

/** PLATFORM. */
export function PickFolder(_start: string): Promise<string> {
  return unsupported("ConfigService.PickFolder", CONFIG);
}

/** PLATFORM. */
export function PinPolicyKey(_keyID: string, _pub: string): Promise<boolean> {
  return unsupported("ConfigService.PinPolicyKey", CONFIG);
}

/** PLATFORM. */
export function PolicyKeyFingerprint(_pub: string): Promise<string> {
  return unsupported("ConfigService.PolicyKeyFingerprint", CONFIG);
}

/** PLATFORM. */
export function PrioritySortKeys(): Promise<string[] | null> {
  return unsupported("ConfigService.PrioritySortKeys", CONFIG);
}

/** PLATFORM. */
export function RemoteBinds(): Promise<string[] | null> {
  return unsupported("ConfigService.RemoteBinds", CONFIG);
}

/** PLATFORM. */
export function RemoveGroup(_name: string): Promise<void> {
  return unsupported("ConfigService.RemoveGroup", CONFIG);
}

/** PLATFORM. */
export function RemoveProject(_name: string): Promise<void> {
  return unsupported("ConfigService.RemoveProject", CONFIG);
}

/** PLATFORM. */
export function RemoveSSHHost(_name: string): Promise<void> {
  return unsupported("ConfigService.RemoveSSHHost", CONFIG);
}

/** PLATFORM. */
export function RemoveStack(_stack: string, _keep: string[] | null): Promise<void> {
  return unsupported("ConfigService.RemoveStack", CONFIG);
}

/** PLATFORM. */
export function RenameGroup(_name: string, _label: string): Promise<void> {
  return unsupported("ConfigService.RenameGroup", CONFIG);
}

/** PLATFORM. */
export function ResolveSSHHost(_d: M.SSHHostDTO): Promise<P.SSHResolveData> {
  return unsupported("ConfigService.ResolveSSHHost", CONFIG);
}

/** PLATFORM. */
export function ReviewKinds(): Promise<M.ReviewKindDTO[] | null> {
  return unsupported("ConfigService.ReviewKinds", CONFIG);
}

/** PLATFORM. */
export function ReviewProviderKinds(): Promise<string[] | null> {
  return unsupported("ConfigService.ReviewProviderKinds", CONFIG);
}

/** PLATFORM. */
export function SSHConfigAliases(): Promise<string[] | null> {
  return unsupported("ConfigService.SSHConfigAliases", CONFIG);
}

/** PLATFORM. */
export function SSHHostForms(): Promise<M.SSHHostDTO[] | null> {
  return unsupported("ConfigService.SSHHostForms", CONFIG);
}

/** PLATFORM. */
export function SSHHostPreview(_d: M.SSHHostDTO): Promise<M.SSHHostPreviewDTO> {
  return unsupported("ConfigService.SSHHostPreview", CONFIG);
}

/** PLATFORM. */
export function SandboxImageDefault(): Promise<string> {
  return unsupported("ConfigService.SandboxImageDefault", CONFIG);
}

/** LOCAL: "" means no login needed; the daemon refuses the open with its own sentence if one is. */
export function SandboxLoginNeeded(_project: string, _agentKind: string): Promise<string> {
  return Promise.resolve("");
}

/** PLATFORM. */
export function SandboxStatus(): Promise<M.SandboxStatusDTO> {
  return unsupported("ConfigService.SandboxStatus", CONFIG);
}

/** PLATFORM. */
export function SaveHubProject(_patch: M.HubProjectPatchDTO): Promise<void> {
  return unsupported("ConfigService.SaveHubProject", CONFIG);
}

/** PLATFORM. */
export function SaveHubRules(_r: M.HubRulesDTO): Promise<void> {
  return unsupported("ConfigService.SaveHubRules", CONFIG);
}

/** PLATFORM. */
export function SaveProject(_dto: M.ProjectFormDTO): Promise<void> {
  return unsupported("ConfigService.SaveProject", CONFIG);
}

/** PLATFORM. */
export function SaveSSHHost(_original: string, _d: M.SSHHostDTO): Promise<void> {
  return unsupported("ConfigService.SaveSSHHost", CONFIG);
}

/** PLATFORM. */
export function SaveSettings(_dto: M.SettingsDTO): Promise<void> {
  return unsupported("ConfigService.SaveSettings", CONFIG);
}

/** PLATFORM. */
export function SaveTeamLimits(_d: M.TeamLimitsDTO): Promise<void> {
  return unsupported("ConfigService.SaveTeamLimits", CONFIG);
}

/** PLATFORM. */
export function ScanStackFolder(_path: string): Promise<M.StackFolderDTO> {
  return unsupported("ConfigService.ScanStackFolder", CONFIG);
}

/** PLATFORM. */
export function SetGroupCollapsed(_name: string, _collapsed: boolean): Promise<void> {
  return unsupported("ConfigService.SetGroupCollapsed", CONFIG);
}

/** PLATFORM. */
export function SetLinearKey(_key: string): Promise<string> {
  return unsupported("ConfigService.SetLinearKey", CONFIG);
}

/** PLATFORM. */
export function SetProjectLayout(_dto: M.ProjectLayoutDTO): Promise<void> {
  return unsupported("ConfigService.SetProjectLayout", CONFIG);
}

/** PLATFORM. */
export function SetProjectStack(_project: string, _stack: string): Promise<void> {
  return unsupported("ConfigService.SetProjectStack", CONFIG);
}

/** PLATFORM. */
export function SetSandboxImage(_image: string): Promise<void> {
  return unsupported("ConfigService.SetSandboxImage", CONFIG);
}

/** PLATFORM. */
export function SetSandboxToken(_token: string): Promise<void> {
  return unsupported("ConfigService.SetSandboxToken", CONFIG);
}

/** PLATFORM. */
export function SetSandboxUseAPIKey(_on: boolean): Promise<void> {
  return unsupported("ConfigService.SetSandboxUseAPIKey", CONFIG);
}

/** PLATFORM. */
export function SetStackMemberLabels(_stack: string, _labels: { [_ in string]?: string[] | null } | null): Promise<void> {
  return unsupported("ConfigService.SetStackMemberLabels", CONFIG);
}

/** PLATFORM. */
export function Setup(_dto: M.SetupDTO): Promise<M.SetupResultDTO> {
  return unsupported("ConfigService.Setup", CONFIG);
}

/** PLATFORM. */
export function TeamLimits(): Promise<M.TeamLimitsDTO> {
  return unsupported("ConfigService.TeamLimits", CONFIG);
}

/** PLATFORM. */
export function TeamPolicy(): Promise<M.TeamPolicyDTO> {
  return unsupported("ConfigService.TeamPolicy", CONFIG);
}

/** PLATFORM. */
export function TransportTokens(): Promise<string[] | null> {
  return unsupported("ConfigService.TransportTokens", CONFIG);
}

/** PLATFORM. */
export function ValidateLinearKey(_key: string): Promise<void> {
  return unsupported("ConfigService.ValidateLinearKey", CONFIG);
}

export const ROUTES: Routes<typeof Gen.ConfigService> = {
  AddGroup: { kind: "platform", reason: CONFIG },
  ClearSandboxToken: { kind: "platform", reason: CONFIG },
  ConfigExists: { kind: "local" },
  CreateStack: { kind: "platform", reason: CONFIG },
  CreateStackFromFolder: { kind: "platform", reason: CONFIG },
  DetectToolchains: { kind: "platform", reason: CONFIG },
  GetAppearance: { kind: "absent" },
  GetProject: { kind: "platform", reason: CONFIG },
  GetSettings: { kind: "platform", reason: CONFIG },
  GetTheme: { kind: "absent" },
  HubProjects: { kind: "platform", reason: CONFIG },
  HubReadiness: { kind: "platform", reason: CONFIG },
  InspectPath: { kind: "platform", reason: CONFIG },
  LinearKeyStatus: { kind: "platform", reason: CONFIG },
  MigrateReview: { kind: "platform", reason: CONFIG },
  OpenLinearKeyHelp: { kind: "platform", reason: CONFIG },
  PickFile: { kind: "platform", reason: CONFIG },
  PickFolder: { kind: "platform", reason: CONFIG },
  PinPolicyKey: { kind: "platform", reason: CONFIG },
  PolicyKeyFingerprint: { kind: "platform", reason: CONFIG },
  PreviewTheme: { kind: "absent" },
  PrioritySortKeys: { kind: "platform", reason: CONFIG },
  RemoteBinds: { kind: "platform", reason: CONFIG },
  RemoveGroup: { kind: "platform", reason: CONFIG },
  RemoveProject: { kind: "platform", reason: CONFIG },
  RemoveSSHHost: { kind: "platform", reason: CONFIG },
  RemoveStack: { kind: "platform", reason: CONFIG },
  RenameGroup: { kind: "platform", reason: CONFIG },
  ResolveSSHHost: { kind: "platform", reason: CONFIG },
  ReviewKinds: { kind: "platform", reason: CONFIG },
  ReviewProviderKinds: { kind: "platform", reason: CONFIG },
  SSHConfigAliases: { kind: "platform", reason: CONFIG },
  SSHHostForms: { kind: "platform", reason: CONFIG },
  SSHHostPreview: { kind: "platform", reason: CONFIG },
  SandboxImageDefault: { kind: "platform", reason: CONFIG },
  SandboxLoginNeeded: { kind: "local" },
  SandboxStatus: { kind: "platform", reason: CONFIG },
  SaveHubProject: { kind: "platform", reason: CONFIG },
  SaveHubRules: { kind: "platform", reason: CONFIG },
  SaveProject: { kind: "platform", reason: CONFIG },
  SaveSSHHost: { kind: "platform", reason: CONFIG },
  SaveSettings: { kind: "platform", reason: CONFIG },
  SaveTeamLimits: { kind: "platform", reason: CONFIG },
  ScanStackFolder: { kind: "platform", reason: CONFIG },
  SetAppearance: { kind: "absent" },
  SetGroupCollapsed: { kind: "platform", reason: CONFIG },
  SetLinearKey: { kind: "platform", reason: CONFIG },
  SetProjectLayout: { kind: "platform", reason: CONFIG },
  SetProjectStack: { kind: "platform", reason: CONFIG },
  SetSandboxImage: { kind: "platform", reason: CONFIG },
  SetSandboxToken: { kind: "platform", reason: CONFIG },
  SetSandboxUseAPIKey: { kind: "platform", reason: CONFIG },
  SetStackMemberLabels: { kind: "platform", reason: CONFIG },
  SetTheme: { kind: "absent" },
  Setup: { kind: "platform", reason: CONFIG },
  TeamLimits: { kind: "platform", reason: CONFIG },
  TeamPolicy: { kind: "platform", reason: CONFIG },
  Themes: { kind: "absent" },
  TransportTokens: { kind: "platform", reason: CONFIG },
  ValidateLinearKey: { kind: "platform", reason: CONFIG },
};
