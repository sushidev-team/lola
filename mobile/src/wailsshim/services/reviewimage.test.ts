import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("../bridge", () => ({ bridge: { request: vi.fn() } }));

import { bridge } from "../bridge";
import { ReviewPackageImage } from "./daemon";
import { MAX_CACHED_REVIEW_IMAGE_BYTES, REVIEW_IMAGE_CHUNK_BYTES, REVIEW_IMAGE_MAX_BYTES, clearReviewImageCache } from "./reviewimage";

const request = vi.mocked(bridge.request);

const b64 = (bytes: Uint8Array) => Buffer.from(bytes).toString("base64");

/** Bytes whose value depends on their position, so a misplaced chunk shows. */
function pattern(n: number): Uint8Array {
  const out = new Uint8Array(n);
  for (let i = 0; i < n; i++) out[i] = (i * 31 + 7) & 255;
  return out;
}

/** A daemon serving `raw` in chunked parts, as handleReviewPackage does. */
function serve(raw: Uint8Array, mime = "image/png") {
  const offsets: number[] = [];
  request.mockImplementation(async (_m, _cmd, f) => {
    const a = (f as { args: { offset?: number; chunked?: boolean } }).args;
    const offset = a.offset ?? 0;
    offsets.push(offset);
    const end = Math.min(offset + REVIEW_IMAGE_CHUNK_BYTES, raw.length);
    return { dataURL: "", mime, size: raw.length, offset, data: b64(raw.subarray(offset, end)), eof: end === raw.length };
  });
  return offsets;
}

beforeEach(() => {
  request.mockReset();
  clearReviewImageCache();
});

describe("ReviewPackageImage", () => {
  it("reads three chunks in order and assembles the exact bytes", async () => {
    const raw = pattern(2 * REVIEW_IMAGE_CHUNK_BYTES + 5);
    const offsets = serve(raw);

    const r = await ReviewPackageImage("s1", "p1", "shot");

    expect(offsets).toEqual([0, REVIEW_IMAGE_CHUNK_BYTES, 2 * REVIEW_IMAGE_CHUNK_BYTES]);
    expect(request.mock.calls[0]).toEqual([
      "DaemonService.ReviewPackageImage",
      "reviewPackageImage",
      { session: "s1", args: { package: "p1", artifact: "shot", chunked: true } },
    ]);
    expect(r.dataURL).toBe(`data:image/png;base64,${b64(raw)}`);
  });

  it("answers a repeat view from the cache without a request", async () => {
    serve(pattern(10));
    const first = await ReviewPackageImage("s1", "p1", "shot");
    request.mockClear();

    const again = await ReviewPackageImage("s1", "p1", "shot");

    expect(request).not.toHaveBeenCalled();
    expect(again.dataURL).toBe(first.dataURL);
  });

  it("bounds the cache by total bytes, evicting the oldest", async () => {
    const big = (n: number) => `data:image/png;base64,${"A".repeat(n)}`;
    const each = Math.floor(MAX_CACHED_REVIEW_IMAGE_BYTES / 2) + 1024;
    request.mockImplementation(async () => ({ dataURL: big(each) }));
    await ReviewPackageImage("s1", "p1", "a");
    await ReviewPackageImage("s1", "p1", "b");
    await ReviewPackageImage("s1", "p1", "c");
    request.mockClear();

    await ReviewPackageImage("s1", "p1", "c");
    expect(request).not.toHaveBeenCalled();
    await ReviewPackageImage("s1", "p1", "a");
    expect(request).toHaveBeenCalledTimes(1);
  });

  it("shares one in-flight load per key", async () => {
    const raw = pattern(2 * REVIEW_IMAGE_CHUNK_BYTES + 5);
    const offsets = serve(raw);

    const [x, y] = await Promise.all([ReviewPackageImage("s1", "p1", "shot"), ReviewPackageImage("s1", "p1", "shot")]);

    expect(offsets).toEqual([0, REVIEW_IMAGE_CHUNK_BYTES, 2 * REVIEW_IMAGE_CHUNK_BYTES]);
    expect(x.dataURL).toBe(y.dataURL);
  });

  it("does not keep a failed load in flight", async () => {
    request.mockRejectedValueOnce(new Error("offline"));
    await expect(ReviewPackageImage("s1", "p1", "shot")).rejects.toThrow("offline");
    serve(pattern(10));
    await expect(ReviewPackageImage("s1", "p1", "shot")).resolves.toHaveProperty("dataURL");
  });

  it("uses an older daemon's whole data URL as it is", async () => {
    request.mockResolvedValue({ dataURL: "data:image/png;base64,AAAA" });

    const r = await ReviewPackageImage("s1", "p1", "shot");

    expect(request).toHaveBeenCalledTimes(1);
    expect(r.dataURL).toBe("data:image/png;base64,AAAA");
  });

  it("refuses an image over the retained limit", async () => {
    request.mockResolvedValue({ dataURL: "", mime: "image/png", size: REVIEW_IMAGE_MAX_BYTES + 1, data: "AAAA", eof: false });

    await expect(ReviewPackageImage("s1", "p1", "shot")).rejects.toThrow("This screenshot is too large.");
  });

  it("refuses a short part that is not the last", async () => {
    request.mockResolvedValue({ dataURL: "", mime: "image/png", size: 99, data: "AAAA", eof: false });

    await expect(ReviewPackageImage("s1", "p1", "shot")).rejects.toThrow("This screenshot could not be loaded.");
    expect(request).toHaveBeenCalledTimes(1);
  });

  it("refuses an answer that is not an image", async () => {
    request.mockResolvedValue({ dataURL: "", mime: "text/html", size: 3, data: "AAAA", eof: true });

    await expect(ReviewPackageImage("s1", "p1", "shot")).rejects.toThrow("This screenshot could not be loaded.");
  });
});
