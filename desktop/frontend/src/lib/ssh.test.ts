import { describe, it, expect } from "vitest";
import {
  agentName,
  allowedLine,
  blockView,
  canGrant,
  chipFact,
  chipFor,
  composerLine,
  connView,
  connectsLine,
  defaultStream,
  deliveryLine,
  deriveBrief,
  dockCards,
  elementRing,
  findOffsets,
  foldRows,
  formatBytes,
  formatMs,
  grantOptions,
  hostRows,
  hypothesisView,
  identity,
  initialHost,
  itemMatches,
  linkify,
  matchesFilter,
  maybeAnsweredInPane,
  mergeItems,
  mmss,
  nextApprovalTarget,
  nowRow,
  outputNotice,
  planBar,
  previewLine,
  railKinds,
  severityView,
  stepView,
  tabCount,
  tierOf,
  tierWord,
  tokenize,
  urgentConn,
  SSH_REMOTE_OUTLIVES,
  droveFor,
  expiryWords,
  sessionKey,
  transcriptLabel,
  type SSHBlock,
  type SSHConnInfo,
  type SSHTimelineItem,
} from "./ssh";

const conn = (o: Partial<SSHConnInfo> = {}) =>
  ({ id: "c1", session: "s", host: "prod-api", alias: "prod-api", tier: "production", entry: "direct", state: "connected", wheel: "agent", ...o }) as SSHConnInfo;
const block = (o: Partial<SSHBlock> = {}) =>
  ({ id: "b1", conn: "c1", session: "s", host: "prod-api", tier: "production", command: "ls", actor: "agent", status: "succeeded", approval: { granted: true }, exitCode: 0, durationMs: 300, ...o }) as SSHBlock;
const blockItem = (o: Partial<SSHBlock> = {}, order = 1): SSHTimelineItem =>
  ({ id: `i${order}`, kind: "block", order, seq: order, at: "", block: block({ id: `b${order}`, ...o }) }) as SSHTimelineItem;
const element = (kind: string, o: Record<string, unknown> = {}, order = 1): SSHTimelineItem =>
  ({ id: `e${order}`, kind: "element", order, seq: order, at: "", element: { id: `e${order}`, kind, revision: 1, ...o } }) as SSHTimelineItem;
const system = (kind: string, order: number, at = "", conn = "c1"): SSHTimelineItem =>
  ({ id: `s${order}`, kind: "system", order, seq: order, at, system: { kind, text: kind, conn } }) as SSHTimelineItem;

describe("tier", () => {
  it("reads unset and unknown as production", () => {
    expect(tierOf(undefined)).toBe("production");
    expect(tierOf("")).toBe("production");
    expect(tierOf("nonsense")).toBe("production");
    expect(tierWord("nonsense")).toBe("PRODUCTION");
    expect(tierWord("dev")).toBe("DEV");
  });
});

describe("names", () => {
  it("names the agents", () => {
    expect(agentName("codex")).toBe("Codex");
    expect(agentName("opencode")).toBe("OpenCode");
    expect(agentName("")).toBe("Claude");
  });
  it("prefers the resolved identity, then config, then the alias", () => {
    expect(identity(conn({ resolvedUser: "deploy", resolvedHostname: "10.0.1.4" }))).toBe("deploy@10.0.1.4");
    expect(identity(conn({ user: "ops" }))).toBe("ops@prod-api");
    expect(identity(conn())).toBe("prod-api");
  });
  it("says so when ssh config could not be read", () => {
    expect(connectsLine(conn()).known).toBe(false);
    expect(connectsLine(conn({ resolvedHostname: "h" })).text).toContain("(alias prod-api)");
  });
});

describe("numbers", () => {
  it("formats bytes, durations and a clock", () => {
    expect(formatBytes(0)).toBe("0 B");
    expect(formatBytes(2048)).toBe("2 KB");
    expect(formatBytes(3.1 * 1024 * 1024)).toBe("3.1 MB");
    expect(formatMs(400)).toBe("0.4s");
    expect(formatMs(72_000)).toBe("1m 12s");
    expect(mmss(252_000)).toBe("4:12");
    expect(mmss(-5)).toBe("0:00");
  });
});

