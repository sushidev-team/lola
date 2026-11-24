// PreviewService: one function per generated method, routed as ROUTES says.
import type * as Gen from "@bindings-generated/desktop";
import { unsupported } from "../errors";
import { MAC_ONLY } from "./reasons";
import type { Routes } from "./routes";

/** PLATFORM. The preview proxy listens on the Mac's loopback. */
export function Open(_session: string, _url: string): Promise<string> {
  return unsupported("PreviewService.Open", MAC_ONLY);
}

export function TestLogins(..._args: Parameters<typeof Gen.PreviewService.TestLogins>): Promise<Awaited<ReturnType<typeof Gen.PreviewService.TestLogins>>> {
  return unsupported("PreviewService.TestLogins", MAC_ONLY);
}

export function SaveTestLogin(..._args: Parameters<typeof Gen.PreviewService.SaveTestLogin>): Promise<void> {
  return unsupported("PreviewService.SaveTestLogin", MAC_ONLY);
}

export function RemoveTestLogin(..._args: Parameters<typeof Gen.PreviewService.RemoveTestLogin>): Promise<void> {
  return unsupported("PreviewService.RemoveTestLogin", MAC_ONLY);
}

export function UseTestLogin(..._args: Parameters<typeof Gen.PreviewService.UseTestLogin>): Promise<Awaited<ReturnType<typeof Gen.PreviewService.UseTestLogin>>> {
  return unsupported("PreviewService.UseTestLogin", MAC_ONLY);
}

export function SetAutoLogin(..._args: Parameters<typeof Gen.PreviewService.SetAutoLogin>): Promise<void> {
  return unsupported("PreviewService.SetAutoLogin", MAC_ONLY);
}

export function AutoTestLogin(..._args: Parameters<typeof Gen.PreviewService.AutoTestLogin>): Promise<Awaited<ReturnType<typeof Gen.PreviewService.AutoTestLogin>>> {
  return unsupported("PreviewService.AutoTestLogin", MAC_ONLY);
}

export function SaveAnnotationImage(..._args: Parameters<typeof Gen.PreviewService.SaveAnnotationImage>): Promise<string> {
  return unsupported("PreviewService.SaveAnnotationImage", MAC_ONLY);
}

export const ROUTES: Routes<typeof Gen.PreviewService> = {
  SaveAnnotationImage: { kind: "platform", reason: MAC_ONLY },
  AutoTestLogin: { kind: "platform", reason: MAC_ONLY },
  SetAutoLogin: { kind: "platform", reason: MAC_ONLY },
  TestLogins: { kind: "platform", reason: MAC_ONLY },
  SaveTestLogin: { kind: "platform", reason: MAC_ONLY },
  RemoveTestLogin: { kind: "platform", reason: MAC_ONLY },
  UseTestLogin: { kind: "platform", reason: MAC_ONLY },
  Open: { kind: "platform", reason: MAC_ONLY },
};
