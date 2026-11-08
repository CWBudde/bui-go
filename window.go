// SPDX-License-Identifier: GPL-2.0-or-later

package bui

import "github.com/CWBudde/bui-go/internal/engine"

// Window is a native window showing a node tree. It is UI-thread only.
type Window struct {
	c   engine.Window
	ctx *context
}

// NewWindow creates a window on root, hidden until Show. The error is
// ErrNoDisplay without a window system. Like NewHeadless, it panics if a live
// Window or Headless holds root, an ancestor or a descendant of it.
func NewWindow(root *Node, title string, w, h int) (*Window, error) {
	root.live()

	if !Supported() {
		return nil, ErrNoDisplay
	}

	ctx := newContext(root)

	c := host.WindowNew(ctx.c, title, w, h)
	if c == 0 {
		ctx.free()
		return nil, ErrNoDisplay
	}

	return &Window{c: c, ctx: ctx}, nil
}

func (w *Window) live() engine.Window {
	if w.ctx.closing {
		panic("bui: use of closed Window")
	}

	return w.c
}

// Show maps the window.
func (w *Window) Show() {
	host.WindowShow(w.live())
}

// Close frees the window and then its context. The window does not own the
// root: close that afterwards, as the order window, context, root is mandatory.
// Close is idempotent, and from a handler it waits like Node.Close.
func (w *Window) Close() {
	if w.ctx.closing {
		return
	}

	w.ctx.closing = true

	closeOrDefer(func() {
		host.WindowFree(w.c)
		w.c = 0
		w.ctx.free()
	})
}
