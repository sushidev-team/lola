<script lang="ts">
  import { tick } from "svelte";
  import { connectPreviewTools, type ConsoleLine, type DebugCommand, type DebugConnection, type DebugTarget, type ElementInfo, type TreeRow } from "$lib/previewtools";
  import PreviewSplitter from "./PreviewSplitter.svelte";
  import Button from "../Button.svelte";
  import Icon from "../Icon.svelte";
  let { id, target, external, close }: { id: number; target?: DebugTarget | null; external: boolean; close: () => void } = $props();
  let panel = $state<"Console" | "Elements">("Console");
  let ready = $state(false), picking = $state(false), error = $state("");
  let dimensions = $state(""), expression = $state("");
  let lines = $state.raw<ConsoleLine[]>([]), element = $state.raw<ElementInfo | null>(null);
  let inspectorTab = $state<"Styles" | "Computed" | "Attributes">("Computed");
  let styleQuery = $state(""), tree = $state.raw<TreeRow[]>([]), treeBox = $state<HTMLElement>();
  const htmlVoidTags = new Set(["area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta", "param", "source", "track", "wbr"]);
  function hasEndTag(row: TreeRow) {
    return row.offset === undefined && (row.foreign || !htmlVoidTags.has(row.name));
  }
  // End tags are presentation only. Keep paging, selection and arrow-key
  // navigation indexed by the actual element rows from the inspected page.
  const closingAfter = $derived.by(() => {
    const open: TreeRow[] = [];
    return tree.map((row, i) => {
      if (row.expanded && hasEndTag(row)) open.push(row);
      const nextDepth = tree[i + 1]?.depth ?? 0;
      const closing: TreeRow[] = [];
      while (open.length && open[open.length - 1].depth >= nextDepth) closing.push(open.pop()!);
      return closing;
    });
  });
  let inspectorWidth = $state(0), inspectorHeight = $state(0);
  let rowRatio = $state(.5), columnRatio = $state(.535);
  const wide = $derived(inspectorWidth >= 660);
  const available = $derived(Math.max(0, (wide ? inspectorWidth : inspectorHeight) - 12));
  const minTree = $derived(Math.min(Math.floor(available / 2), wide ? 150 : 80));
  const treeSize = $derived(Math.round(Math.max(minTree, Math.min(available - minTree, available * (wide ? columnRatio : rowRatio)))));
  function resizeTree(size: number) {
    if (!available) return;
    if (wide) columnRatio = size / available;
    else rowRatio = size / available;
  }
  const wrapKey = "lola.preview.wrapDOM";
  let wrap = $state(false), menu = $state(false), menuBox = $state<HTMLElement>();
  let menuItem = $state<HTMLButtonElement>();
  $effect(() => { try { wrap = localStorage.getItem(wrapKey) === "true"; } catch {} });
  async function openMenu() { menu = true; await tick(); menuItem?.focus(); }
  function closeMenu() { menu = false; menuBox?.querySelector("button")?.focus(); }
  function toggleWrap() {
    wrap = !wrap;
    try { localStorage.setItem(wrapKey, String(wrap)); } catch {}
    closeMenu();
  }
  function dismissMenu(e: Event) {
    if (menu && !menuBox?.contains(e.target as Node)) menu = false;
  }
  function menuKey(e: KeyboardEvent) {
    if (e.key === "Escape" && menu) { e.preventDefault(); e.stopPropagation(); closeMenu(); }
    else if (["ArrowDown", "ArrowUp", "Home", "End"].includes(e.key)) { e.preventDefault(); e.stopPropagation(); void openMenu(); }
  }
  const filteredStyles = $derived((element?.css ?? []).filter(([key, val]) =>
    !styleQuery || (key + " " + val).toLowerCase().includes(styleQuery.toLowerCase())));
  let treeScoped = $state(false);
  let treeVersion = -1;
  let output = $state<HTMLElement>();
  let connection: DebugConnection | undefined, revision = -1, after = 0;
  let documentKey = $state("");
  let queue = Promise.resolve();

  function run(cmd: DebugCommand) {
    const owner = connection, requestedDocument = documentKey;
    if (!owner) return;
    const response = queue.then(async () => {
      if (owner !== connection) return;
      const snapshot = await owner.run({ documentKey: cmd.op === "read" ? documentKey : requestedDocument, ...cmd, revision: cmd.revision ?? revision, after, treeVersion, computed: inspectorTab === "Computed", styles: inspectorTab === "Styles", elements: panel === "Elements" });
      if (!snapshot || owner !== connection) return;
      if (documentKey !== snapshot.documentKey) { documentKey = snapshot.documentKey; lines = []; element = null; tree = []; treeScoped = false; treeVersion = -1; revision = -1; after = 0; }
      error = "";
      dimensions = Math.round(snapshot.width) + " × " + Math.round(snapshot.height);
      picking = snapshot.picking;
      if (cmd.op === "clear") lines = [];
      if (snapshot.lines.length) {
        const keepBottom = !output || output.scrollHeight - output.clientHeight - output.scrollTop < 20;
        const batch = snapshot.lines.filter(line => {
          if (line.id <= after) return false;
          after = line.id;
          return true;
        });
        if (batch.length) {
          lines = [...lines, ...batch].slice(-150);
          if (keepBottom) void tick().then(() => output?.scrollTo?.(0, output.scrollHeight));
        }
      }
      if (snapshot.tree) {
        const previous = tree.find(n => n.selected)?.id;
        tree = snapshot.tree;
        treeScoped = !!snapshot.treeScoped;
        treeVersion = snapshot.treeVersion ?? -1;
        if (tree.find(n => n.selected)?.id !== previous) {

          void tick().then(() => treeBox?.querySelector('[aria-selected="true"]')?.scrollIntoView?.({ block: "nearest", inline: "nearest" }));
        }
      }
      if (snapshot.element) {
        element = snapshot.element;
        revision = snapshot.revision;
      }
      return snapshot;
    }).catch(err => { if (owner === connection) error = String(err); });
    queue = response.then(() => {});
    return response;
  }
  $effect(() => {
    const tab = id, frame = target, native = external;
    ready = false; picking = false; error = ""; dimensions = ""; lines = []; element = null;
    expression = ""; documentKey = ""; tree = []; treeScoped = false; treeVersion = -1; styleQuery = ""; revision = -1; after = 0; queue = Promise.resolve();
    if (!native && !frame) return;
    let owner: DebugConnection, stopped = false, timer: ReturnType<typeof setTimeout>;
    // Local frames signal changes. The separate native webview exposes only a
    // pull channel, with at most one read in flight while its tools are open.
    async function read() {
      if (stopped) return;
      await run({ op: "read" });
      if (native && !stopped) timer = setTimeout(read, 400);
    }
    try { owner = connectPreviewTools(tab, frame ?? null, () => { if (!stopped) void read(); }); }
    catch (err) { error = String(err); return; }
    connection = owner;
    owner.ready.then(() => { if (!stopped) { ready = true; void read(); } },
      err => { if (!stopped) error = String(err); });
    return () => { stopped = true; clearTimeout(timer); connection = undefined; owner.close(); };
  });
  // Bind edits to the element and document that supplied the row. A late
  // blur from an outgoing inspector must never edit the incoming selection.
  function editStyle(index: number | undefined, page: string) {
    return async (name: string, value: string, originalName?: string, ruleId?: number, enabled?: boolean) => {
      const op = enabled !== undefined ? "toggleStyle" : ruleId === undefined ? "style" : "ruleStyle";
      const snapshot = await run({ op, on: enabled, name, value, originalName, ruleId, index, documentKey: page });
      return snapshot ? snapshot.editError ?? "" : "Couldn’t apply this declaration.";
    };
  }
  function switchPanel(next: typeof panel) {
    if (next === "Console" && picking) run({ op: "pick", on: false });
    menu = false; panel = next;
    if (ready) run({ op: "read" });
  }
  function pick() { panel = "Elements"; run({ op: "pick", on: !picking }); }
  function showDetail(name: typeof inspectorTab) {
    inspectorTab = name;
    run({ op: "read", revision: -1 });
  }
  function treeAction(node: TreeRow, branch = false) {
    if (node.offset !== undefined || (branch && node.count)) {
      return run({ op: "branch", index: node.id, on: node.offset !== undefined || !node.expanded, scoped: node.limited, offset: node.offset });
    }
    return run({ op: "select", index: node.id });
  }
  function treeKey(e: KeyboardEvent, node: TreeRow, i: number) {
    const buttons = treeBox?.querySelectorAll<HTMLButtonElement>('[role="treeitem"]');
    if (!buttons) return;
    let next = i;
    if (e.key === "ArrowDown") next++;
    else if (e.key === "ArrowUp") next--;
    else if (e.key === "Home") next = 0;
    else if (e.key === "End") next = tree.length - 1;
    else if (e.key === "ArrowRight" && node.count) {
      if (node.expanded) next++; else treeAction(node, true);
    } else if (e.key === "ArrowLeft") {
      if (node.expanded) treeAction(node, true);
      else for (let p = i - 1; p >= 0; p--) { if (tree[p].depth < node.depth) { next = p; break; } }
    } else return;
    e.preventDefault(); e.stopPropagation();
    if (buttons[next] && next !== i) {
      buttons[next].focus();
      if (tree[next].offset === undefined) treeAction(tree[next]);
    }
  }
  function evaluate(e: SubmitEvent) {
    e.preventDefault();
    if (ready && expression.trim()) { run({ op: "eval", text: expression }); expression = ""; }
  }
