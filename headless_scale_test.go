// SPDX-License-Identifier: GPL-2.0-or-later

package bui

import (
	"bytes"
	"image"
	"math"
	"testing"
)

// buttonAtNaturalSize is a button packed at its natural size, so that its size follows the
// scale rather than the frame's. The kit's scale is process-wide, so the test puts it back.
func buttonAtNaturalSize(t *testing.T) (btn *Node, h *Headless) {
	t.Helper()

	root := NewNode("frame")
	btn = NewNode("button")

	if err := btn.SetStr("text", "Press me"); err != nil {
		t.Fatal(err)
	}

	root.Add(btn)
	btn.Pack(PackOptions{})
	t.Cleanup(root.Close)
	t.Cleanup(func() { kit.SetScale(1) })

	h = NewHeadless(root, 400, 200)
	t.Cleanup(h.Close)
	h.Render()

	return btn, h
}

func near(got, want int) bool { return got >= want-1 && got <= want+1 }

// SetScale lays the tree out again, so that a button at its natural size grows to about
// twice its size at the next render, though the raster keeps its size. The same scale again changes nothing.
func TestHeadlessSetScale(t *testing.T) {
	btn, h := buttonAtNaturalSize(t)
	_, _, w1, h1 := btn.Rect()

	h.SetScale(2)

	if !h.NeedsRender() {
		t.Error("NeedsRender after SetScale(2) = false, want true")
	}

	if damage := h.RenderDamage(false); len(damage) == 0 {
		t.Error("RenderDamage after SetScale(2) redrew nothing")
	}

	// Text does not measure exactly twice as wide at twice the scale, but without the
	// relayout the button would keep its width.
	if _, _, w2, h2 := btn.Rect(); w2 < 3*w1/2 || !near(h2, 2*h1) {
		t.Errorf("button at scale 2 is %d x %d, want over %d wide and about %d high", w2, h2, 3*w1/2, 2*h1)
	}

	h.SetScale(2)

	if h.NeedsRender() {
		t.Error("NeedsRender after SetScale with the same scale = true, want false")
	}
}

// Each Headless draws at its own scale, though the kit's is process-wide: used in turn,
// two of them draw what each draws alone.
func TestHeadlessScalesShareAProcess(t *testing.T) {
	_, a := buttonAtNaturalSize(t)
	a.SetScale(1)
	a.Render()
	wantA := a.Image()

	_, b := buttonAtNaturalSize(t)
	b.SetScale(2)
	b.Render()
	wantB := b.Image()

	if equalImages(wantA, wantB) {
		t.Fatal("scale 1 and scale 2 drew the same, so the test cannot tell them apart")
	}

	for range 2 {
		a.Render()

		if !equalImages(a.Image(), wantA) {
			t.Error("the Headless at scale 1 drew differently after one at scale 2")
		}

		b.Render()

		if !equalImages(b.Image(), wantB) {
			t.Error("the Headless at scale 2 drew differently after one at scale 1")
		}
	}
}

// SetScale refuses a scale the kit cannot draw at.
func TestHeadlessSetScaleRange(t *testing.T) {
	_, h := buttonAtNaturalSize(t)

	for _, s := range []float64{0, -1, math.NaN(), math.Inf(1), math.MaxFloat64} {
		mustPanic(t, "SetScale", func() { h.SetScale(s) })
	}
}

// bgraOf turns an Image into the bytes CopyBGRA gives for r.
func bgraOf(img *image.RGBA, r image.Rectangle) []byte {
	var out []byte

	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			c := img.RGBAAt(x, y)
			out = append(out, c.B, c.G, c.R, c.A)
		}
	}

	return out
}

// CopyBGRA gives the pixels Image gives, as B, G, R, A, for the whole raster and for part
// of it, clipped to the raster, and appends to what dst holds.
func TestHeadlessCopyBGRA(t *testing.T) {
	_, _, h := sliderInFrame(t)
	img := h.Image()
	bounds := img.Bounds()

	if got, want := h.CopyBGRA(nil, bounds), bgraOf(img, bounds); !bytes.Equal(got, want) {
		t.Errorf("CopyBGRA of the whole raster differs from Image in %d of %d bytes", diff(got, want), len(want))
	}

	part := image.Rect(10, 20, 150, 70)
	if got, want := h.CopyBGRA(nil, part), bgraOf(img, part); !bytes.Equal(got, want) {
		t.Errorf("CopyBGRA of %v differs from Image in %d of %d bytes", part, diff(got, want), len(want))
	}

	over := image.Rect(300, -5, 400, 10)
	if got, want := h.CopyBGRA(nil, over), bgraOf(img, over.Intersect(bounds)); !bytes.Equal(got, want) {
		t.Errorf("CopyBGRA of %v is %d bytes, want %d clipped to the raster", over, len(got), len(want))
	}

	if got := h.CopyBGRA([]byte{1, 2}, image.Rect(5, 5, 5, 9)); !bytes.Equal(got, []byte{1, 2}) {
		t.Errorf("CopyBGRA of an empty rectangle = %v, want dst unchanged", got)
	}

	got, want := h.CopyBGRA([]byte{9, 9, 9}, part), append([]byte{9, 9, 9}, bgraOf(img, part)...)
	if !bytes.Equal(got, want) {
		t.Error("CopyBGRA did not append to dst")
	}
}

// CopyBGRA reads the raster as the last render left it, before and after a Resize.
func TestHeadlessCopyBGRAAfterResize(t *testing.T) {
	_, _, h := buttonInFrame(t)

	h.Resize(200, 80)

	if got := len(h.CopyBGRA(nil, image.Rect(0, 0, 1000, 1000))); got != 4*320*120 {
		t.Errorf("CopyBGRA before the next render gave %d bytes, want %d", got, 4*320*120)
	}

	h.Render()

	img := h.Image()
	if got, want := h.CopyBGRA(nil, image.Rect(0, 0, 1000, 1000)), bgraOf(img, img.Bounds()); !bytes.Equal(got, want) {
		t.Errorf("CopyBGRA after Resize and Render gave %d bytes, want %d as Image", len(got), len(want))
	}
}

func diff(a, b []byte) int {
	n := max(len(a), len(b)) - min(len(a), len(b))

	for i := range min(len(a), len(b)) {
		if a[i] != b[i] {
			n++
		}
	}

	return n
}

// Presenting a 1080p frame: CopyBGRA into a reused buffer against Image.
func BenchmarkHeadlessFrame(b *testing.B) {
	root := NewNode("frame")
	defer root.Close()

	h := NewHeadless(root, 1920, 1080)
	defer h.Close()

	h.Render()

	bounds := image.Rect(0, 0, 1920, 1080)

	b.Run("Image", func(b *testing.B) {
		for b.Loop() {
			h.Image()
		}
	})

	b.Run("CopyBGRA", func(b *testing.B) {
		buf := make([]byte, 0, 4*1920*1080)

		for b.Loop() {
			buf = h.CopyBGRA(buf[:0], bounds)
		}
	})
}
