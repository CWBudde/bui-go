// SPDX-License-Identifier: GPL-2.0-or-later

package bui

import (
	"encoding/binary"
	"fmt"
	"image"

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
	c := checkRoot(root)

	handle := kit.ContextNew(c)
	if handle == 0 {
		panic("bui: cannot create a context")
	}

	ctx := &context{c: handle, root: root}
	kit.ContextSetEmitHost(ctx.c)
	root.owner = ctx

	return ctx
}

// checkRoot panics unless root can take a context: it is live, not a root yet,
// and no live context holds an ancestor or a descendant of it.
func checkRoot(root *Node) engine.Node {
	c := root.live()
	if root.owner != nil {
		panic("bui: node is already the root of a Window or Headless")
	}

	if liveRootAbove(c) != nil || liveRootIn(c) != nil {
		panic("bui: a Window or Headless already holds an ancestor or a descendant of this node")
	}

	return c
}

// free destroys the context but not the root.
func (ctx *context) free() {
	kit.ContextFree(ctx.c)
	ctx.c = 0
	ctx.root.owner = nil
}

// Headless drives a node tree without a window system: it renders into an
// in-memory raster and takes synthetic input. It is UI-thread only like the rest
// of the kit-touching API.
//
// Its input calls are the kit's, so a program can show the raster in a window of
// its own and forward that window's events. Coordinates are raster pixels. Without
// a window host, the kit's cursor, clipboard and timer requests go nowhere.
type Headless struct {
	ctx    *context
	r      engine.Raster
	w, h   int // the size of the next render
	rw, rh int // the raster's size, from the last render
}

// Mod is a set of modifier keys and held buttons, as the kit's BK_SHIFT and so on.
type Mod int

// The modifier bits of input calls.
const (
	ModShift   Mod = engine.ModShift
	ModCtrl    Mod = engine.ModCtrl
	ModAlt     Mod = engine.ModAlt
	ModButton1 Mod = engine.ModButton1 // the left button is held
)

// maxHeadlessPixels is 256 MiB of raster, or 8192 x 8192. The wasm engine's limit is near
// twice that, where the kit ends the process, and the native one crashes or hangs on far
// larger sizes. This leaves the module room for everything else it holds.
const maxHeadlessPixels = 1 << 26

// NewHeadless creates a context on root with a w x h raster. Close it before
// closing root. It panics on a size that is not positive or is too large for a raster,
// and if a live Window or Headless holds root, an ancestor or a descendant of it.
func NewHeadless(root *Node, w, h int) *Headless {
	checkHeadlessSize(w, h)

	ctx := newContext(root)

	r := kit.RasterNew(w, h)
	if r == 0 {
		ctx.free()
		panic(fmt.Sprintf("bui: cannot create a raster of %d x %d", w, h))
	}

	return &Headless{ctx: ctx, r: r, w: w, h: h, rw: w, rh: h}
}

func checkHeadlessSize(w, h int) {
	if w <= 0 || h <= 0 || w > maxHeadlessPixels/h {
		panic(fmt.Sprintf("bui: Headless size %d x %d is out of range", w, h))
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
	h.rw, h.rh = h.w, h.h

	runDeferred()
}

// RenderDamage draws what changed since the last render, or everything if full,
// and returns the rectangles whose pixels differ from the previous frame: none if
// nothing changed, and the whole raster after a Resize. A full render that draws
// the same pixels returns none too.
func (h *Headless) RenderDamage(full bool) []image.Rectangle {
	ctx := h.live()
	n := kit.RenderDamage(ctx, h.r, h.w, h.h, full)
	h.rw, h.rh = h.w, h.h

	var damage []image.Rectangle

	if n > 0 {
		for _, r := range kit.RasterDamage(h.r) {
			damage = append(damage, image.Rect(r.X, r.Y, r.X+r.W, r.Y+r.H))
		}
	}

	runDeferred()

	return damage
}

// NeedsRender reports whether anything is waiting to be drawn, a Resize to a
// size other than the last render's included.
func (h *Headless) NeedsRender() bool {
	ctx := h.live()

	return h.w != h.rw || h.h != h.rh || kit.NeedsRender(ctx)
}

// Resize sets the size of the next render. It panics where NewHeadless does.
func (h *Headless) Resize(w, height int) {
	h.live()
	checkHeadlessSize(w, height)
	h.w, h.h = w, height
}

// Image returns a copy of the raster as the last render left it, opaque.
func (h *Headless) Image() *image.RGBA {
	h.live()

	pixels := kit.RasterPixels(h.r)
	img := image.NewRGBA(image.Rect(0, 0, h.rw, h.rh))

	if len(pixels) != h.rw*h.rh {
		panic(fmt.Sprintf("bui: raster has %d pixels, want %d x %d", len(pixels), h.rw, h.rh))
	}

	// 0x00RRGGBB shifted left and made opaque is R, G, B, A in big-endian order.
	for i, p := range pixels {
		binary.BigEndian.PutUint32(img.Pix[4*i:], p<<8|0xff)
	}

	return img
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

// PressMods is Press with modifier keys held. It adds ModButton1, as the kit
// expects the state with the button already down.
func (h *Headless) PressMods(x, y int, mods Mod) {
	kit.PressMods(h.live(), x, y, int(mods|ModButton1))
	runDeferred()
}

// ReleaseMods is Release with modifier keys held. It drops ModButton1.
func (h *Headless) ReleaseMods(x, y int, mods Mod) {
	kit.ReleaseMods(h.live(), x, y, int(mods&^ModButton1))
	runDeferred()
}

// Motion moves the pointer. Pass ModButton1 while the left button is held, to
// drag. Moving it far outside the raster leaves every widget. Like the other input
// calls below, it reports whether anything changed.
func (h *Headless) Motion(x, y int, mods Mod) bool {
	changed := kit.Motion(h.live(), x, y, int(mods))

	runDeferred()

	return changed
}

// Wheel turns the wheel by delta steps, positive up.
func (h *Headless) Wheel(x, y, delta int, mods Mod) bool {
	changed := kit.Wheel(h.live(), x, y, delta, int(mods))

	runDeferred()

	return changed
}

// Scroll scrolls precisely, as a touchpad does: dx and dy are wheel notches, with
// dx > 0 right and dy > 0 up.
func (h *Headless) Scroll(x, y int, dx, dy float32, mods Mod) bool {
	changed := kit.Scroll(h.live(), x, y, dx, dy, int(mods))

	runDeferred()

	return changed
}

// Key presses a key. keysym is its X keysym name, such as "a", "Return" or
// "adiaeresis", and text the characters it types, "" for none. Shortcuts pass the
// keysym with ModCtrl and no text.
func (h *Headless) Key(text, keysym string, mods Mod) bool {
	changed := kit.Key(h.live(), text, keysym, int(mods))

	runDeferred()

	return changed
}

// ContextMenu opens the context menu at a point, as a right-button press does.
func (h *Headless) ContextMenu(x, y int, mods Mod) bool {
	changed := kit.ContextMenu(h.live(), x, y, int(mods))

	runDeferred()

	return changed
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
