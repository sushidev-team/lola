<script lang="ts">
  import { tick } from "svelte";
  import { autocompletion, completionKeymap, completionStatus, closeCompletion, acceptCompletion } from "@codemirror/autocomplete";
  import { history, historyKeymap } from "@codemirror/commands";
  import { HighlightStyle, syntaxHighlighting } from "@codemirror/language";
  import { EditorView, keymap, placeholder, tooltips } from "@codemirror/view";
  import { tags } from "@lezer/highlight";
  import { completeDeclaration, inlineCSS, readDeclaration } from "$lib/previewcss";
  import Icon from "../Icon.svelte";
  import Button from "../Button.svelte";

  let { name = "", value = "", disabled = false, ontoggle, declarations, onapply, onreplaced, onstep, onfinish }: {
    disabled?: boolean; ontoggle?: (name: string, enabled: boolean) => Promise<string>;
    name?: string; value?: string; declarations: string[][];
    onapply: (name: string, value: string, originalName?: string) => Promise<string>;
    onfinish?: (restoreFocus: boolean) => void;
    onreplaced: (name: string) => void;
    onstep: (name: string, back: boolean) => boolean;
  } = $props();
  let editing = $state(false), draft = $state(""), styleError = $state("");
  let submitted = $state(false), completing = $state(false);
  let view: EditorView | undefined, baseline = "", pending: string | undefined;
  let actions = $state<HTMLDivElement>(), acting = $state(false);
  async function rowAction(remove: boolean) {
    if (acting) return;
    acting = true;
    try {
      styleError = remove ? await onapply(name, "", name) : await ontoggle!(name, disabled);
      if (remove && !styleError && document.activeElement === document.body) onreplaced("");
    } finally { acting = false; }
  }
  const parsed = $derived(readDeclaration(draft, declarations));
  const diagnostic = $derived(styleError || (draft.trim() && (submitted || (!completing && draft.includes(":"))) ? parsed.error : ""));

  function selectPart(v: EditorView, part: "name" | "value") {
    const text = v.state.doc.toString(), colon = text.indexOf(":");
    const start = colon + 1;
    const anchor = part === "name" ? 0 : start + (text.slice(start).match(/^\s*/)?.[0].length ?? 0);
    const head = part === "name" && colon >= 0 ? colon : text.length;
    v.dispatch({ selection: { anchor, head } });
  }

  function traverse(v: EditorView, back: boolean) {
    const text = v.state.doc.toString(), colon = text.indexOf(":");
    const inName = colon < 0 || v.state.selection.main.head <= colon;
    if (!back) acceptCompletion(v);
    if (back && !inName) { selectPart(v, "name"); return true; }
    if (!back && inName && v.state.doc.length) {
      if (!v.state.doc.toString().includes(":")) v.dispatch({ changes: { from: v.state.doc.length, insert: ": " } });
      selectPart(v, "value");
      return true;
    }
    if (!name && !back && !inName && !parsed.error) {
      void apply();
      return true;
    }
    // WebKit can skip buttons in its native tab order. Walk declaration rows
    // explicitly, then return control to the browser at the inspector boundary.
    return onstep(name, back);
  }

  function reset(v: EditorView) {
    draft = name ? name + ": " + value : "";
    baseline = draft;
    v.dispatch({ changes: { from: 0, to: v.state.doc.length, insert: draft } });
    styleError = ""; submitted = false;
    selectPart(v, name ? "value" : "name");
  }

  async function apply(enter = false) {
    const clear = name && enter && !draft.trim();
    if ((!draft.trim() && !clear) || (name && draft === baseline)) {
      if (name && !view?.hasFocus) editing = false;
      if (!name) onfinish?.(!!view?.hasFocus);
      return;
    }
    submitted = true;
    const change = clear ? { name, value: "", error: "", remove: true } : parsed;
    if (change.error || pending === draft) return;
    const text = draft;
    const hadFocus = view?.hasFocus;
    pending = text;
    const error = await onapply(change.name, change.value, name || undefined);
    if (pending !== text) return;
    pending = undefined;
    if (draft !== text) return;
    styleError = error;
    if (error) return;
    if (name && (change.name !== name || change.remove)) {
      await tick();
      if (hadFocus && document.activeElement === document.body) onreplaced(change.remove ? "" : change.name);
    } else if (name) {
      baseline = text;
      if (!view?.hasFocus) editing = false;
    } else onfinish?.(!!view?.dom.closest(".editor-row")?.contains(document.activeElement));
  }

  export async function focusPart(part: "name" | "value") {
    if (name && !editing) {
      baseline = draft = name + ": " + value;
      editing = true;
      await tick();
    }
    if (view) { view.focus(); selectPart(view, part); }
  }

  function mountEditor(host: HTMLElement) {
    view = new EditorView({
      parent: host, doc: draft, extensions: [
        inlineCSS, EditorView.lineWrapping, history(),
        syntaxHighlighting(HighlightStyle.define([
          { tag: [tags.propertyName, tags.variableName], color: "var(--color-info)" },
          { tag: [tags.number, tags.unit, tags.color], color: "var(--color-orange)" },
          { tag: [tags.string, tags.atom, tags.keyword], color: "var(--color-good)" },
        ])),
        placeholder("property: value"),
        EditorView.contentAttributes.of({ "aria-label": name ? "Edit " + name : "New CSS declaration", spellcheck: "false", autocapitalize: "off" }),
        autocompletion({ override: [completeDeclaration], defaultKeymap: false, interactionDelay: 0, icons: false, maxRenderedOptions: 24, tooltipClass: () => "preview-css-options" }),
        tooltips({ parent: document.body }),
        keymap.of([
          { key: "Escape", run: v => {
            closeCompletion(v);
            if (name) reset(v);
            else onfinish?.(true);
            return true;
          } },
          { key: "Alt-F10", run: () => {
            const button = actions?.querySelector("button");
            button?.focus();
            return !!button;
          } },
          ...completionKeymap,
          { key: "Tab", run: v => traverse(v, false) },
          { key: "Shift-Tab", run: v => traverse(v, true) },
          { key: "Enter", run: () => { void apply(true); return true; } },
          ...historyKeymap,
        ]),
        EditorView.updateListener.of(update => {
          completing = completionStatus(update.state) === "active";
          if (update.docChanged) {
            draft = update.state.doc.toString();
            styleError = "";
            submitted = false;
          }
        }),
      ],
    });
    const mounted = view;
    return { destroy() { mounted.destroy(); if (view === mounted) view = undefined; } };
  }
