// The config overlays (ProjectForm, SettingsForm) hold unsaved edits across
// several tabs, so a blunt nav.closeOverlay() — from a stray Escape or a mis-click
// on the dim backdrop — would silently drop every edit. Each guarded form
// registers its own requestClose here, which runs the dirty check and, when
// dirty, routes the close through the shared confirm dialog. The global Escape
// handler in App.svelte asks THIS coordinator instead of closing outright.
//
// Backdrop click and the header ✕ already reach the form through the Modal's
// onClose; window-level Escape is the one path App owns, so this exists to hand
// that decision back to the form too — keeping all four close paths on one
// function.
//
// Only one overlay is ever mounted at a time, so a single slot is enough.
// unregister is identity-checked because an overlay swap can run the outgoing
// form's onDestroy AFTER the incoming form's onMount, which would otherwise clear
// the live handler.

class OverlayClose {
  #handler: (() => void) | null = null;

  register(fn: () => void) {
    this.#handler = fn;
  }

  unregister(fn: () => void) {
    if (this.#handler === fn) this.#handler = null;
  }

  /**
   * Ask the active overlay to close itself. Returns true when a guarded form
   * handled the request — it decides whether to prompt — and false when nothing
   * is registered, so the caller falls back to closing outright.
   */
  request(): boolean {
    if (!this.#handler) return false;
    this.#handler();
    return true;
  }
}

export const overlayClose = new OverlayClose();
