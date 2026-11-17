// Desktop webview preferences the phone seeds before the shared components read
// them. Storage is a per-viewer convenience: every access tolerates failure.

const DIFF_MODE_KEY = "lola.diff.mode";

/** Seeds the diff mode to "unified" on first boot, never overriding a stored choice. */
export function seedDeskPrefs(): void {
  try {
    if (localStorage.getItem(DIFF_MODE_KEY) === null) localStorage.setItem(DIFF_MODE_KEY, "unified");
  } catch {
    /* storage unavailable: the component default applies */
  }
}
