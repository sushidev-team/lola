<script lang="ts" module>
  import type { PRCheck, PRReadinessData } from "@bindings/internal/protocol";
  import { checkSummary } from "$lib/prchecks";
  import { copy, fill } from "@mobile/lib/copy";

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

  type Tally = { blocker?: number; major?: number; minor?: number; other?: number };

  /** The Agent review row's value: running, the findings of the current PR's reviews, or `fallback`. */
  export function reviewTally(s: { reviewRunning?: boolean; reviewTallies?: Tally[] | null } | undefined, fallback: string): string {
    if (s?.reviewRunning) return copy.session.agentReviewState.running;
    const tallies = s?.reviewTallies ?? [];
    if (!tallies.length) return fallback;
    const sum = (k: keyof Tally) => tallies.reduce((n, t) => n + (t[k] ?? 0), 0);
    const t = copy.session.tally;
    const parts = [
      sum("blocker") ? fill(sum("blocker") === 1 ? t.blocker : t.blockers, { n: sum("blocker") }) : "",
      sum("major") ? fill(t.major, { n: sum("major") }) : "",
      sum("minor") ? fill(t.minor, { n: sum("minor") }) : "",
      sum("other") ? fill(t.other, { n: sum("other") }) : "",
    ].filter((p) => p !== "");
    return parts.length ? parts.slice(0, 2).join(" · ") : copy.session.agentReviewState.clean;
  }

  /** The CI checks row's value: counts from the per-check list, else GitHub's one-word rollup. */
  export function ciSummary(list: PRCheck[] | null | undefined, rollup: string | undefined): string {
    const c = copy.session.checks;
    if (list) {
      const n = checkSummary(list);
      if (n.failed) return fill(c.summary, { failed: n.failed, passed: n.passed });
      if (n.running) return fill(c.running, { n: n.running });
      return n.total ? fill(c.allPassed, { n: n.total }) : c.none;
    }
    return ({ pass: copy.checks.passed, fail: copy.checks.failing, pending: copy.checks.running, none: c.none } as Record<string, string>)[rollup ?? ""] ?? "";
  }
</script>

<script lang="ts">
  import { listedFeedbackCount } from "@mobile/lib/feedbackcount";
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
  import { DaemonService } from "@bindings/desktop";
  import { diffRowTap } from "@mobile/lib/diffrowtap";
  import { refusalText } from "@mobile/lib/refusal";
  import { reviewChecks } from "@mobile/lib/reviewchecks.svelte";
  import { router } from "@mobile/lib/router.svelte";
  import { can } from "@mobile/lib/tiers.svelte";

  // The Review segment (spec §5.2): PR status, a Checks group (CI summary and the
  // agent's self-reported checks, both opening the checks sheet), a Review group
  // (agent review with its findings tally, review material, files changed, open on
  // GitHub), the diff, and a sticky bar with the two decisions.
  // The CI row reads BeforeMerge's readiness answer (`onfacts`), so the segment asks
  // GitHub once; the checks sheet reuses the same answer.
  // Merge opens the merge sheet, which owns readiness and the "anyway"
  // confirmation; feedback queues, never forces. Shared controls get `can`, so a
  // control this phone may not send is not drawn. ONE merge control lives here:
  // the sticky bar's (BeforeMerge gets `hideMerge`).

  let { id, packages = 0 }: { id: string; packages?: number } = $props();

  const session = $derived(store.sessionById(id));
  const drafts = $derived(listedFeedbackCount(id));
  // BeforeMerge reports a GitHub error through `onunknown`; Merge then says so instead of guessing.
  let unknown = $state(false);
  const block = $derived(mergeBlock(session, can("mergePR"), unknown));
  const prOpen = $derived(!!session && session.prNumber > 0 && session.delivery !== "merged" && session.delivery !== "closed");
  const offersAgentReview = $derived(can("review") && prOpen && session?.delivery !== "approved");
  const offersChanges = $derived(!!session && changesOffered(session));
  const stat = $derived(changes.of(id));
  const ciFailed = $derived(session?.delivery === "ci_failed");
  // The readiness answer BeforeMerge read for the PR on screen (a stack lane included).
  let facts = $state<{ target: string; number: number; data: PRReadinessData } | null>(null);
  const factsNow = $derived(facts && facts.target.split("~")[0] === id ? facts : null);
  const checkList = $derived(factsNow?.data.checkList);
  const local = $derived(session?.board?.checks ?? []);
  // A failing count from the PR's own check list, else from the agent's board; shown only when known.
  const failing = $derived(checkList ? checkSummary(checkList).failed : local.filter((c) => c.state === "fail").length);
  const ciLine = $derived(failing > 0 ? fill(copy.session.ciFailed, { n: failing }) : copy.session.ciTitle);
  const ciValue = $derived(ciSummary(checkList, factsNow?.data.checks ?? session?.checks));
  // CI red: the safe step (See checks) is primary; merging anyway is a quiet text action under it.
  const redCi = $derived(ciFailed && !block);
  const hasPr = $derived(!!session && session.prNumber > 0);
  // The Agent review row: running or the current PR's tally, else BeforeMerge's review value.
  const reviewFallback = $derived(
    ({ approved: "Approved", changes_requested: "Changes requested", review_pending: "Required" } as Record<string, string>)[session?.delivery ?? ""] ??
      copy.session.agentReviewState.notRun,
  );
  const reviewValue = $derived(reviewTally(session, reviewFallback));
  const showsAgentReview = $derived(prOpen && (offersAgentReview || !!session?.reviewRunning || (session?.reviewTallies?.length ?? 0) > 0));
  let reviewNotice = $state("");
  let starting = $state(false);
  const materialValue = $derived(
    packages === 0 ? copy.session.material.none : packages === 1 ? copy.session.material.one : fill(copy.session.material.many, { n: packages }),
  );

  $effect(() => {
    const sid = id;
    if (offersChanges) void changes.load(sid);
  });

  function openPR(): void {
    const url = session?.prUrl;
    if (url) store.openURL(url);
  }
  function onfacts(f: { target: string; number: number; data: PRReadinessData }): void {
    facts = f;
    reviewChecks.last = { session: id, target: f.target, number: f.number };
  }
  function openChecks(): void {
    router.open("checks");
  }
  // A phone review runs detached on the Mac: the answer says it started, and the row's
  // running flag and tally (pushed on the session row) carry the outcome.
  async function startReview(): Promise<void> {
    if (!offersAgentReview || starting || session?.reviewRunning) return;
    starting = true;
    reviewNotice = "";
    try {
      const r = await DaemonService.Review(id, "");
      reviewNotice = r?.started ? copy.session.reviewStarted.body : r?.message || copy.session.reviewStarted.body;
    } catch (e) {
      reviewNotice = refusalText(e);
    } finally {
      starting = false;
    }
  }