describe("the chip", () => {
  it("follows the priority table", () => {
    expect(chipFact(conn({ state: "authenticating", pendingApprovals: 2 }))?.words).toBe("sign in");
    expect(chipFact(conn({ pendingApprovals: 2 }))?.words).toBe("2 to approve");
    expect(chipFact(conn({ state: "requested" }))?.words).toBe("wants to connect");
    expect(chipFact(conn({ wheel: "human" }))?.words).toBe("you're driving");
    expect(chipFact(conn({ state: "connecting" }))?.words).toBe("connecting…");
    expect(chipFact(conn({ state: "disconnected" }))?.words).toBe("disconnected");
    expect(chipFact(conn({ state: "failed" }))?.words).toBe("could not connect");
    expect(chipFact(conn({ runningBlocks: 1 }))?.words).toBe("running");
    expect(chipFact(conn())?.words).toBe("connected");
  });
  it("shows a live grant's clock", () => {
    const now = Date.parse("2026-01-01T00:00:00Z");
    const c = conn({ grant: { id: "g", expiresAt: "2026-01-01T00:02:30Z" } as never });
    expect(chipFact(c, now)?.words).toBe("grant 2:30");
  });
  it("draws no chip for an ended connection", () => {
    expect(chipFact(conn({ state: "closed" }))).toBeNull();
    expect(chipFact(conn({ state: "denied" }))).toBeNull();
    expect(chipFor({ connections: [conn({ state: "closed" })], lastSeq: 0 })).toBeNull();
  });
  it("picks the most urgent connection and counts the rest", () => {
    const chip = chipFor({ connections: [conn({ id: "a" }), conn({ id: "b", pendingApprovals: 1 })], lastSeq: 0 });
    expect(chip?.conn.id).toBe("b");
    expect(chip?.more).toBe(1);
  });
});

describe("urgency", () => {
  it("is authentication, approval, go", () => {
    const go = conn({ id: "go", state: "requested" });
    const ap = conn({ id: "ap", pendingApprovals: 1 });
    const au = conn({ id: "au", state: "authenticating" });
    expect(urgentConn([go, ap, au])?.id).toBe("au");
    expect(urgentConn([go, ap])?.id).toBe("ap");
    expect(urgentConn([go, conn({ id: "x" })])?.id).toBe("go");
  });
  it("counts approvals plus connections awaiting a go", () => {
    expect(tabCount([conn({ pendingApprovals: 2 }), conn({ state: "requested" }), conn()])).toBe(3);
  });
  it("goes to the oldest approval across sessions", () => {
    const old = { id: "b1", conn: "c1", createdAt: "2026-01-01T00:00:00Z" } as SSHBlock;
    const young = { id: "b9", conn: "c2", createdAt: "2026-01-02T00:00:00Z" } as SSHBlock;
    const t = nextApprovalTarget([
      { id: "y", ssh: { connections: [], nextApproval: young, lastSeq: 0 } },
      { id: "o", ssh: { connections: [], nextApproval: old, lastSeq: 0 } },
    ]);
    expect(t).toEqual({ at: expect.any(Number), session: "o", conn: "c1", block: "b1" });
    expect(nextApprovalTarget([{ id: "n" }])).toBeUndefined();
  });
});

describe("connection state", () => {
  it("covers every designed state in words", () => {
    expect(connView(conn({ state: "requested" })).word).toBe("Waiting for your go");
    expect(connView(conn({ state: "denied" })).word).toBe("Denied");
    expect(connView(conn({ state: "connecting" })).pulse).toBe(true);
    expect(connView(conn({ state: "authenticating" })).word).toBe("Signing in");
    expect(connView(conn({ state: "failed" })).word).toBe("Could not connect");
    expect(connView(conn({ state: "closed" })).word).toBe("Closed");
    expect(connView(conn({ state: "disconnected" })).word).toBe("Disconnected");
    expect(connView(conn({ state: "disconnected", stateDetail: "Closed after idle timeout" })).word).toBe("Idle, connection closed");
  });
  it("never says connected about a state it does not know", () => {
    expect(connView(conn({ state: "mystery" })).word).toBe("mystery");
  });
  it("only connecting moves", () => {
    for (const s of ["requested", "denied", "authenticating", "connected", "disconnected", "failed", "closed"]) {
      expect(connView(conn({ state: s })).pulse).toBe(false);
    }
  });
});

