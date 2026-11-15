// Whether this client is ever sent widget pages. The phone is not (the daemon drops a widget's
// html and data on the way out), so it sets this at boot and SSHWidget shows a placeholder and
// runs nothing. The desktop never sets it.

let off = false;

export function setWidgetsUnavailable(v: boolean): void {
  off = v;
}

export function widgetsUnavailable(): boolean {
  return off;
}
