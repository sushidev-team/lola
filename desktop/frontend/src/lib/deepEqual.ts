// Structural equality for the config-form DTOs, so a form can tell an edited DTO
// apart from the one it loaded and only prompt to discard when something actually
// changed. Kept pure (no runes, no DOM) so it lives in its own tested module
// rather than inside a component.
//
// Scoped to what the DTOs contain: JSON-shaped values — strings, numbers,
// booleans, null, arrays and plain objects. Order matters inside arrays (a
// reordered priority-sort chain IS a different value) and does not between object
// keys.

export function deepEqual(a: unknown, b: unknown): boolean {
  if (a === b) return true;

  // Past the identity check a mismatched type can never be equal; this also
  // rules out null-vs-object before the property walk below.
  if (typeof a !== typeof b || a === null || b === null) return false;
  if (typeof a !== "object") return false; // primitives that weren't ===

  const aArr = Array.isArray(a);
  if (aArr !== Array.isArray(b)) return false;
  if (aArr) {
    const av = a as unknown[];
    const bv = b as unknown[];
    return av.length === bv.length && av.every((v, i) => deepEqual(v, bv[i]));
  }

  const ao = a as Record<string, unknown>;
  const bo = b as Record<string, unknown>;
  const ak = Object.keys(ao);
  if (ak.length !== Object.keys(bo).length) return false;
  return ak.every(
    (k) => Object.prototype.hasOwnProperty.call(bo, k) && deepEqual(ao[k], bo[k]),
  );
}
