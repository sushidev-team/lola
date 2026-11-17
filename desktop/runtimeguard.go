package main

import "net/http"

// runtimeCallPath is where every bound service call arrives.
const runtimeCallPath = "/wails/runtime"

// runtimeCallGuard keeps a widget from calling lola's services.
//
// A widget is agent-written code in a sandboxed frame, and the sandbox does not stop a frame
// navigating itself. Wails' transport reads the object, method and args from the query string
// when a request has no body, and it checks no method, origin or header. So a page that set
// location to wails://localhost/wails/runtime?object=0&method=...&args=... ran a bound method
// with the arguments it chose, including SSHService.Approve on its own pending command.
//
// A navigation can only be a GET and can set no header, and the runtime's own calls are always
// a POST carrying x-wails-client-id. That is not enough on its own. A navigated page has no CSP,
// and measured in a real WKWebView, its fetch of this path with that header arrives as a POST
// with no preflight. What gives it away is the Origin of "null": a sandboxed frame is an opaque
// origin, and the app's own page sends none here. So that is refused too.
//
// Middleware runs ahead of the transport's handler in application.New. Wails' code decides
// all of this, so TestRuntimeGuardWasReviewedForThisWails fails on an upgrade until someone
// has read the new transport again.
func runtimeCallGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == runtimeCallPath && !isRuntimeCall(r) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isRuntimeCall(r *http.Request) bool {
	return r.Method == http.MethodPost && r.Header.Get("x-wails-client-id") != "" && r.Header.Get("Origin") != "null"
}
