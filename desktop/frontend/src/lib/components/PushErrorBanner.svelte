<script lang="ts">
  import { store } from "$lib/store.svelte";
  import Button from "./Button.svelte";

  // Reads the store directly (leaf component, always mounted) so its own template
  // reacts to pushError in the production WKWebView. See WKWEBVIEW_REACTIVITY.
  //
  // A live push error is almost always an out-of-date daemon: a `lola run`
  // predating a command answers `unknown cmd`, which used to just blank the
  // sidebar / status with no explanation. Restarting it re-execs the newest binary.
  const err = $derived(store.pushError);

  // Only `unknown cmd` actually proves a version skew. Any other failure from a
  // LIVE daemon (the down case never reaches here) is a real command error, and
  // telling the user to restart would send them after the wrong thing.
  const stale = $derived(!!err && /unknown cmd/i.test(err.msg));
</script>

{#if err}
  <div
    class="flex shrink-0 items-center gap-3 border-b border-warn/40 bg-warn/10 px-4 py-2 text-ink"
    role="alert"
  >
    <span class="shrink-0 text-warn">⚠</span>
    <span class="min-w-0 flex-1">
      {#if stale}
        The daemon is out of date — restart it to pick up the latest build.
      {:else}
        The daemon failed to answer <span class="font-mono text-sm">{err.cmd}</span>, so that panel may be stale.
      {/if}
      <span class="selectable text-sm text-faint">({err.cmd}: {err.msg})</span>
    </span>
    {#if stale}
      <Button variant="secondary" onclick={() => store.restartDaemon()}>Restart</Button>
    {/if}
    <Button icon title="dismiss" aria-label="dismiss" onclick={() => store.dismissPushError()}>✕</Button>
  </div>
{/if}
