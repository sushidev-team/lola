<script lang="ts">
  let { width = '100%', height = '14px' }: { width?: string; height?: string } = $props();
</script>

<span class="skeleton" aria-hidden="true" style:width style:height></span>

<style>
  .skeleton { display:block; max-width:100%; border-radius:4px; background:var(--color-edge); animation:skeleton-pulse 1.5s ease-in-out infinite; }
  @keyframes skeleton-pulse { 0%,100% { opacity:.5; } 50% { opacity:.85; } }
  @media(prefers-reduced-motion:reduce) { .skeleton { animation:none; } }
</style>
