// Pure helpers for team session sharing (docs/team-sessions.md): the words and
// the small decisions the share controls and the Team view make. No daemon
// calls and no store, so every rule here is testable on its own.
import type { ShareMemberInfo, ShareOffer, ShareViewer } from "@bindings/internal/protocol";

export type ShareLevel = "view" | "type";

export const LEVELS: readonly { id: ShareLevel; icon: "eye" | "keyboard"; label: string; hint: string }[] = [
  { id: "view", icon: "eye", label: "View", hint: "Teammates watch the terminal" },
  { id: "type", icon: "keyboard", label: "Type", hint: "Teammates can also type into the terminal" },
];

/** The level an unknown value means: the safer one. */
export function levelOf(v: string | undefined | null): ShareLevel {
  return v === "type" ? "type" : "view";
}

/** "Ada" / "Ada and Bob" / "Ada, Bob and 2 more". */
export function attachedLine(viewers: readonly Pick<ShareViewer, "name">[] | null | undefined): string {
  const names = (viewers ?? []).map((v) => v.name.trim()).filter(Boolean);
  if (names.length === 0) return "";
  if (names.length === 1) return names[0];
  if (names.length === 2) return `${names[0]} and ${names[1]}`;
  return `${names[0]}, ${names[1]} and ${names.length - 2} more`;
}

/** Up to two initials for a name: "Ada · office mini" → "A", "Bob Stone" → "BS". */
export function initials(name: string): string {
  const head = name.split("·")[0].trim();
  const parts = head.split(/\s+/).filter(Boolean);
  if (parts.length === 0) return "?";
  const first = [...parts[0]][0] ?? "?";
  const second = parts.length > 1 ? ([...parts[1]][0] ?? "") : "";
  return (first + second).toUpperCase();
}

/** A fingerprint is shown as given (groups of four hex), never reflowed. */
export function sameFingerprint(a: string, b: string): boolean {
  const norm = (s: string) => s.replace(/\s+/g, "").toLowerCase();
  return norm(a) !== "" && norm(a) === norm(b);
}

export type OfferGroup = { owner: string; ownerName: string; online: boolean; offers: ShareOffer[] };

/** Offers grouped by the machine that offers them, online machines first. */
export function groupOffers(offers: readonly ShareOffer[] | null | undefined): OfferGroup[] {
  const by = new Map<string, OfferGroup>();
  for (const o of offers ?? []) {
    let g = by.get(o.ownerDevice);
    if (!g) {
      g = { owner: o.ownerDevice, ownerName: o.ownerMember ? `${o.ownerMember} · ${o.ownerName}` : o.ownerName, online: false, offers: [] };
      by.set(o.ownerDevice, g);
    }
    g.offers.push(o);
    g.online = g.online || o.online;
  }
  return [...by.values()].sort((a, b) => Number(b.online) - Number(a.online) || a.ownerName.localeCompare(b.ownerName));
}

/** Why an offer cannot be attached right now ("" = it can). */
export function attachBlock(o: Pick<ShareOffer, "online" | "trusted" | "ownerName">): string {
  if (!o.trusted) return `Trust ${o.ownerName} first`;
  if (!o.online) return `${o.ownerName} is offline`;
  return "";
}

/** The stream name TermService attaches a share under (shareterm.Prefix). */
export const SHARE_STREAM_PREFIX = "share-";
export const shareStreamName = (shareId: string) => `${SHARE_STREAM_PREFIX}${shareId}`;
export const isShareStream = (name: string) => name.startsWith(SHARE_STREAM_PREFIX);

/** Members who may be offered as an audience: the ones this Mac allowed. */
export function audienceChoices(members: readonly ShareMemberInfo[] | null | undefined): ShareMemberInfo[] {
  return (members ?? []).filter((m) => m.allowed);
}

/** The tooltip of the share button. */
export function shareTitle(shared: boolean, attached: number): string {
  if (!shared) return "Share with team";
  return attached > 0 ? `Shared with your team · ${attached} attached` : "Shared with your team";
}
