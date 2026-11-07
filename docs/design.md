# Design of the wasm engine

Why bui-go runs BUI's kit as WebAssembly, what that was measured to cost, and what it cannot do. The README covers use; this
file holds the reasons and the numbers behind it.

Unless a section says otherwise, the facts here were established against BUI `e3aba90`, wazero v1.12.0, wasi-sdk 34.0
(clang 23.1.0, wasi-libc `2e6fb9d8ee0c`) and Go 1.26.8, on an Intel i7-1255U (12 threads) running Linux 6.8. That laptop was
in active use, so absolute times move with its load, and only ratios from one run compare well.

BUI is now pinned at `v0.4.1` (`09a154c`). Since `e3aba90` its kit gained `BK_version` and its version macros, and resetting a widget
now always goes back to its default, which is the first value it was given unless one is set. The measurements here were not
repeated at the new pin.

## Why WebAssembly

Binding through cgo needs a C and C++ toolchain on every build machine, rules out `CGO_ENABLED=0`, plain cross-compilation
and `scratch` images, and takes one set of committed `.a` files per OS and architecture. The goal was `CGO_ENABLED=0 go build`
on any `GOOS`/`GOARCH` for headless use and on Linux with a real window, with the same public API. The cgo engine stays as the
reference for the tests.

| Option                         | Ships as                    | Verdict                                                                                                           |
| ------------------------------ | --------------------------- | ----------------------------------------------------------------------------------------------------------------- |
| A. purego and a shared library | a native library per target | Fast, but needs the C++ runtime on the target and a native library per platform, so `go get` alone is not enough. |
| **B. WebAssembly in wazero**   | one embedded `.wasm`        | **Chosen.** One artifact for every OS and architecture, and callbacks are ordinary Go host functions.             |
| C. Pure-Go port of the kit     | Go source                   | Rejected: 34k lines of kit code plus FreeType and the image decoders, impossible to keep up with upstream.        |

The split works because the kit renders in software into a `BuiRaster`, and hosts only forward input and present pixels.
`kit_window_x11.c` calls only public kit functions, apart from `bk_window_wake_ready`, which made a Go port of the host
realistic.

GPU rendering, video, the file browser, IME, and windows on Windows and macOS were out of scope from the start.

## Architecture

```
package bui                  public API; owns emitDepth, deferred frees, the Do queue. No cgo.
  engine_cgo.go              //go:build cgo && linux && amd64 && !bui_nocgo
  engine_nocgo.go            //go:build !(cgo && linux && amd64 && !bui_nocgo)
  internal/engine            interfaces only: Kit (nodes, geometry, render, input), Host (window, loop, wake)
  internal/engine/cgoengine  //go:build cgo && linux && amd64; the C calls, bridge.c, bridge.h
  internal/engine/wasmengine no constraint: pure Go; embeds bui_kit.wasm; wazero setup and marshalling
  internal/enginetest        shared conformance suite; its golden test links both engines into one test binary
  internal/host/x11          Go port of kit_window_x11.c
wasm/                        repo-owned C: shim.c (exports, BKHost trampolines), compat.c (libc gaps)
```

Decisions behind it:

- **Both engines can link into one binary,** so a test can compare them. `wasmengine` has no build constraint, and only the
  selection in package `bui` is tagged.
- **The cgo engine needs `cgo && linux && amd64`, not just cgo,** as the committed libraries and link flags exist only for
  that target. Selecting on "cgo on" alone would fail to link on macOS.
- **The file is `engine_nocgo.go`, not `engine_wasm.go`.** Go reads a `_wasm.go` suffix as `GOARCH=wasm`, so that file would
  never compile on a real host.
- **Package `bui` has no cgo at all.** With cgo on and no cgo Go file in a package, a `.c` file fails with
  `C source files not allowed`, so `bridge.c` and `import "C"` always move together.
- **`.wasm` lives in the package that embeds it.** `go:embed ../lib/...` is rejected as `invalid pattern syntax`, and committing
  it matches `lib/` and keeps `go get` simple. Each kit update adds about 1.3 MB gzipped to the history.
