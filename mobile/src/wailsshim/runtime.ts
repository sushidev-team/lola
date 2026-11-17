// The module vite.config.ts aliases `@wailsio/runtime` at.
//
// Only `Events` is imported by the shared frontend, so everything else Wails
// exports is absent rather than stubbed: a missing export is a build error naming
// the file that wants it. `Call` exists only to fail with a sentence if a
// generated binding is ever loaded instead of the shim.

import * as EventsImpl from "./events";
import { UnsupportedOnMobileError } from "./errors";

export const Events = EventsImpl;

export type { WailsEvent, WailsEventCallback } from "./events";

/**
 * Structurally a Promise, which is all the generated bindings' type
 * annotations ever require: nothing in desktop/frontend/src calls `.cancel()`
 * on a service result. Declaring it as a plain Promise is what lets the shim's
 * service methods return ordinary promises and still satisfy every call site.
 */
export type CancellablePromise<T> = Promise<T>;

/**
 * The generated bindings' IPC entry point. Reaching it means a generated
 * service module was loaded instead of the shim, i.e. the alias table in
 * vite.config.ts (or the `paths` map in tsconfig.json) is wrong.
 */
export const Call = {
  ByID(id: number, ..._args: unknown[]): Promise<never> {
    return Promise.reject(
      new UnsupportedOnMobileError(
        `Call.ByID(${id})`,
        "a generated Wails binding was loaded instead of the shim — check the @bindings/desktop alias",
      ),
    );
  },
  ByName(name: string, ..._args: unknown[]): Promise<never> {
    return Promise.reject(
      new UnsupportedOnMobileError(
        `Call.ByName(${name})`,
        "a generated Wails binding was loaded instead of the shim — check the @bindings/desktop alias",
      ),
    );
  },
};
