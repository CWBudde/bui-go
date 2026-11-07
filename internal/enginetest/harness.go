// SPDX-License-Identifier: GPL-2.0-or-later

// Package enginetest holds the tests every engine must pass: a conformance suite
// and a golden test that compares the live pixels of two engines. It reaches the
// kit only through engine.Kit, so nothing here depends on one engine, and it
// imports testing from non-test code on purpose.
//
// A handler running inside an emit must not call t.Fatal or t.FailNow: Goexit
// across the kit's frames ends the process (engine.ExitIfUnwinding). Handlers use
// t.Errorf only, so the harness's fatal helpers are for use outside them.
package enginetest

import (
	"strings"
	"testing"

	"github.com/CWBudde/bui-go/internal/engine"
)

// Pack values, numbered as package bui's Side and Fill.
const (
	sideTop = iota
	sideBottom
	sideLeft
	sideRight
)

const (
	fillNone = iota
	fillX
	fillY
	fillBoth
)

// The property keys and node types used more than once.
const (
	keyText     = "text"
	keyValue    = "value"
	keySetValue = "set_value"
	keyLabel    = "label"
	keyLong     = "description" // not drawn: laying out hundreds of KB of text takes minutes
	keyItems    = "items"
	keyScroll   = "scroll"
	typeFrame   = "frame"
	typeButton  = "button"
	typeLabel   = "label"
	typeSlider  = "slider"
	typeColumn  = "column"
	typeScroll  = "scroll"
	typeNumber  = "number"
	typeDrop    = "dropdown"
	typeCheck   = "checkbox"
	typeText    = "text"
	typeArea    = "textarea"
)

// Text in Han script. The kit's font has no such glyphs and draws its missing-glyph
// boxes, a path worth comparing too. The strings are about the kit's input, not about
// who reads this.
const (
	textCJK      = "日本語のテキスト"       //nolint:gosmopolitan // see above
	textCJKMixed = "mixed: é Ж 語 🎉" //nolint:gosmopolitan // see above
	textCJKShort = "Ж語🎉"            //nolint:gosmopolitan // see above
	textCJKLabel = "Geändert: 変更"   //nolint:gosmopolitan // see above
)

// pack mirrors bui.PackOptions, in the float64 the seam takes.
type pack struct {
	side, fill                 int
	expand                     bool
	padX0, padX1, padY0, padY1 float64
}

// rect is a node's rectangle in raster pixels.
type rect struct{ x, y, w, h int }

func (r rect) contains(x, y int) bool {
	return x >= r.x && x < r.x+r.w && y >= r.y && y < r.y+r.h
}

// harness is one test's view of a kit.
type harness struct {
	t      *testing.T
	k      engine.Kit
	nextID uint32
}

// sub runs f as a subtest with its own harness.
func (h *harness) sub(name string, f func(*harness)) {
	h.t.Helper()
	h.t.Run(name, func(t *testing.T) {
		t.Helper()
		f(&harness{t: t, k: h.k})
	})
}

// bare creates a node without an id, as kit-internal nodes are.
func (h *harness) bare(typ string) engine.Node {
	h.t.Helper()

	n := h.k.NodeNew(typ)
	if n == 0 {
		h.t.Fatalf("NodeNew(%q) = 0", typ)
	}

	return n
}

// node creates a node with a fresh id, as package bui does for every node.
func (h *harness) node(typ string) engine.Node {
	h.t.Helper()

	n := h.bare(typ)
	h.nextID++
	h.k.NodeSetID(n, h.nextID)

	return n
}

// num sets a number property that must exist.
func (h *harness) num(n engine.Node, key string, v float64) {
	h.t.Helper()

	if !h.k.SetNum(n, key, v) {
		h.t.Fatalf("SetNum(%q, %v) = false", key, v)
	}
}

// str sets a string property that must exist.
func (h *harness) str(n engine.Node, key, v string) {
	h.t.Helper()

	if !h.k.SetStr(n, key, v) {
		h.t.Fatalf("SetStr(%q, %s) = false", key, abbrev(v))
	}
}

// add appends child to parent, which must work.
func (h *harness) add(parent, child engine.Node) {
	h.t.Helper()

	if !h.k.NodeAdd(parent, child) {
		h.t.Fatal("NodeAdd = false")
	}
}

// packIn appends child to parent and packs it, setting every key Node.Pack sets.
func (h *harness) packIn(parent, child engine.Node, o pack) {
	h.t.Helper()
	h.add(parent, child)
	h.k.GeomSetPack(child)

	expand := 0.0
	if o.expand {
		expand = 1
	}

	for _, kv := range []struct {
		key string
		v   float64
	}{
		{"side", float64(o.side)},
		{"fill", float64(o.fill)},
		{"expand", expand},
		{"padx0", o.padX0},
		{"padx1", o.padX1},
		{"pady0", o.padY0},
		{"pady1", o.padY1},
	} {
		h.k.GeomSet(child, kv.key, kv.v)
	}
}

func (h *harness) rectOf(n engine.Node) rect {
	x, y, w, ht := h.k.NodeRect(n)

	return rect{x, y, w, ht}
}

