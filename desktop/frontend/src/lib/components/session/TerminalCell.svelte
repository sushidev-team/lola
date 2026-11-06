<script lang="ts">
  // The one place a session's own terminal is mounted, and always rendered, with `name`
  // possibly empty. It holds the only {#if name}{#key name}, because an {#if} that mounts a
  // LiveTerminal in a parent's template froze that parent's template effect in the production
  // WKWebView (CockpitLayout records it), and in the session view Enter and Esc flip such a
  // branch every time. Selection changes keyed by name are the proven path.
  //
  // A terminal is attached only while it is on screen, so `name` is empty in every other view:
  // an attached client silently defers every automated send to the agent. There is no read-only
  // or hidden client either, because a sole read-only client makes every send-keys fail and
  // still resizes the agent.
  //
  // It takes the keyboard on mount only when the session is open. In the band under the list a click
  // gives it the keyboard, so walking the list past a session on its terminal cannot trap j and k.
  //
  // p-4 and bg-panel: see the old note on Ghostty's padding, which this keeps. No isolate or z-0
  // here or on any ancestor, which blanks the WebGL canvas in WKWebView.
  import LiveTerminal from "../LiveTerminal.svelte";

  let {
    name = "",
    focusRequest = 0,
    autofocus = true,
    onExit,
    onLeave,
    style = "",
  }: {
    name?: string;
    focusRequest?: number;
    autofocus?: boolean;
    onExit?: () => void;
    /** Ctrl-Q. Esc belongs to whatever runs in the terminal. */
    onLeave?: () => void;
    style?: string;
  } = $props();
</script>

<div class="min-h-0 min-w-0 bg-panel p-4 {name ? '' : 'hidden'}" {style} data-terminal-cell>
  {#if name}
    {#key name}
      <LiveTerminal {name} webgl interactive {autofocus} {focusRequest} {onExit} onEscapeFocus={onLeave} />
    {/key}
  {/if}
</div>
