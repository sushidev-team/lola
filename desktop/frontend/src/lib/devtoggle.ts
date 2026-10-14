// devtoggle.ts — the dev toggle's tooltip, shared by the session menu and the
// terminal header so both describe the same consequence.
//
// What a click does depends on the project: without a port_range only ONE
// session per project may run dev_commands, so starting them here STOPS them
// wherever they were; with one, every session binds its own leased ports
// ($LOLA_PORT) and the stacks run side by side. Saying "stops them elsewhere"
// for a multi-active project would make a human hesitate over a click that
// takes nothing from anyone.
import type { SessionInfo } from "./store.svelte";

type DevSession = Pick<SessionInfo, "devActive" | "devCommands" | "devParallel" | "ports">;

export function devToggleTitle(s: DevSession): string {
  const cmds = (s.devCommands ?? []).join(", ");
  const ports = s.ports?.length ? ` on port${s.ports.length > 1 ? "s" : ""} ${s.ports.join(", ")}` : "";
  if (s.devActive) return `stop ${cmds}${ports}`;
  if (s.devParallel) return `run ${cmds} here${ports} (other sessions keep theirs)`;
  return `run ${cmds} here (stops them in any other session of this project)`;
}
