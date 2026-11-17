// Type-only: imported by nothing, compiled by `npm run check`. A generated
// service method the shim lacks, mis-types, or exports without being listed
// here as an extra fails the build, so the shim cannot drift from the bindings.
import type * as Gen from "@bindings-generated/desktop";
import type * as Shim from "./desktop";

/** Mobile-only methods allowed on a generated namespace. */
type Extras = { DaemonService: "Panes" | "ShellCreate" | "PaneClose" | "PaneResize" };
/** Generated methods deliberately not exported (shared code feature-probes them). */
type Absent = {
  ConfigService: "GetAppearance" | "SetAppearance" | "GetTheme" | "SetTheme" | "PreviewTheme" | "Themes";
};

type Check<S, G, X extends string = never, A extends string = never> = {
  [K in Exclude<keyof G, A>]-?: K extends keyof S
    ? S[K] extends G[K]
      ? true
      : { error: "signature differs"; method: K }
    : { error: "missing"; method: K };
} & { [K in Exclude<keyof S, keyof G | X>]: { error: "extra"; method: K } } & {
  [K in Extract<keyof S, A>]: { error: "absent method exported"; method: K };
};

type AllTrue<T> = { [K in keyof T]: T[K] extends true ? true : T[K] }[keyof T] extends true ? true : false;
type Svc = keyof typeof Gen;
/** Every service module also exports its ROUTES table (services/routes.ts). */
type X<N> = "ROUTES" | (N extends keyof Extras ? Extras[N] : never);
type A<N> = N extends keyof Absent ? Absent[N] : never;
type Report = { [N in Svc]: AllTrue<Check<(typeof Shim)[N], (typeof Gen)[N], X<N>, A<N>>> };

const ok: { [N in Svc]: true } = null as unknown as Report;
void ok;

/** Every generated service has a shim module. */
const services: Record<Svc, unknown> = null as unknown as typeof Shim;
void services;
