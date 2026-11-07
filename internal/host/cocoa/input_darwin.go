// SPDX-License-Identifier: GPL-2.0-or-later

package cocoa

import (
	"math"

	"github.com/CWBudde/bui-go/internal/engine"
	"github.com/ebitengine/purego/objc"
)

const (
	// NSEventType values the loop tells apart.
	eventKeyDown = 10
	eventKeyUp   = 11

	// buttonMiddle is the middle button's buttonNumber and the kit's number for it.
	buttonMiddle = 2
	buttonRight  = 3

	// scrollStep is how many points of precise scroll make a wheel notch.
	scrollStep = 12.0
	// maxNotches bounds what one scroll event sends per axis.
	maxNotches = 8
	// warpSettle is how long after a warp a move can still carry the old position.
	warpSettle = 0.003
	// offWindow is where the pointer goes for the kit once it has left.
	offWindow = -100000

	trackingOptions = 0x01 | 0x04 | 0x80 | 0x200 // entered and exited, cursor update, always, visible rect
)

var (
	selButtonNumber     = objc.RegisterName("buttonNumber")
	selConvertFromView  = objc.RegisterName("convertPoint:fromView:")
	selConvertToView    = objc.RegisterName("convertPoint:toView:")
	selLocationInWindow = objc.RegisterName("locationInWindow")
	selMakeFirstResp    = objc.RegisterName("makeFirstResponder:")
	selTimestamp        = objc.RegisterName("timestamp")
	selPhase            = objc.RegisterName("phase")
	selMomentumPhase    = objc.RegisterName("momentumPhase")
	selDeltaX           = objc.RegisterName("deltaX")
	selDeltaY           = objc.RegisterName("deltaY")
	selScrollingDeltaX  = objc.RegisterName("scrollingDeltaX")
	selScrollingDeltaY  = objc.RegisterName("scrollingDeltaY")
	selHasPreciseDeltas = objc.RegisterName("hasPreciseScrollingDeltas")
	selInitTracking     = objc.RegisterName("initWithRect:options:owner:userInfo:")
	selAddTracking      = objc.RegisterName("addTrackingArea:")
	selEventWindow      = objc.RegisterName("window")
)

// viewClass is registered by load, next to the delegate's.
var viewClass objc.Class

// guarded runs f as a callback from AppKit. A panic must not cross AppKit's frames.
func guarded(f func()) {
	returned := false

	defer engine.ExitIfUnwinding(&returned)

	f()

	returned = true
}

// viewMethod is a view's event method, for a window that is still open.
func viewMethod(sel string, f func(win *window, ev objc.ID)) objc.MethodDef {
	return objc.MethodDef{
		Cmd: objc.RegisterName(sel),
		Fn: func(self objc.ID, _ objc.SEL, ev objc.ID) {
			guarded(func() {
				if win := current.findView(self); win != nil {
					f(win, ev)
				}
			})
		},
	}
}

func yes(objc.ID, objc.SEL) bool                  { return true }
func yesForEvent(objc.ID, objc.SEL, objc.ID) bool { return true }

func registerView() bool {
	motion := func(win *window, ev objc.ID) { win.motion(ev) }

	cls, err := objc.RegisterClass("BUIGoView", objc.GetClass("NSView"), nil, nil, []objc.MethodDef{
		{Cmd: objc.RegisterName("acceptsFirstResponder"), Fn: yes},
		{Cmd: objc.RegisterName("acceptsFirstMouse:"), Fn: yesForEvent},
		viewMethod("mouseDown:", (*window).mouseDown),
		viewMethod("mouseUp:", (*window).mouseUp),
		viewMethod("rightMouseDown:", (*window).rightDown),
		viewMethod("rightMouseUp:", (*window).rightUp),
		viewMethod("otherMouseDown:", (*window).otherDown),
		viewMethod("otherMouseUp:", (*window).otherUp),
		viewMethod("mouseMoved:", motion),
		viewMethod("mouseDragged:", motion),
		viewMethod("rightMouseDragged:", motion),
		viewMethod("otherMouseDragged:", motion),
		viewMethod("mouseExited:", (*window).mouseExited),
		viewMethod("scrollWheel:", (*window).scrollWheel),
		viewMethod("keyDown:", func(win *window, ev objc.ID) { win.handleKey(ev, true) }),
		viewMethod("keyUp:", func(win *window, ev objc.ID) {
			if ev != win.host.keyUpDone {
				win.handleKey(ev, false)
			}
		}),
		viewMethod("flagsChanged:", func(win *window, ev objc.ID) {
			win.handleFlags(objc.Send[uint64](ev, selModifierFlags))
		}),
		viewMethod("cursorUpdate:", func(win *window, _ objc.ID) { win.applyCursor() }),
	})
	if err != nil {
		return false
	}

	viewClass = cls

	return true
}

