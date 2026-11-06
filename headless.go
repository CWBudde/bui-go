// SPDX-License-Identifier: GPL-2.0-or-later

package bui

/*
#include "bridge.h"
*/
import "C"

// context is a BKContext with the emit-only host, shared by Window and
// Headless.
type context struct {
	c       *C.BKContext
	root    *Node
	closing bool
}

// newContext attaches a context to root. The host goes in first: BK_window_new
// saves the host it finds and forwards only emit.
func newContext(root *Node) *context {
	c := root.live()
	if root.owner != nil {
		panic("bui: node is already the root of a Window or Headless")
	}
	ctx := &context{c: C.BK_context_new(c), root: root}
	C.bui_context_set_emit_host(ctx.c)
	root.owner = ctx
	return ctx
}

// free destroys the context but not the root.
func (ctx *context) free() {
	C.BK_context_free(ctx.c)
	ctx.c = nil
	ctx.root.owner = nil
}

// Headless drives a node tree without a window system: it renders into an
// in-memory raster and takes synthetic pointer input. It is UI-thread only like
// the rest of the kit-touching API.
type Headless struct {
	ctx  *context
	r    *C.BuiRaster
	w, h int
}

// NewHeadless creates a context on root with a w x h raster. Close it before
// closing root.
func NewHeadless(root *Node, w, h int) *Headless {
	return &Headless{
		ctx: newContext(root),
		r:   C.BUI_raster_create(C.int(w), C.int(h)),
		w:   w,
		h:   h,
	}
}

func (h *Headless) live() *C.BKContext {
	if h.ctx.closing {
		panic("bui: use of closed Headless")
	}
	return h.ctx.c
}

// Render lays out and draws the whole tree.
func (h *Headless) Render() {
	C.BK_render(h.live(), h.r, C.int(h.w), C.int(h.h), 1)
	runDeferred()
}

// Press sends a left-button press. The kit reports non-zero even on a miss, so
// nothing is returned: whether it hit shows in the handlers.
func (h *Headless) Press(x, y int) {
	C.BK_press(h.live(), C.int(x), C.int(y), C.BK_BUTTON1)
	runDeferred()
}

// Release sends a left-button release; a button under it fires OnClick.
func (h *Headless) Release(x, y int) {
	C.BK_release(h.live(), C.int(x), C.int(y), C.BK_BUTTON1)
	runDeferred()
}

// Close frees the context and raster, not the root: close that afterwards. It
// is idempotent, and from a handler it waits like Node.Close.
func (h *Headless) Close() {
	if h.ctx.closing {
		return
	}
	h.ctx.closing = true
	closeOrDefer(func() {
		h.ctx.free()
		C.BUI_raster_free(h.r)
		h.r = nil
	})
}
