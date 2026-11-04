import { nav } from "./nav.svelte";
import { store, type ProjectInfo } from "./store.svelte";

export type ProjectAction = { id: string; label: string; shortcut?: string; enabled: boolean; run: () => void };

export function openSession(id: string, project = "") {
  const session = store.sessionById(id);
  nav.setTriage("");
  nav.goCockpit(session?.project || project);
  nav.setLens("list");
  nav.select(id);
  nav.toggleFocusTerm(id);
}

export async function openShell(project = "") {
  const result = await store.openManual({ project, branch: "", agent: false, location: project ? "main" : "temporary" });
  if (result?.sessionId) openSession(result.sessionId, project);
}

// One action catalog for the project page, context menu and command launcher.
export function projectActions(p: ProjectInfo): ProjectAction[] {
  return [
    { id: "open", label: "Open project", shortcut: "↵", enabled: true, run: () => nav.goDetail(p.name) },
    { id: "new", label: "New session…", shortcut: "⌘↵", enabled: !!p.path, run: () => nav.newSession(p.name) },
    { id: "shell", label: "Open shell here", shortcut: "⌘O", enabled: !!p.path, run: () => { void openShell(p.name); } },
    { id: "worktree", label: "New worktree session…", enabled: !!p.path, run: () => nav.newSession(p.name, "worktree") },
    { id: "ticket", label: "Start a ticket", enabled: true, run: () => nav.goTicketPicker(p.name) },
    { id: "pr", label: "Open a PR", enabled: p.repoConfigured, run: () => nav.goPRPicker(p.name) },
    { id: "edit", label: "Edit project", enabled: true, run: () => nav.openOverlay("project", p.name, "repo") },
  ];
}
