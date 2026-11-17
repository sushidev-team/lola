<script lang="ts">
  import { onMount } from "svelte";
  import type { PrRow } from "@bindings/internal/protocol";
  import Icon from "$lib/components/Icon.svelte";
  import { store } from "$lib/store.svelte";
  import LargeTitle from "@mobile/lib/components/LargeTitle.svelte";
  import TouchButton from "@mobile/lib/components/TouchButton.svelte";
  import VirtualList from "@mobile/lib/components/VirtualList.svelte";
  import { copy } from "@mobile/lib/copy";
  import { PrPickerModel, middleEllipsis } from "@mobile/lib/projects.svelte";
  import { router } from "@mobile/lib/router.svelte";
  import { statusTone, statusWord } from "@mobile/lib/statustone";

  // The repository's open PRs; a tap puts an agent on one. The list is the
  // daemon's cached snapshot, so a stale one says so.
  const project = $derived(router.top.name === "tab" ? (router.top.project ?? "") : "");
  const info = $derived(store.projectByName(project));
  const repoMissing = $derived(info !== undefined && !info.repoConfigured);
  const m = new PrPickerModel();

  onMount(() => {
    if (repoMissing) m.loading = false;
    else void m.load(project);
  });

  /** "dependabot[bot]" is a bot account; "Dependabot" is how it is shown. */
  const isBot = (p: PrRow): boolean => /\[bot\]$/i.test(p.author);
  const authorName = (p: PrRow): string => {
    if (!isBot(p)) return p.author;
    const n = p.author.replace(/\[bot\]$/i, "");
    return n.charAt(0).toUpperCase() + n.slice(1);
  };

  /** The one word a PR's state earns; a draft says so with its pill instead. */
  function word(p: PrRow): string {
    if (p.isDraft) return "";
    switch (p.status) {
      case "review_pending":
        return copy.projects.meta.reviewRequested;
      case "changes_requested":
        return copy.projects.meta.changesRequested;
      case "ci_failed":
        return copy.projects.meta.ciFailed;
      case "ci_pending":
        return copy.projects.meta.ciRunning;
      default:
        return statusWord(p.status);
    }
  }
</script>

<div class="flex h-full min-h-0 flex-col bg-canvas">
  <LargeTitle
    title={copy.projects.prs.title}
    back={{ label: store.displayNameFor(project), onclick: () => router.back() }}
  >
    {#snippet trailing()}
      <TouchButton
        icon
        aria-label={copy.projects.refresh}
        loading={m.loading}
        disabled={m.loading || repoMissing}
        onclick={() => m.load(project, true)}
      >
        <Icon name="refresh-cw" size={20} />
      </TouchButton>
    {/snippet}
  </LargeTitle>

  {#if m.data?.stale}
    <div class="mx-4 mb-2 shrink-0 rounded-xl bg-sel px-3 py-2 text-subhead text-warn" role="status">
      {copy.projects.stale.body}
    </div>
  {/if}

  {#if m.refusal}
    <div class="mx-4 mb-2 flex shrink-0 items-start gap-2 rounded-xl bg-sel pl-3 text-subhead text-warn" role="status">
      <span class="min-w-0 flex-1 py-2">{m.refusal}</span>
      <TouchButton icon aria-label="Dismiss" class="text-warn!" onclick={() => (m.refusal = "")}>
        <Icon name="x" size={14} />
      </TouchButton>
    </div>
  {/if}

  <div class="min-h-0 flex-1">
    {#if repoMissing}
      <p class="px-8 py-12 text-center text-callout text-faint">{copy.projects.noRepo.body}</p>
    {:else if m.loading && !m.data}
      <p class="px-8 py-12 text-center text-callout text-faint">{copy.projects.prs.loading.status}</p>
    {:else if m.error}
      <div class="flex flex-col items-center gap-3 px-8 py-12 text-center">
        <span class="text-headline text-ink">{copy.projects.prs.failed.body}</span>
        <span class="text-callout text-bad">{m.error}</span>
        <TouchButton variant="secondary" loading={m.loading} onclick={() => m.load(project, true)}>
          {copy.projects.retry}
        </TouchButton>
      </div>
    {:else if m.rows.length === 0}
      <p class="px-8 py-12 text-center text-callout text-faint">{copy.projects.prs.none.body}</p>
    {:else}
      <VirtualList items={m.rows} key={(p: PrRow) => String(p.number)} label={copy.projects.prs.title}>
        {#snippet row(p: PrRow)}
          <button
            type="button"
            class="flex w-full touch-manipulation pl-4 text-left active:bg-sel disabled:opacity-55"
            disabled={m.opening !== 0}
            onclick={() => m.open(project, p)}
          >
            <span class="flex min-w-0 flex-1 items-center gap-2 border-b border-edge-soft py-2.5 pr-4">
              <span class="flex min-w-0 flex-1 flex-col gap-0.5">
                <!-- A PR title is untrusted text: a text node, never markup. One line; the chips ride after it. -->
                <span class="flex min-w-0 items-center gap-2">
                  <span class="min-w-0 truncate text-body text-ink">{p.title}</span>
                  {#if p.isDraft}<span class="shrink-0 rounded-md bg-sel px-1.5 text-[11px] leading-4 text-subtext">{copy.projects.meta.draft}</span>{/if}
                  {#if p.isFork}<span class="shrink-0 rounded-md bg-sel px-1.5 text-[11px] leading-4 text-subtext">{copy.projects.meta.fork}</span>{/if}
                </span>
                <span class="flex min-w-0 items-center gap-1.5 text-subhead font-normal text-subtext">
                  <span class="num shrink-0">#{p.number}</span>
                  {#if word(p)}
                    <span aria-hidden="true">·</span>
                    <span class="shrink-0 {statusTone(p.status)}">{word(p)}</span>
                  {/if}
                  {#if authorName(p)}
                    <span aria-hidden="true">·</span>
                    <span class="shrink-0">{authorName(p)}</span>
                  {/if}
                  {#if p.branch}
                    <span aria-hidden="true">·</span>
                    <span class="num min-w-0 truncate text-faint">{middleEllipsis(p.branch)}</span>
                  {/if}
                </span>
              </span>
              <Icon name="chevron-right" size={16} class="shrink-0 text-faint" />
            </span>
          </button>
        {/snippet}
      </VirtualList>
    {/if}
  </div>
</div>
