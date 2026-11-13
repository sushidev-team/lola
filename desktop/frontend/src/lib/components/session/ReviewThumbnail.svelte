<script lang="ts">
  import { DaemonService } from '@bindings/desktop';
  import Icon from '../Icon.svelte';
  let { pkg, artifact, label }: { pkg: {session:string;id:string}; artifact:string; label:string } = $props();
  let src = $state(''), failed = $state(false);
  $effect(() => {
    const session = pkg.session, id = pkg.id, image = artifact;
    let active = true; src = ''; failed = false;
    DaemonService.ReviewPackageImage(session,id,image).then(result => {
      if (!active) return;
      if (/^data:image\/(png|jpeg);base64,/.test(result.dataURL)) src = result.dataURL;
      else failed = true;
    }).catch(() => { if (active) failed = true; });
    return () => { active = false; };
  });
</script>
<div class="thumbnail">
  {#if src && !failed}<img {src} alt={label} onerror={() => failed = true} />
  {:else}<div class="placeholder"><Icon name="image" size={22}/><span>{failed ? 'Open screenshot' : 'Loading preview…'}</span></div>{/if}
</div>
<style>
  .thumbnail { aspect-ratio:16/9; overflow:hidden; background:var(--color-canvas); border-bottom:1px solid var(--color-edge); }
  img { width:100%; height:100%; object-fit:contain; }
  .placeholder { display:flex; height:100%; align-items:center; justify-content:center; flex-direction:column; gap:8px; color:var(--color-ink-2); font-size:12px; }
</style>
