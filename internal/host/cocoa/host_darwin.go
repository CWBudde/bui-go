// SPDX-License-Identifier: GPL-2.0-or-later

// Package cocoa is a window host in pure Go on AppKit, reached through purego and the
// Objective-C runtime, a port of BUI's kit_window_cocoa.m. The kit renders into a raster in
// software, so a host only forwards input and presents pixels.
//
// The port isn't finished: it has no menu, live resize or scale changes between screens yet.
//
// All of it runs on the main OS thread, which AppKit requires, except Wake. Package bui
// keeps the main goroutine there.
package cocoa

import (
	"runtime"
	"sync/atomic"
	"time"

	"github.com/CWBudde/bui-go/internal/engine"
	"github.com/ebitengine/purego/objc"
)

const (
	activationPolicyRegular    = 0
	activationPolicyProhibited = 2
	eventMaskAny               = ^uint64(0)
	eventTypeAppDefined        = 15
	// wakeSubtype marks the event that ends a wait in nextEvent, as in the C host.
	wakeSubtype = 0x4255

	framePeriod      = time.Second / 60
	maxEventsPerTurn = 256
)

// Host implements engine.Host on AppKit.
type Host struct {
	kit engine.Kit

	app objc.ID

	windows []*window
	lastID  engine.Window
	open    int

	// wakeWanted says a wake's work is due. wakePosted says the event that ends the wait
	// is in the queue, so that a burst of wakes posts one.
	ready      atomic.Bool
	wakeWanted atomic.Bool
	wakePosted atomic.Bool
	wakeFunc   func()
	quit       bool

	frameDue time.Time
	session  int8

	// depth counts the events AppKit is dispatching, and dead holds what they freed.
	depth int
	dead  []objc.ID

	ignoreMomentum bool    // a key was pressed since momentum scrolling began
	keyUpDone      objc.ID // the key-up that dispatch handled, not retained
	warpTime       float64 // the system uptime of the last warp
	cursorHidden   bool

	// The app-wide state a test mustn't take from the user: their mouse's buttons, their
	// clipboard and their focus.
	buttons    func() uint64
	pasteboard func() objc.ID
	noActivate bool
}

var _ engine.Host = (*Host)(nil)

// New returns a host for kit. It touches neither AppKit nor kit before a window opens.
func New(kit engine.Kit) *Host {
	return &Host{
		kit:        kit,
		buttons:    func() uint64 { return objc.Send[uint64](clsNSEvent, selPressedButtons) },
		pasteboard: func() objc.ID { return clsNSPasteboard.Send(selGeneralPasteboard) },
	}
}

// Supported implements engine.Host: AppKit needs the main thread and a GUI session.
func (h *Host) Supported() bool {
	if !load() || pthreadMainNP() == 0 {
		return false
	}

	if h.session == 0 {
		h.session = -1

		if d := cgSessionCurrent(); d != 0 {
			cfRelease(d)

			h.session = 1
		}
	}

	return h.session > 0
}

// start makes the process an application, once.
func (h *Host) start() {
	if h.app != 0 {
		return
	}

	h.app = clsNSApplication.Send(selSharedApplication)

	policy := activationPolicyRegular
	if h.noActivate {
		// Anything else comes to the front by itself once it waits for events, with no call
		// asking for it.
		policy = activationPolicyProhibited
	}

	h.app.Send(selSetActivationPolicy, policy)
	h.setUpApp()
	h.app.Send(selFinishLaunching)
	h.ready.Store(true)
}

// WakeInit implements engine.Host.
func (h *Host) WakeInit(f func()) { h.wakeFunc = f }

// Wake implements engine.Host. NSApplication documents postEvent as callable from any
// thread. A pool belongs to its OS thread, so the goroutine stays on one until it pops.
func (h *Host) Wake() {
	if !h.ready.Load() {
		return
	}

	h.wakeWanted.Store(true)

	if h.wakePosted.Swap(true) {
		return
	}

	runtime.LockOSThread()

	defer runtime.UnlockOSThread()

	pool := poolPush()
	defer poolPop(pool)

	ev := clsNSEvent.Send(selOtherEvent, uint64(eventTypeAppDefined), nsPoint{}, uint64(0), float64(0),
		int64(0), objc.ID(0), int16(wakeSubtype), int64(0), int64(0))
	h.app.Send(selPostEvent, ev, false)
}

