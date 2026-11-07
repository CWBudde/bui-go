// SPDX-License-Identifier: GPL-2.0-or-later

package enginetest

import (
	"testing"

	"github.com/CWBudde/bui-go/internal/engine"
)

// Conformance runs the suite on k, as subtests. Every assertion must hold on any
// engine, and each subtest builds and frees its own tree.
func Conformance(t *testing.T, k engine.Kit) {
	t.Helper()
	k.SetEmit(ignoreEmit)

	for _, c := range []struct {
		name string
		f    func(*harness)
	}{
		{"Tree", testTree},
		{"Ids", testIDs},
		{"Properties", testProperties},
		{"SetStrs", testSetStrs},
		{"Geometry", testGeometry},
		{"Rendering", testRendering},
		{"Events", testEvents},
		{"Input", testInput},
		{"WindowHost", testWindowHost},
		{"ReentryDuringEmit", testReentry},
		{"NegativeCoordinates", testNegativeCoordinates},
		{"EvalNumber", testEvalNumber},
		{"Lifecycle", testLifecycle},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Helper()
			c.f(&harness{t: t, k: k})
		})
	}
}
