// What the Phones panel says about reaching this Mac away from home: the
// daemon's relay state and reason (`hubStatus.relay`, a closed vocabulary)
// turned into one sentence and the one thing a person can do about it. Every
// sentence is fixed here; nothing the hub sent is shown except through the
// daemon's sanitized lastError, which this file never reads.

import type { HubRelayInfo, HubStatusData } from "@bindings/internal/protocol";

/** What the panel offers next to the sentence. */
export type RelayAction = "" | "join";

export type RelaySummary = {
  /** Short status, shown first ("Available", "Needs a team hub"). */
  title: string;
  /** One sentence on what it means or what to do. */
  detail: string;
  action: RelayAction;
  /** The state is good (connected) rather than a problem or a wait. */
  ok: boolean;
};

const S = (title: string, detail: string, action: RelayAction = "", ok = false): RelaySummary => ({
  title,
  detail,
  action,
  ok,
});

/**
 * summarizeRelay maps the hub status to the panel's relay summary. `status` is
 * null before the first read (or on a daemon without hub commands).
 */
export function summarizeRelay(status: HubStatusData | null | undefined): RelaySummary {
  if (!status) return S("Unknown", "Lola has not reported the relay yet.");
  const r: HubRelayInfo | null | undefined = status.relay;
  if (!r) return S("Off", "Turn on the connection above to let phones reach this Mac.");
  switch (r.state) {
    case "connected": {
      const n = r.activeConns ?? 0;
      const via = n === 1 ? "1 phone is connected through the team hub." : `${n} phones are connected through the team hub.`;
      return S("Available", n > 0 ? via : "Paired phones can reach this Mac away from home.", "", true);
    }
    case "syncing":
    case "connecting":
      return S("Connecting", "Lola is connecting to your team hub.");
    case "unsupported":
      return r.reason === "version"
        ? S("Team hub needs an update", "Your team hub runs a different relay version. Update Lola or ask your hub admin to update the hub.")
        : S("Team hub needs an update", "Your team hub does not have the relay yet. Ask your hub admin to update it, then sync the team hub.");
    case "refused":
      return S("Refused", "Your team hub no longer accepts this Mac. Join the team again.", "join");
    case "backoff":
      return r.reason === "rate_limited"
        ? S("Retrying", "Your team hub asked Lola to slow down. Lola tries again shortly.")
        : S("Retrying", "Lola cannot reach your team hub's relay right now. It keeps trying.");
  }
  // off, and any state a newer daemon adds.
  switch (r.reason) {
    case "hub_off":
      // The toggle lives in the Team hub tab, so the same button applies.
      return S("Team hub is off", "The team hub is turned off on this Mac. Turn it on in the Team hub settings.", "join");
    case "not_joined":
      return S("Needs a team hub", "Away-from-home access goes through a team hub. Join a team hub to use it.", "join");
    case "revoked":
      return S("Refused", "Your team hub no longer accepts this Mac. Join the team again.", "join");
    case "no_phones":
      return S("No phone paired yet", "Pair a phone above. It can then reach this Mac away from home.");
    case "disabled":
      return S("Turned off", "Turn on the option above and save to let phones use the team hub.");
    case "config_error":
      return S("Settings error", "The config.toml on this Mac has an error. Fix it, then reload Lola.");
    case "remote_off":
      return S("Off", "Phone access is not running on this Mac.");
  }
  return S("Off", "Phones reach this Mac on the local network only.");
}