// freeTree destroys a subtree as Node.free does. NodeFree only detaches a child
// with an id, so the children with ids go first, back to front, as each free
// detaches from the parent. Children without one stay for the kit to free with
// their parent, but nodes with ids below them still need freeing.
func freeTree(k engine.Kit, n engine.Node) {
	freeChildren(k, n)
	k.NodeSetID(n, 0)
	k.NodeFree(n)
}

func freeChildren(k engine.Kit, n engine.Node) {
	for i := k.NodeChildCount(n) - 1; i >= 0; i-- {
		child := k.NodeChild(n, i)
		if k.NodeID(child) != 0 {
			freeTree(k, child)
		} else {
			freeChildren(k, child)
		}
	}
}

// scene is a tree with a context and a raster, as bui.Headless holds.
type scene struct {
	h    *harness
	root engine.Node
	ctx  engine.Context
	r    engine.Raster
	w    int
	ht   int
}

// scene attaches a context with the emit host and a w x ht raster to root.
func (h *harness) scene(root engine.Node, w, ht int) *scene {
	h.t.Helper()

	ctx := h.k.ContextNew(root)
	if ctx == 0 {
		h.t.Fatal("ContextNew = 0")
	}

	h.k.ContextSetEmitHost(ctx)

	r := h.k.RasterNew(w, ht)
	if r == 0 {
		h.t.Fatalf("RasterNew(%d, %d) = 0", w, ht)
	}

	return &scene{h: h, root: root, ctx: ctx, r: r, w: w, ht: ht}
}

// close frees in the order package bui enforces: context, raster, then the tree.
func (s *scene) close() {
	s.h.k.ContextFree(s.ctx)
	s.h.k.RasterFree(s.r)
	freeTree(s.h.k, s.root)
}

// pixels reads the raster, which must have one pixel per position.
func (s *scene) pixels() []uint32 {
	s.h.t.Helper()

	px := s.h.k.RasterPixels(s.r)
	if len(px) != s.w*s.ht {
		s.h.t.Fatalf("RasterPixels: %d pixels, want %d x %d = %d", len(px), s.w, s.ht, s.w*s.ht)
	}

	return px
}

// render draws the whole tree and returns the pixels.
func (s *scene) render() []uint32 {
	s.h.t.Helper()
	s.h.k.Render(s.ctx, s.r, s.w, s.ht)

	return s.pixels()
}

// center returns the middle of a rendered node, which must have a size.
func (s *scene) center(n engine.Node) (x, y int) {
	s.h.t.Helper()

	r := s.h.rectOf(n)
	if r.w <= 0 || r.h <= 0 {
		s.h.t.Fatalf("node has no size: %+v", r)
	}

	return r.x + r.w/2, r.y + r.h/2
}

// click presses and releases the left button in the middle of a rendered node.
func (s *scene) click(n engine.Node) {
	s.h.t.Helper()

	x, y := s.center(n)
	s.h.k.Press(s.ctx, x, y)
	s.h.k.Release(s.ctx, x, y)
}

// ignoreEmit stands for "no receiver": the seam does not promise that a nil
// receiver is safe when an event arrives.
func ignoreEmit(uint32, int, float64) {}

// emitted is one event as a receiver saw it.
type emitted struct {
	id   uint32
	kind int
	a    float64
}

// recorder is a receiver that keeps what it sees.
type recorder struct{ events []emitted }

func (r *recorder) receive(id uint32, kind int, a float64) {
	r.events = append(r.events, emitted{id, kind, a})
}

// of returns the events of one kind for one node, ignoring all others, as a
// receiver is told to.
func (r *recorder) of(id uint32, kind int) []emitted {
	var out []emitted

	for _, e := range r.events {
		if e.id == id && e.kind == kind {
			out = append(out, e)
		}
	}

	return out
}

// listen makes f the receiver until the test ends. SetEmit state is process-wide
// on an engine, so no receiver may outlive its test.
func (h *harness) listen(f engine.EmitFunc) {
	h.k.SetEmit(f)
	h.t.Cleanup(func() { h.k.SetEmit(ignoreEmit) })
}

// record makes a new recorder the receiver.
func (h *harness) record() *recorder {
	r := &recorder{}
	h.listen(r.receive)

	return r
}

// checkEq reports a difference without stopping, so it is safe in a handler.
func checkEq[T comparable](t *testing.T, what string, got, want T) {
	t.Helper()

	if got != want {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

// checkStr is checkEq for strings that can be hundreds of KB long.
func checkStr(t *testing.T, what, got, want string) {
	t.Helper()

	if got != want {
		t.Errorf("%s = %s, want %s", what, abbrev(got), abbrev(want))
	}
}

// abbrev quotes a string for a message, keeping its length when it cuts it.
func abbrev(s string) string {
	const limit = 60

	if len(s) <= limit {
		return quote(s)
	}

	return quote(s[:limit]) + "... (" + itoa(len(s)) + " bytes)"
}

// longText returns exactly size bytes of text that differs along its length, so a
// shifted, truncated or stale copy cannot compare equal.
func longText(size int) string {
	var b strings.Builder

	b.Grow(size + 16)

	for i := 0; b.Len() < size; i++ {
		b.WriteString(itoa(i))
		b.WriteByte(' ')
	}

	return b.String()[:size]
}
