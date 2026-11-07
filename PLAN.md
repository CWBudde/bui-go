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

Checked at BUI `e3aba90`, with wazero v1.12.0, wasi-sdk 34.0 and Go 1.26.8. Phase 0 turned most of the earlier
"verified by reading" rows into experiments; their numbers are in "Phase 0 results".

### Verified by experiment

| Fact                                                                                                                                                                                                           | Consequence for the plan                                                                                        |
| -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------- |
| A host function can call back into the same module's exports.                                                                                                                                                  | Handlers that call `SetStr` during an event work. Phase 0 did it through a real click on the kit.               |
| wazero recovers a panic in a host function and returns it from `Call` as an error. The caller never sees a panic.                                                                                              | `exitIfUnwinding` must stay inside the host function, and every export error is checked.                        |
| After `memory.grow`, `Memory().Read` returns a new backing array, so older views go stale.                                                                                                                     | Never keep a pixel slice across an export call.                                                                 |
| `go:embed ../lib/...` is rejected: `invalid pattern syntax`.                                                                                                                                                   | The `.wasm` must live in or below the embedding package, not in `lib/wasm/`.                                    |
| With cgo on and no cgo Go file in the package, a `.c` file fails: `C source files not allowed`.                                                                                                                | `bridge.c` and `import "C"` must always be selected together.                                                   |
| Native `Render` of a small tree at 400x300 takes about 64 µs, and create + render + close about 118 µs (i7-1255U). Phase 0 measured 40-75 µs, depending on the machine's load.                                 | The old 10 ms budget allowed about 150x native, so it could not fail. Budgets are now relative.                 |
| FreeType's `smooth.c` longjmps out of its rasterizer when the cell pool overflows. With exnref `setjmp` the jump lands on wazero's compiler and interpreter, and the bitmaps equal a native build bit for bit. | **The biggest risk is retired.** No Binaryen or Asyncify route is needed.                                       |
| wazero accepts only the new exception opcodes (`try_table`, `throw_ref`), behind the experimental `CoreFeaturesExceptionHandling`, on both engines. LLVM 23 still emits the legacy ones unless told otherwise. | `-mllvm -wasm-use-legacy-eh=false` is required at compile time, and the engine depends on an experimental flag. |
| Blender's containers (`BLI_vector.hh`, `BLI_map.hh`, `BLI_array.hh`) use `try`, `catch` and `throw`, so the C++ files do not compile with `-fno-exceptions`.                                                   | Corrects "libc++ must work without exceptions": C++ builds with `-fwasm-exceptions`, eh libc++ and `-lunwind`.  |
| The default font needs no file system: with no mounts at all, text renders pixel-identical to native.                                                                                                          | The fonts question is closed. Named families need a mount or `BUI_text_add_font`.                               |
| Image decoding runs only on worker threads, and `BLI_threadpool_insert` ignores a `pthread_create` failure. On wasm a request stays pending forever, although the file reads fine through WASI.                | Images need the upstream change in "Phase 0 results". Compiling formats out does not avoid the pending state.   |
| `find_package(Threads)` finds wasi-libc's stub `libpthread`. CMake sets `WASI` but not `UNIX` for wasi-sdk, so the X11 and `-lm` branches are skipped and `kit_window_null.c` is compiled.                     | The BUI tree builds unmodified with `BUI_GPU`, `BUI_VIDEO` and `BUI_FILEBROWSER` off.                           |
| The module imports only 13 WASI preview1 functions and `bui.emit`: no `dlopen`, no threads, no sockets.                                                                                                        | Nothing outside wazero's WASI is needed.                                                                        |
| wasi-libc defines no `FE_DIVBYZERO` or `FE_INVALID`, since wasm has no floating-point exception flags.                                                                                                         | `wasm/compat.h` defines them as 0. `EvalNumber` then accepts an inf or nan that turns finite, like `1/(1/0)`.   |
| wasi-sdk's prebuilt libc has a byte-by-byte `memcmp`, and the raster's damage pass compares every tile row of every frame with it.                                                                             | `wasm/compat.c` overrides it with a SIMD `memcmp`. It was half of a small render's time.                        |
| Rebuilding with `scripts/build-wasm.sh`, also from an empty `build/wasm`, gives byte-identical modules in the same checkout.                                                                                   | Phase 6's "identical bytes" check is realistic. Another checkout path is not tried yet.                         |

