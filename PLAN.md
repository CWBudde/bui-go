# PLAN: a cgo-free path for bui-go

## Context

bui-go binds BUI's C kit (Blender's widgets as a retained-mode C runtime) through cgo and committed static libraries
(`lib/linux_amd64/*.a`, built by `scripts/build-kit.sh` from the commit in `BUI_COMMIT`). That costs us:

- a C/C++ toolchain on every build machine, so no `CGO_ENABLED=0`, no trivial cross-compilation, no `scratch` images;
- one committed `.a` set per OS/arch, and Linux amd64 is the only one so far;
- the UI-thread and `cgo.Handle` machinery in `bui.go`, `callback.go`, `bridge.c`.

**Goal:** `CGO_ENABLED=0 go build` works, on any `GOOS/GOARCH` for headless use and on Linux with a real window, with
the same public API as today. The cgo path stays, for the GPU window and as the reference for tests.

**Non-goals for the first release:** GPU rendering, video, the file browser, IME, and Windows or macOS windows.

## Approach

Compile the kit to WebAssembly and run it in [wazero](https://github.com/tetratelabs/wazero), a Go runtime with no cgo.
Write the window hosts in Go.

| Option                                                | cgo-free | Ships as                    | Verdict                                                                                                                 |
| ----------------------------------------------------- | -------- | --------------------------- | ----------------------------------------------------------------------------------------------------------------------- |
| **A. purego + shared lib** (`dlopen` `libbui_kit.so`) | yes      | a native library per target | Fast, but needs the C++ runtime on the target and a native lib per platform, so `go get` alone is not enough. Fallback. |
| **B. WebAssembly in wazero**                          | yes      | one embedded `.wasm`        | **Chosen.** One artifact for every OS/arch, and callbacks are ordinary Go host functions.                               |
| C. Pure-Go port of the kit                            | yes      | Go source                   | Rejected: 34k lines of kit code plus FreeType and the image decoders, impossible to keep up with upstream.              |

The split works because the kit renders in software into a `BuiRaster` and hosts only forward input and present pixels.
`kit_window_x11.c` calls only public kit functions, apart from `bk_window_wake_ready`, so a Go port is realistic.

## What the code and upstream say

Checked at BUI `e3aba90`, with wazero v1.12.0 and Go 1.26.8.

### Verified by experiment

| Fact                                                                                                               | Consequence for the plan                                                                        |
| ------------------------------------------------------------------------------------------------------------------ | ----------------------------------------------------------------------------------------------- |
| A host function can call back into the same module's exports.                                                      | Handlers that call `SetStr` during an event work.                                               |
| wazero recovers a panic in a host function and returns it from `Call` as an error. The caller never sees a panic.  | `exitIfUnwinding` must stay inside the host function, and every export error is checked.        |
| After `memory.grow`, `Memory().Read` returns a new backing array, so older views go stale.                         | Never keep a pixel slice across an export call.                                                 |
| `go:embed ../lib/...` is rejected: `invalid pattern syntax`.                                                       | The `.wasm` must live in or below the embedding package, not in `lib/wasm/`.                    |
| With cgo on and no cgo Go file in the package, a `.c` file fails: `C source files not allowed`.                    | `bridge.c` and `import "C"` must always be selected together.                                   |
| Native `Render` of a small tree at 400x300 takes about 64 µs, and create + render + close about 118 µs (i7-1255U). | The old 10 ms budget allowed about 150x native, so it could not fail. Budgets are now relative. |

### Verified by reading, not run

| Fact                                                                                                                                                                                                  | Consequence for the plan                                                                  |
| ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------- |
| FreeType needs real `setjmp`/`longjmp`: `smooth.c` longjmps out of the rasterizer when its cell pool overflows. `sfnt.c`, `ftbase.c` and `format_jpeg.c` also use them.                               | **Biggest technical risk.** A stub could trap on ordinary glyphs. See Phase 0.            |
| wazero supports only the new exception opcodes (`try_table`, `throw_ref`), behind the experimental `CoreFeaturesExceptionHandling`. Legacy opcodes are rejected. The interpreter implements them too. | The module must be built for exnref, and the engine depends on an experimental flag.      |
| The default font needs no file system: Inter is embedded (`gpu/extern/font_inter.h`), and `BUI_text_add_font` loads from memory, with a header comment naming wasm as its use.                        | The old fonts question is answered. Named families need a mount or `BUI_text_add_font`.   |
| Image decoding runs only on worker threads, and `BLI_threadpool_insert` ignores a `pthread_create` failure.                                                                                           | Without threads an image stays pending forever, with no error. See Phase 0.               |
| wazero's clock, sleep and random sources are fake by default.                                                                                                                                         | `WithSysNanotime`, `WithSysWalltime`, `WithSysNanosleep` and `WithRandSource` are needed. |
| Upstream has feature options: `BUI_GPU`, `BUI_VIDEO`, `BUI_FILEBROWSER`, `BUI_IMAGE_*`.                                                                                                               | `dlopen` (GL, GStreamer) and `getmntent` can go without patching BUI.                     |
| The committed libraries and `#cgo` link flags exist only for Linux amd64, but selecting on "cgo on" alone would pick the cgo engine everywhere.                                                       | The cgo engine needs `cgo && linux && amd64`. Otherwise macOS with cgo fails to link.     |
| 196 objects are linked, of which 6 are C++: 3 in `core/intern`, 3 in `kit/intern/blender`. `interface_string_search.cc` uses `fstream`, and two files have exception landing pads.                    | Corrects the earlier "8 files in core/gpu". libc++ must work without exceptions.          |
| The binding makes 28 kit calls plus 4 bridge calls, not "about 15". The X11 host adds about 15 more.                                                                                                  | The interface is split into a kit part and a host part.                                   |
| `bui_test.go` uses no C types. Its only private dependency on the handle code is `liveHandleCount`.                                                                                                   | Phase 1 test changes stay very small.                                                     |
| `Headless` has no public way to read pixels.                                                                                                                                                          | Golden tests drive the engines directly, so the public API does not grow.                 |
| wazero is Apache-2.0, which is incompatible with GPLv2-only. BUI is GPL-2.0-or-later, and `LICENSE` holds the GPLv3 text.                                                                             | A binary with the wasm engine is distributed under GPLv3. Document it.                    |

### Assumptions still to prove

- wazero runs at 2-4x native. This is a guess, and Phase 0 measures it.
- Cold module compile time is acceptable. wazero compiles the whole module up front, and a slow compile would hit every
  program start and every test binary.
- wasi-sdk's flags for exnref `setjmp` work with wazero's implementation.
- wasi-sdk's stub pthreads are enough for the mutex and condition calls in `bui_threads.c`.

## Target architecture

```
package bui                 public API (unchanged); owns emitDepth, deferred frees, the Do queue. No cgo.
  engine_cgo.go             //go:build cgo && linux && amd64 && !bui_nocgo     selects cgoengine + its native window
  engine_wasm.go            //go:build !(cgo && linux && amd64 && !bui_nocgo)  selects wasmengine + Go hosts
  internal/engine           interfaces only: Kit (nodes, geometry, render, input), Host (window, loop, wake)
  internal/engine/cgoengine //go:build cgo && linux && amd64     today's C calls, bridge.c, bridge.h
  internal/engine/wasmengine no constraint: pure Go; embeds bui_kit.wasm; wazero setup and marshalling
  internal/enginetest       shared conformance suite; golden test links both engines into one test binary
  internal/host/x11         Go port of kit_window_x11.c; win32 and cocoa later
wasm/                       repo-owned C: shim.c (exports, BKHost trampolines), compat.c (libc gaps)
```

Decisions behind this layout:

- **Both engines can link into one binary.** The earlier design selected one by tag, so its golden test could never
  compare them. `wasmengine` has no build constraint, and only the selection in package `bui` is tagged.
- **Package `bui` has no cgo at all**, which removes the `.c` file problem. `lib/` and `include/` stay at the root, so
  `build-kit.sh` keeps working.
- **`BK_window_*` comes from `kit_window_null.c`** in the module. The Go host owns the window, loop, timers and cursor.
- **A C shim is needed.** `BKHost` holds function pointers, so `shim.c` fills it with trampolines that call wazero
  imports. They pass scalars only, so Go never parses a C struct layout.
- **A node's identity is a `uint32` id in the kit's `ref` field.** `void *` is 32 bits in the module, so it cannot hold
  a `cgo.Handle`, and a small id table needs no map from offsets. The shim keeps the bridge's null-node and no-handle
  filtering, so those events never cross into Go.
- **`Do` wakes through a Go channel** on the wasm engine, since other goroutines must not touch the module. An X11 reader
  goroutine feeds the same `select` as timers and wakes, so no pipe is needed.
- **The module loads lazily** behind a `sync.Once`, so importing `bui` costs nothing until the first kit call.
- **A kit trap is fatal.** After a trap the C state is unknown, so any export error ends the process as a panic across
  C frames does today. It is not surfaced as a Go error.

## Phases

### Phase 0: feasibility spike (go/no-go)

Riskiest step first, so a no-go comes early. Spike code stays out of the repo's main packages.

- [ ] Set up the toolchain
  - [ ] Pin a full wasi-sdk release (34 is the latest today), with version and checksum recorded in `scripts/build-wasm.sh`
  - [ ] Use wasi-sdk's own clang, not the system one, so that builds are reproducible
- [ ] Prove `setjmp`/`longjmp` first, using only FreeType and `core`
  - [ ] Build with exnref (`-mllvm -wasm-enable-sjlj -mllvm -wasm-use-legacy-eh=false -lsetjmp`)
  - [ ] Force the overflow path: render a complex glyph at a large size and confirm that the longjmp lands correctly
  - [ ] Run on wazero's compiler and on its interpreter
  - [ ] If this fails, try Binaryen's translation (`wasm-opt --translate-to-exnref`), then Asyncify-based `setjmp`, then
        stop and take the fallback
- [ ] Build `kit`, `gpu` and `core` for `wasm32-wasip1`
  - [ ] Add `scripts/build-wasm.sh`: same pin, same clean-tree check and path stripping as `build-kit.sh`
  - [ ] Set `BUI_GPU=OFF`, `BUI_VIDEO=OFF` and `BUI_FILEBROWSER=OFF`
  - [ ] Check that `find_package(Threads REQUIRED)` and the `UNIX` branches in the CMake files accept WASI
  - [ ] Check the 6 C++ files with `-fno-exceptions` against wasi-sdk's libc++
  - [ ] Put remaining libc gaps in `wasm/compat.c`; the BUI tree stays unmodified
- [ ] Link a reactor module
  - [ ] Use `-mexec-model=reactor`, run `_initialize`, and export only what the shim lists plus `malloc` and `free`
  - [ ] Set `-Wl,--stack-first` and an explicit `-z stack-size`, since the small default stack would silently overwrite
        data when it overflows, and nested emits share it
- [ ] Decide how images work without threads
  - [ ] Confirm by test that an image stays pending forever
  - [ ] Check that the SVG icons do not take that path
  - [ ] Preferred: propose an upstream option to decode inside `BK_image_poll` when no workers exist
  - [ ] Otherwise: ship with `BUI_IMAGE_*` off and document it
- [ ] Render from a throwaway Go program
  - [ ] Create the counter example's tree, `BK_render` into a 400x300 raster, dump a PNG
  - [ ] Drive one press and release that fires a handler which calls `SetStr` during the emit
- [ ] Measure against the cgo build
  - [ ] Pixels: count exact matches, and record the largest per-channel difference
  - [ ] Size, render time, hover time, and cold and cached startup
- [ ] Record the numbers and the decision in this file

**Go criteria:**

- [ ] The rendered image matches: exact, or a per-channel difference of at most 2 on at most 0.1% of pixels. libm and
      compiler differences can shift antialiased edges by a step without any bug, so exact-only would fail for the wrong
      reason. Anything larger is a bug to explain.
- [ ] `bui_kit.wasm` is at most 8 MB, before optional `wasm-opt`.
- [ ] Render and hover are at most 5x native on the same tree, and a full 1920x1080 frame of a dense tree stays under 16 ms.
- [ ] Startup is under 2 s cold and under 100 ms with the compilation cache.
- [ ] Text renders with no font files mounted.

**No-go fallback:** option A with purego. Phase 1 is useful either way, because it gives that engine the same seam.

### Phase 1: engine seam, no behaviour change

- [ ] Define `internal/engine`
  - [ ] `Kit`: the 28 kit calls and 4 bridge calls in Go types
  - [ ] `Host`: window, loop, wake and quit
  - [ ] An emit callback of `(id uint32, kind int, a float64)`
- [ ] Move C code to `internal/engine/cgoengine`
  - [ ] Move `bridge.c`, `bridge.h` and every `import "C"`
  - [ ] Point `#cgo` flags at the root `lib/` and `include/`, and check that the `..` paths pass cgo's flag checks
  - [ ] Move `cgo.Handle` use inside the engine, and keep `liveHandleCount` working through it
- [ ] Add the selection files with the `cgo && linux && amd64 && !bui_nocgo` rule
  - [ ] Keep `exitIfUnwinding` at the point where the kit calls into Go
- [ ] Exit: the existing suite passes under `-race` and `-asan`, including the opt-in `TestX` run, with `bui_test.go`
      essentially unchanged

### Phase 2: wasm engine, headless

- [ ] Implement `wasmengine`
  - [ ] Lazy runtime setup, with the compiler where supported and the interpreter elsewhere
  - [ ] `CoreFeaturesV2 | experimental.CoreFeaturesExceptionHandling`
  - [ ] Real clock, sleep and random sources, stderr wired through, and no file system mounts
  - [ ] Compilation cache in `os.UserCacheDir()`, falling back without error if it is not writable
  - [ ] Memory limit through `WithMemoryLimitPages`
- [ ] Implement the calls
  - [ ] String marshalling through `malloc` and `free`, reusing one scratch buffer
  - [ ] Nodes, properties, geometry, `Add`, `Pack`, `Remove`, `Close`, events, `EvalNumber`
  - [ ] `Headless` render, press and release
  - [ ] Check every export error and treat it as fatal
- [ ] `Supported` returns false and `Run` returns `ErrNoDisplay` for now, as the API already defines
- [ ] Build the shared tests in `internal/enginetest`
  - [ ] Conformance suite that runs on each engine
  - [ ] Golden test comparing live pixels across trees (buttons, sliders, text, menus, scrolled views) at the Phase 0
        tolerance, in one test binary
- [ ] Commit `internal/engine/wasmengine/bui_kit.wasm`, as with the `.a` files
- [ ] Exit
  - [ ] `CGO_ENABLED=0 go test ./...` passes, with window tests skipped
  - [ ] `go test -tags bui_nocgo -race ./...` passes on a cgo machine
  - [ ] `CGO_ENABLED=0 GOOS={windows,darwin} go build ./...` succeeds

### Phase 3: Linux window host in Go

- [ ] Pick the X11 client
  - [ ] Start with `jezek/xgb` (v1.3.1, BSD), pure Go with `shm` and `render` packages
  - [ ] Check keyboard mapping and `XIM`-free text entry early; fall back to purego over libX11 only if it proves unworkable
- [ ] Extend `Kit` with the input calls the host needs
  - [ ] `BK_motion`, `BK_wheel`, `BK_scroll`, `BK_key`, `BK_key_release`, `BK_button`, `BK_context_menu`
  - [ ] `BK_timer`, `BK_animating`, `BK_needs_render`, `BK_set_scale`, `BK_image_poll`, `BUI_raster_damage`
  - [ ] The rest of the `BKHost` trampolines: `request`, `set_cursor`, `warp`, clipboard, `schedule`
- [ ] Port `kit_window_x11.c` (1087 lines)
  - [ ] Window creation, close button, resize, and `Xft.dpi` scale
  - [ ] Expose and damage presentation with `PutImage`, then MIT-SHM if frame times call for it
  - [ ] Cursors from the `BK_CURSOR_*` set, and pointer warp
  - [ ] Clipboard
  - [ ] Keysym and text translation for `BK_key` (highest risk)
  - [ ] Drag and wheel behaviour matching the C host
- [ ] Build the loop
  - [ ] One `select` over X events, timers, frame requests and the `Do` wake channel
  - [ ] Keep the documented panic rule: a panic in a handler or `Do` function in the loop ends the process
- [ ] Exit
  - [ ] `CGO_ENABLED=0 go run ./examples/counter` works under a real X session
  - [ ] `ldd` on that binary reports "not a dynamic executable"
  - [ ] `xvfb-run -a go test ./...` and `BUI_TEST_XDOTOOL=1 xvfb-run -a go test -run '^TestX' .` pass on the wasm engine

### Phase 4: hardening

- [ ] Performance
  - [ ] Benchmark render, hover and event latency on both engines, with results tracked over time
  - [ ] Avoid allocation per kit call
- [ ] Memory
  - [ ] Add a leak test for create/close loops, comparing module memory after a warm-up
  - [ ] Test resize loops, because linear memory never shrinks, and document that
  - [ ] Decide what an out-of-memory `malloc` does once it hits the page limit
- [ ] Failure behaviour
  - [ ] Test a panic and a `Goexit` in a handler on the wasm engine (the `TestPanicAcrossCFramesExits` equivalent)
  - [ ] Test that a kit trap ends the process with a useful message
  - [ ] Document that wasm has no guard pages, so a NULL read or a bad offset inside the module may go undetected
- [ ] Fuzz `SetStr` and `EvalNumber` inputs on both engines, and compare the results

### Phase 5: other platforms (separate, optional)

Do not start before Phase 3 proves the `Host` interface. Headless works everywhere after Phase 2.

- [ ] Windows host through `syscall` and `x/sys/windows`, porting `kit_window_win32.c` (987 lines)
- [ ] macOS host through purego and the Objective-C runtime, porting `kit_window_cocoa.m` (1921 lines)

### Phase 6: CI, docs, release

- [ ] CI
  - [ ] Cgo engine with `-race`; the `-asan` and `xdotool` runs move from manual into CI if they are stable
  - [ ] Wasm engine on Linux, with `-race` through the `bui_nocgo` tag, and `CGO_ENABLED=0` without it
  - [ ] Build `CGO_ENABLED=0 GOOS={linux,windows,darwin} GOARCH={amd64,arm64}`
  - [ ] Test with cgo **on** on Linux arm64 and macOS, which must select the wasm engine and link
  - [ ] Rebuild `bui_kit.wasm` from `BUI_COMMIT` and require identical bytes
  - [ ] Check the `.wasm` size budget
- [ ] README and package docs
  - [ ] Replace "Linux amd64 only" and the "Linux cgo spike" package comment with an engine table
  - [ ] Document `bui_nocgo`, the compilation cache, the limits below, and the interpreter's speed on other architectures
- [ ] Licensing
  - [ ] State that a binary with the wasm engine is distributed under GPLv3, because of Apache-2.0 wazero
  - [ ] Give the `.wasm`'s corresponding source: BUI at `BUI_COMMIT`, `wasm/`, `build-wasm.sh`, and the wasi-sdk version
  - [ ] Add notices for wazero, xgb, wasi-libc, libc++ and the embedded Inter font
- [ ] Tag a first release once Phase 3 is green

## Known limits of the cgo-free path

- No GPU window (`BK_window_set_gpu`), software raster only.
- No worker threads. Video and the file browser are off, and images depend on the Phase 0 decision.
- Only the embedded Inter face works by default. Named families need a mount or `BUI_text_add_font`.
- Custom nodes with a C `draw` callback are not reachable; the Go binding does not expose them today.
- wazero's compiler covers amd64 and arm64 only. Other architectures use the interpreter, which its README says is often
  an order of magnitude slower. They are build targets, not performance targets.
- The module depends on an experimental wazero feature, so pin wazero and treat upgrades as deliberate changes.

## Open decisions

- [ ] **Module location:** committed in-tree (the default: consistent with `lib/`, simple `go get`) or a release asset.
      Decide after Phase 0 gives the real size.
- [ ] **Font mounts:** whether to expose a public way to add fonts, which would be new API, or stay with Inter for the
      first release (the default).

## Critical files

- **Modify:** `bui.go`, `node.go`, `window.go`, `headless.go`, `callback.go`, `README.md`, `justfile`, `.github/workflows/*`
- **Move:** `bridge.c`, `bridge.h` and the `import "C"` code to `internal/engine/cgoengine/`
- **New:** `internal/engine/`, `internal/enginetest/`, `internal/host/x11/`, `scripts/build-wasm.sh`, `wasm/shim.c`,
  `wasm/compat.c`
- **Reuse:** `scripts/build-kit.sh` (pin, clean-tree check, path stripping), `BUI_COMMIT`, `kit_window_null.c`

## Final verification

- [ ] `CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go test ./...` on Linux amd64
- [ ] `go test -race ./...` and `go test -tags bui_nocgo -race ./...`, to run both engines on one machine
- [ ] Golden comparison of cgo and wasm within the agreed tolerance
- [ ] `xvfb-run -a go test ./...` and `BUI_TEST_XDOTOOL=1 xvfb-run -a go test -run '^TestX' .` on the wasm engine
- [ ] `CGO_ENABLED=0 GOOS=windows go build ./...` and the same for `darwin`
- [ ] `ldd` on the built `examples/counter` reports "not a dynamic executable"
- [ ] Manual, under a real X session: run the counter example, hover, drag the slider, resize, close with the window button
