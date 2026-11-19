// Asks which further repositories of a stack to check out — before a ticket
// starts without a routing label (TicketPicker), or for a session that already
// runs (SessionMenu). `ask` opens StackAppsDialog and resolves with the names
// the person ticked, or null when they cancelled.

export type AppChoice = {
  name: string;
  label: string;
  /** Already part of the session: shown ticked and fixed. */
  included: boolean;
  /** Why it is included ("selected by a label"), "" for a free choice. */
  note: string;
};

export type AppChooserRequest = {
  title: string;
  intro: string;
  /** The confirm button's label. */
  confirm: string;
  choices: AppChoice[];
  /** The confirm needs at least one ticked choice (adding to a session). */
  requireOne: boolean;
};

class AppChooser {
  request = $state<AppChooserRequest | null>(null);
  #resolve: ((names: string[] | null) => void) | null = null;

  ask(req: AppChooserRequest): Promise<string[] | null> {
    this.#resolve?.(null);
    return new Promise((resolve) => {
      this.#resolve = resolve;
      this.request = req;
    });
  }

  finish(names: string[] | null) {
    const resolve = this.#resolve;
    this.#resolve = null;
    this.request = null;
    resolve?.(names);
  }
}

export const appChooser = new AppChooser();
