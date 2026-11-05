<script lang="ts">
  // The row's activity line for a session on a host. A running command is a fact, so
  // it is mono. A headline is a claim, so it sits behind a hollow ring. A plate is not
  // needed at this size: the ring and the mono are the same two voices.
  import { plural, type SSHSessionInfo } from "$lib/ssh";

  let { info }: { info: SSHSessionInfo | null | undefined } = $props();
  const open = $derived(info?.openItems ?? 0);
</script>

<div class="flex min-w-0 items-center gap-1.5 text-sm text-faint">
  {#if info?.runningCommand}
    <span class="truncate font-mono">{info.runningCommand}</span>
  {:else if info?.headline}
    <span class="inline-block h-2 w-2 shrink-0 rounded-full border border-faint" aria-hidden="true"></span>
    <span class="truncate">{info.headline}</span>
  {/if}
  {#if open > 0}<span class="num shrink-0">· {plural(open, "question")}</span>{/if}
</div>
