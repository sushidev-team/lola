//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Cocoa -framework WebKit
#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>

// _setOverrideDeviceScaleFactor: is a private WKWebView SPI (used by Electron,
// Playwright) that forces window.devicePixelRatio to a given value. Wails serves
// its assets over a custom URL scheme, and WKWebView has a long-standing bug
// where it then reports devicePixelRatio = 1 on Retina — so canvas/WebGL content
// (the xterm.js terminals) renders at 1x and macOS upscales it, looking soft.
// Overriding the factor to the screen's real backing scale restores crisp text.
@interface WKWebView (LolaHiDPI)
- (void)_setOverrideDeviceScaleFactor:(CGFloat)factor;
// _setPageZoomFactor: is the WKWebView SPI behind Safari's Cmd+/- (page zoom):
// it shrinks the layout viewport to width/factor and repaints at `factor`, so
// content REFLOWS and a responsive layout keeps fitting the window width. This
// is deliberately NOT -setMagnification: (public), which visually scales the
// rendered surface with a fixed top-left origin and no reflow — past 100% that
// pushes the flight-deck off the right edge of our frameless, scrollbar-less
// window (the reported bug). Same respondsToSelector-guarded SPI approach as
// _setOverrideDeviceScaleFactor: above.
- (void)_setPageZoomFactor:(double)factor;
@end

// lolaWebView pulls the WKWebView out of a Wails NSWindow via KVC. Returns nil
// (never raises) if the key is absent or the value isn't a WKWebView.
static id lolaWebView(id window) {
    id webView = nil;
    @try {
        webView = [window valueForKey:@"webView"];
    } @catch (NSException *e) {
        return nil;
    }
    if (webView == nil || ![webView isKindOfClass:[WKWebView class]]) {
        return nil;
    }
    return webView;
}

static void lolaApplyScale(id window) {
    id webView = lolaWebView(window);
    if (webView == nil) {
        return;
    }
    // Prefer the window's actual screen (correct after a move to a different-DPI
    // display); mainScreen only as a fallback before the window is placed.
    NSScreen *screen = [(NSWindow *)window screen];
    if (screen == nil) {
        screen = [NSScreen mainScreen];
    }
    CGFloat scale = screen ? [screen backingScaleFactor] : 1.0;
    if (scale < 1.0) {
        scale = 1.0;
    }
    // No-op on a 1x display (scale 1); the fix that matters is forcing 2 on a
    // Retina panel where the custom-scheme WKWebView otherwise reports 1.
    if ([webView respondsToSelector:@selector(_setOverrideDeviceScaleFactor:)]) {
        [(WKWebView *)webView _setOverrideDeviceScaleFactor:scale];
    }
}

void lolaFixHiDPI(void *nsWindowPtr) {
    if (nsWindowPtr == NULL) {
        return;
    }
    id window = (__bridge id)nsWindowPtr;
    if ([NSThread isMainThread]) {
        lolaApplyScale(window);
    } else {
        dispatch_async(dispatch_get_main_queue(), ^{
            lolaApplyScale(window);
        });
    }
}

static void lolaApplyPageZoom(id window, double factor) {
    id webView = lolaWebView(window);
    if (webView == nil) {
        return;
    }
    if ([webView respondsToSelector:@selector(_setPageZoomFactor:)]) {
        [(WKWebView *)webView _setPageZoomFactor:factor];
    }
    // Belt-and-braces: zero out any leftover pinch/menu magnification so the two
    // zoom mechanisms can't compound into the very overflow we're fixing.
    if ([webView respondsToSelector:@selector(setMagnification:)]) {
        [(WKWebView *)webView setMagnification:1.0];
    }
}

void lolaSetPageZoom(void *nsWindowPtr, double factor) {
    if (nsWindowPtr == NULL) {
        return;
    }
    id window = (__bridge id)nsWindowPtr;
    if ([NSThread isMainThread]) {
        lolaApplyPageZoom(window, factor);
    } else {
        dispatch_async(dispatch_get_main_queue(), ^{
            lolaApplyPageZoom(window, factor);
        });
    }
}
*/
import "C"

import "unsafe"

// fixHiDPI forces the window's WKWebView to report the screen's real backing
// scale factor, so Retina renders crisply (see the C comment). No-op on a
// non-Retina display (factor stays 1). nsWindow is WebviewWindow.NativeWindow().
func fixHiDPI(nsWindow unsafe.Pointer) {
	if nsWindow != nil {
		C.lolaFixHiDPI(nsWindow)
	}
}

// setPageZoom applies WKWebView page zoom (reflowing, width-fitting — Safari's
// Cmd+/-) to the window, replacing Wails' magnification-based Zoom menu roles.
// See the C comment on _setPageZoomFactor:. No-op on a nil window.
func setPageZoom(nsWindow unsafe.Pointer, factor float64) {
	if nsWindow != nil {
		C.lolaSetPageZoom(nsWindow, C.double(factor))
	}
}
