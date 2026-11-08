<script lang="ts">
  import Icon from "$lib/components/Icon.svelte";
  import type { Snippet } from "svelte";
  import { onMount, onDestroy } from "svelte";
  import Button from "./Button.svelte";
  import HelpText from "./HelpText.svelte";
  let {
    title,
    titleHelp = "",
    onClose,
    width = "560px",
    bodyClass,
    children,
    footer,
    onkey,
    portal = false,
    fullscreen = false,
    describedBy,
  }: {
    title: string;
    titleHelp?: string;
    onClose: () => void;
    width?: string;
    bodyClass?: string;
    children: Snippet;
    footer?: Snippet;
    onkey?: (event: KeyboardEvent) => void;
    portal?: boolean;
    fullscreen?: boolean;
    describedBy?: string;
  } = $props();

  function placeOverlay(node: HTMLElement) {
    if (portal) document.body.appendChild(node);
    return { destroy() { if (portal) node.remove(); } };
  }
  let dialog: HTMLDivElement;
  // Remember what was focused before the dialog opened so focus can return
  // there when it closes — the standard modal contract for keyboard users.
  let prevFocus: HTMLElement | null = null;

  function focusables(): HTMLElement[] {
    return Array.from(
      dialog.querySelectorAll<HTMLElement>(
        'summary,a[href],button:not([disabled]),input:not([disabled]),textarea:not([disabled]),select:not([disabled]),[tabindex]:not([tabindex="-1"])',
      ),
    ).filter((el) => {
      let ancestor = el.parentElement;
      while (ancestor && ancestor !== dialog) {
        if (ancestor instanceof HTMLDetailsElement && !ancestor.open && ancestor.querySelector("summary") !== el) return false;
        ancestor = ancestor.parentElement;
      }
      return true;
    });
  }

  onMount(() => {
    prevFocus = document.activeElement as HTMLElement | null;
    // Move focus into the dialog on open (first control, else the dialog box).
    const els = focusables();
    (els[0] ?? dialog).focus();
  });

  onDestroy(() => {
    // The overlay unmounts on every close path (Escape, backdrop, ✕ button),
    // so restoring here covers them all without disturbing those handlers.
    prevFocus?.focus?.();
  });

  // Trap Tab within the dialog: wrap first↔last so focus can't escape the modal
  // while it is open. Backdrop close stays on the outer element below; Escape is
  // App.svelte's (see the note above the markup).
  function onKeydown(e: KeyboardEvent) {
    onkey?.(e);
    if (e.defaultPrevented) return;
    if (e.key !== "Tab") return;
    const els = focusables();
    if (els.length === 0) {
      e.preventDefault();
      dialog.focus();
      return;
    }
    const first = els[0];
    const last = els[els.length - 1];
    const active = document.activeElement;
    if (e.shiftKey && active === first) {
      e.preventDefault();
      last.focus();
    } else if (!e.shiftKey && active === last) {
      e.preventDefault();
      first.focus();
    }
  }
</script>

<!-- Escape is NOT handled here: App.svelte's global key handler owns it for every
     overlay, so routing it through onClose here as well would fire a guarded
     form's requestClose twice on one keypress — opening the discard prompt and
     then, on the second pass, cancelling it. Backdrop click still closes. -->
<!-- svelte-ignore a11y_click_events_have_key_events -->
<div
  use:placeOverlay
  class="fixed inset-0 z-40 flex items-center justify-center bg-black/45 backdrop-blur-[2px]"
  onclick={(e) => e.target === e.currentTarget && onClose()}
  role="presentation"
>
  <div
    bind:this={dialog}
    class="modal-shell w-full overflow-hidden border border-edge bg-panel shadow-2xl {fullscreen ? 'grid h-dvh max-h-dvh' : 'windowed flex max-h-[84vh] flex-col'}"
    style="max-width:{fullscreen ? '100vw' : width};{fullscreen ? `grid-template-rows:auto minmax(0,1fr)${footer ? ' auto' : ''}` : ''}"
    role="dialog"
    aria-modal="true"
    aria-label={title}
    aria-describedby={describedBy}
    tabindex="-1"
    onkeydown={onKeydown}
  >
    <header class="modal-header flex items-center border-b border-edge/70 py-2.5 {fullscreen ? 'drag pl-[82px] pr-4' : 'px-4'}">
      <!-- text-lg carries its own 600 + tracking: a dialog title is the one
           level that must out-rank everything inside the dialog. -->
      <h2 class="min-w-0 text-lg text-ink">{title}</h2>
      {#if titleHelp}<span class="ml-2 inline-flex"><HelpText label={title} detail={titleHelp} /></span>{/if}
      <Button icon class="ml-auto" onclick={onClose} aria-label="close"><Icon name="x" size={14} /></Button>
    </header>
    <div class="modal-body min-h-0 flex-1 {bodyClass ?? 'overflow-auto p-4'}" class:default-body={bodyClass === undefined}>
      {@render children()}
    </div>
    {#if footer}
      <footer class="modal-footer border-t border-edge/70 px-4 py-2.5">{@render footer()}</footer>
    {/if}
  </div>
</div>

<style>
  .windowed { width:calc(100% - 32px); border-radius:16px; border-color:color-mix(in srgb,var(--color-edge) 75%,transparent); box-shadow:0 24px 64px -16px rgb(0 0 0 / .4),0 8px 24px -12px rgb(0 0 0 / .25); animation:dialog-enter 160ms cubic-bezier(.16,1,.3,1); }
  .windowed .modal-header { padding:24px 24px 0; border:0; align-items:flex-start; gap:8px; }
  .windowed .modal-header h2 { font-size:18px; line-height:28px; letter-spacing:-.02em; overflow-wrap:anywhere; }
  .windowed .modal-body { margin-top:16px; }
  .windowed .modal-body.default-body { margin-top:0; padding:16px 24px 24px; }
  .windowed .modal-footer { padding:0 24px 24px; border:0; background:transparent; }
  .windowed .modal-footer > :global(.flex) { flex-wrap:wrap; }
  .windowed .modal-footer :global(button),.windowed :global(.dialog-actions button) { min-height:36px; height:auto; padding:8px 12px; border-radius:8px; white-space:normal; }
  .windowed :global(.dialog-actions) { display:flex; align-items:center; gap:8px; flex-wrap:wrap; }
  .windowed :global(.dialog-actions .cancel) { margin-right:auto; color:var(--color-ink-2); }
  .windowed :global(form > .dialog-actions) { margin-top:8px; }
  @keyframes dialog-enter { from { opacity:.85; transform:translateY(6px) scale(.985); } to { opacity:1; transform:none; } }
  @media(prefers-reduced-motion:reduce) { .windowed { animation:none; } }
  @media(max-width:480px) {
    .windowed .modal-header { padding:20px 20px 0; }
    .windowed .modal-body.default-body { padding:14px 20px 24px; }
    .windowed .modal-footer { padding:0 20px 20px; }
    .windowed :global(.dialog-actions) { display:grid; grid-template-columns:minmax(0,1fr); }
    .windowed :global(.dialog-actions button) { width:100%; min-height:40px; }
    .windowed :global(.dialog-actions .cancel) { margin:0; }
    .windowed :global(.dialog-actions > .flex) { flex-wrap:wrap; }
  }
</style>
