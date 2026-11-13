// A QR encoder, byte mode only, written here rather than installed.
//
// This repository pins its dependencies deliberately — `make tidy` runs with
// GOPROXY off and the frontend's package.json is a fixed list — so a QR for one
// affordance is not worth a supply-chain edge. What is actually needed is also
// small: one mode (byte), because the payload is an opaque token whose alphabet
// is not known to be alphanumeric; and no decoder, because nothing here reads a
// QR. The result is the standard encoder minus the modes it does not use.
//
// It is a pure module with no DOM and no Svelte: the matrix is data, and
// `toPath` turns it into one SVG path string. That is what makes it testable,
// and the tests pin it against published constants (the 32 format-information
// strings, the 34 version-information strings, the Reed-Solomon generator
// polynomials, the capacity table) rather than against itself — a self-
// consistent encoder that produces an unscannable code is exactly the failure
// mode a round-trip test cannot see.

export type ECCLevel = "L" | "M" | "Q" | "H";

export interface QRMatrix {
  /** Side length in modules, 21 + 4 * (version - 1). */
  size: number;
  /** Row-major, `modules[y][x]`; true is dark. */
  modules: boolean[][];
  version: number;
  ecc: ECCLevel;
  /** The mask pattern chosen by the penalty score, 0-7. */
  mask: number;
}

export interface EncodeOptions {
  ecc?: ECCLevel;
  /** Refuse to go below this version. Default 1. */
  minVersion?: number;
  /** Refuse to go above this version. Default 40. */
  maxVersion?: number;
}

const ECC_ORDER: ECCLevel[] = ["L", "M", "Q", "H"];

/** The two-bit field the format information carries. Not the array index. */
const ECC_FORMAT_BITS: Record<ECCLevel, number> = { L: 1, M: 0, Q: 3, H: 2 };

// Error-correction codewords per block, indexed [ecc][version]. Index 0 of each
// row is unused so a version can index directly.
const ECC_CODEWORDS_PER_BLOCK: Record<ECCLevel, number[]> = {
  L: [-1, 7, 10, 15, 20, 26, 18, 20, 24, 30, 18, 20, 24, 26, 30, 22, 24, 28, 30, 28, 28, 28, 28, 30, 30, 26, 28, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30],
  M: [-1, 10, 16, 26, 18, 24, 16, 18, 22, 22, 26, 30, 22, 22, 24, 24, 28, 28, 26, 26, 26, 26, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28],
  Q: [-1, 13, 22, 18, 26, 18, 24, 18, 22, 20, 24, 28, 26, 24, 20, 30, 24, 28, 28, 26, 30, 28, 30, 30, 30, 30, 28, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30],
  H: [-1, 17, 28, 22, 16, 22, 28, 26, 26, 24, 28, 24, 28, 22, 24, 24, 30, 28, 28, 26, 28, 30, 24, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30],
};

// Number of error-correction blocks, indexed [ecc][version].
const NUM_ECC_BLOCKS: Record<ECCLevel, number[]> = {
  L: [-1, 1, 1, 1, 1, 1, 2, 2, 2, 2, 4, 4, 4, 4, 4, 6, 6, 6, 6, 7, 8, 8, 9, 9, 10, 12, 12, 12, 13, 14, 15, 16, 17, 18, 19, 19, 20, 21, 22, 24, 25],
  M: [-1, 1, 1, 1, 2, 2, 4, 4, 4, 5, 5, 5, 8, 9, 9, 10, 10, 11, 13, 14, 16, 17, 17, 18, 20, 21, 23, 25, 26, 28, 29, 31, 33, 35, 37, 38, 40, 43, 45, 47, 49],
  Q: [-1, 1, 1, 2, 2, 4, 4, 6, 6, 8, 8, 8, 10, 12, 16, 12, 17, 16, 18, 21, 20, 23, 23, 25, 27, 29, 34, 34, 35, 38, 40, 43, 45, 48, 51, 53, 56, 59, 62, 65, 68],
  H: [-1, 1, 1, 2, 4, 4, 4, 5, 5, 8, 8, 11, 11, 16, 16, 18, 16, 19, 21, 25, 25, 25, 34, 30, 32, 35, 37, 40, 42, 45, 48, 51, 54, 57, 60, 63, 66, 70, 74, 77, 81],
};

/**
 * Total modules a version can carry data or ECC in — everything but the finder
 * patterns, separators, timing patterns, alignment patterns, the format areas
 * and (from version 7) the version areas.
 */
