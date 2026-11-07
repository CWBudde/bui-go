// SPDX-License-Identifier: GPL-2.0-or-later

package cocoa

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CWBudde/bui-go/internal/engine"
	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

const (
	eventLeftDown  = 1
	eventLeftUp    = 2
	eventRightDown = 3
	eventRightUp   = 4
	eventMoved     = 5
	eventLeftDrag  = 6
	eventOtherDown = 25
	eventOtherUp   = 26
	keyA           = 0x00
	keyBackSpace   = 0x33
	keyReturn      = 0x24
	keyF1          = 0x7A
	keyLeft        = 0x7B
	keyKeypad5     = 0x57
	keyQ           = 0x0C
	scrollUnitPx   = 0
	scrollUnitLn   = 1
	fieldPhase     = 99
	fieldMomentum  = 123
	phaseBegan     = 1
	phaseChanged   = 2
	childEnv       = "BUI_COCOA_TEST_CHILD"
)

var (
	selMouseEvent = objc.RegisterName(
		"mouseEventWithType:location:modifierFlags:timestamp:windowNumber:context:eventNumber:clickCount:pressure:",
	)
	selKeyEvent = objc.RegisterName(
		"keyEventWithType:location:modifierFlags:timestamp:windowNumber:context:characters:" +
			"charactersIgnoringModifiers:isARepeat:keyCode:",
	)
	selWindowNumber  = objc.RegisterName("windowNumber")
	selEventWithCG   = objc.RegisterName("eventWithCGEvent:")
	selCurrentCursor = objc.RegisterName("currentCursor")
	selScrollWheel   = objc.RegisterName("scrollWheel:")
	selOtherDown     = objc.RegisterName("otherMouseDown:")
	selOtherUp       = objc.RegisterName("otherMouseUp:")
	selKeyUp         = objc.RegisterName("keyUp:")
	selBecomeKey     = objc.RegisterName("windowDidBecomeKey:")
	selDelegate      = objc.RegisterName("delegate")
	selNotification  = objc.RegisterName("notificationWithName:object:")
	selMouseExited   = objc.RegisterName("mouseExited:")
	selUnique        = objc.RegisterName("pasteboardWithUniqueName")
)

// inputFixture is one widget filling a window, which tests drive with events they post to
// the queue, the way the window server's reach it. That needs no grant, and the user's
// pointer and focus stay out of it.
type inputFixture struct {
	*fixture

	t     *testing.T
	id    engine.Window
	win   *window
	child engine.Node
}

func openInput(t *testing.T, typ string, width, height int) *inputFixture {
	t.Helper()

	f := newFixtureOf()
	k := f.k

	child := k.NodeNew(typ)
	k.NodeSetID(child, 1)
	k.NodeAdd(f.root, child)
	k.GeomSetPack(child)
	k.GeomSet(child, "fill", 3)
	k.GeomSet(child, "expand", 1)

	id := f.h.WindowNew(f.ctx, "bui cocoa test", width, height)
	win := f.h.find(id)
	f.h.WindowShow(id)
	win.render()

	return &inputFixture{fixture: f, t: t, id: id, win: win, child: child}
}

func (f *inputFixture) close() {
	// Nothing a test does may take the user's focus.
	if objc.Send[bool](f.h.app, objc.RegisterName("isActive")) {
		f.t.Error("the test activated the application")
	}

	f.h.WindowFree(f.id)
	f.k.SetEmit(func(uint32, int, float64) {})
	f.free()
}

func (f *inputFixture) center() (x, y int) {
	cx, cy, cw, ch := f.k.NodeRect(f.child)

	return cx + cw/2, cy + ch/2
}

// windowPoint converts raster pixels to AppKit's window coordinates, which have y up.
func (f *inputFixture) windowPoint(x, y int) nsPoint {
	return nsPoint{float64(x) / f.win.px, (float64(f.win.h) - float64(y)) / f.win.px}
}

// send posts an event and has the loop take it, so that it goes through dispatch and NSApp.
// The wait is for the first event after launching, which a loop that doesn't wait can miss.
func (f *inputFixture) send(ev objc.ID) {
	f.h.app.Send(selPostEvent, ev, false)
	f.h.pump(clsNSDate.Send(selDateSinceNow, float64(1)))
}

// sendToWindow skips the queue and NSApp, which only sends keys to the key window, and an
// application that may not be active has none. The view's own methods still run.
func (f *inputFixture) sendToWindow(ev objc.ID) {
	f.win.nswin.Send(selSendEvent, ev)
}

func (f *inputFixture) now() float64 {
	return objc.Send[float64](clsNSProcessInfo.Send(selProcessInfo), selSystemUptime)
}

func (f *inputFixture) mouse(kind uint64, x, y int, flags uint64, button int64) {
	pool := poolPush()
	defer poolPop(pool)

	ev := clsNSEvent.Send(selMouseEvent, kind, f.windowPoint(x, y), flags, f.now(),
		objc.Send[int64](f.win.nswin, selWindowNumber), objc.ID(0), button, int64(1), float32(1))
	f.send(ev)
}

