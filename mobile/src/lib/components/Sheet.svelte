<script lang="ts">
  import type { Snippet } from "svelte";
  import { Spring } from "svelte/motion";
  import { confirm } from "$lib/confirm.svelte";
  import { copy } from "@mobile/lib/copy";
  import { DETENT, MOTION, reducedMotion, type Detent } from "@mobile/lib/mobiletokens";

  // A bottom sheet: the app's one modal shape (spec §5.3, §6.2, §6.4).
  //
  // Invariants:
  //  - The backdrop is a real button, so VoiceOver can dismiss; it and Escape
  //    close at once. Only a drag releases through the exit spring, because the
  //    finger already carries the sheet part of the way.
  //  - Position is one number, the translateY in px: the spring presents the
  //    sheet, a drag sets it instantly, a release settles it at 0 or off screen.
  //  - Reduced motion removes the translation entirely and fades instead.
  //  - A destructive confirmation never lives in a sheet; it uses the shared
  //    confirm dialog, which sits above this at z-index 40.
  //  - The default detent is `fit`: the panel is as tall as its content and
  //    never taller than the screen minus the top inset and 44pt, so the content
  //    scrolls inside it. With a `title` the header (56pt: cancel | title | Done
  //    or action) sits under the handle and does not scroll.
  //  - The panel is pinned to the bottom edge and lifted by `--kb`, the soft
  //    keyboard's height past the home indicator, on the keyboard's own curve; its
  //    max-height subtracts the lift so the top never leaves the safe area.
  //
  //  - `action` without `cancel` draws a leading "Cancel" on its own, so a sheet
  //    that commits something can always be left. The grid stays `1fr auto 1fr`,
  //    which keeps the title centred.
  //  - `dirty` (the person typed something): EVERY dismissal (backdrop, Escape,
  //    Cancel, a drag released past the threshold) asks "Discard changes?" through
  //    the shared confirm instead of closing, and a dragged sheet springs back.
  //    `action.onclick` is the commit and is never gated.
  //  - `footer` is a non-scrolling bar pinned under the content (`data-sheet-footer`),
  //    a sibling of the scroller, for the sheet's primary actions (Mark reviewed,
  //    Restore, Pick, Merge). It carries the safe-area padding, so the content's own
  //    bottom padding drops to 8px while one is present.
  //
  // The sheet's content scrolls; the grab handle, header and footer do not, so the
  // drag never competes with a list inside.

  let {
    /** Names the dialog for assistive technology. Say what the sheet is FOR. Defaults to `title`. */
    label: labelProp,
    /** Names the backdrop's dismiss target. */
    dismissLabel = "Close",
    /** fit (default): as tall as the content; medium 52%, large 92%, full 100% minus the top safe area. */
    detent = "fit",
    /** A titled header: 56pt, under the handle. */
    title,
    /** Trailing text button that closes. "Done" by default when titled; `false` hides it. */
    done,
    /** Replaces Done: a bold accent button that dims when disabled. */
    action,
    /** Leading text button that closes. */
    cancel,
    /** Unsaved input: dismissing asks to discard first. */
    dirty = false,
    /** Section navigation between the title and the scrolling content. */
    navigation,
    /** A pinned bar under the scrolling content. */
    footer,
    onclose,
    children,
  }: {
    label?: string;
    dismissLabel?: string;
    detent?: Detent;
    title?: string;
    done?: string | false;
    action?: { label: string; onclick: () => void; disabled?: boolean };
    cancel?: string;
    dirty?: boolean;
    navigation?: Snippet;
    footer?: Snippet;
    onclose: () => void;
    children: Snippet;
  } = $props();

  const label = $derived(labelProp || title || "");
  const cancelLabel = $derived(cancel ?? (action ? "Cancel" : ""));
  const doneLabel = $derived(done === undefined ? (title !== undefined ? "Done" : false) : done);

  const MIN_SAMPLE_MS = 8;
  const reduced = reducedMotion();
  const offscreen = typeof window === "undefined" ? 1000 : window.innerHeight || 1000;

  const y = new Spring(reduced ? 0 : offscreen, MOTION.sheet);
  let panel = $state<HTMLElement>();
  let dragging = $state(false);
  let closing = false;
  let startY = 0;
  let lastY = 0;
  let lastT = 0;
  let velocity = 0;

  // The top stays below the status bar (or the banner that already paid for it),
  // with 44pt of backdrop to tap, and the keyboard lift comes off the same budget.
  const MAX =
    "calc(100% - var(--lola-top-inset, env(safe-area-inset-top, 0px)) - 44px - var(--kb, 0px))";
  const height = $derived(
    detent === "fit"
      ? "auto"
      : detent === "full"
        ? "calc(100% - var(--lola-top-inset, env(safe-area-inset-top, 0px)) - var(--kb, 0px))"
        : `${DETENT[detent] * 100}%`,
  );
  const maxHeight = $derived(detent === "full" ? "none" : MAX);

  $effect(() => {
    if (!reduced) y.target = 0;
  });

  // Focus moves into the dialog when it opens and back to what had it when it
  // closes, so VoiceOver and a hardware keyboard stay inside the modal.
  $effect(() => {
    const before = document.activeElement as HTMLElement | null;
    panel?.focus({ preventScroll: true });
    return () => before?.focus?.({ preventScroll: true });
  });

  const panelHeight = (): number => panel?.offsetHeight || offscreen;

  /** Every dismissal but a drag: close, or ask first when there is something to lose. */
  function requestClose(): void {
    if (!dirty) {
      onclose();
      return;
    }
    if (confirm.request) return;
    confirm.ask({
      title: copy.sheet.discard.title,
      body: copy.sheet.discard.body,
      confirmLabel: copy.sheet.discard.action,
      onConfirm: onclose,
    });
  }

  function dismissByDrag(): void {
    if (closing) return;
    if (dirty) {
      if (!reduced) y.target = 0;
      requestClose();
      return;
    }
    closing = true;
    if (reduced) {
      onclose();
      return;
    }
    void y.set(panelHeight() + 1).then(onclose);
  }

  function down(e: PointerEvent): void {
    if (closing) return;
    dragging = true;
    startY = lastY = e.clientY;
    lastT = e.timeStamp;
    velocity = 0;
    (e.currentTarget as HTMLElement).setPointerCapture?.(e.pointerId);
  }

  function move(e: PointerEvent): void {
    if (!dragging) return;
    // Samples closer than one frame say nothing about speed and would only add noise.
    const dt = e.timeStamp - lastT;
    if (dt >= MIN_SAMPLE_MS) {
      velocity = ((e.clientY - lastY) / dt) * 1000;
      lastY = e.clientY;
      lastT = e.timeStamp;
    }
    if (!reduced) void y.set(Math.max(0, e.clientY - startY), { instant: true });
  }

  function up(e: PointerEvent): void {
    if (!dragging) return;
    dragging = false;
    const dy = Math.max(0, e.clientY - startY);
    const far = dy > MOTION.dismissFraction * panelHeight();
    if (far || velocity > MOTION.dismissVelocity) dismissByDrag();
    else if (!reduced) y.target = 0;
  }
