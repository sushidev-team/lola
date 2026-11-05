<script lang="ts">
  // The sidebar's direct sessions, beside Temporary sessions and the same shape.
  // Shown only once hosts exist (or a session does), so a config with no [[ssh_host]]
  // never grows a section.
  import { onMount } from "svelte";
  import { store } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import { ssh } from "$lib/ssh.svelte";
  import { terms, SSH } from "$lib/terms.svelte";
  import { openSession } from "$lib/projectActions";
  import { sessionMenu } from "$lib/sessionmenu.svelte";
  import { sshNeedsHuman, tierShort, urgentConn } from "$lib/ssh";
  import Button from "../Button.svelte";

  const sessions = $derived(store.sessions.filter((s) => s.location === "ssh"));
  const shown = $derived(ssh.hosts.length > 0 || sessions.length > 0);

  onMount(() => {
    void ssh.loadHosts();
  });

  function open(id: string) {
    terms.select(id, SSH);
    openSession(id);
  }
</script>

{#if shown}
  <section class="px-3 py-2" aria-label="SSH sessions">
    <div class="flex items-center justify-between px-2 py-1">
      <span class="text-sm text-faint">SSH sessions</span>
      <Button size="xs" icon class="h-5! w-5!" aria-label="New SSH session" title="New SSH session (⌘⇧H)" onclick={() => ssh.newSession()}>+</Button>
    </div>
    {#each sessions as s (s.id)}
      {@const c = urgentConn(s.ssh?.connections)}
      <button
        class="flex w-full items-center gap-1.5 rounded px-2 py-1.5 text-left text-sm hover:bg-sel {nav.selectedId === s.id ? 'bg-sel' : ''}"
        title={s.title}
        onclick={() => open(s.id)}
        oncontextmenu={(e) => sessionMenu.open(s.id, e)}
      >
        {#if c}<span class="label shrink-0 text-faint">{tierShort(c.tier)}</span>{/if}
        <span class="min-w-0 flex-1 truncate">{s.title || c?.label || c?.host || "SSH session"}</span>
        {#if sshNeedsHuman(s)}<span class="h-1.5 w-1.5 shrink-0 rounded-full bg-orange" aria-label="Needs you"></span>{/if}
      </button>
    {/each}
  </section>
{/if}
