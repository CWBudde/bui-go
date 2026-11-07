// SPDX-License-Identifier: GPL-2.0-or-later

package cocoa

import (
	"time"
	"unsafe"

	"github.com/CWBudde/bui-go/internal/engine"
	"github.com/ebitengine/purego/objc"
)

const (
	styleTitled      = 1 << 0
	styleClosable    = 1 << 1
	styleMiniaturize = 1 << 2
	styleResizable   = 1 << 3
	backingBuffered  = 2

	// The raster's words are 0xXXRRGGBB, little endian: kCGImageAlphaNoneSkipFirst and
	// kCGBitmapByteOrder32Little, as in the C host.
	bitmapInfo = 6 | 2<<12

	pixelSize   = 4
	timerCustom = -1
)

// window is one NSWindow on a kit context. It implements engine.WindowHost.
type window struct {
	host   *Host
	id     engine.Window
	nswin  objc.ID // 0 once closed
	owned  objc.ID // the same window, which this holds a reference to until WindowFree
	layer  objc.ID
	ctx    engine.Context
	raster engine.Raster

	px            float64 // pixels per point
	w, h          int     // the window's size in pixels, which is the raster's next size
	rasterW       int
	rasterH       int
	renderPending bool
	full          bool

	tipID             int
	tipDue, customDue time.Time
}

var _ engine.WindowHost = (*window)(nil)

// windowsByObject finds a window for the delegate's callbacks, which get only the NSWindow.
func (h *Host) findNS(ns objc.ID) *window {
	for _, win := range h.windows {
		if win.nswin == ns && ns != 0 {
			return win
		}
	}

	return nil
}

// current is the host the delegate reports to. A process has one AppKit, so one host.
var current *Host

func registerDelegate() bool {
	onClose := func(_ objc.ID, _ objc.SEL, note objc.ID) {
		if win := current.findNS(note.Send(selObject)); win != nil {
			win.forget()
		}
	}
	onResize := func(_ objc.ID, _ objc.SEL, note objc.ID) {
		if win := current.findNS(note.Send(selObject)); win != nil {
			win.syncSize()
		}
	}

	cls, err := objc.RegisterClass("BUIGoWindowDelegate", objc.GetClass("NSObject"), nil, nil, []objc.MethodDef{
		{Cmd: objc.RegisterName("windowWillClose:"), Fn: onClose},
		{Cmd: objc.RegisterName("windowDidResize:"), Fn: onResize},
	})
	if err != nil {
		return false
	}

	delegateClass = cls

	return true
}

// WindowNew implements engine.Host. A logical size w x h takes w x h points, so the kit
// sees w*px x h*px pixels and draws its widgets px times larger, as in the C host.
func (h *Host) WindowNew(ctx engine.Context, title string, width, height int) engine.Window {
	if !h.Supported() {
		return 0
	}

	current = h

	pool := poolPush()
	defer poolPop(pool)

	h.start()

	px := objc.Send[float64](clsNSScreen.Send(selMainScreen), selBackingScale)
	if px <= 0 {
		px = 1
	}

	h.kit.SetScale(float32(px))

	win := &window{host: h, ctx: ctx, px: px}
	win.create(title, max(width, 1), max(height, 1))

	win.raster = h.kit.RasterNew(1, 1)
	win.rasterW, win.rasterH = 1, 1

	h.lastID++
	win.id = h.lastID
	h.windows = append(h.windows, win)
	h.open++

	h.kit.ContextSetWindowHost(ctx, win)

	return win.id
}

func (win *window) create(title string, width, height int) {
	rect := nsRect{Size: nsSize{float64(width), float64(height)}}

	ns := clsNSWindow.Send(selAlloc).Send(selInitWindow, rect,
		uint64(styleTitled|styleClosable|styleMiniaturize|styleResizable), uint64(backingBuffered), false)
	ns.Send(selSetReleased, false)
	ns.Send(selSetTitle, nsString(title))
	ns.Send(selSetDelegate, objc.ID(delegateClass).Send(selAlloc).Send(selInit))

	// A layer-hosting view: the host owns the layer, and its contents are the frame.
	view := clsNSView.Send(selAlloc).Send(selInitFrame, rect)
	layer := clsCALayer.Send(selAlloc).Send(selInit)
	layer.Send(selSetContentsScale, win.pxOf(ns))
	layer.Send(selSetContentsGrav, nsString("topLeft"))
	view.Send(selSetLayer, layer)
	view.Send(selSetWantsLayer, true)
	ns.Send(selSetContentView, view)
	view.Send(selRelease)
	ns.Send(selCenter)

	win.nswin, win.owned, win.layer = ns, ns, layer
	win.syncSize()
}

