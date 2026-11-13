<script lang="ts">
  // THE way the app shows "when": a fluid relative age (lib/time.ts — "now",
  // "5m ago", "3h ago", "Mar 4") that stays live without a refresh, with the
  // exact moment in a styled hover card. Use it for every wire timestamp rather
  // than rendering a daemon-formatted age string, which freezes between polls
  // and cannot say what time it actually was.
  import HoverCard from "./HoverCard.svelte";
  import { exactTime, fluidAgo, nextTickIn, toMillis } from "$lib/time";

  let {
    at,
    ago = true,
    prefix = "",
    hint = "",
    fallback = "",
    class: cls = "",
  }: {
    /** ISO string, epoch ms or Date. Unknown renders nothing. */
    at: string | number | Date | undefined | null;
    /** Append "ago" to a relative unit ("5m ago"); false gives "5m". */
    ago?: boolean;
    /** Text before the age, e.g. "updated ". */
    prefix?: string;
    /** A line above the exact time in the hover card. */
    hint?: string;
    /** Preformatted label from an older daemon without a raw timestamp. */
    fallback?: string;
    class?: string;
  } = $props();

  let now = $state(Date.now());
  let timer: ReturnType<typeof setTimeout> | undefined;
  // One timer per label, scheduled for the moment its text can next change —
  // a "3h ago" wakes hourly, not every second.
  $effect(() => {
    const target = at;
    clearTimeout(timer);
    const arm = () => {
      timer = setTimeout(() => {
        now = Date.now();
        arm();
      }, nextTickIn(target, Date.now()));
    };
    now = Date.now();
    arm();
    return () => clearTimeout(timer);
  });

  const f = $derived(fluidAgo(at, now));
  const text = $derived(!f.text ? "" : f.text === "now" ? (ago ? "just now" : "now") : f.rel && ago ? `${f.text} ago` : f.text);
  const exact = $derived(exactTime(at));
</script>

{#if text}
  <HoverCard class="num whitespace-nowrap outline-none {cls}">
    {#snippet card()}
      {#if hint}<div class="text-faint">{hint}</div>{/if}
      <time class="num" datetime={new Date(toMillis(at)).toISOString()}>{exact}</time>
    {/snippet}
    {prefix}{text}
  </HoverCard>
{:else if fallback}
  <span class="num whitespace-nowrap {cls}">{prefix}{fallback}</span>
{/if}