</script>

<div class="grid h-full min-h-0 min-w-0 grid-cols-[minmax(0,1fr)] grid-rows-[minmax(0,1fr)_auto]" data-review-segment data-review-list>
  <div class="review-scroll min-h-0 min-w-0 overflow-y-auto overscroll-y-contain pb-4" use:diffRowTap>
    {#if session}
      <div class="min-w-0 px-4">
        <BeforeMerge {session} reviewLayout {can} hideMerge heading={false} onunknown={(u) => (unknown = u)} {onfacts} />
      </div>
    {/if}
    {#if hasPr || local.length > 0}
      <div class="min-w-0" data-review-checks>
        <ListGroup header={copy.session.groups.checks}>
          {#if hasPr}
            <ListRow label={copy.session.checks.row} icon="list-checks" detail={ciValue || undefined} chevron onclick={openChecks} />
          {/if}
          {#if local.length > 0}
            <ListRow
              label={copy.session.checks.local}
              icon="circle-check"
              detail={fill(copy.session.checks.localValue, { p: local.filter((c) => c.state === "pass").length, n: local.length })}
              chevron
              onclick={openChecks}
            />
          {/if}
        </ListGroup>
      </div>
    {/if}
    {#if showsAgentReview || prOpen || packages > 0 || offersChanges || (session && session.prNumber > 0 && session.prUrl)}
      <div class="min-w-0" data-review-actions>
        <ListGroup header={copy.session.groups.review} footer={reviewNotice || undefined}>
          {#if showsAgentReview}
            <ListRow
              label={copy.session.agentReview}
              icon="shield-check"
              detail={reviewValue}
              chevron={offersAgentReview && !session?.reviewRunning}
              disabled={starting}
              onclick={offersAgentReview && !session?.reviewRunning ? startReview : undefined}
            />
          {/if}
          {#if prOpen || packages > 0}
            <ListRow label={copy.session.material.row} icon="package" detail={materialValue} chevron onclick={() => router.open("reviewPackage")} />
          {/if}
          {#if offersChanges}
            <ListRow
              label={copy.session.files}
              icon="file-diff"
              detail={stat && stat.files > 0 ? `+${stat.add} −${stat.del}` : undefined}
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
    <div class="flex flex-wrap items-center gap-2 px-4">
      {#if drafts > 0}
        <TouchButton variant="secondary" class="flex-1 justify-center!" onclick={() => router.open("feedback")}>
          {fill(copy.session.reviewBar.feedback, { n: drafts })}
        </TouchButton>
      {/if}
      {#if redCi}
        <button
          type="button"
          class="inline-flex min-h-11 items-center justify-center rounded-lg px-2 text-subhead text-subtext"
          data-merge-anyway
          onclick={() => router.open("merge")}
        >
          {copy.session.reviewBar.mergeAnyway}
        </button>
        <TouchButton variant="primary" class="flex-1 justify-center!" onclick={openChecks}>
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
  </div>
</div>

<style>
  .review-scroll { touch-action:pan-y pinch-zoom; overscroll-behavior-x:none; }
  .review-bar { background:var(--color-canvas); }
  [data-review-segment] :global(.diff-row) {
    min-height: 28px;
    line-height: 28px;
  }
</style>
