import { nav } from './nav.svelte';

class ReviewWorkspace {
  session = $state('');
  open(id: string) {
    this.session = id.split('~')[0];
    nav.openOverlay('pr-review');
  }
}
export const reviewWorkspace = new ReviewWorkspace();