export function rawDataModules(version: number): number {
  let n = (16 * version + 128) * version + 64;
  if (version >= 2) {
    const numAlign = Math.floor(version / 7) + 2;
    n -= (25 * numAlign - 10) * numAlign - 55;
    if (version >= 7) n -= 36;
  }
  return n;
}

/** Data codewords available at a version and level, ECC already subtracted. */
export function dataCodewords(version: number, ecc: ECCLevel): number {
  return (
    Math.floor(rawDataModules(version) / 8) -
    ECC_CODEWORDS_PER_BLOCK[ecc][version] * NUM_ECC_BLOCKS[ecc][version]
  );
}

/**
 * Alignment-pattern centre coordinates for a version.
 *
 * Computed rather than tabulated: the arithmetic is the specification's own and
 * a 40-row table transcribed by hand is 40 chances to typo a coordinate that
 * only shows up as an unscannable code at one size.
 */
export function alignmentPositions(version: number): number[] {
  if (version === 1) return [];
  const size = 4 * version + 17;
  const numAlign = Math.floor(version / 7) + 2;
  // Version 32 is the one case the general step formula does not produce.
  const step = version === 32 ? 26 : Math.ceil((version * 4 + 4) / (numAlign * 2 - 2)) * 2;
  const out = [6];
  for (let pos = size - 7; out.length < numAlign; pos -= step) out.splice(1, 0, pos);
  return out;
}

// --- GF(256), the field Reed-Solomon works in -------------------------------
// Primitive polynomial x^8 + x^4 + x^3 + x^2 + 1 (0x11D), as the QR spec fixes.

function gfMul(a: number, b: number): number {
  let r = 0;
  for (let i = 7; i >= 0; i--) {
    r = (r << 1) ^ ((r >>> 7) * 0x11d);
    r ^= ((b >>> i) & 1) * a;
  }
  return r & 0xff;
}

/** The generator polynomial of the given degree, high-order term omitted. */
export function rsGenerator(degree: number): number[] {
  const result = new Array<number>(degree).fill(0);
  result[degree - 1] = 1;
  let root = 1;
  for (let i = 0; i < degree; i++) {
    for (let j = 0; j < degree; j++) {
      result[j] = gfMul(result[j], root);
      if (j + 1 < degree) result[j] ^= result[j + 1];
    }
    root = gfMul(root, 0x02);
  }
  return result;
}

/** The Reed-Solomon remainder: the ECC codewords for one block. */
export function rsRemainder(data: readonly number[], generator: readonly number[]): number[] {
  const result = new Array<number>(generator.length).fill(0);
  for (const b of data) {
    const factor = b ^ (result.shift() as number);
    result.push(0);
    for (let i = 0; i < generator.length; i++) result[i] ^= gfMul(generator[i], factor);
  }
  return result;
}

// --- BCH, the format and version information --------------------------------

/**
 * The 15-bit format information for a level and mask: 5 data bits, a BCH(15,5)
 * remainder, XORed with the specification's 0x5412 so an all-zero format is not
 * an all-zero pattern.
 */
export function formatBits(ecc: ECCLevel, mask: number): number {
  const data = (ECC_FORMAT_BITS[ecc] << 3) | mask;
  let rem = data;
  for (let i = 0; i < 10; i++) rem = (rem << 1) ^ ((rem >>> 9) * 0x537);
  return (((data << 10) | rem) ^ 0x5412) & 0x7fff;
}

/** The 18-bit version information, present from version 7 up. */
export function versionBits(version: number): number {
  let rem = version;
  for (let i = 0; i < 12; i++) rem = (rem << 1) ^ ((rem >>> 11) * 0x1f25);
  return ((version << 12) | rem) >>> 0;
}

// --- encoding ---------------------------------------------------------------

function utf8(s: string): number[] {
  return Array.from(new TextEncoder().encode(s));
}

function charCountBits(version: number): number {
  // Byte mode: 8 bits up to version 9, 16 from version 10.
  return version <= 9 ? 8 : 16;
}

/** The smallest version at or above `min` whose data capacity fits `bytes`. */
function chooseVersion(byteLen: number, ecc: ECCLevel, min: number, max: number): number {
  for (let v = min; v <= max; v++) {
    const capacityBits = dataCodewords(v, ecc) * 8;
    const needed = 4 + charCountBits(v) + byteLen * 8;
    if (needed <= capacityBits) return v;
  }
  throw new Error(
    `qr: ${byteLen} bytes does not fit a version ${max} code at error-correction level ${ecc}`,
  );
}

