// SPDX-License-Identifier: GPL-2.0-or-later

package cocoa

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/CWBudde/bui-go/internal/engine"
	"github.com/ebitengine/purego/objc"
)

const (
	// The events AppKit's loop for dragging a window's edge takes: NSEventMaskLeftMouseUp
	// and NSEventMaskLeftMouseDragged.
	maskLeftUp   = 1 << eventLeftUp
	maskLeftDrag = 1 << eventLeftDrag
)

// renderSpy records the size of each render, and whether it came during a live resize.
type renderSpy struct {
	engine.Kit

	win   *window
	sizes [][2]int
	live  int
}

func (s *renderSpy) RenderDamage(ctx engine.Context, r engine.Raster, w, h int, full bool) int {
	s.sizes = append(s.sizes, [2]int{w, h})

	if objc.Send[bool](s.win.view, selInLiveResize) {
		s.live++
	}

	return s.Kit.RenderDamage(ctx, r, w, h, full)
}

// Dragging a window's corner runs AppKit's own loop until the button goes up, and the window
// keeps drawing at each new size meanwhile. The drag is made of events posted to the
// application's own queue, so the user's pointer stays where it is.
func TestLiveResizeDraws(t *testing.T) {
	requireDisplay(t)

	onMain(func() {
		f := openInput(t, "button", 200, 100)
		defer f.close()

		spy := &renderSpy{Kit: f.h.kit, win: f.win}
		f.h.kit = spy

		defer func() { f.h.kit = spy.Kit }()

		pool := poolPush()
		defer poolPop(pool)

		before := f.win.w
		frame := objc.Send[nsRect](f.win.nswin, selFrame)
		number := objc.Send[int64](f.win.nswin, selWindowNumber)

		// In window coordinates, which have y up: from the bottom right corner, out and down.
		post := func(kind uint64, dx float64) {
			ev := clsNSEvent.Send(selMouseEvent, kind, nsPoint{frame.Size.W - 1 + dx, 1 - dx}, uint64(0), f.now(),
				number, objc.ID(0), int64(0), int64(1), float32(1))
			f.h.app.Send(selPostEvent, ev, false)
		}

		post(eventLeftDown, 0)

		for i := 1; i <= 10; i++ {
			post(eventLeftDrag, float64(5*i))
		}

		post(eventLeftUp, 50)

		// Only the loop's wait and dispatch: a turn would render afterwards anyway.
		f.h.pump(clsNSDate.Send(selDateSinceNow, float64(1)))

		if f.win.w <= before {
			t.Errorf("width %d after the drag, from %d: AppKit didn't resize", f.win.w, before)
		}

		if spy.live == 0 {
			t.Errorf("no render during the live resize, renders %v", spy.sizes)
		}

		if len(spy.sizes) == 0 || spy.sizes[len(spy.sizes)-1] != [2]int{f.win.w, f.win.h} {
			t.Errorf("renders %v, want the last at %d x %d", spy.sizes, f.win.w, f.win.h)
		}
	})
}

// A loop AppKit runs for a drag takes only mouse events, so the wake's event stays queued
// until it ends. Do and the renders it asks for run inside it all the same.
func TestWakeRunsInsideATrackingLoop(t *testing.T) {
	requireDisplay(t)

	onMain(func() {
		f := newFixture()
		defer f.free()

		id := f.h.WindowNew(f.ctx, "bui cocoa test", 200, 100)
		defer f.h.WindowFree(id)

		win := f.h.find(id)
		f.h.WindowShow(id)
		win.render()

		var drained atomic.Bool

		f.h.WakeInit(func() {
			drained.Store(true)
			win.Request()
		})

		pool := poolPush()
		defer poolPop(pool)

		mode := nsString("NSEventTrackingRunLoopMode")

		time.AfterFunc(100*time.Millisecond, f.h.Wake)

		deadline := time.Now().Add(2 * time.Second)
		for (!drained.Load() || win.renderPending) && time.Now().Before(deadline) {
			f.h.app.Send(selNextEvent, uint64(maskLeftUp|maskLeftDrag), clsNSDate.Send(selDateSinceNow, 0.05), mode, true)
		}

		if !drained.Load() {
			t.Error("the wake didn't run inside the tracking loop")
		}

		if win.renderPending {
			t.Error("the render it asked for didn't run inside the tracking loop")
		}

		// The wake's event, which the loop left queued.
		f.h.pump(objc.ID(0))
	})
}

// A loop nested in a wake's own work, as a modal panel runs, doesn't run the next wake inside
// it, where the kit is still in the middle of a call. The turn after runs it.
func TestWakeDoesNotNestInItsOwnDrain(t *testing.T) {
	requireDisplay(t)

	var rescued atomic.Bool

	onMain(func() {
		f := newFixture()
		defer f.free()

		id := f.h.WindowNew(f.ctx, "bui cocoa test", 200, 100)
		defer f.h.WindowFree(id)

		f.h.WindowShow(id)

		depth, deepest, drains := 0, 0, 0

		f.h.WakeInit(func() {
			depth++
			deepest = max(deepest, depth)
			drains++

			if drains == 1 {
				f.h.Wake()

				// Not the wake's event: only its source could run it in here.
				f.h.app.Send(selNextEvent, uint64(maskLeftUp), clsNSDate.Send(selDateSinceNow, 0.2), nsDefaultRunLoopMode, true)
			} else {
				f.h.Quit()
			}

			depth--
		})

		timer := time.AfterFunc(5*time.Second, func() {
			rescued.Store(true)
			f.h.Quit()
		})
		defer timer.Stop()

		f.h.Wake()

		if err := f.h.Run(); err != nil {
			t.Error(err)
		}

		if deepest != 1 || drains < 2 {
			t.Errorf("wakes nested %d deep, %d drains", deepest, drains)
		}
	})

	if rescued.Load() {
		t.Error("the second wake never ran")
	}
}

// A wake that comes while a loop nested in a drain runs, and whose event that loop takes, as a
// modal loop takes every event, still ends the host's wait. The drain here runs from the wake's
// source, inside that wait.
func TestWakeTakenByALoopNestedInADrain(t *testing.T) {
	requireDisplay(t)

	var rescued atomic.Bool

	onMain(func() {
		f := newFixture()
		defer f.free()

		id := f.h.WindowNew(f.ctx, "bui cocoa test", 200, 100)
		defer f.h.WindowFree(id)

		f.h.WindowShow(id)

		drains := 0

		f.h.WakeInit(func() {
			drains++
			if drains != 1 {
				return
			}

			time.AfterFunc(50*time.Millisecond, f.h.Quit)

			pool := poolPush()
			defer poolPop(pool)

			for end := time.Now().Add(300 * time.Millisecond); time.Now().Before(end); {
				ev := f.h.app.Send(selNextEvent, eventMaskAny, clsNSDate.Send(selDateSinceNow, 0.02), nsDefaultRunLoopMode, true)
				if ev != 0 && !isWake(ev) {
					f.h.app.Send(selSendEvent, ev)
				}
			}
		})

		timer := time.AfterFunc(5*time.Second, func() {
			rescued.Store(true)
			f.h.wakePosted.Store(false)
			f.h.Quit()
		})
		defer timer.Stop()

		// Late enough that the loop waits with nothing due.
		time.AfterFunc(300*time.Millisecond, f.h.Wake)

		if err := f.h.Run(); err != nil {
			t.Error(err)
		}
	})

	if rescued.Load() {
		t.Error("the loop kept waiting after a Quit that a nested loop took")
	}
}
