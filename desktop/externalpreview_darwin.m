// A standalone WKWebView per external Preview tab. These views have ordinary
// browser navigation and no Wails handlers, proxy cookies or injected scripts.
#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>
#import <objc/runtime.h>

static BOOL webURL(NSURL *url) {
    return url.host.length && !url.user && !url.password &&
        ([url.scheme.lowercaseString isEqual:@"https"] || [url.scheme.lowercaseString isEqual:@"http"]);
}

@interface LolaExternalPreview : NSObject <WKScriptMessageHandler, WKUIDelegate, WKNavigationDelegate>
@property(nonatomic, weak) WKWebView *host;
@property(nonatomic, strong) NSMutableDictionary<NSNumber *, WKWebView *> *tabs;
@property(nonatomic, strong) WKWebsiteDataStore *siteData;
@end

@implementation LolaExternalPreview
- (void)send:(WKWebView *)page data:(NSDictionary *)data {
    NSNumber *tab = [self.tabs allKeysForObject:page].firstObject;
    if (!tab) return;
    NSMutableDictionary *detail = data.mutableCopy;
    detail[@"id"] = tab;
    [self.host callAsyncJavaScript:@"window.dispatchEvent(new CustomEvent('lola-external-preview', {detail}))"
        arguments:@{@"detail":detail} inFrame:nil inContentWorld:WKContentWorld.pageWorld completionHandler:nil];
}

- (void)observeValueForKeyPath:(NSString *)key ofObject:(WKWebView *)page change:(NSDictionary *)change context:(void *)context {
    [self send:page data:@{@"type":@"location", @"url":page.URL.absoluteString ?: @"",
                            @"title":page.title ?: @"", @"back":@(page.canGoBack)}];
}

- (void)webView:(WKWebView *)page didFinishNavigation:(WKNavigation *)navigation {
    NSURL *url = page.URL;
    if (!webURL(url)) return;
    [page evaluateJavaScript:@"document.querySelector('link[rel~=\"icon\" i][href]')?.href?.slice(0, 32769) || ''"
        inFrame:nil inContentWorld:WKContentWorld.defaultClientWorld completionHandler:^(id icon, NSError *error) {
        if (!error && [page.URL isEqual:url] && [icon isKindOfClass:NSString.class])
            [self send:page data:@{@"type":@"favicon", @"url":url.absoluteString, @"icon":icon}];
    }];
}

- (void)webView:(WKWebView *)page decidePolicyForNavigationAction:(WKNavigationAction *)action
        decisionHandler:(void (^)(WKNavigationActionPolicy))decide {
    NSURL *url = action.request.URL;
    if (action.targetFrame && action.navigationType == WKNavigationTypeLinkActivated &&
        ((action.modifierFlags & NSEventModifierFlagCommand) || action.buttonNumber == 2) && webURL(url)) {
        if (!page.hidden) [self send:page data:@{@"type":@"new-tab", @"url":url.absoluteString}];
        decide(WKNavigationActionPolicyCancel);
        return;
    }
    decide((webURL(url) || (!action.targetFrame.mainFrame && [url.absoluteString isEqual:@"about:blank"]))
           ? WKNavigationActionPolicyAllow : WKNavigationActionPolicyCancel);
}

- (WKWebView *)webView:(WKWebView *)page createWebViewWithConfiguration:(WKWebViewConfiguration *)configuration
    forNavigationAction:(WKNavigationAction *)action windowFeatures:(WKWindowFeatures *)features {
    NSURL *url = action.request.URL;
    if (!page.hidden && webURL(url)) [self send:page data:@{@"type":@"new-tab", @"url":url.absoluteString}];
    return nil;
}

- (void)webView:(WKWebView *)page didFailProvisionalNavigation:(WKNavigation *)navigation withError:(NSError *)error {
    if (![error.domain isEqual:NSURLErrorDomain] || error.code != NSURLErrorCancelled)
        [self send:page data:@{@"type":@"error", @"message":error.localizedDescription}];
}

- (void)removeTab:(NSNumber *)tab {
    WKWebView *page = self.tabs[tab];
    for (NSString *key in @[@"URL", @"title", @"canGoBack"]) [page removeObserver:self forKeyPath:key];
    [page stopLoading];
    [page removeFromSuperview];
    [self.tabs removeObjectForKey:tab];
    if (!self.tabs.count) self.siteData = nil;
}

- (void)userContentController:(WKUserContentController *)controller didReceiveScriptMessage:(WKScriptMessage *)message {
    if (!message.frameInfo.mainFrame || message.webView != self.host || ![message.body isKindOfClass:NSDictionary.class]) return;
    NSDictionary *args = message.body;
    NSNumber *tab = args[@"id"];
    if (![tab isKindOfClass:NSNumber.class]) return;
    NSString *action = args[@"action"];
    WKWebView *page = self.tabs[tab];
    if ([action isEqual:@"close"]) { [self removeTab:tab]; return; }
    if ([action isEqual:@"hide"]) { page.hidden = YES; return; }
    if ([action isEqual:@"back"]) { if (page.canGoBack) [page goBack]; return; }
    if ([action isEqual:@"reload"]) { [page reload]; return; }
    if ([action isEqual:@"load"]) {
        NSURL *url = [args[@"url"] isKindOfClass:NSString.class] ? [NSURL URLWithString:args[@"url"]] : nil;
        if (!webURL(url)) return;
        if (!page) {
            WKWebViewConfiguration *config = [WKWebViewConfiguration new];
            if (!self.siteData) self.siteData = [WKWebsiteDataStore nonPersistentDataStore];
            config.websiteDataStore = self.siteData;
            page = [[WKWebView alloc] initWithFrame:NSZeroRect configuration:config];
            page.hidden = YES;
            page.UIDelegate = self;
            page.navigationDelegate = self;
            self.tabs[tab] = page;
            [self.host addSubview:page];
            for (NSString *key in @[@"URL", @"title", @"canGoBack"]) [page addObserver:self forKeyPath:key options:0 context:NULL];
        }
        [page loadRequest:[NSURLRequest requestWithURL:url]];
    } else if ([action isEqual:@"place"] && page) {
        NSRect bounds = self.host.bounds;
        double width = [args[@"viewport"] doubleValue];
        if (width <= 0) return;
        double scale = NSWidth(bounds) / width;
        NSRect rect = NSMakeRect([args[@"x"] doubleValue]*scale, [args[@"y"] doubleValue]*scale,
                                 [args[@"width"] doubleValue]*scale, [args[@"height"] doubleValue]*scale);
        if (!self.host.isFlipped) rect.origin.y = NSHeight(bounds) - NSMaxY(rect);
        page.frame = NSIntersectionRect(bounds, rect);
        page.hidden = NSIsEmptyRect(page.frame);
    }
}
@end

void lolaInstallExternalPreview(WKWebView *host) {
    static char key;
    if (!host || objc_getAssociatedObject(host, &key)) return;
    LolaExternalPreview *browser = [LolaExternalPreview new];
    browser.host = host;
    browser.tabs = [NSMutableDictionary new];
    [host.configuration.userContentController addScriptMessageHandler:browser name:@"lolaExternalPreview"];
    objc_setAssociatedObject(host, &key, browser, OBJC_ASSOCIATION_RETAIN_NONATOMIC);
}
