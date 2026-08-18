<script lang="ts">
  import { pillClasses, statusLabel } from "$lib/theme";
  import Button from "./Button.svelte";
  let {
    status,
    interpreted = "",
    onResolve = undefined,
    resolveBranch = "",
  }: {
    status: string;
    interpreted?: string;
    /**
     * Makes a `merge_conflict` pill ACTIONABLE: hovering (or focusing) it turns
     * the word "conflict" into "resolve", and clicking asks the session's coding
     * agent to merge the project's default branch and fix the conflicts
     * (cmd=resolveConflict). Omitted on read-only surfaces, and ignored for
     * every other status.
     */
    onResolve?: (() => void) | undefined;
    /** The project's default_branch, named in the tooltip so the promise is exact. */
    resolveBranch?: string;
  } = $props();
  // No `dim` opacity. Group opacity composites the pill — fill AND label as one
  // layer — over whatever is behind it, which pulls the two toward a common
  // colour and quietly undoes the AA the pill's measured tokens guarantee. The
  // grid's de-emphasis already comes from the muted tints the non-attention
  // statuses carry (urgent/broken stay solid and loud); fading the whole pill
  // only cost legibility on top of that.
  //
  // The agent-axis badge is GONE from here. It rendered two-letter glyphs
  // ("·wk", "·?!", "·en") ported from the TUI's agentBadge
  // (internal/tui/sessionview.go) — correct there, where every column is
  // precious, and unreadable in an app that has room for words. The axis is now
  // rendered by <AgentActivity> as a live pulse plus plain language on the row's
  // secondary line; this component is back to one job — the delivery state, as
  // one pill.

  const actionable = $derived(status === "merge_conflict" && !!onResolve);
  const branch = $derived(resolveBranch || "the default branch");
  const tip = $derived(
    `Resolve the conflicts: merges ${branch} (the project's default branch) into this branch — ` +
      `the session's coding agent does the work and pushes the merge.`,
  );

  let busy = $state(false);

  // stopPropagation, because the pill sits inside a row whose own click selects
  // the session: without it a click here would ALSO move the selection, and a
  // dblclick-adjacent misfire would open the terminal.
  async function resolve(e: MouseEvent) {
    e.stopPropagation();
    if (!onResolve || busy) return;
    busy = true;
    try {
      await onResolve();
    } finally {
      busy = false;
    }
  }
</script>

<span class="inline-flex items-center align-middle whitespace-nowrap">
  {#if actionable}
    <!-- A Button, not a hand-rolled control (see the Button invariant): the
         overrides are only the pill's own geometry, which sits below the ladder's
         smallest size — `h-auto!`/`rounded!` beat the base's h-6/rounded-md, and
         the pill's own fill classes supply the colour the `bare` variant leaves
         alone. -->
    <Button
      variant="bare"
      size="xs"
      loading={busy}
      title={tip}
      class="group h-auto! rounded! py-[1px]! {pillClasses(status)}"
      onclick={resolve}
    >
      <!-- Both labels share ONE grid cell, so the pill is as wide as the wider
           of the two and the row does not reflow when the word swaps under the
           cursor. No aria-label over them: `visibility:hidden` takes the resting
           word out of the accessibility tree too, so the button's name follows
           the same swap the eye sees, and `title` supplies the explanation. -->
      <span class="grid">
        <span class="col-start-1 row-start-1 group-hover:invisible group-focus-visible:invisible">
          {statusLabel(status)}
        </span>
        <span class="invisible col-start-1 row-start-1 group-hover:visible group-focus-visible:visible">
          resolve
        </span>
      </span>
    </Button>
  {:else}
    <span
      class="inline-flex items-center whitespace-nowrap rounded px-1.5 py-[1px] text-sm {pillClasses(
        status,
      )}"
    >
      {statusLabel(status)}
    </span>
  {/if}
  {#if interpreted}
    <!-- The [statusagent] interpreter DISAGREES with the deterministic axis:
         an untrusted approximation, "≈"-marked (statusPillFor in the TUI). -->
    <span class="ml-0.5 text-sm text-orange">≈{statusLabel(interpreted)}</span>
  {/if}
</span>
