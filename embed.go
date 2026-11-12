// SPDX-License-Identifier: GPL-2.0-or-later

package bui

import (
	"time"

	"github.com/CWBudde/bui-go/internal/engine"
)

// Host is what the kit asks of the window a program shows a Headless in: the kit's
// BKHost without its events, which reach handlers as before. Install one with
// Headless.SetHost.
//
// Its methods run on the UI thread inside a kit call, such as the Motion that moved the
// pointer over a text field. They must not call into this package, apart from closing
// things, which waits until that kit call returns. They note what to do and let the
// program's loop do it. A panic in one ends the process, as one in a handler does.
type Host interface {
	// Redraw asks for a render on the next turn of the program's loop.
	Redraw()
	// SetCursor sets the pointer shape over the window.
	SetCursor(c Cursor)
	// Warp moves the pointer to raster coordinates. A number field being dragged wraps
	// the pointer at the window's edges, so that a drag can go on.
	Warp(x, y int)
	// ClipboardGet returns the clipboard's text. ok is false if there is none, and the
	// kit then pastes the text it copied last itself.
	ClipboardGet() (text string, ok bool)
	ClipboardSet(text string)
	// Schedule asks for Headless.Timer(t) once after has passed. The kit ignores a
	// timer that is no longer current, so a host can fire every one it is given.
	Schedule(after time.Duration, t Timer)
}

// Cursor is a pointer shape, as the kit's BK_CURSOR_* values.
type Cursor int

// The pointer shapes the kit asks for.
const (
	CursorDefault    Cursor = iota
	CursorEW                // horizontal arrows, over number fields
	CursorNS                // vertical arrows
	CursorText              // over text fields
	CursorCrosshair         // eyedropper, colour areas
	CursorHand              // a link
	CursorMove              // a panel's grip
	CursorNone              // hidden, while a number field is dragged
	CursorNESW              // diagonal resize, bottom left to top right
	CursorNWSE              // diagonal resize, top left to bottom right
	CursorNotAllowed        // the action is not allowed
	CursorWait              // busy
	CursorProgress          // busy in the background, still interactive
)

// Timer names a callback the kit asked for with Host.Schedule.
type Timer int

// SetHost makes host the window of h, so that the kit's redraw requests, cursor, pointer
// warps, clipboard and timers reach it. nil removes the host. Close removes it too.
func (h *Headless) SetHost(host Host) {
	ctx := h.live()

	if host == nil {
		kit.ContextSetEmitHost(ctx)
		return
	}

	kit.ContextSetWindowHost(ctx, hostAdapter{host})
}

// Timer fires a timer that Host.Schedule asked for. A tooltip timer shows the tooltip.
func (h *Headless) Timer(t Timer) {
	kit.Timer(h.live(), int(t))
	runDeferred()
}

// Animating reports whether a program should keep rendering frames, as while a panel
// opens. A program that renders only on Redraw would show such an animation stalled.
func (h *Headless) Animating() bool {
	return kit.Animating(h.live())
}

// hostAdapter turns a Host into what the engines call. Each call counts as an emit, so
// that a Close in it waits until the kit call it runs in has returned, and a panic in it
// ends the process on every engine, as one in a handler does.
type hostAdapter struct{ h Host }

var _ engine.WindowHost = hostAdapter{}

func (a hostAdapter) Request()             { call(a.h.Redraw) }
func (a hostAdapter) SetCursor(cursor int) { call(func() { a.h.SetCursor(Cursor(cursor)) }) }
func (a hostAdapter) Warp(x, y int)        { call(func() { a.h.Warp(x, y) }) }

func (a hostAdapter) ClipboardGet() (text string, ok bool) {
	call(func() { text, ok = a.h.ClipboardGet() })

	return text, ok
}

func (a hostAdapter) ClipboardSet(text string) { call(func() { a.h.ClipboardSet(text) }) }

func (a hostAdapter) Schedule(ms, timer int) {
	call(func() { a.h.Schedule(time.Duration(ms)*time.Millisecond, Timer(timer)) })
}

// call runs f, a call into the program's Host, as an emit. It defers
// engine.ExitIfUnwinding itself, as recover works only there. A panic ends the process,
// so emitDepth needs no deferred decrement.
func call(f func()) {
	returned := false
	defer engine.ExitIfUnwinding(&returned)

	emitDepth++

	f()

	emitDepth--

	returned = true
}

// Flush runs destruction that waits for a kit call to return, then the functions Do
// queued, as Run's loop does on a wake. A program that runs a loop of its own instead of
// Run calls it there, after SetWake's function told it to. It panics inside Run and in a
// handler, where the kit is still delivering an event.
func Flush() {
	if inRun || emitDepth > 0 {
		panic("bui: Flush called from inside Run or a handler")
	}

	runDeferred()
	drainDo()
}

// SetWake sets a function that Do calls after it queued a function, so that a program
// with a loop of its own can wake it and call Flush there. It runs on the goroutine that
// called Do, so it must be safe from any goroutine and must not block. nil removes it.
// Run's loop is woken as well, whether a function is set or not.
func SetWake(wake func()) {
	doMu.Lock()
	defer doMu.Unlock()

	doWake = wake
}
