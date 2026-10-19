// The project form's Repositories tab: [[project.linked_repo]] rows and the
// label picker that decides which issues get each repository checked out.

export interface LinkedRepoRow {
  name: string;
  path: string;
  repo: string;
  defaultBranch: string;
  labels: string[];
}

export function blankLinkedRepo(): LinkedRepoRow {
  return { name: "", path: "", repo: "", defaultBranch: "", labels: [] };
}

/** "apps / website · workspace" and "Apps/Website" both become "apps/website". */
function norm(label: string): string {
  return label
    .replace(/\s*·\s*workspace$/, "")
    .replace(/\s*\/\s*/g, "/")
    .trim()
    .toLocaleLowerCase();
}

/**
 * The picker's view of a row's labels: a hand-written name ("website") or
 * "group/name" ("apps/website") that names one of the team's labels is shown
 * as that label's id, so it reads as ticked rather than as an unknown entry.
 * The daemon matches all three forms, so nothing is rewritten until a human
 * actually toggles a label — an untouched form stays clean.
 */
export function labelIdsFor(values: string[] | null, options: { id: string; label: string }[]): string[] {
  return (values ?? []).map((v) => {
    if (options.some((o) => o.id === v)) return v;
    const want = norm(v);
    const hit = options.find((o) => {
      const full = norm(o.label);
      return full === want || full.split("/").pop() === want;
    });
    return hit ? hit.id : v;
  });
}
