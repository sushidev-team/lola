// SandboxLoginService: one function per generated method, routed as ROUTES says.
import type * as Gen from "@bindings-generated/desktop";
import { unsupported } from "../errors";
import { MAC_ONLY } from "./reasons";
import type { Routes } from "./routes";

/** PLATFORM. */
export function Cancel(_run: number): Promise<void> {
  return unsupported("SandboxLoginService.Cancel", MAC_ONLY);
}

/** PLATFORM. */
export function Resize(_run: number, _cols: number, _rows: number): Promise<void> {
  return unsupported("SandboxLoginService.Resize", MAC_ONLY);
}

/** PLATFORM. */
export function Start(_cols: number, _rows: number): Promise<number> {
  return unsupported("SandboxLoginService.Start", MAC_ONLY);
}

/** PLATFORM. */
export function Write(_run: number, _data: string): Promise<void> {
  return unsupported("SandboxLoginService.Write", MAC_ONLY);
}

export const ROUTES: Routes<typeof Gen.SandboxLoginService> = {
  Cancel: { kind: "platform", reason: MAC_ONLY },
  Resize: { kind: "platform", reason: MAC_ONLY },
  Start: { kind: "platform", reason: MAC_ONLY },
  Write: { kind: "platform", reason: MAC_ONLY },
};
