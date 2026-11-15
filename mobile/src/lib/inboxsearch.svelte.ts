// Search across the Inbox's sessions, client side: title, issue key, branch and project
// name, with the desktop's own fuzzy match. It composes with the status filter and the
// project grouping because it only narrows the models they already list.
//
// Invariants:
//  - Models that match keep their identity, so the keyed list and its item cache see an
//    unchanged row when only the query moved.
//  - The query is memory only: it is not a route and not a preference, and it clears with
//    the app. The Inbox stays mounted under a pushed session, so a person returning from
//    one finds the search as they left it.
//  - Pure and display only: nothing here reaches the daemon.

import { matchesSearch } from "$lib/search";
import { BUCKETS, type Bucket, type RowModel } from "./inbox.svelte";

class InboxSearch {
  query = $state("");

  /** True while a non-blank query narrows the list. */
  get active(): boolean {
    return this.query.trim() !== "";
  }

  clear(): void {
    this.query = "";
  }
}

export const inboxSearch = new InboxSearch();

/** True when the model's title, issue key, branch or project (id or name) matches every word of `query`. */
export function matchesModel(m: RowModel, query: string, projectLabel: string): boolean {
  return matchesSearch(query, m.session.title, m.session.issue, m.branch, projectLabel, m.project);
}

/** The buckets narrowed to the matches; the same object back for a blank query. */
export function searchBuckets(
  buckets: Record<Bucket, readonly RowModel[]>,
  query: string,
  label: (project: string) => string,
): Record<Bucket, readonly RowModel[]> {
  if (query.trim() === "") return buckets;
  const out = { needs: [], working: [], review: [], done: [] } as Record<Bucket, RowModel[]>;
  for (const b of BUCKETS) out[b] = buckets[b].filter((m) => matchesModel(m, query, label(m.project)));
  return out;
}
