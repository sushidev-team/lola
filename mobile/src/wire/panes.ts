// The pane inventory and shell creation, mirrored by hand from Go.
//
// Every OTHER Response.Data shape reaches the app as a type from the desktop's
// generated bindings (`@bindings/internal/protocol`), because a DTO is a shape
// and the shape is identical on both platforms — the same daemon produces it.
// These three are the exception, and it is worth saying why rather than leaving
// the next reader to wonder: `protocol.PanesData`, `protocol.PaneInfo` and
// `protocol.ShellCreateData` exist in Go but are ABSENT from the checked-in
// bindings, which have not been regenerated since they landed. Regenerating
// them is a desktop build step and not a mobile change, so the types are
// mirrored here instead, in the same spirit as the rest of this package.
//
// Delete this file the day the bindings carry them, and import from
// `@bindings/internal/protocol` like everything else.
//
// The Go sources are:
//
//   internal/protocol/protocol.go   PaneInfo, PanesData, ShellCreateData
//   internal/daemon/panes.go        the handlers, the pane-kind vocabulary,
//                                   the shell cap and the refusal reasons
//
// Nothing here performs I/O. `normalizePanesData` is pure and is called by the
// shim (mobile/src/wailsshim/desktop.ts) on the decoded payload.

// ---------------------------------------------------------------------------
// The pane-kind vocabulary
// ---------------------------------------------------------------------------

/**
 * The ROLE of a pane, as the daemon reports it.
 *
 * These four strings are the wire contract, mirroring the `paneKind*` constants
 * in internal/daemon/panes.go. lola's actual naming convention — `<id>-shell-N`,
 * `<id>-dev-N`, `<id>-review` — lives in internal/runtime and internal/devtab
 * and is deliberately not a client's business: the daemon classifies, the client
 * groups and labels.
 */
export const PANE_KIND_AGENT = "agent";
export const PANE_KIND_SHELL = "shell";
export const PANE_KIND_DEV = "dev";
export const PANE_KIND_REVIEW = "review";

export const PANE_KINDS = [
  PANE_KIND_AGENT,
  PANE_KIND_SHELL,
  PANE_KIND_DEV,
  PANE_KIND_REVIEW,
] as const;

export type PaneKind = (typeof PANE_KINDS)[number];

/**
 * Whether `v` is a kind THIS BUILD knows.
 *
 * It exists because `PaneInfo.kind` is typed as the union above, which is the
 * vocabulary the daemon ships today and not a promise about tomorrow: a phone
 * on the App Store outlives the Mac's daemon build, so a fifth kind will
 * eventually arrive at a client that has never heard of it. A tab strip must
 * render such a pane as a plain tab using its `label` — never drop it, and never
 * let a `switch` decide it cannot happen. Narrow with this before switching.
 */
export function isPaneKind(v: string): v is PaneKind {
  return (PANE_KINDS as readonly string[]).includes(v);
}

// ---------------------------------------------------------------------------
// The types
// ---------------------------------------------------------------------------

/**
 * One pane of a session. Mirrors `protocol.PaneInfo`.
 *
 * `label` is the daemon's own human-readable name for the tab ("agent",
 * "shell 2", "dev 1", "review") and is what a strip should draw. `index` is the
 * N of a numbered tab, and is ABSENT for the agent and review panes — the Go
 * field is `json:"index,omitempty"` and 0 is its zero value, so the field is
 * simply not written for them.
 */
export interface PaneInfo {
  name: string;
  kind: PaneKind;
  label: string;
  index?: number;
}

/**
 * Which panes exist for a session. Mirrors `protocol.PanesData`.
 *
 * `panes` is ALREADY in the order a tab strip should draw it: the agent first,
 * then shells and dev tabs in index order, then the review pane. Do not re-sort
 * it — the ordering is the daemon's, and it is derived from tmux on every call
 * rather than read from the session record, so it is the live truth about what
 * exists rather than a cache that offers tabs that are gone.
 *
 * `review` is a CONVENIENCE POINTER at the review pane, which — when it exists —
 * is also the last entry of `panes`. Draw the strip from `panes`; reach for
 * `review` only when a view wants that one pane specifically.
 *
 * `canCreateShell` is the daemon's answer to "may another shell be started", and
 * it is the ONLY correct source for that: it folds together a cap this file
 * deliberately does not mirror and whether the session still has a worktree. Do
 * not re-derive it by counting shells.
 *
 * Two encoding details this shape smooths over, both handled by
 * `normalizePanesData` rather than by every call site:
 *
 *   - The Go field is `Panes []PaneInfo json:"panes"` with NO omitempty, so a
 *     session whose tmux shows nothing encodes as `"panes": null`. The generated
 *     bindings' `createFrom` normalizes a missing slice for every other Data
 *     type; there is no generated class here to do it, so the shim does.
 *   - The Go field is `Review PaneInfo json:"review,omitempty"`, and omitempty
 *     HAS NO EFFECT ON A STRUCT — it applies to false, 0, nil pointers and empty
 *     strings, slices and maps, never to a struct value. So `review` is always
 *     present on the wire and a session with no review pane carries the zero
 *     PaneInfo, `{"name":"","kind":"","label":""}`. Rendering that verbatim is a
 *     nameless tab, so it is normalized to absent here.
 */