// keyEvent's last two arguments go on the stack. On arm64 macOS packs them by size (isARepeat
// at 0, keyCode at 2), while purego gives each 8 bytes, so there they share one word. On amd64
// each has a slot of its own.
func (f *inputFixture) keyEvent(kind uint64, code uint16, chars, ignoring string, flags uint64) objc.ID {
	window := objc.Send[int64](f.win.nswin, selWindowNumber)

	if runtime.GOARCH == "amd64" {
		return clsNSEvent.Send(selKeyEvent, kind, nsPoint{}, flags, f.now(), window, objc.ID(0),
			nsString(chars), nsString(ignoring), false, code)
	}

	return clsNSEvent.Send(selKeyEvent, kind, nsPoint{}, flags, f.now(), window, objc.ID(0),
		nsString(chars), nsString(ignoring), uint64(code)<<16)
}

// key sends a press and a release of a key.
func (f *inputFixture) key(code uint16, chars string, flags uint64) {
	pool := poolPush()
	defer poolPop(pool)

	f.sendToWindow(f.keyEvent(eventKeyDown, code, chars, chars, flags))
	f.sendToWindow(f.keyEvent(eventKeyUp, code, chars, chars, flags))
}

func (f *inputFixture) click(x, y int) {
	f.mouse(eventMoved, x, y, 0, 0)
	f.mouse(eventLeftDown, x, y, 0, 0)
	f.mouse(eventLeftUp, x, y, 0, 0)
}

// The C functions tests make events with that NSEvent has no constructor for: scrolling, and
// mouse buttons past the second, as mouseEventWithType: can't set a buttonNumber.
var (
	cgScrollEvent func(source uintptr, units int32, wheels int32, w1, w2, w3 int32) uintptr
	cgMouseEvent  func(source uintptr, kind uint32, at nsPoint, button uint32) uintptr
	cgSetField    func(ev uintptr, field int32, value int64)
	cgSetLocation func(ev uintptr, at nsPoint)
	cgSetFlags    func(ev uintptr, flags uint64)
)

func bindCG(t *testing.T) {
	t.Helper()

	for name, fn := range map[string]any{
		"CGEventCreateScrollWheelEvent2": &cgScrollEvent,
		"CGEventCreateMouseEvent":        &cgMouseEvent,
		"CGEventSetIntegerValueField":    &cgSetField,
		"CGEventSetLocation":             &cgSetLocation,
		"CGEventSetFlags":                &cgSetFlags,
	} {
		p, err := purego.Dlsym(purego.RTLD_DEFAULT, name)
		if err != nil {
			t.Error(err)

			return
		}

		purego.RegisterFunc(fn, p)
	}
}

// scroll calls the view's method directly: an event from CGEvent belongs to no window, so
// NSApp would route it by the real pointer. Its location is taken as the window's, which
// the conversion below sets up.
func (f *inputFixture) scroll(x, y int, units, dy, dx int32, flags uint64, phase, momentum int64) {
	pool := poolPush()
	defer poolPop(pool)

	cg := cgScrollEvent(0, units, 2, dy, dx, 0)
	if cg == 0 {
		f.t.Error("no scroll event")

		return
	}
	defer cfRelease(cg)

	p := f.windowPoint(x, y)
	cgSetLocation(cg, nsPoint{p.X, primaryHeight() - p.Y})
	cgSetFlags(cg, flags)
	cgSetField(cg, fieldPhase, phase)
	cgSetField(cg, fieldMomentum, momentum)

	f.win.view.Send(selScrollWheel, clsNSEvent.Send(selEventWithCG, cg))
}

// otherMouse calls the view's method directly, for the reason scroll does.
func (f *inputFixture) otherMouse(kind uint64, x, y int, button uint32) {
	pool := poolPush()
	defer poolPop(pool)

	p := f.windowPoint(x, y)

	cg := cgMouseEvent(0, uint32(kind), nsPoint{p.X, primaryHeight() - p.Y}, button)
	if cg == 0 {
		f.t.Error("no mouse event")

		return
	}
	defer cfRelease(cg)

	sel := selOtherDown
	if kind == eventOtherUp {
		sel = selOtherUp
	}

	f.win.view.Send(sel, clsNSEvent.Send(selEventWithCG, cg))
}

