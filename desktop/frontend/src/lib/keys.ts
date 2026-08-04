// isChord reports whether a key event carries an OS/webview modifier, i.e. is a
// chord the app must NOT read as one of its bare-letter shortcuts.
//
// Every global shortcut is a BARE key ('c', 'x', 's', ','…), so without this
// guard Cmd-C fired "coderabbit review" instead of Copy and Cmd-X opened the
// kill-session confirmation instead of Cut. Cmd/Ctrl/Alt chords belong to the
// platform (copy/paste/select-all/find, Preferences on Cmd-,) or to the webview.
//
// Shift is deliberately NOT a modifier here: 'V', 'G', 'N', 'S', 'R', 'P' and
// '?' ARE the shortcuts — bailing on Shift would unbind half the key model.
export function isChord(e: Pick<KeyboardEvent, "metaKey" | "ctrlKey" | "altKey">): boolean {
  return e.metaKey || e.ctrlKey || e.altKey;
}
