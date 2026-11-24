// Runs in the inspected document only. The UI asks for bounded deltas while
// visible. No document-wide mutation observer or shadow copy of the DOM.
function lolaPreviewDebugger(changed = () => {}) {
  const documentKey = Math.random().toString(36).slice(2);
  let selected = document.documentElement, revision = 1, sequence = 0;
  let lines = [], picking = false, box, hover, paint = 0;
  const cleanup = [];
  const clip = (s, n = 1024) => String(s).slice(0, n);
  const tag = el => el ? clip(el.localName + (el.id ? "#" + el.id : "") +
    Array.from(el.classList).slice(0, 3).map(c => "." + c).join(""), 150) : "";
  function describe(value) {
    if (value instanceof Error) return clip(String(value) + (value.stack ? "\n" + value.stack : ""));
    if (value instanceof Element) return "<" + tag(value) + ">";
    if (typeof value !== "object" || value === null) return clip(value);
    // Avoid traversing application objects or invoking getters on its log path.
    const parts = [];
    for (const key in value) {
      if (!Object.hasOwn(value, key)) continue;
      const d = Object.getOwnPropertyDescriptor(value, key);
      parts.push(key + ": " + (!d || !("value" in d) ? "[Getter]" :
        typeof d.value === "object" && d.value !== null ? "[…]" : clip(d.value, 120)));
      if (parts.length === 8) { parts.push("…"); break; }
    }
    return clip((Array.isArray(value) ? "[" : "{") + parts.join(", ") + (Array.isArray(value) ? "]" : "}"));
  }
  function record(level, args) {
    try {
      lines.push({ id: ++sequence, level, text: clip(args.slice(0, 12).map(describe).join(" "), 1800) });
      if (lines.length > 150) lines.shift();
      changed();
    } catch { /* A hostile proxy or custom formatter must not break console.log. */ }
  }
  function listen(target, event, handler, capture = false) {
    target.addEventListener(event, handler, capture);
    cleanup.push(() => target.removeEventListener(event, handler, capture));
  }
  for (const level of ["log", "info", "debug", "warn", "error"]) {
    const original = console[level];
    if (typeof original !== "function") continue;
    const wrapped = function (...args) { record(level, args); return original.apply(this, args); };
    console[level] = wrapped;
    cleanup.push(() => { if (console[level] === wrapped) console[level] = original; });
  }
  let resizeTimer = 0;
  listen(window, "resize", () => {
    changed(); // The viewport readout stays live. Recompute styles after dragging.
    clearTimeout(resizeTimer);
    resizeTimer = setTimeout(() => { revision++; changed(); }, 160);
  });
  cleanup.push(() => clearTimeout(resizeTimer));
  listen(window, "error", e => record("error", [e.error || e.message]));
  listen(window, "unhandledrejection", e => record("error", [e.reason]));

  function highlight(el) {
    if (!el || !el.isConnected) { box?.remove(); return; }
    if (!box) {
      box = document.createElement("div");
      box.style.cssText = "position:fixed;pointer-events:none;z-index:2147483647;border:2px solid #3487ef;background:#3487ef22;box-sizing:border-box";
    }
    if (!box.isConnected) document.documentElement.appendChild(box);
    const r = el.getBoundingClientRect();
    Object.assign(box.style, { left:r.x+"px", top:r.y+"px", width:r.width+"px", height:r.height+"px" });
  }
  function move(e) {
    hover = e.composedPath()[0];
    if (!paint) paint = requestAnimationFrame(() => { paint = 0; highlight(hover instanceof Element ? hover : null); });
  }
  function swallow(e) { e.preventDefault(); e.stopImmediatePropagation(); }
  function choose(e) {
    swallow(e);
    const el = e.composedPath()[0];
    if (el instanceof Element) { select(el); reveal(el); }
    pick(false);
  }
  function escape(e) { if (e.key === "Escape") { swallow(e); pick(false); } }
  function pick(on) {
    if (picking === on) return;
    picking = on;
    changed();
    const method = on ? "addEventListener" : "removeEventListener";
    window[method]("pointermove", move, true);
    for (const event of ["pointerdown","pointerup","mousedown","mouseup"]) window[method](event, swallow, true);
    window[method]("click", choose, true);
    window[method]("keydown", escape, true);
    if (!on) { cancelAnimationFrame(paint); paint = 0; highlight(null); }
  }
  // Keep identities weak and retain actual elements only for the visible rows.
  const ids = new WeakMap(), visible = new Map();
  let nextId = 0, branches = new WeakMap(), treeVersion = 1;
  const pageSize = 24;
  const parentOf = el => el.parentElement || el.getRootNode().host;
  const childCount = el => el.childElementCount + (el.shadowRoot?.childElementCount || 0);
  function childAt(el, i) {
    const shadow = el.shadowRoot?.children;
    return shadow && i < shadow.length ? shadow[i] : el.children[i - (shadow?.length || 0)];
  }
  function reveal(el) {
    branches = new WeakMap();
    for (let child = el, parent = parentOf(child); parent; child = parent, parent = parentOf(child)) {
      let index = 0;
      for (let prev = child.previousElementSibling; prev; prev = prev.previousElementSibling) index++;
      if (child.parentElement === parent) index += parent.shadowRoot?.childElementCount || 0;
      branches.set(parent, index - index % pageSize);
    }
    treeVersion++;
  }
  if (document.documentElement) branches.set(document.documentElement, 0);
  let observingTree = false;
  const observer = new MutationObserver(records => {
    const updates = records.filter(r => r.target !== box && (r.type !== "childList" ||
      [...r.addedNodes, ...r.removedNodes].some(n => n !== box)));
    if (!updates.length) return;
    treeVersion++;
    revision++;
    changed();
  });
  // CSSOM edits and changes to inherited styles can happen without any mutation
  // on a visible node. Recheck the open styles pane at rest, sending only changes.
  let styleTimer = 0, auditDue = false, lastDetails = "";
  function scheduleAudit(active) {
    clearTimeout(styleTimer);
    styleTimer = active ? setTimeout(() => { auditDue = true; changed(); }, 2400) : 0;
  }
  cleanup.push(() => { observer.disconnect(); clearTimeout(styleTimer); });
  function tree() {
    observer.disconnect();
    const rows = [];
    visible.clear();
    function visit(el, depth) {
      if (!el || el === box || rows.length >= 400) return;
      if (!ids.has(el)) ids.set(el, ++nextId);
      const id = ids.get(el), count = childCount(el);
      visible.set(id, el);
      const expanded = count > 0 && branches.has(el);
      const attrs = [];
      for (const key of ["id", "class", "name", "type", "role", "href", "src"]) {
        if (el.hasAttribute(key)) attrs.push([key, clip(el.getAttribute(key), 2048)]);
        if (attrs.length >= 2) break;
      }
      const text = !count && !["script", "style"].includes(el.localName) && el.firstChild?.nodeType === 3
        ? clip(el.firstChild.data, 80).replace(/\r\n|[\r\n]/g, " ") : "";
      rows.push({ id, depth, name: el.localName, attrs, text, count, expanded, selected: el === selected });
      observer.observe(el, { childList: true, attributes: true });
      if (!count && el.firstChild?.nodeType === 3) observer.observe(el.firstChild, { characterData: true });
      if (expanded && el.shadowRoot) observer.observe(el.shadowRoot, { childList: true });
      if (!expanded) return;
      const start = Math.min(branches.get(el), Math.floor((count - 1) / pageSize) * pageSize);
      const end = Math.min(count, start + pageSize);
      function page(offset, label) { rows.push({ id, depth: depth + 1, offset, name: label }); }
      if (start) page(start - pageSize, "Previous " + Math.min(start, pageSize) + " elements");
      for (let i = start; i < end && rows.length < 400; i++) visit(childAt(el, i), depth + 1);
      if (end < count) page(end, "Next " + Math.min(count - end, pageSize) + " elements");
    }
    visit(document.documentElement, 0);
    return rows;
  }
  function select(el) {
    if (el && el.isConnected && el !== selected) { selected = el; treeVersion++; revision++; }
  }
  function declarations(style) {
    return Array.from(style || [], key => [key, clip(style.getPropertyValue(key), 2048) +
      (style.getPropertyPriority(key) ? " !important" : "")]);
  }
  function matchingRules(el) {
    const root = el.getRootNode(), rules = [], visited = new Set();
    let unreadable = 0, truncated = false;
    function sheet(s, context = "") {
      if (!s || visited.has(s) || s.disabled || (s.media?.mediaText && !matchMedia(s.media.mediaText).matches)) return;
      visited.add(s);
      let list;
      try { list = s.cssRules; } catch { unreadable++; return; }
      const source = s.href || s.ownerNode?.getAttribute("data-vite-dev-id") || "<style>";
      walk(list, source, context, "");
    }
    function walk(list, source, context, parent) {
      for (const rule of list) {
        if (rules.length >= 120) { truncated = true; return; }
        if (rule.media?.mediaText && !matchMedia(rule.media.mediaText).matches) continue;
        if (rule.constructor.name === "CSSSupportsRule" && !CSS.supports(rule.conditionText)) continue;
        if (rule.styleSheet) { sheet(rule.styleSheet, context); continue; }
        let selector = parent, group = context;
        if (rule.selectorText) {
          selector = rule.selectorText;
          if (parent) {
            const ancestor = ":is(" + parent + ")";
            selector = selector.includes("&") ? selector.replaceAll("&", () => ancestor) : ancestor + " :is(" + selector + ")";
          }
        } else if (rule.cssRules) {
          if (rule.type === CSSRule.KEYFRAMES_RULE) continue;
          // Avoid serializing the entire stylesheet just to label a grouping rule.
          const condition = rule.conditionText || rule.name;
          if (condition) group = [context, condition].filter(Boolean).join(" · ");
        }
        if (selector && rule.style?.length) {
          let matches = false;
          try { matches = el.matches(selector); } catch { /* unsupported selector */ }
          if (matches) rules.push({ selector:clip(selector), source:clip(source), context:clip(group), declarations:declarations(rule.style) });
        }
        if (rule.cssRules?.length) walk(rule.cssRules, source, group, selector);
      }
    }
    for (const s of [...(root.styleSheets || []), ...(root.adoptedStyleSheets || [])]) sheet(s);
    return { rules: rules.reverse(), unreadable, truncated };
  }
  function details(computed, styles) {
    const rect = selected.getBoundingClientRect();
    const common = ["display", "position", "top", "right", "bottom", "left", "width", "height",
      "min-width", "max-width", "min-height", "max-height", "box-sizing", "margin", "padding",
      "gap", "flex", "flex-direction", "align-items", "justify-content", "grid-template-columns",
      "overflow", "color", "background-color", "font", "font-family", "font-size", "font-weight",
      "line-height", "text-align", "opacity", "z-index"];
    const style = computed ? getComputedStyle(selected) : null;
    const names = style ? new Set([...common, ...Array.from(style).sort()]) : [];
    const css = Array.from(names, name => [name, clip(style.getPropertyValue(name), 2048)]);
    const attrs = Array.from(selected.attributes, a => [a.name, clip(a.value, 512)]);
    return { tag:tag(selected), attrs, css, inline:declarations(selected.style),
      ...(styles ? matchingRules(selected) : {}), width:Math.round(rect.width), height:Math.round(rect.height) };
  }
  return function handle(cmd) {
    let editError;
    if (cmd.op === "cancel-pick") { pick(false); return; }
    if (cmd.op === "style" && cmd.documentKey !== documentKey) editError = "The page changed. Select the element again.";
    if (cmd.op !== "close" && cmd.op !== "read" && cmd.documentKey !== documentKey) cmd = { ...cmd, op: "read" };
    if (!!cmd.elements !== observingTree) {
      observingTree = !!cmd.elements;
      observer.disconnect();
      treeVersion++;
      revision++;
    }
    switch (cmd.op) {
      case "close": changed = () => {}; pick(false); highlight(null); cleanup.splice(0).forEach(fn => fn()); return;
      case "pick": pick(!!cmd.on); break;
      case "select": select(visible.get(cmd.index)); break;
      case "branch": {
        const el = visible.get(cmd.index);
        if (!el?.isConnected) break;
        if (cmd.on) branches.set(el, Math.max(0, Number.isSafeInteger(cmd.offset) ? cmd.offset : 0));
        else branches.delete(el);
        // Keep a visible selection when hiding or paging its descendants.
        if (!cmd.on || cmd.offset !== undefined) select(el);
        treeVersion++;
        break;
      }
      case "style":
        if (cmd.index !== ids.get(selected) || !selected?.isConnected || !selected.style) {
          editError = "The selection changed. Select the element again.";
        } else if (typeof cmd.name === "string" && typeof cmd.value === "string") {
          const name = cmd.name.trim(), important = /\s*!important\s*$/i;
          const value = cmd.value.replace(important, "").trim();
          if (cmd.value.trim() && (!value || !CSS.supports(name, value))) editError = "Enter a valid CSS property and value.";
          else {
            selected.style.setProperty(name, value, important.test(cmd.value) ? "important" : "");
            revision++;
          }
        }
        break;
      case "refresh": revision++; treeVersion++; break;
      case "clear": lines = []; break;
      case "eval":
        record("input", [cmd.text]);
        try { record("result", [(0, eval)(cmd.text)]); } catch (err) { record("error", [err]); }
        break;
    }
    if (!selected?.isConnected && document.documentElement) { selected = document.documentElement; branches.set(selected, 0); treeVersion++; revision++; }
    const sameDocument = cmd.documentKey === documentKey;
    const result = { documentKey, picking, revision, lines:lines.filter(l => l.id > (sameDocument ? cmd.after || 0 : 0)).slice(-60), width:innerWidth, height:innerHeight };
    if (editError) result.editError = editError;
    const stylesOpen = cmd.elements && (cmd.computed || cmd.styles);
    const needsDetails = !sameDocument || cmd.revision !== revision;
    if (cmd.elements && selected && (needsDetails || auditDue && stylesOpen)) {
      const current = details(cmd.computed, cmd.styles);
      const fingerprint = JSON.stringify(current);
      if (needsDetails || fingerprint !== lastDetails) {
        result.element = current;
        if (!needsDetails) result.revision = ++revision;
      }
      lastDetails = fingerprint;
    }
    if (!stylesOpen || auditDue || needsDetails || !styleTimer) scheduleAudit(stylesOpen);
    auditDue = false;
    if (cmd.elements && (!sameDocument || cmd.treeVersion !== treeVersion)) { result.tree = tree(); result.treeVersion = treeVersion; }
    return result;
  };
}