</script>

<div class="style-row" class:disabled class:editable={!!name} onfocusout={event => {
  if ((editing || !name) && !event.currentTarget.contains(event.relatedTarget as Node | null)) void apply();
}}>
{#if name && !editing}
  <div class="tokens">
    <button class="property" tabindex="0" aria-label={"Edit property " + name}
      onfocus={() => focusPart("name")} onclick={() => focusPart("name")}>{name}</button><span>{": "}</span><button
      class="value" tabindex="0" aria-label={"Edit value of " + name} onfocus={() => focusPart("value")} onclick={() => focusPart("value")}>{value}</button><span>;</span>
  </div>
{:else}
  <div class="editor-row" class:existing={!!name}>
    <div class="editor" class:invalid={!!diagnostic} use:mountEditor></div>
    {#if !name}<Button size="xs" variant="accent" disabled={!draft.trim() || !!parsed.error} onclick={() => void apply()}>{parsed.remove ? "Remove" : "Apply"}</Button>{/if}
  </div>
{/if}
{#if name}
  <div class="actions" bind:this={actions}>
    <Button size="xs" icon disabled={acting} aria-label={(disabled ? "Enable " : "Disable ") + name}
      title={(disabled ? "Enable " : "Disable ") + name} onmousedown={e => e.preventDefault()} onclick={() => void rowAction(false)}>
      <Icon name={disabled ? "circle-check" : "ban"} size={13}/>
    </Button>
    <Button size="xs" icon variant="danger" disabled={acting} aria-label={"Remove " + name}
      title={"Remove " + name} onmousedown={e => e.preventDefault()} onclick={() => void rowAction(true)}>
      <Icon name="trash-2" size={13}/>
    </Button>
  </div>
{/if}
{#if diagnostic}<p class="diagnostic" role="status">{diagnostic}</p>{/if}
</div>

<style>
  .style-row {position:relative}
  .editable {min-height:24px}
  .disabled .tokens,.disabled .tokens button {text-decoration:line-through;color:var(--color-faint)}
  .actions {position:absolute;inset-inline-end:0;top:0;display:flex;align-items:center;border-radius:4px;background:var(--color-panel);opacity:0;pointer-events:none}
  .editable:is(:hover,:focus-within) .actions {opacity:1;pointer-events:auto}
  .editable:focus-within .editor-row {margin-inline-end:50px}
  @media (hover:none) { .actions {opacity:1;pointer-events:auto} .editable {padding-inline-end:50px} .editable:focus-within .editor-row {margin-inline-end:0} }
  .tokens {padding:3px 6px 3px 12px;font-family:var(--font-mono);overflow-wrap:anywhere;color:var(--color-faint)}
  .tokens button {display:inline;text-align:left;white-space:pre-wrap;overflow-wrap:anywhere;cursor:text}
  .tokens button:hover {background:var(--color-sel)}
  .property {color:var(--color-info)}
  .value {color:var(--color-ink)}
  .editor-row.existing {margin:0 0 0 4px}
  .existing .editor :global(.cm-content) {padding:2px 0}
  .editor-row {display:flex;gap:6px;align-items:center;margin:5px 0 5px 6px;flex-wrap:wrap}
  .editor {font-family:var(--font-mono);min-width:0;flex:1 1 120px;background:var(--color-canvas);border:1px solid var(--color-edge);border-radius:5px}
  .editor:focus-within {border-color:var(--color-accent)}
  .editor.invalid {border-color:var(--color-warn)}
  .editor :global(.cm-focused) {outline:none}
  .editor :global(.cm-scroller) {font-family:inherit}
  .editor :global(.cm-content) {padding:4px 0;caret-color:var(--color-ink)}
  .editor :global(.cm-placeholder) {color:var(--color-placeholder)}
  .editor :global(.cm-line) {padding:0 7px}
  .diagnostic {color:var(--color-warn);padding:0 6px 5px}
  :global(.cm-tooltip.cm-tooltip-autocomplete.preview-css-options) {background:var(--color-panel);color:var(--color-ink);border:1px solid var(--color-edge);border-radius:5px;z-index:100}
  :global(.cm-tooltip.cm-tooltip-autocomplete.preview-css-options > ul) {font:var(--text-sm)/1.6 var(--font-mono);max-height:10rem}
  :global(.cm-tooltip.cm-tooltip-autocomplete.preview-css-options ul li[aria-selected]) {background:var(--color-accent-fill);color:var(--color-accent-ink)}
</style>
