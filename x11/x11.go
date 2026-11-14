// SPDX-License-Identifier: GPL-2.0-or-later

// Package x11 shows package bui's node trees in X11 windows, in pure Go. It is a port of
// BUI's kit_window_x11.c on xgb, built on bui.Headless and bui.Host only: the kit renders
// into a raster in software, and this package forwards input and presents the pixels. It
// works with either of package bui's engines.
//
// Threading: as in package bui, everything here is UI-thread only. Run's loop runs the
// functions bui.Do queues, so other goroutines reach the UI thread through bui.Do.
//
// The window forwards what bui.Headless takes. The kit's raw key releases and the middle
// button reach only "events" nodes, which package bui has no handler for, so they are not
// sent.
package x11

import bui "github.com/CWBudde/bui-go"

// std is the process's host: one X connection, made at the first Supported, as the C
// host has it.
var std = newHost()

// Supported reports whether windows can be opened. It connects to the X server named by
// DISPLAY the first time, and the connection stays for the life of the process.
func Supported() bool {
	return std.Supported()
}

// Window is an X window showing a node tree.
type Window struct {
	win *window
}

// NewWindow creates a window on root, hidden until Show. w and h are in units of the UI
// scale, which comes from BUI_UI_SCALE or the server's Xft.dpi. The error is
// bui.ErrNoDisplay without an X server. Like bui.NewHeadless, it panics if a live Window or
// Headless holds root, an ancestor or a descendant of it.
func NewWindow(root *bui.Node, title string, w, h int) (*Window, error) {
	win, err := std.newWindow(root, title, w, h)
	if err != nil {
		return nil, err
	}

	return &Window{win: win}, nil
}

// Show maps the window.
func (w *Window) Show() {
	w.live().show()
}

// Close closes the window and frees its bui.Headless. The window does not own the root:
// close that afterwards. Close is idempotent, and from a handler the Headless waits like
// Node.Close.
func (w *Window) Close() {
	if w.win.freed {
		return
	}

	w.win.free()
}

func (w *Window) live() *window {
	if w.win.freed {
		panic("x11: use of closed Window")
	}

	return w.win
}

// Run runs the message loop until Quit or until every window is closed. It runs the
// functions bui.Do queued, before the loop, on each wake and after it. It returns
// bui.ErrNoDisplay without an X server, leaving queued functions in place, and
// bui.ErrDisplayLost if the connection breaks: every window is closed by then, and later
// calls to Run and NewWindow report bui.ErrNoDisplay.
//
// A panic in a handler ends the process, as the kit's frames are on the stack. One in a
// function bui.Do queued propagates from Run, as Run calls it outside any kit call. The
// functions behind it stay queued, and Run can be called again. Run panics in a handler
// and inside Run or bui.Run.
func Run() error {
	return std.run()
}

// Quit ends the message loop. Outside the loop it makes the next Run return without
// looping, so that a bui.Do(Quit) queued before Run counts.
func Quit() {
	std.Quit()
}