function buildCodewords(bytes: number[], version: number, ecc: ECCLevel): number[] {
  const capacity = dataCodewords(version, ecc);
  const bits: number[] = [];
  const push = (value: number, len: number) => {
    for (let i = len - 1; i >= 0; i--) bits.push((value >>> i) & 1);
  };

  push(0b0100, 4); // byte mode
  push(bytes.length, charCountBits(version));
  for (const b of bytes) push(b, 8);

  // Terminator, then pad to a byte boundary, then the specification's
  // alternating pad bytes.
  const capacityBits = capacity * 8;
  push(0, Math.min(4, capacityBits - bits.length));
  push(0, (8 - (bits.length % 8)) % 8);
  const words: number[] = [];
  for (let i = 0; i < bits.length; i += 8) {
    let w = 0;
    for (let j = 0; j < 8; j++) w = (w << 1) | bits[i + j];
    words.push(w);
  }
  for (let pad = 0xec; words.length < capacity; pad ^= 0xec ^ 0x11) words.push(pad);
  return words;
}

/**
 * Split into blocks, compute each block's ECC, and interleave — the order the
 * specification lays the codewords out in, which is what makes a burst of
 * damage land across blocks rather than inside one.
 */
function interleave(words: number[], version: number, ecc: ECCLevel): number[] {
  const numBlocks = NUM_ECC_BLOCKS[ecc][version];
  const eccLen = ECC_CODEWORDS_PER_BLOCK[ecc][version];
  const rawWords = Math.floor(rawDataModules(version) / 8);
  const shortBlockLen = Math.floor(rawWords / numBlocks) - eccLen;
  const numShort = numBlocks - (rawWords % numBlocks);

  const generator = rsGenerator(eccLen);
  const blocks: number[][] = [];
  const eccBlocks: number[][] = [];
  let off = 0;
  for (let i = 0; i < numBlocks; i++) {
    const len = shortBlockLen + (i < numShort ? 0 : 1);
    const block = words.slice(off, off + len);
    off += len;
    blocks.push(block);
    eccBlocks.push(rsRemainder(block, generator));
  }

  const out: number[] = [];
  for (let i = 0; i <= shortBlockLen; i++) {
    for (let b = 0; b < numBlocks; b++) {
      if (i < blocks[b].length) out.push(blocks[b][i]);
    }
  }
  for (let i = 0; i < eccLen; i++) {
    for (let b = 0; b < numBlocks; b++) out.push(eccBlocks[b][i]);
  }
  return out;
}

// --- the matrix -------------------------------------------------------------

interface Grid {
  size: number;
  modules: boolean[][];
  /** True where a function pattern lives, so data placement steps over it. */
  reserved: boolean[][];
}

function newGrid(size: number): Grid {
  const blank = () =>
    Array.from({ length: size }, () => new Array<boolean>(size).fill(false));
  return { size, modules: blank(), reserved: blank() };
}

function setFn(g: Grid, x: number, y: number, dark: boolean) {
  if (x < 0 || y < 0 || x >= g.size || y >= g.size) return;
  g.modules[y][x] = dark;
  g.reserved[y][x] = true;
}

function drawFinder(g: Grid, cx: number, cy: number) {
  // The 7x7 finder plus its one-module separator, drawn by Chebyshev distance:
  // ring 2 and the 3x3 core are dark, rings 3 and 4 are light.
  for (let dy = -4; dy <= 4; dy++) {
    for (let dx = -4; dx <= 4; dx++) {
      const d = Math.max(Math.abs(dx), Math.abs(dy));
      setFn(g, cx + dx, cy + dy, d !== 2 && d !== 4);
    }
  }
}

