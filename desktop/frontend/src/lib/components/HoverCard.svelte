<script lang="ts">
  // A styled hover/focus card — the replacement for a native `title` where the
  // content has STRUCTURE (rows, emphasis) that the OS tooltip flattens into
  // grey text. Shown after a short delay on pointer hover or keyboard focus,
  // hidden on leave, blur, Escape or any scroll that moves the anchor. Mounted
  // on <body> (like HelpText's popover) so no scrolling ancestor can clip it,
  // and placed below the anchor, flipping above when there is no room.
  import type { Snippet } from "svelte";

  let { children, card, class: cls = "" }: { children: Snippet; card: Snippet; class?: string } = $props();
  let open = $state(false);
  let anchor: HTMLSpanElement;
  let timer: ReturnType<typeof setTimeout> | undefined;
  const id = $props.id();

  const SHOW_DELAY = 250;

  function show() {
    clearTimeout(timer);
    timer = setTimeout(() => (open = true), SHOW_DELAY);
  }
  function hide() {
    clearTimeout(timer);
    open = false;
  }

  function floating(node: HTMLElement) {
    document.body.appendChild(node);
    const a = anchor.getBoundingClientRect();
    const b = node.getBoundingClientRect();
    const margin = 12;
    const gap = 6;
    const left = Math.max(margin, Math.min(a.left + a.width / 2 - b.width / 2, window.innerWidth - b.width - margin));
    const below = a.bottom + gap;
    const top = below + b.height <= window.innerHeight - margin ? below : Math.max(margin, a.top - b.height - gap);
    node.style.left = `${left}px`;
    node.style.top = `${top}px`;
    const escape = (e: KeyboardEvent) => e.key === "Escape" && hide();
    document.addEventListener("keydown", escape, true);
    document.addEventListener("scroll", hide, true);
    return {
      destroy() {
        document.removeEventListener("keydown", escape, true);
        document.removeEventListener("scroll", hide, true);
        node.remove();
      },
    };
  }
</script>

<!-- tabindex makes the card reachable by keyboard; the span itself has no
     action, so the hover handlers only reveal a description. -->
<!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_static_element_interactions -->
<span
  bind:this={anchor}
  class={cls}
  tabindex="0"
  aria-describedby={open ? id : undefined}
  onpointerenter={show}
  onpointerleave={hide}
  onfocusin={show}
  onfocusout={hide}
>
  {@render children()}
</span>
{#if open}
  <div
    use:floating
    {id}
    role="tooltip"
    class="pointer-events-none fixed z-50 w-max max-w-[min(22rem,calc(100vw-24px))] rounded-lg border border-edge bg-panel px-3 py-2.5 text-sm font-normal normal-case leading-snug tracking-normal text-ink shadow-xl"
  >
    {@render card()}
  </div>
{/if}
