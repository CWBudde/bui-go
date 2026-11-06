// SPDX-License-Identifier: GPL-2.0-or-later

// Verification beyond plain "go test". Prefer a private display, as the windowed
// tests open windows on whatever $DISPLAY names:
//
//	xvfb-run -a go test -race -count=5 ./...
//
// C memory: the suite only counts Go handles, so a C free that never happens is
// invisible to it. LeakSanitizer sees that, and leaks fail the run:
//
//	CC=gcc xvfb-run -a go test -asan -count=1 ./...
//
// Real X input, opt-in because it moves the pointer of the display it runs on:
//
//	BUI_TEST_XDOTOOL=1 xvfb-run -a go test -race -count=1 -run '^TestX' ./...

package bui

import (
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Test 1: linking. A call into the static kit returns a computed result.
func TestLinkEvalNumber(t *testing.T) {
	got, ok := EvalNumber("2*pi")
	if !ok {
		t.Fatal("EvalNumber(2*pi) reported failure")
	}
	if math.Abs(got-2*math.Pi) > 1e-9 {
		t.Fatalf("EvalNumber(2*pi) = %v, want %v", got, 2*math.Pi)
	}
	if _, ok := EvalNumber("2*"); ok {
		t.Fatal("EvalNumber(2*) should report failure for a malformed expression")
	}
}

// buttonInFrame builds a frame holding one packed button and renders it once.
func buttonInFrame(t *testing.T) (root, btn *Node, h *Headless) {
	t.Helper()
	root = NewNode("frame")
	if err := root.SetNum("padding", 8); err != nil {
		t.Fatal(err)
	}
	btn = NewNode("button")
	if err := btn.SetStr("text", "Apply"); err != nil {
		t.Fatal(err)
	}
	root.Add(btn)
	btn.Pack(PackOptions{Fill: FillX})
	t.Cleanup(root.Close)
	h = NewHeadless(root, 320, 120)
	t.Cleanup(h.Close)
	h.Render()
	return root, btn, h
}

// Test 2: layout runs in Render and gives a packed child a real rect.
func TestRectAfterRender(t *testing.T) {
	root := NewNode("frame")
	defer root.Close()
	btn := NewNode("button")
	root.Add(btn)
	btn.Pack(PackOptions{Fill: FillX})
	h := NewHeadless(root, 320, 120)
	defer h.Close()
	if x, y, w, ht := btn.Rect(); x != 0 || y != 0 || w != 0 || ht != 0 {
		t.Fatalf("rect before the first Render = (%d,%d,%d,%d), want all zero", x, y, w, ht)
	}
	h.Render()
	if x, y, w, ht := btn.Rect(); w <= 0 || ht <= 0 {
		t.Fatalf("rect after Render = (%d,%d,%d,%d), want non-empty", x, y, w, ht)
	}
}

// Test 3: Press + Release inside fires OnClick once; outside does not fire.
func TestClickHitAndMiss(t *testing.T) {
	_, btn, h := buttonInFrame(t)
	clicks := 0
	btn.OnClick(func() { clicks++ })
	x, y, w, ht := btn.Rect()

	// The button only fills the top of the 120 px raster: 40 px below it is a miss.
	missX, missY := x+w/2, y+ht+40
	h.Press(missX, missY)
	h.Release(missX, missY)
	if clicks != 0 {
		t.Fatalf("clicks after a miss at (%d,%d) = %d, want 0 (button rect %d,%d,%d,%d)", missX, missY, clicks, x, y, w, ht)
	}

	cx, cy := x+w/2, y+ht/2
	h.Press(cx, cy)
	if clicks != 0 {
		t.Fatalf("clicks after Press only = %d, want 0: CLICK belongs to the release", clicks)
	}
	h.Release(cx, cy)
	if clicks != 1 {
		t.Fatalf("clicks after a hit = %d, want exactly 1", clicks)
	}
}

// sliderInFrame builds a slider attached to a context, which it needs to emit.
func sliderInFrame(t *testing.T) (root, s *Node, h *Headless) {
	t.Helper()
	root = NewNode("frame")
	s = NewNode("slider")
	for _, kv := range []struct {
		key string
		v   float64
	}{{"min", 0}, {"max", 10}, {"value", 2.5}} {
		if err := s.SetNum(kv.key, kv.v); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetStr("label", "Size"); err != nil {
		t.Fatal(err)
	}
	root.Add(s)
	s.Pack(PackOptions{Fill: FillX})
	t.Cleanup(root.Close)
	h = NewHeadless(root, 320, 120)
	t.Cleanup(h.Close)
	h.Render()
	return root, s, h
}

// Test 4: "set_value" notifies synchronously, "value" is silent, values clamp.
func TestSliderChange(t *testing.T) {
	_, s, _ := sliderInFrame(t)
	var got []float64
	s.OnChange(func(v float64) { got = append(got, v) })

	if err := s.SetNum("value", 3); err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf(`plain "value" fired OnChange: %v`, got)
	}
	if v := s.Num("value"); v != 3 {
		t.Fatalf(`Num("value") after "value"=3 is %v`, v)
	}

	if err := s.SetNum("set_value", 7); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != 7 {
		t.Fatalf(`OnChange after "set_value"=7 got %v, want [7]`, got)
	}
	if v := s.Num("value"); v != 7 {
		t.Fatalf(`Num("value") after "set_value"=7 is %v`, v)
	}

	if err := s.SetNum("set_value", 7); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf(`"set_value" with an unchanged value fired OnChange: %v`, got)
	}

	if err := s.SetNum("set_value", 99); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1] != 10 {
		t.Fatalf(`OnChange after "set_value"=99 got %v, want [7 10]`, got)
	}
	if v := s.Num("value"); v != 10 {
		t.Fatalf(`Num("value") after clamping is %v, want 10`, v)
	}
	if s := s.Str("label"); s != "Size" {
		t.Fatalf(`Str("label") = %q, want "Size"`, s)
	}
}