func TestClickReachesTheKit(t *testing.T) {
	requireDisplay(t)

	onMain(func() {
		bindCG(t)

		f := openInput(t, "button", 200, 80)
		defer f.close()

		clicks := 0

		f.k.SetEmit(func(id uint32, kind int, _ float64) {
			if id == 1 && kind == engine.EvClick {
				clicks++
			}
		})

		x, y := f.center()
		f.click(x, y)

		if clicks != 1 {
			t.Errorf("%d clicks", clicks)
		}

		// The other buttons are no clicks.
		f.otherMouse(eventOtherDown, x, y, buttonMiddle)
		f.otherMouse(eventOtherUp, x, y, buttonMiddle)
		f.mouse(eventRightDown, x, y, 0, 1)
		f.mouse(eventRightUp, x, y, 0, 1)

		if clicks != 1 {
			t.Errorf("%d clicks after the other buttons", clicks)
		}
	})
}

// Each button reaches the kit as its own call, with the button state the kit documents.
func TestButtonsReachTheKit(t *testing.T) {
	requireDisplay(t)

	onMain(func() {
		bindCG(t)

		f := openInput(t, "button", 200, 80)
		defer f.close()

		spy := &buttonSpy{Kit: f.h.kit}
		f.h.kit = spy

		defer func() { f.h.kit = spy.Kit }()

		x, y := f.center()

		// pressedMouseButtons may not show the button yet on its own press, so the press
		// adds it, and the release takes it off even while it still shows.
		held := uint64(0)
		f.h.buttons = func() uint64 { return held }

		f.mouse(eventLeftDown, x, y, flagShift, 0)

		held = 1

		f.mouse(eventLeftUp, x, y, flagShift, 0)

		held = 0

		f.mouse(eventRightDown, x, y, 0, 1)
		f.mouse(eventRightUp, x, y, 0, 1)
		f.otherMouse(eventOtherDown, x, y, buttonMiddle)
		f.otherMouse(eventOtherUp, x, y, buttonMiddle)
		f.otherMouse(eventOtherDown, x, y, 3) // a side button means nothing here
		f.otherMouse(eventOtherUp, x, y, 3)

		want := []string{
			fmt.Sprintf("press %d", engine.ModShift|engine.ModButton1),
			fmt.Sprintf("release %d", engine.ModShift),
			"menu",
			"button 3 up",
			"button 2 down",
			"button 2 up",
		}
		if !slices.Equal(spy.calls, want) {
			t.Errorf("calls %q, want %q", spy.calls, want)
		}
	})
}

type buttonSpy struct {
	engine.Kit

	calls []string
}

func (s *buttonSpy) PressMods(ctx engine.Context, x, y, mods int) {
	s.calls = append(s.calls, fmt.Sprintf("press %d", mods))
	s.Kit.PressMods(ctx, x, y, mods)
}

func (s *buttonSpy) ReleaseMods(ctx engine.Context, x, y, mods int) {
	s.calls = append(s.calls, fmt.Sprintf("release %d", mods))
	s.Kit.ReleaseMods(ctx, x, y, mods)
}

func (s *buttonSpy) ContextMenu(ctx engine.Context, x, y, mods int) bool {
	s.calls = append(s.calls, "menu")

	return s.Kit.ContextMenu(ctx, x, y, mods)
}

func (s *buttonSpy) Button(ctx engine.Context, x, y, button int, release bool, mods int) bool {
	state := "down"
	if release {
		state = "up"
	}

	s.calls = append(s.calls, fmt.Sprintf("button %d %s", button, state))

	return s.Kit.Button(ctx, x, y, button, release, mods)
}

// A click at the top of a window lands on the widget at the top. The middle of the window
// wouldn't show a flipped y.
func TestClickPositionIsNotFlipped(t *testing.T) {
	requireDisplay(t)

	onMain(func() {
		f := openInput(t, "button", 200, 200)
		defer f.close()

		var got []uint32

		other := f.k.NodeNew("button")
		f.k.NodeSetID(other, 2)
		f.k.NodeAdd(f.root, other)
		f.k.GeomSetPack(other)
		f.win.full = true
		f.win.render()

		f.k.SetEmit(func(id uint32, kind int, _ float64) {
			if kind == engine.EvClick {
				got = append(got, id)
			}
		})

		ox, oy, ow, oh := f.k.NodeRect(other)
		f.click(ox+ow/2, oy+oh/2)

		if len(got) != 1 || got[0] != 2 {
			t.Errorf("a click on node 2, at the bottom, reached %v", got)
		}
	})
}