</script>

<svelte:window onpointerdown={dismissMenu} onfocusin={dismissMenu}/>

<aside class="tools" aria-label="Developer tools">
  <header>
    <Button icon size="sm" aria-label="Pick an element to debug" aria-pressed={picking} selected={picking} disabled={!ready} title="Pick an element" onclick={pick}><Icon name="square-mouse-pointer" size={15}/></Button>
    {#each ["Console", "Elements"] as name}
      <button class="section" class:current={panel === name} aria-pressed={panel === name} onclick={() => switchPanel(name as typeof panel)}>{name}</button>
    {/each}
    <Button icon size="sm" class="ml-auto" aria-label="Close developer tools" title="Close developer tools · ⌘⌥I" onclick={close}><Icon name="x" size={14}/></Button>
  </header>
  <div class="subbar"><span class="font-mono" aria-label="Page viewport">{dimensions ? dimensions + " px" : "Connecting…"}</span>
    {#if panel === "Console"}<Button size="xs" disabled={!lines.length} onclick={() => run({op:"clear"})}>Clear</Button>
    {:else}
      <!-- WebKit focuses the enclosing dialog on mouse-down. Keep focus here
           until the click runs, or dismissMenu removes the item before it toggles. -->
      <!-- svelte-ignore a11y_no_static_element_interactions -->
      <div class="options" bind:this={menuBox} onkeydown={menuKey}
        onmousedown={e => { if (e.button === 0) e.preventDefault(); }}>
        <Button icon size="xs" aria-label="Elements options" title="Elements options" aria-haspopup="menu" aria-expanded={menu}
          onclick={() => menu ? closeMenu() : openMenu()}><Icon name="ellipsis-vertical" size={15}/></Button>
        {#if menu}<div class="options-menu" role="menu" aria-label="Elements options">
          <button bind:this={menuItem} role="menuitemcheckbox" aria-checked={wrap} onclick={toggleWrap}>
            <span>{#if wrap}<Icon name="check" size={14}/>{/if}</span>Wrap DOM lines
          </button>
        </div>{/if}
      </div>
    {/if}
  </div>
  {#if error}<p class="px-3 py-2 text-warn" role="status">{error}</p>{/if}
  {#if panel === "Console"}
    <div class="scroll" bind:this={output} aria-label="Console output">
      {#if !lines.length && ready}<p class="p-3 text-faint">No console messages yet.</p>{/if}
      {#each lines as line (line.id)}
        <div class="line" class:warn={line.level === "warn"} class:bad={line.level === "error"}><span>{line.level}</span><code>{line.text}</code></div>
      {/each}
    </div>
    <form class="prompt" onsubmit={evaluate}><span aria-hidden="true">›</span><input aria-label="Console expression" placeholder="JavaScript expression" bind:value={expression} disabled={!ready} autocomplete="off" spellcheck="false" maxlength="8192"/></form>
  {:else}
    {#if picking}<p class="px-3 py-1 text-accent-ink">Click an element in the preview.</p>{/if}
    <div class="inspector" class:wide bind:clientWidth={inspectorWidth} bind:clientHeight={inspectorHeight}
      style:--tree-size={available ? treeSize + "px" : "minmax(0, 1fr)"}>
      <div class="tree-scroll" class:wrap bind:this={treeBox} role="tree" aria-label="DOM tree">
        {#if treeScoped}<Button size="xs" onclick={() => run({op:"treeBack"})}><Icon name="chevron-left" size={12}/>Back to parent tree</Button>{/if}
        {#each tree as node, i}
          <button type="button" role="treeitem" aria-level={node.depth + 1} aria-selected={node.selected}
            aria-expanded={node.count ? node.expanded : undefined} tabindex={node.selected || (!i && !tree.some(n => n.selected)) ? 0 : -1}
            class="tree-row" class:chosen={node.selected} style:--depth={node.depth}
            onkeydown={e => treeKey(e, node, i)} onclick={e => treeAction(node, e.target instanceof Element && !!e.target.closest('[data-branch]'))}>
            <span class="guides"></span>
            {#if node.offset !== undefined}<span class="text-faint">… {node.name}</span>
            {:else}
              <span data-branch class="disclosure">{#if node.count}<Icon name={node.expanded ? "chevron-down" : "chevron-right"} size={12}/>{/if}</span>
              <span class="markup"><span class="tag">&lt;{node.name}</span>{#each node.attrs as [key, val]}<span class="attr">{" " + key}</span>=<span class="val">"{val}"</span>{/each}<span class="tag">&gt;</span>{#if !node.expanded}
                {#if node.count}<span data-branch>…</span>{:else}{node.text}{/if}{#if hasEndTag(node)}<span class="tag">&lt;/{node.name}&gt;</span>{/if}
                {#if node.count}<span data-branch class="children">{node.count} {node.count === 1 ? "child" : "children"}</span>{/if}
              {/if}</span>
            {/if}
          </button>
          {#each closingAfter[i] as parent}
            <div class="tree-row end-tag" aria-hidden="true" style:--depth={parent.depth}>
              <span class="guides"></span><span class="disclosure"></span><span class="tag">&lt;/{parent.name}&gt;</span>
            </div>
          {/each}
        {/each}
      </div>
      <PreviewSplitter label="Resize DOM tree and element details" orientation={wide ? "vertical" : "horizontal"}
        min={minTree} max={available - minTree} value={treeSize} onresize={resizeTree}
        onreset={() => { if (wide) columnRatio = .535; else rowRatio = .5; }}/>
      <div class="detail">
        <nav class="detail-tabs" aria-label="Element details">
          {#each ["Styles", "Computed", "Attributes"] as name}
            <button class="section" class:current={inspectorTab === name} aria-pressed={inspectorTab === name}
              onclick={() => showDetail(name as typeof inspectorTab)}>{name}</button>
          {/each}
        </nav>
        {#if element}
          {#if inspectorTab === "Computed"}
            <input class="filter" type="search" placeholder="Filter computed styles" aria-label="Filter computed styles" bind:value={styleQuery}/>
            <div class="scroll"><div class="size-readout">{element.width} × {element.height} px</div>
              {#each filteredStyles as [key,val]}<div class="pair"><b>{key}</b><code>{val}</code></div>{:else}<p class="p-3 text-faint">No matching properties.</p>{/each}
            </div>
          {:else if inspectorTab === "Styles"}
            {#await import("./PreviewStyles.svelte")}
              <p class="flex items-center gap-2 p-3 text-faint" role="status"><Icon name="loader-circle" size={14} class="text-accent motion-safe:animate-spin"/>Loading CSS editor…</p>
            {:then { default: Styles }}
              {#key documentKey + "/" + tree.find(n => n.selected)?.id}
                <Styles {element} oncomputed={() => showDetail("Computed")}
                  onapply={editStyle(tree.find(n => n.selected)?.id, documentKey)}/>
              {/key}
            {:catch}
              <p class="p-3 text-warn" role="alert">Couldn’t load the CSS editor. Reopen Styles to retry.</p>
            {/await}
          {:else}
            <div class="scroll">{#each element.attrs as [key,val]}<div class="pair"><b>{key}</b><code>{val}</code></div>{:else}<p class="p-3 text-faint">No attributes.</p>{/each}</div>
          {/if}
        {/if}
      </div>
    </div>
  {/if}
</aside>

<style>
  .tools{height:100%;min-width:0;display:flex;flex-direction:column;background:var(--color-panel);color:var(--color-ink);font-size:var(--text-sm)}
  header,.subbar,.prompt{display:flex;align-items:center;gap:6px;padding:5px 8px;border-bottom:1px solid var(--color-edge);flex:none}
  .section{height:30px;padding:0 8px;color:var(--color-faint);border-bottom:2px solid transparent;cursor:pointer}
  .section.current{color:var(--color-ink);border-color:var(--color-accent)}
  .subbar{display:flex;justify-content:space-between;align-items:center;color:var(--color-faint)}
  .scroll{min-height:0;overflow:auto;flex:1}.line,.pair{display:flex;gap:8px;padding:4px 10px;overflow-wrap:anywhere}.line{border-bottom:1px solid var(--color-edge)}
  .line span,.pair b{font-size:var(--text-xs);color:var(--color-faint)} .line span{min-width:36px}
  .warn,.warn span{color:var(--color-warn)}.bad,.bad span{color:var(--color-bad)}code{white-space:pre-wrap;overflow-wrap:anywhere}
  .prompt{border-top:1px solid var(--color-edge);border-bottom:0;color:var(--color-accent-ink);font-family:var(--font-mono)}
  input{width:100%;min-width:0;padding:5px;color:var(--color-ink);font-family:var(--font-mono)}
  .inspector {display:grid;grid-template:var(--tree-size) 12px minmax(0,1fr) / minmax(0,1fr);min-height:0;flex:1}
  .inspector.wide {grid-template:minmax(0,1fr) / var(--tree-size) 12px minmax(0,1fr)}
  .tree-scroll {overflow:auto;min-width:0;padding-block:4px}
  .detail {display:flex;flex-direction:column;min-width:0;min-height:0}
  .tree-scroll.wrap .tree-row {width:100%;height:auto;min-height:23px;align-items:baseline}
  .wrap .markup {flex:1;min-width:0;white-space:pre-wrap;overflow-wrap:anywhere}
  .wrap .guides {flex:none;max-width:35%}
  .wrap .disclosure {flex:none}
  .options {position:relative}
  .options-menu {position:absolute;right:0;top:100%;z-index:2;padding:4px;background:var(--color-panel);border:1px solid var(--color-edge);border-radius:6px;box-shadow:0 4px 12px #0003}
  .options-menu button {display:flex;align-items:center;gap:6px;white-space:nowrap;padding:5px 8px;border-radius:4px;color:var(--color-ink)}
  .options-menu button:is(:focus-visible,:hover) {background:var(--color-sel)}
  .options-menu button span {width:14px}
  .tree-row {display:flex;align-items:center;width:max-content;min-width:100%;height:23px;font-family:var(--font-mono);text-align:left;padding:0 8px 0 4px;white-space:pre;cursor:pointer;color:var(--color-ink-2)}
  .tree-row.end-tag {cursor:default}
  button.tree-row:hover {background:var(--color-sel)}
  .tree-row.chosen {background:var(--color-accent-fill)}
  .guides {align-self:stretch;width:calc(var(--depth) * 13px);background:repeating-linear-gradient(90deg,transparent 0 6px,var(--color-edge) 6px 7px,transparent 7px 13px)}
  .disclosure {width:16px;display:inline-grid;place-items:center;align-self:stretch;color:var(--color-faint)}
  .children {margin-inline-start:6px;color:var(--color-faint);font-family:var(--font-sans)}
  .children:hover {text-decoration:underline}
  .tag {color:var(--color-accent-ink)} .attr {color:var(--color-info)} .val {color:var(--color-good)}
  .detail-tabs {display:flex;gap:2px;border-bottom:1px solid var(--color-edge)}
  .filter {border-bottom:1px solid var(--color-edge);padding:6px 10px}
  .size-readout {padding:4px 10px;color:var(--color-faint);font-family:var(--font-mono)}
  .pair {display:grid;grid-template-columns:minmax(0,42%) minmax(0,1fr);align-items:start}
  .pair b{min-width:0;font-weight:400}
</style>
