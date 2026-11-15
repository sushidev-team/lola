<script lang="ts" module>
  import { copy } from "@mobile/lib/copy";

  /** Why the sticky Merge button is off, or "" when it may open the merge sheet. */
  export function mergeBlock(
    s: { prNumber: number; delivery?: string } | undefined,
    allowed: boolean,
    unknown = false,
  ): string {
    if (!allowed) return copy.notices.noTier.body;
    if (!s || s.prNumber <= 0) return copy.merge.noPr.body;
    if (s.delivery === "merged") return copy.merge.merged.body;
    if (s.delivery === "closed") return copy.merge.closed.body;
    // GitHub could not be read: Merge cannot say what it would do.
    if (unknown) return copy.session.reviewBar.unknown;
    return "";
  }
</script>

<script lang="ts">
  import { feedback } from "$lib/feedback.svelte";
  import { changes } from "$lib/changes.svelte";
  import { changesOffered } from "$lib/sessionview";
  import { store } from "$lib/store.svelte";
  import BeforeMerge from "$lib/components/session/BeforeMerge.svelte";
  import DiffView from "$lib/components/DiffView.svelte";
  import Icon from "$lib/components/Icon.svelte";
  import ListGroup from "@mobile/lib/components/ListGroup.svelte";
  import ListRow from "@mobile/lib/components/ListRow.svelte";
  import TouchButton from "@mobile/lib/components/TouchButton.svelte";
  import { fill } from "@mobile/lib/copy";
  import { diffRowTap } from "@mobile/lib/diffrowtap";
  import { router } from "@mobile/lib/router.svelte";
  import { can } from "@mobile/lib/tiers.svelte";

  // The Review segment (spec §5.2): PR status, one grouped list of actions (agent
  // review, packages, files changed, open on GitHub), the diff, and a sticky bar
  // with the two decisions.
  // Merge opens the merge sheet, which owns readiness and the "anyway"
  // confirmation; feedback queues, never forces. Shared controls get `can`, so a
  // control this phone may not send is not drawn. ONE merge control lives here:
  // the sticky bar's (BeforeMerge gets `hideMerge`).

  let { id, packages = 0 }: { id: string; packages?: number } = $props();

  const session = $derived(store.sessionById(id));
  const drafts = $derived(feedback.count(id));
  // BeforeMerge reports a GitHub error through `onunknown`; Merge then says so instead of guessing.
  let unknown = $state(false);
  const block = $derived(mergeBlock(session, can("mergePR"), unknown));
  const prOpen = $derived(!!session && session.prNumber > 0 && session.delivery !== "merged" && session.delivery !== "closed");
  const offersAgentReview = $derived(can("review") && prOpen && session?.delivery !== "approved");
  const offersChanges = $derived(!!session && changesOffered(session));
  const stat = $derived(changes.of(id));
  const ciFailed = $derived(session?.delivery === "ci_failed");
  // Local-board checks are the only per-check list on the wire; a count is shown only when it is known.
  const failing = $derived((session?.board?.checks ?? []).filter((c) => c.state === "fail").length);
  const ciLine = $derived(failing > 0 ? fill(copy.session.ciFailed, { n: failing }) : copy.session.ciTitle);
  // CI red: the safe step (See checks) is primary; merging anyway is a quiet text action under it.
  const redCi = $derived(ciFailed && !block);
  // The Agent review row shares BeforeMerge's review value (the phone hides BeforeMerge's own signal row).
  const reviewValue = $derived(
    ({ approved: "Approved", changes_requested: "Changes requested", review_pending: "Required" } as Record<string, string>)[session?.delivery ?? ""] ?? "Not run",
  );

  $effect(() => {
    const sid = id;
    if (offersChanges) void changes.load(sid);
  });

  function openPR(): void {
    const url = session?.prUrl;
    if (url) store.openURL(url);
  }
  function openChecks(): void {
    const url = session?.prUrl;
    if (url) store.openURL(`${url}/checks`);
  }
</script>

