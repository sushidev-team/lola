<script lang="ts">
  import Sheet from "./Sheet.svelte";
  import TouchButton from "./TouchButton.svelte";
  import TriageChips from "./TriageChips.svelte";
  import type { SessionInfo } from "$lib/store.svelte";

  // The search field and the triage chips, moved off the list.
  //
  // WHY THEY MOVED. Together they took two full rows at the top of a phone
  // screen — roughly a fifth of the visible height — above a list that on a
  // normal day holds three sessions. Filtering is something you do occasionally;
  // reading the list is what the screen is for, and the controls for the rare
  // action were permanently crowding out the common one.
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

  <input
    class="w-full rounded border border-edge bg-canvas px-3 py-2.5 text-base text-ink outline-none
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
