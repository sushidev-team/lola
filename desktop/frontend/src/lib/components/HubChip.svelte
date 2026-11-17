<script lang="ts">
  // The small "team chore" mark on a session the team hub handed out
  // (SessionInfo.hub). A press opens the Team view on that chore.
  //
  // It is a real <button> wherever it may be one. Inside another control (the
  // sidebar row is itself a <button>) a nested button is invalid HTML and
  // steals the row's click, so there it stays a static mark: `static` says so
  // up front, and a chip that finds itself inside a button or a link on mount
  // falls back to the mark on its own. A given-back chore, a lost or taken
  // claim and a pull request in review change its tone and its words; the
  // tooltip says where the session came from.
  import { onMount } from "svelte";
  import type { HubSessionHub } from "@bindings/internal/protocol";
  import { hubChipText } from "$lib/hubboard";
  import { hub as hubStore } from "$lib/hub.svelte";
  import { nav } from "$lib/nav.svelte";
  import { tooltip } from "$lib/tooltip";
  import Icon from "./Icon.svelte";

  let { hub, class: cls = "", static: isStatic = false }: { hub: HubSessionHub; class?: string; static?: boolean } = $props();

  const chip = $derived(hubChipText(hub));
  const toneCls = $derived(
    chip.state === "lost" || chip.state === "taken"
      ? "bg-pill-broken/15 text-bad"
      : chip.state === "released"
        ? "bg-pill-grey text-faint"
        : "bg-pill-grey text-pill-grey-fg",
  );

  let probe = $state<HTMLElement>();
  /** Inside another control: draw the static mark. Decided once, on mount. */
  let nested = $state(true);
  onMount(() => {
    nested = !!probe?.parentElement?.closest("button, a, [role='button']");
  });
  const asButton = $derived(!isStatic && !nested);

  function open(e: MouseEvent) {
    e.stopPropagation();
    hubStore.focus(hub.itemId);
    nav.goTeam();
  }
</script>

{#snippet body()}
  <Icon name="users" size={11} />
  <span class="min-w-0 truncate">{chip.label}</span>
  {#if chip.note}<span class="shrink-0">· {chip.note}</span>{/if}
{/snippet}

{#if asButton}
  <!-- Hand-rolled on purpose: a chip-shaped mark that is also a control. A
       <Button> would bring its own height and padding ladder, and this has to
       sit inline with the session's title at the metadata size. -->
  <button
    type="button"
    class="inline-flex min-w-0 shrink items-center gap-1 rounded px-1.5 text-sm enabled:hover:underline focus-visible:outline-1 {toneCls} {cls}"
    aria-label={`${chip.tip.replace(/\n/g, " ")}`}
    use:tooltip={{ text: chip.tip }}
    data-hub-chip={chip.state || "active"}
    onclick={open}
  >{@render body()}</button>
{:else}
  <span
    bind:this={probe}
    class="inline-flex min-w-0 shrink items-center gap-1 rounded px-1.5 text-sm {toneCls} {cls}"
    role="img"
    aria-label={chip.tip.replace(/\n/g, " ")}
    use:tooltip={{ text: chip.tip }}
    data-hub-chip={chip.state || "active"}
  >{@render body()}</span>
{/if}
