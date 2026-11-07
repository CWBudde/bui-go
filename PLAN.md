# Plan

Open work for bui-go, as of 2026-11-07. Finished work belongs in `docs/design.md` and the git history, not here.

## Where we stand

- [bui-go#7](https://github.com/CWBudde/bui-go/pull/7) is merged, so `main` has the wasm engine and the Go X11 host.
- [bui-go#8](https://github.com/CWBudde/bui-go/pull/8) is merged, so `main` pins BUI `v0.4.1`.
- BUI's owner wants the binding in-tree at `bindings/go`. We asked four questions on
  [BUI#8](https://github.com/ppmpreetham/BUI/issues/8#issuecomment-6707514269), about the committed wasm, the second X11 host,
  pinning and tags. **There is no answer yet, and Phase 3 depends on it.**
- [bui-go#9](https://github.com/CWBudde/bui-go/pull/9) is merged, so `main` has the macOS baseline.
- [bui-go#10](https://github.com/CWBudde/bui-go/pull/10) is merged, so `main` has the Cocoa spike. Christian counted its 1.6x as
  close and lifted the hold on the port, so the port goes ahead before the owner answers.
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
- [ ] Decide how to handle cgo and Go hosts disagreeing on a Wayland-only system: cgo opens a native Wayland window, while the Go
      host needs X11 or XWayland. Decide between documenting this and forcing `BUI_WINDOW_BACKEND=x11` in the cgo engine.
      Not reachable at `v0.4.1`, which predates BUI's Wayland host, so this is due at the first bump past that tag.
- [ ] Rewrite "Licensing" in `docs/design.md`, the README and `NOTICES.md`. BUI now states GPL-3.0-or-later, with
      `GPL-2.0-or-later` SPDX headers kept on files. The reading still has no legal review.
- [ ] Hover and drag with a real pointer on a real X session. So far these have only been driven on Xvfb.
- [ ] Decide on wasm containment: a trap or out-of-memory ends the process. Recommendation: keep that behaviour, and file an issue
      for an opt-in fatal hook.
- [ ] Make the load in `scripts/bench.sh` independent of the locale: on the German MacBook `sysctl` gives `7,05`. Set `LC_ALL=C`.

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
- [ ] From the spike: live resize (AppKit's nested loop), scale changes between screens, activation, a real click on the close button,
      and the layer and delegate leaked once per window. (2026-11-07) — partial: the first show activates the app, and `counter`
      came to the front over the editor and quit cleanly. The rest remains.
- [ ] A menu with Cmd+Q, Hide and the window items, as `app_init` in the C host builds.
- [ ] By hand on the MacBook: click, drag, scroll, type and copy and paste in `examples/counter` and a text field, as no test
      reaches the real mouse, keyboard or clipboard.
- [ ] A test job on `macos-latest` for whatever works without a logged-in session, plus a documented manual run on the MacBook.
- [ ] Update the README's platform table and "What has been run" in `docs/design.md`.

## Phase 3: Upstream as `bindings/go`

Starts when the owner answers on BUI#8. Their answers can change the scope below.

- [ ] Fork to `CWBudde/BUI`. Push over HTTPS with the `CWBudde` `gh` token, or load the key for the `github.com-cwbudde` SSH alias,
      which the agent doesn't hold at the moment. Check the commit email first.
- [ ] Move the module to `bindings/go`, under the path `github.com/ppmpreetham/BUI/bindings/go`, in fresh commits that link back here.
- [ ] Leave out `lib/linux_amd64/*.a` and make the cgo engine opt-in, using libraries built with CMake.
- [ ] Build `sjlj_probe.wasm` in the test step instead of committing it.
- [ ] Merge `NOTICES.md` and `licenses/` into `THIRD_PARTY_NOTICES.md` where they overlap, and keep the gaps marked.
- [ ] Add a Go CI job limited to `bindings/go/**`.
- [ ] PR 1: wasm engine, public API, `Headless`, enginetest.
- [ ] PR 2: X11 host, if question 2 gets a yes.
- [ ] PR 3: Cocoa host, if Phase 2 passes.
- [ ] Each PR updates `CHANGELOG.md`, `docs/`, the README's bindings table and the website, and lists its known limits.
- [ ] Before each PR, fetch the module from a scratch module at `github.com/CWBudde/BUI/bindings/go@<sha>` to prove it works outside
      the repo, and confirm BUI's `cmake`, `rust` and `package` jobs are unaffected.
- [ ] After the merge, archive bui-go with a pointer to `bindings/go`, or keep it as a mirror. Christian decides.

## Not planned yet

- Windows host: a port of `kit_window_win32.c`. Windows is built but has never been run.
- Native Wayland in the Go host.
- GPU rendering, video, the file browser and IME in the wasm engine.
