// SPDX-License-Identifier: GPL-2.0-or-later

package cocoa

import (
	"os"
	"strconv"
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

	// NSPasteboardTypeString.
	pasteboardString = "public.utf8-plain-text"
)

// The kit's BK_CURSOR_* values with a shape here. BK_CURSOR_DEFAULT and the rest are the arrow.
const (
	cursorEW         = 1
	cursorNS         = 2
	cursorText       = 3
	cursorCrosshair  = 4
	cursorHand       = 5
	cursorMove       = 6
	cursorNone       = 7
	cursorNotAllowed = 10
)

var (
	selActivate            = objc.RegisterName("activateIgnoringOtherApps:")
	selMakeKeyOrderFront   = objc.RegisterName("makeKeyAndOrderFront:")
	selSetMouseMoved       = objc.RegisterName("setAcceptsMouseMovedEvents:")
	selMouseOutside        = objc.RegisterName("mouseLocationOutsideOfEventStream")
	selConvertRectToScreen = objc.RegisterName("convertRectToScreen:")
	selScreens             = objc.RegisterName("screens")
	selFirstObject         = objc.RegisterName("firstObject")
	selFrame               = objc.RegisterName("frame")
	selProcessInfo         = objc.RegisterName("processInfo")
	selSystemUptime        = objc.RegisterName("systemUptime")
	selSet                 = objc.RegisterName("set")
	selHide                = objc.RegisterName("hide")
	selUnhide              = objc.RegisterName("unhide")
	selArrowCursor         = objc.RegisterName("arrowCursor")
	selResizeEW            = objc.RegisterName("resizeLeftRightCursor")
	selResizeNS            = objc.RegisterName("resizeUpDownCursor")
	selIBeam               = objc.RegisterName("IBeamCursor")
	selCrosshair           = objc.RegisterName("crosshairCursor")
	selPointingHand        = objc.RegisterName("pointingHandCursor")
	selOpenHand            = objc.RegisterName("openHandCursor")
	selNotAllowed          = objc.RegisterName("operationNotAllowedCursor")
	selGeneralPasteboard   = objc.RegisterName("generalPasteboard")
	selStringForType       = objc.RegisterName("stringForType:")
	selDeclareTypes        = objc.RegisterName("declareTypes:owner:")
	selSetString           = objc.RegisterName("setString:forType:")
	selArrayWithObject     = objc.RegisterName("arrayWithObject:")
	selInLiveResize        = objc.RegisterName("inLiveResize")
)

// window is one NSWindow on a kit context. It implements engine.WindowHost.
type window struct {
	host  *Host
	id    engine.Window
	nswin objc.ID // 0 once closed
	owned objc.ID // the same window, which this holds a reference to until WindowFree
	view  objc.ID
	layer objc.ID // held by the view
	// delegate is held here until WindowFree, as a window doesn't retain its delegate.
	delegate objc.ID
	ctx      engine.Context
	raster   engine.Raster

	px            float64 // pixels per point
	scale         float64 // the kit's scale for this window, which kit selects
	w, h          int     // the window's size in pixels, which is the raster's next size
	rasterW       int
	rasterH       int
	renderPending bool
	full          bool

	cursor           int
	flags            uint64  // the modifiers at the last flagsChanged
	scrollX, scrollY float64 // trackpad scroll not yet sent, in points
	shown            bool

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
		guarded(func() {
			if win := current.findNS(note.Send(selObject)); win != nil {
				win.forget()
			}
		})
	}
	onResize := func(_ objc.ID, _ objc.SEL, note objc.ID) {
		guarded(func() {
			if win := current.findNS(note.Send(selObject)); win != nil {
				win.resized()
			}
		})
	}

	onBacking := func(_ objc.ID, _ objc.SEL, note objc.ID) {
		guarded(func() {
			if win := current.findNS(note.Send(selObject)); win != nil {
				win.backingChanged()
			}
		})
	}

	// Modifiers released while another application was in front sent no flagsChanged here.
	onBecomeKey := func(_ objc.ID, _ objc.SEL, note objc.ID) {
		guarded(func() {
			if win := current.findNS(note.Send(selObject)); win != nil {
				win.flags = objc.Send[uint64](clsNSEvent, selModifierFlags)
			}
		})
	}

	cls, err := objc.RegisterClass("BUIGoWindowDelegate", objc.GetClass("NSObject"), nil, nil, []objc.MethodDef{
		{Cmd: objc.RegisterName("windowWillClose:"), Fn: onClose},
		{Cmd: objc.RegisterName("windowDidResize:"), Fn: onResize},
		{Cmd: objc.RegisterName("windowDidChangeBackingProperties:"), Fn: onBacking},
		{Cmd: objc.RegisterName("windowDidBecomeKey:"), Fn: onBecomeKey},
	})
	if err != nil {
		return false
	}

	delegateClass = cls

	return true
}