- **A node's identity is a `uint32` id in the kit's `ref` field.** `void *` is 32 bits in the module, so it cannot hold a
  `cgo.Handle`, and one id table in package `bui` serves both engines. Kit objects cross the seam as opaque `uintptr` values,
  separate from node ids, because tree walks pass through kit-internal nodes that have no id.
- **`BK_window_*` comes from `kit_window_null.c` in the module.** The Go host owns the window, loop, timers and cursor.
- **A C shim is needed.** `BKHost` holds function pointers, so `shim.c` fills it with trampolines that call wazero imports.
  They pass scalars only, so Go never parses a C struct layout. The shim keeps the bridge's null-node and no-handle filtering,
  so those events never cross into Go.
- **`Do` wakes through a Go channel** on the wasm engine, since other goroutines must not touch the module. An X11 reader
  goroutine feeds the same `select` as timers and wakes, so no pipe is needed.
- **The module loads lazily** behind a `sync.Once`, so importing `bui` costs nothing until the first kit call.
- **A kit trap is fatal.** After a trap the C state is unknown, so any export error ends the process, as a panic across C
  frames does. It is not surfaced as a Go error.
- **Each call nesting level has its own `api.Function`, `CallWithStack` slice and scratch buffer,** as handlers re-enter the
  module during an emit. A wazero function keeps one native stack, so re-entering the same one overwrites the call in
  progress and ends in `fatal error: stack overflow` on the compiler. `level` in `kit.go` holds them.

## Building the module

`scripts/build-wasm.sh` builds the unmodified BUI tree. The reasons behind its flags:

- **Compile:** `-mllvm -wasm-enable-sjlj -mllvm -wasm-use-legacy-eh=false`, as FreeType needs `setjmp` and wazero accepts only
  the new exception opcodes (`try_table`, `throw_ref`). LLVM 23 still emits the legacy ones by default, and the flags must
  reach the compile step. `-wasm-enable-sjlj` implies the exception-handling target feature.
- **C++:** `-fwasm-exceptions`, plus eh libc++ and `-lunwind` at link time. Blender's containers (`BLI_vector.hh`,
  `BLI_map.hh`, `BLI_array.hh`) use `try`, `catch` and `throw`, so these files do not compile with `-fno-exceptions`.
- **`-msimd128`, and the SIMD `memcmp` in `wasm/compat.c`,** are part of the performance result (see "What the build changed").
- **Link:** `-mexec-model=reactor`, `-Wl,--stack-first` and an explicit 1 MiB stack, since the default stack is small and an
  overflow would silently overwrite data. With the stack first, an overflow traps.
- **LTO was tried and dropped:** `-flto` changed nothing measurable (dense frame 7.8 against 7.9 ms).
- **Options:** `BUI_GPU`, `BUI_VIDEO` and `BUI_FILEBROWSER` are off. CMake finds wasi-libc's stub `libpthread` through
  `find_package(Threads)`, and it sets `WASI` but not `UNIX` for wasi-sdk, so the X11 and `-lm` branches are skipped and
  `kit_window_null.c` is compiled.
- **Runtime:** `CoreFeaturesV2 | experimental.CoreFeaturesExceptionHandling`, which depends on an experimental wazero feature,
  so wazero is pinned. wazero's clock, sleep and random sources are fake by default, so the real ones are set. stderr is wired
  through, and nothing is mounted.

wasi-sdk's release publishes no digest, so the SHA-256 in `build-wasm.sh` was computed on download.

The module rebuilds byte-identically, also from another checkout path with BUI fetched afresh, which CI relies on. It imports
13 WASI preview1 functions and 7 of its own (`bui.emit` and the `bui.host_*` calls): no `dlopen`, threads or sockets, so nothing
outside wazero's WASI is needed.

## What experiments established