func TestSliderDragAndWheelEvents(t *testing.T) {
	requireDisplay(t)

	onMain(func() {
		bindCG(t)

		f := openInput(t, "slider", 300, 40)
		defer f.close()

		f.k.SetNum(f.child, "min", 0)
		f.k.SetNum(f.child, "max", 100)
		f.k.SetNum(f.child, "value", 50)
		f.win.render()

		x, y := f.center()
		f.mouse(eventMoved, x, y, 0, 0)

		// The wheel with Ctrl steps a value, in both directions.
		f.scroll(x, y, scrollUnitLn, 1, 0, flagControl, 0, 0)

		up := f.k.GetNum(f.child, "value")
		if up <= 50 {
			t.Errorf("wheel up gave %v", up)
		}

		f.scroll(x, y, scrollUnitLn, -1, 0, flagControl, 0, 0)

		if v := f.k.GetNum(f.child, "value"); v >= up {
			t.Errorf("wheel down gave %v, from %v", v, up)
		}

		before := f.k.GetNum(f.child, "value")

		held := uint64(0)
		f.h.buttons = func() uint64 { return held }

		f.mouse(eventLeftDown, x, y, 0, 0)

		held = 1

		f.mouse(eventLeftDrag, x+40, y, 0, 0)

		// While a button is held, leaving the window keeps the drag.
		f.win.view.Send(selMouseExited, objc.ID(0))
		f.mouse(eventLeftDrag, x+60, y, 0, 0)

		held = 0

		f.mouse(eventLeftUp, x+60, y, 0, 0)

		if v := f.k.GetNum(f.child, "value"); v <= before {
			t.Errorf("dragging right went from %v to %v", before, v)
		}
	})
}

// A trackpad's small deltas add up to notches, and momentum after a key press is dropped.
func TestTrackpadScroll(t *testing.T) {
	requireDisplay(t)

	onMain(func() {
		bindCG(t)

		f := openInput(t, "slider", 300, 40)
		defer f.close()

		f.k.SetNum(f.child, "min", 0)
		f.k.SetNum(f.child, "max", 100)
		f.k.SetNum(f.child, "value", 50)
		f.win.render()

		x, y := f.center()
		f.mouse(eventMoved, x, y, 0, 0)

		f.scroll(x, y, scrollUnitPx, 5, 0, flagControl, phaseBegan, 0)
		f.scroll(x, y, scrollUnitPx, 5, 0, flagControl, phaseChanged, 0)

		if v := f.k.GetNum(f.child, "value"); v != 50 {
			t.Errorf("10 points of scroll moved the value to %v", v)
		}

		f.scroll(x, y, scrollUnitPx, 5, 0, flagControl, phaseChanged, 0)

		stepped := f.k.GetNum(f.child, "value")
		if stepped <= 50 {
			t.Errorf("15 points of scroll left the value at %v", stepped)
		}

		f.key(keyA, "a", 0)
		f.scroll(x, y, scrollUnitPx, 50, 0, flagControl, 0, phaseChanged)

		if v := f.k.GetNum(f.child, "value"); v != stepped {
			t.Errorf("momentum after a key press moved the value from %v to %v", stepped, v)
		}
	})
}

// Sideways scrolling reaches the kit as Shift and the wheel, from a wheel and from a trackpad.
func TestHorizontalScrollIsShiftAndWheel(t *testing.T) {
	requireDisplay(t)

	onMain(func() {
		bindCG(t)

		f := openInput(t, "button", 200, 80)
		defer f.close()

		spy := &wheelSpy{Kit: f.h.kit}
		f.h.kit = spy

		defer func() { f.h.kit = spy.Kit }()

		x, y := f.center()

		f.scroll(x, y, scrollUnitLn, 0, 1, 0, 0, 0)
		f.scroll(x, y, scrollUnitLn, 0, -1, 0, 0, 0)
		f.scroll(x, y, scrollUnitPx, 0, 15, 0, phaseBegan, 0)

		want := []string{
			fmt.Sprintf("1 %d", engine.ModShift),
			fmt.Sprintf("-1 %d", engine.ModShift),
			fmt.Sprintf("1 %d", engine.ModShift),
		}
		if !slices.Equal(spy.calls, want) {
			t.Errorf("wheel calls %q, want %q", spy.calls, want)
		}
	})
}

type wheelSpy struct {
	engine.Kit

	calls []string
}

func (s *wheelSpy) Wheel(ctx engine.Context, x, y, delta, mods int) bool {
	s.calls = append(s.calls, fmt.Sprintf("%d %d", delta, mods))

	return s.Kit.Wheel(ctx, x, y, delta, mods)
}

func TestTakeNotches(t *testing.T) {
	for _, tc := range []struct {
		sum, rest float64
		dir, n    int
	}{
		{0, 0, 1, 0},
		{11.5, 11.5, 1, 0},
		{12, 0, 1, 1},
		{-30, -6, -1, 2},
		{25, 1, 1, 2},
		{1000, 1000 - maxNotches*scrollStep, 1, maxNotches},
	} {
		if rest, dir, n := takeNotches(tc.sum); rest != tc.rest || n != tc.n || (n > 0 && dir != tc.dir) {
			t.Errorf("takeNotches(%v) = %v, %d, %d, want %v, %d, %d", tc.sum, rest, dir, n, tc.rest, tc.dir, tc.n)
		}
	}
}

