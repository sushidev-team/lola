// The terminal's view state, shared by the Terminal segment and the sheets that
// are hosted outside it (view settings, pane menu). Spec: docs/mobile/04-client.md
// §2.6 bug 4, §5.3.
//
// Invariants:
//  - `geom` and `capacity` are assigned only when a field differs. Readers
//    re-run on every assignment, and the size pin's settle timer restarts on
//    each one, so an identical report from the terminal must change nothing.
//  - `capacity` is the UNCLAMPED capacity (`capCols`/`capRows`), never
//    `shown`/`shownRows`: those are clamped to the grid the Mac sends, and
//    pinning to them makes each pin depend on the last one's result.
//  - The pin is on when the human turned it on OR the pane is a shell this
//    phone started; turning it off reaches both.
//  - One instance. It is reset when the segment goes away so nothing from a
//    previous session's terminal is shown on the next.

import {
  DEFAULT_MODES,
  type TerminalModes,
} from "@mobile/lib/keybytes";
import { loadFontSize } from "@mobile/lib/prefs";
import {
  forgetOwnShell,
  isOwnShell,
  loadPinEnabled,
  savePinEnabled,
} from "@mobile/lib/panepinstore";
import { sameGeometry, type GridGeometry } from "@mobile/lib/viewport";

/** The text's inline padding, in px; 8 is the floor (see `terminalInset`). */
export const INSET_PX = 12;
export const INSET_MIN_PX = 8;

/**
 * Columns that fit a measured frame width: `floor((width - 2 * inset) / cell)`.
 * Never wider than what is measured, and 0 for anything unmeasured.
 */
export function fitCols(width: number, cellWidth: number, inset: number = INSET_PX): number {
  if (!(width > 0) || !(cellWidth > 0)) return 0;
  return Math.max(0, Math.floor((width - 2 * inset) / cellWidth));
}

/**
 * The inline padding for a frame: 12px, or 8px when 12 would drop a column
 * below the Mac's (pinned) grid that 8 still shows whole. Never anything else.
 */
export function terminalInset(width: number, cellWidth: number, gridCols: number): number {
  if (!(gridCols > 0) || !(cellWidth > 0)) return INSET_PX;
  if (fitCols(width, cellWidth, INSET_PX) >= gridCols) return INSET_PX;
  return fitCols(width, cellWidth, INSET_MIN_PX) >= gridCols ? INSET_MIN_PX : INSET_PX;
}

/** What the terminal component reports on every state change. */
export interface GridReport extends GridGeometry {
  modes: TerminalModes;
  capCols: number;
  capRows: number;
  font: number;
}

/** The two controls the view-settings sheet drives on the live terminal. */
export interface TerminalActions {
  setFont(size: number): void;
  toggleFit(): void;
}

const NO_GEOMETRY: GridGeometry = {
  cols: 0,
  rows: 0,
  shown: 0,
  shownRows: 0,
  first: 1,
  panning: false,
  canFit: false,
  fitActive: false,
  fitSize: 0,
};

const pick = (r: GridGeometry): GridGeometry => ({
  cols: r.cols,
  rows: r.rows,
  shown: r.shown,
  shownRows: r.shownRows,
  first: r.first,
  panning: r.panning,
  canFit: r.canFit,
  fitActive: r.fitActive,
  fitSize: r.fitSize,
});

export class TerminalView {
  // `$state.raw`: whole-object replacement is the only change, and a proxy
  // would hide whether an assignment happened.
  geom = $state.raw<GridGeometry>(NO_GEOMETRY);
  capacity = $state.raw({ cols: 0, rows: 0 });
  modes = $state.raw<TerminalModes>(DEFAULT_MODES);
  font = $state(loadFontSize());
  /** The attached pane, for the auto-pin of shells this phone started. */
  pane = $state("");
  pinEnabled = $state(loadPinEnabled());
  actions = $state.raw<TerminalActions | null>(null);
  private epoch = $state(0);

  /** Takes a report from the terminal. Returns true when `geom` was replaced. */
  apply(r: GridReport): boolean {
    this.modes = r.modes;
    this.font = r.font;
    if (this.capacity.cols !== r.capCols || this.capacity.rows !== r.capRows) {
      this.capacity = { cols: r.capCols, rows: r.capRows };
    }
    if (sameGeometry(this.geom, r)) return false;
    this.geom = pick(r);
    return true;
  }

  /** The pin as it acts: the toggle, or a shell this phone started. */
  get pinActive(): boolean {
    void this.epoch;
    return this.pinEnabled || isOwnShell(this.pane);
  }

  setPin(on: boolean): void {
    this.pinEnabled = on;
    savePinEnabled(on);
    if (!on) forgetOwnShell(this.pane);
    this.epoch++;
  }

  /** The attached pane changed: its measurement is stale until the next report. */
  switchPane(pane: string): void {
    this.pane = pane;
    this.capacity = { cols: 0, rows: 0 };
    this.epoch++;
  }

  reset(): void {
    this.geom = NO_GEOMETRY;
    this.capacity = { cols: 0, rows: 0 };
    this.modes = DEFAULT_MODES;
    this.font = loadFontSize();
    this.pane = "";
    this.pinEnabled = loadPinEnabled();
    this.actions = null;
    this.epoch++;
  }
}

export const terminalView = new TerminalView();