| Fact                                                                                                                                                                                                           | Consequence                                                                                                           |
| -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------- |
| A host function can call back into the same module's exports.                                                                                                                                                  | Handlers that call `SetStr` during an event work.                                                                     |
| wazero recovers a panic in a host function and returns it from `Call` as an error. The caller never sees a panic.                                                                                              | The check for unwinding stays inside the host function (`engine.ExitIfUnwinding`), and every export error is checked. |
| After `memory.grow`, `Memory().Read` returns a new backing array, so older views go stale.                                                                                                                     | Never keep a pixel slice across an export call.                                                                       |
| FreeType's `smooth.c` longjmps out of its rasterizer when the cell pool overflows. With exnref `setjmp` the jump lands on wazero's compiler and interpreter, and the bitmaps equal a native build bit for bit. | No Binaryen or Asyncify route is needed. `wasm/sjlj_probe.c` stays as a regression test.                              |
| The default font needs no file system: with no mounts at all, text renders pixel-identical to native.                                                                                                          | Named families need a mount or `BUI_text_add_font`.                                                                   |
| wasi-libc defines no `FE_DIVBYZERO` or `FE_INVALID`, since wasm has no floating-point exception flags.                                                                                                         | `wasm/compat.h` defines them as 0 (see "EvalNumber").                                                                 |
| wasi-sdk's prebuilt libc has a byte-by-byte `memcmp`, and the raster's damage pass compares every tile row of every frame with it.                                                                             | `wasm/compat.c` overrides it with a SIMD `memcmp`. It was half of a small render's time.                              |
| A failed write or a corrupt entry in wazero's compilation cache fails `CompileModule`, as the cache checks only that its directory exists when it opens.                                                       | `load` retries without a cache on any failure.                                                                        |

Read in the code rather than run: `sfnt.c`, `ftbase.c` and `format_jpeg.c` also use `setjmp`/`longjmp`, by the same mechanism as
the tested path. Natively 196 objects are linked, of which 6 are C++.

## Measurements

Numbers without a date are from 2026-11-06, taken with a throwaway harness that is no longer in the repository.
`scripts/bench.sh` records the render, hover, click and `SetNum` numbers for tracking, in `bench/`.

### Feasibility criteria

These decided whether to go ahead. The size budget stays enforced by `scripts/check-wasm.sh`.

| Criterion                                                                    | Result, on wazero's compiler                                                                                         |
| ---------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------- |
| Pixels exact, or a channel difference of at most 2 on at most 0.1% of pixels | Exact on every tree and both wazero engines. The tolerance was never needed.                                         |
| `bui_kit.wasm` at most 8 MB, before any `wasm-opt`                           | 3.29 MiB. On 2026-11-07, with the input calls added, 3,455,870 bytes.                                                |
| Render and hover at most 5x native; a dense 1920x1080 frame under 16 ms      | Render 1.9-4.4x, hover 2.6-2.7x (medians). The frame took 7.3-8.1 ms in a quiet run, 14-28 ms under a load of 18-22. |
| Startup under 2 s cold and under 100 ms cached                               | See "Startup".                                                                                                       |
| Text renders with no font files mounted                                      | Every comparison ran without a mount.                                                                                |

The compiler is the only performance target. It covers amd64 and arm64; other architectures use the interpreter, and are
build and correctness targets. The interpreter misses the speed criteria by far.

### Speed

Medians. "Render" is `BK_render` with `full = 1`, the call `Headless.Render` makes. Native times moved by up to 3x with the
load, so compare ratios within a row. The paired rows alternate one native and one wasm frame per round, and their ratio is the
median of 200 per-round ratios (dense: p10 1.7, p90 3.4).

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

What remains on the compiler is the raster loops themselves (`BUI_raster_widget_base`, `BUI_raster_end_frame`,
`BUI_raster_fill_rect`) and about 400k out-of-line `fmaxf`/`fminf` calls per dense frame: wasm's `f32.max` has other NaN
rules, so libc keeps them as calls.

Baseline from 2026-11-07 (`bench/2026-11-07-05fdf4f.txt`; 200 iterations, three runs, load falling from 14 to 5):

