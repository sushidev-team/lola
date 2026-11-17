<div role="status" aria-label="Loading review">
  <span class="sr-only">Loading review…</span>
  <div class="review-skeleton" aria-hidden="true">
    <div class="material">
      <div class="intro"><div class="bar title"></div><div class="bar"></div><div class="bar short"></div></div>
      <div class="panel"><div class="bar heading"></div>{#each [1,2,3] as row}<div class="feature"><div class="bar label"></div><div class="bar"></div><div class="bar short"></div></div>{/each}</div>
      <div><div class="bar heading"></div><div class="screens">{#each [1,2,3,4] as screen}<div class="image"></div>{/each}</div></div>
    </div>
    <div class="decisions"><div class="panel"><div class="bar heading"></div><div class="bar"></div><div class="signals">{#each [1,2,3,4] as signal}<div class="bar"></div>{/each}</div><div class="bar action"></div></div><div class="panel"><div class="bar heading"></div><div class="bar"></div><div class="bar short"></div></div></div>
  </div>
</div>

<style>
  .review-skeleton { display:grid; grid-template-columns:minmax(0,1fr) minmax(370px,.7fr); gap:40px; max-width:1400px; margin:auto; animation:review-pulse 1.5s ease-in-out infinite; }
  .material,.decisions { display:grid; align-content:start; gap:32px; min-width:0; }
  .intro,.panel { display:grid; gap:14px; }
  .intro { padding:4px 0; }
  .bar,.image { background:var(--color-edge); border-radius:5px; }
  .bar { height:14px; width:100%; }
  .title { height:32px; width:85%; margin-bottom:10px; }
  .short { width:65%; }
  .heading { height:22px; width:45%; margin-bottom:10px; }
  .label { height:18px; width:75%; }
  .panel { padding:24px; border:1px solid var(--color-edge); border-radius:16px; background:var(--color-panel); }
  .feature { display:grid; gap:12px; padding:16px 0; border-top:1px solid var(--color-edge); }
  .screens { display:grid; grid-template-columns:repeat(4,minmax(0,1fr)); gap:16px; margin-top:20px; }
  .image { aspect-ratio:16/10; border-radius:10px; }
  .signals { display:grid; gap:24px; padding:24px 0; }
  .action { height:32px; }
  @keyframes review-pulse { 0%,100% { opacity:.5; } 50% { opacity:.85; } }
  @media(max-width:1100px) { .review-skeleton { grid-template-columns:minmax(0,1fr) 330px; gap:32px; } }
  @media(max-width:800px) { .review-skeleton { grid-template-columns:minmax(0,1fr); max-width:620px; } .screens { grid-template-columns:repeat(2,minmax(0,1fr)); } }
  @media(prefers-reduced-motion:reduce) { .review-skeleton { animation:none; } }
</style>
