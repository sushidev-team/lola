<script lang="ts">
  import { minuteClock } from "./clock.svelte";
  const { onread }: { onread: (n: number) => void } = $props();
  $effect(() => {
    onread(minuteClock.now);
  });
</script>