func TestTypingReachesATextField(t *testing.T) {
	requireDisplay(t)

	onMain(func() {
		f := openInput(t, "text", 300, 40)
		defer f.close()

		x, y := f.center()
		f.click(x, y)

		f.key(keyA, "a", 0)
		f.key(0x0B, "b", 0)
		f.key(keyBackSpace, "\x7f", 0)
		f.key(0x08, "c", 0)

		// Command makes no text, as Ctrl doesn't on X11, and Return commits.
		f.key(keyQ, "q", flagCommand)
		f.key(keyReturn, "\r", 0)

		if got := f.k.GetStr(f.child, "text"); got != "ac" {
			t.Errorf("typed %q", got)
		}
	})
}

// Non-ASCII text survives the way through NSString, as it does for a layout with umlauts.
func TestTypingUTF8(t *testing.T) {
	requireDisplay(t)

	onMain(func() {
		f := openInput(t, "text", 300, 40)
		defer f.close()

		x, y := f.center()
		f.click(x, y)
		f.key(0x21, "ü", 0)
		f.key(keyReturn, "\r", 0)

		if got := f.k.GetStr(f.child, "text"); got != "ü" {
			t.Errorf("typed %q", got)
		}
	})
}

func TestKeysyms(t *testing.T) {
	requireDisplay(t)

	onMain(func() {
		f := openInput(t, "button", 100, 40)
		defer f.close()

		pool := poolPush()
		defer poolPop(pool)

		for _, tc := range []struct {
			code          uint16
			chars         string
			flags         uint64
			keysym, typed string
		}{
			{keyA, "a", 0, "a", "a"},
			{keyA, "A", flagShift, "A", "A"},
			{keyA, "a", flagCommand, "a", ""},
			{0x31, " ", 0, "space", " "},
			{0x1B, "-", 0, "minus", "-"},
			{keyReturn, "\r", 0, "Return", ""},
			{0x4C, "\x03", 0, "KP_Enter", ""},
			{keyBackSpace, "\x7f", 0, "BackSpace", ""},
			{0x75, "", 0, "Delete", ""},
			{keyF1, "", 0, "F1", ""},
			{keyLeft, "", 0, "Left", ""},
			{0x72, "", 0, "Insert", ""},
			{keyKeypad5, "5", 0, "KP_5", "5"},
			{0x43, "*", 0, "KP_Multiply", "*"},
			{0x7F, "", 0, "", ""}, // AppKit's function-key range, on no known key
			{keyA, "", 0, "", ""},  // a dead key has no characters
		} {
			ev := f.keyEvent(eventKeyDown, tc.code, tc.chars, tc.chars, tc.flags)

			if got := keysymOf(ev, tc.code); got != tc.keysym {
				t.Errorf("key %#x %q: keysym %q, want %q", tc.code, tc.chars, got, tc.keysym)
			}

			if got := typedText(ev, tc.code, tc.flags); got != tc.typed {
				t.Errorf("key %#x %q: text %q, want %q", tc.code, tc.chars, got, tc.typed)
			}
		}
	})
}

func TestModifierBits(t *testing.T) {
	h := New(nil)
	held := uint64(0)
	h.buttons = func() uint64 { return held }

	for _, tc := range []struct {
		flags, buttons uint64
		want           int
	}{
		{0, 0, 0},
		{flagShift, 0, engine.ModShift},
		{flagControl, 0, engine.ModCtrl},
		{flagCommand, 0, engine.ModCtrl},
		{flagOption, 0, engine.ModAlt},
		{0, 1, engine.ModButton1},
		{0, 2, 0},       // the right button alone
		{1 << 16, 0, 0}, // Caps Lock
		{flagShift | flagCommand, 1, engine.ModShift | engine.ModCtrl | engine.ModButton1},
	} {
		held = tc.buttons

		if got := h.modsOf(tc.flags); got != tc.want {
			t.Errorf("modsOf(%#x) with buttons %d = %d, want %d", tc.flags, tc.buttons, got, tc.want)
		}
	}
}

// flagsChanged reports modifiers as keys: only those that changed, and Command as Meta.
func TestModifierKeysReachTheKit(t *testing.T) {
	requireDisplay(t)

	onMain(func() {
		spy := &keySpy{Kit: nil}

		f := openInput(t, "button", 100, 40)
		defer f.close()

		spy.Kit = f.h.kit
		f.h.kit = spy
		f.win.flags = 0

		f.win.handleFlags(flagShift)
		f.win.handleFlags(flagShift | flagCommand)
		f.win.handleFlags(flagCommand)

		want := []string{"+Shift_L", "+Meta_L", "-Shift_L"}
		if !slices.Equal(spy.keys, want) {
			t.Errorf("keys %v, want %v", spy.keys, want)
		}

		f.h.kit = spy.Kit
	})
}

// keySpy records the key calls and passes the rest on.
type keySpy struct {
	engine.Kit

	keys []string
}

func (s *keySpy) Key(ctx engine.Context, text, keysym string, mods int) bool {
	s.keys = append(s.keys, "+"+keysym)

	return s.Kit.Key(ctx, text, keysym, mods)
}

