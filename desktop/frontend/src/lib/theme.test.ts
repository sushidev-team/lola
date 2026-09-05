import { describe, it, expect } from "vitest";
import { AA, FLAVORS, THEME_IDS, TOKEN_NAMES, contrast, panelBg, toTokens } from "./catppuccin";
import {
  ALL_DISPLAYS,
  ALL_STATUSES,
  KANBAN_COLUMNS,
  attention,
  attentionCount,
  deliveryGlyph,
  deliveryLabel,
  deliveryText,
  displayFor,
  displayLabel,
  displayPill,
  displayText,
  eventPhrase,
  inputReasonLabel,
  isAttention,
  kanbanColumn,
  kanbanDotText,
  kanbanKey,
  kanbanTitle,
  legacySortRank,
  pillClasses,
  pillKind,
  sortRank,
  statusBadge,
  statusLabel,
  statusText,
  type Display,
} from "./theme";

// The agent axis' vocabularies. `state.AllDisplays()` is pinned against this
// list by desktop/state_parity_test.go; these pin what the app DOES with each
// word, which no Go test can see.
const AGENT_STATES = [
  "starting", "working", "waiting_input", "idle",
  "exited", "dead", "shell", "orphaned",
];
const DELIVERIES = [
  "none", "draft", "ci_pending", "ci_failed", "merge_conflict",
  "changes_requested", "review_pending", "approved", "merged", "closed",
];

describe("displayFor", () => {
  it("reduces the agent axis to the six-word pill vocabulary", () => {
    expect(displayFor("starting")).toBe("working");
    expect(displayFor("working")).toBe("working");
    expect(displayFor("idle")).toBe("idle");
    expect(displayFor("waiting_input")).toBe("needs_you");
    expect(displayFor("exited")).toBe("gone");
    expect(displayFor("dead")).toBe("gone");
    expect(displayFor("shell")).toBe("shell");
    expect(displayFor("orphaned")).toBe("orphaned");
  });

  it("answers 'working' for an absent or unknown axis, never 'gone'", () => {
    // The axes are optional on the wire, so "" is a real case. Drawing a live
    // agent as gone would hide it from the very views built to surface it —
    // state.DisplayFor makes the same choice for the same reason.
    expect(displayFor("")).toBe("working");
    expect(displayFor(undefined)).toBe("working");
    expect(displayFor("some_future_state")).toBe("working");
  });

  it("never leaves the declared vocabulary", () => {
    for (const a of [...AGENT_STATES, "", "wat"])
      expect(ALL_DISPLAYS, a).toContain(displayFor(a));
  });
});

describe("display presentation", () => {
  it("spells needs_you as two words and passes the rest through", () => {
    expect(displayLabel("needs_you")).toBe("needs you");
    for (const d of ALL_DISPLAYS) expect(displayLabel(d)).not.toContain("_");
  });

  it("gives all six values a DISTINCT pill treatment", () => {
    // The whole point of the split: sixteen collapsed statuses landed on six
    // fills, so two different situations could look identical. Six words on six
    // treatments cannot.
    const seen = ALL_DISPLAYS.map(displayPill);
    expect(new Set(seen).size).toBe(ALL_DISPLAYS.length);
  });

  it("gives all six values a DISTINCT bare text colour", () => {
    const seen = ALL_DISPLAYS.map(displayText);
    expect(new Set(seen).size).toBe(ALL_DISPLAYS.length);
  });

  it("puts the loudest treatment on needs_you and the quietest on gone", () => {
    expect(displayPill("needs_you")).toContain("bg-pill-urgent");
    expect(displayPill("gone")).toBe("text-faint");
  });

  it("names a foreground the flavor can actually reach, for every filled pill", () => {
    // Same property pillClasses is held to: a fill's foreground must resolve to
    // a token toTokens() emits, which is exactly what a Tailwind built-in fails.
    for (const d of ALL_DISPLAYS) {
      const cls = displayPill(d);
      if (!cls.includes("bg-")) continue;
      const fg = cls.split(" ").find((c) => c.startsWith("text-"));
      expect(fg, d).toBeDefined();
      expect(TOKEN_NAMES, d).toContain(`--color-${fg!.slice("text-".length)}`);
    }
  });
});

