<script lang="ts">
  import type { Snippet } from "svelte";
  import Button from "./Button.svelte";
  import Chevron from "./Chevron.svelte";

  let {
    label,
    hint = "",
    children,
    class: cls = "border-t border-edge pt-3",
  }: { label: string; hint?: string; children: Snippet; class?: string } = $props();
  const id = $props.id();
  let open = $state(false);
</script>

<div class={cls}>
  <Button aria-expanded={open} aria-controls={id} onclick={() => { open = !open; }}>
    <Chevron {open} />{label}{#if hint}<span class="font-normal text-faint">&nbsp;{hint}</span>{/if}
  </Button>
  <div {id} hidden={!open}>
    {@render children()}
  </div>
</div>