### Verified by reading, not run

| Fact                                                                                                                                             | Consequence for the plan                                                                  |
| ------------------------------------------------------------------------------------------------------------------------------------------------ | ----------------------------------------------------------------------------------------- |
| `sfnt.c`, `ftbase.c` and `format_jpeg.c` also use `setjmp`/`longjmp`, besides `smooth.c`.                                                        | Same mechanism as the tested path, so no extra risk is expected.                          |
| wazero's clock, sleep and random sources are fake by default.                                                                                    | `WithSysNanotime`, `WithSysWalltime`, `WithSysNanosleep` and `WithRandSource` are needed. |
| The committed libraries and `#cgo` link flags exist only for Linux amd64, but selecting on "cgo on" alone would pick the cgo engine everywhere.  | The cgo engine needs `cgo && linux && amd64`. Otherwise macOS with cgo fails to link.     |
| 196 objects are linked natively, of which 6 are C++: 3 in `core/intern`, 3 in `kit/intern/blender`. `interface_string_search.cc` uses `fstream`. | All 6 compile and link against wasi-sdk's libc++, with exceptions on (see above).         |
| The binding makes 28 kit calls plus 4 bridge calls, not "about 15". The X11 host adds about 15 more.                                             | The interface is split into a kit part and a host part.                                   |
| `bui_test.go` uses no C types. Its only private dependency on the handle code is `liveHandleCount`.                                              | Phase 1 test changes stay very small.                                                     |
| `Headless` has no public way to read pixels.                                                                                                     | Golden tests drive the engines directly, so the public API does not grow.                 |
| wazero is Apache-2.0, which is incompatible with GPLv2-only. BUI is GPL-2.0-or-later, and `LICENSE` holds the GPLv3 text.                        | A binary with the wasm engine is distributed under GPLv3. Document it.                    |

### Assumptions, after Phase 0

- wazero's compiler runs at about 2-4x native for rendering and hovering once the module is built with SIMD. Without
  SIMD and the `memcmp` fix it was 5-18x. The interpreter is 100-1000x native, far worse than "an order of magnitude".
- Cold compile takes 0.5-1.3 s with one compile worker and 0.2 s with four. The file cache brings the compiler's startup to about 25 ms.
- wasi-sdk's exnref `setjmp` works with wazero, and its stub pthreads are enough for the kit's mutex and condition calls:
  both now run in tests.

## Target architecture

