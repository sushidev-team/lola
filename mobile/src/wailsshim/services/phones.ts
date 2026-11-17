// PhonesService: one function per generated method, routed as ROUTES says.
import type * as Gen from "@bindings-generated/desktop";
import type * as P from "@bindings/internal/protocol";
import { unsupported } from "../errors";
import { MAC_ONLY } from "./reasons";
import type { Routes } from "./routes";

/** PLATFORM. */
export function Begin(): Promise<P.PairBeginData> {
  return unsupported("PhonesService.Begin", MAC_ONLY);
}

/** PLATFORM. */
export function Confirm(_a: P.PairConfirmArgs): Promise<P.PairStatusData> {
  return unsupported("PhonesService.Confirm", MAC_ONLY);
}

/** PLATFORM. */
export function Devices(): Promise<P.DevicesData> {
  return unsupported("PhonesService.Devices", MAC_ONLY);
}

/** PLATFORM. */
export function Reject(_pairing: string): Promise<P.PairStatusData> {
  return unsupported("PhonesService.Reject", MAC_ONLY);
}

/** PLATFORM. */
export function Revoke(_id: string): Promise<P.RevokeDeviceData> {
  return unsupported("PhonesService.Revoke", MAC_ONLY);
}

/** PLATFORM. */
export function SetTiers(_id: string, _tiers: string[] | null): Promise<P.DeviceInfo> {
  return unsupported("PhonesService.SetTiers", MAC_ONLY);
}

/** PLATFORM. */
export function Status(_a: P.PairStatusArgs): Promise<P.PairStatusData> {
  return unsupported("PhonesService.Status", MAC_ONLY);
}

export const ROUTES: Routes<typeof Gen.PhonesService> = {
  Begin: { kind: "platform", reason: MAC_ONLY },
  Confirm: { kind: "platform", reason: MAC_ONLY },
  Devices: { kind: "platform", reason: MAC_ONLY },
  Reject: { kind: "platform", reason: MAC_ONLY },
  Revoke: { kind: "platform", reason: MAC_ONLY },
  SetTiers: { kind: "platform", reason: MAC_ONLY },
  Status: { kind: "platform", reason: MAC_ONLY },
};
