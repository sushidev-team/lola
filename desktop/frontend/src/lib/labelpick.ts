// Label references as picker selections, without rewriting the saved values.

/** "apps / website · workspace" and "Apps/Website" both become "apps/website". */
function norm(label: string): string {
  return label
    .replace(/\s*·\s*workspace$/, "")
    .replace(/\s*\/\s*/g, "/")
    .trim()
    .toLocaleLowerCase();
}

/**
 * The picker's view of labels: a hand-written name ("website") or
 * "group/name" ("apps/website") that names one of the team's labels is shown
 * as that label's id, so it reads as ticked rather than as an unknown entry.
 * Pickup filters also accept * patterns over the full group/name. Ambiguous
 * plain names stay unresolved, just as they do in the daemon.
 * Nothing is rewritten until a human
 * actually toggles a label — an untouched form stays clean.
 */
export function labelIdsFor(values: string[] | null, options: { id: string; label: string }[], patterns = false): string[] {
  return [...new Set((values ?? []).flatMap((v) => {
    if (options.some((o) => o.id === v)) return [v];
    const want = norm(v);
    const glob = patterns && want.includes("*")
      ? new RegExp("^" + want.split("*").map((part) => part.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")).join(".*") + "$")
      : null;
    const hits = options.filter((o) => {
      const full = norm(o.label);
      if (glob) return glob.test(full);
      return full === want || full.split("/").pop() === want;
    });
    return hits.length === 1 || (glob && hits.length) ? hits.map((hit) => hit.id) : [v];
  }))];
}