```
package bui                 public API (unchanged); owns emitDepth, deferred frees, the Do queue. No cgo.
  engine_cgo.go             //go:build cgo && linux && amd64 && !bui_nocgo     selects cgoengine + its native window
  engine_nocgo.go           //go:build !(cgo && linux && amd64 && !bui_nocgo)  selects wasmengine + Go hosts
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

Riskiest step first, so a no-go comes early. Spike code stays out of the repo's main packages: the harness is the nested
module `spike/wasm`, and `scripts/build-wasm.sh` and `wasm/` stay for later phases.

- [x] Set up the toolchain
  - [x] Pin a full wasi-sdk release (34.0), with version and checksum recorded in `scripts/build-wasm.sh`
  - [x] Use wasi-sdk's own clang, not the system one, so that builds are reproducible
- [x] Prove `setjmp`/`longjmp` first, using only FreeType (`core` was not needed)
  - [x] Build with exnref (`-mllvm -wasm-enable-sjlj -mllvm -wasm-use-legacy-eh=false -lsetjmp`)
  - [x] Force the overflow path: render a complex glyph at a large size and confirm that the longjmp lands correctly
  - [x] Run on wazero's compiler and on its interpreter
  - [ ] If this fails, try Binaryen's translation (`wasm-opt --translate-to-exnref`), then Asyncify-based `setjmp`, then
        stop and take the fallback. Not needed: the first route worked.
- [x] Build `kit`, `gpu` and `core` for `wasm32-wasip1`
  - [x] Add `scripts/build-wasm.sh`: same pin, same clean-tree check and path stripping as `build-kit.sh`
  - [x] Set `BUI_GPU=OFF`, `BUI_VIDEO=OFF` and `BUI_FILEBROWSER=OFF`
  - [x] Check that `find_package(Threads REQUIRED)` and the `UNIX` branches in the CMake files accept WASI
  - [x] Check the 6 C++ files with `-fno-exceptions` against wasi-sdk's libc++. They do not compile that way; they build
        with `-fwasm-exceptions` instead (see results)
  - [x] Put remaining libc gaps in `wasm/compat.c` and `wasm/compat.h`; the BUI tree stays unmodified
- [x] Link a reactor module
  - [x] Use `-mexec-model=reactor`, run `_initialize`, and export only what the shim lists (`wasm/exports.txt` plus the
        shim's own `export_name` functions) plus `malloc` and `free`
  - [x] Set `-Wl,--stack-first` and an explicit `-z stack-size`, since the small default stack would silently overwrite
        data when it overflows, and nested emits share it
- [x] Decide how images work without threads
  - [x] Confirm by test that an image stays pending forever
  - [x] Check that the SVG icons do not take that path
  - [x] Preferred: propose an upstream option to decode inside `BK_image_poll` when no workers exist (text in results)
  - [ ] Otherwise: ship with `BUI_IMAGE_*` off and document it. Not taken: tested, it does not stop the pending state
- [x] Render from a throwaway Go program (a `go test` harness rather than a `main`)
  - [x] Create the counter example's tree, `BK_render` into a 400x300 raster, dump a PNG
  - [x] Drive one press and release that fires a handler which calls `SetStr` during the emit
- [x] Measure against the cgo build
  - [x] Pixels: count exact matches, and record the largest per-channel difference
  - [x] Size, render time, hover time, and cold and cached startup
- [x] Record the numbers and the decision in this file

**Go criteria:**

- [x] The rendered image matches: exact, or a per-channel difference of at most 2 on at most 0.1% of pixels. libm and
      compiler differences can shift antialiased edges by a step without any bug, so exact-only would fail for the wrong
      reason. Anything larger is a bug to explain. **Result: exact, on every tree and both engines.**
- [x] `bui_kit.wasm` is at most 8 MB, before optional `wasm-opt`. **Result: 3.29 MiB.**
- [x] Render and hover are at most 5x native on the same tree, and a full 1920x1080 frame of a dense tree stays under 16 ms.
      **Result, wazero's compiler: render 1.9-4.4x, hover 2.6-2.7x (medians); the 1080p frame took 7.3-8.1 ms in a
      quiet run (load not recorded; inferred from native's 3.3 ms), but 14-28 ms under a load of 18-22, when native itself took 9-15 ms.** The interpreter misses
      by far.
- [x] Startup is under 2 s cold and under 100 ms with the compilation cache. **Result: 0.49-1.33 s cold with one compile
      worker, 0.19-0.22 s with four, 23-25 ms from the file cache on the compiler, 53-56 ms on the interpreter.**
- [x] Text renders with no font files mounted. **Result: every comparison ran without a mount.**

**No-go fallback:** option A with purego. Phase 1 is useful either way, because it gives that engine the same seam.

#### Phase 0 results

**Verdict: go with conditions.** setjmp, exceptions, pixels, size, startup and text all work. Render and hover speed
meet the budget on wazero's compiler only with the build flags below, and the image loader needs an upstream change
before images can work. The conditions are listed at the end.

**Versions and machine.** BUI `e3aba90`, wasi-sdk 34.0 (clang 23.1.0, wasi-libc `2e6fb9d8ee0c`, archive SHA-256
`b761e3a0…b2c4`, computed on download because the release publishes no digest), wazero v1.12.0, Go 1.26.8. Intel
i7-1255U (12 threads), Linux 6.8. The laptop was in active use: other sessions' compiles pushed the load average to
3-22 during the runs. Native times moved by up to 3x with it, so only ratios from the same run are compared.

**Build** (`scripts/build-wasm.sh`, about 30 s with `-j4`):

- Compile: CMake `Release` (`-O3`; the kit forces `-O2`, as natively), `-msimd128`,
  `-mllvm -wasm-enable-sjlj -mllvm -wasm-use-legacy-eh=false`, `-include wasm/compat.h`, `-ffile-prefix-map`; C++ adds
  `-fwasm-exceptions`. `-wasm-enable-sjlj` implies the exception-handling target feature, so `-mexception-handling` is
  not needed. The `-mllvm` flags must reach the compile step: LLVM 23 still emits legacy `try` by default.
- Link: `-mexec-model=reactor -fwasm-exceptions -Wl,--stack-first -Wl,-z,stack-size=1048576 -Wl,--gc-sections
-Wl,--strip-debug -lsetjmp -lunwind`, and one `--export` per line of `wasm/exports.txt`.
- Runtime: `CoreFeaturesV2 | experimental.CoreFeaturesExceptionHandling`, real clocks, sleep and randomness, stderr, no
  mounts. The module imports 13 WASI preview1 functions and `bui.emit`, and exports 40 functions plus `memory`.
- The CMake options otherwise match `build-kit.sh`; image formats stay on (see the image decision).

**Sizes.** `bui_kit.wasm` 3,444,959 bytes (1,322,836 gzipped), 3,299,496 without SIMD, and 2,847,245 with every
`BUI_IMAGE_*` off. `sjlj_probe.wasm` 1,359,989. For comparison, the committed native `.a` files total 6.4 MB, with
GPU, video and the file browser in.

**setjmp/longjmp** (`TestSjljFreeTypeOverflow`). `@`, `&` and `Ж` at 2000 px and `%` at 1000 px overflow FreeType's
cell pool 4-14 times each in wasm (9-22 times natively, where a cell is bigger). Every bitmap hash equals the native
build of the same sources, on the compiler and the interpreter. A 16 px glyph takes the plain path, also identical.

**Pixels** (`TestGoldenPixels`, `TestEmitSetStrDuringRelease`). The counter tree at 400x300, the same tree hovered, four
icon buttons, a dense 8x40-widget tree at 1920x1080, and the counter after a click: every pixel is exact on both
engines (largest channel difference 0). PNGs go to `build/wasm/out/golden`.

**Events.** A press and release on the counter's button fires the click handler, which reads the slider and calls
`SetStr` on another node while the kit is inside `BK_release`; the label and the pixels then match native. The harness
gives every call nesting level its own `CallWithStack` slice, as a re-entrant call would otherwise clobber the outer
one's.

**Speed** (Go benchmarks, medians, ns/op unless noted; native is the committed cgo libraries):

| Measurement                                | native  | wasm compiler | ratio | wasm interpreter       |
| ------------------------------------------ | ------- | ------------- | ----- | ---------------------- |
| Render, counter 400x300, load ~3           | 39 µs   | 169 µs        | 4.3x  | -                      |
| Render, counter, load ~18                  | 57 µs   | 227 µs        | 4.0x  | 35.8 ms (about 600x)   |
| Render, counter, paired frames, load ~22   | 129 µs  | 405 µs        | 3.2x  | -                      |
| Render, dense 1920x1080, quiet (inferred)  | 3.3 ms  | 7.9 ms        | 2.4x  | -                      |
| Render, dense, load ~18                    | 12.9 ms | 25.1 ms       | 1.9x  | 1.2-1.6 s (about 110x) |
| Render, dense, paired frames, load ~22     | 9.4 ms  | 22.3 ms       | 2.4x  | -                      |
| Hover (motion + damage), counter, load ~18 | 40.6 µs | 104.7 µs      | 2.6x  | 5.3 ms                 |
| Hover, dense 1920x1080, load ~18           | 38 µs   | 103 µs        | 2.7x  | 5.4 ms                 |

"Render" is `BK_render` with `full = 1`, the call `Headless.Render` makes. The paired rows alternate one native and one
wasm frame per round (`TestPairedFrameTimes`), and their ratio is the median of 200 per-round ratios (dense: p10 1.7,
p90 3.4). The spike's native side calls the same C functions as package `bui`. `bui.Headless.Render` on a similar
tree measured 54 µs in a quiet run and 102-169 µs in the run at load ~18, which was slower than the spike's native kit
in that run (47-76 µs); not investigated, as it is the reference side, not the wasm one.

What the build changed (counter render, one interleaved run on a quiet machine, `BenchmarkVariants`):

| Module                                 | Counter render | vs native |
| -------------------------------------- | -------------- | --------- |
| scalar, wasi-libc `memcmp`             | 565 µs         | 14x       |
| `-msimd128`, wasi-libc `memcmp`        | 515 µs         | 13x       |
| `-msimd128`, SIMD `memcmp` in compat.c | 169 µs         | 4.3x      |

wasi-sdk's prebuilt libc compares byte by byte, and the raster's damage pass (`BUI_raster_end_frame`) `memcmp`s every
tile row of every frame, so `memcmp` was half of a small render. A per-function timing with wazero's listeners
(`TestRenderSelfTime`) found it; call counts are small (about 5,000 per counter frame), so call overhead is not the
cost. On the dense tree the scalar build was about 5x native and the final one 2-2.5x. LTO (`-flto`, with the sjlj
flags passed to `wasm-ld` too) changed nothing measurable (dense 7.8 against 7.9 ms) and was dropped. What remains is
the raster loops themselves (`BUI_raster_widget_base`, `BUI_raster_end_frame`, `BUI_raster_fill_rect`) and about 400k
out-of-line `fmaxf`/`fminf` calls per dense frame: wasm's `f32.max` has other NaN rules, so libc keeps them as calls.

**Startup** (`BenchmarkStartup`, new runtime + compile + instantiate + `_initialize`, load ~15):

| Engine      | cold, 1 compile worker | cold, 4 workers | cached (file cache) |
| ----------- | ---------------------- | --------------- | ------------------- |
| compiler    | 0.49 / 0.64 / 1.33 s   | 0.19-0.22 s     | 23-25 ms            |
| interpreter | 54-59 ms               | 52-56 ms        | 53-56 ms            |

The cached column opens a new cache over a warmed directory in every iteration (load ~2), so only the files on disk carry
over. An earlier version reused one warmed cache instance, which shares compiled code in memory, and reported 18-21 ms.
The interpreter gains nothing from the file cache: its cached and cold times are the same.

wazero compiles with one worker by default; `experimental.WithCompilationWorkers` cuts a cold start about 3x.

**Stack** (`TestStackPeak`). The dense 1080p render plus a click with a re-entrant handler peak at 18,560 bytes of the
1 MiB stack (1.8%). 1 MiB stays: linear memory is cheap, and deeper trees or larger glyphs use more.

**EvalNumber** (`TestEvalNumber`). Valid and invalid expressions agree, including `1/0`, `sqrt(-1)` and `log(0)`:
`BUI_expr_eval` rejects a non-finite result itself. Only an inf or nan that turns finite again differs: `1/(1/0)` and
`atan(1/0)` fail natively through the FPU flags, but on wasm they return 0 and π/2.

**Image decision.** `TestImageStaysPendingWithoutThreads` shows the premise: natively `quad.png` decodes and is handed
over within one poll. On both wasm engines it stays pending (1 request, 0 handed over, after 1 s of rendering and
polling), although `BK_image_info` reads the same file's header through WASI. The SVG icons do not use the loader:
`kit_draw.c` rasterizes them synchronously with `imb_svg_rasterize`, and the icon tree is pixel-exact even with every
`BUI_IMAGE_*` off. Turning the formats off does not help: the cache queues a request for any source before a format is
checked (`cache.c`, `lookup` → `request`), and a module built that way still kept the request pending forever. The
current Go API cannot set an image source at all, so this blocks no existing feature, only a future image API.

Decision: take the upstream route, and keep the formats on, as the module stays within budget with them. Until upstream
has it, an image source on the wasm engine never loads, and the known limits say so. The proposal for BUI, not filed:

> **Decode images inline when no worker thread starts.** On targets without threads (wasm32-wasip1, or wherever
> `pthread_create` fails), `bk_loader_push` queues requests that no worker ever takes: `BLI_threadpool_insert` drops the
> `pthread_create` error, `start()` sets `L.nthreads = n` regardless, and `BK_image_stats` reports the request as
> pending forever, with no error reaching the node.
>
> 1. `BLI_threadpool_insert` returns whether the thread started. Existing callers may ignore it.
> 2. The loader's `start()` counts the workers that started. If none did, it switches to inline mode.
> 3. In inline mode, `BK_image_poll` first decodes queued requests on the calling (UI) thread, visible before prefetch,
>    through the same `decode()` and `push_done()` a worker uses, until a time budget is spent (for example 8 ms,
>    settable through a new `BK_image_set_inline_budget(double ms)`). It then hands the done list over as today.
>    `BK_render` already calls `BK_image_poll` first, so headless hosts need no change, and window hosts that poll from
>    their loop get it too. `bk_loader_push` calls `bk_loader_notify` in inline mode, so a waiting loop wakes up to
>    poll.
> 4. Cancellation and priorities keep working, since requests stay in the same queues; the mutex and condition calls
>    stay, uncontended. Animations step through the same path, one frame per poll.
>
> This keeps threaded builds unchanged and turns "pending forever" into "decoded on the next frames".

**Surprises.**

- Blender's containers need real C++ exceptions, so the plan's `-fno-exceptions` check failed. The exnref dependency
  that setjmp brings anyway covers it.
- wasi-sdk's prebuilt libc is not SIMD-optimized, and its `memcmp` alone made the first build 18x slower than native on
  the counter tree. Rendering speed depends on such libc details more than on wazero.
- The interpreter is 100-1000x native, not "an order of magnitude". The counter tree renders in about 36 ms and hovers
  in about 5 ms, usable for a small tool, but a dense full-HD frame takes over a second.
- Pixels match exactly; the antialiasing tolerance was never needed.
- Compiling the image formats out does not stop the pending state.
- `build-wasm.sh`'s first version failed on a fresh checkout (the CMake log went to a directory not yet made). Fixed,
  and a run from an empty `build/wasm` then gave byte-identical modules.

**Conditions for go:**

1. Build as `scripts/build-wasm.sh` does: `-msimd128` and the SIMD `memcmp` in `wasm/compat.c` are part of the
   performance result. Pin wazero, as the module depends on its experimental exception handling.
2. Treat the compiler as the only performance target. Platforms on the interpreter are build and correctness targets.
3. Track the dense 1080p frame in Phase 4: at 2-2.5x native it fits 16 ms with the machine quiet, but not under load.
4. In Phase 2, use `experimental.WithCompilationWorkers` (four workers: 0.2 s cold) and the file compilation cache.
5. Images wait for the upstream change above. No Go image API is added before it lands.
6. Document the `EvalNumber` difference for an inf or nan that turns finite again.

**Reproduce.** `scripts/build-wasm.sh`, then in `spike/wasm`: `go test -count=1 .` (also with `CGO_ENABLED=0`, which
compares the two wasm engines only), `go test -run '^$' -bench 'Render|Hover|Startup' .`, and the opt-in diagnostics
`BUI_SPIKE_PROFILE=1 go test -run 'CallCounts|SelfTime|Paired' -v .` and
`BUI_SPIKE_VARIANTS=1 go test -run '^$' -bench Variants .`, with modules copied to `build/wasm/variants`.

### Phase 1: engine seam, no behaviour change

- [x] Define `internal/engine`
  - [x] `Kit`: the kit and bridge calls in Go types
  - [x] `Host`: window, loop, wake and quit
  - [x] An emit callback of `(id uint32, kind int, a float64)`
- [x] Move C code to `internal/engine/cgoengine`
  - [x] Move `bridge.c`, `bridge.h` and every `import "C"`
  - [x] Point `#cgo` flags at the root `lib/` and `include/`, and check that the `..` paths pass cgo's flag checks
  - [x] Replace `cgo.Handle` with an id table in package `bui`, which both engines share, and keep `liveHandleCount`
        working through it