describe("inputReasonLabel", () => {
  it("names the four answerable reasons", () => {
    expect(inputReasonLabel("question")).toBe("question");
    expect(inputReasonLabel("permission_prompt")).toBe("permission prompt");
    expect(inputReasonLabel("dialog")).toBe("dialog");
    expect(inputReasonLabel("quota_limited")).toBe("usage limit");
  });

  it("says nothing for the idle nudge, which is no longer a reason at all", () => {
    // 90% of the old needs_input traffic and 0% of its questions. The daemon
    // files it as idle now, but a snapshot from before that change can still
    // carry the word, and an explanation that is not true is worse than none.
    expect(inputReasonLabel("idle_notification")).toBe("");
    expect(inputReasonLabel("")).toBe("");
    expect(inputReasonLabel(undefined)).toBe("");
  });
});

describe("the delivery chip", () => {
  it("is silent when there is no PR", () => {
    expect(deliveryLabel("none")).toBe("");
    expect(deliveryLabel("")).toBe("");
    expect(deliveryLabel(undefined)).toBe("");
    expect(deliveryGlyph("none")).toBe("");
  });

  it("borrows statusLabel's spelling rather than inventing a second one", () => {
    for (const d of DELIVERIES) {
      if (d === "none") continue;
      expect(deliveryLabel(d), d).toBe(statusLabel(d));
      expect(deliveryLabel(d), d).not.toContain("_");
    }
  });

  it("carries a one-character mark for every real delivery state", () => {
    for (const d of DELIVERIES) {
      if (d === "none") continue;
      expect([...deliveryGlyph(d)], d).toHaveLength(1);
    }
  });

  it("colours the three regressions loudly and the terminal states quietly", () => {
    expect(deliveryText("ci_failed")).toBe("text-bad");
    expect(deliveryText("merge_conflict")).toBe("text-bad");
    expect(deliveryText("changes_requested")).toBe("text-orange");
    expect(deliveryText("approved")).toBe("text-good");
    for (const d of ["merged", "closed", "draft", "none", ""])
      expect(deliveryText(d), d).toBe("text-faint");
  });
});

describe("attention", () => {
  it("is true for a blocked agent whatever its PR is doing", () => {
    for (const d of DELIVERIES) expect(attention("waiting_input", d), d).toBe(true);
  });

  it("is true for the three delivery regressions whatever the agent is doing", () => {
    for (const a of AGENT_STATES)
      for (const d of ["ci_failed", "changes_requested", "merge_conflict"])
        expect(attention(a, d), `${a}/${d}`).toBe(true);
  });

  it("is false for everything else, including a happily working agent", () => {
    expect(attention("working", "ci_pending")).toBe(false);
    expect(attention("idle", "review_pending")).toBe(false);
    expect(attention("dead", "merged")).toBe(false);
    expect(attention(undefined, undefined)).toBe(false);
  });
});

describe("sortRank", () => {
  it("orders blocked, then broken, then working, then parked, then done", () => {
    expect(sortRank("waiting_input", "none")).toBe(0);
    expect(sortRank("working", "ci_failed")).toBe(1);
    expect(sortRank("working", "none")).toBe(2);
    expect(sortRank("idle", "review_pending")).toBe(3);
    expect(sortRank("idle", "none")).toBe(4);
    expect(sortRank("exited", "none")).toBe(5);
    expect(sortRank("idle", "merged")).toBe(5);
  });

  it("sorts a red build ABOVE a working agent, not below it", () => {
    // The order of the cases is the contract: a working agent whose CI just went
    // red is tier 1 (fix it), not tier 2.
    expect(sortRank("working", "ci_failed")).toBeLessThan(sortRank("working", "ci_pending"));
  });

  it("puts a blocked agent ahead of everything", () => {
    for (const a of AGENT_STATES)
      for (const d of DELIVERIES)
        if (a !== "waiting_input")
          expect(sortRank("waiting_input", "none")).toBeLessThan(sortRank(a, d) + 1);
  });
});

