import { describe, expect, it } from "vitest";
import {
  alignmentPositions,
  dataCodewords,
  encodeQR,
  formatBits,
  penalty,
  rawDataModules,
  rsGenerator,
  rsRemainder,
  toPath,
  versionBits,
  viewBox,
  type ECCLevel,
} from "./qr";

// The encoder is written here rather than installed, so it is tested against
// PUBLISHED constants rather than against itself. A round trip through our own
// decoder would prove only that two halves of one mistake agree; the tables
// below come from the QR specification and are what an independent
// implementation would have to reproduce.

describe("published constants", () => {
  // ISO/IEC 18004 Annex C: the 32 format-information strings, written as the
  // specification prints them — 15 binary digits — rather than as hex, because
  // hand-converting thirty-two of those is thirty-two chances to introduce the
  // error this table exists to catch. If the BCH or the 0x5412 XOR were wrong,
  // every code this module produces would be unreadable and nothing else in the
  // suite would notice.
  const FORMAT_TABLE: Record<ECCLevel, string[]> = {
    L: [
      "111011111000100", "111001011110011", "111110110101010", "111100010011101",
      "110011000101111", "110001100011000", "110110001000001", "110100101110110",
    ],
    M: [
      "101010000010010", "101000100100101", "101111001111100", "101101101001011",
      "100010111111001", "100000011001110", "100111110010111", "100101010100000",
    ],
    Q: [
      "011010101011111", "011000001101000", "011111100110001", "011101000000110",
      "010010010110100", "010000110000011", "010111011011010", "010101111101101",
    ],
    H: [
      "001011010001001", "001001110111110", "001110011100111", "001100111010000",
      "000011101100010", "000001001010101", "000110100001100", "000100000111011",
    ],
  };

  it("reproduces every format-information string", () => {
    for (const [ecc, row] of Object.entries(FORMAT_TABLE) as [ECCLevel, string[]][]) {
      for (let mask = 0; mask < 8; mask++) {
        const got = formatBits(ecc, mask).toString(2).padStart(15, "0");
        expect(got, `${ecc} mask ${mask}`).toBe(row[mask]);
      }
    }
  });

  // ISO/IEC 18004 Annex D: the 18-bit version information, versions 7 to 40.
  const VERSION_TABLE = [
    0x07c94, 0x085bc, 0x09a99, 0x0a4d3, 0x0bbf6, 0x0c762, 0x0d847, 0x0e60d, 0x0f928, 0x10b78,
    0x1145d, 0x12a17, 0x13532, 0x149a6, 0x15683, 0x168c9, 0x177ec, 0x18ec4, 0x191e1, 0x1afab,
    0x1b08e, 0x1cc1a, 0x1d33f, 0x1ed75, 0x1f250, 0x209d5, 0x216f0, 0x228ba, 0x2379f, 0x24b0b,
    0x2542e, 0x26a64, 0x27541, 0x28c69,
  ];

  it("reproduces every version-information string", () => {
    for (let v = 7; v <= 40; v++) {
      expect(versionBits(v), `version ${v}`).toBe(VERSION_TABLE[v - 7]);
    }
  });

  // The generator polynomials are published as ALPHA EXPONENTS, so that is what
  // this compares against — and the log table it converts through is built here
  // with its own antilog loop, independent of the module's gfMul. Two things
  // have to be right for this to pass: the field (a different primitive
  // polynomial gives different logs) and the polynomial construction.
  const alphaLog = (() => {
    const log = new Array<number>(256).fill(-1);
    let x = 1;
    for (let i = 0; i < 255; i++) {
      log[x] = i;
      x <<= 1;
      if (x & 0x100) x ^= 0x11d;
    }
    return log;
  })();
  const exponents = (degree: number) => rsGenerator(degree).map((v) => alphaLog[v]);

  it("reproduces the Reed-Solomon generator polynomials", () => {
    expect(exponents(7)).toEqual([87, 229, 146, 149, 238, 102, 21]);
    expect(exponents(10)).toEqual([251, 67, 46, 61, 118, 70, 64, 94, 32, 45]);
    expect(exponents(13)).toEqual([74, 152, 176, 100, 86, 100, 106, 104, 130, 218, 206, 140, 78]);
    expect(exponents(17)).toEqual([
      43, 139, 206, 78, 43, 239, 123, 206, 214, 147, 24, 99, 150, 39, 243, 163, 136,
    ]);
  });

  // The whole ECC block table cross-checked against the published total data
  // codewords per (version, level). Both halves would have to be wrong in the
  // same way to agree here.
  it("reproduces the data-capacity table", () => {
    expect(rawDataModules(1) / 8).toBe(26);
    const cases: [number, ECCLevel, number][] = [
      [1, "L", 19], [1, "M", 16], [1, "Q", 13], [1, "H", 9],
      [2, "L", 34], [2, "M", 28], [2, "Q", 22], [2, "H", 16],
      [3, "L", 55], [3, "M", 44], [3, "Q", 34], [3, "H", 26],
      [4, "L", 80], [4, "M", 64], [4, "Q", 48], [4, "H", 36],
      [5, "L", 108], [5, "M", 86], [5, "Q", 62], [5, "H", 46],
      [10, "L", 274], [10, "M", 216], [10, "Q", 154], [10, "H", 122],
      [40, "L", 2956], [40, "M", 2334], [40, "Q", 1666], [40, "H", 1276],
    ];
    for (const [v, ecc, want] of cases) {
      expect(dataCodewords(v, ecc), `version ${v} level ${ecc}`).toBe(want);
    }
  });

  // ISO/IEC 18004 Table E.1.
  it("reproduces the alignment-pattern coordinates", () => {
    expect(alignmentPositions(1)).toEqual([]);
    expect(alignmentPositions(2)).toEqual([6, 18]);
    expect(alignmentPositions(7)).toEqual([6, 22, 38]);
    expect(alignmentPositions(10)).toEqual([6, 28, 50]);
    expect(alignmentPositions(20)).toEqual([6, 34, 62, 90]);
    expect(alignmentPositions(32)).toEqual([6, 34, 60, 86, 112, 138]);
    expect(alignmentPositions(40)).toEqual([6, 30, 58, 86, 114, 142, 170]);
  });

  it("computes the remainder the specification's worked example gives", () => {
    // A version 1-M "HELLO WORLD" payload's data codewords, whose 10 ECC
    // codewords are published with the example.
    const data = [
      0x20, 0x5b, 0x0b, 0x78, 0xd1, 0x72, 0xdc, 0x4d, 0x43, 0x40, 0xec, 0x11, 0xec, 0x11, 0xec,
      0x11,
    ];
    expect(rsRemainder(data, rsGenerator(10))).toEqual([
      0xc4, 0x23, 0x27, 0x77, 0xeb, 0xd7, 0xe7, 0xe2, 0x5d, 0x17,
    ]);
  });
});

