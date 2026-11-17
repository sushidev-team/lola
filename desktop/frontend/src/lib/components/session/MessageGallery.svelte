<script lang="ts">
  import { TalkService } from '@bindings/desktop';
  import { untrack } from 'svelte';
  import { SvelteMap } from 'svelte/reactivity';
  import Modal from '../Modal.svelte';

  let { images, initial, worktree, thumbs, onclose }: {
    images: { n: number; path: string }[];
    initial: number;
    worktree: string;
    thumbs: Map<string, string>;
    onclose: () => void;
  } = $props();
  let index = $state(untrack(() => initial));
  let zoom = $state(false);
  let loading = $state(false);
  let error = $state('');
  const originals = new SvelteMap<string, string>();
  const current = $derived(images[index]);
  const source = $derived(originals.get(current.path) || thumbs.get(current.path));
  $effect(() => {
    const path = current.path;
    zoom = false;
    error = '';
    if (originals.has(path)) { loading = false; return; }
    let cancelled = false;
    loading = true;
    TalkService.ReadAttachmentImage(worktree, path).then(data => {
      if (!cancelled) originals.set(path, data);
    }).catch(() => {
      if (!cancelled) error = 'Original image unavailable';
    }).finally(() => { if (!cancelled) loading = false; });
    return () => { cancelled = true; };
  });
  function move(delta: number) { index = (index + delta + images.length) % images.length; }
  function key(event: KeyboardEvent) {
    // This local overlay owns keys; do not let app shortcuts switch the session underneath it.
    event.stopPropagation();
    if (event.key === 'Escape') { event.preventDefault(); onclose(); }
    if (event.key === 'ArrowLeft' || event.key === 'ArrowRight') {
      event.preventDefault(); move(event.key === 'ArrowLeft' ? -1 : 1);
    }
  }
</script>

<Modal portal title="Image {current.n} · {images.length}" width="min(1200px, calc(100vw - 48px))" bodyClass="gallery-body" onClose={onclose} onkey={key}>
  <div class="gallery-stage" class:gallery-zoom={zoom} aria-busy={loading}>
    {#if source}
      <button class="gallery-picture" type="button" aria-label={zoom ? 'Fit image' : 'View image at full size'} onclick={() => zoom = !zoom}>
        <img src={source} alt="Image {current.n}" />
      </button>
    {:else}
      <span class="text-faint">{loading ? 'Loading image…' : error}</span>
    {/if}
  </div>
  <div class="gallery-controls">
    <button type="button" aria-label="Previous image" disabled={images.length < 2} onclick={() => move(-1)}>←</button>
    <div class="gallery-thumbnails" aria-label="Message images">
      {#each images as im, i (im.n)}
        <button type="button" aria-label="Show image {im.n}" aria-pressed={i === index} onclick={() => index = i}>
          {#if originals.get(im.path) || thumbs.get(im.path)}<img src={originals.get(im.path) || thumbs.get(im.path)} alt="" />{/if}
          <span>{im.n}</span>
        </button>
      {/each}
    </div>
    <button type="button" aria-label="Next image" disabled={images.length < 2} onclick={() => move(1)}>→</button>
  </div>
  {#if error && source}<p class="px-4 pb-2 text-sm text-faint" role="status">{error} · showing saved thumbnail</p>{/if}
</Modal>

<style>
  :global(.gallery-body) { display: grid; grid-template-rows: minmax(0,1fr) auto; overflow: hidden; }
  .gallery-stage { display: grid; place-items: center; height: min(65vh, 760px); min-height: 180px; overflow: auto; background: var(--color-canvas); }
  .gallery-picture { width: 100%; height: 100%; min-height: 0; padding: 16px; cursor: zoom-in; }
  .gallery-picture img { display: block; width: 100%; height: 100%; object-fit: contain; }
  .gallery-zoom { place-items: start; }
  .gallery-zoom .gallery-picture { width: max-content; height: max-content; min-width: 100%; min-height: 100%; cursor: zoom-out; }
  .gallery-zoom img { width: auto; height: auto; max-width: none; margin: auto; }
  .gallery-controls { display: flex; align-items: center; justify-content: center; gap: 12px; padding: 12px 16px; }
  .gallery-controls > button { flex-shrink: 0; width: 32px; height: 32px; border-radius: 6px; color: var(--color-ink-2); background: var(--color-sel); }
  .gallery-controls > button:disabled { opacity: .3; }
  .gallery-thumbnails { display: flex; gap: 8px; padding: 3px; overflow-x: auto; }
  .gallery-thumbnails button { position: relative; flex: 0 0 72px; height: 52px; padding: 3px; background: var(--color-canvas); border: 1px solid var(--color-edge); border-radius: 5px; }
  .gallery-thumbnails button[aria-pressed=true] { border-color: var(--cv-message-accent, oklch(68% 0.21 275)); background: color-mix(in srgb, var(--cv-message-accent, oklch(68% 0.21 275)) 18%, var(--color-canvas)); }
  .gallery-thumbnails img { width: 100%; height: 100%; object-fit: contain; }
  .gallery-thumbnails span { position: absolute; top: 2px; left: 2px; padding: 0 4px; font-size: 10px; border-radius: 3px; color: var(--color-canvas); background: var(--cv-message-accent, oklch(68% 0.21 275)); }
  button:focus-visible { outline: 2px solid var(--cv-message-accent, oklch(68% 0.21 275)); outline-offset: 2px; }
</style>
