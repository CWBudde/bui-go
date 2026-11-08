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
