import type { ReviewPackage, ReviewPage } from '@bindings/internal/protocol';
import { prepOf } from './preview.svelte';
import { nav } from './nav.svelte';

/** Stack review packages use session~app IDs. The live workspace and its dev servers belong to the session. */
export function reviewSessionId(id: string): string {
  return id.split('~')[0];
}

/** Published pages come first so an overview link can select its exact recipe, even
 * when several recipes start at /login. Screens can also publish their own recipe. */
export function reviewLivePages(pkg: ReviewPackage) {
  const published = pkg.pages ?? [];
  const pages = [...published];
  const screenPages = (pkg.screens ?? []).map(screen => {
    if (!screen.url) return -1;
    const candidates = published.filter(page => page.url === screen.url);
    const match = candidates.length === 1 ? candidates[0] : candidates.find(page => page.title === screen.title);
    const prep = prepOf(screen);
    if (!prep && !screen.selector && match) return published.indexOf(match);
    if (!prep && !match && candidates.length > 1) return -1;

    // A screenshot can add a different highlight to a page that owns the login.
    const page: ReviewPage = { ...match, ...screen, url: screen.url };
    if (!page.marks?.length && screen.selector) page.marks = [{ selector: screen.selector }];
    const recipe = JSON.stringify(prepOf(page));
    const existing = pages.findIndex(p => p.url === page.url && JSON.stringify(prepOf(p)) === recipe);
    return existing >= 0 ? existing : pages.push(page) - 1;
  });
  return { pages, screenPages };
}

class ReviewWorkspace {
  session = $state('');
  tab = $state<'material' | 'code'>('material');
  turn = $state(0);
  open(id: string, tab: 'material' | 'code' = 'material', turn = 0) {
    this.tab = tab;
    this.turn = turn;
    this.session = reviewSessionId(id);
    nav.openOverlay('pr-review');
  }
}
export const reviewWorkspace = new ReviewWorkspace();
