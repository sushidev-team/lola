<script lang="ts">
  import Button from "./Button.svelte";
  let { label, summary = "", detail = "" }: { label: string; summary?: string; detail?: string } = $props();
  let open = $state(false);
  let trigger: HTMLButtonElement;
  const id = $props.id();

  // Mount above the modal's scrolling columns, so neither can clip the help.
  // Focus stays on the trigger: this popover contains text, not controls.
  function floating(node: HTMLElement) {
    document.body.appendChild(node);
    const anchor = trigger.getBoundingClientRect();
    const bounds = node.getBoundingClientRect();
    const margin = 12;
    const gap = 6;
    const left = Math.max(margin, Math.min(anchor.left, window.innerWidth - bounds.width - margin));
    const below = anchor.bottom + gap;
    const top = below + bounds.height <= window.innerHeight - margin
      ? below : Math.max(margin, anchor.top - bounds.height - gap);
    node.style.left = `${left}px`;
    node.style.top = `${top}px`;

    function outside(event: PointerEvent) {
      if (event.target instanceof Node && !node.contains(event.target) && !trigger.contains(event.target)) open = false;
    }
    function escape(event: KeyboardEvent) {
      if (event.key !== "Escape") return;
      event.preventDefault();
      event.stopPropagation();
      open = false;
      trigger.focus();
    }
    function scroll(event: Event) {
      // Scrolling the help itself is allowed; moving its anchor dismisses it.
      if (event.target instanceof Node && node.contains(event.target)) return;
      open = false;
    }
    const resize = () => { open = false; };
    document.addEventListener("pointerdown", outside, true);
    document.addEventListener("keydown", escape, true);
    document.addEventListener("scroll", scroll, true);
    window.addEventListener("resize", resize);
    return {
      destroy() {
        document.removeEventListener("pointerdown", outside, true);
        document.removeEventListener("keydown", escape, true);
        document.removeEventListener("scroll", scroll, true);
        window.removeEventListener("resize", resize);
        node.remove();
      },
    };
  }
</script>

<span class="inline-flex items-center gap-1 text-sm font-normal normal-case tracking-normal text-faint">
  {#if summary}<span>{summary}</span>{/if}
  {#if detail}
    <Button size="xs" icon aria-label={`More about ${label}`} aria-expanded={open}
      aria-controls={open ? id : undefined} aria-describedby={open ? id : undefined}
      onclick={(e) => {
        e.preventDefault(); e.stopPropagation();
        trigger = e.currentTarget as HTMLButtonElement;
        open = !open;
      }}>
      <svg class="size-3.5" viewBox="0 0 16 16" fill="none" stroke="currentColor" aria-hidden="true">
        <circle cx="8" cy="8" r="6" /><path d="M8 7v4" /><circle cx="8" cy="4.5" r=".5" fill="currentColor" />
      </svg>
    </Button>
  {/if}
</span>
{#if open}
  <div use:floating {id} role="note" aria-label={label}
    class="fixed z-50 max-h-[calc(100vh-24px)] w-[min(20rem,calc(100vw-24px))] overflow-auto overscroll-contain rounded-lg border border-edge bg-panel p-3 text-sm font-normal normal-case leading-relaxed tracking-normal text-ink shadow-xl">
    {detail}
  </div>
{/if}
