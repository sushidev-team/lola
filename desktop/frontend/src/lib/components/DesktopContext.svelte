<script lang="ts">
  import { Window } from '@wailsio/runtime';
  import { Publish } from '@bindings/desktop/contextservice';
  import { nav } from '$lib/nav.svelte';
  import { store } from '$lib/store.svelte';
  import { talk } from '$lib/talk.svelte';
  import { reviewWorkspace } from '$lib/reviewworkspace.svelte';
  import type { DesktopContext } from '@bindings/desktop/models';

  // Read the stores here, like SessionEmbed: the cockpit remains mounted behind
  // other views, so its presence alone must never attribute time to a session.
  const context = $derived.by<DesktopContext>(() => {
    const view = store.configChecked && !store.hasConfig ? 'setup' : nav.overlay || nav.view;
    const id = view === 'pr-review' ? reviewWorkspace.session
      : view === 'rename-session' ? nav.overlayProject
      : view === 'cockpit' ? nav.focusedTerm || nav.selectedId : '';
    const session = id ? store.sessionById(id) || talk.ended(id) : undefined;
    if (!session) return { view };
    return {
      view,
      sessionId: session.id,
      title: session.title || ('branch' in session ? session.branch : '') || session.issue || 'Session',
      projectId: session.project,
      projectName: store.displayNameFor(session.project || ''),
      issueKey: session.issue,
    };
  });
  // Equality on the serialized small record avoids writes on every daemon poll.
  const snapshot = $derived(JSON.stringify(context));
  let pending = Promise.resolve();
  $effect(() => {
    const current: DesktopContext = JSON.parse(snapshot);
    const title = current.sessionId
      ? [...new Set([current.issueKey, current.title, current.projectName].filter(Boolean)), 'Lola'].join(' · ')
      : 'Lola';
    // Also set the browser title for development. Stable IDs never enter either title.
    document.title = title;
    // Serialize native calls so rapid switching cannot leave an older session published last.
    pending = pending.then(async () => {
      // Publish before the native title event: trackers may read the snapshot in
      // response to that event. A failed optional write must still allow the title.
      try { await Publish(current); }
      catch (error) { console.warn('Desktop context update failed', error); }
      try { await Window.SetTitle(title); }
      catch (error) { console.warn('Window title update failed', error); }
    });
  });
</script>
