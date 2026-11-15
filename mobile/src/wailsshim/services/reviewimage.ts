// Review-package screenshots over the remote link.
//
// A retained screenshot may be up to 4 MiB, and the desktop's read returns it
// whole as one data URL, which is over a phone's 1 MiB frame cap. The phone
// reads it in chunks instead (reviewPackageImage with `chunked`) and
// assembles the data URL here. A daemon that predates chunked reads ignores
// the flag and answers with the whole data URL, which is used as it is.
import type * as P from "@bindings/internal/protocol";
import { ATTACH_CHUNK_BYTES, WireError } from "../../wire";
import { bridge } from "../bridge";
import { base64ToBytes, bytesToBase64 } from "./talkimage";

/** Raw bytes per part: the daemon's ReviewImageChunkBytes. */
export const REVIEW_IMAGE_CHUNK_BYTES = ATTACH_CHUNK_BYTES;
/** reviewpack.MaxImage: no retained screenshot is larger. */
export const REVIEW_IMAGE_MAX_BYTES = 4 << 20;
/** Total string length of assembled screenshots kept, so reopening a package costs no round trips. */
export const MAX_CACHED_REVIEW_IMAGE_BYTES = 24 << 20;

const cache = new Map<string, string>();
let cachedBytes = 0;
/** One load per key at a time: a second caller shares the first's promise. */
const inflight = new Map<string, Promise<P.ReviewImageData>>();

function remember(key: string, url: string): void {
  const old = cache.get(key);
  if (old !== undefined) cachedBytes -= old.length;
  cache.delete(key);
  cache.set(key, url);
  cachedBytes += url.length;
  // Oldest first; the newest entry always stays, even when it alone is over the bound.
  while (cachedBytes > MAX_CACHED_REVIEW_IMAGE_BYTES && cache.size > 1) {
    const k = cache.keys().next().value as string;
    cachedBytes -= cache.get(k)!.length;
    cache.delete(k);
  }
}

/** Empties the screenshot cache. Tests only. */
export function clearReviewImageCache(): void {
  cache.clear();
  cachedBytes = 0;
  inflight.clear();
}

/** FORWARDED: cmd=reviewPackageImage, chunk by chunk, answered as the desktop's whole data URL. */
export function ReadReviewImage(session: string, packageID: string, artifact: string): Promise<P.ReviewImageData> {
  // Artifacts are content-addressed, so a cached answer can never be stale.
  const key = `${session}\u0000${packageID}\u0000${artifact}`;
  const hit = cache.get(key);
  if (hit !== undefined) {
    remember(key, hit);
    return Promise.resolve({ dataURL: hit });
  }
  const running = inflight.get(key);
  if (running) return running;
  const p = load(key, session, packageID, artifact).finally(() => {
    if (inflight.get(key) === p) inflight.delete(key);
  });
  inflight.set(key, p);
  return p;
}

async function load(key: string, session: string, packageID: string, artifact: string): Promise<P.ReviewImageData> {
  const chunks: Uint8Array[] = [];
  let mime = "";
  let total = 0;
  for (let offset = 0; ; offset += REVIEW_IMAGE_CHUNK_BYTES) {
    const args = { package: packageID, artifact, chunked: true, ...(offset ? { offset } : {}) };
    const d = await bridge.request<P.ReviewImageData>("DaemonService.ReviewPackageImage", "reviewPackageImage", { session, args });
    if (d.dataURL) {
      remember(key, d.dataURL); // an older daemon answered with the whole image
      return { dataURL: d.dataURL };
    }
    if ((d.size ?? 0) > REVIEW_IMAGE_MAX_BYTES) throw new WireError("This screenshot is too large.");
    if (mime && d.mime !== mime) throw new WireError("This screenshot could not be loaded.");
    mime = d.mime ?? "";
    const part = base64ToBytes(d.data ?? "");
    total += part.length;
    if (total > REVIEW_IMAGE_MAX_BYTES) throw new WireError("This screenshot is too large.");
    chunks.push(part);
    if (d.eof) break;
    if (part.length !== REVIEW_IMAGE_CHUNK_BYTES) throw new WireError("This screenshot could not be loaded.");
  }
  if (!mime.startsWith("image/")) throw new WireError("This screenshot could not be loaded.");
  const all = new Uint8Array(total);
  let at = 0;
  for (const c of chunks) {
    all.set(c, at);
    at += c.length;
  }
  const url = `data:${mime};base64,${bytesToBase64(all)}`;
  remember(key, url);
  return { dataURL: url };
}
