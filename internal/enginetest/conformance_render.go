// SPDX-License-Identifier: GPL-2.0-or-later

package enginetest

import "slices"

func testGeometry(h *harness) {
	h.sub("Pack", testGeomPack)
	h.sub("PadRounding", testGeomRounding)
	h.sub("Resize", testGeomResize)
	h.sub("Padding", testGeomPadding)
}

// testGeomPack uses a non-square raster and different padding on each edge, so that
// a swapped argument or a wrong conversion moves some edge.
func testGeomPack(h *harness) {
	const w, ht = 320, 200

	root, top, bottom, left, right, mid := h.node(typeFrame), h.node(typeButton), h.node(typeButton),
		h.node(typeButton), h.node(typeButton), h.node(typeLabel)

	h.packIn(root, top, pack{fill: fillX, padX0: 10, padX1: 20, padY0: 5, padY1: 7})
	h.packIn(root, bottom, pack{side: sideBottom, fill: fillX, padY1: 3})
	h.packIn(root, left, pack{side: sideLeft, fill: fillY, padX0: 4, padX1: 2})
	h.packIn(root, right, pack{side: sideRight, fill: fillY, padX1: 6})
	h.packIn(root, mid, pack{side: sideTop, fill: fillBoth, expand: true})

	s := h.scene(root, w, ht)
	defer s.close()

	checkEq(h.t, "rect before the first render", h.rectOf(top), rect{})
	s.render()

	checkEq(h.t, "root", h.rectOf(root), rect{0, 0, w, ht})

	t := h.rectOf(top)
	if t.h <= 0 {
		h.t.Fatalf("top has no height: %+v", t)
	}

	checkEq(h.t, "top", t, rect{10, 5, w - 30, t.h})

	b := h.rectOf(bottom)
	checkEq(h.t, "bottom", b, rect{0, ht - 3 - t.h, w, t.h})

	cavityY := t.h + 12
	cavityH := b.y - cavityY

	l := h.rectOf(left)
	checkEq(h.t, "left", l, rect{4, cavityY, l.w, cavityH})

	r := h.rectOf(right)
	checkEq(h.t, "right", r, rect{w - 6 - r.w, cavityY, r.w, cavityH})

	if l.w <= 0 || r.w <= 0 {
		h.t.Errorf("no width: left %+v, right %+v", l, r)
	}

	midX := l.x + l.w + 2
	checkEq(h.t, "middle", h.rectOf(mid), rect{midX, cavityY, r.x - midX, cavityH})
}

// testGeomRounding sees that GeomSet gets the float64: the kit rounds it to nearest.
func testGeomRounding(h *harness) {
	root, a, b := h.node(typeFrame), h.node(typeButton), h.node(typeButton)
	h.packIn(root, a, pack{fill: fillX, padX0: 10.6, padX1: 7.4, padY0: 1.5})
	h.packIn(root, b, pack{fill: fillX, padX0: 2.4, padX1: 3.6, padY0: 0.4})

	s := h.scene(root, 200, 100)
	defer s.close()

	s.render()

	ra, rb := h.rectOf(a), h.rectOf(b)
	checkEq(h.t, "a", ra, rect{11, 2, 200 - 11 - 7, ra.h})
	checkEq(h.t, "b", rb, rect{2, 2 + ra.h, 200 - 2 - 4, rb.h})
}

// testGeomResize renders one tree at two sizes, which relayouts it.
func testGeomResize(h *harness) {
	root, b := h.node(typeFrame), h.node(typeButton)
	h.packIn(root, b, pack{side: sideBottom, fill: fillX})

	s := h.scene(root, 300, 150)
	defer s.close()

	s.render()

	r := h.rectOf(b)
	checkEq(h.t, "at 300 x 150", r, rect{0, 150 - r.h, 300, r.h})

	big := h.k.RasterNew(500, 90)
	defer h.k.RasterFree(big)

	h.k.Render(s.ctx, big, 500, 90)
	checkEq(h.t, "pixels at 500 x 90", len(h.k.RasterPixels(big)), 500*90)
	checkEq(h.t, "root at 500 x 90", h.rectOf(root), rect{0, 0, 500, 90})
	checkEq(h.t, "at 500 x 90", h.rectOf(b), rect{0, 90 - r.h, 500, r.h})
}

// testGeomPadding covers an int property read through SetNum's float64.
func testGeomPadding(h *harness) {
	root, b := h.node(typeFrame), h.node(typeButton)
	h.num(root, "padding", 8)
	h.packIn(root, b, pack{fill: fillX})

	s := h.scene(root, 300, 150)
	defer s.close()

	s.render()

	r := h.rectOf(b)
	checkEq(h.t, "button in a padded frame", r, rect{8, 8, 300 - 16, r.h})
}

