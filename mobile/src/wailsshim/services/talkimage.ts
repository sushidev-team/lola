// Image attachments over the remote link (docs/mobile/02-events.md §12).
//
// Invariants: an upload is sequential parts, each sent after the previous reply;
// no image over ATTACH_MAX_BYTES moves in either direction; the thumb is built
// here because the daemon never has one to give.
import type * as M from "@bindings/desktop/models";
import { native } from "../../native";
import { ATTACH_CHUNK_BYTES, ATTACH_MAX_BYTES, CODE_RATE_LIMITED, WireError, WireRefusalError } from "../../wire";
import type { AttachImageArgs, AttachImageData, AttachmentImageArgs, AttachmentImageData } from "../../wire";
import { bridge } from "../bridge";
import type * as P from "@bindings/internal/protocol";

export const ATTACH_PART_BYTES = ATTACH_CHUNK_BYTES;
/** Resend delay after a `rate_limited` part, and how often one part may be resent. */
export const ATTACH_RATE_RETRY_MS = 1100;
export const ATTACH_RATE_RETRIES = 8;
/** Decoded images kept so a repeat view costs no round trips. */
export const MAX_CACHED_ATTACHMENTS = 16;

const THUMB_EDGE = 144;
const THUMB_QUALITY = 0.7;
const NAME_ALPHABET = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_";

const cache = new Map<string, string>();

function remember(path: string, url: string): void {
  cache.delete(path);
  cache.set(path, url);
  while (cache.size > MAX_CACHED_ATTACHMENTS) cache.delete(cache.keys().next().value as string);
}

/** Empties the data-URL cache. Tests only. */
export function clearAttachmentCache(): void {
  cache.clear();
}

function base64ToBytes(b64: string): Uint8Array<ArrayBuffer> {
  const bin = atob(b64);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

function bytesToBase64(bytes: Uint8Array): string {
  let bin = "";
  for (let i = 0; i < bytes.length; i += 0x8000) {
    bin += String.fromCharCode(...bytes.subarray(i, i + 0x8000));
  }
  return btoa(bin);
}

function uploadName(): string {
  const r = crypto.getRandomValues(new Uint8Array(16));
  return Array.from(r, (b) => NAME_ALPHABET[b & 63]).join("");
}

/** A 144 px long-edge JPEG data URL (q 0.7), or "" when the pixels cannot be decoded. */
async function makeThumb(mime: string, data: string): Promise<string> {
  try {
    const bitmap = await createImageBitmap(new Blob([base64ToBytes(data)], { type: mime }));
    const scale = Math.min(1, THUMB_EDGE / Math.max(bitmap.width, bitmap.height));
    const canvas = document.createElement("canvas");
    canvas.width = Math.max(1, Math.round(bitmap.width * scale));
    canvas.height = Math.max(1, Math.round(bitmap.height * scale));
    const ctx = canvas.getContext("2d");
    if (!ctx) return "";
    ctx.drawImage(bitmap, 0, 0, canvas.width, canvas.height);
    bitmap.close();
    return canvas.toDataURL("image/jpeg", THUMB_QUALITY);
  } catch {
    return "";
  }
}

const sleep = (ms: number) => new Promise<void>((r) => setTimeout(r, ms));

async function sendPart(args: AttachImageArgs): Promise<AttachImageData> {
  for (let retry = 0; ; retry++) {
    try {
      return await bridge.request<AttachImageData>("TalkService.AttachClipboardImage", "attachImage", { args });
    } catch (err) {
      const limited = err instanceof WireRefusalError && err.code === CODE_RATE_LIMITED;
      if (!limited || retry >= ATTACH_RATE_RETRIES) throw err;
      await sleep(ATTACH_RATE_RETRY_MS);
    }
  }
}

/** The session whose worktree is `worktree`, live first, else an ended one that kept it. */
async function sessionFor(worktree: string): Promise<string> {
  const live = await bridge.request<P.SessionsData>("TalkService.ReadAttachmentImage", "sessions");
  const hit = live?.sessions?.find((s) => s.worktree === worktree);
  if (hit) return hit.id;
  const ended = await bridge.request<P.EndedSessionsData>("TalkService.ReadAttachmentImage", "endedSessions");
  const kept = ended?.sessions?.find((s) => s.keptWorktree === worktree);
  if (kept) return kept.id;
  throw new WireError("Open on your Mac to view.");
}

async function uploadImage(a: { session: string; name: string; mime: string; data: string }): Promise<M.ImageAttachment> {
  const bytes = base64ToBytes(a.data);
  if (bytes.length === 0 || bytes.length > ATTACH_MAX_BYTES) throw new WireError("That image is too large.");
  const parts = Math.ceil(bytes.length / ATTACH_PART_BYTES);
  let done: AttachImageData | undefined;
  for (let part = 0; part < parts; part++) {
    const slice = bytes.subarray(part * ATTACH_PART_BYTES, (part + 1) * ATTACH_PART_BYTES);
    done = await sendPart({
      session: a.session,
      name: a.name,
      mime: a.mime,
      data: bytesToBase64(slice),
      part,
      ...(part === parts - 1 ? { last: true } : {}),
    });
  }
  const path = done?.path ?? "";
  if (!path) throw new WireError("The upload didn't finish.");
  const thumb = await makeThumb(a.mime, a.data);
  remember(path, `data:${a.mime};base64,${a.data}`);
  return { path, thumb };
}

/** Uploads an image the phone picked (camera or library) into `session`. */
export function UploadPickedImage(session: string, mime: string, data: string): Promise<M.ImageAttachment> {
  return uploadImage({ session, name: uploadName(), mime, data });
}

/** LOCAL + FORWARDED: paste the phone's clipboard image into the session behind `worktree`. */
export async function AttachClipboardImage(worktree: string): Promise<M.ImageAttachment> {
  if (!native) throw new WireError("No image to paste.");
  const clip = await native.readClipboardImage();
  if (!clip.present) throw new WireError("No image to paste.");
  const session = await sessionFor(worktree);
  return uploadImage({ session, name: uploadName(), mime: clip.mime, data: clip.data });
}

/** FORWARDED: cmd=attachmentImage, chunk by chunk, as a data URL. */
export async function ReadAttachmentImage(worktree: string, path: string): Promise<string> {
  const hit = cache.get(path);
  if (hit !== undefined) {
    remember(path, hit);
    return hit;
  }
  const session = await sessionFor(worktree);
  const chunks: Uint8Array[] = [];
  let mime = "";
  let total = 0;
  for (let offset = 0; ; offset += ATTACH_PART_BYTES) {
    const args: AttachmentImageArgs = { session, path, ...(offset ? { offset } : {}) };
    const d = await bridge.request<AttachmentImageData>("TalkService.ReadAttachmentImage", "attachmentImage", { args });
    if (d.size > ATTACH_MAX_BYTES) throw new WireError("That image is too large.");
    mime = d.mime;
    const part = base64ToBytes(d.data);
    total += part.length;
    if (total > ATTACH_MAX_BYTES) throw new WireError("That image is too large.");
    chunks.push(part);
    if (d.eof) break;
  }
  const all = new Uint8Array(total);
  let at = 0;
  for (const c of chunks) {
    all.set(c, at);
    at += c.length;
  }
  const url = `data:${mime};base64,${bytesToBase64(all)}`;
  remember(path, url);
  return url;
}
