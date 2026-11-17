<script lang="ts" module>
  export type AIModel = { model:string; effort:string };
  export type AIProvider = { enabled:boolean; profiles:Record<string,AIModel|undefined>|null; actions:Record<string,string|undefined>|null };
  export type AISettingsValue = Record<string,AIProvider|undefined>|null;
</script>

<script lang="ts">
  import { untrack } from 'svelte';
  import Select from './Select.svelte';
  import Checkbox from './Checkbox.svelte';
  import PresetInput from './PresetInput.svelte';
  import SettingsGroup from './SettingsGroup.svelte';
  import { modelsFor } from '$lib/settingPresets';
  let { value = $bindable(), defaultProvider = 'claude' }: {value?:AISettingsValue;defaultProvider?:string} = $props();
  let provider = $state(untrack(() => defaultProvider || 'claude'));
  const providers = [{value:'claude',label:'Claude Code'},{value:'codex',label:'Codex'},{value:'opencode',label:'OpenCode'}];
  const profiles = [{id:'fast',label:'Fast',description:'Short, routine tasks'},{id:'balanced',label:'Balanced',description:'Everyday analysis'},{id:'thorough',label:'Thorough',description:'Complex coding and reasoning'}];
  const actions = [{id:'draft',label:'Commit and PR drafts',profile:'fast'},{id:'resolve',label:'Conflict resolution',profile:'thorough'},{id:'summary',label:'Session summaries',profile:'balanced'},{id:'plan',label:'Planning',profile:'thorough'}];
  function defaults() {
    return {enabled:false,profiles:Object.fromEntries(profiles.map(p=>[p.id,{model:provider==='claude'?({fast:'haiku',balanced:'sonnet',thorough:'opus'}[p.id]??''):'',effort:''}])),actions:Object.fromEntries(actions.map(a=>[a.id,a.profile]))};
  }
  const current = $derived.by(() => {
    const d=defaults(),saved=value?.[provider];
    return {enabled:saved?.enabled ?? d.enabled,
      profiles:Object.fromEntries(profiles.map(p=>[p.id,saved?.profiles?.[p.id] ?? d.profiles[p.id]])),
      actions:Object.fromEntries(actions.map(a=>[a.id,saved?.actions?.[a.id] ?? d.actions[a.id]]))};
  });
  const providerName = $derived(providers.find(p=>p.value===provider)?.label ?? provider);
  const efforts = $derived(['',...(provider==='claude'?['low','medium','high','max']:provider==='codex'?['minimal','low','medium','high','xhigh']:['low','medium','high','max'])].map(v=>({value:v,label:v || 'Model default'})));
  function update(next:Partial<AIProvider>) { value={...value,[provider]:{...current,...next}}; }
  function model(id:string,key:keyof AIModel,next:string) { update({profiles:{...current.profiles,[id]:{...current.profiles[id],[key]:next}}}); }
</script>

<div class="provider-context">
  <label for="ai-provider-context">Provider</label>
  <Select id="ai-provider-context" aria-label="AI provider" bind:value={provider}>{#each providers as p}<option value={p.value}>{p.label}</option>{/each}</Select>
  <p>Configure each provider separately. Switching this view keeps your edits.</p>
</div>
<SettingsGroup title={`${providerName} profiles`}>
  <label class="enable-profiles"><Checkbox checked={current.enabled} onchange={event=>update({enabled:event.currentTarget.checked})}/><span>Use these profiles for {providerName}</span></label>
  {#if !current.enabled}<p class="profile-hint">Existing action settings stay in use until enabled.</p>{/if}
  <div class="profile-list">
    {#each profiles as profile (provider + ':' + profile.id)}
      <div class="profile-row">
        <div><strong>{profile.label}</strong><p>{profile.description}</p></div>
        <div><span class="field-label">Model</span><PresetInput label={`${profile.label} model`} value={current.profiles[profile.id].model} options={modelsFor(provider)} onChange={next=>model(profile.id,'model',next)} placeholder={provider==='opencode'?'provider/model':'Model ID or alias'}/></div>
        <div><span class="field-label">{provider==='opencode'?'Variant':'Effort'}</span><PresetInput label={`${profile.label} effort`} value={current.profiles[profile.id].effort} options={efforts} onChange={next=>model(profile.id,'effort',next)} placeholder={provider==='opencode'?'Provider variant':'Reasoning effort'}/></div>
      </div>
    {/each}
  </div>
</SettingsGroup>
<SettingsGroup title="Actions" description="Choose which profile each action uses.">
  {#each actions as action}
    <label class="action-profile"><span>{action.label}</span><Select aria-label={`${action.label} profile`} value={current.actions[action.id]} onchange={event=>update({actions:{...current.actions,[action.id]:event.currentTarget.value}})}>{#each profiles as profile}<option value={profile.id}>{profile.label}</option>{/each}</Select></label>
  {/each}
  <p class="profile-hint">Drafts and new resolver sessions follow the project’s agent. Summaries and planning follow the workspace’s default agent. Existing sessions keep their current model.</p>
</SettingsGroup>

<style>
  .provider-context { display:grid; grid-template-columns:100px minmax(0,1fr); align-items:center; gap:8px 16px; margin-bottom:24px; }
  .provider-context p { grid-column:2; color:var(--color-ink-2); font-size:13px; }
  .enable-profiles { display:flex; align-items:center; gap:8px; cursor:pointer; }
  .profile-hint,.profile-row p { color:var(--color-ink-2); font-size:13px; line-height:1.5; }
  .profile-list { display:grid; gap:18px; }
  .profile-row { display:grid; grid-template-columns:minmax(100px,.7fr) minmax(140px,1.3fr) minmax(110px,.8fr); align-items:start; gap:16px; }
  .profile-row strong { font-weight:500; }
  .field-label { display:block; color:var(--color-ink-2); font-size:13px; margin-bottom:6px; }
  .action-profile { display:grid; grid-template-columns:minmax(0,1fr) minmax(120px,180px); align-items:center; gap:16px; }
  @media(max-width:700px) { .profile-row { grid-template-columns:minmax(0,1fr) minmax(0,1fr); } .profile-row > div:first-child { grid-column:1/-1; } }
</style>
