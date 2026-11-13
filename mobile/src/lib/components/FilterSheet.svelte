<script lang="ts">
  import Sheet from "./Sheet.svelte";
  import TouchButton from "./TouchButton.svelte";
  import TriageChips from "./TriageChips.svelte";
  import type { SessionInfo } from "$lib/store.svelte";

  // The search field and the triage chips.
  //
  // THIS SHEET NOW HOLDS ONE CONTROL THE LIST ALSO DRAWS, and that is a
  // deliberate overlap rather than something nobody noticed. The comment here
  // used to argue that BOTH controls had moved off the screen because together
  // they took roughly a fifth of a phone's height above a list that on a normal
  // day holds three sessions. Half of that argument survived the redesign and
  // half of it did not: the SEARCH field is still the rare action and still
  // belongs behind a button, but the buckets became what the list is ARRANGED
  // by — every row now sits under a section heading naming its bucket — so
  // <FilterRail> puts them back on the screen permanently as a table of
  // contents. See its own header for that reasoning.
  //
  // THE CHIPS STAY HERE ANYWAY, for one reason: this sheet's backdrop covers
  // the rail. Somebody who opened it to type a search and then wants a
  // different bucket would otherwise have to dismiss it, tap a chip and reopen
  // — and the tally directly above ("2 of 7") is only honest if both halves of
  // the filter can be reached from where it is drawn. The two surfaces are
  // bound to the same `nav` fields, so they cannot disagree; what they cost is
  // that a test asking for a chip by name has to say WHICH surface it means
  // (Sessions.test.ts scopes those queries to this dialog). A human may
  // reasonably decide the sheet should become search-only; the brief is silent
  // on it, so the working control was kept.
  //
  // THE FILTER STAYS LIVE while the sheet is open. There is no Apply step and
  // no local draft: `triage` and `query` are bound straight through to nav, so
  // a chip tapped here filters the list underneath immediately. A draft would
  // mean the sheet could be dismissed into a state the list never showed, which
  // is exactly the confusion a two-control header already caused.

  let {
    /** nav.triage, bound through. "" means every bucket. */
    triage = $bindable(""),
    /** nav.query, bound through. */
    query = $bindable(""),
    /** Every session, for the per-chip counts. */
    sessions,
    /** How many rows survive the current filter, for the honest tally. */
    matched,
    onclose,
  }: {
    triage?: string;
    query?: string;
    sessions: SessionInfo[];
    matched: number;
    onclose: () => void;
  } = $props();

  const active = $derived(triage !== "" || query !== "");

  function clear() {
    triage = "";
    query = "";
  }
</script>

<Sheet label="Filters" {onclose}>
  <div class="flex items-baseline gap-2">
    <span class="text-ink">Filters</span>
    <!-- The tally is the sheet's half of the promise the header's dot makes: a
         filtered list must never be mistakable for a short one. -->
    <span class="num ml-auto text-sm text-faint">
      {matched} of {sessions.length}
    </span>
  </div>

  <!-- NO `text-base` HERE, and that is not an oversight. app.css pins every
       input to 16px inside `@layer base`, because iOS zooms the page when a
       field under 16px takes focus and the only way to refuse that is to
       disable pinch-zoom for the whole document — an accessibility regression
       traded for a cosmetic one. A `text-base` utility OUTRANKS a base layer
       (Tailwind emits utilities in `@layer utilities`, which wins), so writing
       the row size here silently put the zoom back on the one field in this app
       a person actually types into. -->
  <input
    class="w-full rounded border border-edge bg-canvas px-3 py-2.5 text-ink outline-none
           focus:border-accent placeholder:text-placeholder"
    type="search"
    inputmode="search"
    autocapitalize="none"
    autocorrect="off"
    spellcheck="false"
    aria-label="Search sessions"
    placeholder="Filter by issue, title or project"
    bind:value={query}
  />

  <!-- `-mx-4` lets the strip scroll edge to edge under the sheet's padding.
       Inset by the padding instead, the first and last chips would be clipped
       by it and the fade would start early, which reads as a rendering fault. -->
  <div class="-mx-4">
    <TriageChips bind:value={triage} {sessions} />
  </div>

  <!-- CLEARING IS ONE TAP FROM HERE, and deliberately not a second control in
       the header. A header button that appears only while a filter is active is
       the two-ambiguous-controls problem this screen was just cured of: it
       shifts the layout as you type and adds a second thing next to the filter
       button whose subject nobody can name. Disabled rather than hidden so the
       sheet does not reflow the moment the last filter goes. -->
  <TouchButton wide variant="secondary" disabled={!active} onclick={clear}>
    Clear filters
  </TouchButton>
  <TouchButton wide onclick={onclose}>Done</TouchButton>
</Sheet>