// Test 5: a key unknown across all classes is an error, for both setters.
func TestUnknownKey(t *testing.T) {
	n := NewNode("label")
	defer n.Close()
	if err := n.SetNum("no_such_key", 1); err == nil {
		t.Error("SetNum with an unknown key returned nil")
	}
	if err := n.SetStr("no_such_key", "x"); err == nil {
		t.Error("SetStr with an unknown key returned nil")
	}
	if err := n.SetStr("text", "ok"); err != nil {
		t.Errorf("SetStr(text): %v", err)
	}
}

// tree builds root > (nested frame > button, label), slider and renders it.
func tree() (root *Node, h *Headless, nodes []*Node) {
	root = NewNode("frame")
	inner := NewNode("frame")
	btn := NewNode("button")
	lbl := NewNode("label")
	s := NewNode("slider")
	root.Add(inner)
	inner.Add(btn)
	inner.Add(lbl)
	root.Add(s)
	for _, n := range []*Node{inner, btn, lbl, s} {
		n.Pack(PackOptions{Fill: FillX})
	}
	h = NewHeadless(root, 200, 100)
	h.Render()
	return root, h, []*Node{inner, btn, lbl, s}
}

// Test 6: closing a root frees the whole subtree, and looping does not grow
// the handle table.
func TestCloseFreesSubtree(t *testing.T) {
	base := liveHandleCount()
	root, h, nodes := tree()
	if got := liveHandleCount() - base; got != 5 {
		t.Fatalf("live handles after building = baseline+%d, want baseline+5", got)
	}
	h.Close()
	root.Close()
	if got := liveHandleCount() - base; got != 0 {
		t.Fatalf("live handles after root.Close = baseline+%d, want baseline", got)
	}
	for _, n := range nodes {
		if !n.closed() {
			t.Errorf("descendant %s was not closed with its root", n.typ)
		}
	}

	for range 1000 {
		root, h, _ := tree()
		h.Close()
		root.Close()
	}
	if got := liveHandleCount() - base; got != 0 {
		t.Fatalf("live handles after 1000 iterations = baseline+%d, want baseline", got)
	}
}

// Test 7: closing a button inside its own OnClick waits until the kit has
// returned from BK_release, the outermost kit call on the stack.
func TestCloseInsideClickIsDeferred(t *testing.T) {
	base := liveHandleCount()
	root, btn, h := buttonInFrame(t)
	x, y, w, ht := btn.Rect()
	fired := 0
	liveInside := -1
	closedInside := true
	btn.OnClick(func() {
		fired++
		btn.Close()
		btn.Close() // a second request must not queue a second free
		liveInside = liveHandleCount() - base
		closedInside = btn.closed()
	})
	h.Press(x+w/2, y+ht/2)
	h.Release(x+w/2, y+ht/2)
	if fired != 1 {
		t.Fatalf("OnClick fired %d times, want 1", fired)
	}
	if liveInside != 2 || closedInside {
		t.Fatalf("inside the callback: live=baseline+%d closed=%v, want baseline+2 and not closed yet", liveInside, closedInside)
	}
	if !btn.closed() {
		t.Fatal("button still not closed after Release returned")
	}
	if got := liveHandleCount() - base; got != 1 {
		t.Fatalf("live handles after Release = baseline+%d, want baseline+1 (the root)", got)
	}
	h.Render() // the tree stays usable without the button
	h.Press(x+w/2, y+ht/2)
	h.Release(x+w/2, y+ht/2)
	if fired != 1 {
		t.Fatalf("OnClick fired %d times after Close, want 1", fired)
	}
	h.Close()
	root.Close()
	if got := liveHandleCount() - base; got != 0 {
		t.Fatalf("live handles at the end = baseline+%d, want baseline", got)
	}
}

