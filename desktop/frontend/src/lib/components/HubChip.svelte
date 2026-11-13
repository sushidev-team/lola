<script lang="ts">
  // The small "team chore" mark on a session the team hub handed out
  // (SessionInfo.hub). Display-only, no control inside, so it may sit inside a
  // sidebar row (itself a <button>). A released chore or a lost lease changes
  // its tone and its words; the tooltip says where the session came from.
  import type { HubSessionHub } from "@bindings/internal/protocol";
  import { hubChipText } from "$lib/hubboard";
  import { tooltip } from "$lib/tooltip";
  import Icon from "./Icon.svelte";

  let { hub, class: cls = "" }: { hub: HubSessionHub; class?: string } = $props();

  const chip = $derived(hubChipText(hub));
  const toneCls = $derived(
    chip.state === "lost" ? "bg-pill-broken/15 text-bad" : chip.state === "released" ? "bg-pill-grey text-faint" : "bg-pill-grey text-pill-grey-fg",
  );
</script>

<span
  class="inline-flex min-w-0 shrink items-center gap-1 rounded px-1.5 text-sm {toneCls} {cls}"
  role="img"
  aria-label={chip.tip.replace(/\n/g, " ")}
  use:tooltip={{ text: chip.tip }}
  data-hub-chip={chip.state || "active"}
>
  <Icon name="users" size={11} />
  <span class="min-w-0 truncate">{chip.label}</span>
  {#if chip.state === "lost"}<span class="shrink-0">· Lease lost</span>{:else if chip.state === "released"}<span class="shrink-0">· Released</span>{/if}
</span>
