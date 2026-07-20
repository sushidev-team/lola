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
@end

static void lolaApplyScale(id window) {
    id webView = nil;
    @try {
        webView = [window valueForKey:@"webView"];
    } @catch (NSException *e) {
        return;
    }
    if (webView == nil || ![webView isKindOfClass:[WKWebView class]]) {
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
