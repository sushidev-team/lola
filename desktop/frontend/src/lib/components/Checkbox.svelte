<script lang="ts" module>
  // The app's ONE checkbox — and it exists for a reason stronger than tidiness.
  //
  // A bare <input type="checkbox"> is drawn by AppKit, not by us: its box, its
  // corner radius, its tick and its focus ring all come from the macOS version
  // the app happens to be running on. Two machines on the SAME build therefore
  // showed visibly different forms (macOS 26's Liquid Glass controls against the
  // older flat ones), which is the worst class of difference to get bug-reported
  // — nothing in the repo explains it and nothing in the repo can change it.
  // `appearance-none` throws the native widget away; everything drawn below is
  // ours, so the control looks identical on every machine and in every flavor.
  //
  // The tick is a real SVG SIBLING, never an ::after on the input: WebKit does
  // not reliably render pseudo-elements on form controls, so a checkmark built
  // that way would work in Chrome (`wails3 dev`) and vanish in the packaged app
  // — reintroducing exactly the divergence this component removes. It paints in
  // `currentColor` and rides the `peer-checked:` variant, so it needs no palette
  // and no state of its own.
  //
  // Every class is a LITERAL, same rule as <Button>: Tailwind scans source text,
  // so a composed `bg-${x}` compiles to nothing.

  // `accent` is the 3:1 decorative fill and `on-accent` its MEASURED ink (the
  // pairing the status pills use), so a checked box stays legible on the light
  // flavors too. Unchecked is the input's own `canvas`-on-`edge`, matching the
  // text fields it sits beside.
  const BOX =
    "peer m-0 size-3.5 shrink-0 appearance-none rounded-[3px] border border-edge bg-canvas " +
    "transition-colors checked:border-accent checked:bg-accent " +
    "enabled:cursor-pointer enabled:hover:border-accent disabled:cursor-not-allowed";

  const TICK =
    "pointer-events-none absolute size-2.5 text-on-accent opacity-0 transition-opacity peer-checked:opacity-100";

  // The fade lives on the WRAPPER, not on the input: `disabled:opacity-40` on
  // the box alone would leave a full-strength tick floating over a dead control.
  const WRAP = "relative inline-flex size-3.5 shrink-0 items-center justify-center has-[:disabled]:opacity-40";
</script>

<script lang="ts">
  import type { HTMLInputAttributes } from "svelte/elements";

  let {
    /**
     * Two-way when the call site uses `bind:checked`. A call site that passes a
     * one-way `checked={…}` plus an `onchange` keeps working: the box flips
     * locally (exactly as the native control did) and the handler's write to the
     * real source of truth flows straight back down.
     */
    checked = $bindable(false),
    /** Extra classes for the WRAPPER — so a fade (`opacity-55` on an inherited
     *  row) dims the tick with the box instead of only half the control. */
    class: klass = "",
    ...rest
  }: { checked?: boolean; class?: string } & HTMLInputAttributes = $props();
</script>

<span class="{WRAP} {klass}">
  <input type="checkbox" bind:checked {...rest} class={BOX} />
  <svg
    class={TICK}
    viewBox="0 0 14 14"
    fill="none"
    stroke="currentColor"
    stroke-width="2.2"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
  >
    <path d="M3 7.4 5.9 10.2 11 3.9" />
  </svg>
</span>