function drawFunctionPatterns(g: Grid, version: number, ecc: ECCLevel) {
  const size = g.size;

  // Timing patterns first: the finders overwrite their own corners after.
  for (let i = 0; i < size; i++) {
    setFn(g, 6, i, i % 2 === 0);
    setFn(g, i, 6, i % 2 === 0);
  }

  drawFinder(g, 3, 3);
  drawFinder(g, size - 4, 3);
  drawFinder(g, 3, size - 4);

  const align = alignmentPositions(version);
  for (let i = 0; i < align.length; i++) {
    for (let j = 0; j < align.length; j++) {
      // The three corners are finder patterns already.
      const corner =
        (i === 0 && j === 0) ||
        (i === 0 && j === align.length - 1) ||
        (i === align.length - 1 && j === 0);
      if (corner) continue;
      const cx = align[i];
      const cy = align[j];
      for (let dy = -2; dy <= 2; dy++) {
        for (let dx = -2; dx <= 2; dx++) {
          setFn(g, cx + dx, cy + dy, Math.max(Math.abs(dx), Math.abs(dy)) !== 1);
        }
      }
    }
  }

  // Format information is drawn with a placeholder mask so the modules are
  // reserved; drawFormat rewrites them once the mask is chosen.
  drawFormat(g, ecc, 0);
  if (version >= 7) drawVersion(g, version);
}

function drawFormat(g: Grid, ecc: ECCLevel, mask: number) {
  const size = g.size;
  const bits = formatBits(ecc, mask);
  const bit = (i: number) => ((bits >>> i) & 1) === 1;

  // First copy, around the top-left finder.
  for (let i = 0; i <= 5; i++) setFn(g, 8, i, bit(i));
  setFn(g, 8, 7, bit(6));
  setFn(g, 8, 8, bit(7));
  setFn(g, 7, 8, bit(8));
  for (let i = 9; i < 15; i++) setFn(g, 14 - i, 8, bit(i));

  // Second copy, split between the other two finders.
  for (let i = 0; i < 8; i++) setFn(g, size - 1 - i, 8, bit(i));
  for (let i = 8; i < 15; i++) setFn(g, 8, size - 15 + i, bit(i));

  // The dark module: always set, always at (8, 4*version + 9).
  setFn(g, 8, size - 8, true);
}

function drawVersion(g: Grid, version: number) {
  const size = g.size;
  const bits = versionBits(version);
  for (let i = 0; i < 18; i++) {
    const dark = ((bits >>> i) & 1) === 1;
    const a = size - 11 + (i % 3);
    const b = Math.floor(i / 3);
    setFn(g, a, b, dark);
    setFn(g, b, a, dark);
  }
}

/** Lay the interleaved codewords out in the standard two-column zigzag. */
function placeData(g: Grid, data: number[]) {
  const size = g.size;
  let i = 0; // bit index
  for (let right = size - 1; right >= 1; right -= 2) {
    if (right === 6) right = 5; // the vertical timing column is skipped entirely
    for (let vert = 0; vert < size; vert++) {
      for (let j = 0; j < 2; j++) {
        const x = right - j;
        const upward = ((right + 1) & 2) === 0;
        const y = upward ? size - 1 - vert : vert;
        if (g.reserved[y][x]) continue;
        if (i < data.length * 8) {
          g.modules[y][x] = ((data[i >>> 3] >>> (7 - (i & 7))) & 1) === 1;
          i++;
        }
        // Remainder bits stay light, which is what the specification says.
      }
    }
  }
}

function maskBit(mask: number, x: number, y: number): boolean {
  switch (mask) {
    case 0: return (x + y) % 2 === 0;
    case 1: return y % 2 === 0;
    case 2: return x % 3 === 0;
    case 3: return (x + y) % 3 === 0;
    case 4: return (Math.floor(y / 2) + Math.floor(x / 3)) % 2 === 0;
    case 5: return ((x * y) % 2) + ((x * y) % 3) === 0;
    case 6: return (((x * y) % 2) + ((x * y) % 3)) % 2 === 0;
    case 7: return (((x + y) % 2) + ((x * y) % 3)) % 2 === 0;
    default: throw new Error(`qr: mask ${mask} is out of range`);
  }
}

function applyMask(g: Grid, mask: number) {
  for (let y = 0; y < g.size; y++) {
    for (let x = 0; x < g.size; x++) {
      if (!g.reserved[y][x] && maskBit(mask, x, y)) g.modules[y][x] = !g.modules[y][x];
    }
  }
}

const P1 = 3;
const P2 = 3;
const P3 = 40;
const P4 = 10;

