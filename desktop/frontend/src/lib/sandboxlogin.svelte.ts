// The pre-start login check for sandboxed sessions. A sandboxed claude can
// only log in with credentials that exist when its container starts, so a
// session started before the Claude login is set up prints "Not logged in" and
// waits. `ensure` asks the backend first; when a login is missing it opens
// SandboxLoginDialog and resolves once the person logged in (true) or gave up
// (false). The daemon refuses the same start, so this is the friendly half.
import { ConfigService } from "@bindings/desktop";

export type SandboxLoginRequest = { project: string; agent: string };

class SandboxLoginGate {
  request = $state<SandboxLoginRequest | null>(null);
  #resolve: ((ok: boolean) => void) | null = null;

  async ensure(project: string, agent = ""): Promise<boolean> {
    if (!project) return true;
    let needed = false;
    try {
      needed = await ConfigService.SandboxLoginNeeded(project, agent);
    } catch {
      return true; // unknown: let the daemon decide
    }
    if (!needed) return true;
    this.#resolve?.(false);
    return new Promise<boolean>((resolve) => {
      this.#resolve = resolve;
      this.request = { project, agent };
    });
  }

  finish(ok: boolean) {
    const resolve = this.#resolve;
    this.#resolve = null;
    this.request = null;
    resolve?.(ok);
  }
}

export const sandboxLogin = new SandboxLoginGate();
