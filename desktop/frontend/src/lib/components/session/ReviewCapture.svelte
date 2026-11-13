<script lang="ts">
  import { tick } from 'svelte';
  import { DaemonService } from '@bindings/desktop';
  import type { ReviewPackage } from '@bindings/internal/protocol';
  import Button from '../Button.svelte';
  import Chevron from '../Chevron.svelte';
  import Icon from '../Icon.svelte';
  import Select from '../Select.svelte';

  let { pkg, initialURL = '', initialScreen = -1, busy = $bindable(false), onCaptured, onClose }: {
    pkg: ReviewPackage; initialURL?: string; initialScreen?: number; busy?: boolean;
    onCaptured: (pkg: ReviewPackage, index: number) => void; onClose: () => void;
  } = $props();
  let root = $state<HTMLElement>();
  let url = $state(''), title = $state('Page capture'), selector = $state('');
  let width = $state(1440), height = $state(900), channel = $state('chrome'), screenIndex = $state(-1);
  let error = $state('');
  let generation = 0;
  const storageKey = $derived(`lola.review.capture.${pkg.session}.${pkg.id}`);
  function save() {
    try { localStorage.setItem(storageKey,JSON.stringify({url,title,selector,width,height,channel,screenIndex})); } catch { /* Keep the in-memory draft. */ }
  }
  function choose(index: number) {
    screenIndex = index;
    const screen = pkg.screens?.[index];
    if (screen) { title = screen.title; url = screen.url || url; selector = screen.selector || ''; }
    save();
  }
  $effect(() => {
    const key = storageKey;
    screenIndex = initialScreen >= 0 ? initialScreen : (pkg.screens?.length ?? 0) >= 4 ? 0 : -1;
    const screen = pkg.screens?.[screenIndex];
    url = screen?.url || initialURL; title = screen?.title || 'Page capture'; selector = screen?.selector || '';
    try {
      const saved = JSON.parse(localStorage.getItem(key) ?? '{}');
      if (typeof saved.url === 'string') url = saved.url;
      if (typeof saved.title === 'string') title = saved.title;
      if (typeof saved.selector === 'string') selector = saved.selector;
      if (typeof saved.width === 'number') width = saved.width;
      if (typeof saved.height === 'number') height = saved.height;
      if (['chrome','msedge',''].includes(saved.channel)) channel = saved.channel;
      if (Number.isInteger(saved.screenIndex) && saved.screenIndex >= -1 && saved.screenIndex < (pkg.screens?.length ?? 0) && (saved.screenIndex >= 0 || (pkg.screens?.length ?? 0) < 4)) screenIndex = saved.screenIndex;
    } catch { /* Invalid storage leaves sensible defaults. */ }
    const token = ++generation;
    void tick().then(() => { if (token === generation) root?.querySelector<HTMLInputElement>('input[type=url]')?.focus(); });
    return () => { generation++; };
  });
  async function capture(event: SubmitEvent) {
    event.preventDefault();
    if (busy) return;
    const token = generation, index = screenIndex < 0 ? (pkg.screens?.length ?? 0) : screenIndex;
    busy = true; error = ''; save();
    try {
      const result = await DaemonService.ReviewPackageCapture(pkg.session,{package:pkg.id,screen:screenIndex,url,title,selector,width,height,channel});
      if (token !== generation) return;
      try { localStorage.removeItem(storageKey); } catch { /* Storage is optional. */ }
      onCaptured(result,index);
    } catch (err) { if (token === generation) error = `${String(err)} Refresh review versions before retrying if the connection was lost.`; }
    finally { if (token === generation) busy = false; }
  }
</script>

