import { describe, expect, it } from "vitest";
import { statusText } from "$lib/theme";
import { statusTone } from "./statustone";

// The one rule this module has, in both directions: every status theme.ts names
// keeps its colour, and only the unnamed fallback moves.
//
// The point of testing the FIRST half is that it is the half a future edit
// would break. A helper that started translating a named status would put a
// third colour vocabulary in the repository, which desktop/state_parity_test.go
// exists to prevent — it pins theme.ts against Go's internal/state, and nothing
// pins a phone-local table against anything.

describe("statusTone", () => {
  it("defers to the shared vocabulary for every status it names", () => {
    for (const status of [
      "needs_input",
      "working",
      "ci_failed",
      "changes_requested",
      "merge_conflict",
      "dead",
      "approved",
      "merged",
      "session_ended",
      "idle",
      "closed",
      "shell",
      "orphaned",
    ]) {
      expect(statusTone(status)).toBe(statusText(status));
    }
  });

  it("steps the unnamed family down off the heading's ink", () => {
    // These three are the ones theme.ts leaves at `text-ink`, which is correct
    // inside a pill and wrong without one: the phone dropped the pill, so the
    // status printed in exactly the row heading's colour and read as emphasis.
    for (const status of ["review_pending", "draft", "ci_pending"]) {
      expect(statusText(status)).toBe("text-ink");
      expect(statusTone(status)).toBe("text-faint");
    }
  });

  it("does the same for a status no build has ever seen", () => {
    // A phone outlives the Mac's daemon, so a newer status word will arrive
    // here eventually. It should read as a quiet state, not as a heading.
    expect(statusTone("wharrgarbl")).toBe("text-faint");
    expect(statusTone("")).toBe("text-faint");
  });
});
