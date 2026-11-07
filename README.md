# bui-go

Experimental Go bindings for [BUI](https://github.com/ppmpreetham/BUI), which provides Blender's UI widgets as a C
library. This is an independent project, not part of BUI.

**Status: experimental.** The API is small and will change, and there are no tagged releases.

## Try it

You need Go 1.26 or newer, and no C toolchain. On Linux, with an X server or XWayland, this opens a real window from a static
binary:

```sh
CGO_ENABLED=0 go run ./examples/counter
```

On Windows and macOS that example exits with `ErrNoDisplay`, as they have no window host yet. Drive the tree with `Headless`
instead, which renders into memory and takes synthetic input. It works on Linux too, also with no display. In a module that
requires `github.com/CWBudde/bui-go`, this prints `clicked`:

```go
package main

import (
	"fmt"

	bui "github.com/CWBudde/bui-go"
)

func main() {
	root := bui.NewNode("frame")
	defer root.Close()

	button := bui.NewNode("button")
	button.SetStr("text", "Add step")
	root.Add(button)
	button.Pack(bui.PackOptions{Fill: bui.FillX})
	button.OnClick(func() { fmt.Println("clicked") })

	view := bui.NewHeadless(root, 320, 120)
	defer view.Close()

	view.Render() // lays out the tree, so Rect is valid from here on
	x, y, w, h := button.Rect()
	view.Press(x+w/2, y+h/2)
	view.Release(x+w/2, y+h/2)
}
```

CI runs the tests on macOS, but only cross-builds for Windows, so that platform is untested.

In a window program, the same calls look like this:

```go
button := bui.NewNode("button")
button.SetStr("text", "Add step")
root.Add(button)
button.Pack(bui.PackOptions{Fill: bui.FillX})
button.OnClick(func() { /* runs on the UI thread */ })

bui.Do(func() { clock.SetStr("text", "updated") }) // safe from any goroutine
```

## Engines

The same API runs on two engines, chosen when you build:

| Build                                 | Engine                                                                              | Windows                    | Needs               |
| ------------------------------------- | ----------------------------------------------------------------------------------- | -------------------------- | ------------------- |
| Linux amd64 with cgo                  | Native: static libraries in `lib/`                                                  | X11                        | C and C++ toolchain |
| Anything else, or the tag `bui_nocgo` | WebAssembly: an embedded module, in [wazero](https://github.com/tetratelabs/wazero) | X11 in pure Go, Linux only | Nothing but Go      |

`go build -tags bui_nocgo` selects the wasm engine on a machine that would otherwise use cgo. Both engines draw the same
pixels: the tests compare them exactly. On Windows and macOS the wasm engine runs headless, and `NewWindow` and `Run` report
`ErrNoDisplay`.

**Speed.** On amd64 and arm64 wazero compiles the module, which renders at about 2-4x native time and handles a hover at
about 1.2-3x. A full 1920x1080 frame takes about 13 ms when the machine is not loaded. Other architectures use wazero's
interpreter, at 100-1000x native: a small tree renders in about 36 ms and a dense full-HD frame in over a second, so they
are for correctness rather than interaction. [`docs/design.md`](docs/design.md) has the measurements, and `scripts/bench.sh`
records new ones.

**Compilation cache.** The first start compiles the module, which takes about 0.2-0.3 s. The result is cached in
`os.UserCacheDir()/bui-go/wazero`, which brings later starts to about 25 ms. A cache that cannot be read or written costs only
that time. The module loads at the first kit call, so importing the package costs nothing.

### Limits of the wasm engine

- No GPU window, video or file browser, and no worker threads. Image sources would stay pending forever, though the API sets
  none today.
- Only the embedded Inter font works by default.
- Core X cursors, not the desktop's theme, and no Compose or IME input. A dead key types its own character.
- Clipboard text that takes X's INCR transfer, past about 256 KiB, is refused in both directions.
- `EvalNumber` accepts an expression whose intermediate inf or nan turns finite again, such as `1/(1/0)`, as wasm has no
  floating-point exception flags.
- No guard pages: inside the module a NULL read or a bad offset returns what memory holds, where native code would crash.
- Closing a context after an edit loses the edited field's old text, in the kit's own code, on either engine.
- Linear memory never shrinks, so the largest raster a process had stays in its footprint. Past 1 GiB the process ends with
  a message, as it does on any fault in the module: its state is unknown after one.
- It depends on an experimental wazero feature, so wazero is pinned, and upgrades are deliberate changes.

## Threading

Call `bui.Run` from the main goroutine. Kit calls are valid only on the UI thread: the main goroutine before and after
`Run`, and handlers and `Do` functions during it. Other goroutines reach the UI thread through `bui.Do`.

## Known limits

- The binding exposes generic properties (`SetNum`, `SetStr`), not typed widgets.
- `Run` on the main goroutine is documented, not enforced.
- `Do` calls made after the last `Run` stay queued, and a leftover `Do(bui.Quit)` ends the next `Run` immediately.
- Continuous rendering could delay `Do` on the native engine.
- The window manager's close button is tested through the message it sends, not through a real window manager.

## Tests

```sh
xvfb-run -a go test -race ./...                       # native engine, compared with the wasm one
xvfb-run -a go test -race -tags bui_nocgo ./...       # package bui on the wasm engine
CGO_ENABLED=0 xvfb-run -a go test ./...               # with no cgo at all
CC=gcc LSAN_OPTIONS=suppressions=$PWD/scripts/lsan.supp xvfb-run -a go test -asan -ldflags='-s=false -w=false' ./...
BUI_TEST_XDOTOOL=1 xvfb-run -a go test -race -run '^TestX' .
go test -tags bui_xlibref ./internal/host/x11/        # key translation against libX11, needs its headers
```

Tests that need a window are skipped when there is no display. `BUI_TEST_XDOTOOL=1` sends real clicks and keys and changes
the keyboard layout, so use a display of its own. `internal/host/x11` starts its own Xvfb when it can.

## Development

```sh
just setup-deps   # treefmt, prettier, gofumpt, gci, shfmt
just fmt          # format with treefmt
just lint         # golangci-lint, at the version pinned in the justfile
just check        # formatting, go.mod tidiness, lint and tests
```

CI runs the same checks and more (`.github/workflows/test.yaml`).

## Licence and source of the binaries

The licence statements in this section are the project's own reading of the licences involved. They have had no legal review
and are not legal advice, so check them for your own case before you distribute a program built on bui-go.

This repository is GPL-2.0-or-later, matching the SPDX headers in BUI's source. Its `LICENSE` file is copied from BUI and
contains the GPLv3 text. We read the GPL as applying to a program that links this package.

**We read a binary with the wasm engine as distributable under GPLv3,** not GPLv2: it includes wazero, which is Apache-2.0, and
as we understand it that licence is compatible with GPLv3 but not with GPLv2. BUI's "or later" is what leaves GPLv3 open.
[`NOTICES.md`](NOTICES.md) lists the third-party parts and their licences, and [`licenses/`](licenses) holds the texts.

We offer the following as the corresponding source of the committed binaries:

- `lib/linux_amd64/`, static libraries including the FreeType, stb_truetype, NanoSVG, libjpeg-turbo, libwebp and Wuffs code
  vendored in BUI's tree: BUI at the commit in [`BUI_COMMIT`](BUI_COMMIT), built by `scripts/build-kit.sh`.
- `internal/engine/wasmengine/bui_kit.wasm`, and the test's `sjlj_probe.wasm`: the same BUI commit, `wasm/` in this
  repository, and wasi-sdk 34.0, whose version and checksum are in `scripts/build-wasm.sh`, which builds them. `NOTICES.md`
  names the wasi-libc and LLVM commits that wasi-sdk release pins.

```sh
scripts/build-kit.sh
scripts/build-wasm.sh
```

Build paths are stripped from the output. The wasm modules rebuild byte-identically from another directory, and CI rebuilds the
main module and requires identical bytes. No such check covers `lib/`, which is built with the local C toolchain.
