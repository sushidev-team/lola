// The sentences a refused call shows. Phone copy: short, sentence case.
export const MAC_ONLY = "Only on your Mac.";
export const CONFIG = "Edit settings on your Mac.";
export const NO_TIER = "This phone can't do that.";
export const MAC_LINK = "Open this link on your Mac.";

export type Reason = typeof MAC_ONLY | typeof CONFIG | typeof NO_TIER | typeof MAC_LINK;
