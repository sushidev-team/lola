import { describe, it, expect } from "vitest";
import { AA, FLAVORS, THEME_IDS, TOKEN_NAMES, contrast, panelBg, toTokens } from "./catppuccin";
import {
  statusText,
  pillKind,
  pillClasses,
  statusLabel,
  statusBadge,
  sortRank,
  isAttention,
  kanbanColumn,
  eventPhrase,
  reactingText,
  attentionCount,
} from "./theme";

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
    expect(pillKind("pr_open")).toBe("done");
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
    // fill first: Rail and SessionsKanban print it straight onto a panel. So
    // `dead` returning a built-in white was 1.14:1 on latte's panel. It is a
    // bad-family status and now says so; the pill supplies its own on-fill
    // foreground separately.
    expect(statusText("dead")).toBe("text-bad");
    expect(pillClasses("dead")).toBe("bg-bad text-on-bad");
  });

  it("is legible on every surface it is printed on, for every status", () => {
    // Not just `dead`, and not just the panel. Every status this function can
    // name has to clear AA on all three bare surfaces, because the row it
    // labels may be unselected (panel), selected (sel), or in a list drawn
    // straight on the canvas. Enumerating the statuses rather than the tokens
    // is what makes a future `case "x": return "text-y"` fail here if `y` is a
    // color the flavor cannot carry.
    const STATUSES = [
      "working", "ci_failed", "changes_requested", "merge_conflict", "dead",
      "approved", "needs_input", "no_signal", "merged", "session_ended",
      "idle", "draft", "pr_open", "review_pending", "ci_pending", "unknown",
    ];
    const REACTING = ["escalated", "ready to merge", "ci retry 1", "addressing review", "rebasing", ""];
    // text-faint is deliberately outside this floor and is NOT a carve-out
    // hiding one of the colors above: --color-faint is the app-wide muted-text
    // token, not a status color, and it misses AA on a panel in every flavor
    // (4.495 / 4.201 / 3.705 / 3.457) — a pre-existing shortfall shared with
    // every secondary label in the app, not something statusText introduced.
    // Raising it is a hierarchy-wide decision; make it there, not here.
    const EXCLUDED = "text-faint";
    for (const id of THEME_IDS) {
      const f = FLAVORS[id];
      const t = toTokens(f);
      const surfaces = { canvas: t["--color-canvas"], panel: panelBg(f), sel: t["--color-sel"] };
      const classes = [...STATUSES.map(statusText), ...REACTING.map(reactingText)];
      for (const cls of new Set(classes)) {
        if (cls === EXCLUDED) continue;
        const fg = t[`--color-${cls.slice("text-".length)}`];
        expect(fg, cls).toBeDefined();
        for (const [where, bg] of Object.entries(surfaces))
          expect(contrast(fg, bg), `${id} ${cls} on ${where}`).toBeGreaterThanOrEqual(AA);
      }
    }
    // …and the exclusion is one specific class, not a filter that could quietly
    // swallow a second one later.
    expect(STATUSES.map(statusText).filter((c) => c === EXCLUDED)).toHaveLength(3);
  });
});

describe("statusLabel", () => {
  it("shortens the noisy labels", () => {
    expect(statusLabel("changes_requested")).toBe("changes");
    expect(statusLabel("review_pending")).toBe("review");
    expect(statusLabel("merge_conflict")).toBe("conflict");
    expect(statusLabel("session_ended")).toBe("ended");
    expect(statusLabel("ci_pending")).toBe("pending");
    expect(statusLabel("needs_input")).toBe("needs you");
  });
  it("passes unknown through unchanged", () => {
    expect(statusLabel("working")).toBe("working");
  });
});

describe("statusBadge", () => {
  it("is two chars for known statuses and ?? otherwise", () => {
    expect(statusBadge("needs_input")).toBe("!!");
    expect(statusBadge("approved")).toBe("ok");
    expect(statusBadge("nope")).toBe("??");
  });
});

describe("sortRank", () => {
  it("orders attention first, terminal last", () => {
    expect(sortRank("needs_input")).toBeLessThan(sortRank("ci_failed"));
    expect(sortRank("ci_failed")).toBeLessThan(sortRank("working"));
    expect(sortRank("working")).toBeLessThan(sortRank("review_pending"));
    expect(sortRank("review_pending")).toBeLessThan(sortRank("merged"));
    expect(sortRank("mystery")).toBe(4);
  });
});

describe("isAttention / attentionCount", () => {
  it("counts only the needs-human statuses", () => {
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

describe("reactingText", () => {
  it("colors escalation red and ready green", () => {
    expect(reactingText("escalated")).toBe("text-bad");
    expect(reactingText("ready to merge")).toBe("text-good");
    expect(reactingText("ci retry 1/2")).toBe("text-warn");
    expect(reactingText("")).toBe("text-ink");
  });
});