| Measurement                       | native | wasm compiler | ratio |
| --------------------------------- | ------ | ------------- | ----- |
| Render, counter 400x300           | 92 µs  | 308 µs        | 3.4x  |
| Render, dense 1920x1080           | 4.0 ms | 9.5 ms        | 2.4x  |
| Hover and leave, counter          | 74 µs  | 104 µs        | 1.4x  |
| Hover and leave, dense            | 70 µs  | 83 µs         | 1.2x  |
| Press and release to a Go handler | 322 ns | 756 ns        | 2.3x  |
| `SetNum`                          | 262 ns | 268 ns        | 1.0x  |

Press and release in that benchmark are cheap because nothing renders in between: it measures the crossing into a handler and
back, not a whole click as a user sees it.

On arm64, from 2026-11-07 (`bench/2026-11-07-a06371a.txt`; Apple M5 Pro, macOS 27.2, Go 1.27.1, 200 iterations, three runs, load 4-5
on 15 cores, medians):

| Measurement                       | wasm compiler |
| --------------------------------- | ------------- |
| Render, counter 400x300           | 86 µs         |
| Render, dense 1920x1080           | 4.4 ms        |
| Hover and leave, counter          | 47 µs         |
| Hover and leave, dense            | 47 µs         |
| Press and release to a Go handler | 342 ns        |
| `SetNum`                          | 85 ns         |

There is no native column: the native engine needs Linux amd64, so this machine has no ratio. These numbers show the module's speed
on arm64 and aren't comparable with the amd64 table, which is another CPU at another load.

Hot calls do not allocate. `TestHotCallsDoNotAllocate` and `TestEmitDoesNotAllocate` hold it at 0 for input, render, properties
and an emit with a call from its handler. `RasterDamage` allocates its result, once per damaged frame.

Long drawn text is very expensive, and not profiled further. One 2000-character dropdown item took a test from 0.13 s to 45 s
on the interpreter, and past 10 minutes under `-race`, as the kit lays it out on every render. Long strings in the tests are not
drawn.

### What the build changed

Counter render, one interleaved run on a quiet machine:

| Module                                 | Counter render | vs native |
| -------------------------------------- | -------------- | --------- |
| scalar, wasi-libc `memcmp`             | 565 µs         | 14x       |
| `-msimd128`, wasi-libc `memcmp`        | 515 µs         | 13x       |
| `-msimd128`, SIMD `memcmp` in compat.c | 169 µs         | 4.3x      |

Call counts are small (about 5,000 per counter frame), so call overhead is not the cost. On the dense tree the scalar build was
about 5x native and the final one 2-2.5x. Rendering speed depends on such libc details more than on wazero.

### Startup

New runtime, compile, instantiate and `_initialize`, at load ~15:

| Engine      | cold, 1 compile worker | cold, 4 workers | cached (file cache) |
| ----------- | ---------------------- | --------------- | ------------------- |
| compiler    | 0.49 / 0.64 / 1.33 s   | 0.19-0.22 s     | 23-25 ms            |
| interpreter | 54-59 ms               | 52-56 ms        | 53-56 ms            |

The cached column opens a new cache over a warmed directory in every iteration, at load ~2, so only the files on disk carry
over. Reusing one warmed cache instance shares compiled code in memory, and reads a misleading 18-21 ms. The interpreter gains
nothing from the file cache.

wazero compiles with one worker by default. Through the real `load`, on 2026-11-07, four workers took a median of 274 ms cold
(12 runs) against 720 ms with one. No test can see the worker count, so this is a measurement, not a regression test.

### Sizes

On 2026-11-06: `bui_kit.wasm` 3,444,959 bytes (1,322,836 gzipped), 3,299,496 without SIMD, and 2,847,245 with every
`BUI_IMAGE_*` off. `sjlj_probe.wasm` 1,359,989. The committed native `.a` files total 6.4 MB, with GPU, video and the file
browser in.

### Stack

A dense 1080p render plus a click with a re-entrant handler peak at 18,560 bytes of the 1 MiB stack (1.8%). 1 MiB stays: linear
memory is cheap, and deeper trees or larger glyphs use more.

### Pixels