func (win *window) pxOf(ns objc.ID) float64 {
	if px := objc.Send[float64](ns, selBackingScale); px > 0 {
		win.px = px
	}

	return win.px
}

// syncSize reads the content view's size, in points, as the next raster's size in pixels.
func (win *window) syncSize() {
	b := objc.Send[nsRect](win.nswin.Send(selContentView), selBounds)

	w, h := int(b.Size.W*win.px), int(b.Size.H*win.px)
	if w != win.w || h != win.h {
		win.w, win.h = w, h
		win.renderPending = true
	}
}

func (h *Host) find(id engine.Window) *window {
	for _, win := range h.windows {
		if win.id == id {
			return win
		}
	}

	return nil
}

// WindowShow implements engine.Host. It doesn't activate the application yet, so that the
// spike's tests leave the user's focus alone. The port has to, as the C host does.
func (h *Host) WindowShow(id engine.Window) {
	if win := h.find(id); win != nil && win.nswin != 0 {
		// Ordering a window front autoreleases references to it, and nothing else would
		// drain them outside the loop: the window would outlive WindowFree.
		pool := poolPush()

		win.nswin.Send(selOrderFront)
		poolPop(pool)

		win.full, win.renderPending = true, true
	}
}

// WindowFree implements engine.Host.
func (h *Host) WindowFree(id engine.Window) {
	win := h.find(id)
	if win == nil {
		return
	}

	pool := poolPush()

	if win.nswin != 0 {
		win.nswin.Send(selClose)
	}

	win.owned.Send(selRelease)
	poolPop(pool)

	h.kit.ContextSetEmitHost(win.ctx)
	h.kit.RasterFree(win.raster)

	for i, other := range h.windows {
		if other == win {
			h.windows = append(h.windows[:i], h.windows[i+1:]...)

			break
		}
	}
}

func (win *window) forget() {
	if win.nswin != 0 {
		win.nswin = 0
		win.host.open--
	}
}

func (win *window) render() {
	win.renderPending = false

	resized := win.rasterW != win.w || win.rasterH != win.h
	full := win.full || resized
	win.full = false

	regions := win.host.kit.RenderDamage(win.ctx, win.raster, win.w, win.h, full)
	win.rasterW, win.rasterH = win.w, win.h

	if regions > 0 || full {
		win.present()
	}

	if win.host.kit.Animating(win.ctx) {
		win.renderPending = true
	}
}

// present makes the raster the layer's contents. CFDataCreate copies, as the view of the
// pixels is only good until the next kit call and Core Animation reads them later.
func (win *window) present() {
	pixels := win.host.kit.RasterView(win.raster)
	if len(pixels) == 0 {
		return
	}

	data := cfDataCreate(0, unsafe.SliceData(pixels), len(pixels))
	provider := cgDataProviderFromData(data)
	cfRelease(data)

	img := cgImageCreate(win.rasterW, win.rasterH, 8, 8*pixelSize, win.rasterW*pixelSize, srgbSpace, bitmapInfo,
		provider, 0, false, 0)
	cgDataProviderRelease(provider)

	if img == 0 {
		return
	}

	tx := clsCATransaction
	tx.Send(selBegin)
	tx.Send(selSetDisableActions, true)
	win.layer.Send(selSetContents, objc.ID(img))
	tx.Send(selCommit)
	tx.Send(selFlush)

	cgImageRelease(img)
}

// Request implements engine.WindowHost.
func (win *window) Request() {
	if win.nswin != 0 {
		win.renderPending = true
	}
}

// SetCursor implements engine.WindowHost. Not in the spike.
func (win *window) SetCursor(int) {}

// Warp implements engine.WindowHost. Not in the spike.
func (win *window) Warp(int, int) {}

// ClipboardGet implements engine.WindowHost. Not in the spike.
func (win *window) ClipboardGet() (string, bool) { return "", false }

// ClipboardSet implements engine.WindowHost. Not in the spike.
func (win *window) ClipboardSet(string) {}

// Schedule implements engine.WindowHost.
func (win *window) Schedule(ms, timer int) {
	due := time.Now().Add(time.Duration(ms) * time.Millisecond)
	if timer == timerCustom {
		win.customDue = due

		return
	}

	win.tipID, win.tipDue = timer, due
}

func (win *window) runTimers(now time.Time) {
	if !win.tipDue.IsZero() && !win.tipDue.After(now) {
		win.tipDue = time.Time{}
		win.host.kit.Timer(win.ctx, win.tipID)
	}

	if win.nswin != 0 && !win.customDue.IsZero() && !win.customDue.After(now) {
		win.customDue = time.Time{}
		win.host.kit.Timer(win.ctx, timerCustom)
	}
}
