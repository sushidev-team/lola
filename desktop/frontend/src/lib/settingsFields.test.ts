import { describe, it, expect } from "vitest";
import { PICKUP_FIELDS, REPEAT_PICKUP, ENTRY_FORMAT, FIELD_KEYS, settingsSaveError } from "./settingsFields";

// Captions are plain words; the config.toml key is secondary text.
describe("settingsFields", () => {
  it("names every pickup caption's config key", () => {
    expect(FIELD_KEYS[PICKUP_FIELDS.matchMode]).toBe("match_mode");
    expect(FIELD_KEYS[PICKUP_FIELDS.dedupMode]).toBe("dedup_mode");
    expect(FIELD_KEYS[PICKUP_FIELDS.onSentSetLabel]).toBe("on_sent_set_label");
  });

  it("keeps jargon out of the captions and hints", () => {
    const words = [...Object.values(PICKUP_FIELDS), ...REPEAT_PICKUP.map((o) => o.label), ...Object.values(ENTRY_FORMAT)];
    for (const w of words) expect(w).not.toMatch(/UUID|dedup|Repeat pickup|After pickup/);
  });

  it("keeps the three repeat-pickup modes the config accepts", () => {
    expect(REPEAT_PICKUP.map((o) => o.id)).toEqual(["label", "seen", "state"]);
  });
});

describe("settings save errors", () => {
  it("groups repeated label requirements by project and explains how to fix them", () => {
    const error = settingsSaveError(new Error('RuntimeError: project "one" polling: dedup_mode=label requires on_sent_set_label\nproject "one" polling: dedup_mode=label requires match_labels (the removed trigger labels are the primary dedup)\nproject "two" polling: dedup_mode=label requires on_sent_set_label\ndefaults.global_cap must be > 0'));
    expect(error.summary).toBe("Label-based pickup needs labels for 2 projects.");
    // The fix is named by the captions the forms show.
    expect(error.description).toContain(REPEAT_PICKUP.find((o) => o.id === "seen")!.label);
    expect(error.description).toContain(PICKUP_FIELDS.dedupMode);
    expect(error.description).toContain(PICKUP_FIELDS.onSentSetLabel);
    expect(error.details).toBe("Affected projects: one, two.\ndefaults.global_cap must be > 0");
  });

  it("retains other validation failures without the runtime wrapper", () => {
    expect(settingsSaveError("RuntimeError: defaults.global_cap must be > 0")).toEqual({ summary: "defaults.global_cap must be > 0" });
  });
});
