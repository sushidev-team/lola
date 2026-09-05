<script lang="ts">
  import type { DevForward } from "@bindings/internal/protocol/models";
  import Sheet from "./Sheet.svelte";
  import TouchButton from "./TouchButton.svelte";

  /**
   * The addresses of a session's dev servers, as this phone can reach them.
   *
   * WHY A SHEET RATHER THAN A BUTTON. A session's dev commands print more than
   * one address as a rule, not as an exception — an app and a bundler, at
   * least — and picking between them is the whole interaction. A single button
   * would have to guess, and guessing wrong opens the bundler's asset server
   * instead of the app.
   *
   * WHAT THESE ADDRESSES ARE. The daemon republishes the session's loopback
   * servers on one private interface while the session is ACTIVE
   * (internal/daemon/devforwardwire.go), so these are LAN addresses reachable
   * from this phone — not the `127.0.0.1:8000` the Mac sees, which would be
   * this phone's own loopback and reach nothing.
   *
   * THEY OPEN ON THIS PHONE. `openExternal`, never the daemon's `cmd=openURL`:
   * that one runs `open` on the MAC, so it would launch Safari on an unattended
   * desktop in another room. Same gesture, opposite machine — this sheet says
   * "on this phone" out loud for that reason.
   */
  let {
    forwards,
    onopen,
    onclose,
  }: {
    forwards: readonly DevForward[];
    onopen: (url: string) => void;
    onclose: () => void;
  } = $props();

  /**
   * What to call one address: THE ORIGINAL, always.
   *
   * The forward's own port is allocated by the kernel — 65497 identifies
   * nothing and changes on every restart. The address a developer knows is the
   * one the server printed: 8000 is the Laravel app, 5175 is vite. Leading with
   * the forward made every row look the same and turned choosing into a guess,
   * which with an app and a bundler is a coin flip.
   */
  function label(f: DevForward): string {
    return f.from || f.url;
  }
</script>

<Sheet label="Dev server links" {onclose}>
  <p class="copy text-sm text-faint">
    Opens on THIS phone. The session's dev servers, republished on your network by the daemon —
    they are reachable only while this session is the active one.
  </p>

  {#each forwards as f (f.url)}
    <TouchButton wide variant="secondary" onclick={() => onopen(f.url)}>
      <span class="flex min-w-0 flex-col items-start">
        <!-- The original first, because it is the one that names the thing. -->
        <span class="num">{label(f)}</span>
        <!-- And where it actually goes, because a person checking WHICH machine
             this reaches has nothing else to read it from. -->
        <span class="num truncate text-xs text-faint">via {f.url}</span>
      </span>
    </TouchButton>
  {/each}

  <TouchButton wide onclick={onclose}>Cancel</TouchButton>
</Sheet>
