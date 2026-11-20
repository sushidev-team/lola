// The variables a project's worktree fields can reference, as data for
// VariableMenu. Two spellings, because two different things read them:
//
//   - COMMANDS (post_create, pre_remove, dev_commands) run in a shell, so they
//     read lola's per-session environment: $LOLA_PORT, $LOLA_DB_NAME, …
//     (internal/runtime/projectenv.go, withSessionEnv) plus the project's own
//     [project.env] keys.
//   - ENV VALUES are rendered by lola before any shell sees them, with the same
//     port names and {{.Field}} placeholders for session metadata.
//
// Keep both lists in step with projectenv.go and the README's "Per-session env
// values" table.

export type Variable = { label: string; token: string };
export type VariableGroup = { title: string; note?: string; items: Variable[] };

export type PortLease = { range: string; perSession: number };

const NO_RANGE = "Empty until the project has a port range.";

/** How many ports one session leases: 0 without a range, else at least 1. */
function leased(lease: PortLease): number {
  return lease.range.trim() ? Math.max(1, Math.floor(lease.perSession) || 1) : 0;
}

function portItems(lease: PortLease): Variable[] {
  const n = leased(lease);
  const items = [{ label: "Port", token: "$LOLA_PORT" }];
  // $LOLA_PORT_1 is $LOLA_PORT; the numbered ones only earn a row past one port.
  for (let i = 2; i <= n; i++) items.push({ label: `Port ${i}`, token: `$LOLA_PORT_${i}` });
  return items;
}

/** The names a [project.env] list defines, in order, without duplicates. */
export function envKeys(env: string[] | null | undefined): string[] {
  const seen = new Set<string>();
  for (const line of env ?? []) {
    const m = /^\s*([A-Za-z_][A-Za-z0-9_]*)\s*=/.exec(line);
    if (m && !m[1].startsWith("LOLA_")) seen.add(m[1]);
  }
  return [...seen];
}

/** Variables a shell command field can use. */
export function commandVariables(lease: PortLease, env: string[] | null | undefined): VariableGroup[] {
  const groups: VariableGroup[] = [
    {
      title: "Session",
      items: [
        { label: "Session id", token: "$LOLA_SESSION_SLUG" },
        { label: "Database name", token: "$LOLA_DB_NAME" },
      ],
    },
    {
      title: "Ports",
      note: leased(lease) ? undefined : NO_RANGE,
      items: portItems(lease),
    },
  ];
  const keys = envKeys(env);
  if (keys.length) groups.push({ title: "Project env", items: keys.map((k) => ({ label: k, token: `$${k}` })) });
  return groups;
}

/** Placeholders an env VALUE can use. */
export function envValueVariables(lease: PortLease): VariableGroup[] {
  return [
    {
      title: "Session",
      items: [
        { label: "Session id", token: "{{.Session}}" },
        { label: "Database name", token: "{{.DBName}}" },
        { label: "Issue", token: "{{.Issue}}" },
        { label: "Branch", token: "{{.Branch}}" },
        { label: "Project", token: "{{.Project}}" },
        { label: "Worktree path", token: "{{.Worktree}}" },
      ],
    },
    {
      title: "Ports",
      note: leased(lease) ? undefined : NO_RANGE,
      items: portItems(lease),
    },
  ];
}

/**
 * Put `token` where the caret is (replacing a selection) and return the new
 * value. A field the human never focused has no caret worth trusting, so the
 * token goes at the end.
 */
export function insertVariable(el: HTMLTextAreaElement | HTMLInputElement, token: string, caretKnown: boolean): string {
  const end = el.value.length;
  const from = caretKnown ? (el.selectionStart ?? end) : end;
  const to = caretKnown ? (el.selectionEnd ?? end) : end;
  el.setRangeText(token, from, to, "end");
  return el.value;
}
