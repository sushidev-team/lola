<script lang="ts">
  // Test double for SetupTokenRunner: one button that reports a stored token,
  // one that reports a failed login.
  import Button from "./Button.svelte";
  let { onStored, onFailed }: { onStored?: () => void; onFailed?: (kind: string) => void } = $props();
</script>

<Button variant="primary" onclick={() => onStored?.()}>Log in with Claude</Button>
<Button onclick={() => onFailed?.("timeout")}>Fail the login</Button>
