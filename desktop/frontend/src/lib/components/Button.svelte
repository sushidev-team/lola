<script lang="ts" module>
  // The app's ONE button. Before it, every action was hand-rolled at its call
  // site: ~60 buttons across 23 files spending five different paddings, four
  // rounding radii, three ways of saying "primary" (`bg-accent`, `bg-accent-fill`,
  // `border-accent`) and two ways of saying "selected" — and most of them had no
  // background at all until you read the label, so a row of actions looked like a
  // row of faint words rather than a row of controls.
  //
  // The shape is Linear's: a ghost button is transparent at rest and grows a
  // `bg-sel` chip on hover. That is what makes it legible as a control without
  // permanently painting chrome into a dense flight deck. Everything louder than
  // that (primary, danger-solid) is a deliberate step up, and there are only two
  // of them.
  //
  // Every class is a LITERAL in the maps below, never composed from fragments:
  // Tailwind's scanner reads source text, so `` `bg-${x}` `` compiles to nothing.
  //
  // `enabled:hover:` rather than `hover:` throughout — CSS still matches :hover on
  // a disabled button, so a plain hover rule lights up a control that cannot be
  // clicked.
  export type ButtonVariant = "ghost" | "accent" | "secondary" | "primary" | "danger" | "danger-solid" | "bare";
  export type ButtonSize = "xs" | "sm" | "md";

  const BASE =
    "inline-flex shrink-0 items-center gap-1.5 rounded-md whitespace-nowrap transition-colors " +
    "disabled:cursor-not-allowed disabled:opacity-40";

  // Heights are the sidebar's density ladder: 24 / 28 / 32px against 12–13px type.
  // Nothing here sets a font size except `xs`, which steps down to the metadata
  // tier — every other size inherits the 13px base from `body` (see app.css).
  const SIZES: Record<ButtonSize, string> = {
    xs: "h-6 px-1.5 text-sm",
    sm: "h-7 px-2",
    md: "h-8 px-3",
  };

  // Icon-only: square, so a lone glyph is not a lopsided pill.
  const ICON_SIZES: Record<ButtonSize, string> = {
    xs: "h-6 w-6 justify-center px-0 text-sm",
    sm: "h-7 w-7 justify-center px-0",
    md: "h-8 w-8 justify-center px-0",
  };

  const VARIANTS: Record<ButtonVariant, string> = {
    // The default. Quiet until you reach for it.
    ghost: "text-faint enabled:hover:bg-sel enabled:hover:text-ink",
    // A ghost that must be noticed among its neighbours (revive on a dead
    // session). Colour is the ONLY thing separating it from `ghost` — never also
    // a weight or a size, per the type map's two-of-three rule.
    accent: "text-info enabled:hover:bg-sel enabled:hover:text-accent-ink",
    // An outlined action that must be findable without being loud.
    secondary: "border border-edge text-ink enabled:hover:border-accent enabled:hover:bg-sel",
    // ONE per surface: the accent chip, tinted into the canvas (never the raw
    // accent, which is a 3:1 decorative fill and cannot carry small text).
    primary: "bg-accent-fill font-medium text-accent-ink enabled:hover:bg-accent-fill-hover",
    // Destructive but reversible-looking: quiet at rest, red only on approach.
    danger: "text-faint enabled:hover:bg-bad/15 enabled:hover:text-bad",
    // The confirm side of a destructive dialog. `on-bad` is the measured text
    // colour for that fill, so it stays legible on the light flavors too.
    "danger-solid": "bg-bad font-medium text-on-bad enabled:hover:opacity-90",
    // No chip and no colour of its own — for a control that lives INSIDE another
    // painted one (the terminal tab's label and its "×"). The surrounding chip
    // owns the background and the text colour; a second chip here would read as a
    // button nested in a button, and its own hover would fight the parent's.
    // A call site adds only what it changes, e.g. `hover:text-bad` on the "×" —
    // no trailing `!` needed, because this variant sets nothing to override.
    bare: "text-inherit",
  };

  // Recolouring a variant (a status chip that is green/amber/red by health) needs
  // Tailwind's trailing-`!` form — `class="text-warn!"`. A plain `text-warn` is
  // the same specificity as the variant's `text-faint`, and which one wins is
  // decided by Tailwind's order in the compiled sheet, not by the class attribute.
  //
  // Selected/toggled (segmented controls, lens pickers, tab-ish rows). One
  // treatment for all of them — the app previously had `bg-accent`+`text-on-accent`
  // on two of them and `bg-accent-fill`+`text-accent-ink` on the others.
  const SELECTED = "bg-accent-fill font-medium text-accent-ink";
</script>

<script lang="ts">
  import type { Snippet } from "svelte";
  import type { HTMLButtonAttributes } from "svelte/elements";

  let {
    variant = "ghost",
    size = "sm",
    /** Toggle state — paints the selected chip and reports aria-pressed. */
    selected = false,
    /** Square icon-only button. Pair with an `aria-label`. */
    icon = false,
    /** Fill the parent's width and left-align (menu-ish / full-width rows). */
    block = false,
    class: klass = "",
    children,
    ...rest
  }: {
    variant?: ButtonVariant;
    size?: ButtonSize;
    selected?: boolean;
    icon?: boolean;
    block?: boolean;
    class?: string;
    children: Snippet;
  } & HTMLButtonAttributes = $props();

  const cls = $derived(
    [
      BASE,
      icon ? ICON_SIZES[size] : SIZES[size],
      selected ? SELECTED : VARIANTS[variant],
      block ? "w-full justify-start" : icon ? "" : "justify-center",
      klass,
    ]
      .filter(Boolean)
      .join(" "),
  );
</script>

<!-- type="button" by default: an unqualified <button> inside a <form> submits it,
     and several of these live in the config overlays. A caller can still pass
     type="submit" through {...rest}. -->
<button type="button" aria-pressed={selected ? true : undefined} {...rest} class={cls}>
  {@render children()}
</button>
