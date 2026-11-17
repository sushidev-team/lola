<script lang="ts">
  import type { DevForward } from "@bindings/internal/protocol/models";
  import Sheet from "./Sheet.svelte";
  import ListGroup from "./ListGroup.svelte";
  import ListRow from "./ListRow.svelte";
  import Icon from "$lib/components/Icon.svelte";
  import EmptyState from "./EmptyState.svelte";
  import { copy, fill } from "@mobile/lib/copy";

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
   * "on this phone" out loud for that reason. The sheet's rows now carry only
   * what names a server (its port and path); the title and the arrow say the
   * rest, and the opening behaviour (http(s) only, forward host) is unchanged.
   */
  let {
    forwards,
    notice = "",
    onstart,
    onopen,
    onclose,
  }: {
    forwards: readonly DevForward[];
    /** Why the last link did not open, or "". */
    notice?: string;
    /** Starts the session's dev server; given only when the session has one to start and this phone may. */
    onstart?: () => void;
    onopen: (url: string) => void;
    onclose: () => void;
  } = $props();

  /**
   * What to call one address: THE ORIGINAL's port, always.
   *
   * The forward's own port is allocated by the kernel — 65497 identifies
   * nothing and changes on every restart. The address a developer knows is the
   * one the server printed: 8000 is the Laravel app, 5175 is vite. Leading with
   * the forward made every row look the same.
   */
  function parts(f: DevForward): { port: string; path: string } {
    for (const raw of [f.from, f.url]) {
      if (!raw) continue;
      try {
        const u = new URL(raw.includes("://") ? raw : `http://${raw}`);
        const port = u.port || (u.protocol === "https:" ? "443" : "80");
        const path = u.pathname + u.search;
        // The original may carry no path; the forward's URL can still name one.
        if (path !== "/" || raw === f.url) return { port, path: path || "/" };
        const own = new URL(f.url);
        return { port, path: own.pathname + own.search || "/" };
      } catch {
        // fall through to the next candidate
      }
    }
    return { port: f.from || f.url, path: "/" };
  }
</script>

<Sheet label="Dev servers" title={copy.devLinks.title} {onclose}>
  {#if notice}
    <p class="copy text-sm text-bad" role="status">{notice}</p>
  {/if}

  {#if forwards.length === 0}
    <EmptyState
      size="sheet"
      icon="globe"
      title={copy.devLinks.empty.title}
      action={onstart ? { label: copy.devLinks.empty.action, onclick: onstart } : undefined}
    />
  {:else}
    <ListGroup>
      {#each forwards as f (f.url)}
        {@const p = parts(f)}
        <ListRow
          icon="globe"
          label={fill(copy.devLinks.row, { port: p.port })}
          detail={p.path}
          detailMono
          onclick={() => onopen(f.url)}
        >
          {#snippet trailing()}<span class="text-faint"><Icon name="arrow-up-right" size={18} /></span>{/snippet}
        </ListRow>
      {/each}
    </ListGroup>
  {/if}
</Sheet>
