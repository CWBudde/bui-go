// SPDX-License-Identifier: GPL-2.0-or-later

package x11

import (
	"time"

	"github.com/CWBudde/bui-go/internal/engine"
	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

// The kit's BK_CURSOR_* values with a meaning for the host, and how many it uses. The
// kit's BK_CURSOR_PROGRESS is past them and shows as the last, as in the C host.
const (
	cursorNone  = 7
	cursorCount = 12
	// timerCustom is BK_TIMER_CUSTOM, from the kit's internal header: a custom node's
	// frame. Any other value of a schedule is the tooltip's serial.
	timerCustom = -1
	// maxWindowSize is what fits X's int16 geometry.
	maxWindowSize = 32767
)

// cursorGlyphs are the cursor font's shapes for the BK_CURSOR_* values, from X11's
// cursorfont.h, in the order of GHOST_WindowX11::setWindowCursorShape. Index cursorNone
// is an empty bitmap instead.
var cursorGlyphs = [cursorCount]uint16{
	68,  // XC_left_ptr
	108, // XC_sb_h_double_arrow
	116, // XC_sb_v_double_arrow
	152, // XC_xterm
	34,  // XC_crosshair
	60,  // XC_hand2
	52,  // XC_fleur
	68,  // unused: cursorNone
	12,  // XC_bottom_left_corner
	14,  // XC_bottom_right_corner
	0,   // XC_X_cursor
	150, // XC_watch
}

// window is one X window on a kit context. It implements engine.WindowHost, so the kit
// reaches it through the context.
type window struct {
	host   *Host
	id     engine.Window
	xid    xproto.Window // 0 once closed
	gc     xproto.Gcontext
	ctx    engine.Context
	raster engine.Raster

	w, h          int // the window's size, which is the raster's next size
	rasterW       int
	rasterH       int
	renderPending bool
	full          bool

	tipID             int
	tipDue, customDue time.Time
}

var _ engine.WindowHost = (*window)(nil)

// clampSize makes a logical size a window size: X has neither empty nor huge windows.
func clampSize(v int, scale float32) int {
	return min(max(int(float32(v)*scale), 1), maxWindowSize)
}

// WindowNew implements engine.Host.
func (h *Host) WindowNew(ctx engine.Context, title string, width, height int) engine.Window {
	if !h.Supported() {
		return 0
	}

	h.kit.SetScale(h.scale)

	win := &window{
		host: h,
		ctx:  ctx,
		w:    clampSize(width, h.scale),
		h:    clampSize(height, h.scale),
	}

	if !win.create(title) {
		return 0
	}

	win.raster = h.kit.RasterNew(1, 1)
	win.rasterW, win.rasterH = 1, 1

	h.lastID++
	win.id = h.lastID
	h.windows = append(h.windows, win)
	h.open++

	h.kit.ContextSetWindowHost(ctx, win)

	return win.id
}

// create makes the X window. A request that fails only reports later, so this one is
// checked, as a failure must come back as a failed WindowNew.
func (win *window) create(title string) bool {
	h := win.host
	c := h.c

	xid, err := xproto.NewWindowId(c)
	if err != nil {
		return false
	}

	gc, err := xproto.NewGcontextId(c)
	if err != nil {
		return false
	}

	const eventMask = xproto.EventMaskExposure | xproto.EventMaskStructureNotify |
		xproto.EventMaskPointerMotion | xproto.EventMaskButtonPress | xproto.EventMaskButtonRelease |
		xproto.EventMaskKeyPress | xproto.EventMaskKeyRelease | xproto.EventMaskLeaveWindow

	err = xproto.CreateWindowChecked(c, rasterDepth, xid, h.root, 0, 0,
		uint16(win.w), uint16(win.h), 0, //nolint:gosec // clampSize bounds them
		xproto.WindowClassInputOutput, h.visual,
		xproto.CwBackPixel|xproto.CwEventMask, []uint32{0, eventMask}).Check()
	if err != nil {
		return false
	}

	win.xid, win.gc = xid, gc

	xproto.CreateGC(c, gc, xproto.Drawable(xid), 0, nil)
	win.setProperty(h.atoms.protocols, xproto.AtomAtom, 32, atomBytes(h.atoms.delete))
	win.setProperty(h.atoms.netWMName, h.atoms.utf8, 8, []byte(title))
	win.setProperty(xproto.AtomWmName, xproto.AtomString, 8, []byte(title))

	return true
}

// setProperty replaces a property of this window.
func (win *window) setProperty(prop, typ xproto.Atom, format byte, data []byte) {
	xproto.ChangeProperty(win.host.c, xproto.PropModeReplace, win.xid, prop, typ, format,
		uint32(len(data))/uint32(format/8), data) //nolint:gosec // data is far below 4 GiB
}

func atomBytes(atoms ...xproto.Atom) []byte {
	b := make([]byte, 4*len(atoms))
	for i, a := range atoms {
		xgb.Put32(b[4*i:], uint32(a))
	}

	return b
}

// find returns the window with an engine id.
func (h *Host) find(id engine.Window) *window {
	for _, win := range h.windows {
		if win.id == id {
			return win
		}
	}

	return nil
}

// findX returns the open window with an X id.
func (h *Host) findX(xid xproto.Window) *window {
	for _, win := range h.windows {
		if win.xid == xid && xid != 0 {
			return win
		}
	}

	return nil
}

// WindowShow implements engine.Host.
func (h *Host) WindowShow(id engine.Window) {
	if win := h.find(id); win != nil && win.xid != 0 {
		xproto.MapWindow(h.c, win.xid)

		win.renderPending = true
	}
}

// WindowFree implements engine.Host.
func (h *Host) WindowFree(id engine.Window) {
	win := h.find(id)
	if win == nil {
		return
	}

	win.close()
	h.kit.ContextSetEmitHost(win.ctx)
	h.kit.RasterFree(win.raster)

	for i, other := range h.windows {
		if other == win {
			h.windows = append(h.windows[:i], h.windows[i+1:]...)

			break
		}
	}
}

// close destroys the X window, which can also come from the window manager's close
// button. The window stays until WindowFree, as its context does.
func (win *window) close() {
	if win.xid == 0 {
		return
	}

	xproto.FreeGC(win.host.c, win.gc)
	xproto.DestroyWindow(win.host.c, win.xid)
	win.forget()
}

// forget closes a window whose X window is already gone, as xkill leaves it.
func (win *window) forget() {
	win.xid = 0
	win.host.open--
}

// render draws what changed into the raster and presents it.
func (win *window) render() {
	win.renderPending = false

	resized := win.rasterW != win.w || win.rasterH != win.h
	full := win.full || resized
	win.full = false

	regions := win.host.kit.RenderDamage(win.ctx, win.raster, win.w, win.h, full)
	win.rasterW, win.rasterH = win.w, win.h

	if regions > 0 || full {
		win.present(full)
	}

	if win.host.kit.Animating(win.ctx) {
		win.renderPending = true
	}
}

// present sends the raster, or just what the last render damaged. The damage comes
// first, as the view of the pixels is only good until the next kit call.
func (win *window) present(all bool) {
	damage := []engine.Rect{{X: 0, Y: 0, W: win.rasterW, H: win.rasterH}}
	if !all {
		damage = win.host.kit.RasterDamage(win.raster)
	}

	pixels := win.host.kit.RasterView(win.raster)

	for _, d := range damage {
		win.host.putRect(win, pixels, d)
	}
}

// putRect sends a rectangle in as many requests as the server's limit asks for.
func (h *Host) putRect(win *window, pixels []byte, d engine.Rect) {
	const pixelSize = 4

	stride := pixelSize * win.rasterW
	row := pixelSize * d.W

	if row <= 0 || d.H <= 0 || row > h.maxData {
		return
	}

	rowsPerRequest := h.maxData / row

	for y := d.Y; y < d.Y+d.H; y += rowsPerRequest {
		rows := min(rowsPerRequest, d.Y+d.H-y)

		var data []byte

		if d.W == win.rasterW {
			data = pixels[y*stride : (y+rows)*stride]
		} else {
			// xgb copies this into its request right away, so one buffer serves them all.
			data = h.putBuf[:0]

			for r := y; r < y+rows; r++ {
				start := r*stride + d.X*pixelSize
				data = append(data, pixels[start:start+row]...)
			}

			h.putBuf = data
		}

		xproto.PutImage(h.c, xproto.ImageFormatZPixmap, xproto.Drawable(win.xid), win.gc,
			uint16(d.W), uint16(rows), int16(d.X), int16(y), //nolint:gosec // bounded by the window's size
			0, rasterDepth, data)
	}
}

// Request implements engine.WindowHost.
func (win *window) Request() {
	if win.xid != 0 {
		win.renderPending = true
	}
}

// SetCursor implements engine.WindowHost.
func (win *window) SetCursor(cursor int) {
	if win.xid != 0 {
		xproto.ChangeWindowAttributes(win.host.c, win.xid, xproto.CwCursor, []uint32{uint32(win.host.cursor(cursor))})
	}
}

// cursor returns the X cursor for a BK_CURSOR_* value, creating it at first use.
func (h *Host) cursor(cursor int) xproto.Cursor {
	cursor = min(max(cursor, 0), cursorCount-1)
	if h.cursors[cursor] != 0 {
		return h.cursors[cursor]
	}

	id, err := xproto.NewCursorId(h.c)
	if err != nil {
		return 0
	}

	if cursor == cursorNone {
		h.createEmptyCursor(id)
	} else {
		h.createFontCursor(id, cursorGlyphs[cursor])
	}

	h.cursors[cursor] = id

	return id
}

// createFontCursor makes a black cursor with a white outline, as XCreateFontCursor does:
// the mask is the next glyph of the font.
func (h *Host) createFontCursor(id xproto.Cursor, glyph uint16) {
	const maxColor = 0xffff

	if h.cursorFont == 0 {
		font, err := xproto.NewFontId(h.c)
		if err != nil {
			return
		}

		const name = "cursor"

		xproto.OpenFont(h.c, font, uint16(len(name)), name)
		h.cursorFont = font
	}

	xproto.CreateGlyphCursor(h.c, id, h.cursorFont, h.cursorFont, glyph, glyph+1,
		0, 0, 0, maxColor, maxColor, maxColor)
}

// createEmptyCursor makes a cursor from a 1 x 1 bitmap of zeros. A new pixmap's content
// is undefined, so it has to be cleared with a fill.
func (h *Host) createEmptyCursor(id xproto.Cursor) {
	pixmap, err := xproto.NewPixmapId(h.c)
	if err != nil {
		return
	}

	gc, err := xproto.NewGcontextId(h.c)
	if err != nil {
		return
	}

	xproto.CreatePixmap(h.c, 1, pixmap, xproto.Drawable(h.root), 1, 1)
	xproto.CreateGC(h.c, gc, xproto.Drawable(pixmap), xproto.GcForeground, []uint32{0})
	xproto.PolyFillRectangle(h.c, xproto.Drawable(pixmap), gc, []xproto.Rectangle{{X: 0, Y: 0, Width: 1, Height: 1}})
	xproto.CreateCursor(h.c, id, pixmap, pixmap, 0, 0, 0, 0, 0, 0, 0, 0)
	xproto.FreeGC(h.c, gc)
	xproto.FreePixmap(h.c, pixmap)
}

// Warp implements engine.WindowHost.
func (win *window) Warp(x, y int) {
	if win.xid != 0 {
		xproto.WarpPointer(win.host.c, 0, win.xid, 0, 0, 0, 0, int16(x), int16(y)) //nolint:gosec // window coordinates
	}
}

// Schedule implements engine.WindowHost.
func (win *window) Schedule(ms, timer int) {
	due := time.Now().Add(time.Duration(ms) * time.Millisecond)
	if timer == timerCustom {
		win.customDue = due

		return
	}

	win.tipID, win.tipDue = timer, due
}

// runTimers fires what is due. A timer is cleared first, as firing can schedule it again.
func (win *window) runTimers(now time.Time) {
	if !win.tipDue.IsZero() && !win.tipDue.After(now) {
		win.tipDue = time.Time{}
		win.host.kit.Timer(win.ctx, win.tipID)
	}

	if win.xid != 0 && !win.customDue.IsZero() && !win.customDue.After(now) {
		win.customDue = time.Time{}
		win.host.kit.Timer(win.ctx, timerCustom)
	}
}
