<script lang="ts">
  import { DaemonService } from '@bindings/desktop';
  import Button from '../Button.svelte';
  let { sessionId, packageId, artifact, label }: { sessionId: string; packageId: string; artifact: string; label: string } = $props();
  let src = $state('');
  let error = $state('');
  let full = $state(false);
  let retry = $state(0);
  $effect(() => {
    const sid = sessionId, pid = packageId, image = artifact;
    retry;
    let active = true;
    src = ''; error = ''; full = false;
    DaemonService.ReviewPackageImage(sid, pid, image).then(result => {
      if (!active) return;
      if (!/^data:image\/(png|jpeg);base64,/.test(result.dataURL)) throw new Error('Unsupported screenshot');
      src = result.dataURL;
    }).catch(e => { if (active) error = String(e); });
    return () => { active = false; };
  });
</script>

<figure class="min-w-0">
  <figcaption class="mb-2 flex items-center justify-between gap-2 text-sm text-ink-2">
    <span>{label}</span>
    {#if src}<Button size="xs" aria-pressed={full} onclick={() => full = !full}>{full ? 'Fit image' : 'Full size'}</Button>{/if}
  </figcaption>
  <div class="image-well" class:full>
    {#if error}<p class="p-4 text-warn" role="status">Could not load screenshot. <Button size="xs" onclick={() => retry++}>Retry image</Button></p>
    {:else if src}<img {src} alt={label} onerror={() => { error = 'Image could not be decoded'; src = ''; }} />
    {:else}<p class="p-4 text-faint" role="status">Loading screenshot…</p>{/if}
  </div>
</figure>

<style>
  .image-well { border:1px solid var(--color-edge); border-radius:6px; background:var(--color-canvas); overflow:auto; max-height:65vh; }
  img { display:block; width:100%; height:auto; }
  .full img { width:auto; max-width:none; }
</style>
