(function () {
  if (window.__lolaPreview) return;
  window.__lolaPreview = true;
  var SRC = "lola-preview";
  var picking = false;
  var box = null;
  var last = null;

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
    if (!el) { box.style.display = "none"; return; }
    var r = el.getBoundingClientRect();
    box.style.cssText += ";display:block;left:" + r.left + "px;top:" + r.top + "px;width:" + r.width + "px;height:" + r.height + "px";
  }

  function onMove(e) { last = target(e); draw(last); }

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

  window.addEventListener("message", function (e) {
    if (e.source !== window.parent || !e.data || e.data.target !== SRC) return;
    if (e.data.type === "pick") set(!!e.data.on);
    if (e.data.type === "prepare" && window.__lolaPrepare) {
      // A spec is plain data from lola's own app; the script never evals it.
      // Progress goes up BEFORE each step: a step that navigates ends this script,
      // and the app resumes after the next document from the last step it saw.
      window.__lolaPrepare(e.data.spec, 12000, function (i) { post({ type: "step", i: i }); }).then(function (r) {
        post({ type: "prepared", found: r.found, waited: r.waited, failed: r.failed });
      });
    }
  });

  post({ type: "ready", url: location.href, title: document.title });
})();