func (s *keySpy) KeyRelease(ctx engine.Context, keysym string, mods int) bool {
	s.keys = append(s.keys, "-"+keysym)

	return s.Kit.KeyRelease(ctx, keysym, mods)
}

// NSApp swallows key-ups while Command or Option is held, and Control and Tab never reach
// the view. dispatch handles those itself, once each.
func TestDispatchHandlesWhatNSAppSwallows(t *testing.T) {
	requireDisplay(t)

	onMain(func() {
		spy := &keySpy{}

		f := openInput(t, "button", 100, 40)
		defer f.close()

		spy.Kit = f.h.kit
		f.h.kit = spy

		pool := poolPush()
		defer poolPop(pool)

		up := f.keyEvent(eventKeyUp, keyQ, "q", "q", flagCommand)
		f.h.dispatch(up)
		f.h.dispatch(f.keyEvent(eventKeyDown, keyCodeTab, "\t", "\t", flagControl))

		// A key-up NSApp does pass on, while dispatch is still in it, is not a second release.
		// A test app has no key window, so the view gets it here as NSApp would hand it over.
		f.h.keyUpDone = up
		f.win.view.Send(selKeyUp, up)
		f.h.keyUpDone = 0

		want := []string{"-q", "+Tab"}
		if !slices.Equal(spy.keys, want) {
			t.Errorf("keys %v, want %v", spy.keys, want)
		}

		f.h.kit = spy.Kit
	})
}

func TestCursors(t *testing.T) {
	requireDisplay(t)

	onMain(func() {
		f := openInput(t, "text", 200, 40)
		defer f.close()

		pool := poolPush()
		defer poolPop(pool)

		for _, tc := range []struct {
			cursor int
			shape  objc.SEL
		}{
			{0, selArrowCursor},
			{cursorEW, selResizeEW},
			{cursorNS, selResizeNS},
			{cursorText, selIBeam},
			{cursorCrosshair, selCrosshair},
			{cursorHand, selPointingHand},
			{cursorMove, selOpenHand},
			{cursorNotAllowed, selNotAllowed},
			{8, selArrowCursor},
			{12, selArrowCursor},
			{-1, selArrowCursor},
		} {
			f.win.cursor = tc.cursor
			f.win.applyCursor()

			if got, want := clsNSCursor.Send(selCurrentCursor), clsNSCursor.Send(tc.shape); got != want {
				t.Errorf("cursor %d shows another shape", tc.cursor)
			}
		}

		// Hide and unhide are counted, so hiding twice must need one unhide.
		f.win.cursor = cursorNone
		f.win.applyCursor()
		f.win.applyCursor()

		if !f.h.cursorHidden {
			t.Error("the none cursor shows")
		}

		f.win.cursor = 0
		f.win.applyCursor()

		if f.h.cursorHidden {
			t.Error("the cursor stays hidden")
		}

		// Closing a window with its cursor hidden shows it again.
		f.win.cursor = cursorNone
		f.win.applyCursor()
		f.win.nswin.Send(selClose)

		if f.h.cursorHidden {
			t.Error("a closed window keeps the cursor hidden")
		}
	})
}

// Only the sum is checked, as a real warp would move the user's pointer.
func TestWarpTarget(t *testing.T) {
	requireDisplay(t)

	onMain(func() {
		f := openInput(t, "button", 200, 100)
		defer f.close()

		pool := poolPush()
		defer poolPop(pool)

		frame := objc.Send[nsRect](f.win.nswin, selFrame)
		content := objc.Send[nsRect](f.win.nswin, objc.RegisterName("contentRectForFrameRect:"), frame)

		// The content's top left, on a screen whose origin is the bottom left.
		top := primaryHeight() - (content.Origin.Y + content.Size.H)

		for _, tc := range []struct {
			x, y int
			want nsPoint
		}{
			{0, 0, nsPoint{content.Origin.X, top}},
			{f.win.w, f.win.h, nsPoint{content.Origin.X + 200, top + 100}},
			{int(10 * f.win.px), int(20 * f.win.px), nsPoint{content.Origin.X + 10, top + 20}},
		} {
			if got := f.win.warpTarget(tc.x, tc.y); got != tc.want {
				t.Errorf("warpTarget(%d, %d) = %v, want %v", tc.x, tc.y, got, tc.want)
			}
		}
	})
}

// A move stamped just after a warp still carries the old position.
func TestMotionAfterAWarpIsSkipped(t *testing.T) {
	requireDisplay(t)

	onMain(func() {
		spy := &motionSpy{}

		f := openInput(t, "button", 200, 80)
		defer f.close()

		spy.Kit = f.h.kit
		f.h.kit = spy

		f.mouse(eventMoved, 5, 5, 0, 0)

		if spy.moves != 1 {
			t.Errorf("%d moves before a warp", spy.moves)
		}

		f.h.warpTime = f.now() + 10

		f.mouse(eventMoved, 6, 6, 0, 0)

		if spy.moves != 1 {
			t.Errorf("%d moves after a warp", spy.moves)
		}

		f.h.warpTime = 0
		f.h.kit = spy.Kit
	})
}

