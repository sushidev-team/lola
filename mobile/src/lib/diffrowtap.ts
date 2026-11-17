// One-handed line comments: a tap anywhere on a diff row opens that row's
// comment composer, not just the 2.5rem line-number gutter.
//
// Invariants:
//  - ONE delegated `click` listener on the diff body container. Rows get no
//    listeners and no extra buttons, so a 2,000-line diff costs nothing more.
//  - The tap is forwarded to the row's `button.line-gutter` with `.click()`, so
//    the desktop component's own selection logic stays the only owner of it.
//  - A tap on a button, link or field is left alone (the gutter itself, a queued
//    comment's remove button, the composer), and so is a tap that ended a text
//    selection: the person was copying, not commenting.

const INTERACTIVE = "button, a, input, textarea, select, label, [role=button], [role=menuitem]";

export function forwardRowTap(event: MouseEvent): void {
  const target = event.target;
  if (!(target instanceof Element)) return;
  if (target.closest(INTERACTIVE)) return;
  const row = target.closest(".diff-row");
  if (!row) return;
  const sel = typeof getSelection === "function" ? getSelection() : null;
  if (sel && !sel.isCollapsed && row.contains(sel.anchorNode)) return;
  row.querySelector<HTMLButtonElement>("button.line-gutter")?.click();
}

/** A Svelte action: `use:diffRowTap` on the container that holds the rows. */
export function diffRowTap(node: HTMLElement): { destroy: () => void } {
  node.addEventListener("click", forwardRowTap);
  return { destroy: () => node.removeEventListener("click", forwardRowTap) };
}
