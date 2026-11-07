// SPDX-License-Identifier: GPL-2.0-or-later

package x11

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/CWBudde/bui-go/internal/engine"
	"github.com/CWBudde/bui-go/internal/engine/wasmengine"
	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgb/xtest"
)

// privateDisplay says the tests have an X server to themselves, so that they may change
// its layout and send it real input.
var privateDisplay bool

// TestMain gives the tests their own X server when Xvfb is there. On a shared one they
// would fail by no fault of the host: windows of another test binary, which go test runs
// in parallel, cover theirs and show in what they read back, and clipboards collide. Nor
// do they then open windows on a real desktop. Subprocesses inherit DISPLAY.
func TestMain(m *testing.M) {
	stop := startXvfb()
	code := m.Run()

	stop()
	os.Exit(code)
}

// startXvfb sets DISPLAY to a new server and returns what ends it. Xvfb picks a free
// display and writes its number to the file descriptor it is told. -noreset is needed as
// it otherwise resets when its last client goes, and a connection during that fails.
func startXvfb() (stop func()) {
	path, err := exec.LookPath("Xvfb")
	if err != nil || os.Getenv(childEnv) != "" {
		return func() {}
	}

	r, w, err := os.Pipe()
	if err != nil {
		return func() {}
	}
	defer r.Close()

	const displayFD = "3" // the first of ExtraFiles

	cmd := exec.Command(path, "-displayfd", displayFD, "-screen", "0", "1280x1024x24", "-nolisten", "tcp", "-noreset")
	cmd.ExtraFiles = []*os.File{w}

	err = cmd.Start()

	_ = w.Close() // the server has its own copy, and EOF must mean it is gone

	if err != nil {
		return func() {}
	}

	stop = func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}

	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil {
		stop()

		return func() {}
	}

	os.Setenv("DISPLAY", ":"+strings.TrimSpace(line))

	privateDisplay = true

	return stop
}

// requirePrivateDisplay skips a test that changes the server or sends it input.
func requirePrivateDisplay(t *testing.T) {
	t.Helper()

	if !privateDisplay {
		t.Skip("needs Xvfb for a server of its own")
	}

	if _, err := exec.LookPath("setxkbmap"); err != nil {
		t.Skip("no setxkbmap")
	}
}

// fixture is a host on the process's wasm kit with one window on a tree that fills it.
// Input arrives through handle, as real X events do, so these tests work on any display
// without touching its pointer or keyboard.
type fixture struct {
	t     *testing.T
	h     *Host
	k     engine.Kit
	root  engine.Node
	child engine.Node
	ctx   engine.Context
	win   engine.Window
	w     *window
}

// newHost returns a host, or skips with no display. The process's kit has one emit
// receiver, which a test may set through emit.
func newHost(t *testing.T) *Host {
	t.Helper()

	k := wasmengine.Kit{}
	k.SetEmit(func(uint32, int, float64) {})

	h := New(k)
	if !h.Supported() {
		t.Skip("no display")
	}

	t.Cleanup(func() { h.c.Close() })

	return h
}

func newFixture(t *testing.T, typ string, width, height int) *fixture {
	t.Helper()

	f := &fixture{t: t, h: newHost(t), k: wasmengine.Kit{}}
	f.h.scale = 1

	f.open(typ, width, height)

	return f
}

func (f *fixture) open(typ string, width, height int) {
	f.t.Helper()

	k := f.k
	f.root, f.child = k.NodeNew("frame"), k.NodeNew(typ)

	if f.root == 0 || f.child == 0 {
		f.t.Fatalf("cannot create a %s", typ)
	}

	k.NodeSetID(f.child, 1)
	k.NodeAdd(f.root, f.child)
	k.GeomSetPack(f.child)
	k.GeomSet(f.child, "fill", 3)
	k.GeomSet(f.child, "expand", 1)

	f.ctx = k.ContextNew(f.root)
	k.ContextSetEmitHost(f.ctx)

	f.win = f.h.WindowNew(f.ctx, "bui x11 test", width, height)
	if f.win == 0 {
		f.t.Fatal("WindowNew failed")
	}

	f.w = f.h.find(f.win)

	// As package bui requires: window, context, then the tree, whose child has an id.
	root, child, ctx, win := f.root, f.child, f.ctx, f.win

	f.t.Cleanup(func() {
		f.h.WindowFree(win)
		k.ContextFree(ctx)
		k.NodeFree(child)
		k.NodeFree(root)
	})
}

// show maps the window and waits for its first frame.
func (f *fixture) show() {
	f.t.Helper()
	f.h.WindowShow(f.win)
	f.pump(func() bool { return f.w.rasterW == f.w.w && f.mapped() && !f.w.renderPending })
}

// mapped reports whether the server has mapped the window. Without a window manager
// that takes one round trip, and the Expose then starts the real first frame.
func (f *fixture) mapped() bool {
	a, err := xproto.GetWindowAttributes(f.h.c, f.w.xid).Reply()

	return err == nil && a.MapState == xproto.MapStateViewable
}

// pump runs loop passes until done, in the way Run does, or fails after 5 s.
func (f *fixture) pump(done func() bool) {
	f.t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			f.t.Fatal("timed out")
		}

		// A pass with nothing due would wait forever, so a wake bounds each one.
		f.h.Wake()
		f.h.turn()
		time.Sleep(time.Millisecond)
	}
}

// settle runs passes until the queue is quiet and nothing is pending, so that all a
// test sent has been handled and presented.
func (f *fixture) settle() {
	f.t.Helper()

	f.sync()

	quiet := 0

	f.pump(func() bool {
		if f.h.events.empty() && !f.w.renderPending {
			quiet++
		} else {
			quiet = 0
		}

		return quiet > 2
	})
	f.sync()
}

// sync waits for the server to process every request sent so far.
func (f *fixture) sync() {
	f.t.Helper()

	if _, err := xproto.GetInputFocus(f.h.c).Reply(); err != nil {
		f.t.Fatal(err)
	}
}

// onScreen reads the window's pixels back from the server, without the padding byte.
func (f *fixture) onScreen() []uint32 {
	f.t.Helper()

	return readImage(f.t, f.h.c, f.w.xid, 0, 0, f.w.w, f.w.h)
}

