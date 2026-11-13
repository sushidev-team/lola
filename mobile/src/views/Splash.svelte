<script lang="ts">
  import LolaLogo from "$lib/components/LolaLogo.svelte";

  // The bridge between the NATIVE launch screen and the first real screen.
  //
  // iOS shows LaunchScreen.storyboard — the wordmark on the brand ground — from
  // the moment the icon is tapped until the WebView has painted. Apple's rule is
  // that the launch screen match the first thing the app draws, so the seam is
  // invisible; this view is the web half of that match, which is why it repeats
  // the storyboard's layout rather than introducing a design of its own. The
  // ground is `bg-canvas` and the storyboard is #22242F: those agree on the dark
  // flavors, and on Latte the transition is a deliberate lightening rather than
  // a flash, because a launch screen cannot read a theme.
  //
  // It exists because the boot path is ASYNCHRONOUS. `connection.restore()` has
  // to reach the keychain and then dial the daemon, and until both answer the
  // app has no idea whether it is about to show a session list or a connect
  // form. Rendering the form meanwhile — which is what it used to do — flashes
  // a screen asking for credentials at someone who has already supplied them,
  // and then replaces it a second later.
  //
  // `message` is what the app is actually waiting on. It is not decoration: a
  // boot that stalls is the case where a person needs to know whether to wait,
  // and "connecting to 127.0.0.1:7717" and "checking for a saved connection"
  // fail very differently.
  let { message = "" }: { message?: string } = $props();
</script>

<div class="flex h-full w-full flex-col items-center justify-center gap-6 bg-canvas px-8">
  <LolaLogo class="w-44 max-w-[60%]" />

  <!-- The status line holds its height whether or not there is a message, so the
       mark does not shift when one arrives. aria-live so a screen reader hears
       the change without the view stealing focus. -->
  <p class="min-h-5 text-center text-sm text-faint" role="status" aria-live="polite">
    {message}
  </p>
</div>
