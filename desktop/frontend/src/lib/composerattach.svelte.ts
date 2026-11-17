// How the composer picks an image where there is no clipboard to paste from. The phone installs a
// picker; the desktop never does, so its composer shows no attach button. The slot is reactive, so
// a composer already on screen shows or hides the button when a picker comes or goes.

import type { ImageAttachment } from "@bindings/desktop";

/** Resolves null when the person cancels. */
export type ImagePicker = () => Promise<ImageAttachment | null>;

const slot = $state<{ picker: ImagePicker | null }>({ picker: null });

export function imagePicker(): ImagePicker | null {
  return slot.picker;
}

export function setImagePicker(f: ImagePicker | null): void {
  slot.picker = f;
}
