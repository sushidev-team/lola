<script lang="ts">
  import type { Snippet } from "svelte";

  // A bottom sheet: the app's one modal shape.
  //
  // WHY THIS IS NOT THE DESKTOP'S `confirm` STORE. That store and its
  // ConfirmDialog live in the shared library, are keyboard-first, and centre a
  // card in a wide window. A phone modal rises from the bottom edge, because
  // that is the half of the screen a thumb reaches. The markup here is the
  // disconnect overlay Sessions.svelte carried inline, lifted verbatim so the
  // settings menu and the filter overlay cannot drift apart from it.
  //
  // THE BACKDROP IS A REAL BUTTON, not a div with an onclick. A tap-to-dismiss
  // target that is not in the accessibility tree is a dead end for anyone using
  // VoiceOver: the sheet would be dismissible by sighted users only. It carries
  // an explicit name for the same reason.
  //
  // ESCAPE LIVES HERE, not at a call site. Only a hardware keyboard can produce
  // one — a Magic Keyboard on an iPad, the same case MobileTerminal's
  // shift+enter branch exists for — but a modal that traps such a keyboard is
  // worse than three lines of handler, and a modal that traps it in two of the
  // app's three sheets is worse still. The view-settings popover had this and
  // the filter and connection sheets did not; putting it in the one shape they
  // all mount means a fourth sheet cannot forget it.

  let {
    /** Names the dialog for assistive technology. Say what the sheet is FOR. */
    label,
    /** Names the backdrop's dismiss target. */
    dismissLabel = "Close",
    onclose,
    children,
  }: {
    label: string;
    dismissLabel?: string;
    onclose: () => void;
    children: Snippet;
  } = $props();
</script>

<svelte:window
  onkeydown={(e: KeyboardEvent) => {
    if (e.key === "Escape") onclose();
  }}
/>

<div class="fixed inset-0 z-50 flex flex-col justify-end bg-black/50 p-3">
  <button type="button" class="absolute inset-0" aria-label={dismissLabel} onclick={onclose}
  ></button>
  <!-- `max-h` plus a scroller because Dynamic Type is unbounded: at the largest
       accessibility size the filter sheet is taller than the screen, and a sheet
       that overflows the viewport hides its own dismiss control. -->
  <div
    class="panel relative flex max-h-[85vh] flex-col gap-3 overflow-y-auto overscroll-contain p-4"
    role="dialog"
    aria-modal="true"
    aria-label={label}
    style="margin-bottom: env(safe-area-inset-bottom, 0px)"
  >
    {@render children()}
  </div>
</div>