// Quit implements engine.Host.
func (h *Host) Quit() {
	h.quit = true
	h.Wake()
}

// Run implements engine.Host. As in the C host it erases an earlier Quit, and returns
// right away with no window open.
func (h *Host) Run() error {
	if h.app == 0 {
		return nil
	}

	h.quit = false

	for h.open > 0 && !h.quit {
		h.turn()
	}

	return nil
}

// turn is one pass of the loop: wait, then events, wakes, timers and renders.
func (h *Host) turn() {
	pool := poolPush()
	defer poolPop(pool)

	// A loop nested in a callback may have taken the posted event, so nothing says it is
	// still queued. Clear the flag before looking at wakeWanted: a wake landing later posts
	// again and ends the wait, and one landing earlier is seen here and skips the wait.
	h.wakePosted.Store(false)

	until := objc.ID(0)
	if !h.wakeWanted.Load() {
		until = h.untilDate()
	}

	h.pump(until)
	h.releaseDead()

	if h.wakeWanted.Swap(false) {
		h.drainWake()
	}

	now := time.Now()

	for _, win := range h.windowsSnapshot() {
		if win.nswin != 0 {
			win.runTimers(now)
		}
	}

	h.renderPending(now)
}

// pump waits for an event until the date, then handles what is queued. A nil date doesn't wait.
func (h *Host) pump(until objc.ID) {
	ev := h.app.Send(selNextEvent, eventMaskAny, until, nsDefaultRunLoopMode, true)

	for range maxEventsPerTurn {
		if ev == 0 {
			return
		}

		if !isWake(ev) {
			h.dispatch(ev)
		}

		ev = h.app.Send(selNextEvent, eventMaskAny, objc.ID(0), nsDefaultRunLoopMode, true)
	}
}

func isWake(ev objc.ID) bool {
	return objc.Send[uint64](ev, selType) == eventTypeAppDefined && objc.Send[int16](ev, selSubtype) == wakeSubtype
}

func (h *Host) untilDate() objc.ID {
	wait, forever := h.nextWait(time.Now())
	if forever {
		return clsNSDate.Send(selDistantFuture)
	}

	return clsNSDate.Send(selDateSinceNow, wait.Seconds())
}

func (h *Host) windowsSnapshot() []*window {
	return append([]*window(nil), h.windows...)
}

// nextWait returns how long nothing is due, or forever.
func (h *Host) nextWait(now time.Time) (wait time.Duration, forever bool) {
	forever = true

	note := func(d time.Duration) {
		if forever || d < wait {
			wait, forever = max(d, 0), false
		}
	}

	for _, win := range h.windows {
		if win.nswin == 0 {
			continue
		}

		if win.renderPending {
			note(h.frameDue.Sub(now))
		}

		if !win.tipDue.IsZero() {
			note(win.tipDue.Sub(now))
		}

		if !win.customDue.IsZero() {
			note(win.customDue.Sub(now))
		}
	}

	return wait, forever
}

func (h *Host) renderPending(now time.Time) {
	if now.Before(h.frameDue) {
		return
	}

	animating := false

	for _, win := range h.windowsSnapshot() {
		if win.nswin != 0 && win.renderPending {
			win.render()

			animating = animating || win.renderPending
		}
	}

	if animating {
		h.frameDue = now.Add(framePeriod)
	}
}

// drainWake is guarded like a callback from the kit, as the X11 host's is.
func (h *Host) drainWake() {
	returned := false

	defer engine.ExitIfUnwinding(&returned)

	h.kit.ImagePoll()

	if h.wakeFunc != nil {
		h.wakeFunc()
	}

	returned = true
}
