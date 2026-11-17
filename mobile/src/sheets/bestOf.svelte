<script lang="ts">
  import { onMount } from "svelte";
  import { bestOf } from "$lib/bestof.svelte";
  import EmptyState from "@mobile/lib/components/EmptyState.svelte";
  import Sheet from "@mobile/lib/components/Sheet.svelte";
  import StatusDot from "@mobile/lib/components/StatusDot.svelte";
  import { copy, fill } from "@mobile/lib/copy";
  import { router } from "@mobile/lib/router.svelte";
  import { can } from "@mobile/lib/tiers.svelte";

  // The attempts of one best-of group, side by side (spec §5.3). The store is
  // pointed at the session directly: its `open` also raises a desktop overlay
  // this screen has no use for. Pick asks through the shared confirm.

  const BEST_OF_POLL_MS = 5000;
  const agentName = (agent: string): string =>
    (copy.agents as Record<string, string>)[agent] ?? agent.charAt(0).toUpperCase() + agent.slice(1);
  const id = $derived(router.top.name === "session" ? router.top.id : "");
  const data = $derived(bestOf.data);
  // An older Mac answers `unknown_cmd`, which arrives as one sentence; it is a state, not an error line.
  const needsUpdate = $derived(bestOf.error === copy.refusals.update);

  onMount(() => {
    bestOf.session = id;
    bestOf.data = null;
    bestOf.error = "";
    bestOf.problems = [];
    void bestOf.load();
    const stop = bestOf.poll(BEST_OF_POLL_MS);
    return () => {
      stop();
      bestOf.session = "";
      bestOf.data = null;
    };
  });
</script>

<Sheet label={copy.bestOf.title} title={copy.bestOf.title} detent="fit" onclose={() => router.close()}>
  <div class="flex min-w-0 flex-col gap-3 px-4 pb-2">
    {#if needsUpdate}
      <EmptyState icon="refresh-cw" title="Update needed" body={copy.refusals.update} />
    {:else if bestOf.error}
      <p class="text-subhead text-subtext" role="alert">{bestOf.error}</p>
    {/if}
    {#each bestOf.problems as p (p)}<p class="text-subhead text-subtext">{p}</p>{/each}
    <ul class="flex min-w-0 flex-col gap-2">
      {#each data?.attempts ?? [] as a, i (a.session)}
        {@const working = a.display === "working"}
        <li class="flex min-w-0 items-center gap-2 rounded-[10px] bg-edge-soft p-3" data-attempt={a.session}>
          <div class="flex min-w-0 flex-1 flex-col gap-0.5">
            <p class="text-callout font-medium">
              {fill(copy.bestOf.attempt, { n: i + 1, agent: agentName(a.agent) })}
            </p>
            <p class="text-footnote text-faint">
              {a.files} files · +{a.additions} −{a.deletions}{a.prNumber ? ` · #${a.prNumber}` : ""}
            </p>
            <p class="flex items-center gap-1.5 text-footnote text-faint">
              <StatusDot tone={working ? "text-info" : "text-good"} />
              <span>{working ? copy.bestOf.states.working : copy.bestOf.states.done}{a.winner ? ` · ${copy.bestOf.kept}` : ""}</span>
            </p>
          </div>
          {#if can("bestOfPick") && !data?.picked}
            <button
              type="button"
              class="min-h-11 min-w-11 shrink-0 touch-manipulation px-2 text-callout text-accent active:opacity-60 disabled:opacity-40"
              disabled={bestOf.busy || working}
              onclick={() => bestOf.askPick(a.session)}
            >
              {copy.bestOf.pick}
            </button>
          {/if}
        </li>
      {/each}
    </ul>
  </div>
</Sheet>
