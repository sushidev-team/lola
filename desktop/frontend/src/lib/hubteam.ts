// Creating a team and administering it: the form-side checks for the create
// code, the team name and the team's Linear and digest settings.
//
// Mirrors of hubproto (hubproto/teams.go), so a form can say what is wrong
// before anything is sent. The daemon checks again and the hub is the real
// gate.
//
// Rules this module keeps:
//   - A create code, a Linear key and a digest webhook are secrets. Every
//     error here is a fixed sentence that echoes no part of the input.
//   - Nothing here decides who is an admin: the hub does, on every request.

import { hubURLError } from "$lib/hubinvite";

export const CREATE_PREFIX = "lola-hub-create:";
/** hubproto's maxInviteLen. */
const MAX_CODE = 4 << 10;
/** hubproto's one-time token shape. */
const TOKEN_RE = /^[A-Za-z0-9_-]{16,128}$/;
/** hubproto.ValidOrg: a lowercase slug. */
export const TEAM_NAME_RE = /^[a-z0-9][a-z0-9_-]{0,63}$/;
/** hubproto.ValidLinearKey. */
export const LINEAR_KEY_RE = /^lin_api_[A-Za-z0-9]{16,128}$/;
/** hubproto.ValidDigestWebhook: a Slack incoming webhook and nothing else. */
export const DIGEST_WEBHOOK_RE = /^https:\/\/hooks\.slack\.com\/services\/[A-Za-z0-9]{1,32}\/[A-Za-z0-9]{1,32}\/[A-Za-z0-9]{1,64}$/;
/** hubproto.ValidMemberLabel. */
const LABEL_RE = /^[A-Za-z0-9][A-Za-z0-9 ._-]{0,39}$/;

/** open: a bare hub address, for a hub that takes a creation without a code. */
export type CreateCodeResult = { ok: true; url: string; open?: true } | { ok: false; error: string };

/**
 * Parses lola-hub-create:<url>#<code>, or a bare hub address for a hub its
 * operator opened to creation without a code. Errors never echo the input.
 */
export function parseCreateCode(input: string): CreateCodeResult {
  const s = input.trim();
  const fail = (error: string): CreateCodeResult => ({ ok: false, error });
  if (!s) return fail("Paste the create code from your hub operator, or the hub’s address.");
  if (s.length > MAX_CODE) return fail("This is too long to be a create code.");
  if (/[ \t\r\n]/.test(s)) return fail("A create code is one line with no spaces. Copy it again in one piece.");
  if (s.startsWith("lola-hub:")) return fail("This is an invite to an existing team. Use “Join a team” instead.");
  if (/^https?:\/\//i.test(s)) {
    const url = s.replace(/\/$/, "");
    if (hubURLError(url)) return fail("This hub address is not valid. Use the hub’s base URL, like https://hub.example.com.");
    return { ok: true, url, open: true };
  }
  if (!s.startsWith(CREATE_PREFIX)) return fail("This is not a create code. It starts with lola-hub-create:");
  const rest = s.slice(CREATE_PREFIX.length);
  const at = rest.indexOf("#");
  if (at < 0) return fail("This create code is incomplete. Copy it again in one piece.");
  const url = rest.slice(0, at).replace(/\/$/, "");
  if (hubURLError(url)) return fail("The create code’s hub URL is not valid. Ask your hub operator for a new one.");
  if (!TOKEN_RE.test(rest.slice(at + 1))) return fail("This create code is incomplete. Copy it again in one piece.");
  return { ok: true, url };
}

/**
 * What a person enters first to create a team: the hub's address. A whole
 * create line (lola-hub-create:<url>#<code>) is taken too, since it names the
 * hub: `line` is then set and no code is asked again.
 */
export type HubAddressResult = { ok: true; url: string; line?: string } | { ok: false; error: string };

export function parseHubAddress(input: string): HubAddressResult {
  const s = input.trim();
  if (!s) return { ok: false, error: "Enter the hub’s address, like https://hub.example.com." };
  const r = parseCreateCode(s);
  if (!r.ok) return r;
  return r.open ? { ok: true, url: r.url } : { ok: true, url: r.url, line: s };
}

export type CreateArgResult = { ok: true; code: string } | { ok: false; error: string };

/**
 * The create code a person enters for a hub that asked for one: the code
 * alone, or the whole line the operator sent. The answer is what the daemon
 * takes (the whole line). A line for ANOTHER hub is refused. Errors never
 * echo the input.
 */
export function createCodeFor(hubURL: string, input: string): CreateArgResult {
  const s = input.trim();
  if (!s) return { ok: false, error: "Paste the create code from your hub operator." };
  if (TOKEN_RE.test(s)) return { ok: true, code: `${CREATE_PREFIX}${hubURL}#${s}` };
  const r = parseCreateCode(s);
  if (!r.ok) return r.error.startsWith("This is not a create code") ? { ok: false, error: "This is not a create code. Copy it again in one piece." } : r;
  if (r.open) return { ok: false, error: "This is an address, not a create code." };
  if (r.url !== hubURL) return { ok: false, error: "This create code is for another hub. Change the hub address, or ask for a code for this hub." };
  return { ok: true, code: s };
}