- [x] Add the selection files with the `cgo && linux && amd64 && !bui_nocgo` rule
  - [x] Keep the unwinding guard at the point where the kit calls into Go, as `engine.ExitIfUnwinding`
- [x] Exit: the existing suite passes under `-race` and `-asan`, including the opt-in `TestX` run, with `bui_test.go`
      essentially unchanged

Corrections from this phase:

- **The file is `engine_nocgo.go`, not `engine_wasm.go`.** Go reads a `_wasm.go` suffix as `GOARCH=wasm`, so that file
  would never compile on a real host.
- **`cgo.Handle` is gone, not moved.** The kit's `ref` field holds a `uint32` id on both engines, so one table in package
  `bui` serves both, and no handle code is left to port. `liveHandleCount` is now the size of that table.
- **Kit objects cross the seam as opaque `uintptr` values** (`engine.Node`, `Context`, `Raster`, `Window`), separate from
  node ids. The tree walks in `Add`, `Close` and `free` pass through kit-internal nodes, which have no id.
- **The wake panic test moved.** `bui_test.go` can no longer call the guarded wake callback, so that case is now
  `TestWakePanicExits` in `cgoengine`. Only this case and `nil` checks that became `0` changed in `bui_test.go`.
- **Without the cgo engine the build still fails**, as it did before, with `undefined: wasmEngineArrivesInPhase2`.
  Phase 2 replaces that line.
