<script lang="ts">
  // The terminal screen's wiring around the tab strip, mounted on its own so a
  // test can drive the strip the way the screen does and watch what comes back.
  //
  // IT EXISTS BECAUSE `rerender` CANNOT PROVE THIS ONE. Testing Library's
  // `rerender` re-runs the strip's load effect even when every prop is
  // byte-identical, so a refetch test written against it passes whether or not
  // `refreshKey` is read at all -- which is exactly the regression worth
  // catching, since a strip that ignores the signal goes straight back to
  // showing a tab whose tmux session is gone. A mutation that commented the
  // `refreshKey` read out survived a full rerender-driven suite. So the
  // counter is bumped HERE, by a button, through ordinary reactivity.
  //
  // It reproduces the seam rather than simplifying it: `active` follows
  // `onselect` exactly as Terminal.svelte's `attach` does, because the strip's
  // move-off-a-dead-pane behaviour is only meaningful if the parent actually
  // moves. The notice is captured rather than rendered -- the wording is the
  // assertion, and a banner would only be a second place for it to be wrong.

  import { untrack } from "svelte";
  import PaneTabs from "./PaneTabs.svelte";

  let {
    /** The session to draw, so a test can switch it without a rerender. */
    session = "lola-fe-42",
    /** The pane the screen starts attached to. */
    pane = "lola-fe-42",
  }: { session?: string; pane?: string } = $props();

  // untrack: the harness seeds from the prop once, on purpose.
  let active = $state(untrack(() => pane));
  let refreshKey = $state(0);

  /** Every pane `onselect` asked for, in order. */
  export const selected: string[] = $state([]);
  /** Every sentence `onnotice` produced, in order, empty strings included. */
  export const notices: string[] = $state([]);
  /**
   * Every inventory `onpanes` reported, in order, as `session` + pane names.
   *
   * The screen hands these to the size pin, which has no other way to learn
   * that a pane it is holding has gone -- so what is asserted is the argument
   * shape, not a render.
   */
  export const inventories: { session: string; names: string[] }[] = $state([]);

  /**
   * The pane whose menu is open, bound the way the terminal screen binds it
   * into `nav`. A test can read it after the strip has been destroyed, which
   * is the only way to see a hold timer that outlived its component.
   */
  export const menu = $state({ pane: "" });
</script>

<!-- The two levers a test needs, as real controls: one bumps the counter the
     screen bumps on a pane exit, the other is the strip itself. -->
<button type="button" onclick={() => refreshKey++}>bump refresh</button>

<PaneTabs
  {session}
  {active}
  {refreshKey}
  onselect={(p) => {
    selected.push(p);
    active = p;
  }}
  onnotice={(m) => notices.push(m)}
  onpanes={(session, names) => inventories.push({ session, names })}
  bind:menuPane={() => menu.pane, (v) => (menu.pane = v)}
/>