func testRendering(h *harness) {
	h.sub("Pixels", testRenderPixels)
	h.sub("RowOrder", testRenderRows)
	h.sub("ChangedTree", testRenderChanged)
	h.sub("SeparateRasters", testRenderSeparate)
}

func distinctColors(px []uint32) int {
	seen := map[uint32]struct{}{}
	for _, p := range px {
		seen[p&rgbMask] = struct{}{}
	}

	return len(seen)
}

func testRenderPixels(h *harness) {
	root, b := h.node(typeFrame), h.node(typeButton)
	h.str(b, keyText, "Button")
	h.packIn(root, b, pack{fill: fillX})

	s := h.scene(root, 233, 77)
	defer s.close()

	s.pixels() // the length holds before a render as well

	px := s.render()
	if n := distinctColors(px); n < 3 {
		h.t.Errorf("%d colours in a rendered button, want a visible tree", n)
	}

	// Writing to the result must not reach the next read.
	saved := slices.Clone(px)
	for i := range px {
		px[i] ^= rgbMask
	}

	checkEq(h.t, "pixels changed by writing to an earlier read", changedPixels(saved, s.pixels(), s.w, rect{}), 0)
	checkEq(h.t, "pixels changed by rendering the same tree", changedPixels(saved, s.render(), s.w, rect{}), 0)
}

// testRenderRows finds a small button in the pixels where NodeRect says it is, which
// a wrong row width, row order or axis would not.
func testRenderRows(h *harness) {
	root, b := h.node(typeFrame), h.node(typeButton)
	h.str(b, keyText, "x")
	h.packIn(root, b, pack{side: sideLeft, padX0: 40, padY0: 10})

	s := h.scene(root, 300, 100)
	defer s.close()

	px := s.render()
	r := h.rectOf(b)
	bg := px[len(px)-1] & rgbMask

	if r.w >= s.w/2 || r.h >= s.ht/2 {
		h.t.Fatalf("button too large for this test: %+v", r)
	}

	const margin = 2 // what the kit may draw around a widget

	inside, outside := 0, 0

	for i, p := range px {
		x, y := i%s.w, i/s.w

		switch {
		case r.contains(x, y):
			if p&rgbMask != bg {
				inside++
			}
		case !(rect{r.x - margin, r.y - margin, r.w + 2*margin, r.h + 2*margin}).contains(x, y):
			if p&rgbMask != bg {
				outside++
			}
		}
	}

	checkEq(h.t, "pixels other than the background outside the button", outside, 0)

	if inside < r.w*r.h/2 {
		h.t.Errorf("%d of %d pixels of the button differ from the background", inside, r.w*r.h)
	}
}

func testRenderChanged(h *harness) {
	root, l, v := h.node(typeFrame), h.node(typeLabel), h.node(typeSlider)
	h.str(l, keyText, "before")
	h.num(v, "max", 10)
	h.packIn(root, l, pack{fill: fillX})
	h.packIn(root, v, pack{fill: fillX})

	s := h.scene(root, 300, 100)
	defer s.close()

	first := s.render()

	h.str(l, keyText, "after")

	second := s.render()
	if changedPixels(first, second, s.w, rect{}) == 0 {
		h.t.Error("a new label text changed no pixels")
	}

	if changedPixels(first, second, s.w, h.rectOf(l)) != 0 {
		h.t.Error("a new label text changed pixels outside the label")
	}

	h.num(v, keyValue, 7)

	if changedPixels(second, s.render(), s.w, h.rectOf(v)) != 0 {
		h.t.Error("a new slider value changed pixels outside the slider")
	}

	h.str(l, keyText, "before")
	h.num(v, keyValue, 0)
	checkEq(h.t, "pixels different after restoring the tree", changedPixels(first, s.render(), s.w, rect{}), 0)
}

// testRenderSeparate needs each context to draw into its own raster.
func testRenderSeparate(h *harness) {
	rootA, la := h.node(typeFrame), h.node(typeLabel)
	rootB, lb := h.node(typeFrame), h.node(typeButton)
	h.str(la, keyText, "tree A")
	h.str(lb, keyText, "tree B")
	h.packIn(rootA, la, pack{fill: fillX})
	h.packIn(rootB, lb, pack{fill: fillX})

	a := h.scene(rootA, 200, 50)
	defer a.close()

	b := h.scene(rootB, 120, 90)
	defer b.close()

	pa := a.render()
	pb := b.render()

	checkEq(h.t, "pixels of A changed by rendering B", changedPixels(pa, a.pixels(), a.w, rect{}), 0)
	checkEq(h.t, "pixels of B changed by rendering A", changedPixels(pb, b.pixels(), b.w, rect{}), 0)
	checkEq(h.t, "pixels of A changed by rendering it again", changedPixels(pa, a.render(), a.w, rect{}), 0)
	checkEq(h.t, "pixels of B after rendering A", changedPixels(pb, b.pixels(), b.w, rect{}), 0)
}