- **`wsl_v5` now reports package `bui`.** It reported nothing there while the files had `import "C"`, so they picked up
  blank lines from `golangci-lint --fix`, which adds noise to this phase's diff.

### Phase 2: wasm engine, headless

- [x] Implement `wasmengine`
  - [x] Lazy runtime setup, with the compiler where supported and the interpreter elsewhere
  - [x] `CoreFeaturesV2 | experimental.CoreFeaturesExceptionHandling` (V2 includes the SIMD the module now uses)
  - [x] Compile with `experimental.WithCompilationWorkers` (Phase 0: four workers cut a cold start from 0.5-1.3 s to
        0.2 s), capped so a test binary does not swamp the machine
  - [x] Real clock, sleep and random sources, stderr wired through, and no file system mounts
  - [x] Compilation cache in `os.UserCacheDir()`, falling back without error if it is not writable
  - [x] Memory limit through `WithMemoryLimitPages`
- [ ] Implement the calls
  - [ ] String marshalling through `malloc` and `free`, reusing one scratch buffer
  - [ ] Give each call nesting level its own `CallWithStack` slice: handlers re-enter the module during an emit
  - [ ] Nodes, properties, geometry, `Add`, `Pack`, `Remove`, `Close`, events, `EvalNumber`
  - [ ] `Headless` render, press and release
  - [ ] Check every export error and treat it as fatal
