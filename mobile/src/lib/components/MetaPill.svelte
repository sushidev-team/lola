<script lang="ts" module>
  // The small mono pill: a PR badge on a card or a header, and a fact chip on
  // the detail screen's context card ("✓ CI pass", "waiting on a permission").
  //
  // ONE COMPONENT FOR BOTH because the design draws one shape for both — the
  // same radius, the same lopsided padding (7px in, 8px out, so the leading
  // glyph is not crowded against the corner while the trailing text keeps its
  // optical margin), the same 11px tabular run. What varies is the ground and
  // the foreground, and those are the two props. Splitting it into PrPill and
  // FactPill would give us two files that must be kept pixel-identical by hand.
  //
  // WHY NOT `$lib/components/PrBadge.svelte` for the PR case. It compiles here
  // — SessionRow renders it verbatim — but it is a different component doing a
  // different job: it decides WHAT to say about a PR (its number, the delivery
  // word, the checks and review glyphs, and which of those the row's status
  // already implies), and it draws that as bare text with no ground at all. The
  // design's badge is a filled chip carrying a branch glyph and a number, and
  // the colour comes from the CHECKS rather than from the delivery word. So the
  // shape is new and the decision stays with the caller, which is the same
  // split SessionRow made when it stopped mounting <AgentActivity>.

  /**
   * The foreground, and the ground, as LITERAL class strings — Tailwind scans
   * source text and a composed `text-${tone}` compiles to nothing (rule 4).
   */
  const TONES = {
    /** A pull request. The design's default for the badge. */
    magenta: "text-magenta",
    /** The same badge when the PR's checks are failing. */
    bad: "text-bad",
    /** A fact that is good news: "✓ CI pass". */
    good: "text-good",
    /** A fact that is merely true: an input reason, a retry count. */
    grey: "text-pill-grey-fg",
  } as const;

  const GROUNDS = {
    /** The selection ground: a badge on a card, a passing-checks fact. */
    sel: "bg-sel",
    /** The quiet ground, paired with `grey` for a fact nobody must act on. */
    grey: "bg-pill-grey",
  } as const;

  export type PillTone = keyof typeof TONES;
  export type PillGround = keyof typeof GROUNDS;
</script>

<script lang="ts">
  import type { Snippet } from "svelte";

  let {
    tone = "magenta",
    ground = "sel",
    /**
     * Makes the pill a control. Opt-in per call site for the HTML reason
     * PrBadge's own `onOpen` states: a hero card and a compact row are
     * themselves <button>s, and a nested button is not parseable — the parser
     * closes the outer one and takes the card's click with it. A call site
     * inside a row therefore passes nothing and the pill stays a <span>.
     */
    onclick = undefined,
    /**
     * The control's accessible name. Required in practice when `onclick` is
     * set: the visible text is "#341", which names nothing on its own.
     */
    ariaLabel = "",
    /** A leading glyph — the caller passes <BranchIcon /> for a PR badge. */
    leading = undefined,
    children,
    class: klass = "",
  }: {
    tone?: PillTone;
    ground?: PillGround;
    onclick?: (() => void) | undefined;
    ariaLabel?: string;
    leading?: Snippet | undefined;
    children: Snippet;
    /** Appended last; override one of the pill's own utilities with a `!`. */
    class?: string;
  } = $props();
</script>

<!-- `num` is tabular figures, and it is not decoration: a PR number and an age
     both change under an observer push, and a proportional "1" would reflow
     every neighbour on the line each time one arrived. `font-medium` is the
     brief's rule for every mono run at this size — 11px regular tabular reads
     as fine print beside 13px medium row text. -->
{#snippet pill()}
  <span
    class="num inline-flex shrink-0 items-center gap-1 rounded-md py-[3px] pr-2 pl-[7px]
           text-sm font-medium {GROUNDS[ground]} {TONES[tone]} {klass}"
  >
    {#if leading}{@render leading()}{/if}
    {@render children()}
  </span>
{/snippet}

{#if onclick}
  <!-- THE TARGET IS THE BUTTON; THE PILL IS ONLY WHAT YOU SEE. The chip itself
       is about 21 points tall, which is half of Apple's minimum, and inflating
       it to 44 would make the badge the tallest thing in a header drawn around
       a 15px title. So the button is transparent, carries the `tap` utility
       (44 in BOTH axes — a two-character pill misses the horizontal minimum as
       badly as the vertical one) and centres the pill inside it. The extra
       height is invisible padding that a finger can still hit.

       No stopPropagation, unlike PrBadge: this control can never be nested
       inside another button, because that markup does not parse (see `onclick`
       above), so there is no outer click to swallow. -->
  <button
    type="button"
    class="tap inline-flex touch-manipulation items-center justify-center active:opacity-70"
    aria-label={ariaLabel}
    {onclick}
  >
    {@render pill()}
  </button>
{:else}
  {@render pill()}
{/if}