func readImage(t *testing.T, c *xgb.Conn, xid xproto.Window, x, y, w, h int) []uint32 {
	t.Helper()

	// A reply's length has to fit too, so a large window comes in strips.
	const rowsPerRead = 64

	out := make([]uint32, 0, w*h)

	for top := y; top < y+h; top += rowsPerRead {
		rows := min(rowsPerRead, y+h-top)

		img, err := xproto.GetImage(c, xproto.ImageFormatZPixmap, xproto.Drawable(xid),
			int16(x), int16(top), uint16(w), uint16(rows), ^uint32(0)).Reply()
		if err != nil {
			t.Fatalf("GetImage: %v", err)
		}

		for i := 0; i+4 <= len(img.Data); i += 4 {
			out = append(out, uint32(img.Data[i])|uint32(img.Data[i+1])<<8|uint32(img.Data[i+2])<<16)
		}
	}

	return out
}

// checkPresented requires the screen to show exactly what the raster holds.
func (f *fixture) checkPresented(what string) []uint32 {
	f.t.Helper()
	f.settle()

	want := f.k.RasterPixels(f.w.raster)
	for i, p := range want {
		want[i] = p & 0xffffff
	}

	got := f.onScreen()
	if len(got) != len(want) {
		f.t.Fatalf("%s: %d pixels on screen, %d in the raster", what, len(got), len(want))
	}

	for i := range want {
		if got[i] != want[i] {
			f.t.Fatalf("%s: pixel (%d, %d) is %#x on screen, %#x in the raster",
				what, i%f.w.w, i/f.w.w, got[i], want[i])
		}
	}

	return got
}

func (f *fixture) center() (x, y int16) {
	f.t.Helper()

	nx, ny, w, h := f.k.NodeRect(f.child)
	if w <= 0 || h <= 0 {
		f.t.Fatalf("node has no size: %d x %d", w, h)
	}

	return int16(nx + w/2), int16(ny + h/2)
}

func (f *fixture) motion(x, y int16, state uint16) {
	f.h.handle(xproto.MotionNotifyEvent{Event: f.w.xid, EventX: x, EventY: y, State: state})
}

func (f *fixture) press(button byte, x, y int16, state uint16) {
	f.h.handle(xproto.ButtonPressEvent{Event: f.w.xid, Detail: xproto.Button(button), EventX: x, EventY: y, State: state})
}

func (f *fixture) release(button byte, x, y int16, state uint16) {
	f.h.handle(xproto.ButtonReleaseEvent{Event: f.w.xid, Detail: xproto.Button(button), EventX: x, EventY: y, State: state})
}

// keycode finds a key that types sym with no modifiers.
func (f *fixture) keycode(sym uint32) xproto.Keycode {
	f.t.Helper()

	k := f.h.keyboard
	for code := int(k.minCode); code < int(k.minCode)+len(k.syms)/k.perCode; code++ {
		if k.lookup(byte(code), 0) == sym {
			return xproto.Keycode(code)
		}
	}

	f.t.Fatalf("no key types %#x", sym)

	return 0
}

func (f *fixture) key(sym uint32) {
	f.t.Helper()

	code := f.keycode(sym)
	f.h.handle(xproto.KeyPressEvent{Event: f.w.xid, Detail: code})
	f.h.handle(xproto.KeyReleaseEvent{Event: f.w.xid, Detail: code, Time: 1})
}

func TestPresentMatchesTheRaster(t *testing.T) {
	f := newFixture(t, "button", 300, 100)
	f.k.SetStr(f.child, "text", "Hover me")
	f.show()

	before := f.checkPresented("the first frame")

	x, y := f.center()
	f.motion(x, y, 0)

	// A hover redraws through the damage list, not a full frame.
	if after := f.checkPresented("the hovered frame"); slices.Equal(before, after) {
		t.Error("hovering changed nothing on screen")
	}

	if damage := f.k.RasterDamage(f.w.raster); len(damage) == 0 {
		t.Error("the hover drew no damage")
	}

	f.h.handle(xproto.LeaveNotifyEvent{Event: f.w.xid})

	if after := f.checkPresented("the frame after leaving"); !slices.Equal(before, after) {
		t.Error("leaving did not restore the screen")
	}
}

// A frame this size takes many requests, past the server's limit for one.
func TestPresentSplitsLargeFrames(t *testing.T) {
	f := newFixture(t, "button", 800, 600)
	f.k.SetStr(f.child, "text", "A large frame")
	f.show()

	if 4*f.w.w*f.w.h <= f.h.maxData {
		t.Fatalf("a %d x %d frame fits one request of %d", f.w.w, f.w.h, f.h.maxData)
	}

	f.checkPresented("a large frame")
}

func TestExposeRedrawsTheWholeWindow(t *testing.T) {
	f := newFixture(t, "button", 200, 80)
	f.show()

	// Clearing shows the black background, and only a new frame can bring it back.
	xproto.ClearArea(f.h.c, false, f.w.xid, 0, 0, 0, 0)
	f.sync()

	if img := f.onScreen(); img[0] != 0 {
		t.Fatalf("the clear left %#x", img[0])
	}

	f.h.handle(xproto.ExposeEvent{Window: f.w.xid, Count: 1})

	if f.w.renderPending {
		t.Error("an Expose with more to come already asked for a frame")
	}

	f.h.handle(xproto.ExposeEvent{Window: f.w.xid, Count: 0})
	f.checkPresented("the frame after an Expose")
}

func TestResize(t *testing.T) {
	f := newFixture(t, "button", 200, 80)
	f.show()

	xproto.ConfigureWindow(f.h.c, f.w.xid, xproto.ConfigWindowWidth|xproto.ConfigWindowHeight, []uint32{350, 120})

	f.pump(func() bool { return f.w.w == 350 && f.w.h == 120 && !f.w.renderPending })
	f.checkPresented("the resized frame")

	if _, _, w, h := f.k.NodeRect(f.child); w != 350 || h != 120 {
		t.Errorf("the node is %d x %d after the resize", w, h)
	}

	// Shrinking matters too: the raster must not keep the old stride.
	xproto.ConfigureWindow(f.h.c, f.w.xid, xproto.ConfigWindowWidth|xproto.ConfigWindowHeight, []uint32{90, 40})

	f.pump(func() bool { return f.w.w == 90 && !f.w.renderPending })
	f.checkPresented("the shrunken frame")
}