- [ ] `Supported` returns false and `Run` returns `ErrNoDisplay` for now, as the API already defines
- [ ] Build the shared tests in `internal/enginetest`
  - [ ] Conformance suite that runs on each engine
  - [ ] Golden test comparing live pixels across trees (buttons, sliders, text, menus, scrolled views) at the Phase 0
        tolerance, in one test binary. Phase 0 got exact matches, so start strict and loosen only with a reason
  - [ ] Keep the `setjmp` probe (`wasm/sjlj_probe.c`) as a regression test for the exnref build
- [x] Commit `internal/engine/wasmengine/bui_kit.wasm`, as with the `.a` files
- [ ] Exit
  - [ ] `CGO_ENABLED=0 go test ./...` passes, with window tests skipped
  - [ ] `go test -tags bui_nocgo -race ./...` passes on a cgo machine
  - [ ] `CGO_ENABLED=0 GOOS={windows,darwin} go build ./...` succeeds

Notes from the runtime work:

- **A cache failure must retry the compile.** wazero checks only that the directory exists when the cache opens. A failed
  write or a corrupt entry then fails `CompileModule`, so `load` retries without a cache on any failure.
- **The interpreter fallback ran for real.** `CGO_ENABLED=0 GOARCH=386 go test ./internal/engine/wasmengine/` passes, and
  wazero has no compiler there, so the default configuration chose the interpreter by itself.