describe("legacySortRank", () => {
  it("is the fallback for a session with no agent axis at all", () => {
    // Without it every row of a pre-split push answers sortRank("", "") = 4 and
    // the whole list flattens into one tier.
    expect(sortRank("", "")).toBe(4);
    expect(legacySortRank("needs_input")).toBe(0);
    expect(legacySortRank("ci_failed")).toBe(1);
    expect(legacySortRank("working")).toBe(2);
    expect(legacySortRank("review_pending")).toBe(3);
    expect(legacySortRank("merged")).toBe(5);
    expect(legacySortRank("mystery")).toBe(4);
  });
});

describe("kanbanKey", () => {
  it("lets terminal beat everything", () => {
    // An exited agent parked on a red CI is not work to fix.
    expect(kanbanKey("exited", "ci_failed")).toBe("done");
    expect(kanbanKey("dead", "review_pending")).toBe("done");
    expect(kanbanKey("waiting_input", "merged")).toBe("done");
  });

  it("puts a blocked agent ahead of a broken build", () => {
    expect(kanbanKey("waiting_input", "ci_failed")).toBe("needs");
  });

  it("buckets the rest by the delivery axis", () => {
    expect(kanbanKey("working", "ci_failed")).toBe("fixing");
    expect(kanbanKey("working", "review_pending")).toBe("review");
    expect(kanbanKey("idle", "approved")).toBe("review");
    expect(kanbanKey("working", "ci_pending")).toBe("working");
    expect(kanbanKey("idle", "none")).toBe("working");
    expect(kanbanKey(undefined, undefined)).toBe("working");
  });

  it("only ever names a column that exists", () => {
    const keys = KANBAN_COLUMNS.map((c) => c.key);
    for (const a of [...AGENT_STATES, ""])
      for (const d of [...DELIVERIES, ""]) expect(keys, `${a}/${d}`).toContain(kanbanKey(a, d));
  });

  it("kanbanTitle is the same answer, spelled for a human", () => {
    expect(kanbanTitle("waiting_input", "none")).toBe("Needs You");
    expect(kanbanTitle("working", "ci_failed")).toBe("Fixing");
    expect(kanbanTitle("dead", "none")).toBe("Done");
  });
});

describe("kanbanDotText", () => {
  it("gives every column its own colour", () => {
    const seen = KANBAN_COLUMNS.map((c) => kanbanDotText(c.key));
    expect(new Set(seen).size).toBe(KANBAN_COLUMNS.length);
  });
  it("falls back to faint for a key it does not know", () => {
    expect(kanbanDotText("banana")).toBe("text-faint");
  });
});

// --- the legacy half -------------------------------------------------------
//
// Still shipped on protocol.SessionInfo.status and still the only vocabulary
// the mobile companion reads, so it is tested as hard as the new one.

describe("pillKind", () => {
  it("maps urgent/broken/work/done/grey buckets", () => {
    expect(pillKind("needs_input")).toBe("urgent");
    expect(pillKind("ci_failed")).toBe("broken");
    expect(pillKind("changes_requested")).toBe("broken");
    expect(pillKind("merge_conflict")).toBe("broken");
    expect(pillKind("working")).toBe("work");
    expect(pillKind("ci_pending")).toBe("work");
    expect(pillKind("draft")).toBe("work");
    expect(pillKind("approved")).toBe("done");
    expect(pillKind("review_pending")).toBe("grey");
  });
  it("falls back to plain for terminal/idle/unknown", () => {
    for (const s of ["merged", "dead", "session_ended", "idle", "wat"])
      expect(pillKind(s)).toBe("plain");
  });
});

