// One sentence for every refusal the phone can show.
//
// The wire speaks in codes (`unknown_cmd`, `denied`) and the shim speaks in class names; neither is
// something to show a person. `refusalText` turns any rejection into the sentence the app says:
// the daemon's own message where it wrote one, our copy where it did not. It never returns an
// error's name or a bare code.

import { copy } from "@mobile/lib/copy";
import { ConnectionClosedError, WireRefusalError } from "@mobile/wire/correlator";
import { ShimNotConnectedError, UnsupportedOnMobileError } from "@mobile/wailsshim/errors";

/** The daemon's message part of a refusal: the text after the `code: ` prefix, "" when there is none. */
function daemonMessage(e: WireRefusalError): string {
  const prefix = `${e.code}: `;
  return e.message.startsWith(prefix) ? e.message.slice(prefix.length).trim() : "";
}

export function refusalText(err: unknown): string {
  if (err instanceof WireRefusalError) {
    const msg = daemonMessage(err);
    switch (err.code) {
      // An old daemon does not know a command a new phone sends, whatever it says about it.
      case "unknown_cmd":
        return copy.refusals.update;
      case "denied":
        return msg || copy.notices.noTier.body;
      case "rate_limited":
        return copy.refusals.rateLimited;
      default:
        return msg || copy.refusals.generic;
    }
  }
  if (err instanceof ConnectionClosedError || err instanceof ShimNotConnectedError) {
    return copy.refusals.offline;
  }
  if (err instanceof UnsupportedOnMobileError) return copy.notices.macOnly.body;
  if (err instanceof Error && err.message) return err.message;
  return copy.refusals.generic;
}