The cgo and wasm engines match exactly: all 22 golden stages across 7 trees are 100.0000% exact with a largest channel difference of 0, including
an open menu, a scrolled view and a 1080p tree (2026-11-07, `internal/enginetest`). So are wazero's compiler and interpreter.
A glyph forced through FreeType's overflow path overflows the cell pool 4-14 times in wasm and 9-22 times natively, where a cell
is bigger, and still gives an identical bitmap.

### Presenting

Presenting a full frame with `PutImage` took 342 µs at 400x300 and 3.8 ms at 1920x1080 in the 2026-11-07 baseline run, and 1.6
and 11.9 ms in a busier one (Xvfb, round trip included, 100 iterations). So a full 1080p frame, render plus present, took about
13 ms: this fits 16 ms when the machine is not loaded. A full frame happens on an expose or a resize, and damage covers the rest,
so nothing in the API calls for MIT-SHM today.

### Fuzzing

On 2026-11-07, at 40 s each, the engines showed no difference: `FuzzEvalNumber` 548k executions, `FuzzSetStr` 1.76M,
`FuzzKeyInput` 217k (`go test -fuzz` in `internal/enginetest`, which needs cgo). `FuzzEvalNumber` accepts native failing where
wasm succeeds only for text that can make an inf or nan, the case in "EvalNumber", so a parser bug in such text would pass. It
compares numbers to 1e-9, relative. CI runs each target for 20 s, and `internal/enginetest/testdata/fuzz` holds a corpus.
`FuzzKeyInput` skips input over 512 bytes: drawing 4 KB of text in one field took seconds, and a worker died in a stall like that.
That stall's exact cause was not established.

## Images without threads

Image decoding runs only on worker threads, and the module has none, so an image source can never load on the wasm engine. The
Go API sets no image source today, so this blocks no existing feature, only a future image API. That API should wait for the
upstream change below.

Natively `quad.png` decodes and is handed over within one poll. On both wazero engines it stayed pending (1 request, 0 handed
over, after 1 s of rendering and polling), although `BK_image_info` reads the same file's header through WASI.

- **The SVG icons do not use the loader.** `kit_draw.c` rasterizes them synchronously with `imb_svg_rasterize`, and the icon
  tree is pixel-exact even with every `BUI_IMAGE_*` off.
- **Compiling the formats out does not help.** The cache queues a request for any source before a format is checked (`cache.c`,
  `lookup` to `request`), and a module built that way still kept the request pending forever. So the formats stay on, as the
  module stays within budget with them.

The proposal for BUI, not filed:

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

## EvalNumber

Valid and invalid expressions agree across the engines, including `1/0`, `sqrt(-1)` and `log(0)`, as `BUI_expr_eval` rejects a
non-finite result itself. Only an inf or nan that turns finite again differs: `1/(1/0)` and `atan(1/0)` fail natively through
the FPU flags, but on wasm they return 0 and π/2, as wasm has no floating-point exception flags.

## Memory and failure

### Out of memory

The memory limit is 16384 pages (1 GiB). Past it `malloc` returns NULL, the kit does not check, and in the module a NULL write
hits the stack's bottom with nothing to stop it. So `wasm/compat.c` wraps `malloc`, `calloc` and `realloc` with `--wrap` and
traps, after a message on stderr. The export Go calls stays unwrapped, so Go reports its own size. A huge image or string ends
the process, as a trap does anywhere else, rather than corrupting it.

### Leaks

2000 create/close cycles of a tree with a window host, a raster, input and an edit grow memory by nothing, and neither do resize
cycles up to 1200x900. Linear memory never shrinks, so a test sees only growth, and a leak under about 30 bytes per cycle hides
in a 64 KiB page.

That test missed the kit's own leak, which `-asan` found natively: `BK_context_free` does not release the context's undo history,
so closing a context after an edit loses the field's old text (4260 bytes per close with a 4096-byte string,
`TestKnownLeakOfUndoHistory`). It costs little in practice, as contexts close rarely. The sanitizer run suppresses it with
`scripts/lsan.supp`, as the BUI tree stays unmodified. It matches a function name, and `go test` strips the symbol table, so the
run passes `-ldflags='-s=false -w=false'`. The allocation tests skip under `-asan`, whose instrumentation allocates.