// findView finds the open window showing a view.
func (h *Host) findView(view objc.ID) *window {
	for _, win := range h.windows {
		if win.view == view && win.nswin != 0 {
			return win
		}
	}

	return nil
}

// eventPixels is an event's position in raster pixels, with y down. The view isn't
// flipped, as the layer it hosts draws the raster upright as it is.
func (win *window) eventPixels(ev objc.ID) (x, y int) {
	loc := objc.Send[nsPoint](ev, selLocationInWindow)
	p := objc.Send[nsPoint](win.view, selConvertFromView, loc, objc.ID(0))
	b := objc.Send[nsRect](win.view, selBounds)

	return int(math.Floor(p.X * win.px)), int(math.Floor((b.Size.H - p.Y) * win.px))
}

func (win *window) mouseDown(ev objc.ID) {
	x, y := win.eventPixels(ev)
	win.nswin.Send(selMakeFirstResp, win.view)
	win.host.kit.PressMods(win.ctx, x, y, win.host.modsOf(objc.Send[uint64](ev, selModifierFlags))|engine.ModButton1)
}

// releaseMods are the modifiers of a release: the kit documents the button as gone.
func (win *window) releaseMods(ev objc.ID) int {
	return win.host.modsOf(objc.Send[uint64](ev, selModifierFlags)) &^ engine.ModButton1
}

func (win *window) mouseUp(ev objc.ID) {
	x, y := win.eventPixels(ev)
	win.host.kit.ReleaseMods(win.ctx, x, y, win.releaseMods(ev))
}

func (win *window) rightDown(ev objc.ID) {
	x, y := win.eventPixels(ev)
	win.host.kit.ContextMenu(win.ctx, x, y, win.host.modsOf(objc.Send[uint64](ev, selModifierFlags)))
}

func (win *window) rightUp(ev objc.ID) {
	x, y := win.eventPixels(ev)
	win.host.kit.Button(win.ctx, x, y, buttonRight, true, win.releaseMods(ev))
}

func (win *window) otherDown(ev objc.ID) {
	if objc.Send[int64](ev, selButtonNumber) != buttonMiddle {
		return
	}

	x, y := win.eventPixels(ev)
	win.host.kit.Button(win.ctx, x, y, buttonMiddle, false, win.host.modsOf(objc.Send[uint64](ev, selModifierFlags)))
}

func (win *window) otherUp(ev objc.ID) {
	if objc.Send[int64](ev, selButtonNumber) != buttonMiddle {
		return
	}

	x, y := win.eventPixels(ev)
	win.host.kit.Button(win.ctx, x, y, buttonMiddle, true, win.releaseMods(ev))
}

// motion skips moves stamped just after a warp: they still carry the old position, and a
// continuous drag would jump.
func (win *window) motion(ev objc.ID) {
	if objc.Send[float64](ev, selTimestamp) < win.host.warpTime+warpSettle {
		return
	}

	x, y := win.eventPixels(ev)
	win.host.kit.Motion(win.ctx, x, y, win.host.modsOf(objc.Send[uint64](ev, selModifierFlags)))
}

// mouseExited leaves a drag alone, as a drag keeps the pointer.
func (win *window) mouseExited(objc.ID) {
	if win.host.buttons() != 0 {
		return
	}

	win.host.showCursor(true)
	clsNSCursor.Send(selArrowCursor).Send(selSet)
	win.host.kit.Motion(win.ctx, offWindow, offWindow, 0)
}

// takeNotches splits whole notches off a scroll sum, at most maxNotches of them. It
// returns the rest, the direction and how many.
func takeNotches(sum float64) (rest float64, dir, n int) {
	dir = 1
	if sum < 0 {
		dir = -1
	}

	for n < maxNotches && math.Abs(sum) >= scrollStep {
		sum -= float64(dir) * scrollStep
		n++
	}

	return sum, dir, n
}

