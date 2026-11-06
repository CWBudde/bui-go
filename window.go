// SPDX-License-Identifier: GPL-2.0-or-later

package bui

/*
#include <stdlib.h>
#include "bridge.h"
*/
import "C"

import "unsafe"

// Window is a native window showing a node tree. It is UI-thread only.
type Window struct {
	c   *C.BKWindow
	ctx *context
}

// NewWindow creates a window on root, hidden until Show. The error is
// ErrNoDisplay without a window system.
func NewWindow(root *Node, title string, w, h int) (*Window, error) {
	root.live()
	if !Supported() {
		return nil, ErrNoDisplay
	}
	ctx := newContext(root)
	ct := C.CString(title)
	defer C.free(unsafe.Pointer(ct))
	c := C.BK_window_new(ctx.c, ct, C.int(w), C.int(h))
	if c == nil {
		ctx.free()
		return nil, ErrNoDisplay
	}
	return &Window{c: c, ctx: ctx}, nil
}

func (w *Window) live() *C.BKWindow {
	if w.ctx.closing {
		panic("bui: use of closed Window")
	}
	return w.c
}

// Show maps the window.
func (w *Window) Show() {
	C.BK_window_show(w.live())
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
		C.BK_window_free(w.c)
		w.c = nil
		w.ctx.free()
	})
}
