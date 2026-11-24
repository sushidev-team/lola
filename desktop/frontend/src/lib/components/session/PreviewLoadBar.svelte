<script lang="ts">
  let { value }: { value?: number } = $props();
</script>

<div class="load-track" role="progressbar" aria-label="Loading page" aria-valuemin="0" aria-valuemax="100" aria-valuenow={value}>
  <span class:indeterminate={value === undefined} style:transform={value === undefined ? undefined : `scaleX(${value / 100})`}></span>
</div>

<style>
  .load-track {
    position: absolute;
    inset: 0 0 auto;
    z-index: 1;
    height: 3px;
    overflow: hidden;
    pointer-events: none;
    background: var(--color-accent-fill);
  }
  span {
    display: block;
    height: 100%;
    background: var(--color-accent);
    transform-origin: left center;
    transition: transform 120ms linear;
  }
  .indeterminate {
    width: 36%;
    animation: travel 1.25s ease-in-out infinite alternate;
  }
  @keyframes travel { to { transform: translateX(178%); } }
  @media (prefers-reduced-motion: reduce) {
    span { transition: none; }
    .indeterminate { animation: none; }
  }
</style>
