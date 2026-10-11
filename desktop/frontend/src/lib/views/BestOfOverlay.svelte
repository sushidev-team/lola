<script lang="ts">
  // Best-of-N compare view (SUSHI-623): every attempt of one group side by side —
  // agent/model, the agent pill, the PR and its CI, the review findings recorded
  // for the CURRENT PR, the diff size and the tokens spent — and the one action
  // that ends the race: keep an attempt.
  //
  // The ★ is the daemon's SUGGESTION (internal/bestof.Suggest: CI, then review
  // findings, then diff size). It is a hint, styled as one; the pick is always a
  // click, and it goes through the confirm dialog because it closes PRs and
  // tears sessions down.
  import { onDestroy } from "svelte";
  import { bestOf } from "$lib/bestof.svelte";
  import { store } from "$lib/store.svelte";
  import { ALL_DISPLAYS, displayLabel, displayPill, deliveryLabel, deliveryText, type Display } from "$lib/theme";
  import Modal from "$lib/components/Modal.svelte";
  import Button from "$lib/components/Button.svelte";

  const d = $derived(bestOf.data);
  const picked = $derived(d?.picked ?? "");

  // The attempts keep moving (CI lands, a review answers) while the overlay is
  // open, so it re-reads on the same cadence the observer refreshes facts.
  // Chained, not an interval: the next read starts only after the last settled.
  const stop = bestOf.poll(15_000);
  onDestroy(stop);

  function who(a: { agent: string; model?: string }): string {
    return a.model ? `${a.agent}@${a.model}` : a.agent;
  }

  function ciClass(c: string | undefined): string {
    switch (c) {
      case "pass":
        return "text-good";
      case "fail":
        return "text-bad";
      case "pending":
        return "text-warn";
      default:
        return "text-faint";
    }
  }

  // The daemon already reduced the agent axis to the Display vocabulary
  // (state.DisplayFor); an unknown word reads as working, like displayFor's.
  function asDisplay(v: string): Display {
    return (ALL_DISPLAYS as string[]).includes(v) ? (v as Display) : "working";
  }

  function compact(n: number): string {
    if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
    if (n >= 1_000) return `${Math.round(n / 1_000)}k`;
    return String(n);
  }
</script>

<Modal title={d ? `Best-of · ${d.issue}` : "Best-of"} onClose={() => bestOf.close()} width="980px">
  {#if bestOf.error}
    <p class="text-bad">{bestOf.error}</p>
  {:else if !d}
    <p class="text-faint">{bestOf.loading ? "Comparing attempts…" : "No comparison yet."}</p>
  {:else}
    <p class="mb-3 truncate text-dim" title={d.title}>{d.title}</p>
    <table class="w-full table-fixed text-sm">
      <thead class="label text-left text-faint">
        <tr>
          <th class="w-[40px] px-2 py-1.5">#</th>
          <th class="px-2 py-1.5">Agent</th>
          <th class="w-[96px] px-2 py-1.5">State</th>
          <th class="w-[150px] px-2 py-1.5">PR</th>
          <th class="w-[64px] px-2 py-1.5">CI</th>
          <th class="w-[120px] px-2 py-1.5">Review</th>
          <th class="w-[130px] px-2 py-1.5">Diff</th>
          <th class="w-[64px] px-2 py-1.5">Tokens</th>
          <th class="w-[92px] px-2 py-1.5"></th>
        </tr>
      </thead>
      <tbody>
        {#each d.attempts as a (a.session)}
          {@const disp = asDisplay(a.display)}
          {@const suggested = !picked && a.session === d.suggested}
          <tr class="border-t border-edge/30" class:bg-sel={a.winner} class:opacity-50={!!picked && !a.winner}>
            <td class="num px-2 py-2 text-faint">
              {#if a.winner}<span class="text-good" title="kept">✓</span>{:else if suggested}<span
                  class="text-warn"
                  title="suggested — CI, then review findings, then diff size">★</span
                >{/if}{a.attempt}
            </td>
            <td class="truncate px-2 py-2" title={a.session}>
              <span class="text-ink">{who(a)}</span>
              <span class="block truncate font-mono text-faint">{a.branch}</span>
            </td>
            <td class="px-2 py-2">
              <span class="rounded px-1.5 py-[1px] {displayPill(disp)}">{displayLabel(disp)}</span>
            </td>
            <td class="px-2 py-2 whitespace-nowrap">
              {#if (a.prNumber ?? 0) > 0}
                <Button
                  variant="bare"
                  size="xs"
                  class="num h-auto! px-0! py-0! text-magenta! enabled:hover:underline"
                  title="open pull request #{a.prNumber}"
                  onclick={() => store.openURL(a.prUrl ?? "")}>#{a.prNumber}</Button
                >
                {#if a.draft}<span class="text-faint">draft</span>{/if}
                <span class={deliveryText(a.delivery)}>{deliveryLabel(a.delivery)}</span>
              {:else}
                <span class="text-faint">—</span>
              {/if}
            </td>
            <td class="px-2 py-2 {ciClass(a.checks)}">{a.checks || "—"}</td>
            <td class="px-2 py-2">
              {#if a.review}
                {#if a.review.blocker + a.review.major + a.review.minor + a.review.other === 0}
                  <span class="text-good">clean</span>
                {:else}
                  <span class="num" title="blocker · major · minor findings across {a.review.passes} pass(es)">
                    <span class:text-bad={a.review.blocker > 0}>{a.review.blocker}B</span>
                    <span class:text-orange={a.review.major > 0}>{a.review.major}M</span>
                    <span class="text-faint">{a.review.minor + a.review.other}m</span>
                  </span>
                {/if}
              {:else}
                <span class="text-faint" title="no review pass has answered for this PR yet">—</span>
              {/if}
            </td>
            <td class="num px-2 py-2 whitespace-nowrap">
              {#if a.diffError}
                <span class="text-faint" title={a.diffError}>?</span>
              {:else}
                {a.files} files <span class="text-good">+{a.additions}</span>
                <span class="text-bad">−{a.deletions}</span>
              {/if}
            </td>
            <td class="num px-2 py-2 text-faint">{a.usage ? compact(a.usage.tokens) : "—"}</td>
            <td class="px-2 py-1 text-right">
              {#if !picked}
                <Button
                  size="xs"
                  variant={suggested ? "accent" : "secondary"}
                  loading={bestOf.busy}
                  onclick={() => bestOf.askPick(a.session)}>Keep</Button
                >
              {/if}
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
    <p class="mt-3 text-faint">
      {#if picked && bestOf.problems.length}
        Kept {picked}, but some steps did not complete — finish them by hand:
      {:else if picked}
        Kept {picked}. The other attempts were closed and torn down.
      {:else if d.suggested}
        ★ is a suggestion (CI, then review findings, then diff size). Keeping an attempt marks its PR ready, closes
        the others' PRs and removes their worktrees — one with uncommitted changes is kept.
      {:else}
        No attempt has an open PR yet. Each one opens a draft PR when it is done.
      {/if}
    </p>
    {#if bestOf.problems.length}
      <ul class="mt-1 list-disc pl-5 text-warn">
        {#each bestOf.problems as p (p)}<li>{p}</li>{/each}
      </ul>
    {/if}
  {/if}
</Modal>
