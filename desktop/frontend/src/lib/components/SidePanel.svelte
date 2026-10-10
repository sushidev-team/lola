<script lang="ts">
  import type { SessionInfo } from "$lib/store.svelte";
  import { sidePanel, REPORT, CHECKPOINTS_TAB, type SideTab } from "$lib/sidepanel.svelte";
  import { checkpoints } from "$lib/checkpoints.svelte";
  import Tabs from "./Tabs.svelte";
  import BoardPanel from "./BoardPanel.svelte";
  import CheckpointList from "./CheckpointList.svelte";

  // The session view's sidebar, beside the live terminal: the agent's report
  // (its CLAIM about its progress) and the turn checkpoints (lola's FACTS about
  // what each turn changed). Both are per-session reading material that wants a
  // narrow column next to the terminal, not the whole pane — which is why
  // checkpoints are a tab HERE rather than in the terminal tab strip, which
  // stays for things you type into. SessionEmbed decides whether the sidebar is
  // shown at all; this decides which tab.
  let { session }: { session: SessionInfo } = $props();

  const count = $derived(checkpoints.of(session.id).list.length);
  const tabs = $derived([
    ...(session.board ? [{ id: REPORT, label: session.board.blocked ? "Report ⏸" : "Report" }] : []),
    ...(session.worktree ? [{ id: CHECKPOINTS_TAB, label: count ? `Checkpoints ${count}` : "Checkpoints" }] : []),
  ]);
  // The remembered tab when this session has it, else whichever it does have.
  const active = $derived<SideTab>(
    tabs.some((t) => t.id === sidePanel.tab) ? sidePanel.tab : ((tabs[0]?.id as SideTab) ?? REPORT),
  );
</script>

<aside
  class="grid h-full min-h-0 w-72 grid-rows-[auto_minmax(0,1fr)] border-l border-edge/60 bg-canvas"
  aria-label="Session sidebar"
>
  <div class="px-2 pt-1">
    <Tabs {tabs} {active} onSelect={(id) => sidePanel.select(id as SideTab)} />
  </div>
  <!-- -mt-3 takes back the gap Tabs leaves under itself for the config forms;
       here each tab body opens with its own padded header. -->
  <div class="-mt-3 min-h-0 overflow-y-auto" role="tabpanel">
    {#if active === REPORT}
      <BoardPanel {session} />
    {:else}
      <CheckpointList sessionId={session.id} />
    {/if}
  </div>
</aside>
