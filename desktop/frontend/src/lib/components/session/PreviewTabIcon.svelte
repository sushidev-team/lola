<script lang="ts">
  import Icon from "../Icon.svelte";
  let { src }: { src?: string } = $props();
  let failed = $state("");
</script>

<span aria-hidden="true" class="grid size-5 shrink-0 place-items-center text-faint">
  {#if src && src !== failed}
    <img {src} alt="" referrerpolicy="no-referrer" draggable="false" class="size-4 object-contain"
      onerror={(event) => failed = event.currentTarget.getAttribute("src") ?? ""} />
  {:else}
    <Icon name="globe" size={15} />
  {/if}
</span>
