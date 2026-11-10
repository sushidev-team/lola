// The two removal questions, shared by the sidebar's context menu and the
// project form, so both ask the same way through the one confirm dialog.
import { confirm } from "./confirm.svelte";
import { store } from "./store.svelte";
import { displayName } from "./slug";

const name = (id: string) => {
  const p = store.projectByName(id);
  return p ? displayName(p) : id;
};

/** Removes one project (a stack member included) from lola's config. */
export function askRemoveProject(project: string) {
  confirm.ask({
    title: "Remove project?",
    body: `Remove ${name(project)} from lola?`,
    detail: "Only lola's configuration changes: the repository on disk, running sessions and their worktrees are left alone.",
    confirmLabel: "Remove",
    onConfirm: () => void store.removeProject(project),
  });
}

/**
 * Removes a stack. Every member is listed with a checkbox: a checked member
 * stays in lola as a standalone project, an unchecked one is removed with the
 * stack. All are checked to start, so the default loses nothing.
 */
export function askRemoveStack(stack: string) {
  const members = store.projects.filter((p) => p.stack === stack);
  confirm.ask({
    title: "Remove stack?",
    body: `Remove stack ${name(stack)}?`,
    detail: members.length
      ? "Checked projects stay in lola on their own; unchecked ones are removed with the stack. Repositories on disk are left alone."
      : "Repositories on disk are left alone.",
    confirmLabel: "Remove",
    choices: members.length
      ? { label: "Keep in lola", items: members.map((m) => ({ id: m.name, label: displayName(m), checked: true })) }
      : undefined,
    onConfirm: (keep) => void store.removeStack(stack, keep),
  });
}
