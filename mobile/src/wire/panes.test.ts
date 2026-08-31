import { describe, expect, it } from "vitest";

import {
  PANE_KINDS,
  PANE_KIND_AGENT,
  PANE_KIND_DEV,
  PANE_KIND_REVIEW,
  PANE_KIND_SHELL,
  isPaneKind,
  normalizePanesData,
  type PaneInfo,
  type RawPanesData,
} from "./panes";

// These types are a HAND-MAINTAINED MIRROR of protocol.PanesData /
// protocol.PaneInfo / protocol.ShellCreateData, and the two encoding details
// below are the ones a mirror gets wrong: a Go slice with no omitempty encodes
// as `null`, and omitempty has NO EFFECT on a struct, so an absent review pane
// arrives as a zero-valued one rather than as a missing field. Both would land
// on a phone as a crash or a nameless tab, with no debugger attached.

describe("the pane-kind vocabulary", () => {
  it("is exactly the four the daemon ships", () => {
    // Mirrors the paneKind* constants in internal/daemon/panes.go.
    expect(PANE_KINDS).toEqual(["agent", "shell", "dev", "review"]);
    expect([PANE_KIND_AGENT, PANE_KIND_SHELL, PANE_KIND_DEV, PANE_KIND_REVIEW]).toEqual([...PANE_KINDS]);
  });

  it("narrows a known kind and refuses one this build has never heard of", () => {
    // A phone outlives the Mac's daemon build, so a fifth kind will arrive one
    // day. The guard says so rather than letting a switch decide it cannot.
    expect(isPaneKind("dev")).toBe(true);
    expect(isPaneKind("hologram")).toBe(false);
    expect(isPaneKind("")).toBe(false);
  });
});

describe("normalizePanesData", () => {
  const agent: PaneInfo = { name: "lola-fe-42", kind: "agent", label: "agent" };
  const shell: PaneInfo = { name: "lola-fe-42-shell-1", kind: "shell", label: "shell 1", index: 1 };
  const review: PaneInfo = { name: "lola-fe-42-review", kind: "review", label: "review" };

  it("passes a full payload through unchanged, keeping the daemon's order", () => {
    const raw: RawPanesData = {
      session: "lola-fe-42",
      panes: [agent, shell, review],
      review,
      canCreateShell: true,
    };
    expect(normalizePanesData("lola-fe-42", raw)).toEqual({
      session: "lola-fe-42",
      panes: [agent, shell, review],
      review,
      canCreateShell: true,
    });
  });

  it("turns a null `panes` into an empty array", () => {
    // `Panes []PaneInfo json:"panes"` has no omitempty, so Go writes null for a
    // nil slice. Without this, a tab strip doing `data.panes.map(...)` throws on
    // exactly the session that has nothing to draw.
    const d = normalizePanesData("lola-fe-42", { session: "lola-fe-42", panes: null, canCreateShell: false });
    expect(d.panes).toEqual([]);
  });

  it("treats a zero-valued review struct as no review pane", () => {
    // `Review PaneInfo json:"review,omitempty"` — omitempty does nothing to a
    // struct, so this is what "there is no review pane" looks like on the wire.
    const d = normalizePanesData("lola-fe-42", {
      session: "lola-fe-42",
      panes: [agent],
      review: { name: "", kind: "" as never, label: "" },
      canCreateShell: true,
    });
    expect(d.review).toBeUndefined();
  });

  it("drops a pane with no name, because it cannot be subscribed to", () => {
    const d = normalizePanesData("lola-fe-42", {
      panes: [agent, { name: "", kind: "shell", label: "shell 9" }],
      canCreateShell: true,
    });
    expect(d.panes).toEqual([agent]);
  });

  it("keeps a kind it does not know, so the tab is still drawn from its label", () => {
    const future = { name: "lola-fe-42-notes-1", kind: "notes" as never, label: "notes 1", index: 1 };
    const d = normalizePanesData("lola-fe-42", { panes: [agent, future], canCreateShell: true });
    expect(d.panes).toEqual([agent, future]);
  });

  it("falls back to the session that was asked for", () => {
    expect(normalizePanesData("lola-fe-42", { panes: [], canCreateShell: false }).session).toBe("lola-fe-42");
  });

  it("fails closed on canCreateShell", () => {
    // A permissive default offers a "+" whose only outcome is a refusal.
    expect(normalizePanesData("x", { panes: [] }).canCreateShell).toBe(false);
    expect(normalizePanesData("x", null).canCreateShell).toBe(false);
    expect(normalizePanesData("x", { panes: [], canCreateShell: "yes" as never }).canCreateShell).toBe(false);
  });

  it("survives a payload that is missing entirely", () => {
    expect(normalizePanesData("lola-fe-42", undefined)).toEqual({
      session: "lola-fe-42",
      panes: [],
      review: undefined,
      canCreateShell: false,
    });
  });
});
