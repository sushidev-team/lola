<script lang="ts">
  import { untrack } from 'svelte';
  let { src = '', name, size = 18, loading = false }: { src?: string; name: string; size?: number; loading?: boolean } = $props();
  let displayedSource = $state('');
  let failedSource = $state('');
  const safeSource = $derived.by(() => {
    // Native cache payloads are bounded raster bytes. Do not accept SVG or a
    // generic data: URL merely because the caller labelled it as an image.
    const data = /^data:image\/(png|jpeg|gif|webp);base64,([A-Za-z0-9+/]+={0,2})$/.exec(src);
    if (data) {
      const bytes = data[2], length = bytes.length * 3 / 4 - (bytes.endsWith('==') ? 2 : bytes.endsWith('=') ? 1 : 0);
      if (bytes.length % 4 !== 0 || length > 256 * 1024) return '';
      try {
        const magic = atob(bytes.slice(0, Math.min(32, bytes.length)));
        const valid = data[1] === 'png' ? magic.startsWith('\x89PNG\r\n\x1a\n') : data[1] === 'jpeg' ? magic.startsWith('\xff\xd8\xff') : data[1] === 'gif' ? /^GIF8[79]a/.test(magic) : magic.startsWith('RIFF') && magic.slice(8, 12) === 'WEBP';
        return valid ? src : '';
      } catch { return ''; }
    }
    // Keep the existing allowlist for old snapshots and local UI fixtures.
    // Production identity responses use cached raster data, never these URLs.
    try {
      const url = new URL(src);
      return url.protocol === 'https:' && !url.username && !url.password && (!url.port || url.port === '443') && ['github.com', 'avatars.githubusercontent.com'].includes(url.hostname) ? url.href : '';
    } catch { return ''; }
  });
  $effect(() => {
    const source = safeSource;
    untrack(() => { failedSource = ''; if (!source) displayedSource = ''; });
  });
  const pendingSource = $derived(safeSource && safeSource !== displayedSource && safeSource !== failedSource ? safeSource : '');
  const shimmer = $derived(!displayedSource && (loading || !!pendingSource));
  const initials = $derived(name.trim().split(/\s+/).slice(0, 2).map(part => part[0]).join('').toUpperCase() || '?');
  function loaded(source: string) { if (source === safeSource) displayedSource = source; }
  function failed(source: string) { if (source === safeSource) failedSource = source; }
</script>

<span class="git-avatar" class:avatar-loading={shimmer} style:width={`${size}px`} style:height={`${size}px`} style:font-size={`${Math.max(9, size * .38)}px`} aria-hidden="true">
  <span>{initials}</span>
  {#if displayedSource}<img class="avatar-image" src={displayedSource} alt="" width={size} height={size} decoding="async" referrerpolicy="no-referrer" />{/if}
  {#if pendingSource}{@const source = pendingSource}<img class="avatar-pending" src={source} alt="" width={size} height={size} loading="lazy" decoding="async" referrerpolicy="no-referrer" onload={() => loaded(source)} onerror={() => failed(source)} />{/if}
</span>

<style>
  .git-avatar { display:inline-grid; place-items:center; position:relative; flex-shrink:0; overflow:hidden; border-radius:50%; background:var(--color-sel); color:var(--color-ink); font-weight:600; line-height:1; isolation:isolate; }
  .avatar-loading { background-image:linear-gradient(105deg,transparent 25%,color-mix(in srgb,var(--color-ink) 12%,transparent) 48%,transparent 70%); background-size:220% 100%; animation:avatar-shimmer 1.5s ease-in-out infinite; }
  img { position:absolute; inset:0; width:100%; height:100%; object-fit:cover; }
  .avatar-pending { opacity:0; }
  @keyframes avatar-shimmer { from { background-position:120% 0; } to { background-position:-120% 0; } }
  @media (prefers-reduced-motion: reduce) { .avatar-loading { animation:none; background-image:none; } }
</style>
