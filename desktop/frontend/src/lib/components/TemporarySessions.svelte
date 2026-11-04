<script lang="ts">
  import { store } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import { openSession, openShell } from "$lib/projectActions";
  import { sessionMenu } from "$lib/sessionmenu.svelte";
  const sessions = $derived(store.sessions.filter(s => s.location === "temporary"));
</script>
<section class="px-3 py-2" aria-label="Temporary sessions">
  <div class="flex items-center justify-between px-2 py-1"><span class="text-sm text-faint">Temporary sessions</span><button class="rounded px-1 text-faint hover:bg-sel" aria-label="New temporary shell" title="New temporary shell" onclick={() => void openShell()}>+</button></div>
  {#each sessions as s (s.id)}
    <button class="block w-full truncate rounded px-2 py-1.5 text-left text-sm hover:bg-sel {nav.selectedId === s.id ? 'bg-sel' : ''}" title={s.worktree} onclick={() => openSession(s.id)} oncontextmenu={(e) => sessionMenu.open(s.id,e)}>{s.title || "Shell"}</button>
  {/each}
</section>
