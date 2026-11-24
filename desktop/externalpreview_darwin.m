// A standalone WKWebView per external Preview tab. These views have ordinary
// browser navigation and no Wails handlers, proxy cookies or active debugging hooks until the user opens Developer Tools.
#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>
#import <objc/runtime.h>

static BOOL webURL(NSURL *url) {
    return url.host.length && !url.user && !url.password &&
        ([url.scheme.lowercaseString isEqual:@"https"] || [url.scheme.lowercaseString isEqual:@"http"]);
}

static NSArray<NSString *> *observedPageKeys(void) {
    return @[@"URL", @"title", @"canGoBack", @"loading", @"estimatedProgress"];
}

@interface LolaExternalPreview : NSObject <WKScriptMessageHandler, WKScriptMessageHandlerWithReply, WKUIDelegate, WKNavigationDelegate>
@property(nonatomic, weak) WKWebView *host;
@property(nonatomic, copy) NSString *debugSource;
@property(nonatomic, weak) WKWebView *debugPage;
@property(nonatomic, copy) NSString *debugToken;
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
    if ([key isEqual:@"loading"] || [key isEqual:@"estimatedProgress"]) {
        [self send:page data:@{@"type":@"progress", @"loading":@(page.isLoading), @"fraction":@(page.estimatedProgress)}];
        return;
    }
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

// This handler belongs only to the host main frame. All evaluations below
// explicitly target a separate, visible external page, never the Wails view.
- (void)stopDebugging {
    WKWebView *page = self.debugPage;
    if (!page) return;
    [page.configuration.userContentController removeAllUserScripts]; // Only our optional debug bootstrap.
    [page callAsyncJavaScript:@"if(window.__lolaTools?.token === token){window.__lolaTools.run({op:'close'}); delete window.__lolaTools}"
        arguments:@{@"token":self.debugToken} inFrame:nil inContentWorld:WKContentWorld.pageWorld completionHandler:nil];
    self.debugPage = nil;
    self.debugToken = nil;
}

- (void)userContentController:(WKUserContentController *)controller didReceiveScriptMessage:(WKScriptMessage *)msg
                 replyHandler:(void (^)(id, NSString *))reply {
    if (!msg.frameInfo.mainFrame || msg.webView != self.host || ![msg.body isKindOfClass:NSDictionary.class]) {
        reply(nil, @"Invalid preview tools request."); return;
    }
    NSDictionary *args = msg.body;
    NSString *token = args[@"token"];
    WKWebView *page = self.tabs[args[@"id"]];
    if (![token isKindOfClass:NSString.class] || ![[NSUUID alloc] initWithUUIDString:token] || !page) {
        reply(nil, @"Preview tab is unavailable."); return;
    }
    if ([args[@"op"] isEqual:@"open"] && !page.hidden) {
        [self stopDebugging];
        self.debugPage = page;
        self.debugToken = token;
        NSString *source = [NSString stringWithFormat:@"(()=>{ const token='%@'; if(window.__lolaTools?.token === token) return; %@; window.__lolaTools = {token,run:lolaPreviewDebugger()}; })()", [[NSUUID alloc] initWithUUIDString:token].UUIDString.lowercaseString, self.debugSource];
        WKUserScript *script = [[WKUserScript alloc] initWithSource:source injectionTime:WKUserScriptInjectionTimeAtDocumentStart forMainFrameOnly:YES];
        [page.configuration.userContentController addUserScript:script];
        [page evaluateJavaScript:source completionHandler:^(id result, NSError *err) { reply(@YES, err.localizedDescription); }];
        return;
    }
    if (page != self.debugPage || ![token isEqual:self.debugToken]) { reply(nil, @"Preview tools closed."); return; }
    if ([args[@"op"] isEqual:@"close"]) { [self stopDebugging]; reply(@YES, nil); return; }
    if (page.hidden) { reply(nil, @"Preview tab is hidden."); return; }
    [page callAsyncJavaScript:@"if(window.__lolaTools?.token !== cmd.token) return null; const json=JSON.stringify(window.__lolaTools.run(cmd)); if(json.length>8388608) throw Error('Page response is too large'); return json"
        arguments:@{@"cmd":args} inFrame:nil inContentWorld:WKContentWorld.pageWorld
        completionHandler:^(id result, NSError *err) { reply(result, err.localizedDescription); }];
}

- (void)removeTab:(NSNumber *)tab {
    WKWebView *page = self.tabs[tab];
    if (page && page == self.debugPage) [self stopDebugging];
    for (NSString *key in observedPageKeys()) [page removeObserver:self forKeyPath:key];
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
            for (NSString *key in observedPageKeys()) [page addObserver:self forKeyPath:key options:0 context:NULL];
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

void lolaInstallExternalPreview(WKWebView *host, NSString *debugSource) {
    static char key;
    if (!host || objc_getAssociatedObject(host, &key)) return;
    LolaExternalPreview *browser = [LolaExternalPreview new];
    browser.host = host;
    browser.debugSource = debugSource;
    [host.configuration.userContentController addScriptMessageHandlerWithReply:browser contentWorld:WKContentWorld.pageWorld name:@"lolaPreviewTools"];
    browser.tabs = [NSMutableDictionary new];
    [host.configuration.userContentController addScriptMessageHandler:browser name:@"lolaExternalPreview"];
    objc_setAssociatedObject(host, &key, browser, OBJC_ASSOCIATION_RETAIN_NONATOMIC);
}
