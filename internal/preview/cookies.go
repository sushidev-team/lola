package preview

import (
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// WebKit sends partitioned cookies from a loopback iframe. On HTTP .localhost
// subdomains it can nevertheless omit Secure cookies from document.cookie.
// Keep a browser-managed copy of the app's explicitly script-readable root
// XSRF cookie for that case. The reserved cookie never goes to the dev server.
const xsrfCopy = "lola_preview_readable_xsrf"

func frameCookies(h http.Header, upstreamHost, previewHost string) {
	lines := h.Values("Set-Cookie")
	h.Del("Set-Cookie")
	for _, line := range lines {
		c, err := http.ParseSetCookie(line)
		if err != nil || c.Name == xsrfCopy || (!cookieDomainMatches(upstreamHost, c.Domain) && !cookieDomainMatches(previewHost, c.Domain)) {
			continue
		}
		// Enforce prefix requirements before changing the cookie's attributes.
		if strings.HasPrefix(c.Name, "__Host-") && (!c.Secure || c.Path != "/" || c.Domain != "") ||
			strings.HasPrefix(c.Name, "__Secure-") && !c.Secure {
			continue
		}
		c.Domain = ""
		c.Secure = true
		c.Partitioned = true
		c.SameSite = http.SameSiteNoneMode
		h.Add("Set-Cookie", c.String())
		if c.Name == "XSRF-TOKEN" && c.Path == "/" {
			if c.HttpOnly {
				c.Value, c.MaxAge = "", -1
			}
			c.Name, c.HttpOnly = xsrfCopy, true
			h.Add("Set-Cookie", c.String())
		}
	}
}

func cookieDomainMatches(host, domain string) bool {
	domain = strings.ToLower(strings.TrimPrefix(domain, "."))
	host = strings.ToLower(host)
	return domain == "" || domain == host || net.ParseIP(host) == nil &&
		strings.Contains(domain, ".") && strings.HasSuffix(host, "."+domain)
}

func stripPreviewCookie(r *http.Request) {
	if _, err := r.Cookie(xsrfCopy); err != nil {
		return
	}
	cookies := r.Cookies()
	r.Header.Del("Cookie")
	for _, c := range cookies {
		if c.Name != xsrfCopy {
			r.AddCookie(c)
		}
	}
}

// A same-origin script can request only the token the app made readable.
// Use the document path so the browser applies current path and expiry rules.
func readPreviewXSRF(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	site := r.Header.Get("Sec-Fetch-Site")
	// Native WebKit omits Fetch Metadata on synchronous XHR. Its referrer
	// still identifies the calling preview, even with an origin-only policy.
	ref, err := url.Parse(r.Referer())
	sameOrigin := site == "same-origin" || site == "" && err == nil && ref.Scheme == "http" && ref.Host == r.Host
	if r.Method != http.MethodGet || !sameOrigin {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	c, err := r.Cookie(xsrfCopy)
	token, tokenErr := r.Cookie("XSRF-TOKEN")
	if err == nil && tokenErr == nil && c.Value == token.Value {
		_, _ = io.WriteString(w, c.Value)
	}
}
