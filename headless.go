// SPDX-License-Identifier: GPL-2.0-or-later

package bui

import (
	"fmt"

	"github.com/CWBudde/bui-go/internal/engine"
)

// context is a BKContext with the emit-only host, shared by Window and
// Headless.
type context struct {
	c       engine.Context
	root    *Node
	closing bool
}

// newContext attaches a context to root. The host goes in first: BK_window_new
// saves the host it finds and forwards only emit.
//
// Contexts don't nest. The kit finds a node's context by walking up to the
// nearest root, so a context around another one never hears that a node inside
// the inner one went away, and keeps pointing at it.
func newContext(root *Node) *context {
	c := root.live()
	if root.owner != nil {
		panic("bui: node is already the root of a Window or Headless")
	}

	if liveRootAbove(c) != nil || liveRootIn(c) != nil {
		panic("bui: a Window or Headless already holds an ancestor or a descendant of this node")
	}

	handle := kit.ContextNew(c)
	if handle == 0 {
		panic("bui: cannot create a context")
	}

	ctx := &context{c: handle, root: root}
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

// maxHeadlessPixels is 256 MiB of raster, or 8192 x 8192. The wasm engine's limit is near
// twice that, where the kit ends the process, and the native one crashes or hangs on far
// larger sizes. This leaves the module room for everything else it holds.
const maxHeadlessPixels = 1 << 26

// NewHeadless creates a context on root with a w x h raster. Close it before
// closing root. It panics on a size that is not positive or is too large for a raster,
// and if a live Window or Headless holds root, an ancestor or a descendant of it.
func NewHeadless(root *Node, w, h int) *Headless {
	if w <= 0 || h <= 0 || w > maxHeadlessPixels/h {
		panic(fmt.Sprintf("bui: Headless size %d x %d is out of range", w, h))
	}

	ctx := newContext(root)

	r := kit.RasterNew(w, h)
	if r == 0 {
		ctx.free()
		panic(fmt.Sprintf("bui: cannot create a raster of %d x %d", w, h))
	}

	return &Headless{ctx: ctx, r: r, w: w, h: h}
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