<section class="capture-panel" aria-label="Capture screenshot" bind:this={root}>
  <header><div><h2>Capture screenshot</h2><p>Save a new review version. Earlier images stay available.</p></div><Button size="sm" icon aria-label="Close capture" disabled={busy} onclick={onClose}><Icon name="x" size={15}/></Button></header>
  <form onsubmit={capture} oninput={save}>
    <fieldset disabled={busy}>
      <div class="capture-source">
        <label>Page URL<input type="url" required maxlength="2048" bind:value={url} placeholder="http://localhost:3000" aria-describedby="capture-context"/></label>
        <p id="capture-context">Uses a fresh browser session without your saved sign-in.</p>
      </div>
      <div class="capture-fields">
        <label>Save to<Select aria-label="Capture destination" value={String(screenIndex)} onchange={e => choose(Number(e.currentTarget.value))}>{#if (pkg.screens?.length ?? 0) < 4}<option value="-1">New screenshot</option>{/if}{#each pkg.screens ?? [] as screen,index}<option value={String(index)}>Update {screen.title}</option>{/each}</Select></label>
        <label>Screenshot title<input required maxlength="200" bind:value={title}/></label>
      </div>
      <details>
        <summary><Chevron class="disclosure-chevron"/><span>Capture options</span><span class="settings-value">{width} × {height}</span></summary>
        <div class="capture-options">
          <label>Element (optional)<input maxlength="500" bind:value={selector} placeholder="main, #settings, …"/></label>
          <div class="capture-fields"><label>Width (px)<input type="number" required min="240" max="4096" step="1" bind:value={width}/></label><label>Height (px)<input type="number" required min="240" max="4096" step="1" bind:value={height}/></label></div>
          <label>Browser<Select aria-label="Capture browser" value={channel} onchange={e => { channel=e.currentTarget.value;save(); }}><option value="chrome">Google Chrome</option><option value="">Playwright Chromium</option><option value="msedge">Microsoft Edge</option></Select></label>
          <p>Requires Node, local Playwright and the selected browser. Nothing is installed automatically.</p>
        </div>
      </details>
    </fieldset>
    {#if error}<p role="alert" class="capture-error">{error}</p>{/if}
    <footer><p>{busy ? 'Opening the page and capturing…' : 'Captures the live page; it does not verify its code revision.'}</p><Button type="submit" size="sm" variant="primary" loading={busy}>{busy ? 'Capturing…' : 'Capture & save'}</Button></footer>
  </form>
</section>

<style>
  .capture-panel { padding:20px 28px; border-bottom:1px solid var(--color-edge); background:var(--color-panel); min-width:0; max-height:65dvh; overflow:auto; }
  header { display:flex; justify-content:space-between; align-items:flex-start; gap:16px; margin-bottom:16px; }
  h2 { font-size:14px; line-height:20px; font-weight:600; color:var(--color-ink); }
  p { font-size:12px; line-height:18px; color:var(--color-ink-2); }
  header p { margin-top:4px; }
  form { max-width:760px; display:grid; gap:16px; }
  fieldset { display:grid; gap:16px; min-width:0; padding:0; border:0; }
  .capture-fields { display:grid; grid-template-columns:repeat(2,minmax(0,1fr)); gap:16px; }
  label { display:grid; gap:6px; font-size:12px; line-height:18px; color:var(--color-ink-2); min-width:0; }
  input { min-width:0; width:100%; border:1px solid var(--color-edge); border-radius:6px; padding:8px; background:var(--color-canvas); color:var(--color-ink); font-size:12px; line-height:18px; }
  input:focus-visible,summary:focus-visible { outline:2px solid var(--color-accent); outline-offset:2px; }
  .capture-source p { margin-top:6px; }
  details { border-top:1px solid var(--color-edge); border-bottom:1px solid var(--color-edge); padding:8px 0; }
  summary { display:flex; align-items:center; gap:8px; font-size:12px; line-height:20px; cursor:pointer; list-style:none; }
  summary::-webkit-details-marker { display:none; }
  details[open] > summary > :global(.disclosure-chevron) { transform:rotate(90deg); }
  .settings-value { margin-left:auto; color:var(--color-ink-2); font-size:11px; }
  .capture-options { display:grid; gap:12px; margin-top:12px; padding-bottom:4px; }
  footer { display:flex; align-items:center; justify-content:space-between; gap:16px; }
  footer p { max-width:48ch; }
  .capture-error { color:var(--color-warn); overflow-wrap:anywhere; }
  @media(max-width:600px) { .capture-panel { padding:16px; } .capture-fields { gap:12px; } footer { align-items:flex-end; } }
</style>
