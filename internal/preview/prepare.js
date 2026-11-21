// Prepares one view of a page for a reviewer: waits for what the agent named,
// scrolls the first mark into the middle of the view and outlines each mark with
// a label. The SAME file runs inside the embedded preview (served with the
// picker) and in the browser that takes a review screenshot, so the picture is
// the view the reviewer gets. It is data-driven: the spec is plain JSON
// (internal/reviewpack.Prep) and nothing in it is ever evaluated as code.
(function () {
  if (window.__lolaPrepare) return;
  var LAYER = "lola-prepare-layer";
  var COLORS = ["#7c5cff", "#e5484d", "#30a46c", "#f5a524"];

  function str(v, max) { return typeof v === "string" ? v.slice(0, max) : ""; }

  // The deepest element whose visible text contains `want`: the label of a
  // button, not the <body> that happens to contain it.
  function byText(want) {
    want = want.toLowerCase();
    var nodes = document.body ? document.body.querySelectorAll("*") : [];
    var best = null;
    for (var i = 0; i < nodes.length && i < 8000; i++) {
      var el = nodes[i];
      if (el.closest("#" + LAYER)) continue;
      var tag = el.tagName;
      if (tag === "SCRIPT" || tag === "STYLE" || tag === "NOSCRIPT") continue;
      if ((el.textContent || "").toLowerCase().indexOf(want) === -1) continue;
      var deeper = false;
      for (var c = el.firstElementChild; c; c = c.nextElementSibling) {
        if ((c.textContent || "").toLowerCase().indexOf(want) !== -1) { deeper = true; break; }
      }
      if (!deeper) { best = el; break; }
    }
    return best;
  }

  function find(m) {
    var sel = str(m.selector, 500);
    if (sel) {
      try { var e = document.querySelector(sel); if (e) return e; } catch (err) {}
    }
    var text = str(m.text, 200);
    return text ? byText(text) : null;
  }

  function layer() {
    var l = document.getElementById(LAYER);
    if (l) l.remove();
    l = document.createElement("div");
    l.id = LAYER;
    l.setAttribute("data-lola-prepare", "");
    l.style.cssText = "position:absolute;left:0;top:0;width:0;height:0;overflow:visible;z-index:2147483646;pointer-events:none";
    document.documentElement.appendChild(l);
    return l;
  }

  function draw(l, el, label, i) {
    var r = el.getBoundingClientRect();
    var color = COLORS[i % COLORS.length];
    var box = document.createElement("div");
    box.style.cssText = "position:absolute;box-sizing:border-box;border:3px solid " + color +
      ";border-radius:4px;box-shadow:0 0 0 2px rgba(255,255,255,.85);left:" + (r.left + window.pageXOffset - 3) +
      "px;top:" + (r.top + window.pageYOffset - 3) + "px;width:" + (r.width + 6) + "px;height:" + (r.height + 6) + "px";
    l.appendChild(box);
    if (label) {
      var tag = document.createElement("div");
      tag.textContent = label;
      var above = r.top > 28;
      tag.style.cssText = "position:absolute;max-width:320px;padding:2px 8px;border-radius:4px;font:600 12px/18px system-ui,sans-serif;color:#fff;background:" +
        color + ";left:" + (r.left + window.pageXOffset - 3) + "px;top:" + (above ? r.top + window.pageYOffset - 28 : r.bottom + window.pageYOffset + 4) + "px";
      l.appendChild(tag);
    }
  }

  function apply(spec, scroll) {
    var marks = Array.isArray(spec.marks) ? spec.marks.slice(0, 4) : [];
    var found = [], els = [];
    for (var i = 0; i < marks.length; i++) {
      var el = find(marks[i] || {});
      els.push(el);
      found.push(!!el);
    }
    var first = null;
    for (var j = 0; j < els.length; j++) if (els[j]) { first = els[j]; break; }
    if (scroll) {
      if (first) first.scrollIntoView({ block: "center", inline: "nearest", behavior: "auto" });
      else if (typeof spec.scrollY === "number" && spec.scrollY > 0) window.scrollTo(0, Math.min(spec.scrollY, 100000));
    }
    var l = layer();
    for (var k = 0; k < els.length; k++) if (els[k]) draw(l, els[k], str(marks[k].label, 80), k);
    return found;
  }

  function ready(spec) {
    var wait = str(spec.waitFor, 500);
    if (wait) {
      try { if (!document.querySelector(wait)) return false; } catch (err) {}
    }
    var marks = Array.isArray(spec.marks) ? spec.marks.slice(0, 4) : [];
    for (var i = 0; i < marks.length; i++) if (!find(marks[i] || {})) return false;
    return true;
  }

  // Resolves { found: [bool per mark], waited: bool } once the page is ready or
  // after `timeout` ms, whichever is first; a mark that never appears is reported
  // rather than guessed at. Layout settles (fonts, images), so it is drawn again.
  window.__lolaPrepare = function (spec, timeout) {
    spec = spec && typeof spec === "object" ? spec : {};
    var deadline = Date.now() + (timeout > 0 ? Math.min(timeout, 30000) : 10000);
    return new Promise(function (resolve) {
      (function poll() {
        var ok = ready(spec);
        if (ok || Date.now() >= deadline) {
          var found = apply(spec, true);
          setTimeout(function () { found = apply(spec, false); resolve({ found: found, waited: !ok }); }, 400);
          return;
        }
        setTimeout(poll, 250);
      })();
    });
  };
})();