describe("block status table", () => {
  const cases: [string, string, string][] = [
    ["pending_approval", "blocked", "Waiting for you"],
    ["queued", "pending", "Queued"],
    ["denied", "refused", "Denied"],
    ["expired", "refused", "Expired"],
    ["refused", "refused", "Did not run"],
  ];
  it.each(cases)("%s", (status, marker, result) => {
    const v = blockView(block({ status, exitCode: undefined }));
    expect(v.marker).toBe(marker);
    expect(v.result).toBe(result);
  });
  it("calls a non-zero exit an exit, never a failure", () => {
    const v = blockView(block({ status: "failed", exitCode: 1, durationMs: 400 }));
    expect(v.marker).toBe("fail");
    expect(v.result).toBe("exit 1 · 0.4s");
    expect(v.resultCls).toBe("text-bad");
    expect(`${v.result} ${v.detail}`.toLowerCase()).not.toContain("failed");
  });
  it("marks a cut-off command apart from one that answered badly", () => {
    expect(blockView(block({ status: "timed_out" })).markerCls).toBe("text-warn");
    expect(blockView(block({ status: "stopped", startedAt: "2026-01-01T00:00:00Z" })).detail).toContain(SSH_REMOTE_OUTLIVES);
    const lost = blockView(block({ status: "lost", exitCode: undefined }));
    expect(lost.marker).toBe("unknown");
    expect(lost.result).toContain("Result unknown");
  });
  it("renders an unknown status in words, never a false pass", () => {
    const v = blockView(block({ status: "brand_new_state" }));
    expect(v.marker).not.toBe("pass");
    expect(v.result).toBe("brand_new_state");
  });
  it("opens stderr only when a failed block has some", () => {
    expect(defaultStream(block({ status: "failed", stderrBytes: 9 }))).toBe("stderr");
    expect(defaultStream(block({ status: "failed", stderrBytes: 0 }))).toBe("stdout");
    expect(defaultStream(block({ status: "succeeded", stderrBytes: 9 }))).toBe("stdout");
  });
  it("shows the last non-empty preview line", () => {
    expect(previewLine(block({ preview: "a\nb\n\n" }))).toBe("b");
  });
  it("says how a block was allowed", () => {
    expect(allowedLine(block({ actor: "human" }))).toBe("You ran this");
    expect(allowedLine(block({ approval: { mode: "grant", granted: true } }))).toContain("Grant");
    expect(allowedLine(block({ status: "pending_approval", approval: { granted: false } }))).toBe("Waiting for you");
  });
  it("words each empty output", () => {
    const ended = block();
    expect(outputNotice(ended, 0, 0)).toBe("No output.");
    expect(outputNotice(block({ status: "running" }), 0, 0)).toBe("No output yet.");
    expect(outputNotice(block({ status: "denied" }), 0, 0)).toContain("did not run");
    expect(outputNotice(block({ outputPruned: true }), 0, 0)).toContain("storage limit");
    expect(outputNotice(block({ droppedBytes: 2048 }), 0, 0)).toContain("2 KB were lost");
    expect(outputNotice(ended, 10, 10)).toBe("");
  });
});

describe("tokenizing untrusted text", () => {
  it("links only http(s) URLs", () => {
    const parts = linkify("see https://a.example/x. and javascript:alert(1) file:///etc/passwd");
    expect(parts.filter((p) => p.url).map((p) => p.text)).toEqual(["https://a.example/x"]);
  });
  it("links a bare block id only when the block is real", () => {
    const known = (id: string) => id === "b12";
    const t = tokenize("see b12 and b99 at https://a.example", known);
    expect(t.filter((x) => x.kind === "block").map((x) => x.text)).toEqual(["b12"]);
    expect(t.filter((x) => x.kind === "url").map((x) => x.text)).toEqual(["https://a.example"]);
    expect(t.map((x) => x.text).join("")).toBe("see b12 and b99 at https://a.example");
  });
  it("finds in loaded text, case-insensitively", () => {
    expect(findOffsets("Error error ERR", "err")).toEqual([0, 6, 12]);
    expect(findOffsets("x", "")).toEqual([]);
  });
});

