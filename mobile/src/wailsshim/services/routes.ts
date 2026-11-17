import type { Reason } from "./reasons";

/**
 * How one generated service method is answered on the phone.
 *
 *  - fwd: one request; its tier is `TIER[cmd]` (wire/tiers.ts), never written here.
 *  - local: answered on the phone.
 *  - platform: rejects `UnsupportedOnMobileError(method, reason)`.
 *  - absent: deliberately not exported; shared code feature-probes it.
 */
export type Route =
  | { kind: "fwd"; cmd: string }
  | { kind: "local" }
  | { kind: "platform"; reason: Reason }
  | { kind: "absent" };

/** Exactly the generated keys, plus the mobile-only extras `X`. */
export type Routes<S, X extends string = never> = Record<(keyof S & string) | X, Route>;
