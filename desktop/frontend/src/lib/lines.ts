// Pure helpers for the multiline list fields (symlinks / post-create / env /
// match labels / priority sort): a config string[] <-> a textarea value, one
// entry per line. Shared by the project and settings overlays, and unit-testable
// without mounting a component or touching the daemon.
//
// Go nil slices arrive as null over the Wails bridge, so every reader tolerates
// null. Editing keeps the raw split (no trimming) so a half-typed line doesn't
// fight the cursor; cleanLines is applied once, on save.

/** Join a config string[] into a textarea value, one entry per line. */
export function linesToText(a: string[] | null | undefined): string {
  return (a ?? []).join("\n");
}

/** Split a textarea value into a config string[]: trim + drop blank lines. */
export function textToLines(s: string): string[] {
  return s
    .split("\n")
    .map((l) => l.trim())
    .filter((l) => l.length > 0);
}

/** Split a textarea value verbatim — what an in-progress edit binds to. */
export function splitLines(s: string): string[] {
  return s.split("\n");
}

/** Clean a list field the way the textarea renders it: trim, drop blanks. */
export function cleanLines(a: string[] | null | undefined): string[] {
  return textToLines(linesToText(a));
}