describe("filters", () => {
  const claim = element("finding", {}, 2);
  it("Facts drops every claim", () => {
    expect(matchesFilter(blockItem(), "facts")).toBe(true);
    expect(matchesFilter(system("connected", 3), "facts")).toBe(true);
    expect(matchesFilter(claim, "facts")).toBe(false);
    expect(matchesFilter({ id: "h", kind: "human", order: 1, seq: 1, at: "", human: { text: "hi", delivered: false } }, "facts")).toBe(false);
  });
  it("Findings is the conclusion view", () => {
    for (const k of ["finding", "hypothesis", "evidence", "recommendation", "proposal"]) expect(matchesFilter(element(k), "findings")).toBe(true);
    for (const k of ["narration", "summary", "plan", "metric", "question"]) expect(matchesFilter(element(k), "findings")).toBe(false);
    expect(matchesFilter(blockItem(), "findings")).toBe(false);
  });
  it("find covers command, why, element text and human text, and never output", () => {
    expect(itemMatches(blockItem({ command: "df -h", why: "disk" }), "disk")).toBe(true);
    expect(itemMatches(blockItem({ preview: "secret-in-output" }), "secret")).toBe(false);
    expect(itemMatches(element("finding", { title: "Log rotation" }), "ROTATION")).toBe(true);
  });
});

describe("merge", () => {
  it("upserts by id and sorts by the fixed order", () => {
    const a = blockItem({ status: "running" }, 2);
    const b = blockItem({}, 1);
    const updated = { ...a, block: block({ id: "b2", status: "failed" }) };
    const merged = mergeItems([a, b], [updated]);
    expect(merged.map((i) => i.id)).toEqual(["i1", "i2"]);
    expect(merged[1].block?.status).toBe("failed");
  });
});

describe("folding", () => {
  const ok = (n: number) => blockItem({}, n);
  it("folds three or more plain successes", () => {
    const rows = foldRows([ok(1), ok(2), ok(3)], "everything");
    expect(rows).toHaveLength(1);
    expect(rows[0].kind).toBe("fold");
  });
  it("leaves two alone", () => {
    expect(foldRows([ok(1), ok(2)], "everything").every((r) => r.kind === "item")).toBe(true);
  });
  it("breaks on anything between, and never folds what must stay visible", () => {
    expect(foldRows([ok(1), ok(2), element("narration", {}, 3), ok(4)], "everything").some((r) => r.kind === "fold")).toBe(false);
    const failed = blockItem({ status: "failed", exitCode: 1 }, 3);
    expect(foldRows([ok(1), ok(2), failed], "everything").some((r) => r.kind === "fold")).toBe(false);
    const human = blockItem({ actor: "human" }, 3);
    expect(foldRows([ok(1), ok(2), human], "everything").some((r) => r.kind === "fold")).toBe(false);
  });
  it("never folds a block an element's evidence points at", () => {
    const ev = element("finding", { evidence: [{ block: "b2", verified: true }] }, 9);
    const rows = foldRows([ok(1), ok(2), ok(3)], "everything", new Set(), new Set(["b2"]));
    expect(rows.some((r) => r.kind === "fold")).toBe(false);
    expect(ev.element?.evidence?.[0].block).toBe("b2");
  });
  it("folds nothing in the audit view", () => {
    expect(foldRows([ok(1), ok(2), ok(3)], "facts").every((r) => r.kind === "item")).toBe(true);
  });
  it("opens a fold it was told to", () => {
    const rows = foldRows([ok(1), ok(2), ok(3)], "everything", new Set(["fold-i1"]));
    expect(rows.every((r) => r.kind === "item")).toBe(true);
  });
});

describe("the rail", () => {
  it("records connected, driving and down from system events", () => {
    const items = [system("requested", 1), system("connected", 2), blockItem({}, 3), system("wheel_taken", 4), system("handed_back", 5), system("disconnected", 6)];
    expect(railKinds(items, conn())).toEqual(["down", "agent", "agent", "human", "agent", "down"]);
  });
  it("covers a session that started with the human driving", () => {
    expect(railKinds([system("connected", 1)], conn({ wheel: "human" }))).toEqual(["human"]);
  });
});