### Failure paths

`TestPanicAcrossCFramesExits` passes with `-tags bui_nocgo`, with panic and `Goexit`. `TestKitFatalPathsExit` covers traps, also
from inside a handler. `internal/host/x11` covers a panic in a `Do` function in the loop, and a broken connection, which closes every
window and makes `Run` return `ErrDisplayLost`. On the native engine Xlib's default handler ends the process instead.

## The X11 host

`internal/host/x11` is a Go port of `kit_window_x11.c` on `jezek/xgb`.

- **The keyboard speaks just enough XKB.** xgb has no XKB package, so `keyboard_xkb.go` writes out `UseExtension` and `GetMap`.
  Both are needed: without `UseExtension` the server reports group 2 as Mod5, the same bit as AltGr, and only `GetMap` tells which
  columns are which, as `us,de` and `de,us` put different things in the same core columns. A server without XKB uses the core
  keymap and its protocol rules, which only the unit tests exercise.
- **Keysyms match libX11.** On every keycode under 18 modifier and group states, on us, de, de nodeadkeys, fr, ru, gr, dvorak,
  us+ru, de+gr, ru+us, us+de, de+us and us+ru+de, the one difference is a deliberate one: Shift+Caps on `µ` gives the real keysym
  where Xlib's case conversion gives one that names nothing (`go test -tags bui_xlibref ./internal/host/x11/`).
- **A dead key types its spacing character,** as Xlib's text does. There is no Compose, as in C.
- **Text is always UTF-8.** The C host's `XLookupString` answers in the locale's encoding, and nothing calls `setlocale`, so typing
  `ä` natively gives a Latin-1 byte. That is a bug of the C path, not fixed here.
- **A goroutine moves every event into an unbounded list.** xgb blocks when its queue of 500 events is full, and it reads replies
  from the same socket, so a UI thread waiting for a reply with nobody reading events would hang forever. Pointer motion replaces
  its predecessor there, and a turn of the loop handles at most 256 events, so a flood leaves timers and renders their turn.
- **XKB's event number is registered before the real connection opens.** xgb keeps its decoders in a process-wide map that every
  connection's goroutine reads without a lock, and that goroutine starts inside `NewConn`. So a short-lived connection asks for the
  number and is gone before the map changes. An xgb extension connecting later in the same process still writes that map at runtime,
  which races with this host's reader. That is how xgb works, and no choice here avoids it.
- **The clipboard refuses what takes INCR, both ways.** Owners send text past about 256 KiB in parts, and this host implements no
  such transfer: a paste of one fails, and a copy that long is refused to whoever asks. xgb truncates an oversized request's length
  field rather than failing, so sending it anyway would corrupt the stream of requests. The C host has no INCR either.
- **Deliberate differences from the C host:** animation pacing is a due time rather than a sleep, so events and `Do` run meanwhile;
  a pending wake runs even when frames are due, which an animation starved in C; a layout change rereads the keymap; the pixel
  format is checked, and a depth other than 24 reports `ErrNoDisplay` where C would fail on `XPutImage`.
- **Core cursors only.** The cursor font gives the classic shapes, where Xlib would load themed ones through libXcursor.
- **The package's tests start their own Xvfb.** On a shared display, windows of a test binary running in parallel cover theirs and
  show in the pixels they read back, and clipboards collide. With a server of their own they can also change the layout and send
  real input. Without Xvfb they use `DISPLAY` and skip what would change it.

## Testing notes

- **Golden tests drive the engines directly,** as `Headless` has no public way to read pixels and the public API should not grow
  for a test. That is why the seam has `RasterPixels`.
- **Some tests are white-box.** No setter in `kit_core.c` reads its strings after emitting, and wazero reads parameters on entry and
  writes results on return, so a shared scratch buffer or stack changes no behaviour today. The tests inspect module memory and
  stack addresses instead, and guard the contract for a kit update that does.