- **Compile workers, through `load`:** a median of 274 ms cold (12 runs, four workers) against 720 ms with wazero's default
  of one. No test can see the worker count, so this is a measurement, not a regression test.
- **The memory limit is 16384 pages (1 GiB).** Past it `malloc` returns 0 with no trap. Phase 4 decides what the binding
  does then.
- **The module is byte-identical on rebuild.** `build-wasm.sh` now copies it into the package, and re-running the script
  leaves `git status` clean in this checkout.
- **The golden test needs a new `Kit` call.** `engine.Kit` cannot read raster pixels yet, on either engine.

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
- No worker threads. Video and the file browser are off. An image source stays pending forever until BUI decodes
  inline when no worker starts (the proposal in "Phase 0 results"); the Go API sets no image sources today.
- Only the embedded Inter face works by default. Named families need a mount or `BUI_text_add_font`.
- Custom nodes with a C `draw` callback are not reachable; the Go binding does not expose them today.
- wazero's compiler covers amd64 and arm64 only. Other architectures use the interpreter, which Phase 0 measured at
  100-1000x native (a small tree renders in about 36 ms, a dense full-HD frame in over a second). They are build
  targets, not performance targets.
- `EvalNumber` accepts an expression whose intermediate inf or nan turns finite again, such as `1/(1/0)`, which fails
  natively: wasm has no floating-point exception flags.