func TestScaleSizesTheWindow(t *testing.T) {
	h := newHost(t)
	h.scale = 2

	defer wasmengine.Kit{}.SetScale(1)

	f := &fixture{t: t, h: h, k: wasmengine.Kit{}}
	f.open("button", 100, 50)

	if f.w.w != 200 || f.w.h != 100 {
		t.Errorf("window is %d x %d at scale 2", f.w.w, f.w.h)
	}
}

func TestBadSizesStillMakeAWindow(t *testing.T) {
	f := newFixture(t, "frame", 0, -5)

	if f.w.w != 1 || f.w.h != 1 {
		t.Errorf("window is %d x %d", f.w.w, f.w.h)
	}

	f.show()
	f.checkPresented("the smallest window")
}

func TestClickReachesTheKit(t *testing.T) {
	f := newFixture(t, "button", 200, 80)

	clicks := 0

	f.k.SetEmit(func(id uint32, kind int, _ float64) {
		if id == 1 && kind == engine.EvClick {
			clicks++
		}
	})
	defer f.k.SetEmit(func(uint32, int, float64) {})

	f.show()

	x, y := f.center()
	f.motion(x, y, 0)
	f.press(buttonLeft, x, y, 0)
	f.release(buttonLeft, x, y, xproto.KeyButMaskButton1)

	if clicks != 1 {
		t.Errorf("%d clicks", clicks)
	}

	// The other buttons are no clicks.
	f.press(buttonMiddle, x, y, 0)
	f.release(buttonMiddle, x, y, 0)
	f.press(buttonRight, x, y, 0)
	f.release(buttonRight, x, y, 0)

	if clicks != 1 {
		t.Errorf("%d clicks after the other buttons", clicks)
	}
}

func TestSliderDragAndWheelEvents(t *testing.T) {
	f := newFixture(t, "slider", 300, 40)
	f.k.SetNum(f.child, "min", 0)
	f.k.SetNum(f.child, "max", 100)
	f.k.SetNum(f.child, "value", 50)
	f.show()

	x, y := f.center()
	f.motion(x, y, 0)

	// The wheel with Ctrl steps a value, in both directions.
	f.press(buttonWheelUp, x, y, xproto.KeyButMaskControl)

	up := f.k.GetNum(f.child, "value")
	if up <= 50 {
		t.Errorf("wheel up gave %v", up)
	}

	f.press(buttonWheelDown, x, y, xproto.KeyButMaskControl)

	if v := f.k.GetNum(f.child, "value"); v >= up {
		t.Errorf("wheel down gave %v, from %v", v, up)
	}

	before := f.k.GetNum(f.child, "value")

	f.press(buttonLeft, x, y, 0)
	f.motion(x+40, y, xproto.KeyButMaskButton1)

	// While a button is held, leaving the window keeps the drag.
	f.h.handle(xproto.LeaveNotifyEvent{Event: f.w.xid, State: xproto.KeyButMaskButton1})
	f.motion(x+60, y, xproto.KeyButMaskButton1)
	f.release(buttonLeft, x+60, y, xproto.KeyButMaskButton1)

	if v := f.k.GetNum(f.child, "value"); v <= before {
		t.Errorf("dragging right went from %v to %v", before, v)
	}

	f.checkPresented("the slider after the drag")
}

func TestTypingReachesATextField(t *testing.T) {
	f := newFixture(t, "text", 300, 40)
	f.show()

	x, y := f.center()
	f.press(buttonLeft, x, y, 0)
	f.release(buttonLeft, x, y, xproto.KeyButMaskButton1)

	const (
		xkBackSpace = 0xff08
		xkReturn    = 0xff0d
	)

	for _, sym := range []uint32{'a', 'b', xkBackSpace, 'c'} {
		f.key(sym)
	}

	// Control characters are not text, so Ctrl + a key types nothing and Return commits.
	code := f.keycode('q')
	f.h.handle(xproto.KeyPressEvent{Event: f.w.xid, Detail: code, State: xproto.KeyButMaskControl})
	f.key(xkReturn)

	if got := f.k.GetStr(f.child, "text"); got != "ac" {
		t.Errorf("typed %q", got)
	}

	f.checkPresented("the text after typing")
}

func TestModifierBits(t *testing.T) {
	for _, tc := range []struct {
		state uint16
		want  int
	}{
		{0, 0},
		{xproto.KeyButMaskShift, engine.ModShift},
		{xproto.KeyButMaskControl, engine.ModCtrl},
		{xproto.KeyButMaskMod1, engine.ModAlt},
		{xproto.KeyButMaskButton1, engine.ModButton1},
		{xproto.KeyButMaskMod5 | xproto.KeyButMaskLock | xproto.KeyButMaskMod2, 0},
		{xproto.KeyButMaskShift | xproto.KeyButMaskButton1, engine.ModShift | engine.ModButton1},
	} {
		if got := modsOf(tc.state); got != tc.want {
			t.Errorf("modsOf(%#x) = %d, want %d", tc.state, got, tc.want)
		}
	}
}

func TestAutorepeatIsNotARelease(t *testing.T) {
	q := &eventQueue{notify: make(chan struct{}, 1)}
	release := xproto.KeyReleaseEvent{Detail: 10, Time: 5}

	q.push(xproto.KeyPressEvent{Detail: 10, Time: 5})

	if !q.isAutorepeat(release) {
		t.Error("a press right after it, same key and time, is autorepeat")
	}

	q.pop()
	q.push(xproto.KeyPressEvent{Detail: 10, Time: 6})

	if q.isAutorepeat(release) {
		t.Error("a press at a later time is a new press")
	}

	q.pop()
	q.push(xproto.KeyPressEvent{Detail: 11, Time: 5})

	if q.isAutorepeat(release) {
		t.Error("another key is no autorepeat")
	}

	q.pop()

	if q.isAutorepeat(release) {
		t.Error("with nothing queued it is a release")
	}
}

