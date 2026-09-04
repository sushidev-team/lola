<script lang="ts">
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
    urls,
    onopen,
    onclose,
  }: { urls: readonly string[]; onopen: (url: string) => void; onclose: () => void } = $props();

  /**
   * What to call one address.
   *
   * The PORT is the whole label: a person recognises "8000" as the Laravel app
   * and "5173" as vite, and the host is the same for all of them, so leading
   * with it would put the one identical part first on every row.
   */
  function label(url: string): string {
    try {
      const u = new URL(url);
      return u.port ? `Port ${u.port}` : u.hostname;
    } catch {
      return url;
    }
  }
</script>

<Sheet label="Dev server links" {onclose}>
  <p class="copy text-sm text-faint">
    Opens on THIS phone. The session's dev servers, republished on your network by the daemon —
    they are reachable only while this session is the active one.
  </p>

  {#each urls as url (url)}
    <TouchButton wide variant="secondary" onclick={() => onopen(url)}>
      <span class="flex min-w-0 flex-col items-start">
        <span>{label(url)}</span>
        <span class="num truncate text-xs text-faint">{url}</span>
      </span>
    </TouchButton>
  {/each}

  <TouchButton wide onclick={onclose}>Cancel</TouchButton>
</Sheet>