- **Regression tests cover the stack peak and the pending image,** in `internal/engine/wasmengine`; the image test needs BUI's
  tree in `third_party/` and skips without it, as in CI. `BenchmarkStartup` there records startup and worker counts, which nothing
  asserts.
- **The golden suite compares builds, and also pins.** Comparing two builds of one kit cannot see a bug they share, so
  `TestGoldenPinned` holds the wasm engine to hashes in `internal/enginetest/testdata/pinned.sha256`. A new kit or module changes
  them on purpose: check the drawing, then run `go test ./internal/enginetest/ -run TestGoldenPinned -update-pinned`.

## What has been run

As of 2026-11-07, at `77f19ed`, CI passes these on GitHub: the native engine, the wasm engine with and without cgo, tests with cgo on
on macOS and Linux arm64 (which select the wasm engine), a cross-build without cgo for Linux, Windows and macOS on amd64 and arm64,
and the module's byte-identical rebuild.

- **Windows is built, not run.**
- **Windows has no window host.** It would be a port of `kit_window_win32.c` (987 lines).
- **macOS has only a spike of a window host,** described under "The Cocoa spike". A full port of `kit_window_cocoa.m` (1921 lines) is
  held until BUI's owner answers on [BUI#8](https://github.com/ppmpreetham/BUI/issues/8).
- **A real pointer on a real desktop has not been tried.** Drawing, the `Do` ticker, resizing and the close button were checked on a
  real X session and window manager, through `xdotool windowsize` and `wmctrl -c`. Hovering and dragging were driven on Xvfb only.

### On a MacBook

On 2026-11-07, at `a06371a`, on an Apple M5 Pro with macOS 27.2, over SSH:

- **`go test -count=1 ./...` passes** with and without cgo, on Go 1.27.1 and 1.26.8. All four runs had 329 passes, 54 skips and the
  same skipped tests. The skips need a display or Xvfb, the opt-in `xdotool` run, a subprocess, or BUI's test images.
- **Pixels match Linux amd64.** `TestGoldenPinned` runs here and passes against the hashes pinned on Linux amd64, and it fails when
  the first or last of the 29 hashes is changed.
- **A GUI program started over SSH reaches the logged-in session,** with the user logged in at the console. A small AppKit probe
  listed its own window as on screen when started directly, even though `launchctl managername` says `Background`, and also through
  `launchctl bootstrap gui/501` with a temporary plist. `launchctl asuser 501`, run as that user without `sudo`, fails with
  "Operation not permitted" when switching audit session. Neither working method took focus from the user (`NSApp.isActive` stayed 0), as the probe is an
  accessory app that orders itself front.
- **Screenshots need Screen Recording and synthetic input needs Accessibility.** Without grants, `CGPreflightScreenCaptureAccess`
  and `AXIsProcessTrusted` return 0, `screencapture -l` on the probe's own window fails with "could not create image from window",
  and a key event sent to its own pid with `CGEventPostToPid` doesn't arrive. With both granted, both return 1, the capture shows the
  window's red, and the event arrives.
- **Only `launchctl bootstrap gui/501` gets the grants.** The same binary started directly from SSH still reads 0 for both and fails
  both actions. So a test that needs a permission has to run as an agent in the GUI domain; a test that only opens a window doesn't.
- **A grant holds for one binary at one path.** On this ad-hoc signed binary it was lost with the same code hash at another path, and
  with a different hash at the granted path, and it came back when the granted binary was restored. Rebuilding the same source in
  another directory also changed the hash. `go test` builds a new binary each time, so tests that need a permission need a fixed
  binary and a new grant whenever it changes. Whether a real signing identity avoids that hasn't been tried.

### The Cocoa spike

On 2026-11-07, on the same MacBook. `internal/host/cocoa` is about 750 lines of Go through purego and the Objective-C runtime, built
with `CGO_ENABLED=0`. It opens a window and presents the raster, and it has no input, activation, menu, cursors or clipboard.

