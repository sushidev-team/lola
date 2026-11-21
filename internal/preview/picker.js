(function () {
  if (window.__lolaPreview) return;
  window.__lolaPreview = true;
  var SRC = "lola-preview";
  var picking = false;
  var box = null;
  var selected = null;
  var observer = null;

  function post(msg) {
    msg.source = SRC;
    try { window.parent.postMessage(msg, "*"); } catch (e) {}
  }

  function esc(s) {
    return window.CSS && CSS.escape ? CSS.escape(s) : String(s).replace(/[^a-zA-Z0-9_-]/g, "\\$&");
  }

  function part(el) {
    var tag = el.tagName.toLowerCase();
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

  function target(e) {
    var t = e.target;
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

  function onMove(e) { draw(target(e)); }

  function alignSelection() {
    if (!selected) return;
    draw(selected);
    var r = selected.getBoundingClientRect();
    post({ type: "selection-bounds", rect: { x: r.left, y: r.top, w: r.width, h: r.height } });
  }

  function clearSelection() {
    selected = null;
    if (observer) observer.disconnect();
    observer = null;
    document.removeEventListener("scroll", alignSelection, true);
    window.removeEventListener("resize", alignSelection);
    if (box) box.style.display = "none";
  }

  function onClick(e) {
    var el = target(e);
    if (!el) return;
    e.preventDefault();
    e.stopPropagation();
    e.stopImmediatePropagation();
    var r = el.getBoundingClientRect();
    post({
      type: "pick",
      url: location.href,
      selector: selector(el),
      tag: el.tagName.toLowerCase(),
      html: el.outerHTML.slice(0, 6000),
      text: (el.innerText || el.textContent || "").trim().slice(0, 400),
      rect: { x: Math.round(r.left), y: Math.round(r.top), w: Math.round(r.width), h: Math.round(r.height) }
    });
    set(false);
    selected = el;
    document.addEventListener("scroll", alignSelection, true);
    window.addEventListener("resize", alignSelection);
    if (window.ResizeObserver) {
      observer = new ResizeObserver(alignSelection);
      observer.observe(el);
    }
    alignSelection();
  }

  function swallow(e) { if (target(e)) { e.preventDefault(); e.stopPropagation(); } }

  // Let the page handle its own Escape first (for example a combobox).
  window.addEventListener("keydown", function (e) {
    if (e.key !== "Escape" || e.repeat || e.defaultPrevented || e.isComposing ||
        !e.isTrusted || e.altKey || e.ctrlKey || e.metaKey || e.shiftKey) return;
    e.preventDefault();
    set(false);
    post({ type: "escape" });
  });

  function set(on) {
    if (on === picking) return;
    picking = on;
    var f = on ? "addEventListener" : "removeEventListener";
    document[f]("mousemove", onMove, true);
    document[f]("click", onClick, true);
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
        if (anchor.path !== location.pathname + location.search) return;
        var el;
        try { el = document.querySelector(anchor.selector); } catch (err) { return; }
        if (!el) return;
        targets.add(el);
        var r = el.getBoundingClientRect();
        if (!r.width || !r.height) return;
        var left = Math.max(0, r.left), top = Math.max(0, r.top);
        var right = Math.min(innerWidth, r.right), bottom = Math.min(innerHeight, r.bottom);
        for (var p = el.parentElement; p && p !== document.documentElement; p = p.parentElement) {
          var style = getComputedStyle(p);
          if (style.overflowX === "visible" && style.overflowY === "visible") continue;
          var clip = p.getBoundingClientRect();
          if (style.overflowX !== "visible") { left = Math.max(left, clip.left); right = Math.min(right, clip.right); }
          if (style.overflowY !== "visible") { top = Math.max(top, clip.top); bottom = Math.min(bottom, clip.bottom); }
        }
        if (right > left && bottom > top) rects.push({ number: anchor.number, x: left, y: top, w: right-left, h: bottom-top });
      });
      observed.forEach(function (el) { if (!targets.has(el)) sizes.unobserve(el); });
      targets.forEach(function (el) { if (!observed.has(el)) sizes.observe(el); });
      observed = targets;
      var signature = JSON.stringify(rects);
      if (signature !== previous) post({ type: "comment-bounds", revision: message.revision, rects: rects });
      previous = signature;
    }
    var changes = new MutationObserver(schedule);
    changes.observe(document.documentElement, { subtree: true, childList: true, attributes: true, characterData: true });
    document.addEventListener("scroll", schedule, true);
    document.addEventListener("load", schedule, true);
    window.addEventListener("resize", schedule);
    window.addEventListener("popstate", schedule);
    measure();
    stopComments = function () {
      changes.disconnect(); sizes.disconnect(); cancelAnimationFrame(pending);
      document.removeEventListener("scroll", schedule, true);
      document.removeEventListener("load", schedule, true);
      window.removeEventListener("resize", schedule);
      window.removeEventListener("popstate", schedule);
      stopComments = function () {};
    };
  }
  function announce() { post({ type: "ready", documentId: documentId, url: location.href, title: document.title }); }
  window.addEventListener("message", function (e) {
    if (e.source !== window.parent || !e.data || e.data.target !== SRC) return;
    if (e.data.type === "hello") announce();
    if (e.data.type === "comment-anchors" && e.data.documentId === documentId) watchComments(e.data);
    if (e.data.type === "pick") { clearSelection(); set(!!e.data.on); }
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
