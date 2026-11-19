<script lang="ts" module>
  import type { SessionInfo } from "$lib/store.svelte";

  export type MenuRow =
    | "rename"
    | "switchAgent"
    | "model"
    | "dev"
    | "devUrl"
    | "tasks"
    | "checkpoints"
    | "bestOf"
    | "broadcast"
    | "plan"
    | "review"
    | "coderabbit"
    | "revive"
    | "resolve"
    | "remove";

  /** The rows a session's menu draws, in order; a row the device cannot do is absent (spec §5.4). */
  export function menuRows(s: SessionInfo, can: (cmd: string) => boolean): MenuRow[] {
    const dead = s.status === "dead" || s.status === "session_ended";
    const prReview = s.prNumber > 0 && s.location !== "ssh";
    const rows: [MenuRow, boolean][] = [
      ["rename", can("renameSession")],
      ["switchAgent", can("switchAgent") && !s.location && s.status !== "shell"],
      // The composer's ModelSettings shows for any session that is not running a turn-less shell or dead.
      ["model", can("agentSetting") && !s.goalId && !dead && s.status !== "shell" && !s.location],
      ["plan", can("planDecide") && s.plan?.gate === "submitted"],
      ["tasks", can("sessionTask")],
      ["checkpoints", can("checkpoints") && !s.location],
      ["bestOf", can("bestOfCompare") && !!s.bestOf],
      ["broadcast", can("broadcast") && !!s.project],
      ["dev", can("dev") && (s.devCommands?.length ?? 0) > 0],
      ["devUrl", !!s.devActive && (s.devForwards?.length ?? 0) > 0],
      ["review", can("review") && prReview],
      ["coderabbit", can("coderabbit") && prReview],
      ["revive", can("revive") && dead],
      ["resolve", can("resolveConflict") && s.delivery === "merge_conflict"],
      ["remove", can("kill")],
    ];
    return rows.filter(([, ok]) => ok).map(([r]) => r);
  }
</script>

<script lang="ts">
  import type { IconName } from "$lib/icons";
  import { store } from "$lib/store.svelte";
  import { confirm } from "$lib/confirm.svelte";
  import ListGroup from "@mobile/lib/components/ListGroup.svelte";
  import ListRow from "@mobile/lib/components/ListRow.svelte";
  import Sheet from "@mobile/lib/components/Sheet.svelte";
  import { displayFor, displayLabel } from "$lib/theme";
  import { copy } from "@mobile/lib/copy";
  import { statusWord } from "@mobile/lib/statustone";
  import { router } from "@mobile/lib/router.svelte";
  import { can } from "@mobile/lib/tiers.svelte";

  // The session menu (spec §5.3). One tap closes the sheet, then acts: an action
  // that opens a dialog or another sheet must find nothing of this one in the way.
  // Outcomes and refusals reach the person through `store.flash` (FlashToast).

  const id = $derived(router.top.name === "session" ? router.top.id : "");
  const session = $derived(store.sessionById(id));
  const rows = $derived(session ? menuRows(session, can) : []);
  /** The session's state in one word: a regressed PR names itself, else the agent axis. */
  const stateWord = $derived.by(() => {
    if (!session) return "";
    const d = session.delivery ?? "";
    if (d === "ci_failed" || d === "changes_requested" || d === "merge_conflict") return statusWord(d);
    const w = displayLabel(displayFor(session.agentState));
    return w.charAt(0).toUpperCase() + w.slice(1);
  });
  /** An action sheet's groups, 16px apart: set up, work, review, then Remove alone, then Cancel alone. */
  const GROUPS: readonly (readonly MenuRow[])[] = [
    ["plan", "rename", "switchAgent", "model"],
    ["tasks", "checkpoints", "bestOf", "broadcast", "dev", "devUrl"],
    ["review", "coderabbit", "revive", "resolve"],
  ];
  const groups = $derived(GROUPS.map((g) => g.filter((r) => rows.includes(r))).filter((g) => g.length > 0));
  const hasRemove = $derived(rows.includes("remove"));

  const ICONS: Record<MenuRow, IconName> = {
    rename: "pencil",
    switchAgent: "repeat",
    model: "cpu",
    dev: "terminal",
    devUrl: "globe",
    tasks: "list-checks",
    checkpoints: "history",
    bestOf: "git-compare-arrows",
    broadcast: "megaphone",
    plan: "list-checks",
    review: "shield-check",
    coderabbit: "sparkles",
    revive: "refresh-cw",
    resolve: "git-merge",
    remove: "trash-2",
  };

  function labelFor(row: MenuRow): string {
    const m = copy.session.menu;
    return row === "dev" && session?.devActive ? m.devStop : m[row];
  }

  function act(row: MenuRow): void {
    const s = session;
    if (!s) return;
    const sheet = {
      rename: "rename",
      switchAgent: "switchAgent",
      model: "model",
      devUrl: "devLinks",
      tasks: "tasks",
      checkpoints: "checkpoints",
      bestOf: "bestOf",
      broadcast: "broadcast",
      plan: "plan",
    } as const;
    if (row in sheet) {
      router.open(sheet[row as keyof typeof sheet]);
      return;
    }
    router.close();
    switch (row) {
      case "dev":
        void store.dev(s.id, !s.devActive);
        break;
      case "review":
        void store.review(s.id);
        break;
      case "coderabbit":
        void store.coderabbit(s.id);
        break;
      case "revive":
        void store.revive(s.id);
        break;
      case "resolve":
        void store.resolveConflict(s.id);
        break;
      case "remove":
        confirm.ask({
          title: copy.session.removeConfirm.title,
          body: copy.session.removeConfirm.body,
          confirmLabel: copy.session.removeConfirm.action,
          onConfirm: () => void store.kill(s.id, false, false, () => router.back()),
        });
        break;
    }
  }
</script>

<!-- An action sheet: no header, only the rows. -->
<Sheet label={copy.session.menu.title} onclose={() => router.close()}>
  {#if stateWord}<p class="pb-3 text-center text-footnote text-faint" data-state-word>{stateWord}</p>{/if}
  <div class="flex flex-col gap-4" role="menu" aria-label={copy.session.menu.title}>
    {#each groups as group (group[0])}
      <ListGroup>
        {#each group as row (row)}
          <ListRow role="menuitem" icon={ICONS[row]} label={labelFor(row)} onclick={() => act(row)} />
        {/each}
      </ListGroup>
    {/each}
    {#if hasRemove}
      <ListGroup>
        <ListRow role="menuitem" icon="trash-2" tone="danger" label={copy.session.menu.remove} onclick={() => act("remove")} />
      </ListGroup>
    {/if}
    <ListGroup>
      <ListRow role="menuitem" align="center" tone="accent" label={copy.session.menu.cancel} onclick={() => router.close()} />
    </ListGroup>
  </div>
</Sheet>
