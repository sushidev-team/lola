<script lang="ts">
  import TouchButton from "./TouchButton.svelte";
  let { variant = "ghost", prominent = false, disabled = false }: { variant?: "ghost" | "primary" | "secondary"; prominent?: boolean; disabled?: boolean } = $props();
</script>

<TouchButton {variant} {prominent} {disabled}>Go</TouchButton>
