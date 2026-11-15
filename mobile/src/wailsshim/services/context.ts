// ContextService: one function per generated method, routed as ROUTES says.
import type * as Gen from "@bindings-generated/desktop";
import type * as M from "@bindings/desktop/models";
import type { Routes } from "./routes";

/** LOCAL: no-op; there is no desktop window to mirror context to. */
export function Publish(_current: M.DesktopContext): Promise<void> {
  return Promise.resolve();
}

export const ROUTES: Routes<typeof Gen.ContextService> = {
  Publish: { kind: "local" },
};
