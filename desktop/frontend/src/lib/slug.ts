/**
 * Project id slugging — the TypeScript mirror of Go's `config.Slug` /
 * `config.SlugTyping` (internal/config/slug.go).
 *
 * It is duplicated rather than called over the Wails bridge because the id is
 * derived on every keystroke; a round-trip per character would make the field
 * feel dead. Go remains the authority — ConfigService.SaveProject re-slugs
 * whatever arrives, so a drift here can never write an unsafe name to disk, it
 * can only make the preview disagree with the result. Keep the two in step;
 * slug.test.ts asserts the same cases as the Go test.
 */

/** Characters that may not appear in an id; each run collapses to one "-". */
const INVALID = /[^a-z0-9._-]+/g;

/**
 * Reduce free text to a project id: one path-safe segment usable verbatim as a
 * directory name, a filename and part of a tmux session name.
 *
 * Returns "" when the input reduces to nothing (or to a traversal segment).
 * Callers must treat that as "no usable id" — never substitute a placeholder,
 * since a project silently named "project" is worse than an error.
 */
export function slug(s: string): string {
  const out = s.trim().toLowerCase().replace(INVALID, "-").replace(/^[-.]+|[-.]+$/g, "");
  return out === "." || out === ".." ? "" : out;
}

/**
 * slug's incremental half, for normalizing an id WHILE it is typed: same
 * lowercasing and collapsing, but no edge trimming. Trimming mid-typing would
 * eat the separator the moment it is typed — "nori-" would snap back to "nori"
 * and the next keystroke would land as "noria", making a hyphenated id
 * impossible to enter.
 *
 * Always run `slug` before saving: this can leave a trailing "-" or ".".
 */
export function slugTyping(s: string): string {
  return s.toLowerCase().replace(INVALID, "-");
}

/** Whether s is already exactly its own slug, i.e. safe to use as an id as-is. */
export function isSlug(s: string): boolean {
  return s !== "" && slug(s) === s;
}

/** The display string for a project: its label when set, otherwise its id. */
export function displayName(p: { name: string; label?: string }): string {
  const l = (p.label ?? "").trim();
  return l === "" ? p.name : l;
}