describe("the Brief", () => {
  const items = [
    element("summary", { title: "first", createdAt: "2026-01-01T00:00:00Z" }, 1),
    element("summary", { title: "latest", createdAt: "2026-01-01T00:30:00Z" }, 2),
    element("plan", { title: "p", steps: [{ text: "a", state: "done" }, { text: "b", state: "active" }, { text: "c", state: "pending" }] }, 3),
    element("hypothesis", { state: "confirmed" }, 4),
    element("hypothesis", { state: "open" }, 5),
    element("hypothesis", { state: "refuted" }, 6),
    element("finding", { severity: "medium" }, 7),
    element("finding", { severity: "critical" }, 8),
  ];
  it("is derived from the items, worst findings first", () => {
    const now = Date.parse("2026-01-01T00:40:00Z");
    const b = deriveBrief(items, now);
    expect(b.summary?.title).toBe("latest");
    expect(b.summary?.stale).toBe(false);
    expect(b.plan).toMatchObject({ done: 1, total: 3, current: "b" });
    expect(b.theories).toEqual({ confirmed: 1, open: 1, refuted: 1 });
    expect(b.findings.map((f) => f.severity)).toEqual(["critical", "medium"]);
  });
  it("marks an old summary stale", () => {
    expect(deriveBrief(items, Date.parse("2026-01-01T01:00:00Z")).summary?.stale).toBe(true);
  });
  it("draws the bar in ▰▱", () => {
    expect(planBar(2, 5)).toBe("▰▰▱▱▱");
  });
});

describe("the Now row", () => {
  const agent = { agentState: "working", agent: "claude" };
  it("puts approval first, then what runs", () => {
    expect(nowRow([blockItem({ status: "pending_approval" }, 1)], agent).text).toBe("Waiting for you to approve 1 command");
    expect(nowRow([blockItem({ status: "running", command: "df" }, 1)], agent)).toMatchObject({ text: "Running", command: "df", action: "stop" });
    expect(nowRow([blockItem({ status: "running" }, 1), blockItem({ status: "queued" }, 2)], agent).text).toBe("Running 1, 1 queued");
  });
  it("falls back to the agent's axis", () => {
    expect(nowRow([], agent).text).toBe("Claude is working");
    expect(nowRow([], { agentState: "idle" }).text).toBe("Claude is waiting");
    expect(nowRow([], { agentState: "waiting_input", inputReason: "idle_notification" }).action).toBeUndefined();
    expect(nowRow([], { agentState: "waiting_input", inputReason: "permission_prompt" }).action).toBe("show");
    expect(nowRow([], { agentState: "dead" }).action).toBe("revive");
  });
});

describe("the composer's state line", () => {
  it("tells the truth about what happens to a message", () => {
    expect(composerLine({ agentState: "working" }, "agent").line).toContain("next command");
    expect(composerLine({ agentState: "idle" }, "agent").placeholder).toBe("Tell Claude what to do…");
    expect(composerLine({ agentState: "waiting_input", inputReason: "dialog" }, "agent").action).toBe("show");
    expect(composerLine({ agentState: "waiting_input", inputReason: "idle_notification" }, "agent").action).toBeUndefined();
    expect(composerLine({ agentState: "dead" }, "agent").action).toBe("revive");
    expect(composerLine({ agentState: "starting" }, "agent").line).toBe("Claude is starting.");
  });
  it("is replaced while you drive", () => {
    const l = composerLine({ agentState: "working", agent: "codex" }, "human");
    expect(l.driving).toBe(true);
    expect(l.placeholder).toBe("Ask Codex to carry on…");
  });
  it("says Delivered, never read or seen", () => {
    const d = deliveryLine({ text: "x", delivered: true, via: "reply" }, "claude", false);
    expect(d.text).toContain("Delivered with its last command");
    expect(deliveryLine({ text: "x", delivered: true, via: "prompt" }, "claude", false).text).toContain("Delivered at its prompt");
    expect(`${d.text}`.toLowerCase()).not.toMatch(/\b(read|seen)\b/);
    expect(deliveryLine({ text: "x", delivered: false }, "claude", false)).toEqual({ text: "Queued", canRecall: true });
    expect(deliveryLine({ text: "x", delivered: false }, "claude", true).text).toBe("Queued. Claude has exited.");
    expect(deliveryLine({ text: "x", delivered: false, recalled: true }, "claude", false).canRecall).toBe(false);
  });
  it("hints only after a minute of silence from an idle agent", () => {
    const msg = { id: "h", kind: "human", order: 1, seq: 1, at: "", human: { text: "x", delivered: true, deliveredAt: "2026-01-01T00:00:00Z" } } as SSHTimelineItem;
    const later = Date.parse("2026-01-01T00:02:00Z");
    expect(maybeAnsweredInPane([msg], msg, "idle", later)).toBe(true);
    expect(maybeAnsweredInPane([msg], msg, "working", later)).toBe(false);
    expect(maybeAnsweredInPane([msg], msg, "idle", Date.parse("2026-01-01T00:00:30Z"))).toBe(false);
    expect(maybeAnsweredInPane([msg, element("narration", {}, 2)], msg, "idle", later)).toBe(false);
  });
});