func TestCursors(t *testing.T) {
	f := newFixture(t, "text", 200, 40)
	f.show()

	// Past either end clamps: the kit's BK_CURSOR_PROGRESS is 12.
	for c := -1; c <= cursorCount+1; c++ {
		f.w.SetCursor(c)
	}

	f.sync()

	for c, id := range f.h.cursors {
		if id == 0 {
			t.Errorf("no X cursor for %d", c)
		}
	}

	// The cursors go by the shared table, so each shape is made once.
	if f.h.cursors[0] != f.h.cursor(0) {
		t.Error("a second request made a new cursor")
	}

	f.checkNoXErrors()
}

func (f *fixture) checkNoXErrors() {
	f.t.Helper()
	f.sync()

	// Errors arrive through the event goroutine, shortly after the reply that passed them.
	time.Sleep(20 * time.Millisecond)

	f.h.events.mu.Lock()
	defer f.h.events.mu.Unlock()

	if f.h.events.errors != 0 {
		f.t.Errorf("%d X errors, printed above", f.h.events.errors)
	}
}

// A hover on a closed window, a cursor, a warp: requests that race with a close must
// not reach the server.
func TestHostCallsAfterCloseAreIgnored(t *testing.T) {
	f := newFixture(t, "button", 200, 80)
	f.show()

	f.w.close()
	f.w.Request()
	f.w.SetCursor(1)
	f.w.Warp(1, 1)
	f.w.ClipboardSet("x")

	if _, ok := f.w.ClipboardGet(); ok {
		t.Error("a closed window read the clipboard")
	}

	if f.w.renderPending {
		t.Error("a closed window has a render pending")
	}

	f.checkNoXErrors()
}

func TestTooltipTimerFiresInTheLoop(t *testing.T) {
	f := newFixture(t, "button", 300, 200)
	f.k.SetStr(f.child, "text", "Hover me")
	f.k.SetStr(f.child, "description", "A tooltip")
	f.show()

	x, y := f.center()
	f.motion(x, y, 0)

	hovered := f.checkPresented("the hovered frame")

	if f.w.tipDue.IsZero() {
		t.Fatal("hovering scheduled no tooltip")
	}

	if wait, forever := f.h.nextWait(time.Now()); forever || wait <= 0 {
		t.Errorf("the wait is %v, forever %v, with a timer running", wait, forever)
	}

	f.pump(func() bool { return f.w.tipDue.IsZero() && !f.w.renderPending })

	if after := f.checkPresented("the frame with the tooltip"); slices.Equal(hovered, after) {
		t.Error("the tooltip drew nothing")
	}
}

func TestClipboardBetweenClients(t *testing.T) {
	f := newFixture(t, "text", 300, 40)
	f.show()

	peer, err := xgb.NewConn()
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()

	t.Run("another client pastes what we copied", func(t *testing.T) {
		f.w.ClipboardSet("von bui: äöü")
		f.sync()

		for _, target := range []xproto.Atom{f.h.atoms.utf8, xproto.AtomString} {
			if got := peerPaste(t, f, peer, target); got != "von bui: äöü" {
				t.Errorf("target %d: pasted %q", target, got)
			}
		}

		got := peerPaste(t, f, peer, f.h.atoms.targets)
		if len(got) != 3*4 {
			t.Errorf("TARGETS has %d bytes", len(got))
		}
	})

	t.Run("a target we do not have is refused", func(t *testing.T) {
		f.w.ClipboardSet("text")
		f.sync()

		if got := peerPaste(t, f, peer, xproto.AtomBitmap); got != "" {
			t.Errorf("pasted %q", got)
		}
	})

	t.Run("we paste what we own without asking", func(t *testing.T) {
		f.w.ClipboardSet("eigen")
		f.sync()

		start := time.Now()

		if got, ok := f.w.ClipboardGet(); !ok || got != "eigen" {
			t.Errorf("got %q, %v", got, ok)
		}

		if d := time.Since(start); d >= clipboardTimeout {
			t.Errorf("took %v, which is the timeout", d)
		}
	})

	t.Run("we paste from another client", func(t *testing.T) {
		stop := peerOwn(t, f, peer, "vom Peer: €\x00cut")
		defer stop()

		if got, ok := f.w.ClipboardGet(); !ok || got != "vom Peer: €" {
			t.Errorf("got %q, %v", got, ok)
		}
	})

	t.Run("no owner means no text, without the timeout", func(t *testing.T) {
		xproto.SetSelectionOwner(peer, 0, f.h.atoms.clipboard, xproto.TimeCurrentTime)

		if _, err := xproto.GetInputFocus(peer).Reply(); err != nil {
			t.Fatal(err)
		}

		start := time.Now()

		if _, ok := f.w.ClipboardGet(); ok {
			t.Error("got text from nobody")
		}

		if d := time.Since(start); d >= clipboardTimeout {
			t.Errorf("took %v, which is the timeout", d)
		}
	})
}

// peerPaste asks for the clipboard from another connection while our loop answers.
func peerPaste(t *testing.T, f *fixture, peer *xgb.Conn, target xproto.Atom) string {
	t.Helper()

	xid, err := xproto.NewWindowId(peer)
	if err != nil {
		t.Fatal(err)
	}

	screen := xproto.Setup(peer).DefaultScreen(peer)

	if err := xproto.CreateWindowChecked(peer, screen.RootDepth, xid, screen.Root, 0, 0, 1, 1, 0,
		xproto.WindowClassInputOutput, screen.RootVisual, 0, nil).Check(); err != nil {
		t.Fatal(err)
	}
	defer xproto.DestroyWindow(peer, xid)

	xproto.ConvertSelection(peer, xid, f.h.atoms.clipboard, target, f.h.atoms.selections[0], xproto.TimeCurrentTime)

	if _, err := xproto.GetInputFocus(peer).Reply(); err != nil {
		t.Fatal(err)
	}

	f.settle()

	// The reply comes on the peer's connection: wait there, with the loop not needed.
	type result struct {
		ev  xgb.Event
		err error
	}

	got := make(chan result, 1)

	go func() {
		for {
			ev, xerr := peer.WaitForEvent()
			if ev == nil {
				got <- result{nil, xerr}

				return
			}

			if _, ok := ev.(xproto.SelectionNotifyEvent); ok {
				got <- result{ev, nil}

				return
			}
		}
	}()

	select {
	case r := <-got:
		if r.ev == nil {
			t.Fatalf("waiting for SelectionNotify: %v", r.err)
		}

		sn, _ := r.ev.(xproto.SelectionNotifyEvent)
		if sn.Property == xproto.AtomNone {
			return ""
		}

		prop, err := xproto.GetProperty(peer, true, xid, f.h.atoms.selections[0], xproto.GetPropertyTypeAny, 0, 1<<16).Reply()
		if err != nil {
			t.Fatal(err)
		}

		return string(prop.Value)
	case <-time.After(5 * time.Second):
		t.Fatal("no SelectionNotify")
	}

	return ""
}

