// The hub INVITE, as an admin hands it to a new member out of band:
//
//   lola-hub:<url>#<token>[#<key_id>:<public key>]
//
// A mirror of internal/hub/invite.go's ParseInvite, so the join form can say
// what is wrong before anything is sent and show the policy key it carries.
// The daemon parses the invite again and is the real gate.
//
// Rules this module keeps:
//   - The whole invite is a secret (it holds the enroll token). Every error is
//     a fixed sentence that echoes no part of the input.
//   - The policy key in an invite is only DISPLAYED here. It is pinned only
//     after the person ticks "Trust this key" (ConfigService.PinPolicyKey),
//     and never from a hub response.

export const INVITE_PREFIX = "lola-hub:";
/** internal/hub/invite.go maxInvite. */
export const MAX_INVITE = 4 << 10;

/** hubproto's key-id shape. */
export const KEY_ID_RE = /^[A-Za-z0-9_.-]{1,64}$/;
/** An ed25519 public key in base64url without padding: 32 bytes, 43 characters. */
export const PUBLIC_KEY_RE = /^[A-Za-z0-9_-]{42}[AEIMQUYcgkosw048]$/;

export type Invite = {
  /** "" for a bare token. */
  url: string;
  token: string;
  /** Both set or both "". */
  keyId: string;
  publicKey: string;
};

export type InviteResult = { ok: true; invite: Invite } | { ok: false; error: string };

/**
 * The static rule for a hub URL (config.ValidateHubURL): https on any host,
 * plain http only on a loopback host; no credentials, path, query or
 * fragment. Returns "" when fine, else a fixed sentence.
 */
export function hubURLError(raw: string): string {
  if (!raw) return "Enter the hub’s URL.";
  let u: URL;
  try {
    u = new URL(raw);
  } catch {
    return "Enter a full URL, like https://hub.example.com.";
  }
  if (u.username || u.password || u.search || u.hash || raw.includes("?") || raw.includes("#")) {
    return "The hub URL must not carry a login, a query or a fragment.";
  }
  if (u.pathname !== "/" && u.pathname !== "") return "Enter the hub’s base URL, without a path.";
  if (!u.hostname) return "Enter a full URL, like https://hub.example.com.";
  if (u.protocol === "https:") return "";
  if (u.protocol === "http:" && ["localhost", "127.0.0.1", "[::1]"].includes(u.hostname)) return "";
  return "Use https (plain http only for localhost).";
}

/** Parses an invite or a bare token. Errors never echo the input. */
export function parseInvite(input: string): InviteResult {
  const s = input.trim();
  const fail = (error: string): InviteResult => ({ ok: false, error });
  if (!s) return fail("Paste the invite from your hub admin.");
  if (s.length > MAX_INVITE) return fail("This is too long to be an invite.");
  if (/[ \t\r\n]/.test(s)) return fail("An invite is one line with no spaces. Copy it again in one piece.");
  if (!s.startsWith(INVITE_PREFIX)) {
    if (s.includes("#")) return fail("This is not a lola-hub: invite or a bare token.");
    return { ok: true, invite: { url: "", token: s, keyId: "", publicKey: "" } };
  }
  const parts = s.slice(INVITE_PREFIX.length).split("#");
  if (parts.length < 2 || parts.length > 3) return fail("This invite is incomplete. Copy it again in one piece.");
  const url = parts[0].replace(/\/$/, "");
  if (hubURLError(url)) return fail("The invite’s hub URL is not valid. Ask your hub admin for a new invite.");
  const token = parts[1];
  if (!token) return fail("The invite carries no token. Ask your hub admin for a new invite.");
  let keyId = "";
  let publicKey = "";
  if (parts.length === 3) {
    const at = parts[2].indexOf(":");
    const id = at < 0 ? "" : parts[2].slice(0, at);
    const pub = at < 0 ? "" : parts[2].slice(at + 1);
    if (!KEY_ID_RE.test(id)) return fail("The invite’s policy key id is malformed.");
    if (!PUBLIC_KEY_RE.test(pub)) return fail("The invite’s policy key is not a valid public key.");
    keyId = id;
    publicKey = pub;
  }
  return { ok: true, invite: { url, token, keyId, publicKey } };
}

/**
 * Plain words for a refused join. The daemon's errors are lola's own fixed
 * texts; the known ones get a sentence a person can act on, anything else is
 * shown as the daemon said it (without Go's "Error:" prefix).
 */
export function joinErrorText(raw: string): string {
  const s = raw.replace(/^Error:\s*/, "");
  const rules: [RegExp, string][] = [
    // Order matters: the "accepted, then failed" and "not sent" cases name the
    // token too, and must not read as a refused invite.
    [/accepted the one-time token/i, "The hub accepted the invite, but this Mac could not finish saving the membership. The invite is now used: if Team hub does not show as joined, ask your hub admin for a new one."],
    [/not writable|read-only|permission denied/i, "Lola cannot write its settings folder, so it did not send the invite. Nothing was used up."],
    [/token.*(rejected|used|expired|unknown|invalid)|unauthori[sz]ed|401/i, "The hub did not accept this invite. It may be used up or expired: ask your hub admin for a new one."],
    [/another hub url/i, "The invite is for a different hub than the URL you entered."],
    [/timed? ?out|deadline|context canceled/i, "The hub did not answer in time. Check the URL and your network, then try again."],
    [/no such host|connection refused|not reachable|dial|network is unreachable/i, "Lola could not reach the hub. Check the URL and your network."],
    [/certificate|x509|tls/i, "The hub’s HTTPS certificate is not trusted on this Mac."],
    [/already joined/i, "This Mac is already in a hub. Leave it first."],
    [/daemon|socket|lola\.sock/i, "The lola daemon is not answering, so joining is unavailable."],
  ];
  for (const [re, text] of rules) if (re.test(s)) return text;
  return s;
}
