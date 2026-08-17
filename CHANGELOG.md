# Changelog

## [0.2.8](https://github.com/sushidev-team/lola/compare/v0.2.7...v0.2.8) (2026-08-17)


### Bug Fixes

* **lolaenv:** keep the shell-quoting doc comment gofmt-stable ([53b4006](https://github.com/sushidev-team/lola/commit/53b4006a7e3cd76f3925209e17e896a4f6c005c5))

## [0.2.7](https://github.com/sushidev-team/lola/compare/v0.2.6...v0.2.7) (2026-08-17)


### Features

* **desktop:** make a DMG-only install reach a working state ([95e7632](https://github.com/sushidev-team/lola/commit/95e76324c0b3c68fc06576e6700fa3c4c2f3def1))


### Performance Improvements

* **dev:** find the dev server's address seconds after activation, not a cycle later ([c2a106a](https://github.com/sushidev-team/lola/commit/c2a106a645a1cdd617d89609ce9508092567c2de))

## [0.2.6](https://github.com/sushidev-team/lola/compare/v0.2.5...v0.2.6) (2026-08-16)


### Features

* **daemon:** per-project dev commands with a one-session Active toggle ([13d825c](https://github.com/sushidev-team/lola/commit/13d825c4b0b3bc6ac319a88eb5f6d209ce879345))
* **desktop:** a loading state on Button ([cf7f187](https://github.com/sushidev-team/lola/commit/cf7f187595c21b8f87e703867ba3c6a2ce5a65e1))
* **dev:** reclaim squatted ports on take-over, and show what a session serves ([91f0278](https://github.com/sushidev-team/lola/commit/91f0278579d61ce548e1f885535c437e17b71fdf))
* **projects:** pick the folder, and the rest of the project fills itself ([b92e83c](https://github.com/sushidev-team/lola/commit/b92e83c356e5e32f1a1f8f86ff816d19c5ac9fb7))


### Bug Fixes

* **review:** never hand findings to an agent behind a modal dialog ([971c7f7](https://github.com/sushidev-team/lola/commit/971c7f77cfe416049b51819c52c846010ac0a7c6))
* **teardown:** kill every process below a pane, not just its group ([b5f2e31](https://github.com/sushidev-team/lola/commit/b5f2e319e5003817349c56faf5d124d08a78cb2d))

## [0.2.5](https://github.com/sushidev-team/lola/compare/v0.2.4...v0.2.5) (2026-08-12)


### Features

* **desktop:** make URLs in a terminal clickable ([21de937](https://github.com/sushidev-team/lola/commit/21de937082a5f9147aa83ef9c7d59e15b1694d8f))
* **review:** render the github review comment for humans ([ec41d99](https://github.com/sushidev-team/lola/commit/ec41d99f3baeaabdfe0853b24c8334791e0efa53))
* **runtime:** complete teardown on a merged PR ([f10d596](https://github.com/sushidev-team/lola/commit/f10d5963a0d31c9806f5ff22884720ad69660e04))
* **runtime:** per-session values in [[project]].env, and shells that export them ([289ade0](https://github.com/sushidev-team/lola/commit/289ade064decf7961e4f1014892cab24cc2ca258))

## [0.2.4](https://github.com/sushidev-team/lola/compare/v0.2.3...v0.2.4) (2026-08-07)


### Bug Fixes

* **ci:** prove the notarization key parses before submitting ([7fabcc3](https://github.com/sushidev-team/lola/commit/7fabcc3e48d045342b09f93e44cf258c48ee90eb))

## [0.2.3](https://github.com/sushidev-team/lola/compare/v0.2.2...v0.2.3) (2026-08-07)


### Features

* **desktop:** reshape the terminal grid into readable landscape tiles ([c4a72cb](https://github.com/sushidev-team/lola/commit/c4a72cb320a1adfe9e114da7262622f5f3bf23e0))


### Bug Fixes

* **desktop:** fill the active terminal tab instead of hiding it ([7cc1836](https://github.com/sushidev-team/lola/commit/7cc1836a61545f1f9053a9d5480154938762e45c))

## [0.2.2](https://github.com/sushidev-team/lola/compare/v0.2.1...v0.2.2) (2026-08-07)


### Features

* **desktop:** animate the tab strip and give rename real room ([5745ac0](https://github.com/sushidev-team/lola/commit/5745ac08a6c2fe78ef9fc5ceaf21f0ef510cbc8f))
* **desktop:** give the footer row two halves ([63edebc](https://github.com/sushidev-team/lola/commit/63edebc610c8a67fe884336a3c4d33d10384f307))
* **desktop:** move the version out of the utility row ([72dc778](https://github.com/sushidev-team/lola/commit/72dc778fc62e43800cfc9bcdd02981d874055ec0))
* **desktop:** unbox the cockpit into bands ([b19240e](https://github.com/sushidev-team/lola/commit/b19240ee285a09032fda1418f298aa898530670c))


### Bug Fixes

* **ci:** sign by the identity's hash, not by the entity's name ([03b3f39](https://github.com/sushidev-team/lola/commit/03b3f3927bb1bbf075c10b4711c09c7c03ab030d))
* **desktop:** let the daemon line have the footer until you reach for it ([39bf192](https://github.com/sushidev-team/lola/commit/39bf19200be16d956d1dec0b578b27b9e2e44b20))
* **desktop:** make the terminal band a sheet the list slides under ([9048e7a](https://github.com/sushidev-team/lola/commit/9048e7a357b9aeacc0e9f321de93b5c9a1f88da4))
* **desktop:** square the lens picker and quieten the board heads ([50024e7](https://github.com/sushidev-team/lola/commit/50024e75ce222d66ea7279817c0eace61bd46d95))

## [0.2.1](https://github.com/sushidev-team/lola/compare/v0.2.0...v0.2.1) (2026-08-05)


### Features

* **config:** [statusagent] section ([78f9aad](https://github.com/sushidev-team/lola/commit/78f9aadfb299d47a4b86ffea3ced19ce1689bef7))
* **daemon,protocol:** wire the status interpreter + display overlay ([8c5515a](https://github.com/sushidev-team/lola/commit/8c5515afdd10f97c58cf184e8b9ad9cdc67768ef))
* **daemon,runtime,session:** two-axis observer, reactions, and adoption ([8c3859f](https://github.com/sushidev-team/lola/commit/8c3859fc4747fdd4c56b1906fd109d5519cf1386))
* **desktop,tui:** synced multi-shell terminal tabs + kill dialog, WKWebView/keyboard fixes ([74b9448](https://github.com/sushidev-team/lola/commit/74b94483eb1e22592b32c4740d876ac598870f43))
* **desktop,tui:** TUI honors [ui].theme + unsaved-work guards and keyboard-trap fixes ([cf17624](https://github.com/sushidev-team/lola/commit/cf17624d7f1a154600cfdee07ec9eb939a1a100b))
* **desktop:** give actions a real button component ([813557c](https://github.com/sushidev-team/lola/commit/813557cc6e55306a7cdfdeb08062d70578b21915))
* **desktop:** give the ⌘ chords a Session menu of their own ([46618b9](https://github.com/sushidev-team/lola/commit/46618b901242db68568b133e172d6866475ace63))
* **desktop:** Interpreter settings tab + ≈ overlay rendering ([ffdf868](https://github.com/sushidev-team/lola/commit/ffdf868d897d00c9f6526ea38e892da1aee4cb19))
* **desktop:** let a second click on a project clear its scope ([6c387f1](https://github.com/sushidev-team/lola/commit/6c387f1cea76aa30d20446ecd7597a5695aa4a73))
* **desktop:** mirror the two-axis vocabulary + agent-aware pills ([9040b80](https://github.com/sushidev-team/lola/commit/9040b80243cf3b4e3ffcef31d873fef1c11ef388))
* **desktop:** name the macOS app "Lola" ([51b303d](https://github.com/sushidev-team/lola/commit/51b303d2880edd1ab2d4a5a85f98d90384ea77b8))
* **desktop:** replace the ad-hoc font sizes with a five-size type scale ([f72cade](https://github.com/sushidev-team/lola/commit/f72cade71203c0bcc8100cddeed9b179e8e6fca5))
* **desktop:** replace the cockpit's left rail with a full-height sidebar ([a87cb53](https://github.com/sushidev-team/lola/commit/a87cb5353fa86484c8d58d7ca0c8ef20f3f1cc32))
* **desktop:** session right-click context menu + non-selectable rows ([2d931c8](https://github.com/sushidev-team/lola/commit/2d931c85111925b5ace7310c6d4c0635ea7a02e9))
* **desktop:** stop printing the same fact in three columns ([343e583](https://github.com/sushidev-team/lola/commit/343e5830e964bcc31f26f816f70691b5d26d6d64))
* **hook,daemon:** ingest the full lifecycle-hook payload ([60fd045](https://github.com/sushidev-team/lola/commit/60fd045a0fed8105cb11c41a648f8ecf3eddc5ec))
* **protocol,tui:** expose both axes + freshness; agent-aware rendering ([fbcaa38](https://github.com/sushidev-team/lola/commit/fbcaa38802567115890a17ae5e5ad855eb7971cc))
* scaling of app possible ([0df277d](https://github.com/sushidev-team/lola/commit/0df277d87a64ff82bf73a62c8cf504a9a1ff8432))
* **scm,daemon:** delivery cutover — scm ships facts, state derives ([a0ae0ee](https://github.com/sushidev-team/lola/commit/a0ae0eef0cba16158ea555738ff62f136adeea64))
* **session:** two-axis fields, setter mutators, Store.Apply, axis migration ([af48b40](https://github.com/sushidev-team/lola/commit/af48b40118605eef56a15a0d1ab3b9d3a15e4ca9))
* **state:** single two-axis status vocabulary package ([ed928fa](https://github.com/sushidev-team/lola/commit/ed928fa099580027197e9faf041f6fbb8a794dd1))
* **statusagent:** bounded opt-in status-interpreter package ([5090650](https://github.com/sushidev-team/lola/commit/509065016bcca8434d063f1266468a0d1c7c3534))
* **tmux,daemon:** session_activity signal + one tmux ls per observe cycle ([9179f69](https://github.com/sushidev-team/lola/commit/9179f69d3a76b9f667de0f0ba3555b7f21dd97be))
* **tmux:** make the session status bar opt-in, and reconcile it on adopt ([de217ba](https://github.com/sushidev-team/lola/commit/de217ba622eb4a4b3f0f72248f5734860eda3c16))
* **tui:** Interpreter settings tab + ≈-marked interpreted status ([6d3f84b](https://github.com/sushidev-team/lola/commit/6d3f84b846e56a3d83417532324bfb77f6c8a5e9))
* unified TUI/app keyboard scheme + fix desktop WKWebView reactivity ([f253f35](https://github.com/sushidev-team/lola/commit/f253f35fa01157394d35341f5e1051ecef3ed81e))


### Bug Fixes

* **daemon:** deliver review hand-offs to a worker parked for input ([7d454ce](https://github.com/sushidev-team/lola/commit/7d454cebe8332f5a723e975d9081d1b50aa1db34))
* **daemon:** drop merged session from store even when its worktree is dirty ([8d4e133](https://github.com/sushidev-team/lola/commit/8d4e133167e246bc25b04adb5e0ac6b996ba22ed))
* **daemon:** drop the agent's notification once it stops waiting ([e22c846](https://github.com/sushidev-team/lola/commit/e22c846e7732149e7dde536d0b79db570b98a97a))
* **daemon:** hash-skip refreshes a still-valid interpreter overlay ([5a469a3](https://github.com/sushidev-team/lola/commit/5a469a396a58907c0f07cfc330b6faf366d6b54a))
* **desktop:** never delete the app bundle on a case-only rename ([8f026ca](https://github.com/sushidev-team/lola/commit/8f026ca94356bdf071802cae383e0bcefca6c3ac))
* **desktop:** non-selectable chrome + reformat the triage "need you" ([898834b](https://github.com/sushidev-team/lola/commit/898834ba47ee3f842cd271a28d2312594d781cb3))
* **desktop:** offer force when a kill hits a dirty worktree ([d02ad7d](https://github.com/sushidev-team/lola/commit/d02ad7dc26c35f69e5e3d8773cd0ba5336d4d009))
* **desktop:** stop the activity headline reading as an error ([5bf7739](https://github.com/sushidev-team/lola/commit/5bf7739a1d9f3db8b6e06f40ef1c8d0ccdf690f7))
* git keep keep ([649d76d](https://github.com/sushidev-team/lola/commit/649d76da360458b03720893ea5101d3de06e290e))
* **review:** give claude review findings a scannable shape ([33b9adc](https://github.com/sushidev-team/lola/commit/33b9adc58cc8c4e0c1eb0e7bc6c3f05268e79d5b))
* **runtime,daemon:** embedded shell tabs are not sessions ([b6c8835](https://github.com/sushidev-team/lola/commit/b6c883564fcc907f2b48552f6e9a9e473a87ce3b))
* **tui,desktop:** humanize every status label ([b0451f9](https://github.com/sushidev-team/lola/commit/b0451f9687c7be449716d4ab62a2c34087e9b683))
* **tui,desktop:** no agent badge for a routinely idle agent ([e2bfdb6](https://github.com/sushidev-team/lola/commit/e2bfdb61526212e48436e32d2b3943d3bc77a954))

## [0.2.0](https://github.com/sushidev-team/lola/compare/v0.1.0...v0.2.0) (2026-07-21)


### ⚠ BREAKING CHANGES

* remove the AO bridge — Lola is native-only
* rebrand aop → Lola; spawn context prompt; per-poll repo mapping

### Features

* backfill Linear issue titles for pre-existing sessions ([81cc595](https://github.com/sushidev-team/lola/commit/81cc59550262ebb2bec6735c240ed8f2602266b8))
* bulletproof working-vs-waiting state + isolated-tmux attach UX ([2ce07bb](https://github.com/sushidev-team/lola/commit/2ce07bbcbca12ed372aece3ddd03b7dfeaeef068))
* clearer attach failures, migration warning, unmissable PR display ([6f9533c](https://github.com/sushidev-team/lola/commit/6f9533c1fcbe8040f16922b653345fa55de006f2))
* cockpit status pills + Triage needs-you sparkline ([efab1f4](https://github.com/sushidev-team/lola/commit/efab1f4ca497c1fc3122e050e9572df8e03bfc77))
* cockpit visuals to match the proposal — row highlight, tinted pills, meter track ([89c40c0](https://github.com/sushidev-team/lola/commit/89c40c055ea8286c653493c3dc1d85deab12c5e4))
* CodeRabbit PR-comment watch + configurable coding agent ([7a083b0](https://github.com/sushidev-team/lola/commit/7a083b0a311392534594a0579b226b7d815fcaea))
* **config:** inherit project settings from [defaults] ([7f8c3fe](https://github.com/sushidev-team/lola/commit/7f8c3fee1a46f763dc2d9c085ad9a2b98df648cb))
* **config:** nest polls under projects with lazy migration (Phase 1) ([3c2c9fc](https://github.com/sushidev-team/lola/commit/3c2c9fc0a8068e16c019d2abaf3ad1b3ed4f9b64))
* **config:** project display label + daemon-side id rename ([60dda8c](https://github.com/sushidev-team/lola/commit/60dda8c1f6477cc3bd362606f42bd7afd3defb86))
* **daemon:** add cmd=projects, a cache-served project list (Phase 2a) ([9df26c9](https://github.com/sushidev-team/lola/commit/9df26c941bbb16a0baeaf816315e7fcf1ee081e5))
* **desktop:** adopt the hand-placed 'cut' runner placement for the app icon ([49a4fa4](https://github.com/sushidev-team/lola/commit/49a4fa4c3175d43fc4c9bed55c10083f5f4f8634))
* **desktop:** bigger app icon + wordmark logo in the status bar ([edfd18f](https://github.com/sushidev-team/lola/commit/edfd18ffa48a9de40f1e210c88f174a75b3a007c))
* **desktop:** Catppuccin Mocha terminal + fix the agent's black input box ([07226c8](https://github.com/sushidev-team/lola/commit/07226c89b6eccaa758b7f8462f2d27f01b186e3d))
* **desktop:** Catppuccin theming + exact Ghostty font-metric match ([a1334aa](https://github.com/sushidev-team/lola/commit/a1334aa9012c6ff9db2534ded2c8f6d4c4835660))
* **desktop:** enlarge the app icon runner ~12% to fill more of the tile ([fefe677](https://github.com/sushidev-team/lola/commit/fefe6776eca7cc155a4420c4257a7d350e72112c))
* **desktop:** focus-to-fullscreen terminal, padded embed, font polish ([47bdca5](https://github.com/sushidev-team/lola/commit/47bdca5f9094b55302fae76dfed4f75e59bb672b))
* **desktop:** Hack font for terminals — crisper, more legible text ([8e7695d](https://github.com/sushidev-team/lola/commit/8e7695dfc376f0296458b5ac400286e898c188fa))
* **desktop:** in-app self-update from GitHub Releases ([d0e8e1c](https://github.com/sushidev-team/lola/commit/d0e8e1cd932b2ecfccc4e230c7a6ae6335f8a1a1))
* **desktop:** Linear cascading poll pickers + first-run setup ([44567fc](https://github.com/sushidev-team/lola/commit/44567fc61d4a875798eb3edfae019955df82688f))
* **desktop:** native macOS app (Wails 3 + Svelte 5 + xterm) ([6a0f100](https://github.com/sushidev-team/lola/commit/6a0f1006ed1b08d73cd08b58b14cb8f707b24c42))
* **desktop:** native macOS app (Wails 3 + Svelte 5 + xterm) ([59f10ed](https://github.com/sushidev-team/lola/commit/59f10ed08a3528854746deac54da39bda6f16bb2))
* **desktop:** polish cockpit layout — filling panels, auto-select ([886dbcf](https://github.com/sushidev-team/lola/commit/886dbcfe162359dc3dd98f0904e7ff29ce23d247))
* **desktop:** scale the app-icon runner 1.30x to fill the tile ([70b69a6](https://github.com/sushidev-team/lola/commit/70b69a6598a0f04154170dcbe368edd16e79a0c7))
* **desktop:** settings button, status-bar menu, real app identity ([6076ca2](https://github.com/sushidev-team/lola/commit/6076ca2665bc796baf4c666985976893bcb27668))
* **desktop:** use lola icon for app launcher and window favicon ([975d012](https://github.com/sushidev-team/lola/commit/975d0129c2a000265c0659def1430040cc3d3dd3))
* **desktop:** use the edge-to-edge runner for the app icon ([e4b622f](https://github.com/sushidev-team/lola/commit/e4b622f3fce5ba1a6a8e17539ba9713ab76ac0ef))
* Detail panel explains an empty preview instead of a bare "(no preview)" ([ac48d98](https://github.com/sushidev-team/lola/commit/ac48d983c1c14d019cf111fe03c351ae15b84642))
* doctor overlay as a floating modal over the cockpit ([d6504ac](https://github.com/sushidev-team/lola/commit/d6504ace134c2bd0b2f34076585d3b0e7792d766))
* drop on_sent_remove_label — auto-remove trigger labels + form field help ([a3f6615](https://github.com/sushidev-team/lola/commit/a3f6615cbb9df7b4960ca682398bb678849e06e3))
* forward Linear API key to agent sessions via a 0600 env file ([1fb6763](https://github.com/sushidev-team/lola/commit/1fb67636768ca7de7b1e5ac20d3fd2968934b075))
* implement aop — Linear→AO poller per SPEC v2 ([ab99735](https://github.com/sushidev-team/lola/commit/ab997358489721c19e0d7c33dff994893ad64ce3))
* lola attach — tab-per-agent viewer over the tmux server ([9995991](https://github.com/sushidev-team/lola/commit/9995991b87de69715432120eee463685b7b5524f))
* lola doctor, first-run setup wizard, descriptive runtime error ([1f8bbc5](https://github.com/sushidev-team/lola/commit/1f8bbc5108450741cedb1ca7e70e16a50694d8cb))
* lola kill &lt;session&gt; [--force] + sessions-tab kill action ([ff382b6](https://github.com/sushidev-team/lola/commit/ff382b6e04005129f087a23933deef2b5ca40d69))
* manual worktree + openURL; keep the cockpit as the main screen (Phase 4b) ([f475460](https://github.com/sushidev-team/lola/commit/f475460a1154d1bf7d6f342b3dc6be1701d8befc))
* open a branch or PR in a throwaway worktree + shell ([e3124e2](https://github.com/sushidev-team/lola/commit/e3124e2a74f23c02634dd45f4c07fe30cba7b203))
* P1 session observability — tmux adapter, PR/CI observer, sessions TUI ([a881170](https://github.com/sushidev-team/lola/commit/a88117024d60aa7aeab3442317dabfe0b5377cd1))
* P2 native runtime — Lola spawns her own runners ([4be6a52](https://github.com/sushidev-team/lola/commit/4be6a529a62722486d525e5a90b0b55350dcacdd))
* P3 reaction engine — Lola runs again ([6415033](https://github.com/sushidev-team/lola/commit/6415033adfda27d8dfe82f95e39ee5992c67218d))
* P4 Linear write-back — state transitions, comments, escalation ([d067268](https://github.com/sushidev-team/lola/commit/d067268652aea7203889cd05cfbfb9c6ace1346d))
* P5 (scoped) — headless-claude summaries at decision points ([47eb17c](https://github.com/sushidev-team/lola/commit/47eb17c86870a3dc08a3d15b98f104071165a280))
* P7 attention & inline answer — reply to a stopped agent in place ([1892c8a](https://github.com/sushidev-team/lola/commit/1892c8a45d2fdbfc13e417a75b32e20a88f59621))
* P8 session views — filterable list + kanban, research-grounded ([f096b70](https://github.com/sushidev-team/lola/commit/f096b70a1a268b1b11de734b8b7ae556dd334087))
* P9 QA buddy — event-triggered CodeRabbit review pass (not a 2nd agent) ([2a3581e](https://github.com/sushidev-team/lola/commit/2a3581ea7f8414baad3ab53f48e8ff63c9aa6c30))
* poll edit form as a floating modal over the cockpit ([ae41b0a](https://github.com/sushidev-team/lola/commit/ae41b0a60905dcb24464b26d2e112804e31a05fc))
* PR picker — list & open a project's pull requests (Phase 4a) ([dcdc8ee](https://github.com/sushidev-team/lola/commit/dcdc8ee92d0da969928f6be23648ef55c9ed1c94))
* pr/manual agent launch — the push-back upgrade (Phase 5) ([2f9435d](https://github.com/sushidev-team/lola/commit/2f9435dd9bf56ed388787be37672f8b880c766cc))
* rebrand aop → Lola; spawn context prompt; per-poll repo mapping ([a351a01](https://github.com/sushidev-team/lola/commit/a351a014d7e42eb07feb40dbf8d73387e672760a))
* remove the AO bridge — Lola is native-only ([373dae9](https://github.com/sushidev-team/lola/commit/373dae9e28023c55e5fc14a9ccd56575d92e7b76))
* restructure cockpit list/kanban/triage; drop the relic summary strip ([0cd3b49](https://github.com/sushidev-team/lola/commit/0cd3b49f91ddc99c5d9502b18469a76637900007))
* revive a dead session onto its kept worktree ([640186e](https://github.com/sushidev-team/lola/commit/640186e72d3b42093c01ec5a080d2c7669662f43))
* **session:** freeze session Kind/Agentless discriminator (Phase 0) ([e7abaf3](https://github.com/sushidev-team/lola/commit/e7abaf366150e52de2326b3e3b78ff450c6bed59))
* **settings:** pick priority_sort keys in order ([c708114](https://github.com/sushidev-team/lola/commit/c70811443b34dd571c6bce7fbecb1e32cc41fb31))
* **settings:** pick workspace labels for the [defaults] keys ([608ec18](https://github.com/sushidev-team/lola/commit/608ec18daf19b57a97e785e0776d9b0717a5893e))
* surface the Linear issue title on sessions ([d37d2e0](https://github.com/sushidev-team/lola/commit/d37d2e093ebd368aba7eefa84ddcecb9e19135a7))
* ticket picker + openTicket — start a Linear issue on demand (Phase 6) ([4c2443b](https://github.com/sushidev-team/lola/commit/4c2443b48e0758d50b0d53c2a83a77cee9d4b64f))
* **tui,desktop:** auto-detect the GitHub repo from the checkout ([fe5dd64](https://github.com/sushidev-team/lola/commit/fe5dd649a26bf10c974c7e2677f277e475cecf0c))
* **tui,desktop:** one tabbed project form, inherit/override UX ([b4b88cb](https://github.com/sushidev-team/lola/commit/b4b88cb04be629ffa8ff7d4e46aeaa42a974df11))
* **tui,desktop:** pick the default branch from the checkout ([47e3baa](https://github.com/sushidev-team/lola/commit/47e3baaaaf7f18e7f8017997cc89caba107c75e1))
* **tui:** cockpit rail is a project switcher, drop the "Polls" vestige ([6de7b14](https://github.com/sushidev-team/lola/commit/6de7b14028ad9a7cd8f6d86a2073b89485dc9d43))
* **tui:** debounce the live-agent re-attach on selection change ([424e9f5](https://github.com/sushidev-team/lola/commit/424e9f510a6d37d5a5014e396d57f52c9ef71e69))
* **tui:** embed the agent — enter opens the tmux session in-panel, Ctrl-q detaches ([b0a532a](https://github.com/sushidev-team/lola/commit/b0a532afd8bd6fe439e65a36cd5d59193b711645))
* **tui:** embedded worktree shell — 's' opens a terminal, Ctrl-q exits ([8f68a16](https://github.com/sushidev-team/lola/commit/8f68a16c1d93be8f93f67a66e7b05db6e1d03078))
* **tui:** in-TUI project editor (P) + stop wheel→arrow translation ([64924e6](https://github.com/sushidev-team/lola/commit/64924e666616319192b1cdd1da902333c359422f))
* **tui:** live agent embedded in the Detail panel + enter-to-focus/expand ([80e2f3a](https://github.com/sushidev-team/lola/commit/80e2f3a835c31c633e29d70cfc94dc25627da31d))
* **tui:** mouse-wheel scroll forwarded to the focused embed ([7393920](https://github.com/sushidev-team/lola/commit/73939203f4df3be9646c230ba25002daf56d8f26))
* **tui:** persistent embedded terminals — Ctrl-q detaches, 's' re-enters ([509bb82](https://github.com/sushidev-team/lola/commit/509bb82b63d5644bc8aa1e5ca18f1a59eec2c34a))
* **tui:** project detail screen — the action hub (Phase 3) ([9a57126](https://github.com/sushidev-team/lola/commit/9a571264cf9f28dd10dd3842c33765cf78e2c3e2))
* **tui:** project editor — two-mode editing (open a list before editing lines) ([59034a5](https://github.com/sushidev-team/lola/commit/59034a5e962c13ea9b114442deedcf963fa677a3))
* **tui:** project-list Home screen as the landing view (Phase 2b) ([490b797](https://github.com/sushidev-team/lola/commit/490b797646ad11e5a54cfe42288e1b2d94eb97bc))
* **tui:** render the embedded terminal's cursor (bubbletea v2 tea.View.Cursor) ([eda546c](https://github.com/sushidev-team/lola/commit/eda546cf1b3067e21775866d1f78968e99985835))
* **tui:** rework Triage meters — thin bars, colored numbers, no gray block ([c704756](https://github.com/sushidev-team/lola/commit/c70475669ac6afef7c6ad92db6fc6413e473ee33))
* **tui:** rounded panels, per-pane gutters, numbered chips ([bdd2a3f](https://github.com/sushidev-team/lola/commit/bdd2a3f7cada99fbdebb7017063f1c308349881e))
* **tui:** self-manage the daemon lifecycle from the TUI ([0740038](https://github.com/sushidev-team/lola/commit/07400383f655728a0eed93149e6f9da7f877abda))
* **tui:** show an ellipsized title in narrow Sessions panels ([d52d946](https://github.com/sushidev-team/lola/commit/d52d9463811f98258d1a245bb3d8c5f74340a1e1))
* **tui:** truecolor cockpit palette on an opaque canvas ([70d58e1](https://github.com/sushidev-team/lola/commit/70d58e16a86602309ac7230d3d8587d48891b4dc))
* **tui:** unify shell + agent into one embedded/focus model; fix paste ([2c1107b](https://github.com/sushidev-team/lola/commit/2c1107becb44692aed8c32fc8de376e76c15b792))
* unified cockpit TUI — bordered panels, always-on vitals, focus model ([1b4b90e](https://github.com/sushidev-team/lola/commit/1b4b90ee9004446407225eb0d23522296b8196ac))
* **vtterm:** embedded-terminal engine (PTY + x/vt emulator) ([108c25d](https://github.com/sushidev-team/lola/commit/108c25d8091cbb5e3d87c61f1d5a014823258021))
* **writeback:** gate "In Review" on a valid PR + edit write-back in the TUI ([6232444](https://github.com/sushidev-team/lola/commit/6232444e72d5f793dcf3465a7522c1ad8b384a86))


### Bug Fixes

* **attention:** a resting prompt beats a frozen status-line working cue ([dcb0727](https://github.com/sushidev-team/lola/commit/dcb0727a8588ea763ee496308c88e3cca40e6185))
* **ci:** run on macOS, skip build scaffold, and split -race around an upstream vt race ([c8ff6a5](https://github.com/sushidev-team/lola/commit/c8ff6a5cf4fd37e0de24e86807ae2766be4bb36d))
* **ci:** track desktop/frontend/dist/.gitkeep so the embed resolves on a clean checkout ([e31e55a](https://github.com/sushidev-team/lola/commit/e31e55a30c748f5aa7f473caec53450e1ff1c406))
* **coderabbit:** reliably deliver PR feedback worker, scroll default ([6ed77e7](https://github.com/sushidev-team/lola/commit/6ed77e76e84336207c6b4fe0ba9d43668ee43fe1))
* **config:** repair legacy priority_sort keys instead of rejecting them ([b125604](https://github.com/sushidev-team/lola/commit/b125604cbe980f72313a96afc5d44f7bff7ba5bd))
* **daemon:** shield openTicket's spawn from shutdown like a tick ([6d44d82](https://github.com/sushidev-team/lola/commit/6d44d82acb5ebcdd97fe081b9c818e9965c10cd2))
* **desktop:** address review findings across backend, frontend, and TUI ([fc4308e](https://github.com/sushidev-team/lola/commit/fc4308e55ae368519c64fc5cdcc1362025cbcfc5))
* **desktop:** full-width panels in WebKit + resilient refresh ([77d080b](https://github.com/sushidev-team/lola/commit/77d080bb0b344fd9bf73ba865fb8143ab403509e))
* **desktop:** hold --color-faint to a 3:1 legibility floor ([3613d32](https://github.com/sushidev-team/lola/commit/3613d32f45a849747571a4de70b201bbfb3e2f83))
* **desktop:** left-align triage meter values, drop the indent ([3286a07](https://github.com/sushidev-team/lola/commit/3286a0745cadd478e2354bb553e2e2362f14795a))
* **desktop:** make the app icon fill the tile (no nested-square Dock icon) ([8bb38fb](https://github.com/sushidev-team/lola/commit/8bb38fb698900816dd1789039ca27f97575cfa1e))
* **desktop:** open terminal tiles on single click, not double ([c642fb0](https://github.com/sushidev-team/lola/commit/c642fb0bf152f646c43d4bb1cb433b9a7850f46a))
* **desktop:** rail panels always visible, vitals spacing, JetBrains Mono ([cbf8cad](https://github.com/sushidev-team/lola/commit/cbf8cadc95df0e150e36f096bdedd6d0a40f5c58))
* **desktop:** ship a full-bleed icns only so the Dock icon fills the slot ([351614c](https://github.com/sushidev-team/lola/commit/351614c676cd9d1f5c818de3534ae70a7a5b1e9b))
* **desktop:** shorter vitals bar, traffic lights centered to it ([ceaf670](https://github.com/sushidev-team/lola/commit/ceaf670c8c14e47a7399574d290e726b7cd1ede4))
* **desktop:** shrink status-bar logo and tighten gap to traffic lights ([0c0677f](https://github.com/sushidev-team/lola/commit/0c0677f29fd4dc837fd8e9f7f7c39d95b39138eb))
* **desktop:** strip background from the Liquid Glass icon layer ([0106d83](https://github.com/sushidev-team/lola/commit/0106d832f5e1815a7c3a9521d24991428a8b6b9d))
* **desktop:** track a frontend/dist placeholder so go vet works on a clean checkout ([bd666c6](https://github.com/sushidev-team/lola/commit/bd666c6f95f167806f9d384cb013a9ecdebb551e))
* **desktop:** traffic lights at standard top position ([7827fa2](https://github.com/sushidev-team/lola/commit/7827fa27cc6904a5051322fcd2b18b74dffbbd0b))
* **desktop:** transparent terminal background, blends into lola ([4aae7f0](https://github.com/sushidev-team/lola/commit/4aae7f0f41079500ad31e013ec145ec428ce3d5b))
* icon on dev ([e892f14](https://github.com/sushidev-team/lola/commit/e892f145df85b3ec7913a7913112f77b1ffbf947))
* reload after rename etc. ([41f5960](https://github.com/sushidev-team/lola/commit/41f5960563a6e17b5048ada8fabaddefb9ad37a7))
* remove fiinished sessions ([5f6385e](https://github.com/sushidev-team/lola/commit/5f6385e1e1fee54ee8266f89010ed373569921c4))
* **tmux:** pane-target commands need "=name:" not "=name" (broke preview, answer, reactions) ([3958459](https://github.com/sushidev-team/lola/commit/39584591e5a790ce7ab370db318ec4bdc69d72f8))
* **tmux:** pin server cwd so a deleted dir can't wedge agent launches ([fd6e80b](https://github.com/sushidev-team/lola/commit/fd6e80b163ecea717cec06b1c5ad9d08a55cd93b))
* **tui:** drop dead project picker from the polling form ([eeb8476](https://github.com/sushidev-team/lola/commit/eeb8476f1302e3f93dc9f3cf2657120be8b40d18))
* **tui:** drop pane-heading digit, put the name in the chip ([4b3bceb](https://github.com/sushidev-team/lola/commit/4b3bceb201fd2c442d9dd5ce67cddec801f900cc))
* **tui:** n on the rail creates a project again ([35f626e](https://github.com/sushidev-team/lola/commit/35f626e10191c75ac8d091462e1c82838f2efc2b))
* **tui:** name the stale daemon when it rejects a valid config ([429ed0e](https://github.com/sushidev-team/lola/commit/429ed0eb64e2d0b23ee7d8d7525db5a1ca55d451))
* **tui:** paste into text fields ([2f33d72](https://github.com/sushidev-team/lola/commit/2f33d72c30973bf2efcb618e7ace112cb29de69d))
* **tui:** show label names, not UUIDs, in the settings editor ([79521ab](https://github.com/sushidev-team/lola/commit/79521ab9b0d549cfd5e0ee81bc41333ecb859e07))
* **tui:** show the effective sort chain, not "(none)" ([7fdbbaa](https://github.com/sushidev-team/lola/commit/7fdbbaacbc1fd0c8606f04514b8b005d64e4984a))
* **tui:** space toggles multi-select and rail enable again ([c56589c](https://github.com/sushidev-team/lola/commit/c56589c41eab9e1eb1de1e317e473e38913273ae))
* ui improvements & fixes ([183d4a6](https://github.com/sushidev-team/lola/commit/183d4a665277a5272096f62a59d65434d63f3d6f))
* **vtterm:** answer terminal queries — embedded agent was blank because nothing replied ([1e2d7b8](https://github.com/sushidev-team/lola/commit/1e2d7b8397e6e8f95240cdfd41ddc18f175598ea))
