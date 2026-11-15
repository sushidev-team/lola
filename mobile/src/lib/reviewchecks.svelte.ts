// Which pull request the Review segment last read readiness for, so the checks sheet
// lists the same PR (a stack lane included) without guessing. The answer itself lives
// in the shared readiness cache (`readPRReadinessRecent`); this holds only its key.
// Display only: nothing here decides a merge or a send.

export interface ReviewChecksTarget {
  /** The session whose Review segment read it. */
  session: string;
  /** The session or stack lane (`<id>~<app>`) the read was for. */
  target: string;
  number: number;
}

class ReviewChecks {
  last = $state<ReviewChecksTarget | null>(null);

  /** The PR to list for `session`: the segment's last read, else the session's own PR. */
  targetFor(session: string, prNumber: number): ReviewChecksTarget | null {
    if (this.last && this.last.session === session) return this.last;
    return prNumber > 0 ? { session, target: session, number: prNumber } : null;
  }
}

export const reviewChecks = new ReviewChecks();
