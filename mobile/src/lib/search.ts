// Free-text filtering over the session list.
//
// A phone list has no columns to sort by and no room for the desktop's project
// sidebar, so the one control that replaces both is a search field. It is
// deliberately dumb - a case-insensitive substring over the fields a person
// actually remembers - because anything cleverer (fuzzy matching, ranking)
// reorders the list under a thumb, and the list's order is already meaningful:
// `sortSessions` puts what needs a human first.

/** The fields a search looks at. A subset of SessionInfo, so tests need no DTO. */
export interface Searchable {
  id: string;
  issue: string;
  title: string;
  project: string;
  branch?: string;
}

/** Fields are joined with a separator no query can contain, so a match can
 *  never straddle two of them ("bar" ending the title and "lola" starting the
 *  project must not answer a search for "barlola"). */
const SEP = "\u0000";

/**
 * Does this session match? An empty query matches everything.
 *
 * `project` is matched on the configured NAME as well as whatever display label
 * the caller resolves: a project has two names - `Name` is identity, `Label` is
 * display - and a person may remember either.
 */
export function matchesQuery(s: Searchable, query: string, projectLabel = ""): boolean {
  const q = query.trim().toLowerCase();
  if (q === "") return true;
  const hay = [s.issue, s.title, s.project, projectLabel, s.branch ?? "", s.id]
    .join(SEP)
    .toLowerCase();
  return hay.includes(q);
}

/** Apply a query to an already-scoped, already-sorted list. Order is preserved. */
export function searchSessions<T extends Searchable>(
  list: T[],
  query: string,
  labelFor: (project: string) => string = () => "",
): T[] {
  if (query.trim() === "") return list;
  return list.filter((s) => matchesQuery(s, query, labelFor(s.project)));
}