type motionSpy struct {
	engine.Kit

	moves int
}

func (s *motionSpy) Motion(ctx engine.Context, x, y, mods int) bool {
	s.moves++

	return s.Kit.Motion(ctx, x, y, mods)
}

// The pointer leaving tells the kit, which takes off a hover.
func TestMouseExitedLeavesTheWidgets(t *testing.T) {
	requireDisplay(t)

	onMain(func() {
		spy := &exitSpy{}

		f := openInput(t, "button", 200, 80)
		defer f.close()

		spy.Kit = f.h.kit
		f.h.kit = spy

		f.win.view.Send(selMouseExited, objc.ID(0))

		if spy.x != offWindow || spy.y != offWindow {
			t.Errorf("the pointer left to %d, %d", spy.x, spy.y)
		}

		f.h.kit = spy.Kit
	})
}

type exitSpy struct {
	engine.Kit

	x, y int
}

func (s *exitSpy) Motion(ctx engine.Context, x, y, mods int) bool {
	s.x, s.y = x, y

	return s.Kit.Motion(ctx, x, y, mods)
}

func TestClipboardRoundTrip(t *testing.T) {
	requireDisplay(t)

	onMain(func() {
		f := openInput(t, "button", 100, 40)
		defer f.close()

		pool := poolPush()

		// A private pasteboard: the general one is the user's.
		pb := clsNSPasteboard.Send(selUnique).Send(selRetain)
		f.h.pasteboard = func() objc.ID { return pb }

		poolPop(pool)

		defer func() {
			pool := poolPush()

			pb.Send(objc.RegisterName("releaseGlobally"))
			pb.Send(selRelease)
			poolPop(pool)
		}()

		if text, ok := f.win.ClipboardGet(); ok {
			t.Errorf("an empty pasteboard gave %q", text)
		}

		for _, text := range []string{"hello", "", "Grüße 🎈"} {
			f.win.ClipboardSet(text)

			if got, ok := f.win.ClipboardGet(); !ok || got != text {
				t.Errorf("ClipboardGet after setting %q: %q, %v", text, got, ok)
			}
		}

		// Bytes that are no UTF-8 have no NSString, and leave the clipboard alone.
		f.win.ClipboardSet("kept")
		f.win.ClipboardSet("\xff\xfe")

		if got, _ := f.win.ClipboardGet(); got != "kept" {
			t.Errorf("after invalid UTF-8 the clipboard holds %q", got)
		}
	})
}

func TestHostCallsAfterCloseAreIgnored(t *testing.T) {
	requireDisplay(t)

	onMain(func() {
		f := openInput(t, "button", 200, 80)
		defer f.close()

		touched := false
		f.h.pasteboard = func() objc.ID {
			touched = true

			return 0
		}

		pool := poolPush()

		f.win.nswin.Send(selClose)
		poolPop(pool)

		f.win.renderPending = false

		f.win.Request()
		f.win.SetCursor(1)
		f.win.Warp(1, 1)
		f.win.ClipboardSet("x")

		if _, ok := f.win.ClipboardGet(); ok {
			t.Error("a closed window read the clipboard")
		}

		if f.win.renderPending {
			t.Error("a closed window has a render pending")
		}

		if touched {
			t.Error("a closed window reached the pasteboard")
		}
	})
}

func TestTooltipTimerFiresInTheLoop(t *testing.T) {
	requireDisplay(t)

	onMain(func() {
		f := openInput(t, "button", 300, 200)
		defer f.close()

		f.k.SetStr(f.child, "text", "Hover me")
		f.k.SetStr(f.child, "description", "A tooltip")
		f.win.render()

		x, y := f.center()
		f.mouse(eventMoved, x, y, 0, 0)
		f.win.render()

		hovered := layerPixels(t, f.win)

		if f.win.tipDue.IsZero() {
			t.Error("hovering scheduled no tooltip")

			return
		}

		if wait, forever := f.h.nextWait(time.Now()); forever || wait <= 0 {
			t.Errorf("the wait is %v, forever %v, with a timer running", wait, forever)
		}

		deadline := time.Now().Add(5 * time.Second)
		for (!f.win.tipDue.IsZero() || f.win.renderPending) && time.Now().Before(deadline) {
			f.h.turn()
		}

		if !f.win.tipDue.IsZero() {
			t.Error("the tooltip timer never fired")
		}

		if after := layerPixels(t, f.win); string(after) == string(hovered) {
			t.Error("the tooltip drew nothing")
		}
	})
}

