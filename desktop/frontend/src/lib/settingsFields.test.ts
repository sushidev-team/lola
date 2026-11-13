import { describe, it, expect } from "vitest";
import { PICKUP_FIELDS, REPEAT_PICKUP, ENTRY_FORMAT, FIELD_KEYS } from "./settingsFields";

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
