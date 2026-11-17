// DoctorService: one function per generated method, routed as ROUTES says.
import type * as Gen from "@bindings-generated/desktop";
import type * as M from "@bindings/desktop/models";
import { unsupported } from "../errors";
import { MAC_ONLY } from "./reasons";
import type { Routes } from "./routes";

/** PLATFORM. */
export function Run(): Promise<M.DoctorReportDTO> {
  return unsupported("DoctorService.Run", MAC_ONLY);
}

export const ROUTES: Routes<typeof Gen.DoctorService> = {
  Run: { kind: "platform", reason: MAC_ONLY },
};