/** Turns what a person types into a team name: lowercase, dashes for gaps. */
export function slugTeamName(raw: string): string {
  return raw.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+/, "").slice(0, 64);
}

/** The inline error for a team name ("" = fine or empty). */
export function teamNameError(name: string): string {
  if (!name) return "";
  return TEAM_NAME_RE.test(name) ? "" : "Use lowercase letters, digits and dashes, starting with a letter or digit.";
}

/** The inline error for a pasted Linear key ("" = fine or empty). */
export function linearKeyError(key: string): string {
  const s = key.trim();
  if (!s) return "";
  return LINEAR_KEY_RE.test(s) ? "" : "A Linear API key starts with lin_api_. Create one in Linear under Settings, Security & access.";
}

/** The inline error for a pasted digest webhook ("" = fine or empty). */
export function digestWebhookError(hook: string): string {
  const s = hook.trim();
  if (!s) return "";
  return DIGEST_WEBHOOK_RE.test(s) ? "" : "Only a Slack incoming webhook works here: https://hooks.slack.com/services/…";
}

/** The inline error for the teammate an invite is for ("" = fine or empty). */
export function inviteLabelError(label: string): string {
  const s = label.trim();
  if (!s) return "";
  return LABEL_RE.test(s) ? "" : "Use letters, digits, spaces, dots and dashes, up to 40 characters.";
}

/** Invite lifetimes on offer (seconds; hubproto allows a minute to 30 days). */
export const INVITE_LIFETIMES = [
  { value: 24 * 3600, label: "1 day" },
  { value: 7 * 24 * 3600, label: "7 days" },
  { value: 30 * 24 * 3600, label: "30 days" },
];

/** The hub's error classes in plain words; an unknown class reads as itself. */
export function statusClassText(cls: string | undefined): string {
  if (!cls) return "";
  const known: Record<string, string> = {
    "linear: unauthorized": "Linear no longer accepts the key. Paste a new one.",
    "linear: no such team": "Linear does not know the chosen team. Pick one again.",
    "linear: no completed state": "The Linear team has no “done” state, so issues cannot be closed.",
    "linear: network error": "The hub could not reach Linear the last time it tried.",
    "linear: timeout": "Linear did not answer the hub in time the last time it tried.",
    "linear: http error": "Linear answered the hub with an error the last time it tried.",
    "linear: graphql error": "Linear refused the hub’s last request.",
    "digest: webhook post failed": "Slack did not accept the last digest. Check the webhook, or paste a new one.",
    "digest: webhook cannot be opened": "The hub can no longer read the stored webhook. Paste it again.",
  };
  return known[cls] ?? `The hub reported: ${cls}.`;
}

/**
 * Plain words for a refused create or admin action. The daemon's errors are
 * lola's own fixed texts; the known ones get a sentence a person can act on.
 */
export function teamErrorText(raw: string): string {
  const s = raw.replace(/^Error:\s*/, "");
  const rules: [RegExp, string][] = [
    [/did not answer like a lola hub/i, "That address did not answer like a Lola hub. Check the address."],
    [/needs a create code/i, "This hub needs a create code to create a team. Ask your hub operator for one."],
    [/as many teams as it allows/i, "This hub holds as many teams as it allows. Ask your hub operator for a create code."],
    [/accepted the create code/i, "The hub created the team, but this Mac could not finish saving it. A create code is now used: ask your hub operator to check the team."],
    [/already holds a team admin key/i, "This Mac holds the admin key of another team. Creating a team here would discard that key."],
    [/already joined/i, "This Mac is already in a team. Leave it first."],
    [/not writable|read-only|permission denied/i, "Lola cannot write its settings folder, so nothing was sent."],
    [/Linear refused/i, "Linear did not accept that key. Nothing was changed."],
    [/cannot do this right now/i, "The hub cannot do this right now: it stores no secrets, or Linear did not answer. Nothing was changed."],
    [/no admin key/i, "This Mac does not hold the team’s admin key."],
    [/409|conflict/i, "That name is taken on this hub, or this Mac is already enrolled there. Pick another name."],
    [/403|forbidden/i, "The hub did not accept this Mac as a team admin. Its admin key may have been removed."],
    [/429|rate/i, "Too many requests. Wait a minute, then try again."],
    [/limit/i, "The team has too many unused invites. Wait for some to expire."],
    [/unauthori[sz]ed|401/i, "The hub did not accept this. A create code works once and expires: ask your hub operator for a new one."],
    [/not found|404/i, "This hub does not support teams yet, or the device is not in this team. Ask your hub operator to update the hub."],
    [/timed? ?out|deadline|context canceled/i, "The hub did not answer in time. Check your network, then try again."],
    [/no such host|connection refused|not reachable|dial|network is unreachable/i, "Lola could not reach the hub. Check your network."],
    [/certificate|x509|tls/i, "The hub’s HTTPS certificate is not trusted on this Mac."],
    [/unknown cmd/i, "Update the lola daemon to manage a team."],
    [/daemon|socket|lola\.sock/i, "The lola daemon is not answering."],
  ];
  for (const [re, text] of rules) if (re.test(s)) return text;
  return s;
}