/** The specification's four penalty rules, summed. Lower is better. */
export function penalty(modules: boolean[][]): number {
  const size = modules.length;
  let score = 0;

  // Rule 1: runs of five or more same-coloured modules in a line.
  const runScore = (run: number) => (run >= 5 ? P1 + (run - 5) : 0);
  for (let i = 0; i < size; i++) {
    let runH = 1;
    let runV = 1;
    for (let j = 1; j < size; j++) {
      runH = modules[i][j] === modules[i][j - 1] ? runH + 1 : ((score += runScore(runH)), 1);
      runV = modules[j][i] === modules[j - 1][i] ? runV + 1 : ((score += runScore(runV)), 1);
    }
    score += runScore(runH) + runScore(runV);
  }

  // Rule 2: 2x2 blocks of one colour.
  for (let y = 0; y < size - 1; y++) {
    for (let x = 0; x < size - 1; x++) {
      const c = modules[y][x];
      if (c === modules[y][x + 1] && c === modules[y + 1][x] && c === modules[y + 1][x + 1]) {
        score += P2;
      }
    }
  }

  // Rule 3: the finder-like 1:1:3:1:1 pattern with four light modules beside
  // it, in either orientation.
  const PATTERN = [true, false, true, true, true, false, true];
  const at = (get: (k: number) => boolean | undefined, base: number, want: readonly boolean[]) =>
    want.every((w, k) => get(base + k) === w);
  const LIGHT4 = [false, false, false, false];
  for (let i = 0; i < size; i++) {
    const row = (k: number) => (k >= 0 && k < size ? modules[i][k] : undefined);
    const col = (k: number) => (k >= 0 && k < size ? modules[k][i] : undefined);
    for (const get of [row, col]) {
      for (let j = 0; j + 7 <= size; j++) {
        if (!at(get, j, PATTERN)) continue;
        if (at(get, j - 4, LIGHT4) || at(get, j + 7, LIGHT4)) score += P3;
      }
    }
  }

  // Rule 4: deviation of the dark-module proportion from one half.
  let dark = 0;
  for (const row of modules) for (const m of row) if (m) dark++;
  const total = size * size;
  const k = Math.ceil(Math.abs(dark * 20 - total * 10) / total) - 1;
  score += Math.max(k, 0) * P4;
  return score;
}

/**
 * Encode a string as a QR matrix.
 *
 * The mask is chosen the way the specification says — draw all eight, score
 * each with the penalty rules, keep the lowest — rather than fixed. It costs
 * eight draws of a small grid and it is what keeps a payload with an unlucky
 * bit pattern from producing a code with finder-like noise in it.
 */
export function encodeQR(data: string, opts: EncodeOptions = {}): QRMatrix {
  const ecc = opts.ecc ?? "M";
  if (!ECC_ORDER.includes(ecc)) throw new Error(`qr: unknown error-correction level ${ecc}`);
  const min = Math.max(1, opts.minVersion ?? 1);
  const max = Math.min(40, opts.maxVersion ?? 40);
  if (min > max) throw new Error("qr: minVersion is above maxVersion");

  const bytes = utf8(data);
  const version = chooseVersion(bytes.length, ecc, min, max);
  const words = interleave(buildCodewords(bytes, version, ecc), version, ecc);

  let best: { mask: number; grid: Grid; score: number } | null = null;
  for (let mask = 0; mask < 8; mask++) {
    const g = newGrid(4 * version + 17);
    drawFunctionPatterns(g, version, ecc);
    placeData(g, words);
    applyMask(g, mask);
    drawFormat(g, ecc, mask);
    const score = penalty(g.modules);
    if (!best || score < best.score) best = { mask, grid: g, score };
  }
  const chosen = best as { mask: number; grid: Grid; score: number };
  return {
    size: chosen.grid.size,
    modules: chosen.grid.modules,
    version,
    ecc,
    mask: chosen.mask,
  };
}

/**
 * One SVG path covering every dark module, for a viewBox of
 * `size + 2 * quiet` units.
 *
 * A single path rather than one `<rect>` per module: a version 10 code is over
 * 3000 modules, and three thousand elements in a WKWebView is a real cost for
 * something that is a static picture. The quiet zone is part of the geometry
 * because a QR without one is materially harder to scan, and leaving it to the
 * caller means it gets forgotten.
 */
export function toPath(m: QRMatrix, quiet = 4): string {
  const parts: string[] = [];
  for (let y = 0; y < m.size; y++) {
    for (let x = 0; x < m.size; x++) {
      if (m.modules[y][x]) parts.push(`M${x + quiet} ${y + quiet}h1v1h-1z`);
    }
  }
  return parts.join("");
}

/** The viewBox side length that matches `toPath`'s output. */
export function viewBox(m: QRMatrix, quiet = 4): number {
  return m.size + 2 * quiet;
}