// WindowNew implements engine.Host. As in the C host, a logical size w x h takes
// w*scale/px x h*scale/px points, so the kit sees w*scale x h*scale pixels and draws its
// widgets scale times larger. Without BUI_UI_SCALE, scale is px, so w x h takes w x h points.
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

	win := &window{host: h, ctx: ctx, px: px}
	win.setScale()

	k := win.scale / px
	win.create(title, max(float64(width)*k, 1), max(float64(height)*k, 1))

	// It opened on another screen than the main one.
	if win.px != px {
		win.setScale()
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

// create makes the NSWindow, with a content view of width x height points.
func (win *window) create(title string, width, height float64) {
	rect := nsRect{Size: nsSize{width, height}}

	ns := clsNSWindow.Send(selAlloc).Send(selInitWindow, rect,
		uint64(styleTitled|styleClosable|styleMiniaturize|styleResizable), uint64(backingBuffered), false)
	ns.Send(selSetReleased, false)
	ns.Send(selSetTitle, nsString(title))
	delegate := objc.ID(delegateClass).Send(selAlloc).Send(selInit)
	ns.Send(selSetDelegate, delegate)

	// A layer-hosting view: the host owns the layer, and its contents are the frame.
	view := objc.ID(viewClass).Send(selAlloc).Send(selInitFrame, rect)
	layer := clsCALayer.Send(selAlloc).Send(selInit)
	layer.Send(selSetContentsScale, win.pxOf(ns))
	layer.Send(selSetContentsGrav, nsString("topLeft"))
	view.Send(selSetLayer, layer)
	view.Send(selSetWantsLayer, true)
	layer.Send(selRelease)
	ns.Send(selSetContentView, view)
	ns.Send(selMakeFirstResp, view)
	ns.Send(selSetMouseMoved, true)
	addTracking(view)
	view.Send(selRelease)
	ns.Send(selCenter)

	win.nswin, win.owned, win.view, win.layer, win.delegate = ns, ns, view, layer, delegate
	win.flags = objc.Send[uint64](clsNSEvent, selModifierFlags)
	win.syncSize()
}

func (win *window) pxOf(ns objc.ID) float64 {
	if px := win.host.backing(ns); px > 0 {
		win.px = px
	}

	return win.px
}

// setScale gives the kit this window's scale, which follows its pixels per point.
func (win *window) setScale() {
	win.scale = uiScale(win.px)
	win.kit()
}

// kit returns the kit with this window's scale selected. The scale is process-wide, as
// BK_set_scale's is, so windows on screens of different scale would otherwise draw and
// measure at the scale of whichever window changed last, as they do in the C host. Each call
// for a window that may draw or measure goes through here.
func (win *window) kit() engine.Kit {
	win.host.kit.SetScale(float32(win.scale))

	return win.host.kit
}

// uiScale is BUI_UI_SCALE, else the backing scale and at least 1, as in the C host.
func uiScale(px float64) float64 {
	if env := envScale(); env > 0 {
		return env
	}

	return max(1, px)
}

// envScale is BUI_UI_SCALE, 0 if it isn't set or doesn't parse to more than 0. The C host
// takes atof's reading, which accepts a number followed by anything.
func envScale() float64 {
	v, err := strconv.ParseFloat(os.Getenv("BUI_UI_SCALE"), 64)
	if err != nil || v <= 0 {
		return 0
	}

	return v
}

// backingChanged follows a move to a screen of another backing scale, as the C host's
// on_backing_changed does: the layer takes the new pixels per point, and unless
// BUI_UI_SCALE fixes it, the kit takes the new scale and measures everything again.
//
// The relayout is the C host's. The raster's size changes with the pixels per point, and
// the kit measures everything again on any new size, so today it changes nothing a test
// can see. It keeps the layout right should the kit stop doing that.
func (win *window) backingChanged() {
	if px := win.host.backing(win.nswin); px > 0 && px != win.px {
		win.px = px
		win.layer.Send(selSetContentsScale, px)

		old := win.scale
		win.setScale()

		if win.scale != old {
			kit := win.kit()
			kit.NodeRelayout(kit.ContextRoot(win.ctx))
		}
	}

	win.full, win.renderPending = true, true
	win.resized()
}

// resized reads the new size. Dragging an edge runs AppKit's own loop, which doesn't return
// to the host's until the button goes up, so the window draws its new size here, as in the C
// host.
func (win *window) resized() {
	win.syncSize()

	if win.renderPending && objc.Send[bool](win.view, selInLiveResize) {
		win.render()
	}
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

// WindowShow implements engine.Host. A program started from a terminal isn't in front, so
// the first show activates it. Without that, typing would go nowhere until the first click.
func (h *Host) WindowShow(id engine.Window) {
	if win := h.find(id); win != nil && win.nswin != 0 {
		// Ordering a window front autoreleases references to it, and nothing else would
		// drain them outside the loop: the window would outlive WindowFree.
		pool := poolPush()

		if win.host.noActivate {
			win.nswin.Send(selOrderFront)
		} else {
			if !win.shown {
				win.shown = true

				h.app.Send(selActivate, true)
			}

			win.nswin.Send(selMakeKeyOrderFront, objc.ID(0))
		}

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

	win.owned.Send(selSetDelegate, objc.ID(0))

	// AppKit may be inside this window's methods, or its delegate's, if it is freed from one
	// of its events.
	if h.depth > 0 {
		h.dead = append(h.dead, win.owned, win.delegate)
	} else {
		win.owned.Send(selRelease)
		win.delegate.Send(selRelease)
	}

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
		if win.cursor == cursorNone {
			win.host.showCursor(true)
		}

		win.nswin = 0
		win.host.open--
	}
}

func (win *window) render() {
	win.renderPending = false

	resized := win.rasterW != win.w || win.rasterH != win.h
	full := win.full || resized
	win.full = false

	regions := win.kit().RenderDamage(win.ctx, win.raster, win.w, win.h, full)
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

// SetCursor implements engine.WindowHost. Like a window's cursor elsewhere, it only shows
// while the pointer is over the view or dragging from it, and cursorUpdate: applies it on entry.
func (win *window) SetCursor(cursor int) {
	win.cursor = cursor

	if win.nswin == 0 {
		return
	}

	pool := poolPush()
	defer poolPop(pool)

	if win.pointerInside() || win.host.buttons() != 0 {
		win.applyCursor()
	}
}

func (win *window) pointerInside() bool {
	p := objc.Send[nsPoint](win.view, selConvertFromView, objc.Send[nsPoint](win.nswin, selMouseOutside), objc.ID(0))
	b := objc.Send[nsRect](win.view, selBounds)

	return p.X >= 0 && p.Y >= 0 && p.X < b.Size.W && p.Y < b.Size.H
}

func (win *window) applyCursor() {
	win.host.showCursor(win.cursor != cursorNone)

	if win.cursor != cursorNone {
		clsNSCursor.Send(cursorSelector(win.cursor)).Send(selSet)
	}
}

// showCursor keeps NSCursor's hide and unhide paired, as they are counted.
func (h *Host) showCursor(visible bool) {
	switch {
	case visible && h.cursorHidden:
		clsNSCursor.Send(selUnhide)
	case !visible && !h.cursorHidden:
		clsNSCursor.Send(selHide)
	default:
		return
	}

	h.cursorHidden = !visible
}

// cursorSelector picks NSCursor's class method for a BK_CURSOR_* value. AppKit has no public
// cursor for the two diagonal resizes or for waiting, so those show the arrow.
func cursorSelector(cursor int) objc.SEL {
	switch cursor {
	case cursorEW:
		return selResizeEW
	case cursorNS:
		return selResizeNS
	case cursorText:
		return selIBeam
	case cursorCrosshair:
		return selCrosshair
	case cursorHand:
		return selPointingHand
	case cursorMove:
		return selOpenHand
	case cursorNotAllowed:
		return selNotAllowed
	}

	return selArrowCursor
}

// warpTarget converts window pixels to Quartz's global coordinates, which start at the top
// left of the primary screen.
func (win *window) warpTarget(x, y int) nsPoint {
	b := objc.Send[nsRect](win.view, selBounds)
	inView := nsPoint{float64(x) / win.px, b.Size.H - float64(y)/win.px}
	inWindow := objc.Send[nsPoint](win.view, selConvertToView, inView, objc.ID(0))
	onScreen := objc.Send[nsRect](win.nswin, selConvertRectToScreen, nsRect{Origin: inWindow})

	return nsPoint{onScreen.Origin.X, primaryHeight() - onScreen.Origin.Y}
}

func primaryHeight() float64 {
	primary := clsNSScreen.Send(selScreens).Send(selFirstObject)
	if primary == 0 {
		return 0
	}

	f := objc.Send[nsRect](primary, selFrame)

	return f.Origin.Y + f.Size.H
}

// Warp implements engine.WindowHost. The association drops the 500 ms of mouse events that
// Quartz suppresses after a synthetic move.
func (win *window) Warp(x, y int) {
	if win.nswin == 0 {
		return
	}

	pool := poolPush()
	defer poolPop(pool)

	cgWarp(win.warpTarget(x, y))
	cgAssociateMouse(true)

	win.host.warpTime = objc.Send[float64](clsNSProcessInfo.Send(selProcessInfo), selSystemUptime)
}

// ClipboardGet implements engine.WindowHost.
func (win *window) ClipboardGet() (string, bool) {
	if win.nswin == 0 {
		return "", false
	}

	pool := poolPush()
	defer poolPop(pool)

	s := win.host.pasteboard().Send(selStringForType, nsString(pasteboardString))
	if s == 0 {
		return "", false
	}

	return goString(s), true
}

// ClipboardSet implements engine.WindowHost. Text that isn't UTF-8 has no NSString, and is
// dropped.
func (win *window) ClipboardSet(text string) {
	if win.nswin == 0 {
		return
	}

	pool := poolPush()
	defer poolPop(pool)

	s := nsString(text)
	if s == 0 {
		return
	}

	kind := nsString(pasteboardString)
	pb := win.host.pasteboard()
	pb.Send(selDeclareTypes, clsNSArray.Send(selArrayWithObject, kind), objc.ID(0))
	pb.Send(selSetString, s, kind)
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

func (win *window) runTimers(now time.Time) {
	if !win.tipDue.IsZero() && !win.tipDue.After(now) {
		win.tipDue = time.Time{}
		win.kit().Timer(win.ctx, win.tipID)
	}

	if win.nswin != 0 && !win.customDue.IsZero() && !win.customDue.After(now) {
		win.customDue = time.Time{}
		win.kit().Timer(win.ctx, timerCustom)
	}
}
