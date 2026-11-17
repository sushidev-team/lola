// lola hub console: progressive enhancement only. Every form works without it.
(function () {
  "use strict";
  // Confirm destructive actions.
  document.addEventListener("click", function (e) {
    var b = e.target.closest("[data-confirm]");
    if (b && !window.confirm(b.getAttribute("data-confirm"))) {
      e.preventDefault();
    }
  });
  // Copy buttons.
  document.querySelectorAll("[data-copy]").forEach(function (b) {
    b.addEventListener("click", function () {
      var el = document.getElementById(b.getAttribute("data-copy"));
      if (!el) return;
      el.select();
      var done = function () {
        var old = b.textContent;
        b.textContent = "Copied";
        setTimeout(function () { b.textContent = old; }, 1500);
      };
      if (navigator.clipboard) {
        navigator.clipboard.writeText(el.value).then(done, function () { document.execCommand("copy"); done(); });
      } else {
        document.execCommand("copy");
        done();
      }
    });
  });
  // Show only the fields of the selected chore kind.
  document.querySelectorAll("[data-kind-form]").forEach(function (f) {
    var sel = f.querySelector("[data-kind-select]");
    var sync = function () {
      f.querySelectorAll("fieldset[data-kind]").forEach(function (fs) {
        var on = fs.getAttribute("data-kind") === sel.value;
        fs.hidden = !on;
        fs.disabled = !on;
      });
    };
    sel.addEventListener("change", sync);
    sync();
  });
})();