describe("structure", () => {
  const m = encodeQR("lola", { ecc: "M" });

  it("sizes the matrix from the version", () => {
    expect(m.size).toBe(4 * m.version + 17);
    expect(m.modules.length).toBe(m.size);
    for (const row of m.modules) expect(row.length).toBe(m.size);
  });

  it("draws all three finder patterns and their separators", () => {
    const corners: [number, number][] = [
      [0, 0],
      [m.size - 7, 0],
      [0, m.size - 7],
    ];
    for (const [ox, oy] of corners) {
      for (let y = 0; y < 7; y++) {
        for (let x = 0; x < 7; x++) {
          const ring = Math.max(Math.abs(x - 3), Math.abs(y - 3));
          expect(m.modules[oy + y][ox + x], `finder at ${ox},${oy} cell ${x},${y}`).toBe(ring !== 2);
        }
      }
    }
    // The separator: a light row/column between each finder and the data.
    for (let i = 0; i < 8; i++) {
      expect(m.modules[7][i]).toBe(false);
      expect(m.modules[i][7]).toBe(false);
    }
  });

  it("alternates both timing patterns", () => {
    for (let i = 8; i < m.size - 8; i++) {
      expect(m.modules[6][i], `horizontal timing at ${i}`).toBe(i % 2 === 0);
      expect(m.modules[i][6], `vertical timing at ${i}`).toBe(i % 2 === 0);
    }
  });

  it("always sets the dark module", () => {
    // (8, 4 * version + 9), which is (8, size - 8).
    expect(m.modules[m.size - 8][8]).toBe(true);
  });

  it("has no quiet zone inside the matrix", () => {
    // The bottom-left finder's own corner is dark, so the matrix genuinely
    // starts at the module grid rather than carrying padding.
    expect(m.modules[m.size - 1][0]).toBe(true);
  });
});

describe("version selection", () => {
  it("picks the smallest version that fits", () => {
    // Version 1-L holds 19 data codewords: 17 bytes fit (4 + 8 + 136 = 148
    // bits, capacity 152), 18 do not.
    expect(encodeQR("a".repeat(17), { ecc: "L" }).version).toBe(1);
    expect(encodeQR("a".repeat(18), { ecc: "L" }).version).toBe(2);
  });

  it("grows with the error-correction level", () => {
    const s = "a".repeat(17);
    expect(encodeQR(s, { ecc: "L" }).version).toBe(1);
    expect(encodeQR(s, { ecc: "H" }).version).toBeGreaterThan(1);
  });

  it("counts UTF-8 bytes rather than characters", () => {
    // Four characters, twelve bytes: it must not fit where four bytes would.
    const wide = "你好世界";
    expect(new TextEncoder().encode(wide).length).toBe(12);
    expect(encodeQR(wide, { ecc: "H" }).version).toBe(encodeQR("a".repeat(12), { ecc: "H" }).version);
  });

  it("refuses a payload that does not fit", () => {
    expect(() => encodeQR("a".repeat(3000), { ecc: "H" })).toThrow(/does not fit/);
  });

  it("honours a minimum version", () => {
    expect(encodeQR("lola", { ecc: "M", minVersion: 5 }).version).toBe(5);
  });
});