describe("pillClasses", () => {
  it("gives dead a solid red fill even though it is plain", () => {
    expect(pillClasses("dead")).toContain("bg-bad");
  });
  it("uses the urgent fill for needs_input", () => {
    expect(pillClasses("needs_input")).toContain("bg-pill-urgent");
  });

  it("names a foreground the flavor can actually reach, for every status", () => {
    // `dead` used to carry Tailwind's built-in white, the one foreground no
    // flavor can override — and it was the worst pill in the app on the DEFAULT
    // flavor at 2.32:1. Asserted as a property rather than a blacklist: the
    // foreground utility must resolve to a token toTokens() emits, which is
    // exactly what a built-in color fails. catppuccin.test.ts then holds those
    // token pairs to AA in all four flavors.
    for (const s of ["dead", "needs_input", "ci_failed", "working", "approved", "review_pending", "merged"]) {
      const fg = pillClasses(s)
        .split(" ")
        .find((c) => c.startsWith("text-"));
      expect(fg, s).toBeDefined();
      expect(TOKEN_NAMES, s).toContain(`--color-${fg!.slice("text-".length)}`);
    }
  });
});

describe("statusText", () => {
  it("never hands a bare caller a color that assumes a fill behind it", () => {
    // statusText has three callers and only ONE of them (pillClasses) paints a
    // fill first: the sidebar and the phone's session rows print it straight
    // onto a panel. So `dead` returning a built-in white was 1.14:1 on latte. It
    // is a bad-family status and now says so; the pill supplies its own on-fill
    // foreground separately.
    expect(statusText("dead")).toBe("text-bad");
    expect(pillClasses("dead")).toBe("bg-bad text-on-bad");
  });

  it("is legible on every surface it is printed on, for every status", () => {
    // Not just `dead`, and not just the panel. Every colour this file can name
    // has to clear AA on all three bare surfaces, because the row it labels may
    // be unselected (panel), selected (sel), or in a list drawn straight on the
    // canvas. Enumerating the INPUTS rather than the tokens is what makes a
    // future `case "x": return "text-y"` fail here if `y` is a color the flavor
    // cannot carry — which is why the two-axis functions are swept here too, and
    // in particular displayPill's unfilled values, which are printed bare by
    // definition.
    const STATUSES = [
      "working", "ci_failed", "changes_requested", "merge_conflict", "dead",
      "approved", "needs_input", "merged", "session_ended",
      "idle", "draft", "review_pending", "ci_pending", "unknown",
      "closed", "shell", "orphaned",
    ];
    const AXIS_CLASSES = [
      ...ALL_DISPLAYS.map(displayText),
      ...ALL_DISPLAYS.map(displayPill).filter((c) => !c.includes("bg-")),
      ...DELIVERIES.map(deliveryText),
      ...KANBAN_COLUMNS.map((c) => kanbanDotText(c.key)),
    ];
    // text-faint is outside THIS (AA) floor by design, and NOT a carve-out
    // hiding one of the colors above: --color-faint is the app-wide DE-EMPHASIZED
    // text token, not a status color. It carries no essential information — the
    // status colors above do — so it is deliberately held to the lower MUTED
    // (3:1) floor instead, which keeps it a step below `ink` in the hierarchy.
    // That floor is enforced in catppuccin.test.ts; holding faint to AA here
    // would only re-raise the hierarchy question this token exists to settle.
    const EXCLUDED = "text-faint";
    for (const id of THEME_IDS) {
      const f = FLAVORS[id];
      const t = toTokens(f);
      const surfaces = { canvas: t["--color-canvas"], panel: panelBg(f), sel: t["--color-sel"] };
      const classes = [...STATUSES.map(statusText), ...AXIS_CLASSES];
      for (const cls of new Set(classes)) {
        if (cls === EXCLUDED) continue;
        const fg = t[`--color-${cls.slice("text-".length)}`];
        expect(fg, cls).toBeDefined();
        for (const [where, bg] of Object.entries(surfaces))
          expect(contrast(fg, bg), `${id} ${cls} on ${where}`).toBeGreaterThanOrEqual(AA);
      }
    }
    // …and the exclusion is one specific class, not a filter that could quietly
    // swallow a second one later: merged/session_ended/idle/closed/shell/orphaned.
    expect(STATUSES.map(statusText).filter((c) => c === EXCLUDED)).toHaveLength(6);
  });
});