</script>

<svelte:window
  onkeydown={(e: KeyboardEvent) => {
    if (e.key === "Escape") requestClose();
  }}
/>

<div class="fixed inset-0 z-30">
  <button
    type="button"
    class="absolute inset-0 bg-black/40"
    class:sheet-fade={reduced}
    aria-label={dismissLabel}
    onclick={requestClose}
  ></button>
  <!-- Pinned to the bottom edge and lifted by `--kb`; its own background runs to
       the screen bottom, so no strip shows under it. Dynamic Type is unbounded:
       the content scrolls so a tall sheet never hides its own handle. -->
  <div
    bind:this={panel}
    class="panel absolute inset-x-0 flex flex-col rounded-t-card border-t border-edge-soft bg-panel shadow-sheet outline-none focus:outline-none focus-visible:outline-none"
    class:sheet-fade={reduced}
    role="dialog"
    aria-modal="true"
    tabindex="-1"
    aria-label={label}
    data-detent={detent}
    style="bottom: var(--kb, 0px); height: {height}; max-height: {maxHeight}; transform: translateY({reduced ? 0 : y.current}px)"
  >
    <div
      class="flex h-6 shrink-0 touch-none items-center justify-center"
      data-sheet-handle
      role="presentation"
      onpointerdown={down}
      onpointermove={move}
      onpointerup={up}
      onpointercancel={up}
    >
      <span class="h-1 w-9 rounded-full bg-edge" aria-hidden="true"></span>
    </div>
    {#if title !== undefined}
      <header class="grid h-14 shrink-0 grid-cols-[1fr_auto_1fr] items-center px-2" data-sheet-header>
        <div class="flex justify-start">
          {#if cancelLabel}
            <button
              type="button"
              class="min-h-11 min-w-11 touch-manipulation px-2 text-left text-callout text-accent active:opacity-60"
              onclick={requestClose}
            >
              {cancelLabel}
            </button>
          {/if}
        </div>
        <h2 class="max-w-[60vw] truncate text-center text-headline text-ink">{title}</h2>
        <div class="flex justify-end">
          {#if action}
            <button
              type="button"
              class="min-h-11 min-w-11 touch-manipulation px-2 text-right text-callout font-semibold whitespace-nowrap text-accent active:opacity-60 disabled:opacity-40"
              disabled={action.disabled}
              onclick={action.onclick}
            >
              {action.label}
            </button>
          {:else if doneLabel}
            <button
              type="button"
              class="min-h-11 min-w-11 touch-manipulation px-2 text-right text-callout text-accent active:opacity-60"
              onclick={onclose}
            >
              {doneLabel}
            </button>
          {/if}
        </div>
      </header>
    {/if}
    {@render navigation?.()}
    <div
      class="flex min-h-0 flex-1 flex-col overflow-y-auto overscroll-contain"
      style="padding-bottom: {footer ? '8px' : 'max(16px, calc(env(safe-area-inset-bottom, 0px) + 8px))'}"
      data-sheet-content
    >
      {@render children()}
    </div>
    {#if footer}
      <div
        class="shrink-0 border-t border-edge-soft px-gutter pt-3"
        style="padding-bottom: max(16px, calc(env(safe-area-inset-bottom, 0px) + 8px))"
        data-sheet-footer
      >
        {@render footer()}
      </div>
    {/if}
  </div>
</div>

<style>
  /* The keyboard's own curve; nothing under reduced motion. */
  .panel {
    transition: bottom 250ms cubic-bezier(0.17, 0.59, 0.4, 0.77);
  }
  @media (prefers-reduced-motion: reduce) {
    .panel {
      transition: none;
    }
  }
  .sheet-fade {
    animation: sheet-fade 150ms ease-out;
  }
  @keyframes sheet-fade {
    from {
      opacity: 0;
    }
  }
</style>
