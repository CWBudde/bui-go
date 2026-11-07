// SPDX-License-Identifier: GPL-2.0-or-later

//go:build cgo && linux && amd64

package enginetest

import (
	"testing"

	"github.com/CWBudde/bui-go/internal/engine/cgoengine"
	"github.com/CWBudde/bui-go/internal/engine/wasmengine"
)

func TestConformanceCgo(t *testing.T) {
	Conformance(t, cgoengine.Kit{})
}

// The native libraries are the reference for the module's pixels.
func TestGoldenCgoAgainstWasm(t *testing.T) {
	Golden(t, cgoengine.Kit{}, wasmengine.Kit{})
}

func BenchmarkCgo(b *testing.B) { benchmarks(b, cgoengine.Kit{}) }
