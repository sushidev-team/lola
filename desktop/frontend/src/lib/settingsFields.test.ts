import { describe, expect, it } from 'vitest';
import { settingsSaveError } from './settingsFields';

describe('settings save errors', () => {
  it('groups repeated label requirements by project and explains how to fix them', () => {
    const error = settingsSaveError(new Error('RuntimeError: project "one" polling: dedup_mode=label requires on_sent_set_label\nproject "one" polling: dedup_mode=label requires match_labels (the removed trigger labels are the primary dedup)\nproject "two" polling: dedup_mode=label requires on_sent_set_label\ndefaults.global_cap must be > 0'));
    expect(error.summary).toBe('Label-based pickup needs labels for 2 projects.');
    expect(error.description).toContain('Remember picked-up issues');
    expect(error.description).toContain('After pickup label');
    expect(error.details).toBe('Affected projects: one, two.\ndefaults.global_cap must be > 0');
  });

  it('retains other validation failures without the runtime wrapper', () => {
    expect(settingsSaveError('RuntimeError: defaults.global_cap must be > 0')).toEqual({summary:'defaults.global_cap must be > 0'});
  });
});
