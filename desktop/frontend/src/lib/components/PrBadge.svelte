<script lang="ts">
  import { deliveryGlyph, deliveryLabel, deliveryText } from "$lib/theme";
  import Button from "./Button.svelte";
  import type { SessionInfo } from "$lib/store.svelte";

  // THE PR IS THE SECONDARY AXIS, and this is all of it. The status pill beside
  // it carries the AGENT axis now, so nothing here is implied by anything there
  // — before the split the two competed for one word and the delivery state won
  // post-PR, which is how "the agent is still typing" became invisible.
  //
  // Two shapes, chosen by which props a call site has:
  //
  //   delivery given  → the real chip: #123 plus the delivery word. The desktop
  //                     surfaces pass it.
  //   delivery absent → the legacy check/review glyph pair, suppressed where the
  //                     `status` on the same row already states the fact. That
  //                     is the mobile companion's shape (it prints the rolled-up
  //                     status word itself), and it is unchanged.
  let {
    session,
    status = "",
    delivery = "",
    onOpen = undefined,
  }: {
    session: Pick<SessionInfo, "prNumber" | "prUrl" | "checks" | "review">;
    /** Legacy rolled-up status, used only to suppress a glyph it already states. */
    status?: string;
    /** The delivery axis. When set, the chip states it in words. */
    delivery?: string;
    /**
     * Makes the PR NUMBER a control that opens the pull request. Opt-in per call
     * site, and the reason is HTML rather than taste: a kanban card and a phone
     * row are themselves <button>s, and a nested button is not parseable — the
     * parser closes the outer one and takes the card's click with it. So only a
     * call site that is NOT inside a button passes this.
     */
    onOpen?: (() => void) | undefined;
  } = $props();

  const chip = $derived(deliveryLabel(delivery));

  // Guarded on the URL as well as the handler: the daemon ships prUrl alongside
  // prNumber, but a record restored from an older snapshot can carry the number
  // with no link, and a control that opens nothing is worse than a plain number.
  const openable = $derived(!!onOpen && session.prNumber > 0 && !!session.prUrl);

  const checksImplied = $derived(
    (status === "ci_failed" && session.checks === "fail") ||
      (status === "ci_pending" && session.checks === "pending"),
  );
  const reviewImplied = $derived(
    (status === "approved" && session.review === "APPROVED") ||
      (status === "changes_requested" && session.review === "CHANGES_REQUESTED"),
  );

  // stopPropagation for the same reason StatusPill's action does: this badge
  // sits inside rows and tiles whose own click selects or opens the session.
  function open(e: MouseEvent) {
    e.stopPropagation();
    onOpen?.();
  }
</script>

{#if session.prNumber > 0}
  <!-- `num`: the PR number sits in a table column and must not reflow its
       neighbours when a wider one arrives on the next observer push. -->
  <span class="num inline-flex items-center gap-1 align-middle whitespace-nowrap text-sm">
    {#if openable}
      <!-- The PR number was rendered on four surfaces and was a control on none
           of them: opening a PR needed the detail panel, the context menu, or
           knowing that `o` is bound. It is the most-pointed-at string in the app,
           so it is now the control it always looked like.
           A Button (see the Button invariant), overridden only for the badge's
           own geometry — it must sit on the text baseline of a table cell, not
           occupy the ladder's 24px row. -->
      <Button
        variant="bare"
        size="xs"
        title="open pull request #{session.prNumber}"
        class="num h-auto! rounded! px-0! py-0! text-magenta! underline-offset-2 enabled:hover:underline"
        onclick={open}
      >
        #{session.prNumber}
      </Button>
    {:else}
      <span class="text-magenta">#{session.prNumber}</span>
    {/if}
    {#if chip}
      <span class={deliveryText(delivery)}>
        <span aria-hidden="true">{deliveryGlyph(delivery)}</span>
        {chip}
      </span>
    {:else}
      {#if !checksImplied}
        {#if session.checks === "pass"}<span class="text-good" title="checks pass">✓</span>
        {:else if session.checks === "fail"}<span class="text-bad" title="checks failed">✕ci</span>
        {:else if session.checks === "pending"}<span class="text-warn" title="checks running">⧗</span>{/if}
      {/if}
      {#if !reviewImplied}
        {#if session.review === "APPROVED"}<span class="text-good" title="approved">✓rev</span>
        {:else if session.review === "CHANGES_REQUESTED"}<span class="text-bad" title="changes requested">✕rev</span>{/if}
      {/if}
    {/if}
  </span>
{:else}
  <span class="text-sm text-faint">—</span>
{/if}
