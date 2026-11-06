// SPDX-License-Identifier: GPL-2.0-or-later

package bui

import "github.com/CWBudde/bui-go/internal/engine"

// context is a BKContext with the emit-only host, shared by Window and
// Headless.
type context struct {
	c       engine.Context
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

	ctx := &context{c: kit.ContextNew(c), root: root}
	kit.ContextSetEmitHost(ctx.c)
	root.owner = ctx

	return ctx
}

// free destroys the context but not the root.
func (ctx *context) free() {
	kit.ContextFree(ctx.c)
	ctx.c = 0
	ctx.root.owner = nil
}

// Headless drives a node tree without a window system: it renders into an
// in-memory raster and takes synthetic pointer input. It is UI-thread only like
// the rest of the kit-touching API.
type Headless struct {
	ctx  *context
	r    engine.Raster
	w, h int
}

// NewHeadless creates a context on root with a w x h raster. Close it before
// closing root.
func NewHeadless(root *Node, w, h int) *Headless {
	return &Headless{
		ctx: newContext(root),
		r:   kit.RasterNew(w, h),
		w:   w,
		h:   h,
	}
}

func (h *Headless) live() engine.Context {
	if h.ctx.closing {
		panic("bui: use of closed Headless")
	}

	return h.ctx.c
}

// Render lays out and draws the whole tree.
func (h *Headless) Render() {
	kit.Render(h.live(), h.r, h.w, h.h)
	runDeferred()
}

// Press sends a left-button press. The kit reports non-zero even on a miss, so
// nothing is returned: whether it hit shows in the handlers.
func (h *Headless) Press(x, y int) {
	kit.Press(h.live(), x, y)
	runDeferred()
}

// Release sends a left-button release; a button under it fires OnClick.
func (h *Headless) Release(x, y int) {
	kit.Release(h.live(), x, y)
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
		kit.RasterFree(h.r)
		h.r = 0
	})
}
