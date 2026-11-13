<script lang="ts">
  // Test double for SetupTokenRunner: one button that reports a stored token.
  import Button from "./Button.svelte";
  let { onStored }: { onStored?: () => void } = $props();
</script>

<Button variant="primary" onclick={() => onStored?.()}>Log in with Claude</Button>