// peerOwn makes the peer own the clipboard and answer one request with text.
func peerOwn(t *testing.T, f *fixture, peer *xgb.Conn, text string) (stop func()) {
	t.Helper()

	xid, err := xproto.NewWindowId(peer)
	if err != nil {
		t.Fatal(err)
	}

	screen := xproto.Setup(peer).DefaultScreen(peer)

	if err := xproto.CreateWindowChecked(peer, screen.RootDepth, xid, screen.Root, 0, 0, 1, 1, 0,
		xproto.WindowClassInputOutput, screen.RootVisual, 0, nil).Check(); err != nil {
		t.Fatal(err)
	}

	xproto.SetSelectionOwner(peer, xid, f.h.atoms.clipboard, xproto.TimeCurrentTime)

	if _, err := xproto.GetInputFocus(peer).Reply(); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})

	go func() {
		defer close(done)

		for {
			ev, _ := peer.WaitForEvent()
			if ev == nil {
				return
			}

			rq, ok := ev.(xproto.SelectionRequestEvent)
			if !ok {
				continue
			}

			xproto.ChangeProperty(peer, xproto.PropModeReplace, rq.Requestor, rq.Property, rq.Target, 8,
				uint32(len(text)), []byte(text))

			notify := xproto.SelectionNotifyEvent{
				Time: rq.Time, Requestor: rq.Requestor, Selection: rq.Selection, Target: rq.Target, Property: rq.Property,
			}
			xproto.SendEvent(peer, false, rq.Requestor, 0, string(notify.Bytes()))

			return
		}
	}()

	return func() {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("the peer got no request")
		}

		xproto.DestroyWindow(peer, xid)
	}
}

// An owner that answers after the timeout must not answer the next paste, with its text
// or with its refusal.
func TestClipboardIgnoresLateAnswers(t *testing.T) {
	late := clipboardTimeout + 50*time.Millisecond

	for _, tc := range []struct {
		name  string
		first string
	}{
		{"late text", "alt"},
		{"late refusal", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, "text", 300, 40)
			f.show()

			peer, err := xgb.NewConn()
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()

			done := peerAnswers(t, f, peer, []peerAnswer{{delay: late, text: tc.first}, {delay: 100 * time.Millisecond, text: "neu"}})

			if got, ok := f.w.ClipboardGet(); ok {
				t.Errorf("the first paste got %q before the owner answered", got)
			}

			if got, ok := f.w.ClipboardGet(); !ok || got != "neu" {
				t.Errorf("the second paste got %q, %v", got, ok)
			}

			<-done
		})
	}
}

// peerAnswer is how a peer answers one request: after a delay, with text or, if there is
// none, by refusing.
type peerAnswer struct {
	delay time.Duration
	text  string
	incr  bool
}

// peerAnswers makes the peer own the clipboard and answer requests in order. The returned
// channel closes after the last.
func peerAnswers(t *testing.T, f *fixture, peer *xgb.Conn, answers []peerAnswer) <-chan struct{} {
	t.Helper()

	xid, err := xproto.NewWindowId(peer)
	if err != nil {
		t.Fatal(err)
	}

	screen := xproto.Setup(peer).DefaultScreen(peer)

	if err := xproto.CreateWindowChecked(peer, screen.RootDepth, xid, screen.Root, 0, 0, 1, 1, 0,
		xproto.WindowClassInputOutput, screen.RootVisual, 0, nil).Check(); err != nil {
		t.Fatal(err)
	}

	xproto.SetSelectionOwner(peer, xid, f.h.atoms.clipboard, xproto.TimeCurrentTime)

	if _, err := xproto.GetInputFocus(peer).Reply(); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})

	go func() {
		defer close(done)

		for _, answer := range answers {
			var rq xproto.SelectionRequestEvent

			for ok := false; !ok; {
				ev, _ := peer.WaitForEvent()
				if ev == nil {
					return
				}

				rq, ok = ev.(xproto.SelectionRequestEvent)
			}

			time.Sleep(answer.delay)

			notify := xproto.SelectionNotifyEvent{
				Time: rq.Time, Requestor: rq.Requestor, Selection: rq.Selection, Target: rq.Target, Property: xproto.AtomNone,
			}

			switch {
			case answer.incr:
				size := atomBytes(1 << 20)
				xproto.ChangeProperty(peer, xproto.PropModeReplace, rq.Requestor, rq.Property, f.h.atoms.incr, 32, 1, size)

				notify.Property = rq.Property
			case answer.text != "":
				xproto.ChangeProperty(peer, xproto.PropModeReplace, rq.Requestor, rq.Property, rq.Target, 8,
					uint32(len(answer.text)), []byte(answer.text))

				notify.Property = rq.Property
			}

			xproto.SendEvent(peer, false, rq.Requestor, 0, string(notify.Bytes()))
			xproto.GetInputFocus(peer).Reply() //nolint:errcheck // only flushes what was sent
		}
	}()

	return done
}

