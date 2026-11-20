// PreviewService: one function per generated method, routed as ROUTES says.
import type * as Gen from "@bindings-generated/desktop";
import { unsupported } from "../errors";
import { MAC_ONLY } from "./reasons";
import type { Routes } from "./routes";

/** PLATFORM. The preview proxy listens on the Mac's loopback. */
export function Open(_session: string, _url: string): Promise<string> {
  return unsupported("PreviewService.Open", MAC_ONLY);
}

export const ROUTES: Routes<typeof Gen.PreviewService> = {
  Open: { kind: "platform", reason: MAC_ONLY },
};
