# Project workspace requirement check

Checked against the requests in this conversation on 2026-11-12.

- Project clicks open Overview/Git content; session links still open sessions. Overview includes session/activity/repository information. The last project tab persists across projects and reloads.
- One compact project toolbar; centered Git operations, emphasized Push, Stash and Pop. Sidebar checkout header and instruction footer removed.
- Remote identity and worktree entries are compact single rows; full worktree paths remain available on hover.
- Local/Remote/Worktrees/Stashes sections have icons and square, edge-to-edge headers. Slash-separated branches collapse recursively. Active checkout is highlighted. Disclosure motion honors reduced motion.
- Left-aligned ref badges, remote owner avatars and checkout markers align beside the graph. Author avatars fall back to initials. Pending uncached profiles shimmer subtly, respecting reduced motion; cached pictures remain visible during refresh. Images and hashed repository/email associations persist under ~/.lola/cache/git-identities, with one background refresh per used identity/image per app process. Lane tint is confined to the graph, fading right-to-left. Badges have solid fills, semibold text and a 20px height. Hover tints branch badges and reveals full names over adjacent columns without shifting layout. Badges align with their graph nodes without horizontal connector strokes; side-branch connectors leave the node horizontally before rounding into the adjacent lane; through lanes remain vertical at row boundaries. A neutral hover/focus band spans message, author, SHA and age, honoring hidden/resized columns.
- Working changes appears only for a dirty checkout, with an In progress badge, dashed node, modified/added/deleted/conflict counts and a hover/focus review cue. Commit text and its label are 13px, counts 12px. Numeric values, SHAs and ages use JetBrains Mono.
- History has no heading bar. Cmd-F is wired through the native menu; browser Ctrl/Cmd-F also opens the loaded-history search. Column settings are at the top right, with persisted visibility and pointer/keyboard resizing.
- Commit clicks show metadata/files without opening the diff pane. File selection or View changes opens it. Modifier selection compares two commits with explicit direction and swap.
- The compact commit tooltip appears after 500ms only over the message column (keyboard focus remains available). It groups title, author, date and abbreviated SHA, plus up to three branch chips and an overflow count. Same-tip local/remote pairs and redundant remote HEAD are collapsed. Checkout details are concise; exact values remain in accessible labels. Native titles do not overlap it. Equivalent rerenders and rapid re-entry preserve the reveal timer.
- Context menus cover branches, remotes, worktrees, commits, stashes and rail background. Double-click checks out; internal pointer dragging uses an exact-size rendered copy anchored to the original grab point, preserves below-threshold clicks and cancels on Escape. Drops require an explicit branch badge or sidebar branch; arbitrary commit/graph rows are not targets. Drops onto the same branch open its actions. Drag/drop opens a directionally explicit merge/rebase chooser. Mutating branch refs are checked against the selected tips.
- Unchecked pulls fast-forward their upstream without switching the visible checkout. Existing branch owners are reauthorized through the worktree/sandbox boundary. Divergent pulls are refused; branch push requires an upstream.
- Stash includes untracked but not ignored files; apply/pop require a clean checkout and matching HEAD. Conflicted pop retains the stash. Stash actions and history-changing operations have explicit confirmation. Merge/rebase conflicts expose recovery controls.
- Staged/unstaged sections scroll separately. Files have one compact row and list/tree layouts. Shift-click ranges and Cmd/Ctrl-click toggles are independent per section; tree ranges follow visible files. Green hover/focus actions and Stage all act only when clicked.
- Commit composer has subject/body, a confirmed amend option pinned to HEAD, and a signing shield tooltip at the right of the Amend row. Drafts survive failures. Installed Git preserves normal hooks/signing.

Evidence: focused frontend/component/store tests; native-Git temporary repository tests (including SSH signatures, unchecked pull, merge/rebase recovery and stash conflicts); service sandbox tests; Svelte check/build; WebKit desktop/light/narrow captures in this folder. Browser fixtures never mutate the user's repository. Native app packaging/relaunch is not part of this verification.