- **The window draws correctly.** A build of `examples/counter` made on Linux, captured on screen twice, shows a 320x188 window as
  640x376 pixels, with sharp text, upright, in the right colours, and the label moved from "Running for 3 s" to "Running for 9 s". That
  was checked by eye. `TestLayerHoldsTheRaster` compares exactly: all 480,000 pixels of the layer match the raster, and 244 of 600 rows
  differ when it is flipped.
- **It presents through a layer.** Each frame is copied into a `CFData` and wrapped in a `CGImage`, which is set as the contents of a
  layer-hosting view, with animations off.
- **The loop fits the main thread.** Package `bui` already locks its `init` to the main thread, and the host pulls events with
  `nextEventMatchingMask:` rather than `[NSApp run]`, as the X11 host does with its socket. `Wake` works from any goroutine:
  500 round trips against a blocked loop, with collections forced, took 51-86 µs on average, and the slowest took 199 µs to 4 ms.
- **Frame time is about 1.6x X11's,** but on different machines, so this is only a rough comparison. Memory grew by 1.7 MB between
  2000 and 6000 presents, so there is no frame-sized leak.

  | Present        | Cocoa, M5 Pro, load 11-16       | X11 on Xvfb, i7-1255U, load 3 |
  | -------------- | ------------------------------- | ----------------------------- |
  | Counter window | 419-424 µs                      | 241-310 µs                    |
  | 1920x1080      | 5.38-6.77 ms (median about 5.4) | 3.3-3.47 ms                   |

  With the 4.4 ms dense render, a 1080p frame costs about 10 ms, under the 16 ms of 60 Hz. The clock stops when Core Animation is
  handed the frame, and the window server draws later, so this isn't time to screen.

- **Every darwin build changes.** purego brings its own `fakecgo`, also for programs that only use `Headless`, and `counter` grows by
  0.77 MB, from 11.23 to 12.00 MB.
- **Tests with screenshots don't need a grant on the program under test.** The grant sits on a small fixed helper, run as an agent,
  that captures the program's window. `counter` was rebuilt just before the capture and still worked. Synthetic input hasn't been
  tried this way.
- **Other tests:** with this host, `go test -count=1 -v ./...` has 334 passes, 54 skips and no failures on Go 1.27.1 and 1.26.8, with and
  without cgo. `-race` on the package passes with cgo, and a cross-build without cgo works for Linux, Windows and macOS on amd64 and arm64.

**Not tried:** a click on the real close button (`TestCloseEndsRun` closes it from code), dragging a window's edge, whose nested
AppKit loop the host doesn't handle yet, moving between screens of different scale, and activation. The layer and the delegate are
each leaked once per window.

## Known limits

- No GPU window (`BK_window_set_gpu`), software raster only.
- No guard pages. In the module a NULL read or a bad offset returns whatever memory holds and a write overwrites it, where native
  code would crash; only an access past the end of memory traps. `live()` in package `bui` guards the binding's own references,
  but a bug inside the kit can go undetected here that would crash natively.
- Linear memory never shrinks: the largest raster or string a process had stays in its footprint, and a scratch buffer stays at its
  largest size too. Past 1 GiB the process ends (see "Out of memory").
- Core X cursors, not the desktop's cursor theme. No Compose or IME input.
- No worker threads. Video and the file browser are off, and an image source stays pending forever (see "Images without threads").
- Only the embedded Inter face works by default. Named families need a mount or `BUI_text_add_font`.
- Custom nodes with a C `draw` callback are not reachable. The Go binding does not expose them today.
- wazero's compiler covers amd64 and arm64 only. Other architectures use the interpreter, at 100-1000x native.
- `EvalNumber` differs for an inf or nan that turns finite again (see "EvalNumber").
- The module depends on an experimental wazero feature, so wazero is pinned and upgrades are deliberate changes.

## Licensing

The README states how the licences are read, and [`NOTICES.md`](../NOTICES.md) lists the parts. That reading has had no legal review.
It starts from two facts: wazero is Apache-2.0, and BUI is GPL-2.0-or-later with the GPLv3 text in its
`LICENSE`.