// Headless and root closed together from a callback run in request order, after
// the kit has returned.
func TestCloseAllInsideClickIsDeferred(t *testing.T) {
	base := liveHandleCount()
	root, btn, h := buttonInFrame(t)
	x, y, w, ht := btn.Rect()
	btn.OnClick(func() {
		h.Close()
		root.Close()
	})
	h.Press(x+w/2, y+ht/2)
	h.Release(x+w/2, y+ht/2)
	if got := liveHandleCount() - base; got != 0 {
		t.Fatalf("live handles after Release = baseline+%d, want baseline", got)
	}
}

// A callback fired from a setter has no Press/Release to flush it: the setter
// itself must, once back at the outermost level.
func TestCloseInsideSetterIsDeferred(t *testing.T) {
	root, s, h := sliderInFrame(t)
	var closedInside bool
	s.OnChange(func(float64) {
		s.Close()
		closedInside = s.closed()
	})
	if err := s.SetNum("set_value", 5); err != nil {
		t.Fatal(err)
	}
	if closedInside {
		t.Fatal("slider freed inside its OnChange")
	}
	if !s.closed() {
		t.Fatal("slider not closed after SetNum returned")
	}
	h.Close()
	root.Close()
}

func mustPanic(t *testing.T, what string, f func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Errorf("%s did not panic", what)
		}
	}()
	f()
}

// Test 8: use after Close panics; double Close is safe.
func TestUseAfterClosePanics(t *testing.T) {
	root, h, _ := tree()
	h.Close()
	h.Close()
	mustPanic(t, "Headless.Render", h.Render)
	mustPanic(t, "Headless.Press", func() { h.Press(0, 0) })
	mustPanic(t, "Headless.Release", func() { h.Release(0, 0) })

	other := NewNode("label")
	defer other.Close()
	root.Close()
	root.Close()
	mustPanic(t, "SetNum", func() { _ = root.SetNum("padding", 1) })
	mustPanic(t, "SetStr", func() { _ = root.SetStr("text", "x") })
	mustPanic(t, "Num", func() { root.Num("padding") })
	mustPanic(t, "Str", func() { root.Str("text") })
	mustPanic(t, "Add parent", func() { root.Add(other) })
	mustPanic(t, "Add child", func() { other.Add(root) })
	mustPanic(t, "Pack", func() { root.Pack(PackOptions{}) })
	mustPanic(t, "Rect", func() { root.Rect() })
	mustPanic(t, "NewHeadless", func() { NewHeadless(root, 1, 1) })
	mustPanic(t, "OnClick", func() { root.OnClick(func() {}) })
	mustPanic(t, "OnChange", func() { root.OnChange(func(float64) {}) })
}

// Freeing the root under a live context leaves dangling pointers in C, so the
// binding refuses the wrong order; a second context on one root as well.
func TestRootOrderIsEnforced(t *testing.T) {
	root, _, h := buttonInFrame(t)
	mustPanic(t, "root.Close with a live context", root.Close)
	mustPanic(t, "second context on a root", func() { NewHeadless(root, 1, 1) })
	h.Close()
	root.Close()
}

func TestUnknownNodeTypePanics(t *testing.T) {
	mustPanic(t, "NewNode", func() { NewNode("no_such_type") })
}

// Test 9: Do from any goroutine neither blocks nor runs anything before the
// loop drains it, and it keeps FIFO order. No display needed.
func TestDoBeforeRunIsQueued(t *testing.T) {
	var order []int
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 3 {
			Do(func() { order = append(order, i) })
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Do blocked with no loop running")
	}
	if len(order) != 0 {
		t.Fatalf("Do ran its function without a drain: %v", order)
	}
	if n := pendingDo(); n != 3 {
		t.Fatalf("pending after three Do = %d, want 3", n)
	}
	drainDo()
	if len(order) != 3 || order[0] != 0 || order[1] != 1 || order[2] != 2 {
		t.Fatalf("drain order = %v, want [0 1 2]", order)
	}
	if n := pendingDo(); n != 0 {
		t.Fatalf("pending after the drain = %d, want 0", n)
	}
}

