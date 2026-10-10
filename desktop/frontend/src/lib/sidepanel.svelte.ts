// The session view's sidebar: which tab it shows (the agent's report or the
// turn checkpoints) and whether it is shown at all. Both are layout preferences
// that apply to every session and are remembered per viewer — browser storage
// is the right home, and a storage that throws simply means the defaults.
export const REPORT = "report";
export const CHECKPOINTS_TAB = "checkpoints";
export type SideTab = typeof REPORT | typeof CHECKPOINTS_TAB;

const HIDDEN_PREF = "lola.boardPanel"; // the key predates the checkpoints tab
const TAB_PREF = "lola.sidePanelTab";

function read(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

function write(key: string, value: string) {
  try {
    localStorage.setItem(key, value);
  } catch {
    // per-viewer convenience only
  }
}

class SidePanel {
  hidden = $state(read(HIDDEN_PREF) === "hidden");
  tab = $state<SideTab>(read(TAB_PREF) === CHECKPOINTS_TAB ? CHECKPOINTS_TAB : REPORT);

  toggle() {
    this.hidden = !this.hidden;
    write(HIDDEN_PREF, this.hidden ? "hidden" : "shown");
  }

  /** Show the sidebar on `tab` (the menu's "Checkpoints" lands here). */
  show(tab: SideTab) {
    this.select(tab);
    if (this.hidden) this.toggle();
  }

  select(tab: SideTab) {
    this.tab = tab;
    write(TAB_PREF, tab);
  }
}

export const sidePanel = new SidePanel();