// scrollWheel takes one notch per event from a wheel, and sums a trackpad's precise deltas
// into notches. Horizontal scrolling is Shift and the wheel, as in the X11 host, and macOS
// turns Shift and a wheel into horizontal deltas anyway.
func (win *window) scrollWheel(ev objc.ID) {
	h := win.host
	momentum := objc.Send[uint64](ev, selMomentumPhase)
	phase := objc.Send[uint64](ev, selPhase)

	// Momentum after a key press would turn into another action, such as Ctrl and scroll.
	if momentum != 0 {
		if h.ignoreMomentum {
			return
		}
	} else {
		h.ignoreMomentum = false
	}

	x, y := win.eventPixels(ev)
	mods := h.modsOf(objc.Send[uint64](ev, selModifierFlags)) &^ engine.ModButton1

	if phase == 0 && momentum == 0 {
		win.wheelEvent(ev, x, y, mods)

		return
	}

	const phaseBegan = 1
	if phase == phaseBegan {
		win.scrollX, win.scrollY = 0, 0
	}

	dx, dy := objc.Send[float64](ev, selScrollingDeltaX), objc.Send[float64](ev, selScrollingDeltaY)
	if !objc.Send[bool](ev, selHasPreciseDeltas) {
		dx, dy = dx*scrollStep, dy*scrollStep
	}

	var dir, n int

	win.scrollY, dir, n = takeNotches(win.scrollY + dy)
	win.wheel(x, y, dir, n, mods)

	win.scrollX, dir, n = takeNotches(win.scrollX + dx)
	win.wheel(x, y, dir, n, mods|engine.ModShift)
}

func (win *window) wheelEvent(ev objc.ID, x, y, mods int) {
	sign := func(d float64) int {
		if d > 0 {
			return 1
		}

		return -1
	}

	if dx := objc.Send[float64](ev, selDeltaX); dx != 0 {
		win.wheel(x, y, sign(dx), 1, mods|engine.ModShift)
	}

	if dy := objc.Send[float64](ev, selDeltaY); dy != 0 {
		win.wheel(x, y, sign(dy), 1, mods)
	}
}

// wheel stops if a handler closes the window.
func (win *window) wheel(x, y, dir, n, mods int) {
	for range n {
		if win.nswin == 0 {
			return
		}

		win.host.kit.Wheel(win.ctx, x, y, dir, mods)
	}
}

// addTracking asks for enter, exit and cursor updates over the whole view. The visible-rect
// option makes the area follow resizes, so it is only added once.
func addTracking(view objc.ID) {
	area := clsNSTrackingArea.Send(selAlloc).Send(selInitTracking, nsRect{}, uint64(trackingOptions), view, objc.ID(0))
	view.Send(selAddTracking, area)
	area.Send(selRelease)
}

// dispatch sends an event on through AppKit, with the two exceptions of the C host.
func (h *Host) dispatch(ev objc.ID) {
	// AppKit is inside the event from here on, also while this handles it itself.
	h.depth++

	defer func() { h.depth-- }()

	switch objc.Send[uint64](ev, selType) {
	case eventKeyDown:
		// Control and Tab never reach the view, as AppKit moves between views with them.
		if objc.Send[uint16](ev, selKeyCode) == keyCodeTab && objc.Send[uint64](ev, selModifierFlags)&flagControl != 0 {
			if win := h.findNS(ev.Send(selEventWindow)); win != nil {
				guarded(func() { win.handleKey(ev, true) })

				return
			}
		}
	case eventKeyUp:
		// NSApp swallows key-ups while Command or Option is held. It may still pass one on,
		// which keyUpDone catches.
		if objc.Send[uint64](ev, selModifierFlags)&(flagCommand|flagOption) != 0 {
			if win := h.findNS(ev.Send(selEventWindow)); win != nil {
				guarded(func() { win.handleKey(ev, false) })

				h.keyUpDone = ev
			}
		}
	}

	h.app.Send(selSendEvent, ev)
	h.keyUpDone = 0
}

// releaseDead releases windows freed while AppKit was inside one of their events.
func (h *Host) releaseDead() {
	if h.depth > 0 {
		return
	}

	for _, ns := range h.dead {
		ns.Send(selRelease)
	}

	h.dead = nil
}
