<script lang="ts">
  // The wiring the terminal screen uses, mounted on its own so a test can drive
  // the popover and watch what reaches the terminal.
  //
  // It is not a convenience: the thing worth pinning is the SEAM. Moving A−/A+
  // out of the header and into a popover would be a silent regression if the
  // size stopped being remembered, because a forgotten font size looks exactly
  // like a font size that was never changed. So the harness reproduces the call
  // site exactly — the popover emits an absolute size, MobileTerminal.setFont
  // takes it, and MobileTerminal's own debounced writer persists it — rather
  // than saving anything itself.
  import MobileTerminal from "./MobileTerminal.svelte";
  import ViewSettings, { type ViewGeometry } from "./ViewSettings.svelte";
  import { loadFontSize } from "@mobile/lib/prefs";

  let termRef = $state<ReturnType<typeof MobileTerminal> | undefined>();
  let font = $state(loadFontSize());
  let geom = $state<ViewGeometry>({
    cols: 0,
    rows: 0,
    shown: 0,
    first: 1,
    panning: false,
    canFit: false,
    fitActive: false,
    fitSize: 0,
  });
</script>

<ViewSettings
  {font}
  {geom}
  onfont={(size) => termRef?.setFont(size)}
  onfit={() => termRef?.toggleFit()}
/>

<MobileTerminal
  bind:this={termRef}
  pane="harness-pane"
  onstate={(st) => {
    font = st.font;
    geom = {
      cols: st.cols,
      rows: st.rows,
      shown: st.shown,
      first: st.first,
      panning: st.panning,
      canFit: st.canFit,
      fitActive: st.fitActive,
      fitSize: st.fitSize,
    };
  }}
/>