describe("the dock", () => {
  const pending = blockItem({ status: "pending_approval" }, 1);
  it("orders approvals, then a go, and leaves a sign-in to the stream", () => {
    const au = conn({ id: "au", state: "authenticating" });
    const go = conn({ id: "go", state: "requested" });
    const cards = dockCards([go, au, conn()], [pending], "c1");
    expect(cards.map((c) => c.kind)).toEqual(["approval", "go"]);
    const ap = cards[0];
    if (ap.kind === "approval") expect([ap.index, ap.of]).toEqual([1, 2]);
  });
  it("adds a drop or a failure only for the shown connection, and last", () => {
    const d = conn({ id: "d", state: "disconnected" });
    expect(dockCards([d], [], "d").map((c) => c.kind)).toEqual(["reconnect"]);
    expect(dockCards([d], [], "other")).toEqual([]);
    expect(dockCards([conn({ id: "f", state: "failed" })], [], "f").map((c) => c.kind)).toEqual(["failed"]);
  });
  it("never offers a grant on production, whatever the wire says", () => {
    expect(canGrant({ tier: "production", grantMaxMinutes: 120 })).toBe(false);
    expect(canGrant({ tier: "", grantMaxMinutes: 120 })).toBe(false);
    expect(canGrant({ tier: "staging", grantMaxMinutes: 30 })).toBe(true);
    expect(canGrant({ tier: "dev", grantMaxMinutes: 0 })).toBe(false);
    expect(grantOptions(30)).toEqual([5, 15, 30]);
    expect(grantOptions(120)).toEqual([5, 15, 30, 60, 120]);
  });
});

describe("voices", () => {
  it("keeps a confirmed theory a claim", () => {
    const v = hypothesisView("confirmed");
    expect(v.word).toBe("Agent says confirmed");
    expect(JSON.stringify(v)).not.toContain("good");
    expect(elementRing({ kind: "hypothesis", state: "confirmed" })).not.toContain("good");
    expect(hypothesisView("refuted").word).toBe("Agent says refuted");
  });
  it("colours a severity's ring and never fills it", () => {
    for (const s of ["critical", "high", "medium", "low", "info", "unknown"]) {
      const v = severityView(s);
      expect(v.ring).toContain("border");
      expect(v.ring).not.toMatch(/\bbg-/);
    }
  });
  it("draws plan steps with no green", () => {
    for (const s of ["pending", "active", "done", "skipped", "failed"]) expect(JSON.stringify(stepView(s))).not.toContain("good");
  });
});

