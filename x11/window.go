// SPDX-License-Identifier: GPL-2.0-or-later

package x11

import (
	"image"
	"slices"
	"time"

	bui "github.com/CWBudde/bui-go"
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
	timerCustom bui.Timer = -1
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

// window is one X window showing a bui.Headless. It is the Headless's bui.Host, so the
// kit reaches it through the Headless.
type window struct {
	host *host
	hl   *bui.Headless
	xid  xproto.Window // 0 once closed
	gc   xproto.Gcontext

	w, h          int // the window's size, which is the raster's next size
	rasterW       int
	rasterH       int
	renderPending bool
	full          bool
	// damage is what the last render redrew.
	damage []image.Rectangle
	// freed says Window.Close ran, which closes the X window and the Headless.
	freed bool

	tipID             bui.Timer
	tipDue, customDue time.Time

	// late holds this window's pastes that timed out with no answer seen since, oldest
	// first.
	late []lateRequest
}

var _ bui.Host = (*window)(nil)

// clampSize makes a logical size a window size: X has neither empty nor huge windows.
func clampSize(v int, scale float32) int {
	return min(max(int(float32(v)*scale), 1), maxWindowSize)
}

// newWindow is NewWindow. The Headless comes first, so that its checks of root hold with
// or without a display, as in bui.NewWindow.
func (h *host) newWindow(root *bui.Node, title string, width, height int) (*window, error) {
	hl := bui.NewHeadless(root, 1, 1)

	if !h.Supported() {
		hl.Close()

		return nil, bui.ErrNoDisplay
	}

	win := &window{
		host:    h,
		hl:      hl,
		w:       clampSize(width, h.scale),
		h:       clampSize(height, h.scale),
		rasterW: 1,
		rasterH: 1,
	}

	if !win.create(title) {
		hl.Close()

		return nil, bui.ErrNoDisplay
	}

	hl.SetScale(float64(h.scale))
	hl.SetHost(win)

	h.windows = append(h.windows, win)
	h.open++

	return win, nil
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
		xproto.EventMaskKeyPress | xproto.EventMaskLeaveWindow

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

// setProperty replaces a property of this window. What does not fit one request goes out
// as a replace and appends, as xgb truncates a request's length field rather than failing.
// maxData is a multiple of 4, so every part holds whole items of any format.
func (win *window) setProperty(prop, typ xproto.Atom, format byte, data []byte) {
	h := win.host
	mode := byte(xproto.PropModeReplace)

	for {
		part := data[:min(len(data), h.maxData)]
		xproto.ChangeProperty(h.c, mode, win.xid, prop, typ, format,
			uint32(len(part))/uint32(format/8), part) //nolint:gosec // part fits one request

		data = data[len(part):]
		if len(data) == 0 {
			return
		}

		mode = xproto.PropModeAppend
	}
}

func atomBytes(atoms ...xproto.Atom) []byte {
	b := make([]byte, 4*len(atoms))
	for i, a := range atoms {
		xgb.Put32(b[4*i:], uint32(a))
	}

	return b
}

// findX returns the open window with an X id.
func (h *host) findX(xid xproto.Window) *window {
	for _, win := range h.windows {
		if win.xid == xid && xid != 0 {
			return win
		}
	}

	return nil
}

// show maps the window.
func (win *window) show() {
	if win.xid != 0 {
		xproto.MapWindow(win.host.c, win.xid)

		win.renderPending = true
	}
}

// free is Window.Close: the X window, then the Headless, which waits if a handler is
// running.
func (win *window) free() {
	h := win.host

	win.close()
	win.freed = true
	win.hl.Close()

	h.windows = slices.DeleteFunc(h.windows, func(other *window) bool { return other == win })
}

// close destroys the X window, which can also come from the window manager's close
// button. The Headless stays until Window.Close.
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

// render draws what changed into the raster and presents it. Should a handler run during
// the render and close the window, the Headless is gone, so the window checks first.
func (win *window) render() {
	win.renderPending = false

	resized := win.rasterW != win.w || win.rasterH != win.h
	full := win.full || resized
	win.full = false

	win.hl.Resize(win.w, win.h)
	win.damage = win.hl.RenderDamage(full)
	win.rasterW, win.rasterH = win.w, win.h

	if win.xid == 0 {
		return
	}

	if len(win.damage) > 0 || full {
		win.present(full)
	}

	if win.hl.Animating() {
		win.renderPending = true
	}
}

// present sends the raster, or just what the last render damaged.
func (win *window) present(all bool) {
	damage := win.damage
	if all {
		damage = []image.Rectangle{image.Rect(0, 0, win.rasterW, win.rasterH)}
	}

	for _, d := range damage {
		win.host.putRect(win, d)
	}
}

// putRect sends a rectangle in as many requests as the server's limit asks for. A server
// may take requests as short as 16 KiB, so a row that does not fit one goes in columns.
// Each request's pixels are copied once, into a buffer that serves them all, as xgb
// copies them into its request right away.
func (h *host) putRect(win *window, d image.Rectangle) {
	const pixelSize = 4

	if d.Empty() {
		return
	}

	if cols := h.maxData / pixelSize; d.Dx() > cols {
		for x := d.Min.X; x < d.Max.X; x += cols {
			h.putRect(win, image.Rect(x, d.Min.Y, min(x+cols, d.Max.X), d.Max.Y))
		}

		return
	}

	rowsPerRequest := h.maxData / (pixelSize * d.Dx())

	for y := d.Min.Y; y < d.Max.Y; y += rowsPerRequest {
		rows := min(rowsPerRequest, d.Max.Y-y)
		h.putBuf = win.hl.CopyBGRA(h.putBuf[:0], image.Rect(d.Min.X, y, d.Max.X, y+rows))

		xproto.PutImage(h.c, xproto.ImageFormatZPixmap, xproto.Drawable(win.xid), win.gc,
			uint16(d.Dx()), uint16(rows), int16(d.Min.X), int16(y), //nolint:gosec // bounded by the window's size
			0, rasterDepth, h.putBuf)
	}
}

// Redraw implements bui.Host.
func (win *window) Redraw() {
	if win.xid != 0 {
		win.renderPending = true
	}
}

// SetCursor implements bui.Host.
func (win *window) SetCursor(cursor bui.Cursor) {
	if win.xid != 0 {
		xproto.ChangeWindowAttributes(win.host.c, win.xid, xproto.CwCursor, []uint32{uint32(win.host.cursor(int(cursor)))})
	}
}

// cursor returns the X cursor for a BK_CURSOR_* value, creating it at first use.
func (h *host) cursor(cursor int) xproto.Cursor {
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
func (h *host) createFontCursor(id xproto.Cursor, glyph uint16) {
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
func (h *host) createEmptyCursor(id xproto.Cursor) {
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

// Warp implements bui.Host.
func (win *window) Warp(x, y int) {
	if win.xid != 0 {
		xproto.WarpPointer(win.host.c, 0, win.xid, 0, 0, 0, 0, int16(x), int16(y)) //nolint:gosec // window coordinates
	}
}

// Schedule implements bui.Host.
func (win *window) Schedule(after time.Duration, timer bui.Timer) {
	due := time.Now().Add(after)
	if timer == timerCustom {
		win.customDue = due

		return
	}

	win.tipID, win.tipDue = timer, due
}

// runTimers fires what is due. A timer is cleared first, as firing can schedule it again,
// and the window can be closed by the first, in which case the Headless is gone.
func (win *window) runTimers(now time.Time) {
	if !win.tipDue.IsZero() && !win.tipDue.After(now) {
		win.tipDue = time.Time{}
		win.hl.Timer(win.tipID)
	}

	if win.xid != 0 && !win.customDue.IsZero() && !win.customDue.After(now) {
		win.customDue = time.Time{}
		win.hl.Timer(timerCustom)
	}
}
