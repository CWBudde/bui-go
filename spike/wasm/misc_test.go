// SPDX-License-Identifier: GPL-2.0-or-later

package wasmspike

import (
	"context"
	"math"
	"testing"
)

// TestEvalNumber compares BK_eval_number across the kits. Valid expressions
// must agree. Natively, division by zero and invalid operations anywhere in the
// expression fail through the FPU's exception flags. WebAssembly has none (see
// wasm/compat.h); BUI_expr_eval still rejects a non-finite result, so only an
// inf or nan that turns finite again gets through. That gap is logged, not
// failed.
func TestEvalNumber(t *testing.T) {
	ks := kits(t)

	cases := []struct {
		expr    string
		fpFlags bool // the native result depends on FE_DIVBYZERO / FE_INVALID
	}{
		{"2*pi", false},
		{"1/3+2^2", false},
		{"2*", false},
		{"sqrt(2)", false},
		{"1/0", true},
		{"sqrt(-1)", true},
		{"log(0)", true},
		{"1/(1/0)", true},   // inf in between, finite at the end
		{"atan(1/0)", true}, // the same through a function
	}

	for _, c := range cases {
		refV, refOK := ks[0].EvalNumber(c.expr)

		for _, k := range ks[1:] {
			v, ok := k.EvalNumber(c.expr)
			same := ok == refOK && (v == refV || (math.IsNaN(v) && math.IsNaN(refV)))

			switch {
			case same:
				t.Logf("%-10q %s %v,%v = %s", c.expr, ks[0].Name(), refV, refOK, k.Name())
			case c.fpFlags:
				t.Logf("%-10q %s %v,%v but %s %v,%v (no FP exception flags in wasm)",
					c.expr, ks[0].Name(), refV, refOK, k.Name(), v, ok)
			default:
				t.Errorf("%q: %s %v,%v but %s %v,%v", c.expr, ks[0].Name(), refV, refOK, k.Name(), v, ok)
			}
		}
	}
}

// TestStackPeak measures how much of the module's stack the heaviest spike
// workloads use: the 1080p dense tree, a click whose handler re-enters the
// module, and FreeType's large cell pool.
func TestStackPeak(t *testing.T) {
	bin, err := ReadModule("bui_kit.wasm")
	skipIfNotBuilt(t, err)

	k, err := NewWasmKit(context.Background(), Compiler, nil, bin, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()

	k.Call("bui_stack_paint")

	renderTree(k, 1920, 1080, func(k Kit) Node { return BuildDense(k, 8, 40) }, nil)
	c := BuildCounter(k)
	hl := k.NewHeadless(c.Root, 400, 300)
	hl.Render()

	r := k.Rect(c.Button)
	hl.Press(r[0]+r[2]/2, r[1]+r[3]/2)
	hl.Release(r[0]+r[2]/2, r[1]+r[3]/2)
	hl.Render()
	hl.Close()
	k.FreeTree(c.Root)

	peak, size := uint32(k.Call("bui_stack_peak")), uint32(k.Call("bui_stack_size"))
	t.Logf("stack: peak %d bytes of %d (%.1f%%)", peak, size, 100*float64(peak)/float64(size))

	if c.Total != 1 {
		t.Errorf("click did not reach the handler (total %d)", c.Total)
	}

	if peak >= size {
		t.Errorf("stack overflow: peak %d of %d", peak, size)
	}
}
