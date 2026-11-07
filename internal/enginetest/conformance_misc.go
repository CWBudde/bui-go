// SPDX-License-Identifier: GPL-2.0-or-later

package enginetest

import (
	"math"

	"github.com/CWBudde/bui-go/internal/engine"
)

// testNegativeCoordinates has a button at the origin filling the raster. A wrong
// conversion that clamps to 0, drops the sign or wraps to a large value would hit it
// (clamp, sign), or at least show in the release that follows.
func testNegativeCoordinates(h *harness) {
	rec := h.record()

	s, b := oneButton(h)
	defer s.close()

	checkEq(h.t, "button rect", h.rectOf(b), rect{0, 0, 200, 100})

	id := h.k.NodeID(b)

	for _, p := range [][2]int{{-1, -1}, {-5, 5}, {5, -5}, {-50, -50}, {-100000, 50}, {50, -100000}} {
		h.k.Press(s.ctx, p[0], p[1])
		h.k.Release(s.ctx, p[0], p[1])
		checkEq(h.t, "clicks at "+itoa(p[0])+","+itoa(p[1]), len(rec.of(id, engine.EvClick)), 0)
	}

	x, y := s.center(b)

	// Half inside, half out: the grab decides.
	h.k.Press(s.ctx, x, y)
	h.k.Release(s.ctx, -3, -3)
	checkEq(h.t, "clicks after a release at a negative position", len(rec.of(id, engine.EvClick)), 0)

	h.k.Press(s.ctx, -3, -3)
	h.k.Release(s.ctx, x, y)
	checkEq(h.t, "clicks after a press at a negative position", len(rec.of(id, engine.EvClick)), 0)

	h.k.Release(s.ctx, -7, -7) // with no press at all

	s.render()
	s.click(b)
	checkEq(h.t, "clicks after a later valid click", len(rec.of(id, engine.EvClick)), 1)
}

func testEvalNumber(h *harness) {
	const precision = 1e-12

	for _, c := range []struct {
		expr string
		want float64
	}{
		{"2*pi", 2 * math.Pi},
		{"1+2*3", 7},
		{"-1.5", -1.5},
		{"0.1+0.2", 0.1 + 0.2},
		{"sqrt(2)", math.Sqrt2},
		{"1e300*10", 1e301},
		{"1+1\x00garbage", 2},
	} {
		v, ok := h.k.EvalNumber(c.expr)
		if !ok {
			h.t.Errorf("EvalNumber(%q) failed", c.expr)

			continue
		}

		if math.Abs(v-c.want) > precision*math.Max(1, math.Abs(c.want)) {
			h.t.Errorf("EvalNumber(%q) = %v, want %v", c.expr, v, c.want)
		}
	}

	// PLAN.md documents that 1/(1/0) differs between engines, so it is not here.
	for _, expr := range []string{"", " ", "1+", "(", "1 2", "no_such_name", "sqrt(", "1/0", "sqrt(-1)", "log(0)"} {
		if v, ok := h.k.EvalNumber(expr); ok {
			h.t.Errorf("EvalNumber(%q) = %v, want a failure", expr, v)
		}
	}

	// A failure leaves nothing behind for the next call.
	if v, ok := h.k.EvalNumber("2+2"); !ok || v != 4 {
		h.t.Errorf("EvalNumber after failures = %v, %v", v, ok)
	}
}

// lifecycleLoops is a coarse smoke test: no leak shows through the seam itself, but a
// leak this size in a fixed heap, or reuse of a stale object, tends to.
const lifecycleLoops = 300

func testLifecycle(h *harness) {
	h.sub("Trees", testLifecycleTrees)
	h.sub("ContextsOnOneRoot", testLifecycleContexts)
	h.sub("Rasters", testLifecycleRasters)
}

// testLifecycleTrees rebuilds one tree many times, which must give the same pixels.
func testLifecycleTrees(h *harness) {
	rec := h.record()

	var first []uint32

	for i := range lifecycleLoops {
		root, b, l := h.node(typeFrame), h.node(typeButton), h.node(typeLabel)
		h.str(b, keyText, "Button")
		h.str(l, keyLong, longText(2000))
		h.packIn(root, b, pack{fill: fillX})
		h.packIn(root, l, pack{fill: fillX})

		s := h.scene(root, 200, 100)
		px := s.render()

		if first == nil {
			first = px
		} else if d := changedPixels(first, px, s.w, rect{}); d != 0 {
			h.t.Errorf("loop %d: %d pixels differ from the first", i, d)
		}

		rec.events = rec.events[:0]

		s.click(b)
		checkEq(h.t, "clicks in a loop", len(rec.of(h.k.NodeID(b), engine.EvClick)), 1)
		s.close()
	}
}

func testLifecycleContexts(h *harness) {
	root, b := h.node(typeFrame), h.node(typeButton)
	defer freeTree(h.k, root)

	h.packIn(root, b, pack{fill: fillX})

	r := h.k.RasterNew(100, 50)
	defer h.k.RasterFree(r)

	var first []uint32

	for i := range lifecycleLoops {
		ctx := h.k.ContextNew(root)
		if ctx == 0 {
			h.t.Fatalf("loop %d: ContextNew = 0", i)
		}

		h.k.ContextSetEmitHost(ctx)
		h.k.Render(ctx, r, 100, 50)

		px := h.k.RasterPixels(r)
		if first == nil {
			first = px
		} else if d := changedPixels(first, px, 100, rect{}); d != 0 {
			h.t.Errorf("loop %d: %d pixels differ from the first", i, d)
		}

		h.k.ContextFree(ctx)
	}
}

// testLifecycleRasters goes through sizes, so that memory gets reused at another size.
func testLifecycleRasters(h *harness) {
	root, b := h.node(typeFrame), h.node(typeButton)
	defer freeTree(h.k, root)

	h.packIn(root, b, pack{fill: fillX})

	ctx := h.k.ContextNew(root)
	defer h.k.ContextFree(ctx)

	for i := range lifecycleLoops {
		w, ht := 50+i%7*40, 20+i%5*30

		r := h.k.RasterNew(w, ht)
		if r == 0 {
			h.t.Fatalf("loop %d: RasterNew(%d, %d) = 0", i, w, ht)
		}

		if i%3 == 0 {
			h.k.Render(ctx, r, w, ht)
			checkEq(h.t, "button width in a raster", h.rectOf(b).w, w)
		}

		checkEq(h.t, "pixels in a raster", len(h.k.RasterPixels(r)), w*ht)
		h.k.RasterFree(r)
	}
}
