<script lang="ts">
  import type { SessionInfo } from "$lib/store.svelte";
  import { appLabel, appState, appPr, otherActive } from "$lib/stackapps";
  import HoverCard from "./HoverCard.svelte";
  import PrBadge from "./PrBadge.svelte";

  // The "+N" beside a STACK session's PR badge: N other apps have a PR or local
  // changes. Its hover card lists EVERY app of the session — the badge itself
  // only ever shows the focus app's PR. Renders nothing for a plain session or
  // when no other app has news. No control inside it, so it may sit in a
  // kanban card (itself a <button>).
  let { session }: { session: Pick<SessionInfo, "stack" | "apps" | "focusApp"> } = $props();

  const n = $derived(otherActive(session));
</script>

{#if n > 0}
  <HoverCard class="num rounded bg-pill-grey px-1 text-sm text-pill-grey-fg outline-none focus-visible:ring-1 focus-visible:ring-accent">
    <span aria-label="{n} more app{n === 1 ? '' : 's'}">+{n}</span>
    {#snippet card()}
      <ul class="space-y-1" aria-label="Apps">
        {#each session.apps ?? [] as a (a.name)}
          {@const st = appState(a)}
          <li class="flex items-center gap-3">
            <span class="min-w-0 truncate" class:font-medium={a.name === session.focusApp}>{appLabel(a)}</span>
            <span class="ml-auto shrink-0">
              {#if st === "pr"}
                <PrBadge session={appPr(a)} delivery={a.delivery ?? ""} />
              {:else if st === "changes"}
                <span class="text-warn">changes, no PR yet</span>
              {:else}
                <span class="text-faint">untouched</span>
              {/if}
            </span>
          </li>
        {/each}
      </ul>
    {/snippet}
  </HoverCard>
{/if}
