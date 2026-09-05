// The five design tokens the phone needs and the desktop does not.
//
// WHY THIS FILE EXISTS AT ALL. The mobile design (Figma "Lola / Mobile
// Redesign") names seven colours that `catppuccin.ts`'s `toTokens` does not
// emit: a darker chrome ground for the tab bar and the accessory bar (`crust`),
// a prose tier between `ink` and `faint` (`subtext`), a hairline rule softer
// than `edge` (`edge-soft`), and the two tinted chip grounds the status chips
// sit on (`pill-*-soft`) with a foreground each. Every one of them exists in the
// flavor already — they are fields on `Flavor`, or one lerp away from two of
// them — so nothing here invents a colour. It only NAMES combinations the
// desktop chrome never needed.
//
// WHY NOT IN desktop/frontend. Because this project does not edit desktop/**:
// the whole reuse bet is that the shared library compiles here unchanged, and
// the same argument TouchButton makes about Button's size ladder applies to the
// token set. The cost is stated rather than hidden — the token list now lives in
// two files, and a future desktop token with one of these names would collide.
// The names are therefore chosen to be ones the desktop has no use for.
//
// WHY DERIVED RATHER THAN LITERAL. `[ui].theme` repaints all four flavors at
// runtime (theme-runtime's `applyFlavor`), so a literal `#181926` would be
// correct on macchiato and wrong on the other three — latte in particular, where
// `crust` is LIGHTER than `base` and a hard-coded near-black tab bar would be a
// black slab across the bottom of a light app. Everything below is a function of
// the live `Flavor`, applied the same way and at the same moment the shared
// tokens are.

import { mix, readable, type Flavor } from "$lib/catppuccin";

/**
 * How far `edge-soft` sits from the app ground toward the selected-row band.
 *
 * The design's rule tone (#2f3348 on macchiato) is between `mantle` (#1e2030)
 * and `surface0` (#363a4f) — softer than `edge`, which is `surface1`/`surface2`
 * and reads as a panel border rather than as a list hairline. Fitted against the
 * design's own value: 0.74 reproduces it to within one 8-bit step per channel.
 */
export const EDGE_SOFT_T = 0.74;

/**
 * How much of a status colour is tinted into the panel for a SOFT chip ground.
 *
 * Fitted the same way, against both of the design's soft chips at once: peach
 * into base gives #524449 and red into base gives #503c4e at 0.22, which is what
 * the Figma file holds for `pill-urgent-soft` and `pill-broken-soft`. It is
 * deliberately below `PILL_TINT` (0.28, the desktop's solid-ish work/done pills)
 * — these chips carry a caps label in the status colour itself, so the ground
 * has to stay a ground.
 */
export const PILL_SOFT_T = 0.22;

/**
 * The mobile-only tokens for one flavor, in the shape `applyFlavor` writes.
 *
 * Pure, and exported separately from the applier so a test can assert the values
 * without a DOM.
 */
export function mobileTokens(f: Flavor): Record<string, string> {
  // The soft grounds are computed first because their foregrounds are measured
  // AGAINST them. The design draws `text-orange` on `pill-urgent-soft`, which is
  // comfortable on the three dark flavors and is not a safe assumption on latte,
  // where both the tint and the accent move — so the label takes the same AA
  // walk every other status colour in this app takes.
  const urgentSoft = mix(f.peach, f.base, PILL_SOFT_T);
  const brokenSoft = mix(f.red, f.base, PILL_SOFT_T);

  return {
    // The chrome ground: the tab bar and the accessory bar. One step BELOW the
    // app ground, which is what separates fixed chrome from scrolling content
    // without a border having to do it alone.
    "--color-crust": f.crust,

    // Prose. One step above `faint` and one below `ink`: the activity sentence
    // on a card, the label on an unselected filter chip, an accessory key's
    // legend. `faint` is for facts you are not reading (an age, a project); this
    // is for a sentence you are.
    "--color-subtext": readable(f.subtext1, f.base, f.mantle),

    // The list hairline and the card border.
    "--color-edge-soft": mix(f.surface0, f.mantle, EDGE_SOFT_T),

    // The two soft chip grounds and their labels.
    "--color-pill-urgent-soft": urgentSoft,
    "--color-pill-urgent-soft-fg": readable(f.peach, urgentSoft),
    "--color-pill-broken-soft": brokenSoft,
    "--color-pill-broken-soft-fg": readable(f.red, brokenSoft),
  };
}

/**
 * Push the mobile tokens onto the document.
 *
 * Idempotent and cheap, exactly like `applyFlavor` — call it on every flavor
 * change and on boot. It writes the same way (inline custom properties on the
 * root element), so the two sets cascade identically and a token defined in
 * `app.css`'s `@theme` block is the pre-JS fallback for its own runtime value.
 */
export function applyMobileTokens(
  flavor: Flavor,
  root: HTMLElement = document.documentElement,
): void {
  for (const [name, value] of Object.entries(mobileTokens(flavor))) {
    root.style.setProperty(name, value);
  }
}
