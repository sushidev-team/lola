<script lang="ts" module>
  // The app's ONE <select>, for the same reason <Checkbox> exists: the native
  // menulist is drawn by AppKit, so its chevron (a stepper on older macOS, a
  // Liquid Glass caret on 26), its inner padding and its focus ring change with
  // the machine rather than with this repo. `appearance-none` drops the widget
  // and the field becomes an ordinary bordered box identical to the text inputs
  // beside it; the caret below is ours.
  //
  // What stays native ON PURPOSE: the popup that opens on click. It is an AppKit
  // menu rendered outside the web view — unreachable from CSS, and re-creating
  // it in the DOM would mean re-creating keyboard navigation, type-ahead and
  // screen-reader semantics. `color-scheme` (app.css, written per flavor by
  // theme-runtime) is the one lever over it, and it is enough: the popup follows
  // light/dark correctly.
  //
  // Every class is a LITERAL, same rule as <Button>: Tailwind scans source text.

  // Mirrors the forms' `inputCls` — minus `px-2`, because the caret owns the
  // right inset. Keep the two in step; a select that does not match the text
  // field one row above it is the thing this component is for.
  const FIELD =
    "peer w-full appearance-none rounded border border-edge bg-canvas py-1.5 pr-7 pl-2 text-ink outline-none " +
    "enabled:cursor-pointer focus:border-accent disabled:cursor-not-allowed";

  const CARET = "pointer-events-none absolute top-1/2 right-2 size-3 -translate-y-1/2 text-faint";

  // Grid, not a bare inline wrapper: the fade for a disabled control belongs on
  // the whole thing (see <Checkbox>), and the field must still fill the row —
  // WKWebView does not stretch a flex child inside a flex column (see app.css).
  const WRAP = "relative block w-full has-[:disabled]:opacity-40";
</script>

<script lang="ts">
  import type { Snippet } from "svelte";
  import type { HTMLSelectAttributes } from "svelte/elements";

  let {
    /** The <option> list. Passed as children so call sites keep writing plain
     *  markup — this component owns chrome, never the data. */
    children,
    /** Extra classes for the WRAPPER, so a row-level fade dims the caret too. */
    class: klass = "",
    ...rest
  }: { children: Snippet; class?: string } & HTMLSelectAttributes = $props();
</script>

<span class="{WRAP} {klass}">
  <select {...rest} class={FIELD}>
    {@render children()}
  </select>
  <svg
    class={CARET}
    viewBox="0 0 12 12"
    fill="none"
    stroke="currentColor"
    stroke-width="1.6"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
  >
    <path d="M2.5 4.5 6 8 9.5 4.5" />
  </svg>
</span>
