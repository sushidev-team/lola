<script lang="ts">
  let { name }: { name: string } = $props();
</script>

<div data-testid="terminal" data-name={name}></div>