// A client with a selection that never answers must not hang the paste, and events that
// arrive during the wait must keep their order.
func TestClipboardTimeoutKeepsEvents(t *testing.T) {
	f := newFixture(t, "text", 300, 40)
	f.show()

	silent, err := xgb.NewConn()
	if err != nil {
		t.Fatal(err)
	}
	defer silent.Close()

	xid, err := xproto.NewWindowId(silent)
	if err != nil {
		t.Fatal(err)
	}

	screen := xproto.Setup(silent).DefaultScreen(silent)

	if err := xproto.CreateWindowChecked(silent, screen.RootDepth, xid, screen.Root, 0, 0, 1, 1, 0,
		xproto.WindowClassInputOutput, screen.RootVisual, 0, nil).Check(); err != nil {
		t.Fatal(err)
	}

	xproto.SetSelectionOwner(silent, xid, f.h.atoms.clipboard, xproto.TimeCurrentTime)

	if _, err := xproto.GetInputFocus(silent).Reply(); err != nil {
		t.Fatal(err)
	}

	f.settle()

	f.h.events.push(xproto.ExposeEvent{Window: f.w.xid, Count: 7})
	f.h.events.push(xproto.ExposeEvent{Window: f.w.xid, Count: 8})

	start := time.Now()

	if _, ok := f.w.ClipboardGet(); ok {
		t.Error("got text from a silent owner")
	}

	if d := time.Since(start); d < clipboardTimeout || d > 3*clipboardTimeout {
		t.Errorf("the wait took %v", d)
	}

	for _, want := range []uint16{7, 8} {
		e, ok := f.h.events.pop().(xproto.ExposeEvent)
		if !ok || e.Count != want {
			t.Errorf("event %v, want the Expose with count %d", e, want)
		}
	}
}

func TestResourceDPI(t *testing.T) {
	for _, tc := range []struct {
		res  string
		want float64
	}{
		{"", 0},
		{"Xft.dpi:\t144\n", 144},
		{"Xcursor.size: 24\nXft.dpi:  192\nXft.hinting: 1\n", 192},
		{"Xft.dpi: lots\n", 0},
		{"NotXft.dpi: 200\n", 0},
	} {
		if got := resourceDPI(tc.res); got != tc.want {
			t.Errorf("resourceDPI(%q) = %v, want %v", tc.res, got, tc.want)
		}
	}
}

func TestDisplayScaleEnvironment(t *testing.T) {
	h := newHost(t)

	for _, tc := range []struct {
		env  string
		want float32
	}{
		{"2", 2},
		{"1.5", 1.5},
	} {
		t.Setenv("BUI_UI_SCALE", tc.env)

		if got := displayScale(h.c, h.root); got != tc.want {
			t.Errorf("BUI_UI_SCALE=%s gives %v", tc.env, got)
		}
	}

	// Nonsense falls through to the server's answer, at least 1.
	for _, env := range []string{"0", "-1", "big"} {
		t.Setenv("BUI_UI_SCALE", env)

		if got := displayScale(h.c, h.root); got < 1 {
			t.Errorf("BUI_UI_SCALE=%s gives %v", env, got)
		}
	}
}

func TestRunEndsWhenTheLastWindowCloses(t *testing.T) {
	f := newFixture(t, "button", 200, 80)
	f.show()

	peer, err := xgb.NewConn()
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()

	// What a window manager's close button sends.
	var data [5]uint32
	data[0] = uint32(f.h.atoms.delete)

	msg := xproto.ClientMessageEvent{
		Format: 32, Window: f.w.xid, Type: f.h.atoms.protocols,
		Data: xproto.ClientMessageDataUnionData32New(data[:]),
	}
	xproto.SendEvent(peer, false, f.w.xid, 0, string(msg.Bytes()))

	if _, err := xproto.GetInputFocus(peer).Reply(); err != nil {
		t.Fatal(err)
	}

	if err := runWithTimeout(t, f.h); err != nil {
		t.Errorf("Run = %v", err)
	}

	if f.w.xid != 0 {
		t.Error("the window is still open")
	}

	if f.h.open != 0 {
		t.Errorf("open = %d", f.h.open)
	}
}

// runWithTimeout runs the loop, failing rather than hanging if it never returns.
func runWithTimeout(t *testing.T, h *Host) error {
	t.Helper()

	timer := time.AfterFunc(5*time.Second, func() {
		buf := make([]byte, 1<<16)
		t.Errorf("Run did not return:\n%s", buf[:runtime.Stack(buf, true)])
		os.Exit(3)
	})
	defer timer.Stop()

	return h.Run()
}

func TestRunWithOneOfTwoWindowsClosed(t *testing.T) {
	f := newFixture(t, "button", 200, 80)
	first := f.w

	f.open("button", 100, 40)
	first.close()

	if f.h.open != 1 {
		t.Fatalf("open = %d", f.h.open)
	}

	woke := make(chan struct{}, 1)

	f.h.WakeInit(func() {
		woke <- struct{}{}

		f.h.Quit()
	})
	f.h.Wake()

	if err := runWithTimeout(t, f.h); err != nil {
		t.Errorf("Run = %v", err)
	}

	select {
	case <-woke:
	default:
		t.Error("the loop stopped with a window open and no wake")
	}
}

func TestWakeFromAnotherGoroutine(t *testing.T) {
	f := newFixture(t, "button", 200, 80)
	f.show()

	count := 0

	f.h.WakeInit(func() {
		count++

		f.h.Quit()
	})

	go func() {
		time.Sleep(10 * time.Millisecond)
		f.h.Wake()
	}()

	if err := runWithTimeout(t, f.h); err != nil {
		t.Errorf("Run = %v", err)
	}

	if count == 0 {
		t.Error("the wake function did not run")
	}
}

// A burst of wakes must never block a sender, as Do holds its lock while waking.
func TestWakeNeverBlocks(t *testing.T) {
	h := New(wasmengine.Kit{})

	done := make(chan struct{})

	go func() {
		for range 1000 {
			h.Wake()
		}

		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Wake blocked")
	}
}

// A Do during an endless animation must still run, which the C host's loop would not do.
func TestWakeRunsWhileRendering(t *testing.T) {
	f := newFixture(t, "button", 200, 80)
	f.show()

	woke := false

	f.h.WakeInit(func() { woke = true })
	f.h.Wake()
	f.w.renderPending = true
	f.h.frameDue = time.Time{}
	f.h.turn()

	if !woke {
		t.Error("a pending render starved the wake")
	}
}

