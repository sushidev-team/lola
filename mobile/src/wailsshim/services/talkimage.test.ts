import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const clip = vi.hoisted(() => ({ native: { readClipboardImage: vi.fn() } }));
vi.mock("../bridge", () => ({ bridge: { request: vi.fn() } }));
vi.mock("../../native", () => clip);

import { ATTACH_CHUNK_BYTES, ATTACH_MAX_BYTES, CODE_RATE_LIMITED, WireRefusalError } from "../../wire";
import type { AttachImageArgs } from "../../wire";
import { bridge } from "../bridge";
import {
  ATTACH_RATE_RETRIES,
  ATTACH_RATE_RETRY_MS,
  MAX_CACHED_ATTACHMENTS,
  UploadPickedImage,
  clearAttachmentCache,
} from "./talkimage";
import { AttachClipboardImage, ReadAttachmentImage } from "./talk";

const request = vi.mocked(bridge.request);

const b64 = (bytes: Uint8Array) => Buffer.from(bytes).toString("base64");
const bytesOf = (n: number, fill = 7) => new Uint8Array(n).fill(fill);
const SESSIONS = { sessions: [{ id: "s1", worktree: "/w/s1" }] };

beforeEach(() => {
  request.mockReset();
  clip.native.readClipboardImage.mockReset();
  clearAttachmentCache();
});
afterEach(() => vi.useRealTimers());

/** A daemon that serves sessions and records upload parts. */
function daemon(onPart: (a: AttachImageArgs) => unknown) {
  const parts: AttachImageArgs[] = [];
  request.mockImplementation(async (_m, cmd, f) => {
    if (cmd === "sessions") return SESSIONS;
    if (cmd === "endedSessions") return { sessions: [] };
    const a = (f as { args: AttachImageArgs }).args;
    parts.push(a);
    return onPart(a);
  });
  return parts;
}

describe("UploadPickedImage", () => {
  it("uploads a picked image into the named session without a session lookup", async () => {
    const raw = bytesOf(ATTACH_CHUNK_BYTES + 1);
    const parts = daemon((a) => (a.last ? { received: raw.length, path: "/w/s1/.lola/images/pick.jpg" } : { received: 1 }));

    const r = await UploadPickedImage("s1", "image/jpeg", b64(raw));

    expect(request.mock.calls.map((c) => c[1])).toEqual(["attachImage", "attachImage"]);
    expect(parts.map((p) => [p.session, p.mime, p.part, p.last])).toEqual([
      ["s1", "image/jpeg", 0, undefined],
      ["s1", "image/jpeg", 1, true],
    ]);
    expect(r.path).toBe("/w/s1/.lola/images/pick.jpg");
  });

  it("refuses an image over the limit before sending a part", async () => {
    await expect(UploadPickedImage("s1", "image/jpeg", b64(bytesOf(ATTACH_MAX_BYTES + 1)))).rejects.toThrow(
      "That image is too large.",
    );
    expect(request).not.toHaveBeenCalled();
  });
});

describe("AttachClipboardImage", () => {
  it("rejects when the clipboard holds no image", async () => {
    clip.native.readClipboardImage.mockResolvedValue({ present: false });
    await expect(AttachClipboardImage("/w/s1")).rejects.toThrow("No image to paste.");
  });

  it("uploads sequential parts, marks only the last, and resolves the path", async () => {
    const raw = bytesOf(ATTACH_CHUNK_BYTES + 10);
    clip.native.readClipboardImage.mockResolvedValue({ present: true, mime: "image/png", data: b64(raw) });
    const parts = daemon((a) => (a.last ? { received: raw.length, path: "/w/s1/.lola/images/paste-1.png" } : { received: 1 }));

    const r = await AttachClipboardImage("/w/s1");

    expect(parts.map((p) => p.part)).toEqual([0, 1]);
    expect(parts.map((p) => p.last)).toEqual([undefined, true]);
    expect(parts.every((p) => p.session === "s1" && p.mime === "image/png")).toBe(true);
    expect(parts[0].name).toMatch(/^[A-Za-z0-9_-]{16}$/);
    expect(new Set(parts.map((p) => p.name)).size).toBe(1);
    expect(atob(parts[0].data)).toHaveLength(ATTACH_CHUNK_BYTES);
    expect(atob(parts[1].data)).toHaveLength(10);
    expect(r.path).toBe("/w/s1/.lola/images/paste-1.png");
    expect(r.thumb).toBe("");
  });

  it("resends a rate-limited part unchanged after a pause, then gives up", async () => {
    vi.useFakeTimers();
    clip.native.readClipboardImage.mockResolvedValue({ present: true, mime: "image/png", data: b64(bytesOf(10)) });
    let calls = 0;
    const parts = daemon((a) => {
      if (calls++ < 2) throw new WireRefusalError(CODE_RATE_LIMITED, "slow down");
      return { received: 10, path: a.last ? "/p.png" : undefined };
    });
    const done = AttachClipboardImage("/w/s1");
    await vi.advanceTimersByTimeAsync(ATTACH_RATE_RETRY_MS * 2);
    await expect(done).resolves.toMatchObject({ path: "/p.png" });
    expect(parts).toHaveLength(3);
    expect(parts[0]).toEqual(parts[2]);

    calls = -1000;
    parts.length = 0;
    const fail = AttachClipboardImage("/w/s1").catch((e: unknown) => e);
    await vi.advanceTimersByTimeAsync(ATTACH_RATE_RETRY_MS * (ATTACH_RATE_RETRIES + 1));
    expect(await fail).toBeInstanceOf(WireRefusalError);
    expect(parts).toHaveLength(ATTACH_RATE_RETRIES + 1);
  });

  it("aborts on any other refusal with the daemon's sentence", async () => {
    clip.native.readClipboardImage.mockResolvedValue({ present: true, mime: "image/png", data: b64(bytesOf(10)) });
    daemon(() => {
      throw new Error("image too large for this session");
    });
    await expect(AttachClipboardImage("/w/s1")).rejects.toThrow("image too large for this session");
  });

  it("routes a paste to the selected tab when two sessions share a checkout", async () => {
    clip.native.readClipboardImage.mockResolvedValue({ present: true, mime: "image/png", data: b64(bytesOf(10)) });
    request.mockResolvedValueOnce({ sessions: [
      { id: "owner", worktree: "/checkout" },
      { id: "tab", worktree: "/checkout", attachmentRoot: "/private/tab" },
    ] }).mockResolvedValueOnce({ received: 10, path: "/private/tab/.lola/images/a.png" });
    await AttachClipboardImage("/private/tab");
    expect(request.mock.calls[1][2]).toMatchObject({ args: { session: "tab" } });
  });

  it("says to open the Mac when no session owns the worktree", async () => {
    clip.native.readClipboardImage.mockResolvedValue({ present: true, mime: "image/png", data: b64(bytesOf(10)) });
    daemon(() => ({}));
    await expect(AttachClipboardImage("/elsewhere")).rejects.toThrow("Open on your Mac to view.");
  });
});

