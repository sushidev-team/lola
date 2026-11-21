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

  // ---- Steps: clicks, typing, a login ---------------------------------------
  // Run BEFORE the marks, only on a local host (the same rule as
  // reviewpack.LocalHost, checked again before every step: a page that sent the
  // browser somewhere else mid-flow gets nothing typed into it). A step that
  // navigates ends this script; the caller resumes with spec.from after the next
  // document announces itself, using the progress callback below.

  function localHost(h) {
    h = String(h || "").toLowerCase().replace(/^\[|\]$/g, "");
    if (!h) return false;
    if (h === "localhost" || /\.localhost$/.test(h) || /\.test$/.test(h)) return true;
    var m = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/.exec(h);
    if (m) {
      var a = +m[1], b = +m[2];
      if (+m[3] > 255 || +m[4] > 255 || a > 255 || b > 255) return false;
      return a === 127 || a === 10 || (a === 172 && b >= 16 && b <= 31) || (a === 192 && b === 168) || (a === 169 && b === 254);
    }
    return h === "::1" || /^f[cd][0-9a-f]{2}:/.test(h) || /^fe[89ab][0-9a-f]:/.test(h);
  }
  window.__lolaLocalHost = localHost;

  var KEYS = { Enter: 13, Escape: 27, Tab: 9, Space: 32, ArrowUp: 38, ArrowDown: 40 };
  var stepWait = 8000; // per step; spec.stepTimeoutMs may only shorten it
  var token = 0;

  function sleep(ms) { return new Promise(function (r) { setTimeout(r, ms); }); }

  function until(fn, ms, id) {
    var deadline = Date.now() + ms;
    return new Promise(function (resolve) {
      (function poll() {
        if (id !== token) return resolve(null);
        var v = fn();
        if (v || Date.now() >= deadline) return resolve(v || null);
        setTimeout(poll, 100);
      })();
    });
  }

  function qs(sel) { try { return sel ? document.querySelector(sel) : null; } catch (err) { return null; } }

  function lower(s) { return String(s || "").toLowerCase(); }

  // A field by selector, or by what a person would call it: a label, a
  // placeholder, an aria-label or its name.
  function findField(step) {
    var el = qs(str(step.selector, 500));
    if (el) return el;
    var want = lower(str(step.text, 200));
    if (!want) return null;
    var fields = document.querySelectorAll("input,textarea,select");
    for (var i = 0; i < fields.length; i++) {
      var f = fields[i];
      if (f.type === "hidden") continue;
      var names = [f.getAttribute("placeholder"), f.getAttribute("aria-label"), f.getAttribute("name")];
      if (f.labels) for (var j = 0; j < f.labels.length; j++) names.push(f.labels[j].textContent);
      for (var k = 0; k < names.length; k++) if (lower(names[k]).indexOf(want) !== -1) return f;
    }
    return null;
  }

  var CLICKABLE = "a,button,[role=button],[role=tab],[role=menuitem],[role=link],input[type=submit],input[type=button],summary,label";

  // Something to click: by selector, or the control whose text matches best (the
  // shortest one, so "Save" finds the Save button, not the form that contains it).
  function findClickable(step) {
    var el = qs(str(step.selector, 500));
    if (el) return el;
    var want = lower(str(step.text, 200));
    if (!want) return null;
    var nodes = document.querySelectorAll(CLICKABLE), best = null, bestLen = 1e9;
    for (var i = 0; i < nodes.length && i < 4000; i++) {
      var n = nodes[i];
      var t = lower(n.value && /^(submit|button)$/.test(n.type) ? n.value : n.textContent) + " " + lower(n.getAttribute("aria-label"));
      if (t.indexOf(want) !== -1 && t.length < bestLen) { best = n; bestLen = t.length; }
    }
    return best || byText(str(step.text, 200));
  }

  function findAny(step) { return qs(str(step.selector, 500)) || (step.text ? byText(str(step.text, 200)) : null); }

  function setValue(el, value) {
    // Only a text field, a text area or a select takes a value (a file input would throw).
    var tag = el.tagName;
    if (tag !== "SELECT" && tag !== "TEXTAREA" && !(tag === "INPUT" && el.type !== "file")) return false;
    el.focus();
    if (el.tagName === "SELECT") {
      var want = lower(value), hit = null;
      for (var i = 0; i < el.options.length; i++) {
        if (lower(el.options[i].value) === want || lower(el.options[i].text) === want) { hit = el.options[i]; break; }
      }
      if (!hit) return false;
      el.value = hit.value;
    } else {
      // The native setter, so a framework that tracks the value notices it.
      var proto = el.tagName === "TEXTAREA" ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
      var set = Object.getOwnPropertyDescriptor(proto, "value");
      if (set && set.set) set.set.call(el, value); else el.value = value;
    }
    el.dispatchEvent(new Event("input", { bubbles: true }));
    el.dispatchEvent(new Event("change", { bubbles: true }));
    return true;
  }

  function press(el, key) {
    el = el || document.activeElement || document.body;
    var code = KEYS[key];
    var init = { key: key === "Space" ? " " : key, code: key, keyCode: code, which: code, bubbles: true, cancelable: true };
    var down = el.dispatchEvent(new KeyboardEvent("keydown", init));
    el.dispatchEvent(new KeyboardEvent("keyup", init));
    if (!down) return;
    // A synthetic Enter does not submit a form or press a button by itself.
    if (key === "Enter") {
      if (el.tagName === "INPUT" && el.form) { if (el.form.requestSubmit) el.form.requestSubmit(); else el.form.submit(); }
      else if (el.tagName === "BUTTON" || el.tagName === "A") el.click();
    } else if (key === "Space" && (el.tagName === "BUTTON" || el.type === "checkbox")) el.click();
  }

  function describe(step) { return step.do + " " + (step.selector || step.text || step.path || step.key || ""); }

  // Resolves null when the step worked, else why it did not.
  // `acted` is called the moment the step has had its effect: a click that navigates
  // is over for resuming purposes even though the page never gets to finish it.
  function runStep(step, id, acted) {
    if (!localHost(location.hostname)) return Promise.resolve("this page is not on a local address");
    var el;
    switch (step.do) {
      case "click":
        return until(function () { return findClickable(step); }, stepWait, id).then(function (e) {
          if (!e) return "nothing to click was found";
          e.scrollIntoView({ block: "center", inline: "nearest", behavior: "auto" });
          if (e.focus) e.focus();
          e.click();
          acted();
          return sleep(700).then(function () { return null; });
        });
      case "fill":
        return until(function () { return findField(step); }, stepWait, id).then(function (e) {
          if (!e) return "no such field was found";
          if (!setValue(e, str(step.value, 200))) return "that field does not take this value";
          acted();
          return null;
        });
      case "press":
        el = step.selector || step.text ? findClickable(step) || findField(step) : null;
        if ((step.selector || step.text) && !el) return Promise.resolve("no such element was found");
        if (!KEYS[step.key]) return Promise.resolve("unknown key");
        press(el, step.key);
        acted();
        return sleep(700).then(function () { return null; });
      case "wait":
        if (step.selector || step.text) {
          return until(function () { return findAny(step); }, stepWait, id).then(function (e) { if (!e) return "it never appeared"; acted(); return null; });
        }
        return sleep(Math.max(1, Math.min(+step.ms || 0, 3000))).then(function () { return null; });
      case "goto":
        if (typeof step.path !== "string" || step.path.charAt(0) !== "/" || step.path.charAt(1) === "/" || step.path.indexOf("\\") !== -1) {
          return Promise.resolve("not a path on this origin");
        }
        location.assign(location.origin + step.path.slice(0, 500));
        acted();
        return sleep(5000).then(function () { return null; });
      default:
        return Promise.resolve("unknown step");
    }
  }

  // Runs steps[from..]; resolves { failed: null | {index, step, why} } or
  // { superseded: true } when a newer run took over. Each step is reported (onStep,
  // window.__lolaStepDone) once it has had its effect, so a document that a step
  // replaced is resumed AFTER that step, and a step still looking for its target is
  // run again on the new document.
  function runSteps(steps, from, onStep, id) {
    var i = Math.max(0, from | 0);
    return new Promise(function (resolve) {
      (function next() {
        if (id !== token) return resolve({ superseded: true });
        if (i >= steps.length) return resolve({ failed: null });
        var at = i, step = steps[at] && typeof steps[at] === "object" ? steps[at] : {}, reported = false;
        function acted() {
          if (reported || id !== token) return;
          reported = true;
          if (onStep) { try { onStep(at); } catch (err) {} }
          if (typeof window.__lolaStepDone === "function") { try { window.__lolaStepDone(at); } catch (err) {} }
        }
        // A throw inside a step becomes that step's failure, never a promise that hangs.
        Promise.resolve().then(function () { return runStep(step, id, acted); }).then(function (why) {
          if (id !== token) return resolve({ superseded: true });
          if (why) return resolve({ failed: { index: at, step: describe(step), why: why } });
          acted();
          i++;
          next();
        }, function (err) {
          if (id !== token) return resolve({ superseded: true });
          resolve({ failed: { index: at, step: describe(step), why: "it threw: " + String(err && err.message || err).slice(0, 120) } });
        });
      })();
    });
  }

  // Resolves { found: [bool per mark], waited: bool, failed: null | {index, step,
  // why} }: the steps first (from spec.from), then the marks. It resolves once the
  // page is ready or after `timeout` ms, whichever is first; a mark that never
  // appears is reported rather than guessed at. Layout settles (fonts, images), so
  // the outlines are drawn again.
  // The marks as they are right now (the capture asks again just before the shot).
  // Whether the page is ready right now (waitFor present, every mark findable).
  window.__lolaReady = function (spec) {
    return ready(spec && typeof spec === "object" ? spec : {});
  };

  window.__lolaCheck = function (spec) {
    spec = spec && typeof spec === "object" ? spec : {};
    return apply(spec, false);
  };

  window.__lolaPrepare = function (spec, timeout, onStep) {
    spec = spec && typeof spec === "object" ? spec : {};
    var id = ++token;
    stepWait = spec.stepTimeoutMs > 0 ? Math.max(100, Math.min(spec.stepTimeoutMs, 8000)) : 8000;
    var steps = Array.isArray(spec.steps) ? spec.steps.slice(0, 12) : [];
    var marks = Array.isArray(spec.marks) ? spec.marks.slice(0, 4) : [];
    var none = marks.map(function () { return false; });
    return runSteps(steps, spec.from, onStep, id).then(function (sr) {
      if (sr.superseded) return { superseded: true, found: none, waited: false, failed: null };
      if (sr.failed) return { found: none, waited: false, failed: sr.failed };
      var deadline = Date.now() + (timeout > 0 ? Math.min(timeout, 30000) : 10000);
      return new Promise(function (resolve) {
        (function poll() {
          if (id !== token) return resolve({ superseded: true, found: none, waited: false, failed: null });
          var ok = ready(spec);
          if (ok || Date.now() >= deadline) {
            var found = apply(spec, true);
            setTimeout(function () {
              if (id !== token) return resolve({ superseded: true, found: none, waited: false, failed: null });
              found = apply(spec, false);
              resolve({ found: found, waited: !ok, failed: null });
            }, 400);
            return;
          }
          setTimeout(poll, 250);
        })();
      });
    });
  };
})();
