# Changelog

## [0.19.0](https://github.com/sushidev-team/lola/compare/v0.18.0...v0.19.0) (2026-11-24)


### Features

* **desktop:** add Linear agent assignment and hub claim status bindings ([c65b7c5](https://github.com/sushidev-team/lola/commit/c65b7c5d51ba1d22f906c39dc72462d3d8009524))
* **preview:** add docked developer tools, saved pages, and image comments ([dadcf7f](https://github.com/sushidev-team/lola/commit/dadcf7f7483a10fb8df99f17d66f5813ad765371))
* **preview:** add editable annotations and batched feedback ([d9e3f5d](https://github.com/sushidev-team/lola/commit/d9e3f5d0bc87b3e74890a434cb666a4d10865104))
* **preview:** edit CSS in place and show matching DOM end tags ([6a1d486](https://github.com/sushidev-team/lola/commit/6a1d4863c0f5cbedd70d4db90f2806b2f6fee100))


### Bug Fixes

* **desktop:** scroll to the current branch tip after pulling ([3697779](https://github.com/sushidev-team/lola/commit/3697779c07f7b1382c19e61dc03b05ce126b441a))
* **preview:** keep auto-login across dev port changes ([fa8de15](https://github.com/sushidev-team/lola/commit/fa8de15c7fcb4f7a75191172901f35f6dfc139fb))
* **preview:** preserve both endpoints in arrow feedback ([f8a4b9b](https://github.com/sushidev-team/lola/commit/f8a4b9b50c70932cf9936a64b395c39d2e1b594f))
* **preview:** restore DOM wrap toggle and remove row tooltips ([5d50608](https://github.com/sushidev-team/lola/commit/5d50608d83729b5f3d4b850bc06e467ec5f1a97c))
* **projectgit:** order branch history by author date ([9f446a5](https://github.com/sushidev-team/lola/commit/9f446a54f3caffde41543f6a8a2a339913a48493))
* support image attachments in borrowed checkouts ([78b2322](https://github.com/sushidev-team/lola/commit/78b23222d94e7c2c6a6c48417b60e04c1635769b))

## [0.18.0](https://github.com/sushidev-team/lola/compare/v0.17.1...v0.18.0) (2026-11-24)


### Features

* **conversation:** link replies to the original message ([3c4e27e](https://github.com/sushidev-team/lola/commit/3c4e27eafbc01bfa6c0a4f8182377419fd951445))
* **desktop:** add bulk project import from a parent folder ([aca8faa](https://github.com/sushidev-team/lola/commit/aca8faa9233f9694606d0a0891a683b4b19b27b7))
* **desktop:** add preview tab navigation and favicons ([38dfee1](https://github.com/sushidev-team/lola/commit/38dfee1881b02a12538b770be2a10b714122ce76))
* **desktop:** add project group actions and highlight removals ([8154c04](https://github.com/sushidev-team/lola/commit/8154c04d8705b7e509989b7cf50fb187d760f09e))
* **desktop:** open Git directly from Preview with Cmd+Shift+G ([e42dd4f](https://github.com/sushidev-team/lola/commit/e42dd4f0b3a68334bc7fb049e4d237f5ad47988c))
* **desktop:** prioritize the session overview and open changes in Review ([56eb3d4](https://github.com/sushidev-team/lola/commit/56eb3d40bcb000a264dc763d647f4e38ca8be927))
* **desktop:** show loading progress in preview webviews ([09e5314](https://github.com/sushidev-team/lola/commit/09e53145a516cc70c417f762b47f1fd00112aa85))
* **desktop:** toggle session preview with Cmd+Shift+P ([8ab5574](https://github.com/sushidev-team/lola/commit/8ab557410ade33e6b08817014134a22f76dc654d))
* **preview:** add saved test logins and optional auto-login ([ad73f8c](https://github.com/sushidev-team/lola/commit/ad73f8cd7fb76f038f64749a2dbf5d6d61fc97c0))
* **preview:** open any web address and preserve Statamic sidebar sessions ([49000d0](https://github.com/sushidev-team/lola/commit/49000d0c058eddcfcc50295ab181754bf4b68541))


### Bug Fixes

* **daemon:** send the task reminder once per session ([bbe21f8](https://github.com/sushidev-team/lola/commit/bbe21f8bc351e33dc3c145aad84559069cc6bea7))
* **desktop:** keep dev preview tabs open in the background ([c20f6bd](https://github.com/sushidev-team/lola/commit/c20f6bd60ccf56164eae26d154949a0d37dce076))
* **desktop:** prevent WebKit click offset in capped preview stage ([90ffcaf](https://github.com/sushidev-team/lola/commit/90ffcafe75b7475d5463076cff2e0bdc78f419ef))
* **desktop:** remove doubled Git sidebar borders ([c887a70](https://github.com/sushidev-team/lola/commit/c887a7079ca24e3606f0f80823bab569eccd2741))
* **desktop:** retry saved logins after preview redirects ([55c4a3a](https://github.com/sushidev-team/lola/commit/55c4a3a815bf4254acb2dbb3b3a238b15ac2f135))
* **desktop:** show and refresh worktree changes in the Git inspector ([61801a4](https://github.com/sushidev-team/lola/commit/61801a47cd664ec4413a0ca7662bfccff6684b22))
* lease ports and enable Start dev in local checkout sessions ([438450f](https://github.com/sushidev-team/lola/commit/438450fb26aa7eb25bf961646cd10368491b0f0c))
* **mobile:** handle desktop project folder methods ([379f36f](https://github.com/sushidev-team/lola/commit/379f36f75cd977e263c2e73402d548251e435774))
* preserve per-repository PRs across daemon restarts ([30de219](https://github.com/sushidev-team/lola/commit/30de2198f0209e748c5af05eb0d443fdc47d7f4c))
* **preview:** keep Statamic control-panel navigation on the preview origin ([ee26efc](https://github.com/sushidev-team/lola/commit/ee26efc004c07785154580f06b68d1759c8ba383))
* **projectgit:** preserve local changes during branch merges ([5be6f11](https://github.com/sushidev-team/lola/commit/5be6f110a9d9d20d62f44575bfaec19df1677ffa))

## [0.17.1](https://github.com/sushidev-team/lola/compare/v0.17.0...v0.17.1) (2026-11-22)


### Bug Fixes

* **mobile:** route sandbox tab login checks locally ([f83cc4d](https://github.com/sushidev-team/lola/commit/f83cc4dcad66e9d1f0b2ceda8cec68a730722bad))

## [0.17.0](https://github.com/sushidev-team/lola/compare/v0.16.0...v0.17.0) (2026-11-22)


### Features

* **conversation:** copy code from collapsed and expanded snippets ([6f15050](https://github.com/sushidev-team/lola/commit/6f15050fc7d3e40c3719f74a68f1e976a5149029))
* **desktop:** add live tabs to session previews ([54e4981](https://github.com/sushidev-team/lola/commit/54e498108eba92d376a32c210cfae2d5117f6b13))
* **desktop:** open agent conversations in tabs on the same checkout ([663a480](https://github.com/sushidev-team/lola/commit/663a4803732e25c45639a3aee95e9d79bd445468))
* **desktop:** queue branch deletions and refresh open branch menus ([dd1904f](https://github.com/sushidev-team/lola/commit/dd1904f068e9e19ac35fd1fbf84ca272e950e90a))
* **desktop:** show progress on active branch rows ([4017167](https://github.com/sushidev-team/lola/commit/4017167d27efabc8243fa34a28a9b4e42371d373))
* **review:** keep hash routes in preview comments and wait for the picker before inspecting ([c427c7b](https://github.com/sushidev-team/lola/commit/c427c7ba8aecb93fc3b56f33cf159b14514d51c7))


### Bug Fixes

* **desktop:** close the unstaged diff when its changes are cleared ([c19919c](https://github.com/sushidev-team/lola/commit/c19919cb1bb9025dbdea0143f6688a9936f18a21))
* **desktop:** prevent long activity maps from overflowing the conversation ([0a26051](https://github.com/sushidev-team/lola/commit/0a26051861a2f74427653ab7012babbd94c4dbd1))
* **git:** align merge lanes and working changes in the commit graph ([2ea671c](https://github.com/sushidev-team/lola/commit/2ea671c8ab9178b83faf3631f5d2ede495d96932))
* **preview:** close retired proxy listeners, restrict upstream dials to loopback and stream oversize pages ([a3bdb1f](https://github.com/sushidev-team/lola/commit/a3bdb1fe66377b8ab6ed127d24fce481a34f1b79))
* **preview:** keep embedded login requests on the preview origin ([702fcea](https://github.com/sushidev-team/lola/commit/702fceabbc84030f4e7146901288b85d49b2558f))
* **projectgit:** allow deleting branches merged into the remote default ([0afabd0](https://github.com/sushidev-team/lola/commit/0afabd04d4732084dd4f720dd14d7fd78a1c354d))

## [0.16.0](https://github.com/sushidev-team/lola/compare/v0.15.0...v0.16.0) (2026-11-21)


### Features

* **desktop:** open graph and inspector pull requests as links and simplify the commit summary ([92b9069](https://github.com/sushidev-team/lola/commit/92b9069536a8fc9e111665721cf77d7f097df02f))
* **dev:** a sandboxed agent requests its dev server; the human's click starts it on the host ([17be5f6](https://github.com/sushidev-team/lola/commit/17be5f6dc86ee12926d10d9bab3809cb9943ee39))
* **review:** agent-prepared tabs and matching screenshots ([d28d1df](https://github.com/sushidev-team/lola/commit/d28d1dfdd9394b5153bffa03705f5f3916892171))
* **review:** live dev pages in the review package ([8afccd5](https://github.com/sushidev-team/lola/commit/8afccd57f215f8a27f288b8d94dfcbb27212a98f))
* **review:** live dev pages in the review package open in the preview ([3b099f6](https://github.com/sushidev-team/lola/commit/3b099f6b5522855ddc331fbc75e7a2bbc7618b78))
* **review:** open live pages as preview tabs, agent dev start and port-setup request, phone opens forwarded pages ([0531593](https://github.com/sushidev-team/lola/commit/053159355274f68afac10f2f093e02b967e6ed91))
* **review:** prepare and retain live pages in the review canvas ([db1c71e](https://github.com/sushidev-team/lola/commit/db1c71e2d1d3dadb784c5b7ae544efed60398ead))
* **review:** steps (click, fill, press, wait, goto) in prepared pages, local addresses only ([5bb8ec1](https://github.com/sushidev-team/lola/commit/5bb8ec11e4596a5722aaac127c94292ecee46265))
* **review:** streamline the canvas and annotate live elements ([dbc4685](https://github.com/sushidev-team/lola/commit/dbc4685cb340aebafd44ee488430817491bcb439))


### Bug Fixes

* **desktop:** match preview origins by case-insensitive host and effective port ([f84eaa1](https://github.com/sushidev-team/lola/commit/f84eaa1f4b42d32c77734d7f8cb863df50215d01))
* **mobile:** drop duplicate port-setup shim entries left by two merged fixes ([e715345](https://github.com/sushidev-team/lola/commit/e715345dfbbd164a7a5d557cfa10950015e42f68))
* **preview:** canonicalize the origin before proxy selection ([33170c2](https://github.com/sushidev-team/lola/commit/33170c2c4bd5d8504fb931c49c43bd3702a64fcf))
* **review:** address PR review (resume after completed steps, superseded runs, contained step errors, capture wait-for and mark recheck, redirects, limiter, stack ask) ([102d6a3](https://github.com/sushidev-team/lola/commit/102d6a3c84ebc1de470a8873377a801ed5ac9f7d))
* **review:** keep Open live outside the clipped title, devstart needs dev_commands, recheck waitFor before the shot, sandbox dev guidance ([28c11ed](https://github.com/sushidev-team/lola/commit/28c11ed0cffe7db623564e02242fed0c4475b96d))
* **runtime:** briefing says a sandboxed session cannot run lola dev ([4fb3012](https://github.com/sushidev-team/lola/commit/4fb301281cb36db9c8582cea3e0d6418731fcb83))

## [0.15.0](https://github.com/sushidev-team/lola/compare/v0.14.0...v0.15.0) (2026-11-21)


### Features

* **hub:** agent-reported no_fix_available outcome holds an advisory for a week ([eb38c3d](https://github.com/sushidev-team/lola/commit/eb38c3d030830b7978d893b98feed99e0dd8fef4))
* **hub:** no fix available status for agents (SUSHI-810) ([e88a240](https://github.com/sushidev-team/lola/commit/e88a240e6a74135befd1363190b826ff8295e32e))


### Bug Fixes

* **mobile:** ended session screen fills the height ([03e4537](https://github.com/sushidev-team/lola/commit/03e45370cfd62b03667e3dfdcfd0211913ff2071))
* **mobile:** give the ended session view the full screen height ([0e6fca1](https://github.com/sushidev-team/lola/commit/0e6fca17c3b88233fb8a383789253a3322e58695))
* **mobile:** name a new branch and base when starting a session ([08b5bbf](https://github.com/sushidev-team/lola/commit/08b5bbf56a43e45bc3e689f17bc0ca95f9e1d176))
* **mobile:** name a new branch and base when starting a session (SUSHI-811) ([5a206b7](https://github.com/sushidev-team/lola/commit/5a206b7eff2debd259d2934d9129577e61039929))
* **mobile:** route the new port-setup methods as Mac-only in the shim ([9ebb19a](https://github.com/sushidev-team/lola/commit/9ebb19a50e969d8b7a8799add7e64ca0ebe260a2))
* **mobile:** shim the port-setup bindings and restyle the ended session footer ([813e471](https://github.com/sushidev-team/lola/commit/813e4715796e46c9ce7455551f0f31cd4a4744fb))

## [0.14.0](https://github.com/sushidev-team/lola/compare/v0.13.0...v0.14.0) (2026-11-20)


### Features

* **desktop:** embedded preview + element picker (SUSHI-627) ([109f461](https://github.com/sushidev-team/lola/commit/109f461529ffeb153295ed770d21087af6ba791c))
* **desktop:** preview the session's dev server and send a picked element to the agent ([d13d4c4](https://github.com/sushidev-team/lola/commit/d13d4c4c8cb5ef480b4e4205121391aef5949208))
* **desktop:** remove the project overview ([44d09fb](https://github.com/sushidev-team/lola/commit/44d09fb69ced4893377c0d356c4e83358791421a))
* **desktop:** suggest dynamic port setup and preview apps by session ([01c7d97](https://github.com/sushidev-team/lola/commit/01c7d97d77715b37168ceffda9f251ea1751e073))
* **mobile:** add a branch graph to the project workspace ([71239e2](https://github.com/sushidev-team/lola/commit/71239e273135e00f933d9ee46411d123c3247a17))
* **mobile:** redesign Sessions and Review and fix touch scrolling ([f9a6730](https://github.com/sushidev-team/lola/commit/f9a6730559e589938e6d1e3ffe4cde17a9329473))
* share sessions with the team (SUSHI-802) ([c6701eb](https://github.com/sushidev-team/lola/commit/c6701eba3124cffb185f350024b3b06f12d2e326))
* **Team:** one Macs list for teammates and sharing ([7f088f9](https://github.com/sushidev-team/lola/commit/7f088f9e0a55cf6a13223b735709df0e96f3d16b))


### Bug Fixes

* **daemon:** mark the phone rendezvous set dirty when the relay switch changes ([79cb7be](https://github.com/sushidev-team/lola/commit/79cb7be35c144d17bd902a272a2575cbc5e5e8c8))
* **desktop:** keep the selected Git project when returning to Code ([5299b8b](https://github.com/sushidev-team/lola/commit/5299b8b2f7d12427976193cb50f426c9cd2ba341))
* **desktop:** reveal clicked graph branches in the local sidebar ([cdfee52](https://github.com/sushidev-team/lola/commit/cdfee52c75d8b5bc1d5721e74fdc82ebf100c0b3))
* **desktop:** suppress manual stash warning during automatic pull recovery ([7e5f7d9](https://github.com/sushidev-team/lola/commit/7e5f7d9695750c070d36243df3cd89b25e102ef5))
* **Team:** keep pinned teammates on the Macs list when the Mac read fails ([e42cbf3](https://github.com/sushidev-team/lola/commit/e42cbf34ada52ad6580e9f23cc88f4eed49c154a))
* **worktree:** prevent advisory branches from accumulating ([11acaae](https://github.com/sushidev-team/lola/commit/11acaaedadbd77a3ed1bc22ab7417b1b7c985ee8))

## [0.13.0](https://github.com/sushidev-team/lola/compare/v0.12.0...v0.13.0) (2026-11-20)


### Features

* **codex:** answer the folder trust prompt for lola's worktrees ([a2b2963](https://github.com/sushidev-team/lola/commit/a2b29633c8aa8a960480cc5d5649557179fd607b))
* **codex:** send to Codex from the conversation ([ae38cc5](https://github.com/sushidev-team/lola/commit/ae38cc50c0846e22ea3113ad7e356a8f0b6e6b7d))
* **config:** allow * globs in label names, e.g. */agent-ready ([143d4a9](https://github.com/sushidev-team/lola/commit/143d4a9904e92ec4a0644c151f70d46f9490674d))
* **config:** resolve label names per team in [defaults]; allow typing names in settings ([fb00a2d](https://github.com/sushidev-team/lola/commit/fb00a2d5ebf8fae6d3393a83c0d2c7fa71a7ed22))
* **Conversation:** read Codex sessions into the conversation ([6c51295](https://github.com/sushidev-team/lola/commit/6c512953be1b4c647b5fdbbae88275e3035e78f1))
* **daemon:** acquire missing port leases and refresh port env when starting dev ([d128415](https://github.com/sushidev-team/lola/commit/d12841503b22efd2a05638e280b3cc0488208136))
* **daemon:** notify when the SSH key agent refuses to sign ([0ee8433](https://github.com/sushidev-team/lola/commit/0ee84336eded1be8cfc436761db21e721fc64d6b))
* define pickup labels globally by name (SUSHI-801) ([20b08af](https://github.com/sushidev-team/lola/commit/20b08afe0c471848b33f43271e9f83fb2eee1802))
* **desktop:** bulk pickup labels for selected projects, names optional ([5a166c9](https://github.com/sushidev-team/lola/commit/5a166c9e8e4b315383ee6fd4868b69e76135e178))
* **desktop:** drag & drop images onto the conversation view ([c40f986](https://github.com/sushidev-team/lola/commit/c40f9865e44ad889b61e04de9bee181180eb9426))
* **desktop:** drop images onto the conversation view to attach them ([98efdc5](https://github.com/sushidev-team/lola/commit/98efdc5ddbb24a925108c00e4644ec8f2256b16b))
* **desktop:** fan out sheets in the drop overlay and keep the border still ([16d04d3](https://github.com/sushidev-team/lola/commit/16d04d38a50c2859fbe08ef1f895a44d3a447128))
* **desktop:** resolve conflicted files in a merge editor and stage the result ([c378d9b](https://github.com/sushidev-team/lola/commit/c378d9b7f621cd30ff98132885d9472897c5fdf2))
* **desktop:** resolve conflicts in an aligned full-file merge editor with AI suggestions ([7c499e2](https://github.com/sushidev-team/lola/commit/7c499e2a56694180addcd3c51a2b721c87ebcb52))
* **desktop:** show a drop overlay over the whole conversation view ([ba52e97](https://github.com/sushidev-team/lola/commit/ba52e9776b609d6c9e795d1c3c49920c5b10cb9b))
* **desktop:** show added/removed line counts beside files in the discard dialog; lighter warning ([154ccfd](https://github.com/sushidev-team/lola/commit/154ccfd9ba3bf021e7b44d20c05ebf040d4050f3))
* **desktop:** suggest conflict resolutions with AI from the git helper ([ae6e1b0](https://github.com/sushidev-team/lola/commit/ae6e1b02d59971fe21ca5dd9196a15388583b362))
* **Git:** a text drag across diff lines selects those lines for staging ([64d8cba](https://github.com/sushidev-team/lola/commit/64d8cba76454a136ca57a76e75bd848bbad503e0))
* **Git:** ask a sandboxed session's agent to merge, and say why merge is disabled ([2409a72](https://github.com/sushidev-team/lola/commit/2409a72fe24700fdb0ad5b2ac44ddcf04480f9d5))
* **Git:** briefly dim the change reached with the previous/next buttons ([bc66312](https://github.com/sushidev-team/lola/commit/bc6631289b5075668d3f35313e1db27733f67893))
* **git:** carry local changes across branch switches with an automatic stash ([adbdad4](https://github.com/sushidev-team/lola/commit/adbdad4d9e4714f657a99e41b5d05fda7a83d3a0))
* **Git:** easier multi-line selection, no menu title, faster line staging ([812e5de](https://github.com/sushidev-team/lola/commit/812e5de9cdaf568bb538189f00e25c9eff109464))
* **Git:** open the diff large and stage or unstage single lines ([7f5dcae](https://github.com/sushidev-team/lola/commit/7f5dcae0730864c015fa91da5ebb0c57dc4e3aa2))
* **Git:** open the diff large and stage single lines (SUSHI-805) ([560e76d](https://github.com/sushidev-team/lola/commit/560e76d5a346911349b6190b82c6f9cf75b23e01))
* **Git:** previous/next change buttons in the file diff ([4033112](https://github.com/sushidev-team/lola/commit/403311294dbda90f575d00b291a7db06a48afd22))
* **Git:** report a created pull request as a toast and continue on GitHub ([ba7c290](https://github.com/sushidev-team/lola/commit/ba7c2902997b368f7ffa3a429fdcdb1790702d2d))
* **Git:** show the line button live while dragging over diff lines ([d57197f](https://github.com/sushidev-team/lola/commit/d57197feed6017add002d142c24eb618dddc417b))
* **Git:** show the whole file in the diff, with an optional changes-only fold ([dc3f2e5](https://github.com/sushidev-team/lola/commit/dc3f2e516181559bdcaad210d0c6bf718d686221))
* **git:** support large tracked files and review AI commit exclusions ([8caab02](https://github.com/sushidev-team/lola/commit/8caab027cb5fc9499f97f81dc0b226a0a304edb6))
* **mobile:** existing-branch sessions, remove Activity tab (SUSHI-803) ([7719acf](https://github.com/sushidev-team/lola/commit/7719acfed033b17e518be866679fb49ed75f2d8c))
* **review:** always-visible send, update review in header, compact right panel ([0b30ae8](https://github.com/sushidev-team/lola/commit/0b30ae89096dd6ed6ca9353d93bf50153d8c5aa0))
* **review:** confidence widget in the right panel, slim tests row, comment-free drawing tools ([90c6d39](https://github.com/sushidev-team/lola/commit/90c6d39e70f37cde6d502b0a344ec613fb99b216))
* **review:** name risk likelihood and impact in the badges ([dd2f6f8](https://github.com/sushidev-team/lola/commit/dd2f6f862302f258076044393edff61579817ecc))
* **review:** redesign the desktop review screen (SUSHI-800) ([0012fa2](https://github.com/sushidev-team/lola/commit/0012fa20e66fd04b218c3399a9d650c63341d5ea))
* **review:** resizable columns, screenshot title overlay instead of sidebar, compact risks panel ([519dcee](https://github.com/sushidev-team/lola/commit/519dcee85b74ef7d889a56f90998940249063127))
* **review:** single-row header, overlay zoom controls, icon comparison modes, merged tests section, sticky feedback ([6b5f046](https://github.com/sushidev-team/lola/commit/6b5f0460f9d6ea292d20cb9535421fb2b2bbd796))
* **review:** slimmer header: drop refresh (re-reads on focus), hide unavailable capture, labelled update review ([e11ef61](https://github.com/sushidev-team/lola/commit/e11ef61e018f3b7727d2f66a6d3b5c7d7774a822))
* **review:** styled, faster tooltips on risk badges ([787fab8](https://github.com/sushidev-team/lola/commit/787fab8e21d8a3624a29d2d43ced73660f55ad8a))
* **review:** update review/confidence actions in the widget, drop branch chip, fix boxed info panel ([ad130ca](https://github.com/sushidev-team/lola/commit/ad130ca947435b0310af1c307cc698cae2da0c25))
* **roster:** show a ticket as starting while its session is created ([52eebed](https://github.com/sushidev-team/lola/commit/52eebed92ae00bf5f04612dda9b11c131ee7941e))
* **stacks:** choose a stack session's repositories at start and add more later ([d6559c3](https://github.com/sushidev-team/lola/commit/d6559c341990af3872483a2b2e322af4766250d4))


### Bug Fixes

* **attention:** match key-agent refusals across wrapped rows; gofmt daemon.go ([0e46273](https://github.com/sushidev-team/lola/commit/0e46273ad434a975d8cfcfff1af29315e948b342))
* **codex:** make the folder trust override one codex actually reads ([a1b8335](https://github.com/sushidev-team/lola/commit/a1b83353721c1784d8d4815d3d463733cdee2618))
* **config:** address review of label-name resolution ([3011187](https://github.com/sushidev-team/lola/commit/30111877ff68ffdf3b2c70e04f230483af07592b))
* **daemon:** ignore a session's agent home inherited from the environment ([24e8204](https://github.com/sushidev-team/lola/commit/24e8204aef733f3b93595f03560aa28c43a53c57))
* **desktop:** attach several dropped images one after another ([f28e844](https://github.com/sushidev-team/lola/commit/f28e844fbb2fc06a59381c7fc283bbc55d408631))
* **desktop:** automatically merge diverged branches on pull ([21f7a9d](https://github.com/sushidev-team/lola/commit/21f7a9d7276c7f4f284f58a523945c5d2a35dfbc))
* **desktop:** declare the files-dropped event name ([5d82391](https://github.com/sushidev-team/lola/commit/5d82391930ca5415a1c353b2ac51d6b3ca030d23))
* **desktop:** handle unset default match labels in the name field ([ea2d935](https://github.com/sushidev-team/lola/commit/ea2d935249fecdcee9f01b302c74fbe824fbf8c8))
* **desktop:** import bulk pickup types from the internal protocol bindings ([effc47c](https://github.com/sushidev-team/lola/commit/effc47c4684e99330fa2a9884afefc7d598fe11e))
* **desktop:** open Git on the highlighted project, not the open session's ([c4d3192](https://github.com/sushidev-team/lola/commit/c4d3192781ece8d0364cafab2c6d72851c6b7abd))
* **desktop:** prevent premature quit during dev startup ([77d921c](https://github.com/sushidev-team/lola/commit/77d921c2134dbc012679f20e31b45ea5fd8da3ea))
* **desktop:** recover retained worktrees from ended sessions ([48c8af6](https://github.com/sushidev-team/lola/commit/48c8af6a6701d8062db9676d821f2d6332a672b0))
* **desktop:** remove all sessions sidebar button ([8712f29](https://github.com/sushidev-team/lola/commit/8712f29d5bd3de6a9f2aef622793fbff9c9eec56))
* **desktop:** reserve the leftmost graph lane for the default branch ([9d760af](https://github.com/sushidev-team/lola/commit/9d760af2f2b3b9b14a572d09dee5f31b32dde9eb))
* **desktop:** show the session's right sidebar in narrower columns and let ⌘N always open it ([cd3e434](https://github.com/sushidev-team/lola/commit/cd3e4348760e8a1b5878a33e12ec80d5912b4de9))
* **Git:** address review of the diff and confirm dialog ([9cdef59](https://github.com/sushidev-team/lola/commit/9cdef5906fb8dbe4337ac58b57bae8a643ec7e96))
* **git:** pop needs the discard tier, resolve the worktrees root symlink, fail on a broken git fixture ([1a9e004](https://github.com/sushidev-team/lola/commit/1a9e0046509329d42b8628d495c5a69205407d49))
* **git:** refuse branch checkout from a phone ([4bb2026](https://github.com/sushidev-team/lola/commit/4bb20267c92c17a6fb366f35ea7cbd460e3c5844))
* **Git:** renumber partial line patches and keep newer diff selections ([1e4d14e](https://github.com/sushidev-team/lola/commit/1e4d14eee9f6bd4b7f6842a58eef8f24d36fa13a))
* **git:** resolve native session worktrees in the sandbox check, make the lock test stage a real file ([6f4a3fa](https://github.com/sushidev-team/lola/commit/6f4a3fa41344434602a53c575071e5b13d035e3a))
* key-agent cue survives wraps, resets on death/scroll, covers goal sessions ([45b12f4](https://github.com/sushidev-team/lola/commit/45b12f4dfa859329bfb1fdfc692751b6058aaebc))
* **labels:** resolve inherited names and workspace labels ([9d3a9ae](https://github.com/sushidev-team/lola/commit/9d3a9ae01495508a5bae133008c59b878890db89))
* **mobile:** keep the plan sheet's empty label within the copy budget ([971d3d0](https://github.com/sushidev-team/lola/commit/971d3d00077b3196e807c02985091133831218b0))
* **mobile:** route main's new conflict-editor methods through the shim as Mac-only ([b4f3b79](https://github.com/sushidev-team/lola/commit/b4f3b79687da76b8623dd3f146e68e6e4a3fcb3d))
* **mobile:** route the new ChangeLines binding as Mac-only in the shim ([25d7351](https://github.com/sushidev-team/lola/commit/25d735160c8c2ea29ed4b80764ec3ef6460d24bb))
* **mobile:** shorten plan placeholder and cover DecidePlan in the placement table ([ac3fcab](https://github.com/sushidev-team/lola/commit/ac3fcab65f2a36773c6ef3b44395aca5e0ae8bca))
* **New session:** do not offer a sandboxed agent the main checkout ([dc94199](https://github.com/sushidev-team/lola/commit/dc9419903e786ef46a531122001cefad744b2789))
* **remote:** drop the projectGit audit fields; the contract allows only bools and enums ([0ee4154](https://github.com/sushidev-team/lola/commit/0ee41543bf80bb0de1e0bfd23ab49782647f2c19))
* **review:** CSS ring for confidence, one-line Tests row in the narrow panel ([dbe9ad8](https://github.com/sushidev-team/lola/commit/dbe9ad8f9ac74a907fb5b21162748f4f74c133d6))
* **review:** keep the feedback box and send button fully visible in the review panel ([5d204c5](https://github.com/sushidev-team/lola/commit/5d204c522239f8c39776ced58a046107c393c1e7))
* **review:** send pen stroke points, double-click saved drawings while a tool is armed, keep column widths inside the layout ([c608fd2](https://github.com/sushidev-team/lola/commit/c608fd2ec2bf426600b919e1157927afdd2c86ce))
* **review:** show outdated state once, in the header chip ([96f9207](https://github.com/sushidev-team/lola/commit/96f9207f5bc2eb7134048d00d5cd1bdd4faa6944))
* **Session menu:** drop Project settings while projects show beside sessions ([7702d87](https://github.com/sushidev-team/lola/commit/7702d877ae62cf8be482402e22603b3f4e1d90cc))
* **worktree:** remove a session whose worktree registration vanished but whose .git link stayed ([f5c14ba](https://github.com/sushidev-team/lola/commit/f5c14ba8d4cb9e2d4487a0070d25e5002fbc3416))

## [0.12.0](https://github.com/sushidev-team/lola/compare/v0.11.0...v0.12.0) (2026-11-17)


### Features

* **desktop:** let the projects column shrink beside sessions, keep a wider floor in Git mode ([6c0504d](https://github.com/sushidev-team/lola/commit/6c0504d58a72f11ee0c0fe6b54bb29e77939701e))
* **Git:** show active or default entries in view ([90a8ac0](https://github.com/sushidev-team/lola/commit/90a8ac0d41f64bcf048f833335ff7a323d9e4935))
* **Git:** undo the newest local commit and keep its changes staged ([b8d2741](https://github.com/sushidev-team/lola/commit/b8d27418eb45eb884c576a549ed012dde6f9fd8e))


### Bug Fixes

* **daemon:** browse the stack's Linear team when starting a ticket on a stack member ([5be47ec](https://github.com/sushidev-team/lola/commit/5be47ec53f56b24c4cbdc04846973ff9ef1b0a7b))
* **desktop:** remove failing CI count from the Projects list ([0ed524c](https://github.com/sushidev-team/lola/commit/0ed524c93c04d24c6f18eeb9b25687129d07fdac))

## [0.11.0](https://github.com/sushidev-team/lola/compare/v0.10.1...v0.11.0) (2026-11-16)


### Features

* **desktop:** add a right-sidebar toggle to the project toolbar ([e193775](https://github.com/sushidev-team/lola/commit/e1937756bdd60f2fd4561173f2f156c0a44a4689))
* **desktop:** add Project settings to the session context menu ([a67e203](https://github.com/sushidev-team/lola/commit/a67e2033bb825e29cce67a2777c44200cc2f4fb7))
* **desktop:** animate the sidebar's width and sessions column between Code and Git ([5a99f3c](https://github.com/sushidev-team/lola/commit/5a99f3c8d72637238ce1b878bc419c1312e5b69f))
* **desktop:** bulk actions for several selected projects ([5bd0016](https://github.com/sushidev-team/lola/commit/5bd0016dcb84e251ca44e152ec5b24e094209027))
* **desktop:** combine projects and sessions in one sidebar with a Code/Git switch ([e8ca668](https://github.com/sushidev-team/lola/commit/e8ca668d92dc02ed004aa99d1d78b03f78c61ea7))
* **desktop:** discard changes in a single unstaged file ([a0ef39b](https://github.com/sushidev-team/lola/commit/a0ef39be10eebea31dae134eec17c3909b33d7c2))
* **desktop:** discard changes in a single unstaged file ([898b7a7](https://github.com/sushidev-team/lola/commit/898b7a760907f8f0ab8a23356a2427e3a8bdea4d))
* **desktop:** filter buttons, Raycast-style command palette, hover-only row actions, narrow sidebar tabs ([16e4883](https://github.com/sushidev-team/lola/commit/16e4883f407dce36c20b90e576443fe7a8be9afc))
* **desktop:** filter sessions by several projects, matching any part of a session ([bba94e5](https://github.com/sushidev-team/lola/commit/bba94e56aad615fd622ffa8982ffbacf0ab1b451))
* **desktop:** Git mode shrinks the sidebar and shows a loading shell instantly ([873d88a](https://github.com/sushidev-team/lola/commit/873d88a70c21d05c2958bbdd0dcf54834633599a))
* **desktop:** hide sessions in Git mode and animate the Code/Git switch ([628b816](https://github.com/sushidev-team/lola/commit/628b816137176d31a825fb0bb25954d7826d9194))
* **desktop:** highlight changed characters in diff rows (SUSHI-786) ([29395aa](https://github.com/sushidev-team/lola/commit/29395aaca4ec668e370303da6121169594720075))
* **desktop:** highlight the changed characters within a modified diff line ([d403b19](https://github.com/sushidev-team/lola/commit/d403b194251fadbbba16d32027e9041f64159c74))
* **desktop:** keep Raw in the tab strip so it toggles both ways ([f61b59f](https://github.com/sushidev-team/lola/commit/f61b59f9ff74c02a465629b9b324d60bb19ae9ba))
* **desktop:** make the sidebar setup checklist dismissable and Linear-optional ([0b6c7df](https://github.com/sushidev-team/lola/commit/0b6c7df09cf347345d56303a8fdb1889e808a31c))
* **desktop:** move the agent Terminal into the view as a Raw toggle (SUSHI-787) ([630b2f9](https://github.com/sushidev-team/lola/commit/630b2f91ad741f747799dc4eae89a571d7621a0d))
* **desktop:** move the right sidebar to ⌘N and let ⌘B through open dialogs ([eb04ea9](https://github.com/sushidev-team/lola/commit/eb04ea9a15e8ef75188f890d5b72757074181d1c))
* **desktop:** offer a variable menu on the project form's worktree fields ([95da7e3](https://github.com/sushidev-team/lola/commit/95da7e3168d69973e14543546e56a21831345a9a))
* **desktop:** palette shows only common actions until you type ([dedafc2](https://github.com/sushidev-team/lola/commit/dedafc25a7378b19ce3c2432b20513673072222e))
* **desktop:** put Raw in the conversation header and open the agent terminal ([d11d415](https://github.com/sushidev-team/lola/commit/d11d4155e361d65aa6c3deeb25ef487ad0f871a5))
* **desktop:** Raw switch beside the activity bar ([525126b](https://github.com/sushidev-team/lola/commit/525126bc9c4ea9640cd02f060e25dd1bc4ee8cb5))
* **desktop:** Raw toggle beside the activity bar and over the agent terminal ([fd39e08](https://github.com/sushidev-team/lola/commit/fd39e083c84812a5f0646f7a4ea75546e1b05b73))
* **desktop:** resizable projects column ([5f5c8fb](https://github.com/sushidev-team/lola/commit/5f5c8fbbe64adfc965e7c8d9ad6eb4058631cfda))
* **desktop:** session filter popover, hover-only header buttons, compact search in Git ([3a14fc3](https://github.com/sushidev-team/lola/commit/3a14fc34c40790fc29d66b8c7fb1a771be0ca479))
* **desktop:** show a picker for an answer's options (SUSHI-788) ([8e81597](https://github.com/sushidev-team/lola/commit/8e8159772d4553e765e2665471cf5ccd6d3b2b60))
* **desktop:** show a picker under an answer's options ([3d523b9](https://github.com/sushidev-team/lola/commit/3d523b9df4b3597823ca6de718e7988a219aaaff))
* **desktop:** stash, pull & restore automatically when local changes block a pull ([d5c08c2](https://github.com/sushidev-team/lola/commit/d5c08c220326cf68af1c75e3308f4e3c9d4c4849))
* **desktop:** toggle the right sidebar with n and open a new session with c ([d5cd2ba](https://github.com/sushidev-team/lola/commit/d5cd2ba2402a7a75288c0d901d2c69cfdae51cce))
* **desktop:** wipe the sessions column in and out instead of fading it ([533f6d2](https://github.com/sushidev-team/lola/commit/533f6d2dbe0e353e9e3208e45af2f72c24c35058))
* **desktop:** workspace row with search, compact session filters, no Activity section, slower switch ([6648a1a](https://github.com/sushidev-team/lola/commit/6648a1a2f637dd7eb964ebb5d64fee95ba8299a6))
* **hub:** park advisories that no published version fixes yet ([9bc056a](https://github.com/sushidev-team/lola/commit/9bc056ac1c33b91d34f69e5848c3c55ec43f99fc))
* **ports:** automatic port allocation with busy-port memory ([9c4c5d6](https://github.com/sushidev-team/lola/commit/9c4c5d604171f187d642b1061a8122541072b03c))
* **stack:** add a project to a stack from its context menu ([be335f2](https://github.com/sushidev-team/lola/commit/be335f233639d302d4b1b2501969d712be8c4592))
* **stack:** add a project to a stack from the stack's context menu ([a484db4](https://github.com/sushidev-team/lola/commit/a484db4537aeea34a4aabefd807157d7c8b811bb))


### Bug Fixes

* **desktop:** address review of the combined sidebar and palette ([55af940](https://github.com/sushidev-team/lola/commit/55af94090010829ba3585cf624a3f7e41f89d42c))
* **desktop:** address review of the palette, switch, filters and bulk actions ([491d591](https://github.com/sushidev-team/lola/commit/491d591422d20fad1518ed9c049c443d9990d6fc))
* **desktop:** align the Projects and Sessions column headers on one line ([5a7c868](https://github.com/sushidev-team/lola/commit/5a7c8685bf51d8582ac12def529a9e2f80f15320))
* **desktop:** keep Conversation selected while a dev terminal is on show ([a501cf3](https://github.com/sushidev-team/lola/commit/a501cf34c3c407f9183ca099167c84e6fff7f3b3))
* **desktop:** keep emoji whole when highlighting changed characters ([88b3381](https://github.com/sushidev-team/lola/commit/88b338183e28cb4678683a67c0f6b41772a42774))
* **desktop:** keep Git mode when a stack or path-less project is selected ([cd2f16e](https://github.com/sushidev-team/lola/commit/cd2f16e813c1d1fbb8288a368033b07ff2246524))
* **desktop:** keep sidebar dialogs above the workspace; Focus sessions leaves Git mode ([9cec0e5](https://github.com/sushidev-team/lola/commit/9cec0e5066b4b43ac212356ee585b26e458f896e))
* **desktop:** keep the Git-mode sidebar wide enough for the logo and switch ([0226f4d](https://github.com/sushidev-team/lola/commit/0226f4d242ad2a51724a16b1e9fed3ea9bcf3b70))
* **desktop:** pair intraline highlights across a no-newline marker ([372faae](https://github.com/sushidev-team/lola/commit/372faaeb2a6ed56db2512ea0f2a4d115d9a96329))
* **desktop:** refuse a path-scoped discard with no paths ([c4be4f8](https://github.com/sushidev-team/lola/commit/c4be4f8ade87d90bb7e57538b77a935d09d4f828))
* **desktop:** room for the collapse button in Git mode; keep the switch animation off the main thread ([693df0a](https://github.com/sushidev-team/lola/commit/693df0a772dfb63072e72773da76ae140866febc))
* **desktop:** stop the Code/Git pill flickering while Git loads ([dbedf8f](https://github.com/sushidev-team/lola/commit/dbedf8fbae0407a54b2def17f54f89f3affafd89))
* **hub:** classify Linear refusal reasons instead of one generic error ([036c5d1](https://github.com/sushidev-team/lola/commit/036c5d16f3a4465d794aa740c6c96fc469ae6a6d))
* **mobile:** route the new per-file discard methods through the shim ([34f8d00](https://github.com/sushidev-team/lola/commit/34f8d0033163f71e45daef12b035432d7ce1a02d))


### Performance Improvements

* **desktop:** drop the All sessions row at once, title the Git shell, refresh Git in place ([083f8ac](https://github.com/sushidev-team/lola/commit/083f8acf81037df7eb5db61c32812be66dbd36db))
* **desktop:** keep the Git workspace mounted and drop the full-page skeleton ([d7623b6](https://github.com/sushidev-team/lola/commit/d7623b620032866f75bc9fd04a94cea4fcebf586))
* **desktop:** make the Code/Git switch answer instantly ([2958a96](https://github.com/sushidev-team/lola/commit/2958a965c62aef270f4aec77618bb260875b8a5c))
* **desktop:** open Git in the click's frame so the switch animation isn't stalled ([f2c8997](https://github.com/sushidev-team/lola/commit/f2c8997c8eed96f8b2f325c636254948a6aae601))
* **desktop:** show the sessions column at once, no content transition ([eecd396](https://github.com/sushidev-team/lola/commit/eecd3961ca8e5f60422b3318a60943c1d82c80fd))
* **desktop:** snap the sidebar width and fade the sessions column out sideways ([ebdd771](https://github.com/sushidev-team/lola/commit/ebdd7715dcbadad9d5e3cc878c40c3d248347941))

## [0.10.1](https://github.com/sushidev-team/lola/compare/v0.10.0...v0.10.1) (2026-11-15)


### Bug Fixes

* **daemon:** leave the connected relay when the last phone is revoked ([2a8a43e](https://github.com/sushidev-team/lola/commit/2a8a43ea5bd25ac2003352905c1a0ba91eba213c))

## [0.10.0](https://github.com/sushidev-team/lola/compare/v0.9.0...v0.10.0) (2026-11-15)


### Features

* **mobile:** full review on the phone, project grouping, relay fixes and companion glances ([c6b3e9e](https://github.com/sushidev-team/lola/commit/c6b3e9e7eec328d34eda16ffad509e738137f640))


### Bug Fixes

* **mobile:** clip the app frame and stop lists panning sideways ([7076b9b](https://github.com/sushidev-team/lola/commit/7076b9b2fa5ab61607a0402e5cb228b9d7fc67e1))

## [0.9.0](https://github.com/sushidev-team/lola/compare/v0.8.0...v0.9.0) (2026-11-15)


### Features

* **mobile:** rebuild the phone client on paired devices, server push and a hub relay ([7bf835a](https://github.com/sushidev-team/lola/commit/7bf835ab052402fb1366035c1ab91097ff488332))

## [0.8.0](https://github.com/sushidev-team/lola/compare/v0.7.0...v0.8.0) (2026-11-14)


### Features

* bulk-configure projects (SUSHI-783) ([11ada7d](https://github.com/sushidev-team/lola/commit/11ada7d358d8921766c6eaf0ab334f08ec03cc8a))
* **config:** let projects set commit message instructions ([a1faa3e](https://github.com/sushidev-team/lola/commit/a1faa3e40fe8cc04c8a85da4ccce903d860e5480))
* **desktop:** list open GitHub pull requests per branch ([888873b](https://github.com/sushidev-team/lola/commit/888873b14b7eaf43d76bf509311b763ee7f37edf))
* **desktop:** show combined branch badges and pull requests in the Git view ([3533d87](https://github.com/sushidev-team/lola/commit/3533d87edf1309af356b245803d436478dc79242))
* expand stack settings and clean up merged remote branches ([ffc541d](https://github.com/sushidev-team/lola/commit/ffc541d65ecafd4a3862b7c937a498a6812a1045))


### Bug Fixes

* **deps:** bump @capacitor/ios for GHSA-rvm3-566m-v7fv ([da331d9](https://github.com/sushidev-team/lola/commit/da331d93e38f4142377409b2878db620370415f1))
* **deps:** bump @capacitor/ios to 8.6.2 (GHSA-rvm3-566m-v7fv) ([46dc7fa](https://github.com/sushidev-team/lola/commit/46dc7fab797879f54865d58937093db681a6831c))
* **deps:** resolve tinypool and @capacitor/ios advisories ([e91f26f](https://github.com/sushidev-team/lola/commit/e91f26f7920f7ec068f002979771a3b67d8a09cd))
* **mobile:** pin capacitor-swift-pm to 8.6.2 in CapApp-SPM ([c42f00d](https://github.com/sushidev-team/lola/commit/c42f00d2a19bbfda5265b865ffe214938477a351))

## [0.7.0](https://github.com/sushidev-team/lola/compare/v0.6.0...v0.7.0) (2026-11-14)


### Features

* **desktop:** open sessions from Git context menus ([b107fb9](https://github.com/sushidev-team/lola/commit/b107fb9e80f2038f440bdffdf339f92679e7f260))
* **desktop:** show work in progress of other worktrees in the Git graph ([e2ff2ad](https://github.com/sushidev-team/lola/commit/e2ff2ad0c443ea8443d46f02c3d56028a96c686a))
* **hub:** add team creation and team-admin controls ([4059746](https://github.com/sushidev-team/lola/commit/405974614bed56e4c936afa94bcdc09c07c32f84))
* **hub:** let teams be created on hubs that accept no code ([038d2b9](https://github.com/sushidev-team/lola/commit/038d2b95f1c71b33f0addcde936e88e42c83a7ad))
* **hub:** move the team hub into this repository as hub/ ([179b1a2](https://github.com/sushidev-team/lola/commit/179b1a27c5371ba46f6cda8f11557aaf70d2ffa5))
* **hub:** round out the chore lifecycle, team view and onboarding ([8a3e771](https://github.com/sushidev-team/lola/commit/8a3e771bf9e4eca56de30009a36f9d2f9d1df61a))


### Bug Fixes

* **config:** allow inactive pickup drafts without blocking settings ([d84a227](https://github.com/sushidev-team/lola/commit/d84a227c2970c85ed9d33e9f5ad42978d9efe0a8))
* **deps:** upgrade vitest and uuid for security advisories ([635fb24](https://github.com/sushidev-team/lola/commit/635fb2499d53384a2efdf7dec02158b99bf0a7f7))
* **deps:** upgrade vitest and uuid for security advisories ([a8276a1](https://github.com/sushidev-team/lola/commit/a8276a13d8c7727bfac1eba04c016a8c8211e3a9))
* **desktop:** isolate updater mounts and clean failed installs ([b7c6960](https://github.com/sushidev-team/lola/commit/b7c696080a6ee5b5d4bb37b14c24bf0ef5e25829))
* **desktop:** refresh worktree commits and label fast-forward merges ([c286391](https://github.com/sushidev-team/lola/commit/c2863911683a9708366ecfe9d04e4a95678bfc6f))

## [0.6.0](https://github.com/sushidev-team/lola/compare/v0.5.0...v0.6.0) (2026-11-13)


### Features

* **ai:** add per-provider model and effort profiles ([9dd0f93](https://github.com/sushidev-team/lola/commit/9dd0f93dd551792456139cd05b668e3b6aa9ffe8))
* **desktop:** refine Git workspace navigation and branch actions ([ef9cf51](https://github.com/sushidev-team/lola/commit/ef9cf5159344e4d5068ab6f0a4f3ef1aeaafe967))


### Bug Fixes

* **deps:** bump devalue, vite and source-map-js to patched versions ([35b5bf5](https://github.com/sushidev-team/lola/commit/35b5bf51cf39819ed02aae9d1013c39bff8199f5))
* **deps:** patch devalue, vite, source-map-js advisories ([435809a](https://github.com/sushidev-team/lola/commit/435809a83c2fdcc65ec52939272c433b1dd56167))
* **desktop:** explain Git failures with recovery actions ([5610dc6](https://github.com/sushidev-team/lola/commit/5610dc6904696fe90488a2f18ae6ed73735bca92))
* **desktop:** show success toasts for Git actions ([c6e9700](https://github.com/sushidev-team/lola/commit/c6e97006a368a7aba2e672d61311b2db938fa271))
* **desktop:** simplify Git deletion dialogs ([7d43792](https://github.com/sushidev-team/lola/commit/7d43792c5d1487fef83c4bf8bb77c77b5f88de37))

## [0.5.0](https://github.com/sushidev-team/lola/compare/v0.4.0...v0.5.0) (2026-11-12)


### Features

* add configurable automatic PR comment resolution ([44205f8](https://github.com/sushidev-team/lola/commit/44205f8b8b8c4ceef12ce918be6cea0c5d9b67d3))
* base for git editor ([275608e](https://github.com/sushidev-team/lola/commit/275608e35491e49150266ed5911732ee2d4ece69))
* **conversation:** background running Claude commands from cards ([f6029cd](https://github.com/sushidev-team/lola/commit/f6029cd078117e69588c265064861c1910f2523d))
* **desktop:** add AI commit draft and fetch-remote state to Git view ([8a7733d](https://github.com/sushidev-team/lola/commit/8a7733d340485083712bd2a98045700e8c41e0b3))
* **desktop:** add AI commit drafts and safe discard for unstaged changes ([33cdc05](https://github.com/sushidev-team/lola/commit/33cdc054132391c0b9c1f3ae8ce4313115006c5e))
* **desktop:** add conversation model and settings selector ([f77c893](https://github.com/sushidev-team/lola/commit/f77c893f272af265ad0088f23df761a1ffe8faa3))
* **desktop:** add drag-to-PR and metadata picker to Git view ([ed9f494](https://github.com/sushidev-team/lola/commit/ed9f49416ea97c14038c18de4cca9ab850ae3f46))
* **desktop:** add Git view actions, PR creation, and AI commit drafts ([9360046](https://github.com/sushidev-team/lola/commit/93600465fd780f5003cdcd5a08f60ec2dfe28b3c))
* **desktop:** add pull request creation from Git view ([a377276](https://github.com/sushidev-team/lola/commit/a377276131d62f889cf6c493e78068d0744a205b))
* **desktop:** expose session context for time tracking ([5070657](https://github.com/sushidev-team/lola/commit/50706576d2bf6b5e3661542c7e183e13ae65a14b))
* **desktop:** publish branches and extend Git workspace actions ([1437082](https://github.com/sushidev-team/lola/commit/143708256b1f61d435cb171cbb088cc9139bd1bd))
* **desktop:** simplify settings and add native path browsing ([a81b742](https://github.com/sushidev-team/lola/commit/a81b742e36a7601e48278f8b09409315d2f61436))
* **notify:** add focused macOS alerts for input and ready PRs ([1843780](https://github.com/sushidev-team/lola/commit/18437802fb181ffe6702db6f3f4e5b1b3ffcc747))
* **notify:** add focused macOS alerts for input and ready PRs ([d8f9f78](https://github.com/sushidev-team/lola/commit/d8f9f784f4a8e0cacd7c280b681aab56a6ea4c0c))
* **sidebar:** add session background context menu ([b55610d](https://github.com/sushidev-team/lola/commit/b55610d79c363e348164924158ec2ba86c273a10))
* **stack:** create a stack from a parent folder ([1743e33](https://github.com/sushidev-team/lola/commit/1743e33a88cdfcba39e07b0532c6aade9a2a0133))
* **stack:** create a stack from a parent folder ([3c353d5](https://github.com/sushidev-team/lola/commit/3c353d5db10fdf413b06c9bc53f75b04382fc938))
* **stack:** pick each member's labels on the stack's Members tab ([b826870](https://github.com/sushidev-team/lola/commit/b8268704a0d051bd5e64b87784ee892ff3399c1b))
* **stack:** remove a project or a whole stack from the sidebar ([bca0f4d](https://github.com/sushidev-team/lola/commit/bca0f4d17763e21116046323598ac54607368a7a))
* **ui:** animate current tool output previews ([7f412a7](https://github.com/sushidev-team/lola/commit/7f412a702959416f4ad54f4f4347a27de97d4ee0))


### Bug Fixes

* compact task checks and simplify review summaries ([fba17af](https://github.com/sushidev-team/lola/commit/fba17af76da386558eb19673ac95a9d820dced3d))
* **desktop:** close active tab with Cmd-W ([e2022b3](https://github.com/sushidev-team/lola/commit/e2022b3189db65efd391b1f58113155d2b800b1f))
* **desktop:** complete Git action menus and working-copy cues ([34eb83a](https://github.com/sushidev-team/lola/commit/34eb83addc30678fa96d4482888235d3e54ce502))
* **desktop:** improve project context menus and resize layering ([f447cdf](https://github.com/sushidev-team/lola/commit/f447cdfe23665f13a8fc148ec24eb5b9b995abd3))
* **desktop:** move compact review button into PR status header ([13d58b0](https://github.com/sushidev-team/lola/commit/13d58b04abd3a571b2768f6e0cea354c27a4358f))
* **desktop:** remove duplicate project menu divider ([73e6384](https://github.com/sushidev-team/lola/commit/73e63845bb4af8dd17e34a3191fa3ef5b8b23844))
* **desktop:** reveal remote fetch on hover ([1262e2f](https://github.com/sushidev-team/lola/commit/1262e2f128464c79d6eb0813cd66d604da9e589d))
* **dispatch:** enforce concurrency_cap on running sessions and close the cross-poll race ([80b6e69](https://github.com/sushidev-team/lola/commit/80b6e69ab7bf71be0367d55df55c9e22fef8f880))
* keep permission prompts stable during countdowns ([38870ef](https://github.com/sushidev-team/lola/commit/38870efbd6254678248aa5c5521ed009abf0df3d))
* **stack:** address review on stack removal, label save and the mobile shim ([4595c36](https://github.com/sushidev-team/lola/commit/4595c3604bfc56c786d458d915f4e2d90bc05cd9))
* **stack:** match folder apps to projects by resolved path ([d2ce63a](https://github.com/sushidev-team/lola/commit/d2ce63a55d8c44d153b3656cdc71c74308d3c42f))
* **stack:** set the folder dialog's hints at the metadata size ([460fbb2](https://github.com/sushidev-team/lola/commit/460fbb27a513f4cbfccc1023a13c6dd0d512a286))
* **stack:** show the Members tab after Linear updates ([96e648d](https://github.com/sushidev-team/lola/commit/96e648d702de13699c02c9d8492d02dbab19be6a))

## [0.4.0](https://github.com/sushidev-team/lola/compare/v0.3.0...v0.4.0) (2026-11-09)


### Features

* **theme:** show compact palettes in system appearance pickers ([58079e9](https://github.com/sushidev-team/lola/commit/58079e9cb8a199a5073ebdffd11902e8d5c9eb0a))


### Bug Fixes

* loading icon animation jiggerish ([1640679](https://github.com/sushidev-team/lola/commit/1640679733fce2361d05e36b3f1134d831056de6))
* **tasks:** render feedback as a note instead of an empty disclosure ([f69eae8](https://github.com/sushidev-team/lola/commit/f69eae87ab4d06941a42fc91afa0ec0c9e358407))
* **tasks:** silence confirmations and flatten redundant issue groups ([2079024](https://github.com/sushidev-team/lola/commit/2079024ebf86c53ae678db080e07640b631a42ed))

## [0.3.0](https://github.com/sushidev-team/lola/compare/v0.2.20...v0.3.0) (2026-11-09)


### Features

* add system appearance and clarify task checks ([5eb5767](https://github.com/sushidev-team/lola/commit/5eb57679c43588dda0e28279a476e23031b9c941))
* improve follow-up sessions and PR review workflow ([d50f5ce](https://github.com/sushidev-team/lola/commit/d50f5cee43686043982b7c92fe1c2c9a524f4a85))
* improve review capture and session workflows ([a86c95f](https://github.com/sushidev-team/lola/commit/a86c95fd4984e13b5b87575aa796f2a5435129c4))
* **review:** add anchored preview zoom and vertical screenshot navigation ([81e34dc](https://github.com/sushidev-team/lola/commit/81e34dc86b8690f1763fc8b696902d9b78d1332a))
* **review:** add revision verification, browser capture and automation ([f8b233b](https://github.com/sushidev-team/lola/commit/f8b233b606799528bcbb0540d56c53e3860e050f))
* **review:** combine confidence signals and refine result layout ([80fec70](https://github.com/sushidev-team/lola/commit/80fec70e83fb4efc164952103c288502988a249c))
* **theme:** harmonize palettes and default to Midnight ([c80a45e](https://github.com/sushidev-team/lola/commit/c80a45ed4215d4769df12cf5500daf857ff0612c))


### Bug Fixes

* **desktop:** maximize window on title-bar double-click ([23da7c2](https://github.com/sushidev-team/lola/commit/23da7c259fe89490a8311dc3b824581bd92bab72))
* **release:** bump minor version for pre-1.0 features ([7f5b6fa](https://github.com/sushidev-team/lola/commit/7f5b6fa7bf046bd824a8d2a6664f13a78b0bbd1c))


### Performance Improvements

* **review:** load material progressively with skeleton placeholders ([faf7f1f](https://github.com/sushidev-team/lola/commit/faf7f1f915aaebbf387dee83b30b4837bd3377b4))

## [0.2.20](https://github.com/sushidev-team/lola/compare/v0.2.19...v0.2.20) (2026-11-08)


### Features

* add agent-assisted SSH sessions ([17ff17b](https://github.com/sushidev-team/lola/commit/17ff17b1e5fa3f3840254e4a5b0dab64fb8f81b4))
* add named sessions and project command navigation ([211603e](https://github.com/sushidev-team/lola/commit/211603e83df29f7271b48ea64883f9e6c93fef95))
* add retained PR review packages and desktop workspace ([4414bd9](https://github.com/sushidev-team/lola/commit/4414bd9da091cea6acd4d27227d307b8d40c1afd))
* add screenshot review keyboard shortcuts ([0eec459](https://github.com/sushidev-team/lola/commit/0eec4598337236d52b3be1fa9f7670fd79535951))
* add session activity minimap and Midnight theme ([6d6edc2](https://github.com/sushidev-team/lola/commit/6d6edc2cc7839ec1cdfb5a496d269b5550251fae))
* authorize autonomous goals from the TUI ([db24354](https://github.com/sushidev-team/lola/commit/db24354b048a6df110d0a21457dc7c5c3de108bb))
* checkpoint self-improving workspaces and autonomous goals ([c830152](https://github.com/sushidev-team/lola/commit/c83015259944d8d0aacbaa6cb5588bd462f96561))
* complete workspace goal controls and retain pilot evidence ([772dafc](https://github.com/sushidev-team/lola/commit/772dafce248316e2795978ce9d6f0673a26dbf17))
* configure specialist review profiles in the TUI ([2f524fe](https://github.com/sushidev-team/lola/commit/2f524fea6e9649fcb066b9a4d355f956011e1a87))
* consolidate session navigation in resizable sidebar ([f8db843](https://github.com/sushidev-team/lola/commit/f8db843c1509059ee9c8bd9119d66b06f2af2d41))
* delete selected review comments with command backspace ([89fc7d8](https://github.com/sushidev-team/lola/commit/89fc7d8d681169fd5915d39b8f1bf2e70b6e5575))
* **desktop:** add PR merge controls with method selection ([32323cb](https://github.com/sushidev-team/lola/commit/32323cbb51ec0f0c1b934f671a4bdb56a13891b1))
* **desktop:** improve agent messaging and session activity ([a904b6f](https://github.com/sushidev-team/lola/commit/a904b6f8321324de6531caafc512c9dd6c50e694))
* **desktop:** make the session view the one way to speak to agents ([27df1e5](https://github.com/sushidev-team/lola/commit/27df1e5396e562306c90ec2d9bf363bfe03fb413))
* **desktop:** open session folders in Finder ([f75fbec](https://github.com/sushidev-team/lola/commit/f75fbecf6ccb8a170dfe23d246f982ab81072ad3))
* **desktop:** refine agent conversation rendering and colors ([1f13bff](https://github.com/sushidev-team/lola/commit/1f13bff9da3d4f0271852cae89c9c43584bddd9a))
* edit review comments directly on screenshots ([3925c79](https://github.com/sushidev-team/lola/commit/3925c79b03c9b4b507b01630614b0c484fb58866))
* expose specialist reviews and goal authoring in desktop ([ed2770e](https://github.com/sushidev-team/lola/commit/ed2770e0ce17fa888bc969f27efbdb0bd13cb81e))
* make PR review an interactive screenshot workspace ([c996ffd](https://github.com/sushidev-team/lola/commit/c996ffd7220092712f3fd1faf3008b14cb718659))
* plan and publish reviewed goals from the TUI ([6c0135e](https://github.com/sushidev-team/lola/commit/6c0135ef3be2970fe48bdf124919ff980d1a47a1))
* preserve specialist model and prompt provenance ([20f89dc](https://github.com/sushidev-team/lola/commit/20f89dc0e85c1a10a02273cda597499c3f8e1c6c))
* promote verified review lessons against accepted code ([5261611](https://github.com/sushidev-team/lola/commit/5261611db100cd543ebc7636067898260edc3af4))
* propose prevention work from recurring workspace lessons ([e777089](https://github.com/sushidev-team/lola/commit/e777089c8135f6bc95a7ce292e9e27950c1fdde3))
* redesign PR overview around features and review evidence ([26640a7](https://github.com/sushidev-team/lola/commit/26640a7798c52e6f3a8181bf55fbf695c5899a5b))
* refine conversation controls and conflict recovery ([4f47dca](https://github.com/sushidev-team/lola/commit/4f47dcacabe4cb55597b8ee3831fc8c7966efa97))
* refine sidebar hierarchy and restore header quotas ([cd74172](https://github.com/sushidev-team/lola/commit/cd7417291ce66629b706dfc592e1dae7e77a9b6b))
* revalidate workspace lessons against committed code ([eef8d84](https://github.com/sushidev-team/lola/commit/eef8d84949d4aefd66311251535a6066f8d6cf04))
* **review:** refine PR workspace and disclosure hierarchy ([9ce3448](https://github.com/sushidev-team/lola/commit/9ce3448a4ee447d9655d65c1e247f48b2a109b70))
* route specialist findings and record scoped worker disputes ([5532a74](https://github.com/sushidev-team/lola/commit/5532a740d75f063e42d7edd469aba1dd00552bdb))
* **runtime:** pre-accept Claude Code workspace trust for lola worktrees ([4aeb4d0](https://github.com/sushidev-team/lola/commit/4aeb4d009caceefadf4e0c8c2bead084fcf6615e))
* **sessions:** select existing branches or create a new one ([8ded0ab](https://github.com/sushidev-team/lola/commit/8ded0ab346afe5f3fd40afd8d9862e7608d9416c))
* simplify onboarding and prevent Keychain startup hangs ([0179dff](https://github.com/sushidev-team/lola/commit/0179dff21bfb5db3eb942b2e721a0ecca57dee22))
* stop a session's agent while keeping its worktree ([5531496](https://github.com/sushidev-team/lola/commit/55314967de50144d85efb0967fe2b5894b1c186c))
* **tasks:** add manual checks and task feedback ([74b65d0](https://github.com/sushidev-team/lola/commit/74b65d0079789abb87c6a880ab8132a9170a7a64))
* **ui:** refine Lola workspace and session review panels ([7386dd4](https://github.com/sushidev-team/lola/commit/7386dd42210cd57da0ec358e9dfc3cfb99c51513))
* unify PR review overview and prioritize risks ([9e7ed8e](https://github.com/sushidev-team/lola/commit/9e7ed8ef84791aaed78c6a46e5ac67fe7da014d5))


### Bug Fixes

* **attention:** treat Claude Code's dim prompt suggestion as an empty composer ([c727c36](https://github.com/sushidev-team/lola/commit/c727c36bc5bedb19971d7825b773c703d20e3250))
* better orchestration controls w/ session merge conflicts etc. ([a49e07f](https://github.com/sushidev-team/lola/commit/a49e07f5110a5b4320598c066ad6035df2578566))
* confirm comment edits and preserve complete review feedback ([99232e6](https://github.com/sushidev-team/lola/commit/99232e698c29742bfa7ca7525059952d4ecc5be8))
* correct temporary session creation and exit behavior ([d3b6d10](https://github.com/sushidev-team/lola/commit/d3b6d10c9276d3f02d53e2a51fe1ebe032bfd9c0))
* **desktop:** check for updates on startup and periodically ([9b42b57](https://github.com/sushidev-team/lola/commit/9b42b57baf1eadd09fd0a61432ced2e0d69214b3))
* **desktop:** distinguish merged sessions with a green done icon ([e17af7e](https://github.com/sushidev-team/lola/commit/e17af7e4d95e00fed5ce17e5acee61a25608af59))
* **desktop:** remember chosen merge method ([9c4a3b7](https://github.com/sushidev-team/lola/commit/9c4a3b70656e107bab4948a0b9316f1d1bec7096))
* **desktop:** remove standalone summary from overview ([12d0a6a](https://github.com/sushidev-team/lola/commit/12d0a6a27a6ab9e167a00ffee4063d176bc8cfe8))
* **desktop:** stabilize conversation activity and attachment editing ([66119bf](https://github.com/sushidev-team/lola/commit/66119bfe5fe96ddefcc7f9aaa0ae707827168bb7))
* **desktop:** stop leaking tmux attach clients across web-view reloads ([71306f0](https://github.com/sushidev-team/lola/commit/71306f09e1c30893e5f556a0e816fbc2faad8e8c))
* **goals:** authorize bounded acceptance-check memory ([468bd03](https://github.com/sushidev-team/lola/commit/468bd0310d670f3dca87bd03fa3f3596f48ff752))
* periodic updates ([1087386](https://github.com/sushidev-team/lola/commit/108738663de70c42d898b9030b2615f3b39e4818))
* prevent stale session recovery after cleanup ([bbd2adc](https://github.com/sushidev-team/lola/commit/bbd2adcc9cff9b5372bc9f5bdad4432ef4596734))
* retain screenshot annotations after saving feedback ([545a3ca](https://github.com/sushidev-team/lola/commit/545a3cac48f033e67e4cda7334c22d43f176001e))
* save and dismiss screenshot comments on click-away ([d7add57](https://github.com/sushidev-team/lola/commit/d7add57ef917ce52b7c21673c5c15fafffaf110d))
* simplify screenshot comment editor and feedback recovery ([2b70dd3](https://github.com/sushidev-team/lola/commit/2b70dd39b0d1944978324e37f7a6c6e096d2ff7b))

## [0.2.19](https://github.com/sushidev-team/lola/compare/v0.2.18...v0.2.19) (2026-10-20)


### Features

* **checkpoints:** describe changes beside turn numbers ([a63dd65](https://github.com/sushidev-team/lola/commit/a63dd65fbdaf525dcb13770c13dfeaa746dfc7ad))
* **daemon:** sandbox hook bridge + linked-repo PR tracking ([6062e45](https://github.com/sushidev-team/lola/commit/6062e45a96d17b014c013abee52e4f445404d956))
* **mobile:** select paired devices for wireless launches ([b79ec24](https://github.com/sushidev-team/lola/commit/b79ec248b26468c40740867a91e3f57cea27c9e3))
* optional docker sandbox + stacks (multi-repo projects) ([fd57685](https://github.com/sushidev-team/lola/commit/fd5768599629b3f14a65fe61bccd64a2ad11d206))
* pick linked repos per issue by label ([610e93b](https://github.com/sushidev-team/lola/commit/610e93b0e722688ef25093609d9aa512ef28f2df))
* **review:** publish guarded review statuses and honor required checks ([d260211](https://github.com/sushidev-team/lola/commit/d260211e3d6b556888b8798b3e9312e0e770b469))
* **runtime:** docker sandbox launch + linked-repo worktrees ([d64549f](https://github.com/sushidev-team/lola/commit/d64549f926e3e461d1fd0dea39f7370016cdc9d5))
* stacks — one issue, several repositories, no main repo ([0a9fd98](https://github.com/sushidev-team/lola/commit/0a9fd98f3cd9c61d3e17940fa380cd3ebf4a1db6))
* **ui:** show sandbox + linked-repo PRs in TUI and app ([30e847f](https://github.com/sushidev-team/lola/commit/30e847f85d58ce42e023e2c412066aebe010c600))


### Bug Fixes

* **daemon:** refine dev URLs throughout startup ([cec11c5](https://github.com/sushidev-team/lola/commit/cec11c5e409aaa1096cfdd13fd0d7b772dccf02e))
* **desktop:** draw a readable stack icon in the sidebar ([e566ec2](https://github.com/sushidev-team/lola/commit/e566ec2c41172a4204cbc8b972efe2cfdd7db968))
* **runtime:** derive sandbox mounts only from paths the agent cannot write ([363de68](https://github.com/sushidev-team/lola/commit/363de68ed5637e81b36de8ff8f84bd213623ac6d))
* **sandbox:** mount each worktree's .git pointer read-only in the container ([0b673b9](https://github.com/sushidev-team/lola/commit/0b673b975e79fe98dbbb5d68761fb95bb7db1463))
* **sandbox:** pin host git, explicit project, safe linked rollback/teardown ([cd898c7](https://github.com/sushidev-team/lola/commit/cd898c7ffe1edff627bb119ede38340df6cd88d5))
* **sandbox:** skip pre_remove for sandboxed sessions ([96e6522](https://github.com/sushidev-team/lola/commit/96e65226fd93dcd90b7d8f5fd947a92b8638e85d))

## [0.2.18](https://github.com/sushidev-team/lola/compare/v0.2.17...v0.2.18) (2026-10-16)


### Features

* per-session port leases, parallel dev stacks and a pre_remove hook ([d55524a](https://github.com/sushidev-team/lola/commit/d55524a20e5b3d43cb35dd57112597584a5d1233))


### Bug Fixes

* **runtime:** keep LOLA_DB_NAME injective and keep the held lease on a failed re-lease ([6e8db0c](https://github.com/sushidev-team/lola/commit/6e8db0c1b0a3f24ea44fa5e3396e3b21396c725c))
* **worktree:** keep pre_remove from wedging or outliving teardown ([f9edd93](https://github.com/sushidev-team/lola/commit/f9edd9347e32a4c3c8eb2e4bb56272401cad3bc8))

## [0.2.17](https://github.com/sushidev-team/lola/compare/v0.2.16...v0.2.17) (2026-10-12)


### Features

* **desktop:** polish diff viewer and align file navigation order ([311f163](https://github.com/sushidev-team/lola/commit/311f16360c7be078aff5a9d7f7d5128a85cd987d))
* **desktop:** synchronize diff scrolling and file navigation ([6295f97](https://github.com/sushidev-team/lola/commit/6295f970160685a12ff6951928a7c866c29d491f))


### Bug Fixes

* **desktop:** refine session attention indicator ([ff08ee1](https://github.com/sushidev-team/lola/commit/ff08ee1ab08407849a1083c34ae16d04c8b3b088))
* **runtime:** grant Claude access to session context folders ([82046d7](https://github.com/sushidev-team/lola/commit/82046d79faa3d731c5972300929a6d9dae5e3689))

## [0.2.16](https://github.com/sushidev-team/lola/compare/v0.2.15...v0.2.16) (2026-10-11)


### Features

* **bestof:** run several agents/models on one issue and keep a winner ([fd6cd8c](https://github.com/sushidev-team/lola/commit/fd6cd8c9f0458ed071352ef91f962e8e117a4c3d))
* **bestof:** run several agents/models on one issue and keep a winner ([ea14c4c](https://github.com/sushidev-team/lola/commit/ea14c4cdd21332df4a0a99d4000e3320aae27dbf))
* **broadcast:** send one message to every live agent of a project ([fa94fb8](https://github.com/sushidev-team/lola/commit/fa94fb852407017e0212895a6002983ad867d5ca))


### Bug Fixes

* **bestof:** address review — serialize picks, re-check cap under the claim, surface partial failures ([9d6b4f0](https://github.com/sushidev-team/lola/commit/9d6b4f0d4de0d46700dca2c32e83e5e5410de07d))
* **broadcast:** bound the immediate delivery pass so a large project cannot outlast the client ([18a9e16](https://github.com/sushidev-team/lola/commit/18a9e16b6733837cf093919f07790eacfbfd9c9f))
* **daemon:** defer automatic prompts while human input is present ([030e942](https://github.com/sushidev-team/lola/commit/030e94287e96edfcc8000dbed798f0cfedea28b3))
* **desktop:** simplify session overlap indicators ([21eca74](https://github.com/sushidev-team/lola/commit/21eca7470d274e177bdacf6f70aa1efcce734220))
* **quota:** poll Claude usage when status-line data is missing ([4567139](https://github.com/sushidev-team/lola/commit/456713984b594513aaf7e3d815913eaa14b9d00a))
* **ui:** use consistent fluid timestamps across the app ([4cf5ed5](https://github.com/sushidev-team/lola/commit/4cf5ed51d5f0f788f671d76fc3a4e79c9d63edd2))

## [0.2.15](https://github.com/sushidev-team/lola/compare/v0.2.14...v0.2.15) (2026-10-11)


### Features

* **desktop:** always offer the session sidebar; icon view controls ([60a5978](https://github.com/sushidev-team/lola/commit/60a5978d4c789af0aec9742b914298cd0c336e71))
* **desktop:** fluid relative times with the exact time on hover ([2d88bb3](https://github.com/sushidev-team/lola/commit/2d88bb39c8d9d80191fce78d1d9a03cd7a752490))


### Bug Fixes

* **runtime:** never wire sessions to a deleted lola binary ([210cabd](https://github.com/sushidev-team/lola/commit/210cabd65aaaad7dbd22e99597871c63c734d259))

## [0.2.14](https://github.com/sushidev-team/lola/compare/v0.2.13...v0.2.14) (2026-10-10)


### Features

* agent self-reports (lola report) and stale Linear filter detection ([fd62921](https://github.com/sushidev-team/lola/commit/fd62921620b5d669456b8b3753888872c49e1f24))
* audit agent check claims against transcript evidence ([a4a756d](https://github.com/sushidev-team/lola/commit/a4a756d4a042988e432cc97ce6101f320b91c97d))
* audit agent check claims against transcript evidence ([7492fff](https://github.com/sushidev-team/lola/commit/7492fff750d3f63d14c7df8015ba2410b9f92c37))
* cost tracking, daily budgets and load-aware dispatch ([fea2a2e](https://github.com/sushidev-team/lola/commit/fea2a2e81f5f1789c1ef962014f0f40fb98260c7))
* count codex sessions' tokens too ([17598c6](https://github.com/sushidev-team/lola/commit/17598c6112be8be43ec57952d8c94533c41171f0))
* count opencode tokens; styled hover cards for usage ([ebeb09b](https://github.com/sushidev-team/lola/commit/ebeb09b48cde26623b074da2604c39fb21de9d70))
* cross-session overlap warnings and opt-in local merge queue ([21e03aa](https://github.com/sushidev-team/lola/commit/21e03aa325a1950ee571aa048164aed8951eb8f6))
* cross-session overlap warnings and opt-in local merge queue ([ec35d02](https://github.com/sushidev-team/lola/commit/ec35d02e98503a9653a2b0f7fe817945480964d9))
* **daemon:** diff and feedback commands for the in-app diff viewer ([359ea62](https://github.com/sushidev-team/lola/commit/359ea62c055db7deeaad85aa6803baa6ead40c81))
* **desktop:** agent glyph + lifecycle chip in the session list ([060ea61](https://github.com/sushidev-team/lola/commit/060ea6137da4a669ce70f66793485f9c96be780e))
* **desktop:** diff tab with line comments sent to the agent ([88b0765](https://github.com/sushidev-team/lola/commit/88b0765a31afd2d2bc9bc50ff121e6101f625443))
* **desktop:** move checkpoints into the session sidebar ([f833e56](https://github.com/sushidev-team/lola/commit/f833e56f42ac5e588f706152f311dfa1ab3a8926))
* **desktop:** replace menu bar label with monochrome Lola icon ([c7794fe](https://github.com/sushidev-team/lola/commit/c7794fe27a2a04c2d1afa33d7c4a8e11b8a669ae))
* **dispatch:** plan large issues into dependent sub-issues and dispatch in dependency order ([3de0d12](https://github.com/sushidev-team/lola/commit/3de0d12ebf7496e8b89ac7db2049a83a0d571c19))
* **dispatch:** plan large issues into dependent sub-issues and dispatch in dependency order ([e4de24b](https://github.com/sushidev-team/lola/commit/e4de24b26bfca36207a90137e259e6e7221adef8))
* in-app diff viewer with line comments sent to the agent ([48b704f](https://github.com/sushidev-team/lola/commit/48b704f81aca29b604c3732c64af6670848edef0))
* pace-aware limit bars and a structured usage card ([cf3df18](https://github.com/sushidev-team/lola/commit/cf3df1873ca74bb56c36dd54a9e8619d3a151ae3))
* show claude and codex subscription limits in the header ([f6fe0e3](https://github.com/sushidev-team/lola/commit/f6fe0e32109dcd1d6e622c2dc91fa04522ab408e))
* show subscription limits as progress bars ([38fb595](https://github.com/sushidev-team/lola/commit/38fb5956cdd9feaa6f602bf746b15a7a2dea1971))
* show tokens with a history-ranked size glyph and burn flame ([73ec047](https://github.com/sushidev-team/lola/commit/73ec047743b118bacf50142f7a80aae6fd38c306))
* token usage, subscription limits, daily token budgets and load-aware dispatch ([0fa52e8](https://github.com/sushidev-team/lola/commit/0fa52e8d641a1986aac9af115b6153f39b374a79))
* **tui:** diff overlay with line comments and notes sent to the agent ([530946d](https://github.com/sushidev-team/lola/commit/530946d40d6f3607e6826b3ca37c73b9dc241be5))
* turn checkpoints, fork-from-here, and a shared context folder ([f3db360](https://github.com/sushidev-team/lola/commit/f3db3602aeb349b753970910c4d0618b179b7bf0))
* turn checkpoints, fork-from-here, and a shared context folder ([726f4b3](https://github.com/sushidev-team/lola/commit/726f4b3d676a5052a60fdcbf7ba17af18a1779a4))


### Bug Fixes

* address diff-viewer review findings ([11eaab9](https://github.com/sushidev-team/lola/commit/11eaab95c19d4d322dc05b6d99b5b9f412fa84d8))
* **checkpoint:** keep the index mtime on the snapshot's temp index ([9574a2b](https://github.com/sushidev-team/lola/commit/9574a2b192532807e1b5d5c41b5cf6f3e3dca1b3))
* **claimaudit:** judge a run by the shell structure around it ([43ecebd](https://github.com/sushidev-team/lola/commit/43ecebdf7a4b3c73211b376f182bd824407f397e))
* **desktop:** prefix file-tree file keys so a file named dir:x cannot collide ([62a499e](https://github.com/sushidev-team/lola/commit/62a499e872a25f26f87187e829fa82c8bf97ed71))
* **desktop:** stop the diff tab reloading on every session push ([d8ffdf8](https://github.com/sushidev-team/lola/commit/d8ffdf82878c073549cb98f98a9fc68c3499938a))
* harden checkpoint restore failure and fork slot selection ([bb21d09](https://github.com/sushidev-team/lola/commit/bb21d095e95a1f58cc094cd4b1ca537a7e470ce9))
* make checkpoint restore safe and baseline every agent kind ([77b3c30](https://github.com/sushidev-team/lola/commit/77b3c304b255e050a4377a76ed934b412d4efe6f))
* **plan:** never let a sub-issue match the poll before its blockers are linked ([df2647f](https://github.com/sushidev-team/lola/commit/df2647f17a4a74a4482cad68fa25bc6e8b348d77))

## [0.2.13](https://github.com/sushidev-team/lola/compare/v0.2.12...v0.2.13) (2026-09-05)


### Features

* **daemon:** add cmd=paneClose and cmd=paneResize for the phone's tab strip ([c96b62f](https://github.com/sushidev-team/lola/commit/c96b62f5cec557a1c8baf4760bb93879ec122664))
* **daemon:** add cmd=panes and cmd=shellCreate for the phone's tab strip ([09713c3](https://github.com/sushidev-team/lola/commit/09713c36be70a7a18656bcf98f4eb40e69945bb9))
* **daemon:** let cmd=shellCreate be told the size to create the shell at ([5c56e2e](https://github.com/sushidev-team/lola/commit/5c56e2e82d15d91813c8c1603dc09ba797c39458))
* **daemon:** publish an active session's dev servers on the local network ([44a9e0a](https://github.com/sushidev-team/lola/commit/44a9e0a327fd83aca9b4056e865f8bbadd0f9ce3))
* **desktop:** add a Remote tab to settings for the phone listener ([d214aa6](https://github.com/sushidev-team/lola/commit/d214aa6d99e999ac48d78dc79c681d64735b1f24))
* **desktop:** simplify setup and organize project settings ([edc074f](https://github.com/sushidev-team/lola/commit/edc074fe2ccffcbbd5c87ce029ffb544afa7d9eb))
* **mobile:** a shell this phone started is sized to this phone ([bc3c4ca](https://github.com/sushidev-team/lola/commit/bc3c4caedb1d0fd558bfcbbcb5217d3d701c6928))
* **mobile:** add the M1 iOS client — Capacitor shell, wire client, LolaTransport ([c3befc1](https://github.com/sushidev-team/lola/commit/c3befc1d6d8a04500397073d5307031ce8577cb5))
* **mobile:** client wire and shim for paneClose and paneResize ([f29b0f1](https://github.com/sushidev-team/lola/commit/f29b0f1480a3413d3435d3b5c4e1c484ae3ea37d))
* **mobile:** drop dead tabs, long-press close and rename, pin the pane while focused ([fb9f886](https://github.com/sushidev-team/lola/commit/fb9f8868203a5505c1f898099baa59fdffe8e287))
* **mobile:** find the Mac on a network this phone has never seen ([78cf490](https://github.com/sushidev-team/lola/commit/78cf490356e6a2e659d3774de274622ee27e26fe))
* **mobile:** milestone 1 — iOS companion app, its daemon transport, and local-network discovery ([60cb3e1](https://github.com/sushidev-team/lola/commit/60cb3e14e2f73552f32714815f863974739a76be))
* **mobile:** name the Mac, not the address it happens to be at ([41cff9c](https://github.com/sushidev-team/lola/commit/41cff9c4138ceb75a3b093723a2b79ae792219a0))
* **mobile:** open an active session's dev servers from the phone ([293952d](https://github.com/sushidev-team/lola/commit/293952d0b7eef68dfabe4034733562c0e6530ed9))
* **mobile:** pair by QR from the desktop, add a scriptable connect path, icon and launch screen ([9deefdc](https://github.com/sushidev-team/lola/commit/9deefdc7957dbe8a62340f6b64d04f67ae95db78))
* **mobile:** persist the key in the Keychain, reconnect on foreground, scrollable strips ([6df7809](https://github.com/sushidev-team/lola/commit/6df7809d1e8ad7ebd8c4a1d6d06da6ec749e7851))
* **mobile:** redesign the app against the Figma frames, and give it four tabs ([10cc7e6](https://github.com/sushidev-team/lola/commit/10cc7e6a84b6531f5ee19a13b89cf0eff78b975c))
* **mobile:** settings menu, filter sheet, pane tabs, PR button and a view popover ([019a07e](https://github.com/sushidev-team/lola/commit/019a07eee1daa585d6ba7c960ba8c8e49239ca0c))
* **mobile:** try every address the daemon offered, and stop at a refusal ([8ff8d0d](https://github.com/sushidev-team/lola/commit/8ff8d0deb7f1c8b0d5d0f98886e3c4e1fa2bb574))
* **remote:** add the M1 byte pipe for the mobile companion app ([39c538d](https://github.com/sushidev-team/lola/commit/39c538da331983e49d55a910c7366fe844d58573))
* **remote:** advertise the phone listener on the local network ([7ed8cf0](https://github.com/sushidev-team/lola/commit/7ed8cf06b8fbca517b6423ce3c7f997fcff9840d))
* **remote:** generate and persist the phone bearer key, add a LAN opt-in ([c79d352](https://github.com/sushidev-team/lola/commit/c79d35221eb07c8ccd90644db12e87482f246369))
* **remote:** make the LAN opt-in a config key instead of an environment variable ([4dccf29](https://github.com/sushidev-team/lola/commit/4dccf2912758f15abb58dbec5a6d7773393ec23c))
* **remote:** regenerate the phone key from settings, and report why a listener refused ([6ceea0a](https://github.com/sushidev-team/lola/commit/6ceea0aa16d6d6fbd178427bdb27b81752469272))
* **settings:** add preset selectors and independent sidebar scrolling ([4012b0a](https://github.com/sushidev-team/lola/commit/4012b0a4fda6972a3abfc1dd6fbc5d60737005be))
* **settings:** organize review providers into compact cards ([4c67459](https://github.com/sushidev-team/lola/commit/4c67459abd2b8445893b1b1aca59700a110805a3))
* **settings:** put the local-network advertisement in both settings forms ([b71b9df](https://github.com/sushidev-team/lola/commit/b71b9df417a14bbfed558d92f6ed4840fd5e5525))
* **settings:** shorten hints and show details in help popovers ([76cf87d](https://github.com/sushidev-team/lola/commit/76cf87de2d06b5146d027d2090b2cc6ad6177113))
* **settings:** simplify forms and support interpreter providers ([c567306](https://github.com/sushidev-team/lola/commit/c567306dc804ac2dc49b1d75186d8c1ab5bbd25a))
* simplify setup and grant Codex worktree Git access ([58a9086](https://github.com/sushidev-team/lola/commit/58a9086eb4eba51d72b377415c5affcc3b63dc90))
* **state:** make the agent axis the primary pill and attention a predicate over both ([15c4837](https://github.com/sushidev-team/lola/commit/15c48379b90aba7904578775415307732d02e0ab))
* **tui:** add the Remote tab to settings for the phone listener ([a35f83a](https://github.com/sushidev-team/lola/commit/a35f83a76ca2e8a5991b6d43febe9667ad206d7c))


### Bug Fixes

* added agents.md ([b89c3ab](https://github.com/sushidev-team/lola/commit/b89c3ab7224a56fcca02fa2651689912d1d72fa6))
* address setup and settings review feedback ([887c273](https://github.com/sushidev-team/lola/commit/887c273eea57bf834d5cdb0a6c4485babbb27089))
* **codex:** enable autonomous approval review ([1765a7c](https://github.com/sushidev-team/lola/commit/1765a7c6e68dd83315c4977998bd28830ee7d53e))
* **daemon:** let a release forgive a session that is gone, not just a pane ([9b3ca21](https://github.com/sushidev-team/lola/commit/9b3ca21f66b7f7ff1d26c177e1bd1739c191d5dc))
* **daemon:** name the binary that refused to hand out a connect code ([8e23d67](https://github.com/sushidev-team/lola/commit/8e23d67dd0077e8025f9bcf724a32843dbe500a2))
* **devforward:** dial localhost dual-stack, and check loopback on the connection ([a11c735](https://github.com/sushidev-team/lola/commit/a11c735443446f8a6cd22db87fced8307b57b96b))
* **mobile:** activate a session from the phone, and forward the localhost URLs ([b564fd0](https://github.com/sushidev-team/lola/commit/b564fd0c2253b30ac97ccca538e4a0027c09da59))
* **mobile:** apply the review pass on the Keychain and scrollable strips ([240a81b](https://github.com/sushidev-team/lola/commit/240a81bf0fa1574e394246067296aadd158c5211))
* **mobile:** keep the line you are typing into on screen ([e180ca2](https://github.com/sushidev-team/lola/commit/e180ca245243d53f19e3b73f2e7466724133a5fe))
* **mobile:** name a forward by the address it publishes, and report a link that would not open ([9bea9c5](https://github.com/sushidev-team/lola/commit/9bea9c5c2c11be4cb8c8ecfb974e1ff8f5f0335b))
* **mobile:** name the SPM package LolaTransport so Xcode can resolve it ([bf35c1a](https://github.com/sushidev-team/lola/commit/bf35c1a491af677f635014512a78d763c39f79c6))
* **mobile:** open links with UIApplication, not in-app Safari ([7af9952](https://github.com/sushidev-team/lola/commit/7af9952cf40cbce70b5d84cf524cc83a3486fa63))
* **mobile:** parse devicectl columns by gutter, and discover the signing team ([4ac105a](https://github.com/sushidev-team/lola/commit/4ac105aba204a0e7076a218d448f5768cbf970a2))
* **mobile:** pin to the phone's capacity, not to what fits the current grid ([313614b](https://github.com/sushidev-team/lola/commit/313614b7acdabef3eb9842cab70f701cf67ccdf7))
* **pin:** a refused pin leaves nothing pinned, and nothing believing it is ([4d2bceb](https://github.com/sushidev-team/lola/commit/4d2bceb4430d2587862c6008a77b000acb6acd12))
* **remote:** do not drop a live phone when an address APPEARS ([c090c8e](https://github.com/sushidev-team/lola/commit/c090c8eeb31bb9c1a952675452e4c894e9702525))
* **remote:** drop zone-scoped addresses from the connect code, add make mobile-lan ([7c681df](https://github.com/sushidev-team/lola/commit/7c681df770b935457ed2a197489b33d32d8467b2))
* **remote:** make the advertisement opt-in and anonymous, as PLAN.md requires ([cfd55fb](https://github.com/sushidev-team/lola/commit/cfd55fbce8a7ec9569972031b2983581d08c0f4a))
* **remote:** put dialable addresses in the connect code, never a wildcard ([63d8947](https://github.com/sushidev-team/lola/commit/63d894734528f5b1aec6d8446c2cb2a70adc039b))
* **remote:** rebind the phone listener when the machine changes networks ([fd5620a](https://github.com/sushidev-team/lola/commit/fd5620aef7a9162f207a4121fc3ad1670ebe2ded))
* **remote:** stop rebinding every time AirDrop rotates its address ([831273c](https://github.com/sushidev-team/lola/commit/831273c0554fd1bda2913d7ebc9204273b05209c))
* **review:** let a forced review post to the PR again ([ac41a17](https://github.com/sushidev-team/lola/commit/ac41a17acf61d10d6fafd2a420f1e0b04a90a097))
* **runtime:** grant Codex access to shared worktree git metadata ([3b57539](https://github.com/sushidev-team/lola/commit/3b575395cd338ad352fcfd2f93bb22294e3ac4da))
* **settings:** repair accessible hints and review inconsistencies ([f664f59](https://github.com/sushidev-team/lola/commit/f664f59221832a5269ac13ea5f26b07ed55c3589))
* **tmux:** the size pin never resized a window, and the release un-released it ([55fb0e2](https://github.com/sushidev-team/lola/commit/55fb0e26ab5d991a0d845f2aa7bf7d14a6c2781f))

## [0.2.12](https://github.com/sushidev-team/lola/compare/v0.2.11...v0.2.12) (2026-08-24)


### Features

* **agent:** fallback to another coding agent on usage limits + manual switching ([6b43a0d](https://github.com/sushidev-team/lola/commit/6b43a0dd79dfce55066c06e32402f6f5ec708f87))
* **review:** make every review vendor a config slot ([bb397ab](https://github.com/sushidev-team/lola/commit/bb397ab911b87b7fda8daba696d21a443ad206cd))
* **review:** make every review vendor a config slot ([3d43d01](https://github.com/sushidev-team/lola/commit/3d43d018a1d03e57600963a2214619673111c9a5))
* **tmux:** add PaneProcs listing each pane's owning session name ([3214395](https://github.com/sushidev-team/lola/commit/3214395a1cabdad6a93597e6e9b8628fb460074c))


### Bug Fixes

* **agent:** CodeRabbit pass-1 fixes + doctor chain check + agent chip ([d7c5f59](https://github.com/sushidev-team/lola/commit/d7c5f5922f476c06f673a2ea5a9364af5681557e))
* **agent:** kill a partially created pane when the switch launch fails ([8968821](https://github.com/sushidev-team/lola/commit/896882114875620927fed15f46aea7a37358cf85))
* **agent:** redirect the opencode plugin's hook stdin from /dev/null ([ce2f7f4](https://github.com/sushidev-team/lola/commit/ce2f7f4e7b38e6c089198aba07eb3ff729eaac0a))
* **agent:** resume opencode conversations on revive via --continue ([f01d114](https://github.com/sushidev-team/lola/commit/f01d1148b44157d08eaaa2e3446b2b979975b030))
* **daemon:** spare shell-tab processes from the dev take-over port sweep ([25d5cb9](https://github.com/sushidev-team/lola/commit/25d5cb972f0471e554da9de40270b065fd7dc644))
* **hook:** never read stdin when it is a TTY ([186c17a](https://github.com/sushidev-team/lola/commit/186c17a18792781323c0977cbbc7d6a8c2ef7a65))
* **review:** carry the stderr fixes to the cli-family classifier ([a27f189](https://github.com/sushidev-team/lola/commit/a27f189a7fb4e2b367e2b62108a6e6150a758bd6))
* **review:** fail closed on an unknown kind, serialize the pane tees ([2deb50e](https://github.com/sushidev-team/lola/commit/2deb50ede7d87ba613801fa5ce520f186829f712))
* **review:** stop an agent's narration from being read as an error ([96b7e7b](https://github.com/sushidev-team/lola/commit/96b7e7be577afad3032fee8b0f13ea15c83e7146))
* **runtime:** detect saved opencode sessions across both storage generations ([0fdc157](https://github.com/sushidev-team/lola/commit/0fdc157a443488c9788ddaec44e7856d268c6f3a))
* **tmux:** KillSessionTree must tolerate a teardown raced by the group kill ([0f665c2](https://github.com/sushidev-team/lola/commit/0f665c2cfa767ae499ae58b5b22817babd43ea9f))

## [0.2.11](https://github.com/sushidev-team/lola/compare/v0.2.10...v0.2.11) (2026-08-20)


### Features

* **app:** lead the session header with Active and kill with a drawn skull ([29cb47b](https://github.com/sushidev-team/lola/commit/29cb47bc3b982124bc7055f8e1bd1e40715d8613))


### Bug Fixes

* **terminal:** make shift+enter insert a line break, not send the message ([3bd0584](https://github.com/sushidev-team/lola/commit/3bd0584ac7a396c07cabe2253a7fb1ed8f34339a))

## [0.2.10](https://github.com/sushidev-team/lola/compare/v0.2.9...v0.2.10) (2026-08-18)


### Features

* **app:** group and reorder projects in the sidebar ([218457d](https://github.com/sushidev-team/lola/commit/218457dc4f4e1f6145d7282c5d45d90b0df24838))
* **app:** group and reorder projects in the sidebar ([cdfe396](https://github.com/sushidev-team/lola/commit/cdfe396ceb3f7005a3c8d983f310471ec94bbece))
* **app:** resolve a conflicting session from its status badge ([de60685](https://github.com/sushidev-team/lola/commit/de6068594ef33f12028d18608494eabf9c910162))


### Bug Fixes

* **app:** keep a group row's count on the right and grow its chevron ([87cce16](https://github.com/sushidev-team/lola/commit/87cce16750f5cfad53f0fe1199102ff72baea104))

## [0.2.9](https://github.com/sushidev-team/lola/compare/v0.2.8...v0.2.9) (2026-08-18)


### Features

* **desktop:** draw the form controls instead of letting macOS draw them ([0b0142f](https://github.com/sushidev-team/lola/commit/0b0142f6107b0daf17bc20770eaf6669368a5503))
* **dev:** explain a dev tab that died on a taken port, and offer to free it ([09294bd](https://github.com/sushidev-team/lola/commit/09294bd4ea6115afd7d0a47215e9e4e55133826f))
* inline resolvable PR review threads, the Linear ticket picker, live pane cues ([b8c8225](https://github.com/sushidev-team/lola/commit/b8c8225d233d61e55ad77d9030e1caa84c4b928b))


### Bug Fixes

* **attention:** recognize claude-code's current composer so hand-offs land ([025beeb](https://github.com/sushidev-team/lola/commit/025beeb36351923374c96025ae956477745723c4))
* **desktop:** stop reporting "up to date" when a release has no build yet ([66c3bbc](https://github.com/sushidev-team/lola/commit/66c3bbcc7dba32cc2e6e473dca09e787e631f970))
* **tickets:** head the issue picker with the project, drop the age column ([3288ae1](https://github.com/sushidev-team/lola/commit/3288ae10ac3bcff5127d299192d37aa71fb82829))
* **tmux:** apply the scroll default on a cold server, and lock the copy-mode handshake ([5a288e9](https://github.com/sushidev-team/lola/commit/5a288e97c8d099448a3a75952a41f5b384c8095d))
* **tmux:** let [tmux].mouse own the server's mouse option in both states ([cf37839](https://github.com/sushidev-team/lola/commit/cf3783977259c0cfbbe85b2c341e12780d810be1))
* **tmux:** scroll lola panes from the app through tmux copy mode ([c6f6e07](https://github.com/sushidev-team/lola/commit/c6f6e07c56035e5f14ffabe135732cbed38c7d01))
* **tmux:** scroll the history the pane actually has ([690b1e0](https://github.com/sushidev-team/lola/commit/690b1e0e2eb391207d41f2f23f82506e7f803d88))

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