describe("ReadAttachmentImage", () => {
  const chunks = (total: number) =>
    request.mockImplementation(async (_m, cmd, f) => {
      if (cmd === "sessions") return SESSIONS;
      const offset = (f as { args: { offset?: number } }).args.offset ?? 0;
      const n = Math.min(ATTACH_CHUNK_BYTES, total - offset);
      return { mime: "image/png", size: total, offset, data: b64(bytesOf(n)), eof: offset + n >= total };
    });
  const attachCalls = () => request.mock.calls.filter((c) => c[1] === "attachmentImage");

  it("fetches chunks from offset 0 until eof and returns a data URL", async () => {
    chunks(ATTACH_CHUNK_BYTES + 5);
    const url = await ReadAttachmentImage("/w/s1", "/w/s1/.lola/images/a.png");
    expect(url.startsWith("data:image/png;base64,")).toBe(true);
    expect(atob(url.split(",")[1])).toHaveLength(ATTACH_CHUNK_BYTES + 5);
    expect(attachCalls().map((c) => (c[2] as { args: { offset?: number } }).args.offset)).toEqual([undefined, ATTACH_CHUNK_BYTES]);
  });

  it("serves a repeat from the cache without a request", async () => {
    chunks(10);
    const first = await ReadAttachmentImage("/w/s1", "/p/a.png");
    request.mockClear();
    await expect(ReadAttachmentImage("/w/s1", "/p/a.png")).resolves.toBe(first);
    expect(request).not.toHaveBeenCalled();
  });

  it("evicts the least recently used beyond the cap", async () => {
    chunks(10);
    for (let i = 0; i <= MAX_CACHED_ATTACHMENTS; i++) await ReadAttachmentImage("/w/s1", `/p/${i}.png`);
    request.mockClear();
    await ReadAttachmentImage("/w/s1", "/p/1.png");
    expect(request).not.toHaveBeenCalled();
    await ReadAttachmentImage("/w/s1", "/p/0.png");
    expect(request).toHaveBeenCalled();
  });

  it("refuses an image over the size cap", async () => {
    request.mockImplementation(async (_m, cmd) =>
      cmd === "sessions" ? SESSIONS : { mime: "image/png", size: ATTACH_MAX_BYTES + 1, offset: 0, data: b64(bytesOf(4)) },
    );
    await expect(ReadAttachmentImage("/w/s1", "/p/big.png")).rejects.toThrow("too large");
  });

  it("finds an ended session by its kept worktree", async () => {
    request.mockImplementation(async (_m, cmd) => {
      if (cmd === "sessions") return { sessions: [] };
      if (cmd === "endedSessions") return { sessions: [{ id: "old", keptWorktree: "/w/old" }] };
      return { mime: "image/png", size: 4, offset: 0, data: b64(bytesOf(4)), eof: true };
    });
    await ReadAttachmentImage("/w/old", "/p/x.png");
    expect((attachCalls()[0][2] as { args: { session: string } }).args.session).toBe("old");
  });

  it("rejects when neither a live nor an ended session has the worktree", async () => {
    request.mockImplementation(async (_m, cmd) => (cmd === "sessions" ? { sessions: [] } : { sessions: [] }));
    await expect(ReadAttachmentImage("/w/none", "/p/x.png")).rejects.toThrow("Open on your Mac to view.");
  });
});
