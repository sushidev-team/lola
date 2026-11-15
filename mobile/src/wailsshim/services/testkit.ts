// Shared route assertions for services/*.test.ts. The importing test file must
// `vi.mock("../bridge")` so a forwarded call is observable without a transport.
import { readFileSync, readdirSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it, vi } from "vitest";

import { TIER } from "../../wire";
import { bridge } from "../bridge";
import { UnsupportedOnMobileError } from "../errors";
import type { Route } from "./routes";

const REPO = resolve(__dirname, "../../../..");
const BINDINGS = resolve(REPO, "desktop/frontend/bindings/github.com/sushidev-team/lola/desktop");

/** The methods the generated binding of `service` exports. */
export function generatedMethods(service: string): string[] {
  const src = readFileSync(resolve(BINDINGS, `${service.toLowerCase()}.ts`), "utf8");
  return [...src.matchAll(/^export function (\w+)\(/gm)].map((m) => m[1]);
}

type Fn = (...a: unknown[]) => Promise<unknown>;

/**
 * What the daemon keeps of a phone's request, read from its source:
 * `internal/remote/fields.go` rebuilds every remote request from the flat fields
 * its command lists and silently drops the rest, and `reviewedArgsFields`
 * (fields_test.go) names the JSON fields of each command's Args. A field the
 * shim puts anywhere else reaches the handler as a zero value, with no error.
 */
export interface RemoteFieldRules {
  /** cmd -> the top-level request fields it keeps (`args` included when it takes Args). */
  flat: Map<string, Set<string>>;
  /** cmd -> the top-level JSON fields of its Args. */
  args: Map<string, Set<string>>;
}

function readGo(path: string): string {
  return readFileSync(resolve(REPO, path), "utf8");
}

/** The index just past the bracket that closes the one at `open`. */
function closing(src: string, open: number, l: string, r: string): number {
  let depth = 0;
  for (let i = open; i < src.length; i++) {
    if (src[i] === l) depth++;
    else if (src[i] === r && --depth === 0) return i + 1;
  }
  throw new Error(`testkit: unbalanced ${l}${r} at ${open}`);
}

export function remoteFieldRules(): RemoteFieldRules {
  const consts = new Map<string, string>();
  for (const file of readdirSync(resolve(REPO, "internal/protocol"))) {
    if (!file.endsWith(".go")) continue;
    for (const m of readGo(`internal/protocol/${file}`).matchAll(/\b(Cmd\w+)\s*=\s*"([^"]+)"/g)) consts.set(m[1], m[2]);
  }

  const fields = readGo("internal/remote/fields.go");
  const names = new Map<string, string>();
  const table = fields.slice(fields.indexOf("var flatNames"));
  for (const m of table.slice(0, table.indexOf("\n}")).matchAll(/(f\w+):\s*"(\w+)"/g)) names.set(m[1], m[2]);

  const flat = new Map<string, Set<string>>();
  for (let at = fields.indexOf("put(fieldSpec{"); at >= 0; at = fields.indexOf("put(fieldSpec{", at + 1)) {
    const open = fields.indexOf("{", at);
    const specEnd = closing(fields, open, "{", "}");
    const spec = fields.slice(open, specEnd);
    const bits = /fields:\s*([\w\s|]+?)\s*(?:,|})/.exec(spec)?.[1].split("|").map((b) => b.trim()) ?? [];
    const keep = new Set(bits.map((b) => names.get(b) ?? `?${b}`));
    const rest = fields.slice(specEnd, fields.indexOf(")", specEnd));
    for (const m of rest.matchAll(/"([^"]+)"|protocol\.(Cmd\w+)/g)) {
      const cmd = m[1] ?? consts.get(m[2]) ?? `?${m[2]}`;
      flat.set(cmd, keep);
    }
  }

  const reviewed = readGo("internal/remote/fields_test.go");
  const block = reviewed.slice(reviewed.indexOf("var reviewedArgsFields"));
  const args = new Map<string, Set<string>>();
  for (const m of block.slice(0, block.indexOf("\n}")).matchAll(/"(\w+)":\s*\{([^}]*)\}/g)) {
    args.set(m[1], new Set([...m[2].matchAll(/"([^"]+)"/g)].map((f) => f[1].split(".")[0])));
  }
  return { flat, args };
}

/** Fails unless the daemon keeps every field of this request where the shim put it. */
export function expectKeptByDaemon(rules: RemoteFieldRules, cmd: string, fields: unknown): void {
  const keep = rules.flat.get(cmd);
  expect(keep, `${cmd} has no field spec in internal/remote/fields.go`).toBeDefined();
  const sent = (fields ?? {}) as Record<string, unknown>;
  for (const key of Object.keys(sent)) {
    expect(keep!.has(key), `${cmd}: the daemon drops the flat field "${key}"`).toBe(true);
  }
  if (sent.args === undefined) return;
  expect(typeof sent.args, `${cmd}: args must be an object`).toBe("object");
  const allowed = rules.args.get(cmd) ?? new Set<string>();
  for (const key of Object.keys(sent.args as object)) {
    expect(allowed.has(key), `${cmd}: "${key}" is not a field of its Args`).toBe(true);
  }
}

interface Options {
  /** Mobile-only routes beyond the generated keys. */
  extras?: string[];
  /** Forwarded methods whose request shape is covered by a dedicated test. */
  skipSend?: string[];
}

/** The structural contract of one service module, for every route it declares. */
export function describeRoutes(
  service: string,
  mod: Record<string, unknown>,
  routes: Record<string, Route>,
  { extras = [], skipSend = [] }: Options = {},
): void {
  const request = vi.mocked(bridge.request);
  describe(`${service} routes`, () => {
    it("has exactly the generated keys (plus declared extras)", () => {
      expect(Object.keys(routes).sort()).toEqual([...generatedMethods(service), ...extras].sort());
    });

    it("exports every route except an absent one", () => {
      for (const [name, route] of Object.entries(routes)) {
        if (route.kind === "absent") expect(mod[name], name).toBeUndefined();
        else expect(typeof mod[name], name).toBe("function");
      }
    });

    it("sends a forwarded route's cmd, and a tier that can run", () => {
      for (const [name, route] of Object.entries(routes)) {
        if (route.kind !== "fwd") continue;
        expect(TIER[route.cmd], `${name} -> ${route.cmd}`).toBeDefined();
        expect(TIER[route.cmd], `${name} -> ${route.cmd}`).not.toBe("never");
      }
    });

    for (const [name, route] of Object.entries(routes)) {
      if (route.kind === "fwd" && !skipSend.includes(name)) {
        it(`${name} sends cmd=${route.cmd}`, async () => {
          request.mockClear();
          request.mockResolvedValue({});
          await (mod[name] as Fn)({}).catch(() => {});
          expect(request).toHaveBeenCalledTimes(1);
          expect(request.mock.calls[0][1]).toBe(route.cmd);
        });
      }
      if (route.kind === "platform") {
        it(`${name} rejects UnsupportedOnMobileError with its reason`, async () => {
          request.mockClear();
          const err = await (mod[name] as Fn)().then(
            () => null,
            (e: unknown) => e,
          );
          expect(err).toBeInstanceOf(UnsupportedOnMobileError);
          expect((err as UnsupportedOnMobileError).method).toBe(`${service}.${name}`);
          expect((err as UnsupportedOnMobileError).reason).toBe(route.reason);
          expect(request).not.toHaveBeenCalled();
        });
      }
    }
  });
}
