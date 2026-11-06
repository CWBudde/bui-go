# bui-go

Experimental Go bindings for [BUI](https://github.com/ppmpreetham/BUI), which provides Blender's UI widgets as a C
library. This is an independent project, not part of BUI.

**Status: Linux amd64 spike.** It proves linking, UI-thread ownership, C-to-Go callbacks and calls from goroutines. The
API is small and will change, and there are no tagged releases.

## Try it

You need Go 1.26 or newer, cgo with a C and C++ toolchain, and an X11 session. CMake is not needed, because the static
libraries are committed.

```sh
go run ./examples/counter
```

```go
button := bui.NewNode("button")
button.SetStr("text", "Add step")
root.Add(button)
button.Pack(bui.PackOptions{Fill: bui.FillX})
button.OnClick(func() { /* runs on the UI thread */ })

bui.Do(func() { clock.SetStr("text", "updated") }) // safe from any goroutine
```

## Threading

Call `bui.Run` from the main goroutine. Kit calls are valid only on the UI thread: the main goroutine before and after
`Run`, and handlers and `Do` functions during it. Other goroutines reach the UI thread through `bui.Do`.

## Known limits

- Linux amd64 only.
- The binding exposes generic properties (`SetNum`, `SetStr`), not typed widgets.
- `Run` on the main goroutine is documented, not enforced.
- `Do` calls made after the last `Run` stay queued, and a leftover `Do(bui.Quit)` ends the next `Run` immediately.
- Continuous rendering could delay `Do`.
- The window manager's close button has no automated test.

## Tests

```sh
xvfb-run -a go test -race ./...
CC=gcc xvfb-run -a go test -asan ./...
BUI_TEST_XDOTOOL=1 xvfb-run -a go test -race -run '^TestX' .
```

Tests that need a window are skipped when there is no display. The last command sends real clicks and drags with
`xdotool`.

## Development

```sh
just setup-deps   # treefmt, prettier, gofumpt, gci, shfmt, golangci-lint
just fmt          # format with treefmt
just lint         # golangci-lint
just check        # formatting, go.mod tidiness, lint and tests
```

CI runs the same checks (`.github/workflows/test.yaml`).

## Licence and source of the binaries

This repository is GPL-2.0-or-later, matching the SPDX headers in BUI's source. Its `LICENSE` file is copied from BUI and
contains the GPLv3 text. A program that links this package must comply with the GPL.

`lib/linux_amd64/` contains static libraries built from BUI, including the FreeType, libjpeg-turbo, libwebp and Wuffs
code vendored in BUI's tree. The corresponding source is BUI at the commit in [`BUI_COMMIT`](BUI_COMMIT). To rebuild it:

```sh
scripts/build-kit.sh
```

Build paths are stripped from the output, so rebuilds on the same toolchain from another directory are byte-identical.