describe("statusLabel", () => {
  it("humanizes every raw status word", () => {
    expect(statusLabel("changes_requested")).toBe("changes");
    expect(statusLabel("review_pending")).toBe("review");
    expect(statusLabel("merge_conflict")).toBe("conflict");
    expect(statusLabel("session_ended")).toBe("ended");
    expect(statusLabel("ci_pending")).toBe("ci running");
    expect(statusLabel("ci_failed")).toBe("ci failed");
    expect(statusLabel("needs_input")).toBe("needs you");
    expect(statusLabel("waiting_input")).toBe("waiting");
    expect(statusLabel("quota_limited")).toBe("usage limit");
  });
  it("passes plain words through and de-underscores unknowns", () => {
    expect(statusLabel("working")).toBe("working");
    expect(statusLabel("some_future_word")).toBe("some future word");
  });
  it("never renders an underscore for any vocabulary word", () => {
    for (const s of ALL_STATUSES) expect(statusLabel(s)).not.toContain("_");
  });
});

describe("statusBadge", () => {
  it("is two chars for known statuses and ?? otherwise", () => {
    expect(statusBadge("needs_input")).toBe("!!");
    expect(statusBadge("approved")).toBe("ok");
    expect(statusBadge("nope")).toBe("??");
  });
});

describe("isAttention / attentionCount", () => {
  it("counts only the needs-human statuses", () => {
    // The status-string form. Not dead code despite having no desktop caller:
    // mobile/src/views/Sessions.svelte is its live one, through the $lib alias.
    expect(isAttention("needs_input")).toBe(true);
    expect(isAttention("working")).toBe(false);
    const sessions = [
      { status: "needs_input" },
      { status: "ci_failed" },
      { status: "working" },
      { status: "approved" },
    ];
    expect(attentionCount(sessions)).toBe(2);
  });
});

describe("kanbanColumn", () => {
  it("buckets statuses and defaults unknown to Working", () => {
    expect(kanbanColumn("needs_input")).toBe("Needs You");
    expect(kanbanColumn("approved")).toBe("In Review");
    expect(kanbanColumn("merged")).toBe("Done");
    expect(kanbanColumn("banana")).toBe("Working");
  });

  it("still answers over the pre-split partition, for mobile", () => {
    // Its signature is load-bearing (mobile's TriageChips and SessionRow call
    // it) and so is its ANSWER: the board moved to the two axes, this did not.
    expect(kanbanColumn("ci_pending")).toBe("Working");
    expect(kanbanColumn("idle")).toBe("Working");
    expect(kanbanColumn("dead")).toBe("Done");
  });
});

describe("eventPhrase", () => {
  it("says spawned for a fresh session", () => {
    expect(eventPhrase("", "working")).toBe("spawned");
  });
  it("maps transitions to human phrases", () => {
    expect(eventPhrase("working", "needs_input")).toBe("needs you");
    expect(eventPhrase("working", "merged")).toBe("merged");
    expect(eventPhrase("working", "whatever")).toBe("whatever");
  });
});

// A Display value that Go grows and this file does not would be rendered as
// "working" by displayFor's deliberate fallback, silently. The Go-side pin is
// desktop/state_parity_test.go; this is the TS-side half of it.
describe("ALL_DISPLAYS", () => {
  it("is exactly the six words the pill can draw", () => {
    const want: Display[] = ["working", "idle", "needs_you", "gone", "shell", "orphaned"];
    expect(ALL_DISPLAYS).toEqual(want);
  });
});