// Mapping changes reread the keymap, and a test can cause one without changing it.
func TestMappingNotifyRereadsTheKeymap(t *testing.T) {
	f := newFixture(t, "button", 200, 80)
	old := f.h.keyboard

	f.h.handle(xproto.MappingNotifyEvent{})

	if f.h.keyboard == old {
		t.Error("the keyboard was not reread")
	}

	if !slices.Equal(f.h.keyboard.syms, old.syms) {
		t.Error("the same mapping read differently")
	}
}

// The state an event carries decides the layout, so this is the end of the chain that
// the comparison with Xlib cannot see: what the server really sends after enableXKB. It
// sends real keys and changes the layout.
func TestSecondLayoutReachesEvents(t *testing.T) {
	requirePrivateDisplay(t)

	if out, err := exec.Command("setxkbmap", "us,ru", "-option", "grp:caps_toggle").CombinedOutput(); err != nil {
		t.Skipf("setxkbmap: %v\n%s", err, out)
	}

	t.Cleanup(func() { _ = exec.Command("setxkbmap", "us", "-option", "").Run() })

	f := newFixture(t, "text", 300, 40)
	f.show()

	if err := xtest.Init(f.h.c); err != nil {
		t.Skipf("no XTEST: %v", err)
	}

	const (
		evdevQ    = 24
		evdevCaps = 66
	)

	tap := func(code xproto.Keycode) {
		screen := xproto.Setup(f.h.c).DefaultScreen(f.h.c)
		xtest.FakeInput(f.h.c, xproto.KeyPress, byte(code), 0, screen.Root, 0, 0, 0)
		xtest.FakeInput(f.h.c, xproto.KeyRelease, byte(code), 0, screen.Root, 0, 0, 0)
		f.settle()
	}

	x, y := f.center()
	f.press(buttonLeft, x, y, 0)
	f.release(buttonLeft, x, y, xproto.KeyButMaskButton1)
	xproto.SetInputFocus(f.h.c, xproto.InputFocusParent, f.w.xid, xproto.TimeCurrentTime)
	f.settle()

	f.pump(func() bool { return f.h.keyboard.xkb != nil && f.h.keyboard.lookup(evdevQ, 1<<13) != 'q' })

	tap(evdevQ)
	tap(evdevCaps)
	tap(evdevQ)
	tap(evdevCaps)
	tap(evdevQ)
	f.k.Key(f.ctx, "", "Return", 0)

	if got := f.k.GetStr(f.child, "text"); got != "qйq" {
		t.Errorf("typed %q, want the second layout's letter in the middle", got)
	}
}

// A client with XKB enabled gets no core MappingNotify, so a layout change has to reach
// it through XKB's events.
func TestLayoutChangeReachesTheHost(t *testing.T) {
	requirePrivateDisplay(t)

	if out, err := exec.Command("setxkbmap", "us").CombinedOutput(); err != nil {
		t.Skipf("setxkbmap: %v\n%s", err, out)
	}

	f := newFixture(t, "text", 300, 40)
	f.show()

	if f.h.keyboard.xkb == nil {
		t.Skip("no XKB on this server")
	}

	const evdevY = 29

	if got := f.h.keyboard.lookup(evdevY, 0); got != 'y' {
		t.Fatalf("the us layout gives %#x", got)
	}

	if out, err := exec.Command("setxkbmap", "de").CombinedOutput(); err != nil {
		t.Skipf("setxkbmap: %v\n%s", err, out)
	}

	t.Cleanup(func() { _ = exec.Command("setxkbmap", "us").Run() })

	f.pump(func() bool { return f.h.keyboard.lookup(evdevY, 0) == 'z' })
}

func TestSupportedWithoutDisplay(t *testing.T) {
	t.Setenv("DISPLAY", "")

	h := New(wasmengine.Kit{})
	if h.Supported() {
		t.Error("Supported with DISPLAY unset")
	}

	if win := h.WindowNew(1, "x", 10, 10); win != 0 {
		t.Errorf("WindowNew = %d", win)
	}

	if err := h.Run(); err != nil {
		t.Errorf("Run = %v", err)
	}

	h.Quit()
}

const childEnv = "BUI_X11_TEST_CHILD"

// TestChildProcess does what ends the process when another test asks for it.
func TestChildProcess(t *testing.T) {
	switch os.Getenv(childEnv) {
	case "":
		t.Skip("only runs as a subprocess")
	case "wake-panic":
		f := newFixture(t, "button", 100, 40)
		f.h.WakeInit(func() { panic("boom") })
		f.h.Wake()

		// A recover as a caller might have it must not let the process carry on.
		func() {
			defer func() { _ = recover() }()

			_ = f.h.Run()
		}()
	}

	t.Fatal("the process survived")
}

func TestFatalPathsExit(t *testing.T) {
	for _, tc := range []struct {
		child string
		code  int
	}{
		{"wake-panic", 2},
	} {
		t.Run(tc.child, func(t *testing.T) {
			if _, err := xgb.NewConn(); err != nil {
				t.Skip("no display")
			}

			cmd := exec.Command(os.Args[0], "-test.run=^TestChildProcess$", "-test.count=1")

			cmd.Env = append(os.Environ(), childEnv+"="+tc.child)

			out, err := cmd.CombinedOutput()

			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != tc.code {
				t.Fatalf("child: %v, want exit status %d\n%s", err, tc.code, out)
			}
		})
	}
}

