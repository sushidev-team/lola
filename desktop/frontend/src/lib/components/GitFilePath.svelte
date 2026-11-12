<script lang="ts">
  let {path, leafOnly = false}: {path:string; leafOnly?:boolean} = $props();
  const slash = $derived(path.lastIndexOf('/'));
  const directory = $derived(!leafOnly && slash >= 0 ? path.slice(0,slash) : '');
  const name = $derived(path.slice(slash+1));
</script>
<span class="git-file-path"><span class="directory">{directory}</span><span class="basename" class:with-directory={!!directory}>{directory ? '/' : ''}{name}</span></span>
<style>
  .git-file-path { display:flex; align-items:baseline; min-width:0; flex:1; overflow:hidden; font-size:13px; }
  .directory { min-width:0; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; color:var(--color-ink-2); }
  .basename { min-width:0; flex-shrink:0; max-width:100%; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; color:var(--color-ink); }
  .basename.with-directory { max-width:75%; }
</style>
