// The one confirmation path for destructive actions.
//
// Before this, killing a session opened a dialog but stopping the daemon — which
// halts every poll — fired on a single unguarded click, and a third action
// (removing a project) used its own inline two-step. One request object, one
// dialog, so every irreversible action asks the same way and the Escape/Enter
// handling lives in exactly one place (App.svelte's key handler).
//
// Deliberately dependency-free: components and the store push requests in, the
// dialog reads them out. Nothing here imports the store, so the store can own
// the action helpers without a cycle.

export type ConfirmRequest = {
  /** Dialog title, e.g. "Kill session?" */
  title: string;
  /** The question, as a sentence naming the target. */
  body: string;
  /** Optional second line spelling out the consequence. */
  detail?: string;
  /** Label for the confirming button, e.g. "Kill". */
  confirmLabel: string;
  /** Run on accept. Kept as a closure so the caller decides what "yes" means. */
  onConfirm: () => void;
};

class Confirm {
  request = $state<ConfirmRequest | null>(null);

  ask(r: ConfirmRequest) {
    this.request = r;
  }

  cancel() {
    this.request = null;
  }

  /** Clear FIRST, then run: the action may itself open a dialog or navigate. */
  accept() {
    const r = this.request;
    this.request = null;
    r?.onConfirm();
  }
}

export const confirm = new Confirm();
