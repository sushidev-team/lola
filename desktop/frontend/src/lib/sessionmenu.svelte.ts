// The one right-click menu for sessions. Any session surface (table row, kanban
// card, terminal tile) opens it at the pointer; SessionMenu.svelte (mounted once
// in App.svelte) renders whatever request is pending, so every surface gets the
// same items without re-wiring the actions per view.
//
// Deliberately dependency-free, like confirm.svelte.ts: call sites push a
// request in, the menu component reads it out and owns the actions. Nothing
// here imports the store, so there is no cycle.

export type SessionMenuRequest = {
  /** Session id the menu acts on. */
  id: string;
  /** Viewport coordinates of the right-click (the menu anchors here). */
  x: number;
  y: number;
};

class SessionMenu {
  request = $state<SessionMenuRequest | null>(null);

  /** Open at the event's pointer position, suppressing the WebKit native menu. */
  open(id: string, e: MouseEvent) {
    e.preventDefault();
    this.request = { id, x: e.clientX, y: e.clientY };
  }

  close() {
    this.request = null;
  }
}

export const sessionMenu = new SessionMenu();