Background refresh regression: daemon project-object replacements previously retriggered the Git project-reset effect. ProjectDetail now passes a stable scalar project name. Tests verify repeated project updates preserve single selection, two-commit comparison, direction and the open diff file; real project changes still reset selection.

## Reference research

[GitKraken branching](https://help.gitkraken.com/gitkraken-desktop/branching-and-merging/) supports the double-click and directional drag/drop model. [Git fetch documentation](https://git-scm.com/docs/git-fetch) informs the nonchecked-branch update behavior. [Fork release notes](https://git-fork.com/releasenotes) emphasize predictable file-list selection and staging navigation. [GitButler's getting-started guide](https://docs.gitbutler.com/getting-started) illustrates explicit worktree and drag-based review workflows. Lola keeps ordinary Git branches/worktrees and uses the installed Git executable.

Final confirmation: WebKit desktop, narrow and light layouts passed with no page errors or horizontal page overflow. Pointer/checkout tooltips, branch double-click, drag chooser, four-file range selection and numeric typography were verified. Final focused frontend set: 189 tests passed across 16 files. Cache/service backend and race tests passed. Six real WebKit pointer-drag paths opened menus without mutations. Avatar browser checks verified cached raster bytes, stale display during refresh, reduced-motion behavior and zero remote image requests in cached/loading fixtures. Svelte check: zero errors/warnings; production build passed (existing chunk-size warnings).

Badge refinement verification: 24 component tests passed; WebKit confirmed the 159×20 badge produces a 159×20 drag copy at the exact pointer delta, drop menu still opens, and full-name hover preserves all column geometry.

New branch follow-up: toolbar and branch/commit context menus share a form with a pinned starting commit, local/remote base selector, optional checkout, retained error drafts and busy protection. SHA is smaller muted monospace inside the selector, without overlapping long names. Native Git tests cover existing-name refusal, WIP preservation, transactional failed checkout and service authorization. The focused integration set passed 64 tests. WebKit verified inline SHA geometry, remote selection updating it, and create-without-switch against a fake service; no real repository mutation occurred. Screenshot: [New branch](new-branch.png).

Previously recommended actions (implemented in the follow-up below): publish/set upstream first (new branches currently require an upstream before Push); create a worktree from a branch; cherry-pick/revert with recovery controls; compare commit against working copy. The user subsequently authorized these additions. References: [GitKraken push/upstream](https://help.gitkraken.com/gitkraken-desktop/pushing-and-pulling/), [worktrees](https://help.gitkraken.com/gitkraken-desktop/worktrees/), [cherry-pick](https://help.gitkraken.com/gitkraken-desktop/cherrypick/).


Publish and Git action follow-up: local-only Push opens an inline remote/name form; successful publication sets upstream without forcing divergent history. Branch context menus create worktrees in new folders outside existing checkouts and managed storage. Commit menus add confirmed cherry-pick/revert, recovery controls, and commit-to-working-copy comparison including staged, unstaged and untracked files through a temporary index. The subsequent audit below adds explicit parent selection for merge commits. No user repository operations were executed during verification.

Evidence: 65 focused frontend tests passed, including background selection preservation; service tests cover unknown projects, sandbox branch owners and symlinked managed destinations. Native Git fixtures cover custom remote names, rejected/stale pushes, worktree occupancy, conflict recovery and preservation of split-index bytes during comparison. Svelte check has zero errors/warnings and the frontend build passes. WebKit fixture verifies publish destination submission, new worktree submission, confirmation cancellation, explicit working comparison and narrow layout with no page errors. Captures: [Publish](publish-branch.png), [Narrow](publish-narrow.png), [Light](publish-light.png), [Worktree](create-worktree.png), [Commit menu](commit-actions.png), [Working comparison](compare-working.png).


## Action coverage audit and final refinements

- Set upstream is available independently of publishing, from branch, worktree and repository menus. Local-to-remote drops prefill Push and Set upstream destinations. Tracking changes pin both refs and never push or switch branches.
- Menus disable pulls without upstream, unavailable checkout mutations, incompatible operations during recovery, and stash application onto a dirty checkout. Existing accessible branch owners open their checkout directly. Remote-to-remote drops no longer promise an unavailable operation.
- Merge cherry-pick/revert asks for a parent and names it in confirmation. Continue/Skip/Abort are reachable from recovery controls and repository/current-worktree menus. The ordinary commit composer is disabled during recovery while staging remains usable. Refresh errors say Refresh rather than implying an operation will be retried.
- Changed-file context menus preserve multi-selection, support Shift-F10, and provide view, stage/unstage and copy paths. The regular list uses dim directory prefixes before brighter filenames; directory text truncates first.
- Working changes connects to the actual HEAD. A reserved lane handles HEAD below newer commits without visually attaching to those unrelated commits; search/unknown HEAD omit the connector.

Evidence: 74 focused frontend tests, Svelte check with zero errors/warnings, production build, native temporary-repository upstream/skip/mainline tests and desktop authorization tests. WebKit confirmed both HEAD topologies, file menus, path hierarchy, local-to-remote drag choices, tracking-only submission and narrow layout with no page errors or overflow. Captures: [Working changes and files](working-connection-files.png), [Older HEAD](working-connection-older-head.png), [Remote drop](remote-drop-actions.png), [Set upstream](set-upstream.png), [File menu](file-context.png), [Narrow files](files-narrow.png). Real native-app interaction remains unverified; browser actions use fake services.

Sidebar and discard follow-up: both Git sidebars have pointer/keyboard resize dividers, persisted independent widths and double-click reset; widths are constrained to retain history space, and handles disappear for drawer/stacked layouts. The Changes header centers list/tree controls and places a destructive icon at the right. Discard requires the shared confirmation, names tracked restoration and untracked deletion counts, preserves staged/ignored content, and pins branch, HEAD, index and file bytes before mutation. Changed confirmations fail closed. Nested repositories, submodules, directories, active operations and intent-to-add entries are refused rather than recursively deleted or silently left partially discarded.

Evidence: 69 focused frontend tests passed, Svelte check has zero errors/warnings and production build passed. Native temporary-repository tests cover staged edits/renames, deletions, literal paths, symlinks, ignored files, unborn branches, stale file/index/branch state, nested repositories and intent-to-add refusals. Service tests cover authorization and sandbox refusal. WebKit pointer dragging changed widths from 260/320 to 330/410, retained them on reload, verified centered layout controls and confirmed/cancelled discard against fake services. No page errors or narrow overflow. Screenshots: [Resizable sidebars](resizable-sidebars.png), [Discard confirmation](discard-confirmation.png), [Narrow layout](discard-narrow.png). No real user changes were discarded.

Loading and composer follow-up: first repository reads use aligned placeholders for navigation, history and files, with no false empty-state counts; refresh retains loaded content. Skeleton motion honors reduced motion. Stage/Unstage actions use white text on green/red fills; the discard icon stays red and tinted. Enabled shared buttons use a pointer cursor. Push has a stronger 180ms hover fill; Pull/Push arrows animate once per hover in their respective directions and remain still with reduced motion.

AI draft uses the existing tool-free Claude helper with Haiku and a 30-second deadline. It reads bounded staged excerpts, recent subjects and the editable draft; it never commits. Backend index/HEAD fingerprints and frontend message/revision checks reject stale responses. Native tests cover staged-only content, unborn repositories, sandbox/unknown-project refusal and changes during inference. Live Claude inference and latency remain unverified.

Validation: 76 focused Git frontend tests and 14 shared-control tests passed; focused Go package/service tests passed; Svelte check reports zero errors/warnings and production build passes. WebKit fixture checked loading, reduced motion, fake AI completion, action colors, pointer cursor and single-run arrow animations. Captures: [Loading](loading.png), [Narrow loading](loading-narrow.png), [Light loading](loading-light.png), [AI draft](ai-commit-draft.png), [Light AI draft](ai-commit-draft-light.png). Native app interaction remains unverified.

Hover/spacing refinement: project rows, folder rows and session controls reveal their hover states instantly. Pull/Push arrows now translate through a clipped 16px window, moving 20px out and re-entering from the opposite side at full opacity, once per hover. Git sidebar sections have no inter-section margins or doubled borders when collapsed. WebKit verified zero hover transition duration, opposite arrow positions in each animation half with opacity 1, reduced-motion suppression and zero section gaps. Ninety focused tests passed; Svelte check and production build passed. [Compact sections](compact-sections.png).

Remote provider labels now use bundled 16px Simple Icons marks for github.com, gitlab.com, bitbucket.org and codeberg.org; other hosts use a generic Git mark. Provider names remain as tooltips and accessible image labels. No runtime icon downloads or provider guessing for custom hosts. WebKit verified all four marks and fallback sizing/names; 33 Git UI tests, Svelte check and production build passed. [Provider icons](remote-provider-icons.png).

Toolbar/remote follow-up: Fetch now lives beside each remote provider mark and targets that remote through the existing validated branch-action API. Only the active remote shows a spinner; fetching never toggles the disclosure. Fetch all remains in the repository menu. Removed the toolbar refresh control: local facts reread on window focus unless already loading/mutating, with manual Refresh repository in the repository context menu and error recovery. Push's one-shot arrow duration is 260ms. The main checkout uses a house icon and branch name, retaining its full accessible name. Seventy-six focused tests, Svelte check and production build passed; WebKit verified the remote target, disclosure state, refresh menu, icon and duration. [Toolbar](compact-toolbar.png), [Remotes and checkout](remote-fetch-checkouts.png).


Create PR: toolbar, branch/worktree/repository menus and branch drag menus share
one GitHub form. Dragging source onto target preserves that direction. Source
forks default to the parent; choices include configured repositories. Searchable
multi-select comboboxes portal their lists above the form, without changing its
height. Arrow keys/Enter select, Escape closes the list, Tab advances; selected
values remain visible when closed. Drafts suppress reviewer requests. Local-only
publication requires an explicit checkbox, pins the source tip and never forces.
Failed metadata retains the PR URL; existing and ambiguously created PRs are
recovered without a second creation. AI drafts use bounded committed content,
preserve human edits, and reject stale selections. GitHub only, via signed-in gh.

The publish form is one row, toolbar says Branch, and loading replaces the leading
icon. Graph badges omit remote prefixes while accessible descriptions retain them;
the active checkout uses a computer icon inside its badge.

Validation: 65 focused frontend tests passed, covering the form, combobox keyboard
behavior, drag direction, stale results and failure recovery. Focused native Go
package/service tests passed, including source-owner sandbox refusal, fork targets,
explicit publishing and ambiguous creation. WebKit fixture checked actual pointer
drag into the PR menu, prefilled form, overlay geometry, selection, fake creation,
spinner replacement, badge glyphs, compact publish row and dark/light/narrow layouts.
No real PR or live AI request was made; native app interaction remains unverified.
Captures: [Drag menu](pr-drag-menu.png), [Create PR and combobox](create-pr.png),
[Narrow](create-pr-narrow.png), [Light](create-pr-light.png),
[Compact publish](publish-compact.png).

Final verification: Svelte check reports zero errors/warnings and the production
build passes (existing bundle-size/dynamic-import warnings remain).


Amend confirmation refinement: read remote-tracking ancestry at submit time,
including non-upstream branches and descendant tips. Local-only commits skip the
dialog; known published commits show a short warning with a primary action.
Failed checks and changed drafts/HEAD never trigger an amend. No automatic fetch.
Twenty-two focused UI tests and native amend/service tests passed, as did Svelte
check (zero errors/warnings) and production build. WebKit verified both paths
with fake mutations and the primary styling. [Pushed amend](amend-pushed.png).