// Many goroutines at once: -race watches the queue, nothing is lost. A function
// queued from inside a drain waits for the next one, so a self-requeuing
// function cannot starve the loop.
func TestDoConcurrent(t *testing.T) {
	const goroutines, each = 8, 200
	count := 0
	var wg sync.WaitGroup
	for range goroutines {
		wg.Go(func() {
			for range each {
				Do(func() { count++ })
			}
		})
	}
	wg.Wait()
	drainDo()
	if count != goroutines*each {
		t.Fatalf("ran %d functions, want %d", count, goroutines*each)
	}

	nested := 0
	Do(func() { Do(func() { nested++ }) })
	drainDo()
	if nested != 0 {
		t.Fatal("a function queued during a drain ran in the same drain")
	}
	drainDo()
	if nested != 1 {
		t.Fatalf("nested ran %d times after the second drain, want 1", nested)
	}
}

// runGuarded runs the loop and kills the test binary if it does not return. The
// guard cannot use Do(Quit): that needs the same wake a hang may have lost.
func runGuarded(t *testing.T) {
	t.Helper()
	name := t.Name()
	guard := time.AfterFunc(10*time.Second, func() {
		fmt.Fprintf(os.Stderr, "FAIL: %s: Run did not return within 10s (lost wake or quit)\n", name)
		os.Exit(3)
	})
	defer guard.Stop()
	if err := Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func labelWindow(t *testing.T) (root, label *Node, win *Window) {
	t.Helper()
	if !Supported() {
		t.Skip("no display: BK_window_supported is 0")
	}
	root = NewNode("frame")
	label = NewNode("label")
	if err := label.SetStr("text", "before"); err != nil {
		t.Fatal(err)
	}
	root.Add(label)
	label.Pack(PackOptions{Fill: FillX})
	win, err := NewWindow(root, "bui-go test", 320, 120)
	if err != nil {
		t.Fatal(err)
	}
	win.Show()
	return root, label, win
}

// whenInLoop runs f from the wake callback inside BK_window_run. A function that
// finds itself in Run's initial drain queues itself again, so the path is
// certain rather than a matter of timing.
func whenInLoop(f func()) {
	Do(func() {
		if inLoop {
			f()
		} else {
			whenInLoop(f)
		}
	})
}

// Test 10: the full path. A goroutine reaches the UI thread through Do, the
// wake callback re-enters Go from inside BK_window_run, Quit ends the loop, and
// shutdown runs window, context, root.
func TestWindowDoAndQuit(t *testing.T) {
	base := liveHandleCount()
	root, label, win := labelWindow(t)
	go func() {
		whenInLoop(func() {
			if err := label.SetStr("text", "from goroutine"); err != nil {
				t.Error(err)
			}
			Do(Quit)
		})
	}()
	runGuarded(t)
	if got := label.Str("text"); got != "from goroutine" {
		t.Fatalf("label = %q after Run", got)
	}
	mustPanic(t, "root.Close before Window.Close", root.Close)
	win.Close()
	win.Close()
	mustPanic(t, "Window.Show after Close", win.Show)
	root.Close()
	if got := liveHandleCount() - base; got != 0 {
		t.Fatalf("live handles after shutdown = baseline+%d, want baseline", got)
	}
}

// Queued before Run, wakes are lost and a quit drained before BK_window_run
// would be erased by it. Both must still take effect.
func TestWindowDoQueuedBeforeRun(t *testing.T) {
	root, label, win := labelWindow(t)
	defer root.Close()
	defer win.Close()
	ran := 0
	Do(func() {
		ran++
		if err := label.SetStr("text", "queued"); err != nil {
			t.Error(err)
		}
	})
	Do(Quit)
	runGuarded(t)
	if ran != 1 || label.Str("text") != "queued" {
		t.Fatalf("ran=%d label=%q, want the queued function to have run once", ran, label.Str("text"))
	}
}

// The loop can run again after a Quit, and a burst of wakes loses nothing.
func TestWindowRunTwiceAndWakeBurst(t *testing.T) {
	root, _, win := labelWindow(t)
	defer root.Close()
	defer win.Close()
	for range 2 {
		const burst = 2000
		count := 0
		go func() {
			for range burst {
				Do(func() { count++ })
			}
			Do(Quit)
		}()
		runGuarded(t)
		if count != burst {
			t.Fatalf("ran %d of %d functions", count, burst)
		}
	}
}

// A handler fired inside BK_window_run closes the window. It must not take
// effect until the kit is off the stack, and the loop must end by itself as its
// only window is gone: no Quit is ever sent.
func TestWindowCloseInsideCallback(t *testing.T) {
	if !Supported() {
		t.Skip("no display: BK_window_supported is 0")
	}
	base := liveHandleCount()
	root := NewNode("frame")
	s := NewNode("slider")
	if err := s.SetNum("max", 10); err != nil {
		t.Fatal(err)
	}
	root.Add(s)
	s.Pack(PackOptions{Fill: FillX})
	win, err := NewWindow(root, "bui-go test", 320, 120)
	if err != nil {
		t.Fatal(err)
	}
	win.Show()
	fired, closedInside := 0, true
	s.OnChange(func(float64) {
		fired++
		if !inLoop {
			t.Error("handler ran outside BK_window_run: the test would prove nothing")
		}
		win.Close()
		root.Close()
		closedInside = root.closed()
	})
	whenInLoop(func() {
		if err := s.SetNum("set_value", 5); err != nil {
			t.Error(err)
		}
	})
	runGuarded(t)
	if fired != 1 || closedInside {
		t.Fatalf("fired=%d closedInside=%v, want 1 and false", fired, closedInside)
	}
	if !root.closed() {
		t.Fatal("root not closed after Run returned")
	}
	if got := liveHandleCount() - base; got != 0 {
		t.Fatalf("live handles = baseline+%d, want baseline", got)
	}
}

// With no display, Run and NewWindow report ErrNoDisplay rather than Run
// returning silently. An opened display stays open in-process, hence the
// subprocess.
func TestNoDisplay(t *testing.T) {
	if os.Getenv("BUI_GO_TEST_NO_DISPLAY") == "1" {
		root := NewNode("frame")
		defer root.Close()
		if Supported() {
			t.Fatal("Supported with DISPLAY unset")
		}
		if _, err := NewWindow(root, "x", 10, 10); !errors.Is(err, ErrNoDisplay) {
			t.Fatalf("NewWindow error = %v, want ErrNoDisplay", err)
		}
		ran := false
		Do(func() { ran = true })
		if err := Run(); !errors.Is(err, ErrNoDisplay) {
			t.Fatalf("Run error = %v, want ErrNoDisplay", err)
		}
		if ran {
			t.Fatal("Run without a display ran queued functions")
		}
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestNoDisplay$", "-test.count=1")
	cmd.Env = append(os.Environ(), "DISPLAY=", "BUI_GO_TEST_NO_DISPLAY=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("subprocess: %v\n%s", err, out)
	}
}

// asIfInHandler runs f the way an X-event handler looks to the binding: inside
// emit, and with no binding call around it that flushes afterwards.
func asIfInHandler(f func()) {
	emitDepth++
	defer func() { emitDepth-- }()
	f()
}

// Finding: a free at depth 0 must not overtake frees queued earlier.
func TestDeferredFreesKeepRequestOrder(t *testing.T) {
	var order []string
	asIfInHandler(func() {
		closeOrDefer(func() { order = append(order, "first") })
	})
	closeOrDefer(func() { order = append(order, "second") })
	if got := strings.Join(order, ","); got != "first,second" {
		t.Fatalf("free order = %q, want first,second", got)
	}
}

// A handler's h.Close, then root.Close in a Do of the same wake: the context
// must already be gone when that Do runs. Calls the wake callback directly, as
// the loop would, so no display is needed.
func TestWakeRunsDeferredBeforeDo(t *testing.T) {
	root, _, h := buttonInFrame(t)
	contextGone := false
	asIfInHandler(func() {
		h.Close()
		Do(func() {
			contextGone = h.ctx.c == nil
			root.Close()
		})
	})
	buiGoWake()
	if !contextGone {
		t.Fatal("the Do ran before the deferred Close requested ahead of it")
	}
	if !root.closed() {
		t.Fatal("root not closed after the wake")
	}
}

// The case where the wrong order does damage: BK_context_free closes the open
// popup through its owner, here the root, so the root must still exist. Freed
// first, that is a use-after-free in the kit, which needs an ASan-built kit to
// see reliably; on the stock one this checks the order only.
func TestWakeOrderWithPopoverRootOpen(t *testing.T) {
	root := NewNode("popover")
	root.Add(NewNode("label")) // the body: a popover without one never opens
	h := NewHeadless(root, 300, 200)
	h.Render()
	x, y, w, ht := root.Rect()
	h.Press(x+w/2, y+ht/2)
	h.Release(x+w/2, y+ht/2)
	h.Render()
	contextGone := false
	asIfInHandler(func() {
		h.Close()
		Do(func() {
			contextGone = h.ctx.c == nil
			root.Close()
		})
	})
	buiGoWake()
	if !contextGone || !root.closed() {
		t.Fatalf("context gone when root closed=%v, root closed=%v, want both", contextGone, root.closed())
	}
}

// Deferred destruction from a handler that nothing flushes after: the wake from
// closeOrDefer is all that frees the window and so ends the loop. No Do or Quit.
func TestWindowDeferredCloseWakesLoop(t *testing.T) {
	root, _, win := labelWindow(t)
	whenInLoop(func() {
		asIfInHandler(func() {
			win.Close()
			root.Close()
		})
	})
	runGuarded(t)
	if win.c != nil || !root.closed() {
		t.Fatalf("window freed=%v root closed=%v, want both", win.c == nil, root.closed())
	}
}

// A handler that closes something and quits: the loop ends with the wake unread,
// so only Run's final drain can run the free and the Do, in that order.
func TestHandlerClosesThenQuits(t *testing.T) {
	root, label, win := labelWindow(t)
	defer root.Close()
	defer win.Close()
	ran, closedInDo := false, false
	whenInLoop(func() {
		asIfInHandler(func() {
			label.Close()
			Do(func() {
				ran = true
				closedInDo = label.closed()
			})
			Quit()
		})
	})
	runGuarded(t)
	if !label.closed() {
		t.Fatal("label not freed by the time Run returned")
	}
	if !ran || !closedInDo {
		t.Fatalf("Do ran=%v and saw the free done=%v, want both", ran, closedInDo)
	}
}

// The kit can still emit for a node between Close and its free: X events queued
// behind the closing handler. A second Node on the same C node stands in for
// that kit call, because the closed one rightly refuses SetNum.
func TestEmitForClosingNodeIsIgnored(t *testing.T) {
	_, s, _ := sliderInFrame(t)
	fired := 0
	s.OnChange(func(float64) { fired++ })
	alias := &Node{c: s.c, typ: s.typ}
	asIfInHandler(func() {
		s.Close()
		if err := alias.SetNum("set_value", 5); err != nil {
			t.Error(err)
		}
	})
	runDeferred()
	if fired != 0 {
		t.Fatalf("OnChange fired %d times after Close, want 0", fired)
	}
}

// A node only emits while attached to a context, and BK_context_free is what
// detaches the root. LeakSanitizer cannot check that free, since the kit's global
// context list keeps an unfreed context reachable, so this checks its effect.
func TestContextCloseDetachesRoot(t *testing.T) {
	_, s, h := sliderInFrame(t)
	fired := 0
	s.OnChange(func(float64) { fired++ })
	if err := s.SetNum("set_value", 1); err != nil {
		t.Fatal(err)
	}
	if fired != 1 {
		t.Fatalf("OnChange fired %d times with a live context, want 1", fired)
	}
	h.Close()
	if err := s.SetNum("set_value", 2); err != nil {
		t.Fatal(err)
	}
	if fired != 1 {
		t.Fatal("OnChange fired after Headless.Close: the root is still attached to a context")
	}
	if v := s.Num("value"); v != 2 {
		t.Fatalf("value = %v, want 2", v)
	}
}

// Close requested in a handler makes the node itself unusable at once. Its
// descendants, still alive in C, stay usable until the free, as documented.
func TestDescendantOfDeferredClose(t *testing.T) {
	root, h, nodes := tree()
	defer root.Close()
	defer h.Close()
	inner, btn := nodes[0], nodes[1]
	asIfInHandler(func() {
		inner.Close()
		mustPanic(t, "use of the closing node", func() { inner.Rect() })
		btn.Rect()
	})
	runDeferred()
	mustPanic(t, "use of a freed descendant", func() { btn.Rect() })
}

// Children for the crash tests below, selected by the parent test through the
// environment. Each tries to survive; SURVIVED in the output means it did.
func TestChildProcess(t *testing.T) {
	mode := os.Getenv("BUI_GO_TEST_CHILD")
	if mode == "" {
		t.Skip("only runs as a subprocess of the handler crash tests")
	}
	survive := func(f func()) {
		defer func() { fmt.Printf("recovered: %v\n", recover()) }()
		f()
	}
	switch mode {
	case "handler-panic", "handler-goexit":
		_, btn, h := buttonInFrame(t)
		x, y, w, ht := btn.Rect()
		btn.OnClick(func() {
			if mode == "handler-goexit" {
				t.FailNow()
			}
			panic("boom")
		})
		h.Press(x+w/2, y+ht/2)
		survive(func() { h.Release(x+w/2, y+ht/2) })
	case "wake-panic":
		Do(func() { panic("boom") })
		survive(buiGoWake)
	}
	fmt.Println("SURVIVED")
}

// Finding: a handler panic must end the process even if a caller above the
// abandoned C frames tries to recover, since the kit's state is corrupt by then.
func TestPanicAcrossCFramesExits(t *testing.T) {
	for _, tc := range []struct{ mode, want string }{
		{"handler-panic", "boom"},
		{"handler-goexit", "goroutine "},
		{"wake-panic", "boom"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestChildProcess$", "-test.count=1")
			cmd.Env = append(os.Environ(), "BUI_GO_TEST_CHILD="+tc.mode)
			out, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 2 {
				t.Errorf("child ended with %v, want exit status 2", err)
			}
			if strings.Contains(string(out), "SURVIVED") {
				t.Errorf("child survived:\n%s", out)
			}
			if !strings.Contains(string(out), tc.want) || !strings.Contains(string(out), "goroutine ") {
				t.Errorf("output lacks %q and a stack:\n%s", tc.want, out)
			}
		})
	}
}

// Finding: a panicking Do on the Go stack loses nothing but itself.
func TestDrainPanicKeepsTheRest(t *testing.T) {
	var ran []string
	Do(func() {
		ran = append(ran, "a")
		Do(func() { ran = append(ran, "d") }) // queued while the batch is out
	})
	Do(func() { panic("boom") })
	Do(func() { ran = append(ran, "b") })
	Do(func() {
		ran = append(ran, "c")
		Do(func() { ran = append(ran, "e") })
	})
	mustPanic(t, "drainDo", drainDo)
	if n := pendingDo(); n != 3 {
		t.Fatalf("pending after the panic = %d, want 3", n)
	}
	drainDo()
	drainDo()
	if got := strings.Join(ran, ""); got != "abcde" {
		t.Fatalf("ran %q, want abcde", got)
	}
}

// Finding: a panic in Run's initial drain, recovered by its caller, leaves Run
// usable, and the functions behind it still run.
func TestRunUsableAfterPanicInInitialDrain(t *testing.T) {
	root, _, win := labelWindow(t)
	defer root.Close()
	defer win.Close()
	ran := false
	Do(func() { panic("boom") })
	Do(func() { ran = true })
	mustPanic(t, "Run", func() { _ = Run() })
	if inRun || inLoop {
		t.Fatalf("after the panic inRun=%v inLoop=%v, want both false", inRun, inLoop)
	}
	if ran {
		t.Fatal("the function behind the panic ran during the panic")
	}
	Do(Quit)
	runGuarded(t)
	if !ran {
		t.Fatal("the function behind the panic did not run in the next Run")
	}
}

func TestAddRejectsCycles(t *testing.T) {
	a, b, c := NewNode("frame"), NewNode("frame"), NewNode("frame")
	defer a.Close()
	mustPanic(t, "self-add", func() { a.Add(a) })
	a.Add(b)
	b.Add(c)
	mustPanic(t, "adding a parent to its child", func() { b.Add(a) })
	mustPanic(t, "adding the root to a grandchild", func() { c.Add(a) })
	n := NewNode("label")
	c.Add(n)
	b.Add(n) // reparenting within the tree stays legal
}

// The doc promises this panic: freeing an ancestor frees the root under its
// live context just as closing the root would.
func TestCloseAncestorOfLiveRootPanics(t *testing.T) {
	base := liveHandleCount()
	outer, inner := NewNode("frame"), NewNode("frame")
	middle := NewNode("frame")
	outer.Add(middle)
	middle.Add(inner)
	h := NewHeadless(inner, 100, 100)
	mustPanic(t, "Close of an ancestor", outer.Close)
	mustPanic(t, "Close of the parent", middle.Close)
	h.Close()
	outer.Close()
	if got := liveHandleCount() - base; got != 0 {
		t.Fatalf("live handles = baseline+%d, want baseline", got)
	}
}

// xdotool drives real X input for the TestX tests. They are opt-in: they move
// the pointer of the display they run on, so use xvfb-run (see the file header).
func xdotool(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("xdotool", args...).CombinedOutput()
	if err != nil {
		t.Errorf("xdotool %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// xWindow opens a window holding one child that fills it, once input is opted in.
func xWindow(t *testing.T, typ, title string) (root, child *Node, win *Window) {
	t.Helper()
	if os.Getenv("BUI_TEST_XDOTOOL") != "1" {
		t.Skip("opt-in with BUI_TEST_XDOTOOL=1, on a private display")
	}
	if !Supported() {
		t.Fatal("BUI_TEST_XDOTOOL=1 but no display")
	}
	root = NewNode("frame")
	child = NewNode(typ)
	root.Add(child)
	child.Pack(PackOptions{Fill: FillBoth, Expand: true})
	win, err := NewWindow(root, title, 300, 100)
	if err != nil {
		t.Fatal(err)
	}
	win.Show()
	return root, child, win
}

// xWhenLaidOut calls f on its own goroutine with n's rect and the window id,
// once the loop has rendered.
func xWhenLaidOut(t *testing.T, n *Node, title string, f func(id string, x, y, w, h int)) {
	var try func()
	try = func() {
		x, y, w, h := n.Rect()
		if w <= 0 {
			go func() {
				time.Sleep(20 * time.Millisecond)
				Do(try)
			}()
			return
		}
		go f(xdotool(t, "search", "--name", "^"+title+"$"), x, y, w, h)
	}
	whenInLoop(try)
}

func xClick(t *testing.T, n *Node, title string) {
	xWhenLaidOut(t, n, title, func(id string, x, y, w, h int) {
		xdotool(t, "mousemove", "--window", id, strconv.Itoa(x+w/2), strconv.Itoa(y+h/2))
		time.Sleep(50 * time.Millisecond)
		xdotool(t, "click", "1")
	})
}

// A real click: the handler runs from an X event inside BK_window_run and closes
// everything. Only the wake can then free it, and that alone must end Run.
func TestXClickClosesWindowAndRoot(t *testing.T) {
	const title = "bui-x-close-all"
	root, btn, win := xWindow(t, "button", title)
	fired, depth, closedInside := 0, 0, true
	btn.OnClick(func() {
		fired++
		depth = emitDepth
		win.Close()
		root.Close()
		closedInside = root.closed()
	})
	xClick(t, btn, title)
	runGuarded(t)
	if fired != 1 || depth != 1 || closedInside {
		t.Fatalf("fired=%d depth=%d closedInside=%v, want 1, 1, false", fired, depth, closedInside)
	}
	if win.c != nil || !root.closed() {
		t.Fatalf("window freed=%v root closed=%v, want both", win.c == nil, root.closed())
	}
}

// A real click, handler closes the window and the root follows in a Do: the
// inversion the review found, on the real path.
func TestXClickWindowThenRootInDo(t *testing.T) {
	const title = "bui-x-order"
	root, btn, win := xWindow(t, "button", title)
	windowGone := false
	btn.OnClick(func() {
		win.Close()
		Do(func() {
			windowGone = win.c == nil
			root.Close()
		})
	})
	xClick(t, btn, title)
	runGuarded(t)
	if !windowGone || !root.closed() {
		t.Fatalf("window gone when root closed=%v, root closed=%v, want both", windowGone, root.closed())
	}
}

// A real click, handler closes the button and quits: the final drain's job.
func TestXClickClosesThenQuits(t *testing.T) {
	const title = "bui-x-quit"
	root, btn, win := xWindow(t, "button", title)
	defer root.Close()
	defer win.Close()
	btn.OnClick(func() {
		btn.Close()
		Quit()
	})
	xClick(t, btn, title)
	runGuarded(t)
	if !btn.closed() {
		t.Fatal("button not freed by the time Run returned")
	}
}

// A real drag: the slider closes itself in its first OnChange with motion events
// still arriving and the pointer grabbed. It must stay silent, the loop must
// survive the grab's target going away, and later input must be harmless.
func TestXDragClosesSlider(t *testing.T) {
	const title = "bui-x-drag"
	root, s, win := xWindow(t, "slider", title)
	defer root.Close()
	defer win.Close()
	if err := s.SetNum("max", 10); err != nil {
		t.Fatal(err)
	}
	fired := 0
	s.OnChange(func(float64) {
		fired++
		s.Close()
	})
	xWhenLaidOut(t, s, title, func(id string, x, y, w, h int) {
		cy := strconv.Itoa(y + h/2)
		xdotool(t, "mousemove", "--window", id, strconv.Itoa(x+20), cy, "mousedown", "1")
		for i := range 10 {
			xdotool(t, "mousemove", "--window", id, strconv.Itoa(x+40+i*20), cy)
		}
		xdotool(t, "mouseup", "1")
		xdotool(t, "mousemove", "--window", id, "5", "5", "click", "1")
		time.Sleep(50 * time.Millisecond)
		Do(Quit)
	})
	runGuarded(t)
	if fired != 1 || !s.closed() {
		t.Fatalf("fired=%d closed=%v, want 1 and true", fired, s.closed())
	}
}