// Losing the server ends Run with an error, wherever in the queue the loss shows.
func TestConnectionLostEndsRun(t *testing.T) {
	for _, tc := range []struct {
		name string
		lose func(*fixture)
	}{
		{"closed", func(f *fixture) { f.h.c.Close() }},
		{"signal consumed", func(f *fixture) {
			// Another waiter took the one signal, as a key release's peek can.
			f.settle()
			f.h.events.close()
			<-f.h.events.notify
		}},
		{"behind events", func(f *fixture) {
			// The closing only counts once the events are gone.
			f.settle()
			f.h.events.push(xproto.ExposeEvent{Window: f.w.xid, Count: 1})
			f.h.events.close()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, "button", 100, 40)
			tc.lose(f)

			if err := runWithTimeout(t, f.h); !errors.Is(err, engine.ErrDisplayLost) {
				t.Errorf("Run = %v", err)
			}

			if f.h.open != 0 || f.w.xid != 0 {
				t.Errorf("open = %d, xid = %d", f.h.open, f.w.xid)
			}

			if f.h.Supported() {
				t.Error("Supported after the connection broke")
			}

			if win := f.h.WindowNew(f.ctx, "again", 10, 10); win != 0 {
				t.Errorf("WindowNew = %d", win)
			}
		})
	}
}

// xkill and the like destroy the window with no message: the loop must notice.
func TestRunEndsWhenTheWindowIsDestroyed(t *testing.T) {
	f := newFixture(t, "button", 200, 80)
	f.show()

	peer, err := xgb.NewConn()
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()

	if err := xproto.DestroyWindowChecked(peer, f.w.xid).Check(); err != nil {
		t.Fatal(err)
	}

	if err := runWithTimeout(t, f.h); err != nil {
		t.Errorf("Run = %v", err)
	}

	if f.w.xid != 0 || f.h.open != 0 {
		t.Errorf("xid = %d, open = %d", f.w.xid, f.h.open)
	}
}

// Other extensions' numbers must stay as xgb has them: an embedder may use one.
func TestOnlyXKBsEventIsClaimed(t *testing.T) {
	newHost(t)

	claimed := 0

	for number, decode := range xgb.NewEventFuncs {
		if _, ok := decode(make([]byte, 32)).(xkbEvent); ok {
			claimed++

			if number != xkbEventNumber {
				t.Errorf("event %d decodes as XKB, whose number is %d", number, xkbEventNumber)
			}
		}
	}

	if claimed != 1 {
		t.Errorf("%d event numbers decode as XKB", claimed)
	}
}

// An owner sending in parts stores the size where the text would be, which is no text.
func TestClipboardRefusesIncr(t *testing.T) {
	f := newFixture(t, "text", 300, 40)
	f.show()

	peer, err := xgb.NewConn()
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()

	done := peerAnswers(t, f, peer, []peerAnswer{{incr: true}})

	if got, ok := f.w.ClipboardGet(); ok {
		t.Errorf("paste got %d bytes", len(got))
	}

	<-done
}

// Text that fits one request goes out whole. Longer text is refused, and must not corrupt
// the stream of requests, which is what xgb's truncated length field would do.
func TestClipboardServesOnlyWhatFitsARequest(t *testing.T) {
	for _, tc := range []struct {
		name  string
		extra int
		want  bool
	}{
		{"fits", 0, true},
		{"too long", 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, "text", 300, 40)
			f.show()

			peer, err := xgb.NewConn()
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()

			xid, err := xproto.NewWindowId(peer)
			if err != nil {
				t.Fatal(err)
			}

			screen := xproto.Setup(peer).DefaultScreen(peer)

			if err := xproto.CreateWindowChecked(peer, screen.RootDepth, xid, screen.Root, 0, 0, 1, 1, 0,
				xproto.WindowClassInputOutput, screen.RootVisual, 0, nil).Check(); err != nil {
				t.Fatal(err)
			}

			f.h.clip, f.h.hasClip = strings.Repeat("x", f.h.maxData+tc.extra), true
			f.h.handleSelectionRequest(xproto.SelectionRequestEvent{
				Requestor: xid, Selection: f.h.atoms.clipboard, Target: f.h.atoms.utf8, Property: f.h.atoms.selections[0],
			})

			ev, xerr := peer.WaitForEvent()
			if xerr != nil {
				t.Fatal(xerr)
			}

			sn, ok := ev.(xproto.SelectionNotifyEvent)
			if !ok {
				t.Fatalf("got %v", ev)
			}

			if got := sn.Property != xproto.AtomNone; got != tc.want {
				t.Errorf("served = %v", got)
			}

			if _, err := xproto.GetInputFocus(f.h.c).Reply(); err != nil {
				t.Errorf("the connection did not survive: %v", err)
			}

			f.settle()

			if f.h.events.isClosed() || f.h.events.errors != 0 {
				t.Errorf("closed = %v, %d X errors", f.h.events.isClosed(), f.h.events.errors)
			}
		})
	}
}

func motion(win xproto.Window, state uint16, x int16) xproto.MotionNotifyEvent {
	return xproto.MotionNotifyEvent{Event: win, State: state, EventX: x}
}

func TestMotionReplacesOnlyItsOwnKind(t *testing.T) {
	q := &eventQueue{notify: make(chan struct{}, 1)}

	for _, ev := range []xgb.Event{
		motion(1, 0, 1), motion(1, 0, 2), // the second replaces the first
		motion(2, 0, 3),               // another window
		motion(2, 256, 4),             // a button went down
		xproto.ExposeEvent{Window: 2}, // order against others stays
		motion(2, 256, 5), motion(2, 256, 6),
	} {
		q.push(ev)
	}

	want := []xgb.Event{
		motion(1, 0, 2), motion(2, 0, 3), motion(2, 256, 4), xproto.ExposeEvent{Window: 2}, motion(2, 256, 6),
	}

	for i, w := range want {
		if got := q.pop(); got != w {
			t.Errorf("event %d: %v, want %v", i, got, w)
		}
	}

	if q.pop() != nil || q.events != nil {
		t.Errorf("after draining: %v", q.events)
	}
}

// A flood must leave a turn time for what comes after events.
func TestTurnBoundsItsEvents(t *testing.T) {
	f := newFixture(t, "button", 100, 40)
	f.show()
	f.settle()

	const extra = 10

	for range maxEventsPerTurn + extra {
		f.h.events.push(xproto.ExposeEvent{Window: f.w.xid, Count: 1})
	}

	f.h.turn()

	f.h.events.mu.Lock()
	left := len(f.h.events.events)
	f.h.events.mu.Unlock()

	if left != extra {
		t.Errorf("%d events left, want %d", left, extra)
	}
}