<div class="grid h-full min-h-0 min-w-0 grid-cols-[minmax(0,1fr)] grid-rows-[minmax(0,1fr)_auto]" data-review-segment>
  <div class="min-h-0 min-w-0 overflow-y-auto overscroll-contain" use:diffRowTap>
    {#if session}
      <div class="min-w-0 px-4">
        <BeforeMerge {session} reviewLayout {can} hideMerge heading={false} onunknown={(u) => (unknown = u)} />
      </div>
    {/if}
    {#if offersAgentReview || packages > 0 || offersChanges || (session && session.prNumber > 0 && session.prUrl)}
      <div class="min-w-0 pt-3" data-review-actions>
        <ListGroup>
          {#if offersAgentReview}
            <ListRow label={copy.session.agentReview} icon="shield-check" value={reviewValue} chevron onclick={() => store.review(id)} />
          {/if}
          {#if packages > 0}
            <ListRow label={fill(copy.session.packages, { n: packages })} icon="package" chevron onclick={() => router.open("reviewPackage")} />
          {/if}
          {#if offersChanges}
            <ListRow
              label={copy.session.files}
              icon="file-diff"
              value={stat && stat.files > 0 ? `+${stat.add} −${stat.del}` : undefined}
              chevron
              onclick={() => router.replace({ name: "session", id, seg: "changes" })}
            />
          {/if}
          {#if session && session.prNumber > 0 && session.prUrl}
            <ListRow label={fill(copy.session.openPr, { n: session.prNumber })} icon="git-pull-request" onclick={openPR}>
              {#snippet trailing()}
                <span class="shrink-0 text-faint" aria-hidden="true"><Icon name="external-link" size={14} /></span>
              {/snippet}
            </ListRow>
          {/if}
        </ListGroup>
      </div>
    {/if}
    {#if !offersChanges}
      <div class="min-w-0" class:hidden={stat?.files === 0}>
        <DiffView sessionId={id} compact hideFeedbackBar />
      </div>
    {/if}
  </div>
  <div class="review-bar min-w-0 border-t-[0.5px] border-edge-soft pt-2 pb-[max(8px,calc(env(safe-area-inset-bottom,0px)+8px))]" data-review-bar>
    {#if block}
      <p id="merge-block" class={block === copy.session.reviewBar.unknown ? "sr-only" : "px-4 pb-2 text-[13px] leading-[18px] text-faint"}>{block}</p>
    {/if}
    {#if redCi}
      <p class="px-4 pb-2 text-[13px] leading-[18px]" style="color: var(--color-bad)" data-ci-failed>{ciLine}</p>
    {/if}
    <div class="flex items-center gap-2 px-4">
      {#if drafts > 0}
        <TouchButton variant="secondary" class="flex-1 justify-center!" onclick={() => router.open("feedback")}>
          {fill(copy.session.reviewBar.feedback, { n: drafts })}
        </TouchButton>
      {/if}
      {#if redCi}
        <TouchButton variant="primary" class="flex-1 justify-center!" disabled={!session?.prUrl} onclick={openChecks}>
          {copy.session.reviewBar.seeChecks}
        </TouchButton>
      {:else}
        <TouchButton
          variant="primary"
          class="flex-1 justify-center!"
          disabled={block !== ""}
          aria-describedby={block ? "merge-block" : undefined}
          onclick={() => router.open("merge")}
        >
          {copy.session.reviewBar.merge}
        </TouchButton>
      {/if}
    </div>
    {#if redCi}
      <button
        type="button"
        class="mx-auto mt-1 flex h-11 items-center justify-center px-4 text-[15px] text-accent"
        data-merge-anyway
        onclick={() => router.open("merge")}
      >
        {copy.session.reviewBar.mergeAnyway}
      </button>
    {/if}
  </div>
</div>

<style>
  .review-bar {
    background: color-mix(in srgb, var(--color-canvas) 80%, transparent);
    backdrop-filter: blur(20px) saturate(180%);
    -webkit-backdrop-filter: blur(20px) saturate(180%);
  }
  [data-review-segment] :global(.diff-row) {
    min-height: 28px;
    line-height: 28px;
  }
</style>
