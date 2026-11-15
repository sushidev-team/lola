<script lang="ts" module>
  /** Quiet time after a `diff:<id>` event before the count is re-read. */
  export const CHANGES_DEBOUNCE_MS = 500;
</script>

<script lang="ts">
  import { untrack } from "svelte";
  import { changes } from "$lib/changes.svelte";
  import { pushSource } from "$lib/pushsource";
  import { store } from "$lib/store.svelte";
  import ChangesView from "$lib/components/session/ChangesView.svelte";
  import { feedback } from "$lib/feedback.svelte";
  import TouchButton from "@mobile/lib/components/TouchButton.svelte";
  import { fill, copy } from "@mobile/lib/copy";
  import { diffRowTap } from "@mobile/lib/diffrowtap";
  import { router } from "@mobile/lib/router.svelte";

  // The Changes segment (spec §5.2): the shared ChangesView, one column. While it
  // is on screen the `diff:<id>` topic is watched, and a moved revision re-reads
  // the totals once things have been quiet for CHANGES_DEBOUNCE_MS. Unmounting
  // unwatches, so a hidden segment costs nothing. A refused topic falls back to
  // the desktop's rule: re-read once, then each time the agent state moves.

  let { id }: { id: string } = $props();

  let refused = $state(false);
  $effect(() => {
    const sid = id;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const stop = pushSource()?.diff(
      sid,
      () => {
        clearTimeout(timer);
        timer = setTimeout(() => void changes.load(sid), CHANGES_DEBOUNCE_MS);
      },
      (e) => {
        if (e !== "unknown_session") refused = true;
      },
    );
    return () => {
      clearTimeout(timer);
      stop?.();
      refused = false;
    };
  });

  // The desktop's feedback bar is hidden (hideFeedbackBar); one full-width button
  // takes its place once there is something to send. The general note lives in
  // the feedback sheet.
  const drafts = $derived(feedback.count(id));

  // The "comment on a line" hint shows while there are no drafts, and is gone for good after the first one.
  const HINT_KEY = "lola.changes.draftHintDone";
  function hintDone(): boolean {
    try {
      return localStorage.getItem(HINT_KEY) === "1";
    } catch {
      return false;
    }
  }
  let dismissed = $state(hintDone());
  $effect(() => {
    if (drafts > 0 && !dismissed) {
      dismissed = true;
      try {
        localStorage.setItem(HINT_KEY, "1");
      } catch {
        /* a per-viewer convenience only */
      }
    }
  });

  const agentState = $derived(store.sessionById(id)?.agentState ?? "");
  $effect(() => {
    const sid = id;
    void agentState;
    if (refused) untrack(() => void changes.load(sid));
  });
</script>

<div class="grid h-full min-h-0 min-w-0 grid-cols-[minmax(0,1fr)] grid-rows-[auto_minmax(0,1fr)_auto]" data-changes-segment>
  {#if drafts === 0 && !dismissed}
    <p class="min-w-0 px-4 py-2 text-[13px] leading-[18px] text-faint" style="grid-row: 1" data-drafts-hint>{copy.session.reviewBar.noDrafts}</p>
  {/if}
  <div class="min-h-0 min-w-0" style="grid-row: 2" use:diffRowTap>
    <ChangesView sessionId={id} compact hideFeedbackBar />
  </div>
  {#if drafts > 0}
    <div class="changes-bar min-w-0 border-t-[0.5px] border-edge-soft px-4 pt-2 pb-[max(8px,calc(env(safe-area-inset-bottom,0px)+8px))]" style="grid-row: 3" data-changes-bar>
      <TouchButton variant="primary" prominent onclick={() => router.open("feedback")}>
        {fill(copy.session.reviewBar.feedback, { n: drafts })}
      </TouchButton>
    </div>
  {/if}
</div>

<style>
  .changes-bar {
    background: color-mix(in srgb, var(--color-canvas) 80%, transparent);
    backdrop-filter: blur(20px) saturate(180%);
    -webkit-backdrop-filter: blur(20px) saturate(180%);
  }
  [data-changes-segment] :global(.diff-row) {
    min-height: 28px;
    line-height: 28px;
  }
</style>
