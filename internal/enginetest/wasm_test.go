// SPDX-License-Identifier: GPL-2.0-or-later

package enginetest

import (
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
