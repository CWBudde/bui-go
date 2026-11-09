// SPDX-License-Identifier: GPL-2.0-or-later

package cocoa

import (
	"math"
	"testing"

	"github.com/CWBudde/bui-go/internal/engine"
	"github.com/ebitengine/purego/objc"
)

var (
	selDefaultCenter = objc.RegisterName("defaultCenter")
	selPostNamed     = objc.RegisterName("postNotificationName:object:")
	selContentsScale = objc.RegisterName("contentsScale")
)

// buttonHeight is a button's natural height at scale 1, in pixels.
const buttonHeight = 20

// openNatural opens a window with one button at its natural size, so that its size follows
// the kit's scale, and renders it.
func openNatural(t *testing.T, width, height int) (*inputFixture, engine.Node) {
	t.Helper()

	f := newFixtureOf("Press me")
	b := f.k.NodeChild(f.root, 0)
	f.k.NodeSetID(b, 1)

	id := f.h.WindowNew(f.ctx, "bui cocoa test", width, height)
	win := f.h.find(id)
	f.h.WindowShow(id)
	win.render()
	f.waitOnScreen(t, win)

	return &inputFixture{fixture: f, t: t, id: id, win: win, child: b}, b
}

// otherScale is a backing scale the window isn't at, as if it moved to another screen.
func otherScale(px float64) float64 {
	if px == 1 {
		return 2
	}

	return 1
}

// moveToScreenOf has AppKit tell the window's delegate that its backing scale changed, as
// moving to another screen does. The notification stays in this process.
func (f *inputFixture) moveToScreenOf(px float64) {
	f.h.backing = func(objc.ID) float64 { return px }

	pool := poolPush()
	defer poolPop(pool)

	objc.ID(objc.GetClass("NSNotificationCenter")).Send(selDefaultCenter).Send(selPostNamed,
		nsString("NSWindowDidChangeBackingPropertiesNotification"), f.win.nswin)
}

func (f *inputFixture) contentPoints() nsSize {
	return objc.Send[nsRect](f.win.nswin.Send(selContentView), selBounds).Size
}

func near(got, want float64) bool { return math.Abs(got-want) <= 1 }

// A window moved to a screen of another backing scale draws at that scale: the raster, the
// layer and the layout follow, and a click still finds the button.
func TestScaleChangeBetweenScreens(t *testing.T) {
	requireDisplay(t)
	t.Setenv("BUI_UI_SCALE", "")

	onMain(func() {
		bindCG(t)

		f, b := openNatural(t, 240, 120)
		defer f.close()

		from := f.win.px
		to := otherScale(from)

		_, _, _, before := f.k.NodeRect(b)
		if !near(float64(before), buttonHeight*math.Max(1, from)) {
			t.Errorf("button %d px high at backing scale %v", before, from)
		}

		f.moveToScreenOf(to)

		// Not Fatalf: this runs on the main thread's goroutine, not the test's.
		if f.win.px != to {
			t.Errorf("px %v after moving to a screen of %v", f.win.px, to)

			return
		}

		if got := objc.Send[float64](f.win.layer, selContentsScale); got != to {
			t.Errorf("layer contentsScale %v, want %v", got, to)
		}

		if !f.win.full || !f.win.renderPending {
			t.Errorf("full %v, renderPending %v: no full render due", f.win.full, f.win.renderPending)
		}

		f.win.render()

		pts := f.contentPoints()
		if w, h := int(pts.W*to), int(pts.H*to); f.win.rasterW != w || f.win.rasterH != h {
			t.Errorf("raster %dx%d, want %dx%d", f.win.rasterW, f.win.rasterH, w, h)
		}

		if _, _, _, after := f.k.NodeRect(b); !near(float64(after), buttonHeight*math.Max(1, to)) {
			t.Errorf("button %d px high after moving to %v, was %d", after, to, before)
		}

		clicks := 0

		f.k.SetEmit(func(id uint32, kind int, _ float64) {
			if id == 1 && kind == engine.EvClick {
				clicks++
			}
		})

		f.click(f.center())

		if clicks != 1 {
			t.Errorf("%d clicks at the button's new place", clicks)
		}
	})
}

