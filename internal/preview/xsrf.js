// Laravel clients read XSRF-TOKEN immediately before a request. Some
// WebKit versions send partitioned loopback cookies but hide them from JS.
// Use the browser's current token on these pages, including after rotation.
(function () {
  var cookie = Object.getOwnPropertyDescriptor(Document.prototype, "cookie");
  if (cookie && cookie.get && cookie.set) {
    Object.defineProperty(document, "cookie", {
      configurable: true,
      set: function (value) { cookie.set.call(document, value); },
      get: function () {
        var visible = cookie.get.call(document);
        if (/(^|;\s*)XSRF-TOKEN=/.test(visible)) return visible;
        try {
          var req = new XMLHttpRequest();
          req.open("GET", location.href, false);
          req.setRequestHeader("X-Lola-Preview-Read-XSRF", "1");
          req.send();
          if (req.status === 200 && req.responseText) {
            return (visible ? visible + "; " : "") + "XSRF-TOKEN=" + req.responseText;
          }
        } catch (e) {}
        return visible;
      }
    });
  }
})();
