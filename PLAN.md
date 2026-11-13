# Plan

Open work for bui-go, as of 2026-11-13. Finished work belongs in `docs/design.md` and the git history, not here.

## Where we stand

- [bui-go#7](https://github.com/CWBudde/bui-go/pull/7) is merged, so `main` has the wasm engine and the Go X11 host.
- [bui-go#8](https://github.com/CWBudde/bui-go/pull/8) is merged, so `main` pins BUI `v0.4.1`.
- BUI's owner wants the binding in-tree at `bindings/go`. We asked four questions on
  [BUI#8](https://github.com/ppmpreetham/BUI/issues/8#issuecomment-6707514269), about the committed wasm, the second X11 host,
  pinning and tags. Christian started Phase 3 on 2026-11-08, in the fork only. The owner
  [answered](https://github.com/ppmpreetham/BUI/issues/8) on 2026-11-09. Christian
  [replied](https://github.com/ppmpreetham/BUI/issues/8#issuecomment-6822364116) on 2026-11-12, and we wait for the owner: see
  Phase 3.
- [bui-go#9](https://github.com/CWBudde/bui-go/pull/9) is merged, so `main` has the macOS baseline.
- [bui-go#10](https://github.com/CWBudde/bui-go/pull/10) is merged, so `main` has the Cocoa spike. Christian counted its 1.6x as
  close and lifted the hold on the port, so the port goes ahead before the owner answers.
- [bui-go#11](https://github.com/CWBudde/bui-go/pull/11) is merged, so `main` has macOS input.
- [bui-go#12](https://github.com/CWBudde/bui-go/pull/12) is merged, so `main`'s macOS CI job is green again and runs the Cocoa
  tests without cgo.
- [bui-go#13](https://github.com/CWBudde/bui-go/pull/13) is merged, so `main`'s Cocoa host has a menu, quits with Cmd+Q and draws
  during live resize.
- [bui-go#14](https://github.com/CWBudde/bui-go/pull/14) is merged, so `main`'s Cocoa host follows scale changes between screens.
- [bui-go#15](https://github.com/CWBudde/bui-go/pull/15) is merged, so `main` states its licence as BUI does.
- [bui-go#16](https://github.com/CWBudde/bui-go/pull/16) is merged, so `main` pins BUI `v0.5.0` and the cgo engine opens X11
  windows unless `BUI_WINDOW_BACKEND` says otherwise.
- [bui-go#17](https://github.com/CWBudde/bui-go/pull/17) is merged, so `main` refuses nested Window and Headless contexts, a bug
  review found in the fork's PR 1.
- [bui-go#18](https://github.com/CWBudde/bui-go/pull/18), [bui-go#19](https://github.com/CWBudde/bui-go/pull/19) and
  [bui-go#20](https://github.com/CWBudde/bui-go/pull/20) are merged, so `main` has the fixes for the review findings on the hosts.
- [bui-go#21](https://github.com/CWBudde/bui-go/pull/21) is merged, so `main`'s `Headless` has pixels, damage and the kit's input.
  [bui-go#22](https://github.com/CWBudde/bui-go/pull/22) is merged too, with the check by hand on the MacBook.
- [bui-go#23](https://github.com/CWBudde/bui-go/pull/23) is merged, so `main`'s `Headless` has the host's side of the embedding
  API. [CWBudde/BUI#5](https://github.com/CWBudde/BUI/pull/5) is merged too, so the fork's `main` (`6954d0c`) carries #21 to #23.
  [bui-go#24](https://github.com/CWBudde/bui-go/pull/24) is merged, with the plan of that run.
- The three Phase 3 PRs are merged into the fork's `main`, not upstream:
  [CWBudde/BUI#1](https://github.com/CWBudde/BUI/pull/1), [CWBudde/BUI#4](https://github.com/CWBudde/BUI/pull/4) (the X11 host,
  which [CWBudde/BUI#2](https://github.com/CWBudde/BUI/pull/2) had merged into PR 1's branch) and
  [CWBudde/BUI#3](https://github.com/CWBudde/BUI/pull/3).
- Upstream's `main` is 113 commits past the fork's base `e8126ca`, as of 2026-11-12.
- BUI tagged `v0.5.0` on 2026-11-08, 112 commits past `v0.4.1` (see Phase 0).
- A trial bump to BUI `bca7b8d` worked: the wasm built unmodified, reproduced byte for byte, and passed without cgo with the golden
  hashes unchanged. With cgo, only `TestNoDisplay` failed (see Phase 0).

## Phase 0: Tidy up bui-go

None of this depends on the owner's answer.

- [x] Make `TestNoDisplay` independent of Wayland. (2026-11-07) — the subprocess now also clears `WAYLAND_DISPLAY` and points
      `XDG_RUNTIME_DIR` at a missing path. With libraries built at BUI `bca7b8d` on a Wayland session, it failed before and passes after.
- [x] Fix the X11 check in `scripts/build-kit.sh`. (2026-11-07) — it accepts `BK_window_run` or `bk_x11_window_run` in
      `kit_window_x11.c.o`. The script builds at `bca7b8d` and `v0.4.1`, and the check rejects an archive with only `kit_window_null.c.o`.
- [x] Pin the `v0.4.1` tag instead of a commit, as `BUI_kit.h` now asks, and update the docs that name `e3aba90`. (2026-11-07) —
      `BUI_COMMIT` holds the tag's commit, and the wasm module and libraries are rebuilt. The golden hashes did not change. Measurements
      stay attributed to `e3aba90`.
- [x] Decide how to handle cgo and Go hosts disagreeing on a Wayland-only system: cgo opens a native Wayland window, while the Go
      host needs X11 or XWayland. Decide between documenting this and forcing `BUI_WINDOW_BACKEND=x11` in the cgo engine.
      Not reachable at `v0.4.1`, which predates BUI's Wayland host, so this is due at the first bump past that tag. (2026-11-08) —
      Christian chose a default: the cgo engine sets `BUI_WINDOW_BACKEND=x11` with C's `setenv` only when it is unset.
      `TestWindowBackendDefault` fails without it. On the GNOME session with `DISPLAY` unset, `Supported` gave false by default
      and true with `wayland`. "Wayland" in `docs/design.md` and the README say so.
- [x] Rewrite "Licensing" in `docs/design.md`, the README and `NOTICES.md`. BUI now states GPL-3.0-or-later, with
      `GPL-2.0-or-later` SPDX headers kept on files. The reading still has no legal review. (2026-11-08) — bui-go mirrors BUI, as
      Christian chose: GPL-3.0-or-later as a whole, with its files' `GPL-2.0-or-later` headers unchanged. The README, "Licensing" and
      `NOTICES.md` say so, and the wasm engine's GPLv3 now follows from the whole rather than from "or later".
- [ ] Hover and drag with a real pointer on a real X session. So far these have only been driven on Xvfb.
- [ ] Wasm containment: a trap or out-of-memory ends the process. The owner asks for a way to catch it (Phase 3), so this is no
      longer "keep it and file an issue". A module that trapped is in an unknown state, so catching means at least reporting the
      error and refusing further kit calls. Christian decided on 2026-11-12: both, an error from `Run` and a hook, after which kit
      calls are refused. It comes after PR 1 goes upstream, as the reply on BUI#8 says.
- [x] Make the load in `scripts/bench.sh` independent of the locale: on the German MacBook `sysctl` gives `7,05`. Set `LC_ALL=C`.
      (2026-11-08) — `LC_ALL=C` on the `sysctl` call alone. On the MacBook under `de_DE.UTF-8`, the old `load()` printed
      `1,47 2,14 2,30` and the new one `1.47 2.14 2.30`. The header of `bench/2026-11-07-a06371a.txt` now uses dots too.
- [x] Bump BUI to `v0.5.0`, tagged 2026-11-08: rebuild the wasm module and the libraries, and check the golden hashes. It makes the
      Wayland decision above due. (2026-11-08) — `BUI_COMMIT` holds `37e6fb4`. The kit only gained `BK_ime_*`, and the golden
      hashes did not change on either engine. The libraries now hold BUI's Wayland host: `build-kit.sh` asks for it and fails
      without it, and `NOTICES.md` credits the two protocol files whose generated code it contains.

## Phase 1: macOS baseline on the MacBook

The MacBook is an Apple M5 Pro on macOS 27.2, with Go 1.27.1, CMake and Apple clang. `ninja` is missing, which only matters for building
BUI natively. Use `ssh macbook-ts`: the `macbook` name doesn't resolve from the Linux machine, and the agent must be
`SSH_AUTH_SOCK=/run/user/1000/gcr/ssh`. Go and CMake are in `/opt/homebrew/bin`, so run commands through `zsh -lc`. The clone is
`~/Code/bui-go`. Its `sed` is GNU sed, not BSD sed.

CI only proves that the package passes on `macos-latest`. This phase adds checks CI can't make:

- [x] Clone `main` and run `go test -count=1 ./...` with and without cgo, on Go 1.27.1 and, via `GOTOOLCHAIN`, on 1.26.8. (2026-11-07) —
      all four runs at `a06371a` pass, with 329 passes and the same 54 skips.
- [x] Compare a `Headless` render against Linux amd64 by hash. The golden test should already cover this, so confirm it runs here
      rather than skips. (2026-11-07) — `TestGoldenPinned` runs and passes, and fails when the first or last pinned hash is changed.
- [x] Run `bench/` for arm64 numbers, and add them to `docs/design.md`. The published numbers are all from an amd64 laptop. (2026-11-07)
      — `bench/2026-11-07-a06371a.txt`, wasm only, as the native engine needs Linux amd64. `scripts/bench.sh` now records the load on macOS.
- [x] Establish how a GUI program started over SSH reaches the logged-in session, for example with `launchctl asuser`. Phase 2's
      tests depend on this. (2026-11-07) — running it directly works, as does `launchctl bootstrap gui/501`; `launchctl asuser` without
      `sudo` fails with "Operation not permitted". Tried only while logged in at the console.
- [x] List the macOS permissions tests need: Screen Recording for screenshots, Accessibility for synthetic input. Christian grants
      these in System Settings. An agent must not change them. (2026-11-07) — confirmed with a probe, without and with both grants.
      Grants only apply through `launchctl bootstrap gui/501`, not started directly from SSH, and hold for one binary at one path.

The module itself still gets built on Linux only, because only the Linux x86_64 wasi-sdk is pinned.

## Phase 2: Cocoa window host (spike first)

Goal: `CGO_ENABLED=0 go run ./examples/counter` opens a real window on macOS. `kit_window_cocoa.m` (about 1,900 lines) is the model,
reached through purego and the Objective-C runtime, as `docs/design.md` proposes.

This is a second reimplemented host, so it raises the same question as question 2 on BUI#8. The port was held until the owner
answered, until Christian lifted that hold on 2026-11-07: it serves bui-go either way, and goes upstream if the answer is yes.

**Spike, in a time-boxed branch:**

- [x] Decide how permission tests get a fixed binary, because `go test` builds a new one each time and each new binary loses its
      grants: for example `go test -c` to a fixed path run as an agent, or a real signing identity, which is untried. (2026-11-07) —
      a small fixed helper holds the grant and runs as an agent, so the program under test can be rebuilt freely. Shown for screenshots;
      synthetic input hasn't been tried this way, and neither has a signing identity.
- [x] Add `github.com/ebitengine/purego` and check that the cross-build from Linux with `CGO_ENABLED=0` still works. (2026-11-07) —
      v0.11.3. All six targets build, and test binaries built on Linux run on the Mac.
- [x] Pin AppKit to the main thread. Run it from `init` with `runtime.LockOSThread`, and check that this fits `Do`, `Wake` and the
      kit's calling rules in `engine.Host`. (2026-11-07) — it fits, using a pull loop. `TestWakeEndsABlockedWaitWithGC` passes, and
      `examples/counter` ticks through `bui.Do`.
- [x] Open a window and present a `Headless`-sized raster through `CGImage` or a layer, at Retina scale. (2026-11-07) — a `CGImage` in
      a layer. A capture of the window is 640x376 for 320x188, and `TestLayerHoldsTheRaster` matches every pixel.
- [x] Measure the frame time against the X11 host at the same window size. (2026-11-07) — about 1.6x X11's, on different machines:
      5.4 ms against 3.4 ms at 1080p, 420 µs against 241-310 µs for the counter window.

Continue only if a window draws correctly and the frame time is close to X11's. Otherwise, record why in `docs/design.md` and stop.

The window draws correctly, and Christian counted 1.6x as close on 2026-11-07, as a 1080p frame stays near 10 ms. The numbers are in
"The Cocoa spike" in `docs/design.md`.

**Port, if the spike passes:**

- [x] `internal/host/cocoa` implementing `engine.Host`, with `host_darwin.go` and `host_none.go` narrowed to the remaining platforms.
      (2026-11-07) — the spike had done it; the package comment no longer calls it a spike. All six targets cross-build without cgo.
- [x] Mouse, scroll, keyboard and text input, resize, close, `Do` wakes and the clipboard, tested in the same areas as the X11 host.
      (2026-11-07) — ported from the C view, with cursors and `Warp`. 363 passes and no failures on the MacBook, with and without cgo,
      on Go 1.27.1 and 1.26.8, and each of 47 handler breaks fails its test. The input is synthetic: keys skip `NSApp`, and the real
      clipboard, a real warp and `pressedMouseButtons` aren't reached.
- [x] From the spike: live resize (AppKit's nested loop), scale changes between screens, activation, a real click on the close button,
      and the layer and delegate leaked once per window. (2026-11-07) — partial: the first show activates the app, and `counter`
      came to the front over the editor and quit cleanly. (2026-11-08) — partial: live resize draws, as `windowDidResize:` renders
      and a wake runs inside AppKit's loops through a run-loop source, and the layer and the delegate are released.
      `TestLiveResizeDraws` drags a real corner with posted events, and it, `TestWakeRunsInsideATrackingLoop`,
      `TestWakeDoesNotNestInItsOwnDrain` and `TestWindowFreeReleasesTheWindow` each fail with their part removed. Left: scale changes,
      which need a relayout that `engine.Kit` lacks, so the interface changes for both engines. Activation and the close button go to
      the check by hand. (2026-11-08) — scale changes follow, as `windowDidChangeBackingProperties:` gives the layer the new pixels per
      point and the kit the new scale, then calls the new `NodeRelayout` on `ContextRoot`, as the C host does. `BUI_UI_SCALE` works
      as in the C host. `TestScaleChangeBetweenScreens` and `TestUIScaleFromTheEnvironment` fake the backing scale and fail without
      the handler, the layer's update or the environment rule. The relayout itself is shown only by the conformance case
      `ScaleNeedsRelayout` on both engines: a new raster size already relayouts. Activation, the close button and a real second
      screen are in the check by hand below.
- [x] A menu with Cmd+Q, Hide and the window items, as `app_init` in the C host builds. (2026-11-08) — the C host's two menus, and an
      app delegate that closes every window on quit and cancels. `TestQuitClosesEveryWindow` quits by Cmd+Q, `terminate:` and the quit
      Apple Event in a child, which fails without the delegate, where AppKit exits with status 0. `TestMenu` fails without the Quit
      item. Cmd+W and Minimize need a key window, which tests can't have.
- [x] By hand on the MacBook: click, drag, scroll, type and copy and paste in `examples/counter` and a text field, as no test
      reaches the real mouse, keyboard or clipboard. Also Cmd+Q, Cmd+W, Minimize, the Dock's Quit, the close button, dragging an
      edge while the counter ticks, and whether the first show activates. (2026-11-08) — partial: all of these work at `1ebbfd6`,
      except a dead key, which "By hand on the MacBook" in `docs/design.md` records. Left: the second screen below.
- [ ] By hand with a second screen of another scale: move the window there, and open it there. No screen was at hand on 2026-11-08.
- [ ] Decide on dead keys: ´ then e types "e", as in the C host, since neither goes through `interpretKeyEvents:`. Recommendation:
      keep it as a known limit until IME, which needs `NSTextInputClient` too, and tell BUI's owner, as the C host has the same gap.
- [x] A test job on `macos-latest` for whatever works without a logged-in session, plus a documented manual run on the MacBook.
      (2026-11-08) — the runner has a logged-in session, so the existing macOS job now also runs `go test -v ./internal/host/cocoa`
      without cgo, with none skipped for want of a display. Input tests wait until the window is on screen, as AppKit dropped
      clicks sent before that: the package passed three times in a row on the runner with the wait, and lost clicks without it.
      The command for the MacBook is in "Running the macOS tests by hand" in `docs/design.md`, and passed there as written.
- [x] Update the README's platform table and "What has been run" in `docs/design.md`. (2026-11-08) — the Engines table names
      Cocoa on macOS, and both say what CI runs on macOS.

## Phase 3: Upstream as `bindings/go`

Started on 2026-11-08 before the owner answered our four questions on BUI#8, as Christian chose. The owner answered on 2026-11-09,
and the answer changes the scope: the items from "Reply on BUI#8" down come from it. Everything stays in the fork until Christian
decides to open the upstream PRs.

- [x] Fork to `CWBudde/BUI`. (2026-11-08) — the fork has Actions on and a clone at `/mnt/projekte/Code/BUI`. Its `origin` uses the
      `github.com-cwbudde` SSH alias, as plain `github.com` signs in as MeKo-Christian, which has no access. Commits are by
      `Christian Budde <Christian.Budde@meko.de>`.
- [x] Move the module to `bindings/go`, under the path `github.com/ppmpreetham/BUI/bindings/go`, in fresh commits that link back here.
      (2026-11-08) — in [CWBudde/BUI#1](https://github.com/CWBudde/BUI/pull/1), from `d21272d`. `host_none.go` is the only host
      there; the hosts come with PR 2 and PR 3.
- [x] Leave out `lib/linux_amd64/*.a` and make the cgo engine opt-in, using libraries built with CMake. (2026-11-08) — the tag
      `bui_cgo` links `build/{kit,gpu,core}` from BUI's own CMake build at the repository's root. `go test -tags bui_cgo ./...`
      passes against the fork's tree, the comparison of both engines' pixels included.
- [x] Build `sjlj_probe.wasm` in the test step instead of committing it. (2026-11-08) — `TestSjljFreeTypeOverflow` builds it with
      `build-wasm.sh probe` and skips with a message without the pinned wasi-sdk. CI fetches the SDK first.
- [x] Merge `NOTICES.md` and `licenses/` into `THIRD_PARTY_NOTICES.md` where they overlap, and keep the gaps marked. (2026-11-08) —
      `tools/gen_notices.py` renders a section "The Go binding", and `gen_notices.py --check` passes. PR 2 and PR 3 add xgb, the
      X11 keysym tables and purego.
- [x] Add a Go CI job limited to `bindings/go/**`. (2026-11-08) — `.github/workflows/go.yml`: formatting, lint, tests without cgo
      on Linux and macOS, and `check-wasm`. PR 2 adds the X11 runs, and PR 3 the Cocoa tests on macOS.
- [ ] PR 1: wasm engine, public API, `Headless`, enginetest. Prepared as [CWBudde/BUI#1](https://github.com/CWBudde/BUI/pull/1),
      merged into the fork's `main`. It carries [bui-go#17](https://github.com/CWBudde/bui-go/pull/17) as well. The owner wants no
      committed wasm on `main` (see below), so it needs rework before it goes upstream.
- [ ] PR 2: X11 host. Prepared as [CWBudde/BUI#2](https://github.com/CWBudde/BUI/pull/2) and
      [CWBudde/BUI#4](https://github.com/CWBudde/BUI/pull/4), merged into the fork's `main`. The owner would rather not have it in the
      core (see below).
- [ ] PR 3: Cocoa host. Prepared as [CWBudde/BUI#3](https://github.com/CWBudde/BUI/pull/3), merged into the fork's `main`. Its tests
      ran only in the fork's macOS CI job. The owner's point on the X11 host applies to it too.
- [x] PR 2 and PR 3 both narrow `host_none.go`'s build constraint. Whichever goes second needs
      `//go:build !linux && !darwin && !bui_cgo`. (2026-11-10) — the fork's `main` at `9fdcceb` has exactly that line.
- [x] Check the review findings on the hosts. All of them are in code moved unchanged from here, so fix them here first, then carry
      the fixes into the fork, as for [bui-go#17](https://github.com/CWBudde/bui-go/pull/17). (2026-11-10) — all nine are real and
      fixed, with a test each but the licence notice: X11 in [bui-go#19](https://github.com/CWBudde/bui-go/pull/19) (merged as
      `ee5371e`), Cocoa in [bui-go#20](https://github.com/CWBudde/bui-go/pull/20) (merged as `415f042`). The fork's `main` carries
      them.
- [x] Each PR updates `CHANGELOG.md`, `docs/`, the README's bindings table and the website, and lists its known limits.
      (2026-11-08) — all three PRs do, and PR 2 states that question 2 is open. PR 3 states that the check with a second screen
      is open, and that dead keys type the plain character.
- [ ] Before each PR, prove the module works outside the repo: a scratch module requires `github.com/ppmpreetham/BUI/bindings/go`
      and replaces it with `github.com/CWBudde/BUI/bindings/go <sha>`, as the module declares the upstream path. Confirm BUI's
      `cmake`, `rust` and `package` jobs are unaffected too. (2026-11-08) — partial: with that `replace`, the Go proxy fetched PR
      1's module at `d4bb297`, and its README example ran with `CGO_ENABLED=0`. BUI's jobs are green on all three.
      PR 2 and PR 3 still need the scratch module, and all three need it again on the commit that goes upstream.
- [x] Reply on [BUI#8](https://github.com/ppmpreetham/BUI/issues/8). Christian does that. Points the answer can already state: wazero
      is in `THIRD_PARTY_NOTICES.md` (the fork's `main`, "The Go binding"), and the changelog, docs, README and website are updated.
      On size: `bui_kit.wasm` grew 2,445 bytes from `v0.4.1` to `v0.5.0` (3,454,663 to 3,457,108). It is 1.33 MB gzipped, an upper
      bound for what each update adds to the history, as git's delta compression on it is not measured. (2026-11-12) —
      [posted](https://github.com/ppmpreetham/BUI/issues/8#issuecomment-6822364116) as CWBudde, with Christian's decisions on the
      wasm, the hosts, the generator and trap handling below. It asks the owner to create the wasm repository or name it.
- [ ] Keep the wasm off `main`, as the owner asks: CI builds it from the release tag with the pinned toolchain, and a bot commits it
      to a separate branch or repository that `go get` can fetch, for example `ppmpreetham/BUI-go`. CI rebuilds it from the pinned
      commit and fails if its checksum differs from the committed one. Christian decided on 2026-11-12: a repository holding only
      `bui_kit.wasm` and a small package that embeds it, as its own Go module. `bindings/go` stays on `main` under its path and
      requires that module at the BUI version, and a `go.work` points at a local build for work on `main`. Waits for the owner to
      create the repository or name it, and for a token the bot can push with.
- [x] A public embedding API, so that a window host can live outside package `bui`: render to a pixel buffer and feed events, as the
      owner asks. (2026-11-12) — `Headless` has `Image`, `RenderDamage`, `NeedsRender`, `Resize` and the kit's input with `Mod`
      bits ([bui-go#21](https://github.com/CWBudde/bui-go/pull/21)). [bui-go#23](https://github.com/CWBudde/bui-go/pull/23) adds
      the host's side: `SetHost` with a `bui.Host` for redraw requests, the cursor, warps, the clipboard and timers, fired back with
      `Timer`, plus `Animating`, and `Flush` with `SetWake` for a loop of the program's own. A test per callback passes on both
      engines.
- [ ] Then the X11 and Cocoa hosts as optional packages on that API, outside the core, or in the repository and clearly marked as
      the owner allows. Christian decided on 2026-11-12: a separate module outside BUI, and the core binding is headless only. He
      decided on 2026-11-13 that bui-go is that module: the hosts become public packages here, built on package `bui`'s public API
      only, and once `bindings/go` is upstream, bui-go requires it and keeps only the hosts.
  - [x] Give `Headless` what the hosts called on `engine.Kit` and it lacked. (2026-11-13) — `SetScale` sets the scale one
        `Headless` draws at, selects it before each of its calls, as the kit's scale is process-wide, and lays the tree out again
        when it changes. `CopyBGRA` copies one rectangle of the last render as B, G, R, A into a reused buffer: 1.3 to 1.5 ms for a
        1080p frame, against 4.6 to 8.2 ms for `Image`. `ImagePoll` and `KeyRelease` need no call. `TestHeadlessSetScale`,
        `TestHeadlessScalesShareAProcess` and `TestHeadlessCopyBGRA` each fail with their part removed, on both engines. "What a
        host outside package bui needs" in `docs/design.md` has the reasons.
  - [ ] Move the X11 host onto `Headless` and `bui.Host`, as a public package, with `NewWindow` and `Run` of its own. Then the same
        for Cocoa. Package `bui` then keeps no host, as in the fork.
  - [ ] Carry `SetScale` and `CopyBGRA` into the fork, as fresh commits on its `main`.
- [x] Carry the embedding API into the fork once it is complete, as fresh commits on the fork's `main`. (2026-11-12) —
      [CWBudde/BUI#5](https://github.com/CWBudde/BUI/pull/5) carries #21 to #23 as fresh commits, with the docs and a changelog
      entry. `just check` passes in `bindings/go`, and the 26 carried tests pass with and without cgo. (2026-11-13) — merged as
      `6954d0c`: there, `headless.go`, `headless_test.go`, `embed.go` and `embed_test.go` match bui-go's `main` apart from the
      import path.
- [ ] A release gate, as the owner asks: BUI's release workflow rebuilds the wasm from the release tag and runs the Go tests, and
      `tools/check_versions.py` checks the kit version that `bindings/go` pins.
- [ ] Tags `bindings/go/vX.Y.Z` with the number of the BUI release, made by `tools/release.py` rather than by hand.
- [ ] Startup time, frame time and memory against the native kit, in the form of the other bindings on the website's benchmarks
      page. `scripts/bench.sh` and `bench/` have frame times already.
- [ ] Generate the API from `BUI_kit.h` instead of writing it by hand, as the owner asks, so it can't drift from the kit. This
      touches the seam (`internal/engine`), both engines and `wasm/shim.c`. Christian decides the scope. He decided on 2026-11-12
      that it comes after PR 1 goes upstream.
- [ ] Open the upstream PRs on `ppmpreetham/BUI`. Christian decides when, after the owner answers the reply on BUI#8. Rebase onto
      upstream's `main` first: the fork's work sits on `e8126ca`, 113 commits behind upstream on 2026-11-12.
- [ ] After the merge, point bui-go's README at `bindings/go` for the binding. bui-go stays as the hosts module, as Christian
      decided on 2026-11-13, so it is not archived.

## Not planned yet

- Windows host: a port of `kit_window_win32.c`. Windows is built but has never been run.
- Native Wayland in the Go host.
- GPU rendering, video, the file browser and IME in the wasm engine.
- The kit's raw `KeyRelease` and `Button` on `Headless`. They reach only "events" nodes, which package `bui` has no handler for.