// BUI_UI_SCALE fixes the kit's scale, as in the C host: a window of w x h takes
// w*scale/px points, and moving to another screen changes only the pixels per point.
func TestUIScaleFromTheEnvironment(t *testing.T) {
	requireDisplay(t)
	t.Setenv("BUI_UI_SCALE", "1.5")

	const scale, width, height = 1.5, 240, 120

	onMain(func() {
		f, b := openNatural(t, width, height)
		defer f.close()

		from := f.win.px

		pts := f.contentPoints()
		if !near(pts.W, width*scale/from) || !near(pts.H, height*scale/from) {
			t.Errorf("content %vx%v points, want %vx%v", pts.W, pts.H, width*scale/from, height*scale/from)
		}

		if _, _, _, h := f.k.NodeRect(b); !near(float64(h), buttonHeight*scale) {
			t.Errorf("button %d px high, want %v", h, buttonHeight*scale)
		}

		to := otherScale(from)
		f.moveToScreenOf(to)
		f.win.render()

		if f.win.px != to {
			t.Errorf("px %v after moving to a screen of %v", f.win.px, to)
		}

		if w := int(pts.W * to); f.win.rasterW != w {
			t.Errorf("raster %d px wide, want %d", f.win.rasterW, w)
		}

		if _, _, _, h := f.k.NodeRect(b); !near(float64(h), buttonHeight*scale) {
			t.Errorf("button %d px high after moving, want %v still", h, buttonHeight*scale)
		}
	})
}

// The kit's scale is process-wide, so a window that moves to a screen of another scale
// mustn't take it from a window that stays: the one that stays still measures and draws at
// its own.
func TestEachWindowKeepsItsScale(t *testing.T) {
	requireDisplay(t)
	t.Setenv("BUI_UI_SCALE", "")

	onMain(func() {
		f, b := openNatural(t, 240, 120)
		defer f.close()

		other := newFixtureOf("Other")
		defer other.free()

		otherID := f.h.WindowNew(other.ctx, "bui cocoa test", 240, 120)
		defer f.h.WindowFree(otherID)

		moved := f.h.find(otherID)
		f.h.WindowShow(otherID)
		moved.render()

		stays := f.win.px
		to := otherScale(stays)
		actual := f.h.backing

		f.h.backing = func(ns objc.ID) float64 {
			if ns == moved.nswin {
				return to
			}

			return actual(ns)
		}

		pool := poolPush()
		center := objc.ID(objc.GetClass("NSNotificationCenter")).Send(selDefaultCenter)
		center.Send(selPostNamed, nsString("NSWindowDidChangeBackingPropertiesNotification"), moved.nswin)
		poolPop(pool)

		moved.render()

		// A new text measures the button again, at whatever scale the kit has.
		f.k.SetStr(b, "text", "Press me again")
		f.win.render()

		if _, _, _, h := f.k.NodeRect(b); !near(float64(h), buttonHeight*math.Max(1, stays)) {
			t.Errorf("button %d px high in the window that stayed at %v, after the other moved to %v", h, stays, to)
		}
	})
}

// The size in pixels rounds, as the C host's lround does: a size in points that is a fraction
// of a pixel short of a whole one keeps that pixel, and a half rounds up.
func TestBackingPixelsRound(t *testing.T) {
	for _, c := range []struct {
		points nsSize
		px     float64
		w, h   int
	}{
		{nsSize{320, 240}, 2, 640, 480},
		{nsSize{33.33333333333333, 100.0 / 3}, 3, 100, 100}, // 99.99999999999999 and 100
		{nsSize{100.5, 40.25}, 1, 101, 40},
		{nsSize{199.99999999999997, 0}, 1, 200, 0},
	} {
		if w, h := backingPixels(c.points, c.px); w != c.w || h != c.h {
			t.Errorf("%v at %v: %dx%d, want %dx%d", c.points, c.px, w, h, c.w, c.h)
		}
	}
}