func TestRunWithOneOfTwoWindowsClosed(t *testing.T) {
	requireDisplay(t)

	onMain(func() {
		f := openInput(t, "button", 200, 80)
		defer f.close()

		other := f.h.WindowNew(f.k.ContextNew(f.k.NodeNew("frame")), "bui cocoa test", 100, 60)
		defer f.h.WindowFree(other)

		second := f.h.find(other)
		f.h.WindowShow(other)

		stage := 0

		f.h.WakeInit(func() {
			switch stage {
			case 0:
				f.win.nswin.Send(selPerformClose, objc.ID(0))
				f.h.Wake()
			case 1:
				if f.h.open != 1 || second.nswin == 0 {
					t.Errorf("after the first close: %d open", f.h.open)
				}

				second.nswin.Send(selPerformClose, objc.ID(0))
			}

			stage++
		})
		f.h.Wake()

		if err := f.h.Run(); err != nil {
			t.Error(err)
		}

		if f.h.open != 0 || stage != 2 {
			t.Errorf("Run ended with %d open, at stage %d", f.h.open, stage)
		}
	})
}

// A Wake from a goroutine during a burst of events never blocks, and runs the function.
func TestWakeFromAnotherGoroutine(t *testing.T) {
	requireDisplay(t)

	var fired atomic.Int32

	onMain(func() {
		f := openInput(t, "button", 200, 80)
		defer f.close()

		f.h.WakeInit(func() {
			if fired.Add(1) == 1 {
				f.win.nswin.Send(selPerformClose, objc.ID(0))
			}
		})

		done := make(chan struct{})

		go func() {
			defer close(done)

			for range 1000 {
				f.h.Wake()
			}
		}()

		if err := f.h.Run(); err != nil {
			t.Error(err)
		}

		<-done
	})

	if fired.Load() == 0 {
		t.Error("the wake never ran")
	}
}

// A window freed inside its own event outlives the event, and goes between loop turns.
func TestWindowFreedFromItsOwnEvent(t *testing.T) {
	requireDisplay(t)

	onMain(func() {
		f := openInput(t, "button", 200, 80)

		weak := watchDealloc(t, f.win.nswin)
		if weak == nil {
			f.close()

			return
		}

		id := f.id
		queued := -1

		// The first thing the view's method asks, so the free comes from inside the event.
		f.h.buttons = func() uint64 {
			if queued < 0 {
				f.h.buttons = func() uint64 { return 0 }

				f.h.WindowFree(id)
				queued = len(f.h.dead)
			}

			return 0
		}

		f.mouse(eventMoved, 10, 10, 0, 0)

		if queued != 1 {
			t.Errorf("%d windows waiting for release inside the event, want 1", queued)
		}

		// The next turn releases it. A wake keeps that turn from waiting.
		f.h.Wake()
		f.h.turn()

		if len(f.h.dead) != 0 {
			t.Errorf("%d windows still waiting after a turn", len(f.h.dead))
		}

		if !weak.gone(f.h, 2*time.Second) {
			t.Error("the NSWindow is still alive")
		}

		f.k.SetEmit(func(uint32, int, float64) {})
		f.free()
	})
}

// A panic in a handler ends the process: it must not unwind through AppKit's frames.
func TestFatalPathsExit(t *testing.T) {
	requireDisplay(t)

	cmd := exec.Command(os.Args[0], "-test.run=^TestChildProcess$", "-test.count=1")

	cmd.Env = append(os.Environ(), childEnv+"=view-panic")

	out, err := cmd.CombinedOutput()

	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 2 {
		t.Fatalf("child: %v, want exit status 2\n%s", err, out)
	}
}

func TestChildProcess(t *testing.T) {
	if os.Getenv(childEnv) == "" {
		t.Skip("only runs as a subprocess")
	}

	onMain(func() {
		f := openInput(t, "button", 100, 40)
		f.h.buttons = func() uint64 { panic("boom") }

		// A recover as a caller might have it must not let the process carry on.
		func() {
			defer func() { _ = recover() }()

			f.mouse(eventMoved, 5, 5, 0, 0)
		}()
	})

	t.Error("the process survived")
}

// Modifiers released in another application leave no stale state: becoming key again
// rereads them, or the next press of that modifier would reach the kit as nothing.
func TestBecomingKeyRereadsTheModifiers(t *testing.T) {
	requireDisplay(t)

	onMain(func() {
		f := openInput(t, "button", 100, 40)
		defer f.close()

		pool := poolPush()
		defer poolPop(pool)

		now := objc.Send[uint64](clsNSEvent, selModifierFlags)
		f.win.flags = now ^ flagCommand

		note := objc.ID(objc.GetClass("NSNotification")).Send(selNotification, nsString("NSWindowDidBecomeKeyNotification"), f.win.nswin)
		f.win.nswin.Send(selDelegate).Send(selBecomeKey, note)

		if f.win.flags != now {
			t.Errorf("flags %#x after becoming key, want %#x", f.win.flags, now)
		}
	})
}
