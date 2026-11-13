<script lang="ts">
  import Icon from '../Icon.svelte';
  let { kind, class: cls = '' }: { kind: 'image' | 'text'; class?: string } = $props();
</script>
<Icon name={kind === 'image' ? 'image' : 'file-text'} class={cls} data-attach-glyph={kind} />
