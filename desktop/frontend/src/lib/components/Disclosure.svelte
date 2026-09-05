<script lang="ts">
  import type { Snippet } from "svelte";
  import Button from "./Button.svelte";

  let { label, children }: { label: string; children: Snippet } = $props();
  const id = $props.id();
  let open = $state(false);
</script>

<div class="border-t border-edge pt-3">
  <Button aria-expanded={open} aria-controls={id} onclick={() => { open = !open; }}>
    <span aria-hidden="true">{open ? "▾" : "▸"}</span>{label}
  </Button>
  <div {id} hidden={!open}>
    {@render children()}
  </div>
</div>
