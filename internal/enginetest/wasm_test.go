// SPDX-License-Identifier: GPL-2.0-or-later

package enginetest

import (
	"flag"
	"testing"

	"github.com/CWBudde/bui-go/internal/engine/wasmengine"
)

func TestConformanceWasm(t *testing.T) {
	Conformance(t, wasmengine.Kit{})
}

func TestConformanceWasmInterpreter(t *testing.T) {
	Conformance(t, wasmengine.NewInterpreterKit())
}

// Both of wazero's engines must draw the same, and without cgo this is the only
// comparison there is.
func TestGoldenWasmAgainstInterpreter(t *testing.T) {
	Golden(t, wasmengine.Kit{}, wasmengine.NewInterpreterKit())
}

var updatePinned = flag.Bool("update-pinned", false, "rewrite testdata/pinned.sha256 from the wasm engine")

// Comparing two builds of the kit cannot see a bug they share, so this holds the module to
// what it drew when these were recorded. A new kit or module changes them on purpose: check
// the drawing, and run this with -update-pinned after the package path.
func TestGoldenPinned(t *testing.T) {
	const file = "testdata/pinned.sha256"

	if *updatePinned {
		UpdatePinnedPixels(t, wasmengine.Kit{}, file)
	}

	PinnedPixels(t, wasmengine.Kit{}, file)
}
