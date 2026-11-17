<script lang="ts">
  let {value = $bindable(), inherited}: {value?:string|null; inherited?:string} = $props();
  const id = $props.id();
</script>

<div class="commit-instructions">
  <label for={id}>Commit instructions</label>
  <textarea id={id} rows="4" maxlength="4000" value={value ?? ''}
    aria-describedby={`${id}-hint`}
    placeholder={inherited || 'Follow repository history. For example: use Conventional Commits, keep subjects under 72 characters, and explain why in the body.'}
    oninput={event => value = event.currentTarget.value}></textarea>
  <p id={`${id}-hint`}>{inherited === undefined ? 'Used by AI commit and staged-message drafts. Blank follows repository history.' : 'Overrides workspace instructions for this repository. Blank uses the workspace default, then repository history.'}</p>
</div>

<style>
  .commit-instructions { display:grid; gap:8px; }
  label { font-size:13px; color:var(--color-ink-2); }
  textarea { width:100%; resize:vertical; min-height:96px; padding:10px 12px; border:1px solid var(--color-edge); border-radius:6px; color:var(--color-ink); background:var(--color-canvas); font:inherit; font-size:13px; line-height:1.5; }
  textarea::placeholder { color:var(--color-faint); }
  textarea:focus-visible { outline:2px solid var(--color-accent); outline-offset:2px; }
  p { color:var(--color-ink-2); font-size:12px; line-height:1.5; }
</style>
