import { confirm } from "./confirm.svelte";
import { nav } from "./nav.svelte";
import { store } from "./store.svelte";
import { displayName } from "./slug";

export type BulkAction = { id: string; label: string; variant?: "danger"; enabled: boolean; run: () => void };

const label = (name: string) => {
  const p = store.projectByName(name);
  return p ? displayName(p) : name;
};

/**
 * What can be done to several projects at once: polling for all of them, and removing them from
 * lola's configuration. A stack takes member choices to remove, so removal skips stacks and says so.
 */
export function bulkProjectActions(names: string[]): BulkAction[] {
  const known = names.filter((n) => store.projectByName(n));
  const removable = known.filter((n) => store.projectByName(n)?.kind !== "stack");
  const each = async (run: (name: string) => Promise<unknown> | unknown) => { for (const n of known) await run(n); };
  return [
    { id: "resume", label: `Resume polling (${known.length})`, enabled: known.length > 0, run: () => void each((n) => store.enablePoll(n)) },
    { id: "pause", label: `Pause polling (${known.length})`, enabled: known.length > 0, run: () => void each((n) => store.disablePoll(n)) },
    {
      id: "remove",
      label: `Remove ${removable.length} project${removable.length === 1 ? "" : "s"}…`,
      variant: "danger",
      enabled: removable.length > 0,
      run: () =>
        confirm.ask({
          title: `Remove ${removable.length} projects?`,
          subject: removable.map(label).join(", "),
          body: "",
          detail:
            "Only lola's configuration changes: repositories on disk, running sessions and their worktrees are left alone." +
            (removable.length < known.length ? " Stacks are skipped; remove them one at a time." : ""),
          confirmLabel: "Remove",
          onConfirm: () => {
            void (async () => { for (const n of removable) await store.removeProject(n); nav.scopeTo(""); })();
          },
        }),
    },
    { id: "clear", label: "Clear selection", enabled: true, run: () => nav.scopeTo("") },
  ];
}