describe("the new-session list", () => {
  const hosts = [
    { name: "billing", alias: "billing", tier: "production", grantMaxMinutes: 0, directRefused: "production refuses" },
    { name: "prod", alias: "prod", user: "deploy", tier: "production", grantMaxMinutes: 0, directSandbox: "docker" },
    { name: "open", alias: "open", tier: "production", grantMaxMinutes: 0, directSandbox: "none" },
    { name: "stage", alias: "stage", tier: "staging", grantMaxMinutes: 30, directSandbox: "none" },
  ];
  it("reads the daemon's own answers", () => {
    const rows = hostRows(hosts, []);
    expect(rows[0]).toMatchObject({ refused: true, note: "Cannot start" });
    expect(rows[1]).toMatchObject({ note: "Agent runs in a container", who: "deploy@prod" });
    expect(rows[2]).toMatchObject({ note: "Agent runs on this Mac", noteCls: "text-warn" });
    expect(rows[3]).toMatchObject({ note: "Agent runs on this Mac", noteCls: "text-faint" });
  });
  it("says when a host is already open", () => {
    const sessions = [{ id: "x", title: "Debug", location: "ssh", ssh: { connections: [conn({ host: "prod" })], lastSeq: 0 } }];
    expect(hostRows(hosts, sessions)[1]).toMatchObject({ note: "Open in Debug · in a container", openIn: { id: "x", title: "Debug" } });
  });
  it("keeps the on-this-Mac warning when a production host is already open", () => {
    const here = hosts.findIndex((h) => h.directSandbox !== "docker" && !h.directRefused && (h.tier === "production" || !h.tier));
    const sessions = [{ id: "x", title: "Debug", location: "ssh", ssh: { connections: [conn({ host: hosts[here].name })], lastSeq: 0 } }];
    expect(hostRows(hosts, sessions)[here]).toMatchObject({ note: "Open in Debug · on this Mac", noteCls: "text-warn" });
  });
  it("selects the first usable host unless one was named", () => {
    const rows = hostRows(hosts, []);
    expect(initialHost(rows)).toBe("prod");
    expect(initialHost(rows, "stage")).toBe("stage");
    expect(initialHost(rows, "billing")).toBe("prod");
  });
});

describe("review regressions", () => {
  it("droveFor is in the past tense", () => {
    const hb = system("handed_back", 2, "2026-01-01T00:12:00Z");
    const line = droveFor([system("wheel_taken", 1, "2026-01-01T00:00:00Z"), hb], hb);
    expect(line).toBe("You drove for 12m. Anything inside a nested shell, or a shell lola could not read, is not in this record.");
    expect(droveFor([hb], hb)).toContain("not in this record");
  });
  it("an approval's expiry is still until its last five minutes", () => {
    expect(expiryWords(29 * 60 - 5)).toBe("29 min");
    expect(expiryWords(300)).toBe("5 min");
    expect(expiryWords(299)).toBe("4m 59s");
    expect(expiryWords(-1)).toBe("0s");
    const left = new Date(Date.now() + 28 * 60_000 + 55_000).toISOString();
    expect(blockView(block({ status: "pending_approval", approvalExpiresAt: left })).detail).toBe("");
  });
  it("a warning plate tints the panel instead of replacing it", () => {
    for (const status of ["failed", "timed_out", "stopped", "lost"]) {
      const { plate } = blockView(block({ status, exitCode: status === "failed" ? 1 : undefined }));
      expect(plate).toContain("var(--color-panel)");
      expect(plate).not.toMatch(/bg-(bad|warn)\/5/);
    }
  });
  it("names a transcript's start", () => {
    const hb = { id: "h", kind: "human", order: 2, seq: 2, at: "", human: { conn: "c1", text: "", transcript: "abc" } } as unknown as SSHTimelineItem;
    expect(transcriptLabel([hb], hb)).toBe("Your terminal · 3 B");
    expect(transcriptLabel([system("wheel_taken", 1, "2026-01-01T14:02:00Z"), hb], hb)).toMatch(/^Your terminal since .+ · 3 B$/);
  });
  it("a direct session's list key is its host, never a slice of its id", () => {
    expect(sessionKey({ id: "lola-local-abc", location: "ssh", ssh: { connections: [conn({ host: "prod-api" })], lastSeq: 0 } as never })).toBe("prod-api");
    expect(sessionKey({ id: "lola-local-abc", location: "ssh" })).toBe("");
    expect(sessionKey({ id: "lola-local-abc" })).toBe("lola-loc");
    expect(sessionKey({ id: "x", issue: "ENG-1" })).toBe("ENG-1");
  });
});