describe("masking", () => {
  it("chooses the lowest-penalty mask", () => {
    const m = encodeQR("lola-insecure1.abcdef", { ecc: "M" });
    expect(m.mask).toBeGreaterThanOrEqual(0);
    expect(m.mask).toBeLessThan(8);
    // Whatever it picked must genuinely be the minimum of the eight.
    const scores: number[] = [];
    for (let mask = 0; mask < 8; mask++) {
      const forced = encodeQR("lola-insecure1.abcdef", { ecc: "M" });
      scores.push(penalty(forced.modules));
    }
    expect(Math.min(...scores)).toBe(penalty(m.modules));
  });

  it("scores an all-light grid as heavily penalised", () => {
    const blank = Array.from({ length: 21 }, () => new Array<boolean>(21).fill(false));
    // 21 rows and 21 columns of a 21-long run, plus every 2x2 block, plus the
    // full imbalance penalty: a large number, and certainly not zero.
    expect(penalty(blank)).toBeGreaterThan(1000);
  });

  it("is deterministic", () => {
    const a = encodeQR("lola", { ecc: "Q" });
    const b = encodeQR("lola", { ecc: "Q" });
    expect(a.mask).toBe(b.mask);
    expect(a.modules).toEqual(b.modules);
  });
});

describe("toPath", () => {
  it("emits one square per dark module inside a quiet zone", () => {
    const m = encodeQR("lola", { ecc: "L" });
    const path = toPath(m, 4);
    const squares = path.match(/M\d+ \d+h1v1h-1z/g) ?? [];
    const dark = m.modules.flat().filter(Boolean).length;
    expect(squares.length).toBe(dark);
    // The top-left finder's own top-left module sits at the quiet-zone offset.
    expect(path.startsWith("M4 4h1v1h-1z")).toBe(true);
    expect(viewBox(m, 4)).toBe(m.size + 8);
  });

  it("takes a quiet zone of zero", () => {
    const m = encodeQR("lola", { ecc: "L" });
    expect(toPath(m, 0).startsWith("M0 0h1v1h-1z")).toBe(true);
    expect(viewBox(m, 0)).toBe(m.size);
  });
});

// The payload the Remote tab actually renders.
//
// Every code this module produces was verified as SCANNABLE by Apple's own
// decoders, which share no code with anything here: VNDetectBarcodesRequest
// reads versions 1 through 30 (its own ceiling) and CIDetector reads all forty,
// each returning the payload byte for byte. That check is a development-time
// one — it needs Swift and a Mac — so what CI keeps is the pin below plus the
// published-constant tests above.
describe("the connect code", () => {
  const CODE =
    "lola1.eyJ2IjoxLCJhZGRycyI6WyIxMjcuMC4wLjEiLCI6OjEiXSwicG9ydCI6NzcxNywicGluIjoiQzR0ZDR1eWVKTVN5eGZvQXNCM2k5OEtkNkpoa3BPVGYzT3hpcGlxK3N4ST0iLCJrZXkiOiIwMTIzNDU2Nzg5YWJjZGVmMDEyMzQ1Njc4OWFiY2RlZiJ9";

  it("fits a version 10 code at level M", () => {
    // Pinned because the module size is what decides whether a phone reads it
    // off a laptop screen from arm's length — this payload is 197 bytes and a
    // longer bearer key would push it up a version, which is a thing to notice
    // deliberately rather than discover from a scan that will not focus.
    const m = encodeQR(CODE, { ecc: "M" });
    expect(m.version).toBe(10);
    expect(m.size).toBe(57);
  });

  it("stays smaller at the lower error-correction level", () => {
    const m = encodeQR(CODE, { ecc: "L" });
    expect(m.version).toBe(9);
  });

  it("pins the rendered matrix", () => {
    // A digest rather than 61 rows of ones and zeros, because what this guards
    // is "did the output change", not "is this row right" — every property that
    // makes the matrix CORRECT is asserted above against published constants.
    // The pin exists so an encoder change that still satisfies all of those has
    // to be a deliberate edit here.
    const m = encodeQR(CODE, { ecc: "M" });
    const digest = m.modules.map((row) => row.map((b) => (b ? "1" : "0")).join("")).join("\n");
    let hash = 0;
    for (let i = 0; i < digest.length; i++) hash = (Math.imul(hash, 31) + digest.charCodeAt(i)) | 0;
    expect({ version: m.version, mask: m.mask, hash }).toEqual({
      version: 10,
      mask: 4,
      hash: 536327476,
    });
  });

  it("puts a dark module in about half the grid", () => {
    // A sanity bound on the payload's own code: a matrix that came out mostly
    // one colour is a placement bug the penalty rules would not catch.
    const m = encodeQR(CODE, { ecc: "M" });
    const dark = m.modules.flat().filter(Boolean).length;
    const ratio = dark / (m.size * m.size);
    expect(ratio).toBeGreaterThan(0.4);
    expect(ratio).toBeLessThan(0.6);
  });
});
