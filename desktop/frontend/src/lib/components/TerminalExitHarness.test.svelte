<script lang="ts">
  let { name, onExit }: { name: string; onExit?: () => void } = $props();
</script>
<button onclick={() => onExit?.()}>Exit {name}</button>