export interface PanesData {
  session: string;
  panes: PaneInfo[];
  review?: PaneInfo;
  canCreateShell: boolean;
}

/**
 * The shell tab that was started. Mirrors `protocol.ShellCreateData`.
 *
 * `pane` is the name to subscribe to, like any other pane. The INDEX is
 * allocated by the daemon and never asked for by the caller: the desktop lets
 * its own frontend own the name because both run in one process on one machine,
 * and two phones and a desktop racing for "-shell-2" do not have that luxury.
 * A client that invents a name is a client that collides with another client.
 */
export interface ShellCreateData {
  session: string;
  pane: string;
  index: number;
}

// ---------------------------------------------------------------------------
// Normalization
// ---------------------------------------------------------------------------

/**
 * The decoded `cmd=panes` payload, before normalization: exactly what the
 * daemon's encoder can produce, nullable slice and zero-struct review included.
 */
export interface RawPanesData {
  session?: string;
  panes?: PaneInfo[] | null;
  review?: PaneInfo | null;
  canCreateShell?: boolean;
}

/**
 * Turn a decoded `cmd=panes` payload into the shape declared above.
 *
 * It fails toward FEWER tabs, never a wrong one: a pane with no `name` cannot be
 * subscribed to and cannot be identified, so it is dropped rather than drawn as
 * a tab that does nothing when tapped. Everything else is passed through
 * untouched — an unrecognized `kind` included, because the daemon is the
 * authority on the vocabulary and a newer one is a tab this build should still
 * draw from its label.
 *
 * `session` falls back to the id that was ASKED for. The daemon echoes it, but a
 * view keying its state off the answer must not end up with "" if a future
 * daemon stops echoing it.
 *
 * `canCreateShell` FAILS CLOSED — anything but a literal `true` is "no". An
 * absent field hides the "+" button, which costs one tab a user can add from the
 * Mac; a permissive default offers a button whose only outcome is a refusal.
 */
export function normalizePanesData(session: string, raw: RawPanesData | null | undefined): PanesData {
  const panes = (raw?.panes ?? []).filter((p): p is PaneInfo => !!p && typeof p.name === "string" && p.name !== "");
  const review = raw?.review && raw.review.name ? raw.review : undefined;
  return {
    session: raw?.session || session,
    panes,
    review,
    canCreateShell: raw?.canCreateShell === true,
  };
}

// ---------------------------------------------------------------------------
// Closing a pane
// ---------------------------------------------------------------------------

/**
 * The answer to `cmd=paneClose`. Mirrors `protocol.PaneCloseData`.
 *
 * `closed` is always true on the success path -- the daemon returns an error
 * rather than `closed:false` for every refusal it has -- so it is a confirmation
 * to log, never a branch to take. The interesting half of this command is its
 * ERROR path, and there are three refusals a client must not paper over:
 *
 *   * the AGENT pane, which is the session itself. `handlePaneClose` refuses it
 *     outright, so a strip must not offer a close on that tab at all: a control
 *     whose only possible outcome is a refusal is worse than an absent one.
 *   * a pane that belongs to another session, which is the check that stops one
 *     device closing another session's tab by naming it.
 *   * a tmux that would not take the kill.
 *
 * Each arrives as a `DaemonError` carrying the daemon's own sentence. Show it.
 *
 * There is no normalizer and none is needed: every field is a scalar and nothing
 * here is rendered the way an empty pane name would be.
 */
export interface PaneCloseData {
  session: string;
  pane: string;
  closed: boolean;
}

// ---------------------------------------------------------------------------
// Pinning a pane to a phone's size
// ---------------------------------------------------------------------------

/**
 * The answer to `cmd=paneResize`. Mirrors `protocol.PaneResizeData`.
 *
 * `pinned` REPORTS WHICH WAY THE CALL WENT and is the only field worth reading
 * for that. `cols`/`rows` are `json:",omitempty"` in Go, so a release answers
 * with both absent and a client inferring the direction from their presence
 * would read a release as a pin the moment tmux hands back a zero. The Go
 * comment on the type says exactly this; it is repeated here because this is
 * the side that would get it wrong.
 *
 * There is no normalizer for this shape and it needs none: every field is a
 * scalar, an absent `cols` is genuinely "not reported", and nothing here is
 * rendered as a tab the way an empty pane name would be.
 */
export interface PaneResizeData {
  session: string;
  pane: string;
  pinned: boolean;
  cols?: number;
  rows?: number;
}
