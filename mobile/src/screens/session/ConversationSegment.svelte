<script lang="ts">
  import { onDestroy, untrack } from "svelte";
  import { imagePicker, setImagePicker, type ImagePicker } from "$lib/composerattach.svelte";
  import { nav as deskNav } from "$lib/nav.svelte";
  import { store } from "$lib/store.svelte";
  import { ssh } from "$lib/ssh.svelte";
  import { agentName } from "$lib/ssh";
  import { feed } from "$lib/feed.svelte";
  import { talk } from "$lib/talk.svelte";
  import { terms } from "$lib/terms.svelte";
  import EmptyState from "@mobile/lib/components/EmptyState.svelte";
  import TouchButton from "@mobile/lib/components/TouchButton.svelte";
  import { copy, fill } from "@mobile/lib/copy";
  import { offeredSegments } from "./segments";
  import Workspace from "$lib/components/session/Workspace.svelte";
  import { markConversationSeen } from "@mobile/lib/desknav";
  import { router } from "@mobile/lib/router.svelte";
  import { requestImage } from "../../sheets/attach.svelte";

  // The Conversation segment: the shared Workspace in the column the Session
  // screen gives it. The composer's attach button exists only because this
  // installs a picker, and the picker is the `attach` sheet.
  //
  // Invariants: the picker is installed before the Workspace renders and removed
  // with the segment (only if it is still this segment's), so no other screen's
  // composer offers an attach it cannot serve; a session is marked seen only
  // while this segment is on screen and its agent state moves.

  let { id }: { id: string } = $props();

  const picker: ImagePicker = () => requestImage();
  setImagePicker(picker);
  onDestroy(() => {
    if (imagePicker() === picker) setImagePicker(null);
  });

  const stateSince = $derived(store.sessionById(id)?.agentStateSince);
  $effect(() => {
    void stateSince;
    const route = router.top;
    // markSeen reads and writes `nav.seen`; tracking it would re-run this effect forever.
    untrack(() => markConversationSeen(route, deskNav));
  });

  // The desktop's "Terminal" buttons all end in `terms.focusTerminal(id)`. A phone has no terminal
  // cell on this segment, so a rise in that count moves the screen to the Terminal segment, when the
  // session offers one. The count at mount is the baseline: an old ask never navigates.
  const baseline = untrack(() => terms.terminalFocusAsks(id));
  let asked = baseline;
  $effect(() => {
    const n = terms.terminalFocusAsks(id);
    if (n <= asked) return;
    asked = n;
    untrack(() => {
      if (offeredSegments(store.sessionById(id), 0).includes("terminal")) router.replace({ name: "session", id, seg: "terminal" });
    });
  });

  // First paint says so with three bars; a loaded story with nothing in it says why.
  const view = $derived(ssh.view(id));
  const f = $derived(feed.view(id));
  const loading = $derived(!view.loaded && !f.loaded);
  const empty = $derived(view.loaded && f.loaded && f.items.length === 0 && view.items.length === 0 && !talk.state(id)?.prompt);
  const session = $derived(store.sessionById(id));
  const working = $derived(session?.agentState === "working");
  // CI red is invisible on this tab otherwise: a phone-owned card above the Workspace. A count is
  // shown only when the wire names the failing checks (as the Review segment does).
  const ciFailed = $derived(session?.delivery === "ci_failed");
  const failing = $derived((session?.board?.checks ?? []).filter((c) => c.state === "fail").length);
  const emptyBody = $derived(working ? fill(copy.session.empty.agentWorking, { agent: agentName(session?.agent) }) : undefined);
  let root = $state<HTMLDivElement | null>(null);
  // The composer sits under the story, so the empty state centres in what is above it.
  let below = $state(0);
  $effect(() => {
    const el = root?.querySelector<HTMLElement>(".cv-composer");
    if (!el || (!loading && !empty)) return;
    const measure = () => (below = el.getBoundingClientRect().height);
    measure();
    if (typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  });
</script>

<div class="grid h-full min-h-0 {ciFailed ? 'grid-rows-[auto_minmax(0,1fr)]' : 'grid-rows-[minmax(0,1fr)]'}" data-conversation-segment>
  {#if ciFailed}
    <div class="mx-4 mt-2 flex min-h-11 items-center gap-3 rounded-[14px] border bg-panel px-4 py-2" style="border-color: color-mix(in srgb, var(--color-bad) 40%, transparent)" data-ci-card role="status">
      <div class="grid min-w-0 flex-1 gap-0.5">
        <p class="text-headline text-bad">{copy.session.ciTitle}</p>
        {#if failing > 0}<p class="text-subhead font-normal text-faint" data-ci-count>{fill(copy.session.ciFailed, { n: failing })}</p>{/if}
      </div>
      <TouchButton variant="secondary" class="shrink-0" onclick={() => router.replace({ name: "session", id, seg: "review" })}>{copy.session.reviewBar.seeChecks}</TouchButton>
    </div>
  {/if}
  <div class="relative h-full min-h-0" bind:this={root}>
    <Workspace sessionId={id} />
    {#if loading}
      <div class="pointer-events-none absolute inset-x-0 top-0 grid content-start gap-3 px-4 pt-6" style:bottom="{below}px" aria-hidden="true" data-skeleton>
        {#each [60, 80, 40] as w (w)}<div class="h-3 rounded-md bg-edge-soft motion-safe:animate-pulse" style:width="{w}%"></div>{/each}
      </div>
    {:else if empty}
      <div class="pointer-events-none absolute inset-x-0 top-0 bg-canvas" style:bottom="{below}px" data-empty-story>
        <EmptyState size="screen" icon="message-square" title={copy.session.empty.title} body={emptyBody} />
      </div>
    {/if}
  </div>
</div>

<style>
  /* Phone-only rules for the shared Workspace. Everything is scoped under the Session screen's
     root, `[data-phone-desk]`, so the desktop renders exactly as before. The desktop markup helps
     with three marks: `data-desk-only` (drawn on the desktop only), `data-phone-only` (a `hidden`
     element drawn here) and `data-chord` (a keyboard hint, hidden by the foundation rule). */
  :global([data-phone-desk] [data-desk-only]) { display: none !important; }
  :global([data-phone-desk] span[data-phone-only]) { display: inline !important; }
  :global([data-phone-desk] button[data-phone-only]) { display: inline-flex !important; }
  
  /* --- Reading: rem, so Dynamic Type works ------------------------------------------------- */
  :global([data-phone-desk] [data-conversation]) {
    --text-prose: 1rem;
    --text-prose--line-height: 1.5rem;
  }
  :global([data-phone-desk] [data-conversation] .cv-band) { padding-inline: 16px; }
  :global([data-phone-desk] [data-conversation] [data-voice="agent"][data-role="narration"]),
  :global([data-phone-desk] [data-conversation] [data-voice="agent"] [data-rich-heading]),
  :global([data-phone-desk] [data-conversation] .cv-result [data-rich-lead]),
  :global([data-phone-desk] [data-conversation] .cv-result [data-role="answer"] > [data-rich] > p:first-child:not(:last-child)) {
    font-size: 1rem;
    line-height: 1.5rem;
  }
  /* Tool rows and anything mono. */
  :global([data-phone-desk] [data-conversation] .cv-tool-preview),
  :global([data-phone-desk] [data-conversation] .cv-activity-counts),
  :global([data-phone-desk] [data-conversation] .cv-activity-chip strong),
  :global([data-phone-desk] [data-conversation] .cv-activity-failures),
  :global([data-phone-desk] [data-conversation] .cv-answer-section > summary),
  :global([data-phone-desk] [data-conversation] [data-run]) {
    font-size: 0.8125rem;
    line-height: 1.125rem;
  }
  :global([data-phone-desk] [data-conversation] .cv-system-message),
  :global([data-phone-desk] [data-conversation] .cv-background-event),
  :global([data-phone-desk] [data-conversation] [data-role="answer"] .cv-ledger dt) {
    font-size: 0.75rem;
    line-height: 1.125rem;
  }
  :global([data-phone-desk] [data-conversation] .cv-system-source),
  :global([data-phone-desk] [data-conversation] .cv-receipt-meta),
  :global([data-phone-desk] [data-conversation] .cv-receipt-summary > time),
  :global([data-phone-desk] [data-conversation] .cv-call-group-preview),
  :global([data-phone-desk] [data-conversation] .cv-call-group-meta),
  :global([data-phone-desk] [data-conversation] .cv-disclosure-action) {
    font-size: 0.6875rem;
  }

  /* --- One agent voice: no rail, no gutter, no stacked sheets ------------------------------ */
  :global([data-phone-desk] [data-conversation] .cv-stream::before),
  :global([data-phone-desk] [data-conversation] [data-rail-mark]),
  :global([data-phone-desk] [data-conversation] [data-rail-foot] > .absolute),
  :global([data-phone-desk] [data-conversation] .cv-stack-sheet) {
    display: none !important;
  }
  :global([data-phone-desk] [data-conversation] .cv-activity-log),
  :global([data-phone-desk] [data-conversation] .cv-work-detail) {
    border-left: 0;
    margin-left: 0;
    padding-left: 0;
  }

  /* Tool summary: one 44pt row, "1 command · 2 edits · 4 reads", no border. */
  :global([data-phone-desk] [data-conversation] .cv-activity-toggle) {
    min-height: 44px;
    margin-block: 4px 12px;
    padding: 0;
    border: 0;
    background: transparent;
    grid-template-columns: 16px auto 12px;
    justify-content: start;
    column-gap: 8px;
  }
  :global([data-phone-desk] [data-conversation] .cv-activity-icon) { display: block; width: 16px; height: 16px; color: var(--color-faint); }
  :global([data-phone-desk] [data-conversation] .cv-activity-chip svg) { display: none; }
  :global([data-phone-desk] [data-conversation] .cv-activity-counts) { gap: 0 6px; }
  :global([data-phone-desk] [data-conversation] .cv-activity-chip) { display: inline-flex; white-space: nowrap; color: var(--color-ink-2) !important; }
  /* The dot closes a chip, so a wrap never starts a line with one. */
  :global([data-phone-desk] [data-conversation] .cv-activity-chip:not(:last-child)::after) { content: "·"; margin-left: 6px; }
  :global([data-phone-desk] [data-conversation] .cv-activity > .cv-call-group-head) { width: 100%; }

  /* --- Transcript rhythm: one agent voice, 24pt between turns ------------------------------- */
  :global([data-phone-desk] [data-conversation] .cv-result) {
    padding: 0;
    border: 0;
    border-radius: 0;
    background: transparent;
    box-shadow: none;
  }
  :global([data-phone-desk] [data-conversation] .cv-result [data-rich]),
  :global([data-phone-desk] [data-conversation] [data-voice="agent"] [data-rich]) { color: var(--color-ink); }
  :global([data-phone-desk] [data-conversation] [data-turn]) { margin-top: 24px !important; }
  :global([data-phone-desk] [data-conversation] [data-turn]:first-child) { margin-top: 0 !important; }
  :global([data-phone-desk] [data-conversation] .cv-turn) { padding-block: 0; }
  :global([data-phone-desk] [data-conversation] .cv-stream) { padding-top: 8px; }

  /* The speech glyph on a narration beat is decoration, not an action: one voice, no marker. */
  :global([data-phone-desk] [data-conversation] .cv-note-icon) { display: none; }
  :global([data-phone-desk] [data-conversation] .cv-progress-note) { grid-template-columns: minmax(0, 1fr); }

  /* --- Your message: a right-aligned bubble; the source is plain meta text -------------------- */
  :global([data-phone-desk] [data-conversation] .cv-person) {
    display: flex;
    flex-direction: column;
    align-items: flex-end;
    width: 100%;
    max-width: 100%;
    margin-bottom: 0;
  }
  :global([data-phone-desk] [data-conversation] .cv-user-message) {
    max-width: 80%;
    min-width: 0;
    border: 0;
    border-radius: 18px;
    padding: 10px 14px;
    background: var(--color-sel);
    color: var(--color-ink) !important;
  }
  /* The desktop's subtext colour sits on inner elements; on the bubble the ink wins. */
  :global([data-phone-desk] [data-conversation] .cv-person .cv-user-message :where(p, span, div, li, a, strong, em)) { color: var(--color-ink) !important; }
  :global([data-phone-desk] [data-conversation] .cv-person .cv-speaker) {
    order: 2;
    margin: 4px 0 0;
    justify-content: flex-end;
    color: var(--color-faint);
    font-size: 0.75rem;
    line-height: 1rem;
  }
  :global([data-phone-desk] [data-conversation] .cv-person .cv-speaker strong),
  :global([data-phone-desk] [data-conversation] .cv-source-badge) { display: none; }
  :global([data-phone-desk] [data-conversation] .cv-person .cv-speaker time) { margin-left: 0; font-size: 0.75rem; }

  /* --- Chrome above the story: the map and the request band are not drawn here -------------- */
  :global([data-phone-desk] [data-conversation] .cv-activity-map),
  :global([data-phone-desk] [data-conversation] [data-turn-head]) { display: none; }

  /* --- Permission, question and choice cards: one inset grouped list -------------------------- */
  :global([data-phone-desk] [data-card] [data-phone-list] > * + *) {
    background-image: linear-gradient(var(--color-edge), var(--color-edge));
    background-size: calc(100% - 16px) 1px;
    background-position: right top;
    background-repeat: no-repeat;
  }
  :global([data-phone-desk] [data-card] [data-choice] kbd) { display: none !important; }

  /* --- Composer: [attach] [field] [send], status above, actions wrap below ------------------- */
  :global([data-phone-desk] [data-first-open]) { display: none !important; }
  /* Only the composer's own model chip is hidden; the Model sheet renders ModelSettings elsewhere. */
  :global([data-phone-desk] .cv-composer [data-model-settings]) { display: none !important; }
  :global([data-phone-desk] .cv-composer) {
    display: grid;
    grid-template-columns: 44px minmax(0, 1fr) 44px;
    align-items: end;
    gap: 8px;
    border: 0 !important;
    border-radius: 0;
    background: transparent !important;
    box-shadow: none;
    overflow: visible;
  }
  :global([data-phone-desk] .cv-composer > *) { grid-column: 1 / -1; order: 6; }
  :global([data-phone-desk] .cv-composer > [data-phone-status]) { order: 0; padding-inline: 4px; }
  :global([data-phone-desk] .cv-composer > [data-fill-chip]) { order: 1; }
  /* Attachments: a one-row scroller of 56pt tiles with a 16pt leading inset. */
  :global([data-phone-desk] .cv-composer > [data-attachments]) {
    order: 2;
    flex-wrap: nowrap;
    overflow-x: auto;
    scrollbar-width: none;
    gap: 8px;
    padding: 8px 16px 0;
  }
  :global([data-phone-desk] .cv-composer > [data-attachments]::-webkit-scrollbar) { display: none; }
  :global([data-phone-desk] .cv-composer > [data-composer-attach]) { order: 3; grid-column: 1; width: 44px !important; height: 44px !important; justify-content: center; color: var(--color-ink-2) !important; }
  :global([data-phone-desk] .cv-composer > .cv-composer-field) {
    order: 4;
    grid-column: 2;
    min-height: 44px;
    padding: 10px 14px;
    border-radius: 20px;
    background: var(--color-sel);
  }
  :global([data-phone-desk] .cv-composer > .absolute) { display: none; }
  :global([data-phone-desk] .cv-composer-actions) { display: contents; }
  :global([data-phone-desk] [data-composer-field] .composer-text) {
    font-size: 1rem !important;
    line-height: 1.5rem !important;
    min-height: 1.5rem;
  }
  /* Six lines at 1.5rem, scrolling inside; the mirror follows through the textarea's scroll event. */
  :global([data-phone-desk] [data-composer-field] textarea) { max-height: min(9rem, calc((100dvh - var(--kb, 0px) - 200px) / 2)) !important; scroll-padding-block: 0; }
  :global([data-phone-desk] .cv-composer-field textarea::placeholder) { color: var(--color-ink-2); opacity: 1; }

  :global([data-phone-desk] [data-composer-field] [data-field-mirror] > span.relative) { white-space: nowrap; }
  :global([data-phone-desk] [data-footer-left]),
  :global([data-phone-desk] [data-composer-actions] > div:nth-child(2)) {
    grid-column: 1 / -1;
    order: 7;
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
  }
  :global([data-phone-desk] [data-footer-left]:not(:has(button))),
  :global([data-phone-desk] [data-composer-actions] > div:nth-child(2):not(:has(button))),
  :global([data-phone-desk] [data-footer-attach]),
  :global([data-phone-desk] [data-footer-hint]) { display: none !important; }
  /* The checkbox Stop is gone: the round button stops. */
  :global([data-phone-desk] [data-footer-stop]:not([data-footer-primary])) { display: none !important; }
  :global([data-phone-desk] [data-composer-actions] button:not([data-footer-primary]):not([data-composer-attach])) {
    min-height: 44px !important;
    height: auto !important;
    padding-inline: 16px;
    border-radius: 12px;
  }
  :global([data-phone-desk] [data-composer-actions] button:not([data-footer-primary])::after) { content: none !important; }
  :global([data-phone-desk] [data-footer-primary]) {
    position: relative;
    order: 5;
    grid-column: 3;
    justify-self: center;
    align-self: center;
    width: 36px !important;
    min-width: 36px !important;
    height: 36px !important;
    padding: 0 !important;
    border-radius: 9999px !important;
    justify-content: center;
    background: var(--color-sel) !important;
    color: var(--color-ink-2) !important;
  }
  /* 36pt drawn, 44pt to hit. */
  :global([data-phone-desk] [data-footer-primary]::after) { content: "" !important; position: absolute; inset: -4px; }
  :global([data-phone-desk] [data-footer-primary]:is([data-footer-primary="send"], [data-footer-primary="queue"]):not(:disabled)) {
    background: var(--color-accent) !important;
    color: var(--color-on-accent) !important;
  }
  :global([data-phone-desk] [data-footer-primary="stop"]) { background: var(--color-sel) !important; color: var(--color-ink) !important; }
  :global([data-phone-desk] [data-footer-primary]:not([data-footer-primary="send"], [data-footer-primary="queue"], [data-footer-primary="stop"])) {
    grid-column: 1 / -1;
    justify-self: stretch;
    width: 100% !important;
    height: 44px !important;
    border-radius: 12px !important;
  }

  /* Attachment tiles: 56pt, the index inside the tile, the remove mark on the corner. */
  :global([data-phone-desk] [data-attachment]) { position: relative; flex: 0 0 56px; width: 56px; height: 56px; }
  :global([data-phone-desk] [data-attachment] > :is(img, span:not([data-attachment-badge]))) {
    width: 56px;
    height: 56px;
    border-radius: 10px;
  }
  :global([data-phone-desk] [data-attachment] > [data-attachment-state="loading"]) { border: 0; background: var(--color-edge-soft); }
  :global([data-phone-desk] [data-attachment] > [data-attachment-state="error"]) { overflow: visible; }
  :global([data-phone-desk] [data-attachment] > [data-attachment-badge]) {
    top: auto;
    right: auto;
    bottom: 4px;
    left: 4px;
    width: 18px;
    height: 18px;
    background: color-mix(in srgb, var(--color-crust) 80%, transparent) !important;
    color: var(--color-ink) !important;
    font-size: 0.75rem;
  }
  :global([data-phone-desk] [data-attachment] > [data-attachment-remove]) {
    display: grid !important;
    top: -6px;
    left: auto;
    right: -6px;
    z-index: 1;
    width: 22px;
    height: 22px;
    border: 0;
    background: color-mix(in srgb, var(--color-crust) 80%, transparent);
    color: var(--color-ink);
  }
  :global([data-phone-desk] [data-attachment] > [data-attachment-remove] svg) { width: 10px; height: 10px; }
  :global([data-phone-desk] [data-attachment] > [data-attachment-remove]::after) { content: ""; position: absolute; inset: -11px; }

  /* --- Queued messages: one rounded container, a header row, 44pt rows ----------------------- */
  /* The tray's left edge is the band's 16px, the feed text and the composer's edge. */
  /* --- Keyboard up: the field and Send come first ---------------------------------------------- */
  :global(html[data-kb] [data-phone-desk] [data-phone-footer-text]),
  :global(html[data-kb] [data-phone-desk] [data-phone-dot]),
  :global(html[data-kb] [data-phone-desk] [data-phone-status]:not(:has(button))),
  :global(html[data-kb] [data-phone-desk] [data-queue-phone] [data-queue-body]) { display: none !important; }

  /* --- The first-paint skeleton and the empty story ------------------------------------------ */
  :global([data-phone-desk] [data-empty-story] [data-empty] svg) { width: 28px; height: 28px; }
</style>
