// The pre-start login check for sandboxed sessions. A sandboxed claude can
// only log in with credentials that exist when its container starts, so a
// session started before the Claude login is set up prints "Not logged in" and
// waits. `ensure` asks the backend first; when a login is missing it opens
// SandboxLoginDialog and resolves once the person logged in (true) or gave up
// (false). The daemon refuses the same start, so this is the friendly half.
import { ConfigService } from "@bindings/desktop";

/** agent is the one missing a login: "claude", "codex" or "opencode". */
export type SandboxLoginRequest = { project: string; agent: string };

class SandboxLoginGate {
  request = $state<SandboxLoginRequest | null>(null);
  #resolve: ((ok: boolean) => void) | null = null;

  async ensure(project: string, agent = ""): Promise<boolean> {
    if (!project) return true;
    let missing = "";
    try {
      missing = (await ConfigService.SandboxLoginNeeded(project, agent)) ?? "";
    } catch {
      return true; // unknown: let the daemon decide
    }
    if (!missing) return true;
    this.#resolve?.(false);
    return new Promise<boolean>((resolve) => {
      this.#resolve = resolve;
      this.request = { project, agent: missing };
    });
  }

  /** Asks again for the open request; finishes it once the login exists. */
  async recheck(): Promise<boolean> {
    const req = this.request;
    if (!req) return false;
    try {
      if (!(await ConfigService.SandboxLoginNeeded(req.project, req.agent))) {
        this.finish(true);
        return true;
      }
    } catch {
      // keep asking
    }
    return false;
  }

  finish(ok: boolean) {
    const resolve = this.#resolve;
    this.#resolve = null;
    this.request = null;
    resolve?.(ok);
  }
}

export const sandboxLogin = new SandboxLoginGate();
