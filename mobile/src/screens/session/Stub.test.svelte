<script lang="ts">
  let { id = "", pane = "" }: { id?: string; pane?: string } = $props();
</script>

<div data-stub data-id={id} data-pane={pane}></div>
