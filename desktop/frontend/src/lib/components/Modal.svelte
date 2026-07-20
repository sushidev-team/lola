<script lang="ts">
  import type { Snippet } from "svelte";
  import { onMount, onDestroy } from "svelte";
  let {
    title,
    onClose,
    width = "560px",
    children,
    footer,
  }: {
    title: string;
    onClose: () => void;
    width?: string;
    children: Snippet;
    footer?: Snippet;
  } = $props();

  let dialog: HTMLDivElement;
  // Remember what was focused before the dialog opened so focus can return
  // there when it closes — the standard modal contract for keyboard users.
  let prevFocus: HTMLElement | null = null;

  function focusables(): HTMLElement[] {
    return Array.from(
      dialog.querySelectorAll<HTMLElement>(
        'a[href],button:not([disabled]),input:not([disabled]),textarea:not([disabled]),select:not([disabled]),[tabindex]:not([tabindex="-1"])',
      ),
    );
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
  // while it is open. Escape / backdrop close stay on the outer element below.
  function onKeydown(e: KeyboardEvent) {
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

<div
  class="fixed inset-0 z-40 flex items-center justify-center bg-black/45 backdrop-blur-[2px]"
  onclick={(e) => e.target === e.currentTarget && onClose()}
  onkeydown={(e) => e.key === "Escape" && onClose()}
  role="presentation"
>
  <div
    bind:this={dialog}
    class="flex max-h-[84vh] w-full flex-col overflow-hidden rounded-xl border border-edge bg-panel shadow-2xl"
    style="max-width:{width}"
    role="dialog"
    aria-modal="true"
    aria-label={title}
    tabindex="-1"
    onkeydown={onKeydown}
  >
    <header class="flex items-center border-b border-edge/70 px-4 py-2.5">
      <h2 class="text-sm font-semibold text-accent-ink">{title}</h2>
      <button class="ml-auto text-faint hover:text-ink" onclick={onClose} aria-label="close">✕</button>
    </header>
    <div class="min-h-0 flex-1 overflow-auto p-4">
      {@render children()}
    </div>
    {#if footer}
      <footer class="border-t border-edge/70 px-4 py-2.5">{@render footer()}</footer>
    {/if}
  </div>
</div>
