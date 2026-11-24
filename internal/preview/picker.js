(function () {
  if (window.__lolaPreview) return;
  window.__lolaPreview = true;
  var SRC = "lola-preview";
  var picking = false;
  var elementTool = true;
  var box = null;
  var selected = null;
  var observer = null;
  var selectionArea = null;
  var labelChanges = null;

  function post(msg) {
    msg.source = SRC;
    try { window.parent.postMessage(msg, "*"); } catch (e) {}
  }

  function esc(s) {
    return window.CSS && CSS.escape ? CSS.escape(s) : String(s).replace(/[^a-zA-Z0-9_-]/g, "\\$&");
  }

  function part(el) {
    var tag = esc(el.localName);
    if (el.id) return tag + "#" + esc(el.id);
    var s = tag;
    var cls = (el.getAttribute("class") || "").split(/\s+/).filter(Boolean).slice(0, 2);
    for (var i = 0; i < cls.length; i++) s += "." + esc(cls[i]);
    var p = el.parentElement;
    if (p) {
      var same = 0, idx = 0;
      for (var c = p.firstElementChild; c; c = c.nextElementSibling) {
        if (c.tagName === el.tagName) { same++; if (c === el) idx = same; }
      }
      if (same > 1) s += ":nth-of-type(" + idx + ")";
    }
    return s;
  }

  function selector(el) {
    var parts = [];
    for (var n = el; n && n.nodeType === 1 && n !== document.documentElement; n = n.parentElement) {
      parts.unshift(part(n));
      if (n.id || parts.length >= 6) break;
    }
    return parts.join(" > ");
  }

  function anchorSelector(el) {
    var path = selector(el);
    function unique(s) {
      var matches = document.querySelectorAll(s);
      return matches.length === 1 && matches[0] === el;
    }
    if (path.length <= 300 && unique(path)) return path;
    // Repeated rows can share the entire six-part display selector. Include
    // the distinguishing ancestor and omit CSS classes to keep the anchor short.
    var parts = [];
    for (var n = el; n; n = n.parentElement) {
      var tag = esc(n.localName);
      if (n.previousElementSibling || n.nextElementSibling) {
        var index = 1;
        for (var p = n.previousElementSibling; p; p = p.previousElementSibling) index++;
        tag += ":nth-child(" + index + ")";
      }
      parts.unshift(tag);
      path = parts.join(">");
      if (unique(path)) return path;
    }
    return path;
  }

  function pickable(t) {
    return t && t.nodeType === 1 && t !== box && t !== document.documentElement && t !== document.body ? t : null;
  }

  function draw(el) {
    if (!box) {
      box = document.createElement("div");
      box.setAttribute("data-lola-preview", "");
      box.style.cssText = "position:fixed;z-index:2147483647;pointer-events:none;border:2px solid #7c5cff;background:rgba(124,92,255,.15);border-radius:2px;display:none";
      document.documentElement.appendChild(box);
    }
    if (!el || !el.isConnected) { box.style.display = "none"; return; }
    var r = el.getBoundingClientRect();
    Object.assign(box.style, { display: "block", boxSizing: "border-box", left: r.left + "px", top: r.top + "px", width: r.width + "px", height: r.height + "px" });
  }

  function onMove(e) { if (elementTool) draw(pickable(e.target)); }

  function alignSelection() {
    if (!selected) return;
    if (!selectionArea) draw(selected);
    var r = anchoredRect(selected, selectionArea);
    post({ type: "selection-bounds", rect: { x: r.left, y: r.top, w: r.width, h: r.height } });
  }

  function clearSelection() {
    selected = null;
    selectionArea = null;
    if (observer) observer.disconnect();
    observer = null;
    if (labelChanges) labelChanges.disconnect();
    labelChanges = null;
    document.removeEventListener("scroll", alignSelection, true);
    window.removeEventListener("resize", alignSelection);
    if (box) box.style.display = "none";
  }

  function onClick(e) {
    if (!elementTool) return;
    var el = pickable(e.target);
    if (!el) return;
    e.preventDefault();
    e.stopPropagation();
    e.stopImmediatePropagation();
    pick(el, null, []);
  }

  function anchorBounds(el, label) {
    if (el === document.documentElement) {
      return { left: -window.scrollX, top: -window.scrollY, width: innerWidth, height: innerHeight };
    }
    var r = el.getBoundingClientRect();
    // A label placed in a scroll pane's empty space belongs to its content.
    if (label && (el.scrollLeft || el.scrollTop)) {
      return new DOMRect(r.left - el.scrollLeft, r.top - el.scrollTop, r.width, r.height);
    }
    return r;
  }

  function anchoredRect(el, area) {
    var r = anchorBounds(el, area && area.label);
    if (!area) return r;
    var left = r.left + area.x, top = r.top + area.y;
    return { left: left, top: top, right: left + area.w, bottom: top + area.h, width: area.w, height: area.h };
  }

  // The parent draws in iframe CSS pixels. Resolve the DOM at release, while
  // the page is still in pick mode. Only selectors and bounded visible text
  // leave the page for a region, never the enclosing container's whole HTML.
  function pickAt(m) {
    if ((!picking && !m.editId) || m.documentId !== documentId || !Number.isFinite(m.x) || !Number.isFinite(m.y)) return;
    var area = m.rect, hits = [], more = false, first = null;
    if (area && ![area.x, area.y, area.w, area.h].every(Number.isFinite)) return;
    if (area && m.label) {
      // A dragged label can extend past the frame. Anchor its visible portion.
      var hit = document.elementFromPoint(Math.max(0, Math.min(innerWidth - 1, m.x)), Math.max(0, Math.min(innerHeight - 1, m.y)));
      var underLabel = pickable(hit) || document.documentElement;
      if (hit === document.body && hit !== document.scrollingElement &&
          (hit.scrollHeight > hit.clientHeight || hit.scrollWidth > hit.clientWidth)) underLabel = hit;
      pick(underLabel, area, [], false, m.editId, true);
      return;
    }
    if (area && m.page) {
      pick(document.documentElement, area, [], false, m.editId);
      return;
    }
    if (area && m.region) {
      var nodes = document.body.getElementsByTagName("*");
      for (var i = 0; i < nodes.length; i++) {
        var node = nodes[i], r = node.getBoundingClientRect();
        if (!r.width || !r.height || r.left < area.x || r.top < area.y ||
            r.right > area.x + area.w || r.bottom > area.y + area.h) continue;
        var at = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2);
        if (!at || !node.contains(at)) continue;
        if (!first) first = node;
        if (hits.length === 16) { more = true; break; }
        var label = (node.getAttribute("aria-label") || node.innerText || "").trim().replace(/\s+/g, " ").slice(0, 64);
        hits.push(selector(node).slice(0, 160) + (label ? " - " + label : ""));
      }
    }
    var x = m.region && area ? area.x + area.w / 2 : m.x;
    var y = m.region && area ? area.y + area.h / 2 : m.y;
    var el = first || document.elementFromPoint(x, y) || document.body;
    if (el === document.documentElement) el = document.body;
    if (!area && el === document.body) return;
    if (area && m.start && [m.start.x, m.start.y].every(Number.isFinite)) {
      var tail = document.elementFromPoint(m.start.x, m.start.y);
      pick(el, area, [], false, m.editId, false, {
        tail: endpoint(tail), tip: endpoint(el),
        embedded: [tail, el].some(function (node) { return node && node.matches("iframe,canvas,img,embed,object,video"); })
      });
      return;
    }
    pick(el, area, hits, more, m.editId);
  }

  function endpoint(el) {
    if (!pickable(el)) return "Page background";
    // A small table row gives a cell its item number and name. Never quote
    // the text of a whole document, or the source of an embedded preview.
    if (el.matches("iframe,canvas,img,embed,object,video")) {
      return selector(el).slice(0, 240) + " (embedded content at this point)";
    }
    var target = el.closest("tr,[role=row]") || el;
    var words = (target.innerText || "").trim().replace(/\s+/g, " ");
    var text = words.length <= 400 ? words : "";
    return selector(el).slice(0, 240) + (text ? " — " + text : "");
  }

  function pick(el, area, targets, more, editId, label, arrow) {
    var r = anchorBounds(el, label);
    post({
      type: "pick",
      url: location.href,
      selector: el === document.documentElement ? ":root" : anchorSelector(el),
      ...(editId && { editId: editId }),
      tag: el.tagName.toLowerCase(),
      ...(arrow && { arrow: arrow }),
      html: area ? "" : el.outerHTML.slice(0, 6000),
      targets: targets, moreTargets: !!more, documentId: documentId,
      text: area ? "" : (el.innerText || el.textContent || "").trim().slice(0, 400),
      rect: { x: Math.round(r.left), y: Math.round(r.top), w: Math.round(r.width), h: Math.round(r.height) }
    });
    if (editId) return; // Moving a draft leaves the active tool alone.
    set(false);
    selected = el;
    selectionArea = area && { label: label, x: area.x - Math.round(r.left), y: area.y - Math.round(r.top), w: area.w, h: area.h };
    document.addEventListener("scroll", alignSelection, true);
    window.addEventListener("resize", alignSelection);
    if (window.ResizeObserver) {
      observer = new ResizeObserver(alignSelection);
      observer.observe(el);
    }
    if (label) {
      labelChanges = new MutationObserver(alignSelection);
      labelChanges.observe(document.documentElement, { subtree: true, childList: true, attributes: true, characterData: true });
    }
    alignSelection();
  }

  function swallow(e) { if (pickable(e.target)) { e.preventDefault(); e.stopPropagation(); } }

  var annotationKeys = [];
  // Give the page first refusal on unmodified shortcuts, including its editors.
  window.addEventListener("keydown", function (e) {
    if (e.repeat || e.defaultPrevented || e.isComposing || e.keyCode === 229 ||
        !e.isTrusted || e.altKey || e.ctrlKey || e.metaKey || e.shiftKey) return;
    if (annotationKeys.includes(e.key) && document.designMode !== "on" && !e.composedPath().some(function (el) {
      return el instanceof HTMLElement && (el.isContentEditable || el.matches('input,textarea,select,[contenteditable]:not([contenteditable="false"]),[role="textbox"],[role="combobox"],[role="searchbox"]'));
    })) {
      e.preventDefault();
      post({ type: "comment-shortcut", documentId: documentId, key: e.key });
      return;
    }
    if (e.key !== "Escape") return;
    e.preventDefault();
    if (picking) { set(false); return; }
    post({ type: "escape" });
  });

  function set(on) {
    if (on === picking) return;
    picking = on;
    var f = on ? "addEventListener" : "removeEventListener";
    document[f]("mousemove", onMove, true);
    document[f]("click", onClick, true);
    document[f]("pointerdown", swallow, true);
    document[f]("pointerup", swallow, true);
    document[f]("mousedown", swallow, true);
    document[f]("mouseup", swallow, true);
    document.documentElement.style.cursor = on ? "crosshair" : "";
    if (!on) draw(null);
    post({ type: "mode", picking: on });
  }

  var documentId = Math.random().toString(36).slice(2);
  // Report only geometry. Lola draws the badges outside the app, so comments
  // never enter the page and its own controls remain usable under the borders.
  var stopComments = function () {};
  function watchComments(message) {
    stopComments();
    if (!Array.isArray(message.anchors) || !message.anchors.length) return;
    var anchors = message.anchors.slice(0, 10), pending = 0, previous = "";
    var sizes = new ResizeObserver(schedule);
    var observed = new Set();
    function schedule() {
      if (!pending) pending = requestAnimationFrame(measure);
    }
    function measure() {
      pending = 0;
      var targets = new Set(), rects = [];
      anchors.forEach(function (anchor) {
        if (anchor.path !== location.pathname + location.search + location.hash) return;
        var el;
        try { el = document.querySelector(anchor.selector); } catch (err) { return; }
        if (!el) return;
        targets.add(el);
        var r = anchoredRect(el, anchor.area);
        if ((el !== document.documentElement && !el.getClientRects().length) || !r.width || !r.height) return;
        var left = Math.max(0, r.left), top = Math.max(0, r.top);
        var right = Math.min(innerWidth, r.right), bottom = Math.min(innerHeight, r.bottom);
        var ownScroll = anchor.area && anchor.area.label && (el.scrollHeight > el.clientHeight || el.scrollWidth > el.clientWidth);
        for (var p = ownScroll ? el : el.parentElement; p && p !== document.documentElement; p = p.parentElement) {
          var style = getComputedStyle(p);
          if (style.overflowX === "visible" && style.overflowY === "visible") continue;
          var clip = p.getBoundingClientRect();
          if (style.overflowX !== "visible") { left = Math.max(left, clip.left); right = Math.min(right, clip.right); }
          if (style.overflowY !== "visible") { top = Math.max(top, clip.top); bottom = Math.min(bottom, clip.bottom); }
        }
        if (right > left && bottom > top) rects.push({ number: anchor.number, x: left, y: top, w: right-left, h: bottom-top,
          ...(anchor.area && { originX: r.left, originY: r.top }) });
      });
      observed.forEach(function (el) { if (!targets.has(el)) sizes.unobserve(el); });
      targets.forEach(function (el) { if (!observed.has(el)) sizes.observe(el); });
      observed = targets;
      var signature = JSON.stringify(rects);
      if (signature !== previous) post({ type: "comment-bounds", revision: message.revision, rects: rects });
      previous = signature;
    }
    // Pointer events inside this frame do not bubble to the desktop overlay.
    function deselect() { post({ type: "annotation-deselect", documentId: documentId }); }
    document.addEventListener("pointerdown", deselect, true);
    var changes = new MutationObserver(schedule);
    changes.observe(document.documentElement, { subtree: true, childList: true, attributes: true, characterData: true });
    document.addEventListener("scroll", schedule, true);
    document.addEventListener("load", schedule, true);
    window.addEventListener("resize", schedule);
    window.addEventListener("popstate", schedule);
    window.addEventListener("hashchange", schedule);
    measure();
    stopComments = function () {
      changes.disconnect(); sizes.disconnect(); cancelAnimationFrame(pending);
      document.removeEventListener("pointerdown", deselect, true);
      document.removeEventListener("scroll", schedule, true);
      document.removeEventListener("load", schedule, true);
      window.removeEventListener("resize", schedule);
      window.removeEventListener("popstate", schedule);
      window.removeEventListener("hashchange", schedule);
      stopComments = function () {};
    };
  }
  function favicon() {
    var icon = document.head && document.head.querySelector('link[rel~="icon" i][href]');
    return icon && icon.href.length <= 32768 ? icon.href : "";
  }
  function announce(kind) {
    if (!kind && performance.getEntriesByType && performance.getEntriesByType("navigation")[0]?.type === "back_forward") kind = "traverse";
    post({ type: "ready", picking: picking, documentId: documentId, url: location.href, title: document.title,
      favicon: favicon(), kind: kind, canGoBack: !!(window.navigation && window.navigation.canGoBack) });
  }
  // A cross-origin parent sees the iframe load finish, but cannot see it start.
  function pageLoading(loading) { post({ type: "page-loading", documentId: documentId, loading: loading }); }
  if (window.navigation) {
    var pendingNavigation;
    function finishNavigation() {
      if (!pendingNavigation) return;
      pendingNavigation = null;
      pageLoading(false);
    }
    window.navigation.addEventListener("navigate", function (event) {
      if (event.destination.sameDocument || event.downloadRequest != null) return;
      queueMicrotask(function () {
        if (event.defaultPrevented || event.signal.aborted) return;
        pendingNavigation = event.signal;
        pageLoading(true);
        event.signal.addEventListener("abort", function () {
          if (pendingNavigation === event.signal) finishNavigation();
        }, { once: true });
      });
    });
    window.navigation.addEventListener("navigatesuccess", finishNavigation);
    window.navigation.addEventListener("navigateerror", finishNavigation);
  }
  window.addEventListener("pagehide", function () { pageLoading(true); });
  window.addEventListener("pageshow", function (event) {
    if (event.persisted) { announce("traverse"); pageLoading(false); }
  });
  // Track client-side navigation as well as whole-document loads. The review
  // canvas only needs ready; install these listeners for browser tabs on demand.
  var watchingLocation = false;
  function watchLocation() {
    if (watchingLocation) return;
    watchingLocation = true;
    function newTab(url) {
      try {
        var link = new URL(url, document.baseURI);
        if (!/^https?:$/.test(link.protocol) || link.username || link.password) return false;
        post({ type: "new-tab", documentId: documentId, url: link.href });
        return true;
      } catch (_) { return false; }
    }
    function opensTab(target) {
      if (!target || /^_(self|parent|top|unfencedTop)$/i.test(target)) return false;
      if (target.toLowerCase() === "_blank") return true;
      return target !== window.name && !Array.from(document.querySelectorAll("iframe[name],frame[name]")).some(function (frame) {
        return frame.name === target;
      });
    }
    function openLink(e) {
      if (picking || e.defaultPrevented || e.altKey || e.button > 1) return;
      var link = e.composedPath().find(function (el) { return el.matches && el.matches("a[href],area[href]"); });
      if (!link || link.hasAttribute("download")) return;
      var base = document.querySelector("base[target]");
      var target = link.getAttribute("target") || (base && base.target);
      if (!opensTab(target) && !e.metaKey && !e.ctrlKey && !e.shiftKey && e.button !== 1) return;
      if (newTab(link.getAttribute("href"))) e.preventDefault();
    }
    window.addEventListener("click", openLink);
    window.addEventListener("auxclick", openLink);
    var originalOpen = window.open;
    window.open = function (url, target) {
      if (url && opensTab(String(target || "_blank"))) {
        if (navigator.userActivation && navigator.userActivation.isActive) newTab(url);
        return null;
      }
      return originalOpen.apply(this, arguments);
    };
    var lastURL = location.href, lastTitle = document.title, lastIcon = favicon(), lastBack;
    function changed(kind) {
      var back = !!(window.navigation && window.navigation.canGoBack);
      var icon = favicon();
      if (location.href === lastURL && document.title === lastTitle && icon === lastIcon && back === lastBack) return;
      lastURL = location.href; lastTitle = document.title; lastIcon = icon; lastBack = back;
      post({ type: "location", documentId: documentId, url: lastURL, title: lastTitle, favicon: icon, kind: kind, canGoBack: back });
    }
    if (document.head) new MutationObserver(function () { changed(); }).observe(document.head, { subtree: true, childList: true, characterData: true, attributeFilter: ["href", "rel"] });
    if (window.navigation) window.navigation.addEventListener("currententrychange", function (e) { changed(e.navigationType); });
    else {
      ["pushState", "replaceState"].forEach(function (name) {
        var original = history[name];
        history[name] = function () {
          var result = original.apply(this, arguments);
          changed(name === "replaceState" ? "replace" : "push");
          return result;
        };
      });
      window.addEventListener("popstate", function () { changed("traverse"); });
      window.addEventListener("hashchange", function () { changed(); });
    }
  }
  function loginFields() {
    function usable(input) {
      return !input.disabled && !input.readOnly && input.getClientRects().length > 0 &&
        getComputedStyle(input).visibility === "visible";
    }
    // A registration or password-change form may also have a username.
    // Require one visible password field and one unambiguous username in its form.
    var passwords = Array.from(document.querySelectorAll('input[type="password"]')).filter(usable);
    if (passwords.length !== 1 || passwords[0].autocomplete.includes("new-password")) return null;
    var pass = passwords[0], form = pass.form;
    if (!form || new URL(form.action, document.baseURI).origin !== location.origin) return null;
    var fields = Array.from(form.elements).filter(function (el) {
      return el instanceof HTMLInputElement && ["email", "text", "tel"].includes(el.type) && usable(el);
    });
    var named = fields.filter(function (el) { return el.autocomplete.includes("username") || el.type === "email"; });
    if (named.length) fields = named;
    if (fields.length !== 1) return null;
    return { user: fields[0], pass: pass, form: form };
  }

  function submitButtons(form) {
    return Array.from(form.elements).filter(function (el) {
      return (el.type === "submit" || el.type === "image") && !el.disabled && el.getClientRects().length;
    });
  }

  function autoLoginFields() {
    var fields = loginFields();
    if (!fields || fields.user.value || fields.pass.value) return null;
    var form = fields.form;
    var submits = submitButtons(form);
    if (submits.length > 1) return null;
    var button = submits[0];
    var action = new URL(form.action, document.baseURI);
    var intent = [location.pathname, action.pathname, form.id, form.getAttribute("aria-label"),
      button && (button.innerText || button.value || button.getAttribute("aria-label"))].join(" ");
    if (/register|sign[ _-]?up|reset|change[ _-]?password|create[ _-]?account|registrier/i.test(intent)) return null;
    if (!fields.pass.autocomplete.includes("current-password") && !/log[ _-]?in|sign[ _-]?in|anmeld|einloggen/i.test(intent)) return null;
    if (button && button.hasAttribute("formaction") && new URL(button.formAction).origin !== location.origin) return null;
    var method = button && button.hasAttribute("formmethod") ? button.formMethod : form.getAttribute("method");
    if (method && method.toLowerCase() !== "post") return null;
    return fields;
  }

  // Watch only while the desktop is waiting for a form. SPA routes may mount it
  // after the document has loaded. Never send field values back to the desktop.
  var stopLoginWatch = function () {};
  function watchLogin(on) {
    stopLoginWatch();
    if (!on) return;
    var pending = 0;
    var observer = new MutationObserver(function () {
      if (!pending) pending = requestAnimationFrame(check);
    });
    function check() {
      pending = 0;
      if (!autoLoginFields()) return;
      stopLoginWatch();
      post({ type: "login-form", documentId: documentId });
    }
    stopLoginWatch = function () {
      cancelAnimationFrame(pending);
      observer.disconnect();
      stopLoginWatch = function () {};
    };
    observer.observe(document.documentElement, { subtree: true, childList: true, attributes: true });
    check();
  }
  function useTestLogin(message) {
    var fields = message.automatic ? autoLoginFields() : loginFields();
    if (!fields) return "unavailable";
    var pass = fields.pass, form = fields.form;
    var set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value").set;
    function fill(el, value) {
      set.call(el, value);
      el.dispatchEvent(new Event("input", { bubbles: true }));
      el.dispatchEvent(new Event("change", { bubbles: true }));
    }
    fill(fields.user, message.username);
    fill(pass, message.password);
    // A GET would put the password in browser history. A client-rendered form
    // may omit method and handle submit itself, so dispatch only the event there.
    var buttons = submitButtons(form);
    var button = buttons[0];
    if (buttons.length > 1 || !form.isConnected || pass.form !== form || fields.user.form !== form ||
        (button && button.hasAttribute("formaction") ? new URL(button.formAction).origin : new URL(form.action).origin) !== location.origin ||
        !form.reportValidity()) return "filled";
    if (!form.hasAttribute("method") && !(button && button.hasAttribute("formmethod"))) {
      var event = new SubmitEvent("submit", { bubbles: true, cancelable: true, submitter: button || null });
      return form.dispatchEvent(event) ? "filled" : "submitted";
    }
    if ((button && button.hasAttribute("formmethod") ? button.formMethod : form.method).toLowerCase() !== "post") return "filled";
    form.requestSubmit(button);
    return "submitted";
  }
  var closeDebugger = function () {}, cancelDebugPick = function () {};
  window.addEventListener("pagehide", function () { closeDebugger(); });
  window.addEventListener("message", function (e) {
    if (e.source !== window.parent || !e.data || e.data.target !== SRC) return;
    // Only Lola's desktop origin can acquire a console for this frame.
    // The document-bound port carries replies privately, including after navigation.
    if (e.data.type === "debug-connect" && e.data.documentId === documentId &&
        /^wails:\/\/localhost(?::[0-9]+)?$/.test(e.origin) && e.ports[0]) {
      closeDebugger();
      set(false);
      var port = e.ports[0], dirtyTimer = 0;
      var debug = lolaPreviewDebugger(function () {
        if (!dirtyTimer) dirtyTimer = setTimeout(function () { dirtyTimer = 0; port.postMessage({dirty:true}); }, 80);
      });
      cancelDebugPick = function () { debug({ op: "cancel-pick" }); };
      closeDebugger = function () { cancelDebugPick = function () {}; debug({ op: "close" }); clearTimeout(dirtyTimer); port.close(); closeDebugger = function () {}; };
      port.onmessage = function (event) {
        if (event.data.op === "close") { closeDebugger(); return; }
        if (event.data.op === "pick" && event.data.on) set(false);
        try { port.postMessage({ id:event.data.id, result:debug(event.data) }); }
        catch (err) { port.postMessage({ id:event.data.id, error:String(err) }); }
      };
      port.postMessage({ ready:true });
    }
    if (e.data.type === "comment-shortcut" && e.data.documentId === documentId) {
      annotationKeys = e.data.enabled === true ? (Array.isArray(e.data.keys) ? e.data.keys : ["c"]) : [];
    }
    if (e.data.type === "watch-login" && e.data.documentId === documentId) watchLogin(e.data.on);
    if (e.data.type === "test-login" && e.data.documentId === documentId) {
      var result = "unavailable";
      try { result = useTestLogin(e.data); } catch (_) { /* The page changed or has no usable form. */ }
      post({ type: "login-result", documentId: documentId, result: result });
    }
    if (e.data.type === "hello") {
      if (e.data.watchLocation) watchLocation();
      announce();
    }
    if (e.data.type === "back" && watchingLocation && e.data.documentId === documentId && window.navigation && window.navigation.canGoBack) {
      window.navigation.back();
    }
    if (e.data.type === "comment-anchors" && e.data.documentId === documentId) watchComments(e.data);
    if (e.data.type === "pick-at") pickAt(e.data);
    if (e.data.type === "hover-at" && picking && e.data.documentId === documentId) {
      var hovered = document.elementFromPoint(e.data.x, e.data.y);
      draw(pickable(hovered));
    }
    if (e.data.type === "pick") { if (e.data.on) cancelDebugPick(); elementTool = !e.data.tool || e.data.tool === "comment"; clearSelection(); set(!!e.data.on); }
    if (e.data.type === "check-session") {
      var check = e.data, target;
      try { target = new URL(check.path, location.origin); } catch (err) { return; }
      if (target.origin !== location.origin) return;
      fetch(target.href, { method: "HEAD", credentials: "same-origin", mode: "same-origin", cache: "no-store" }).then(function (response) {
        if (response.status === 401 || (response.redirected && new URL(response.url).pathname === check.entry)) {
          post({ type: "session-expired", id: check.id });
        }
      }).catch(function () { /* An unavailable server is not evidence of an expired login. */ });
    }
    if (e.data.type === "prepare") {
      var failed = function (why) { post({ type: "prepared", found: [], waited: false, failed: { index: 0, step: "", why: why } }); };
      if (!window.__lolaPrepare) return failed("lola's prepare script is not loaded in this page");
      // A spec is plain data from lola's own app; the script never evals it.
      // Progress goes up AFTER each step has had its effect: a step that navigates ends
      // this script, and the app resumes after the next document from the last step it saw.
      window.__lolaPrepare(e.data.spec, 12000, function (i) { post({ type: "step", i: i }); }).then(function (r) {
        if (r.superseded) return; // a newer prepare owns the result
        post({ type: "prepared", url: location.href, found: r.found, waited: r.waited, failed: r.failed });
      }, function (err) { failed("preparing the page threw: " + String(err && err.message || err).slice(0, 120)); });
    }
  });

  announce();
})();