- The module depends on an experimental wazero feature, so pin wazero and treat upgrades as deliberate changes.

## Open decisions

- [x] **Module location:** committed in-tree, consistent with `lib/` and a simple `go get`. Phase 0 size: 3.29 MiB
      (1.26 MiB gzipped) per rebuild, so each kit update adds about that much to the history.
- [ ] **Font mounts:** whether to expose a public way to add fonts, which would be new API, or stay with Inter for the
      first release (the default).

## Critical files

- **Modify:** `bui.go`, `node.go`, `window.go`, `headless.go`, `callback.go`, `README.md`, `justfile`, `.github/workflows/*`
- **Move:** `bridge.c`, `bridge.h` and the `import "C"` code to `internal/engine/cgoengine/`
- **New:** `internal/engine/`, `internal/enginetest/`, `internal/host/x11/`
- **From Phase 0:** `scripts/build-wasm.sh`, `wasm/shim.c`, `wasm/compat.c`, `wasm/compat.h`, `wasm/exports.txt`,
  `wasm/sjlj_probe.c`; `spike/wasm/` is throwaway and goes once `internal/enginetest` covers it
- **Reuse:** `scripts/build-kit.sh` (pin, clean-tree check, path stripping), `BUI_COMMIT`, `kit_window_null.c`

## Final verification

- [ ] `CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go test ./...` on Linux amd64
- [ ] `go test -race ./...` and `go test -tags bui_nocgo -race ./...`, to run both engines on one machine
- [ ] Golden comparison of cgo and wasm within the agreed tolerance
- [ ] `xvfb-run -a go test ./...` and `BUI_TEST_XDOTOOL=1 xvfb-run -a go test -run '^TestX' .` on the wasm engine
- [ ] `CGO_ENABLED=0 GOOS=windows go build ./...` and the same for `darwin`
- [ ] `ldd` on the built `examples/counter` reports "not a dynamic executable"
- [ ] Manual, under a real X session: run the counter example, hover, drag the slider, resize, close with the window button
