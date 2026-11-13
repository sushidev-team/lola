<script lang="ts">
  // Test harness: <Button> takes a children snippet, which a test cannot pass
  // through render()'s props. This wraps it with a fixed label so the test can
  // drive the props it cares about.
  import Button from "./Button.svelte";

  let { loading = false, disabled = false }: { loading?: boolean; disabled?: boolean } = $props();
</script>

<Button size="xs" {loading} {disabled}>Active</Button>
