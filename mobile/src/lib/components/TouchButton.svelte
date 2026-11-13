<script lang="ts" module>
  // The shared <Button> at a size a finger can hit.
  //
  // WHY A WRAPPER RATHER THAN A NEW SIZE. Button.svelte's ladder is 24 / 28 /
  // 32px against a mouse; Apple's minimum comfortable target is 44pt, so every
  // action in this app needs a fourth rung. The honest fix is one line in that
  // component's SIZES map — but this project does not edit desktop/**, because
  // the whole reuse bet is that the shared library compiles here unchanged. So
  // the rung is added from outside instead, and the cost is stated plainly: the
  // ladder is now defined in two files, and a future variant added there will
  // not know about this one. That is a trade a human should get to revisit; it
  // is written up in this builder's report rather than buried here.
  //
  // TRAILING `!` IS LOAD-BEARING, and it is the trap CLAUDE.md names explicitly.
  // A plain `h-11` has exactly the same specificity as the size map's `h-7`, so
  // which one wins is decided by Tailwind's order in the compiled sheet rather
  // than by the class attribute — which means it works until the sheet is
  // regenerated. Every geometry override below therefore carries `!`.
  //
  // Every class is a LITERAL, same rule as the component it wraps: Tailwind
  // scans source text, so a composed `h-${n}` compiles to nothing.

  /** 44pt: Apple's minimum comfortable target, and the app's default. */
  const TOUCH = "h-11! min-w-11! px-3! text-base!";
  /** Square, for a lone glyph. */
  const TOUCH_ICON = "h-11! w-11! px-0!";
  /** A full-width row (the connect screen's primary action, list rows). */
  const TOUCH_BLOCK = "h-12! w-full! justify-center! px-4! text-base!";
</script>

<script lang="ts">
  import type { Snippet } from "svelte";
  import type { HTMLButtonAttributes } from "svelte/elements";
  import Button from "$lib/components/Button.svelte";

  // Spelled out rather than imported from Button.svelte's module block. The
  // type is exported there and the import would work, but nothing in the shared
  // library does it, and a mobile file would be the first thing to break if that
  // export were ever narrowed. A local copy costs one line and a test would not
  // catch the difference either way.
  type ButtonVariant =
    | "ghost"
    | "accent"
    | "secondary"
    | "primary"
    | "danger"
    | "danger-solid"
    | "bare";

  let {
    variant = "ghost",
    icon = false,
    /** A full-width primary action rather than an inline one. */
    wide = false,
    selected = false,
    loading = false,
    class: klass = "",
    children,
    ...rest
  }: {
    variant?: ButtonVariant;
    icon?: boolean;
    wide?: boolean;
    selected?: boolean;
    loading?: boolean;
    class?: string;
    children: Snippet;
  } & HTMLButtonAttributes = $props();

  const size = $derived(wide ? TOUCH_BLOCK : icon ? TOUCH_ICON : TOUCH);
</script>

<!-- `touch-manipulation` removes Safari's 300ms double-tap-to-zoom delay on the
     control without disabling zoom for the page. Every tap in this app is a
     command, and a third of a second of nothing is how an app feels broken. -->
<Button {variant} {icon} {selected} {loading} {...rest} class="touch-manipulation {size} {klass}">
  {@render children()}
</Button>
