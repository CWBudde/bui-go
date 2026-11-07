// SPDX-License-Identifier: GPL-2.0-or-later

package enginetest

import (
	"testing"

	"github.com/CWBudde/bui-go/internal/engine"
	"github.com/CWBudde/bui-go/internal/engine/wasmengine"
)

// benchmarks runs the set on a kit. scripts/bench.sh records the results over time.
func benchmarks(b *testing.B, k engine.Kit) {
	b.Helper()
	k.SetEmit(ignoreEmit)

	b.Run("Render/counter", func(b *testing.B) { benchRender(b, k, goldenClick()) })
	b.Run("Render/dense", func(b *testing.B) { benchRender(b, k, goldenDense()) })
	b.Run("Hover/counter", func(b *testing.B) { benchHover(b, k, goldenClick()) })
	b.Run("Hover/dense", func(b *testing.B) { benchHover(b, k, goldenDense()) })
	b.Run("Click", func(b *testing.B) { benchClick(b, k) })
	b.Run("SetNum", func(b *testing.B) { benchSetNum(b, k) })
}

// benchScene builds a golden case's tree on a kit, drawn once. Stopping the benchmark
// never frees it, as kit state belongs to the process either way.
func benchScene(b *testing.B, k engine.Kit, c goldenCase) (*goldenRun, func()) {
	b.Helper()

	// A harness needs a testing.T for its failures, which never happen on trees the
	// golden test already runs.
	r := &goldenRun{harness: &harness{t: &testing.T{}, k: k}, nodes: map[string]engine.Node{}}
	r.nodes["root"] = r.node(typeFrame)
	c.build(r)
	r.s = r.scene(r.nodes["root"], c.w, c.h)
	r.s.render()

	return r, r.s.close
}

// benchRender is a full redraw, which a window does on an expose or a resize.
func benchRender(b *testing.B, k engine.Kit, c goldenCase) {
	b.Helper()

	r, done := benchScene(b, k, c)
	defer done()

	for b.Loop() {
		k.RenderDamage(r.s.ctx, r.s.r, c.w, c.h, true)
	}
}

// benchHover is the common frame: the pointer moves on and off a widget, and only the
// damage redraws.
func benchHover(b *testing.B, k engine.Kit, c goldenCase) {
	b.Helper()

	r, done := benchScene(b, k, c)
	defer done()

	x, y := hoverPoint(b, r, c)

	for b.Loop() {
		k.Motion(r.s.ctx, x, y, 0)
		k.RenderDamage(r.s.ctx, r.s.r, c.w, c.h, false)
		k.Motion(r.s.ctx, -100000, -100000, 0)
		k.RenderDamage(r.s.ctx, r.s.r, c.w, c.h, false)
	}
}

// hoverPoint finds a place where hovering redraws, as labels do not react, and a
// benchmark of those would time nothing.
func hoverPoint(b *testing.B, r *goldenRun, c goldenCase) (x, y int) {
	b.Helper()

	const step = 8

	k := r.k
	for y := step; y < c.h; y += step {
		k.Motion(r.s.ctx, c.w/5, y, 0)

		if k.RenderDamage(r.s.ctx, r.s.r, c.w, c.h, false) > 0 {
			return c.w / 5, y
		}
	}

	b.Fatal("hovering redraws nothing anywhere")

	return 0, 0
}

// benchClick is event latency: a press and release through to the handler, which is the
// path that crosses the seam in both directions.
func benchClick(b *testing.B, k engine.Kit) {
	b.Helper()

	clicks := 0

	k.SetEmit(func(_ uint32, kind int, _ float64) {
		if kind == engine.EvClick {
			clicks++
		}
	})
	defer k.SetEmit(ignoreEmit)

	h := &harness{t: &testing.T{}, k: k}

	s, btn := oneButton(h)
	defer s.close()

	x, y := s.center(btn)

	for b.Loop() {
		k.PressMods(s.ctx, x, y, engine.ModButton1)
		k.ReleaseMods(s.ctx, x, y, 0)
	}

	if clicks == 0 {
		b.Fatal("no clicks arrived")
	}
}

// benchSetNum is a call with a string argument, the kind a binding makes most.
func benchSetNum(b *testing.B, k engine.Kit) {
	b.Helper()

	h := &harness{t: &testing.T{}, k: k}
	n := h.bare(typeSlider)

	defer k.NodeFree(n)

	k.SetNum(n, "max", 1000)

	v := 0.0

	for b.Loop() {
		v++
		k.SetNum(n, keyValue, v)
	}
}

func BenchmarkWasm(b *testing.B) { benchmarks(b, wasmengine.Kit{}) }
