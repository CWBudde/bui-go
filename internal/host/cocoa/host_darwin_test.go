// SPDX-License-Identifier: GPL-2.0-or-later

package cocoa

import (
	"bytes"
	"fmt"
	"os"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/CWBudde/bui-go/internal/engine"
	"github.com/CWBudde/bui-go/internal/engine/wasmengine"
	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

// Tests run on goroutines of their own, and AppKit only works on the main thread, so
// that thread serves them.
var mainQueue = make(chan func())

func init() { runtime.LockOSThread() }

func TestMain(m *testing.M) {
	code := make(chan int, 1)

	go func() {
		code <- m.Run()

		close(mainQueue)
	}()

	for f := range mainQueue {
		f()
	}

	os.Exit(<-code)
}

// onMain runs f on the main thread. f must not call Fatal, Skip or anything else that ends
// its goroutine: that goroutine serves mainQueue, so every later test would hang. Report
// with Error and return, and leave the rest to the test's own goroutine.
func onMain(f func()) {
	done := make(chan struct{})

	mainQueue <- func() {
		defer close(done)

		f()
	}

	<-done
}

type fixture struct {
	k    wasmengine.Kit
	h    *Host
	root engine.Node
	ctx  engine.Context
}

// requireDisplay skips the test without a GUI session. It runs on the test's goroutine.
func requireDisplay(tb testing.TB) {
	tb.Helper()

	var supported bool

	onMain(func() { supported = New(wasmengine.Kit{}).Supported() })

	if !supported {
		tb.Skip("no display")
	}
}

// newFixture makes a tree with something at the top and at the bottom of the window, so
// that flipped frames differ from right ones. It runs on the main thread.
func newFixture() *fixture { return newFixtureOf("top", "middle", "bottom") }

// newFixtureOf makes a frame with a button of each text in it.
func newFixtureOf(texts ...string) *fixture {
	k := wasmengine.Kit{}
	k.SetEmit(func(uint32, int, float64) {})

	h := New(k)
	h.noActivate = true
	h.buttons = func() uint64 { return 0 }

	root := k.NodeNew("frame")
	k.GeomSetPack(root)

	for _, text := range texts {
		n := k.NodeNew("button")
		k.SetStr(n, "text", text)
		k.NodeAdd(root, n)
		k.GeomSetPack(n)
	}

	ctx := k.ContextNew(root)
	k.ContextSetEmitHost(ctx)

	return &fixture{k: k, h: h, root: root, ctx: ctx}
}

func (f *fixture) free() {
	f.k.ContextFree(f.ctx)
	f.k.NodeFree(f.root)
}

var selOcclusionState = objc.RegisterName("occlusionState")

// occlusionVisible is NSWindowOcclusionStateVisible.
const occlusionVisible = 1 << 1

// waitOnScreen pumps until the window server reports part of the window visible. AppKit drops
// mouse events that reach a window before then, as no hand could click it yet. A fast Mac
// rarely shows that gap, but GitHub's macOS runner lost most clicks sent right after a show.
func (f *fixture) waitOnScreen(tb testing.TB, win *window) {
	tb.Helper()

	deadline := time.Now().Add(5 * time.Second)

	for objc.Send[uint64](win.nswin, selOcclusionState)&occlusionVisible == 0 {
		if time.Now().After(deadline) {
			tb.Error("the window never came on screen")

			return
		}

		// Events and notifications about the window are autoreleased, and nothing else drains
		// them outside the loop: the window would outlive WindowFree.
		pool := poolPush()

		f.h.pump(clsNSDate.Send(selDateSinceNow, 0.02))

		poolPop(pool)
	}
}

// A window opens with no error from AppKit, and its raster has backing-scale pixels.
func TestWindowSizeIsInBackingPixels(t *testing.T) {
	requireDisplay(t)

	onMain(func() {
		f := newFixture()
		defer f.free()

		id := f.h.WindowNew(f.ctx, "bui cocoa test", 400, 300)
		defer f.h.WindowFree(id)

		win := f.h.find(id)
		f.h.WindowShow(id)
		win.render()

		t.Logf("px %v, raster %d x %d", win.px, win.rasterW, win.rasterH)

		if want := int(400 * win.px); win.rasterW != want {
			t.Errorf("raster width %d, want %d", win.rasterW, want)
		}

		if want := int(300 * win.px); win.rasterH != want {
			t.Errorf("raster height %d, want %d", win.rasterH, want)
		}
	})
}

// Core Animation holds the raster's pixels, the right way up: the layer is drawn into a
// bitmap, which needs no permission, unlike a screenshot.
func TestLayerHoldsTheRaster(t *testing.T) {
	requireDisplay(t)

	onMain(func() {
		f := newFixture()
		defer f.free()

		id := f.h.WindowNew(f.ctx, "bui cocoa test", 400, 300)
		defer f.h.WindowFree(id)

		win := f.h.find(id)
		f.h.WindowShow(id)
		win.render()

		want := append([]byte(nil), f.k.RasterView(win.raster)...)

		got := layerPixels(t, win)
		if got == nil {
			return
		}

		differ, distinct := 0, map[uint32]bool{}

		for i := 0; i+pixelSize <= len(want); i += pixelSize {
			w := uint32(want[i]) | uint32(want[i+1])<<8 | uint32(want[i+2])<<16
			distinct[w] = true

			if want[i] != got[i] || want[i+1] != got[i+1] || want[i+2] != got[i+2] {
				differ++
			}
		}

		if len(distinct) < 3 {
			t.Errorf("the raster has only %d colours, so this proves nothing", len(distinct))

			return
		}

		if differ != 0 {
			t.Errorf("%d of %d pixels differ", differ, len(want)/pixelSize)
		}

		// The same comparison has to notice a frame the wrong way up.
		flipped, stride := 0, win.rasterW*pixelSize

		for y := range win.rasterH {
			mirror := (win.rasterH - 1 - y) * stride
			if !bytes.Equal(want[y*stride:(y+1)*stride], got[mirror:mirror+stride]) {
				flipped++
			}
		}

		if flipped == 0 {
			t.Error("the frame looks the same flipped, so this proves nothing about which way up it is")
		}

		t.Logf("%d pixels, %d colours, %d differ, %d of %d rows differ when flipped",
			len(want)/pixelSize, len(distinct), differ, flipped, win.rasterH)
	})
}

func layerPixels(t *testing.T, win *window) []byte {
	t.Helper()

	var (
		create  func(data uintptr, w, h, bits, stride int, space uintptr, info uint32) uintptr
		getData func(uintptr) unsafe.Pointer
		scale   func(ctx uintptr, sx, sy float64)
	)

	for name, fn := range map[string]any{
		"CGBitmapContextCreate":  &create,
		"CGBitmapContextGetData": &getData,
		"CGContextScaleCTM":      &scale,
	} {
		p, err := purego.Dlsym(purego.RTLD_DEFAULT, name)
		if err != nil {
			t.Error(err)

			return nil
		}

		purego.RegisterFunc(fn, p)
	}

	stride := win.rasterW * pixelSize

	ctx := create(0, win.rasterW, win.rasterH, 8, stride, srgbSpace, bitmapInfo)
	if ctx == 0 {
		t.Error("no bitmap context")

		return nil
	}
	defer cfRelease(ctx)

	scale(ctx, win.px, win.px)
	win.layer.Send(objc.RegisterName("renderInContext:"), ctx)

	return append([]byte(nil), unsafe.Slice((*byte)(getData(ctx)), stride*win.rasterH)...)
}

// A Wake from another goroutine ends a wait the loop is really blocked in, every time, and
// a collection goes through meanwhile: AppKit blocking the main thread must not hold up the
// runtime. Each round trip waits for the drain before the next, so none can share a wake.
func TestWakeEndsABlockedWaitWithGC(t *testing.T) {
	const (
		rounds  = 500
		senders = 8
		burst   = 2000
	)

	var (
		f      *fixture
		id     engine.Window
		drains atomic.Int64
		ending atomic.Bool
		drain  = make(chan struct{}, 1)
	)

	requireDisplay(t)

	onMain(func() {
		f = newFixture()
		id = f.h.WindowNew(f.ctx, "bui cocoa test", 200, 100)
		f.h.WindowShow(id)
		f.h.WakeInit(func() {
			drains.Add(1)

			if ending.Load() {
				f.h.Quit()
			}

			select {
			case drain <- struct{}{}:
			default:
			}
		})
	})

	finished := make(chan struct{})

	go func() {
		defer close(finished)

		onMain(func() {
			_ = f.h.Run()
			f.h.WindowFree(id)
			f.free()
		})
	}()

	// Long enough for the first render, so that the loop is waiting with nothing due.
	time.Sleep(200 * time.Millisecond)

	var total, worst time.Duration

	collections := 0

	for i := range rounds {
		if i%20 == 0 {
			runtime.GC()

			collections++
		}

		time.Sleep(time.Millisecond)

		start := time.Now()

		f.h.Wake()

		select {
		case <-drain:
		case <-time.After(10 * time.Second):
			t.Fatalf("round %d: the wake did not reach the blocked loop", i)
		}

		d := time.Since(start)
		total += d
		worst = max(worst, d)
	}

	t.Logf("%d round trips, mean %v, worst %v, %d collections", rounds, total/rounds, worst, collections)

	// A burst never blocks a sender, and shares events rather than queueing one each.
	var sent sync.WaitGroup

	var slowest atomic.Int64

	for range senders {
		sent.Go(func() {
			for range burst {
				start := time.Now()

				f.h.Wake()

				if d := int64(time.Since(start)); d > slowest.Load() {
					slowest.Store(d)
				}
			}
		})
	}

	sent.Wait()
	t.Logf("%d wakes in a burst, slowest %v, %d drains so far", senders*burst, time.Duration(slowest.Load()), drains.Load())

	// Quit is UI-thread only, so it goes through the wake as Do(Quit) does.
	ending.Store(true)
	f.h.Wake()

	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("the loop did not end")
	}
}

// A window closed the way its button does reaches the delegate, written in Go, and ends Run.
func TestCloseEndsRun(t *testing.T) {
	requireDisplay(t)

	onMain(func() {
		f := newFixture()
		defer f.free()

		id := f.h.WindowNew(f.ctx, "bui cocoa test", 200, 100)
		defer f.h.WindowFree(id)

		win := f.h.find(id)
		f.h.WindowShow(id)
		f.h.WakeInit(func() { win.nswin.Send(selPerformClose, objc.ID(0)) })
		f.h.Wake()

		if err := f.h.Run(); err != nil {
			t.Error(err)
		}

		if win.nswin != 0 || f.h.open != 0 {
			t.Errorf("the window is still open: %v, %d", win.nswin, f.h.open)
		}
	})
}

// A loop nested in a callback can take the event that a Wake posted, as AppKit's modal and
// tracking loops do. The next wait must not block on it, and later Wakes must still post.
func TestWakeEatenByANestedLoop(t *testing.T) {
	requireDisplay(t)

	var rescued atomic.Bool

	onMain(func() {
		f := newFixture()
		defer f.free()

		id := f.h.WindowNew(f.ctx, "bui cocoa test", 200, 100)
		defer f.h.WindowFree(id)

		f.h.WindowShow(id)

		drains := 0

		f.h.WakeInit(func() {
			drains++

			switch drains {
			case 1:
				// Force the post: a wake still marked as posted wouldn't queue an event.
				f.h.wakePosted.Store(false)
				f.h.Wake()

				if !eatWake(f.h) {
					t.Error("no wake event to eat")
				}
			case 2:
				go f.h.Wake()
			default:
				f.h.Quit()
			}
		})

		// Without this the test would hang when the wait does block.
		stop := make(chan struct{})
		defer close(stop)

		go func() {
			tick := time.NewTicker(3 * time.Second)
			defer tick.Stop()

			for {
				select {
				case <-stop:
					return
				case <-tick.C:
					rescued.Store(true)
					f.h.wakePosted.Store(false)
					f.h.Wake()
				}
			}
		}()

		// Later than the launch's own events, so that nothing else can end the wait.
		time.AfterFunc(time.Second, f.h.Wake)

		if err := f.h.Run(); err != nil {
			t.Error(err)
		}
	})

	if rescued.Load() {
		t.Error("the loop blocked until the test posted a wake itself")
	}
}

// eatWake takes the wake event off the queue the way a nested loop would.
func eatWake(h *Host) bool {
	for {
		ev := h.app.Send(selNextEvent, eventMaskAny, objc.ID(0), nsDefaultRunLoopMode, true)
		if ev == 0 {
			return false
		}

		if isWake(ev) {
			return true
		}

		h.app.Send(selSendEvent, ev)
	}
}

// Every NSWindow goes away with WindowFree, closed by the host or closed before that the
// way its button does, and so do its layer and its delegate. An object only the watched
// object holds shows when it has.
func TestWindowFreeReleasesTheWindow(t *testing.T) {
	requireDisplay(t)

	for _, tc := range []struct {
		name        string
		closedFirst bool
	}{{"open", false}, {"closed first", true}} {
		t.Run(tc.name, func(t *testing.T) {
			onMain(func() {
				f := newFixture()
				defer f.free()

				id := f.h.WindowNew(f.ctx, "bui cocoa test", 200, 100)
				win := f.h.find(id)
				f.h.WindowShow(id)

				watched := []struct {
					name string
					obj  objc.ID
				}{{"NSWindow", win.nswin}, {"layer", win.layer}, {"delegate", win.delegate}}
				weaks := make([]*deallocWatch, len(watched))

				for i, w := range watched {
					if weaks[i] = watchDealloc(t, w.obj); weaks[i] == nil {
						f.h.WindowFree(id)

						return
					}
				}

				if tc.closedFirst {
					pool := poolPush()

					win.nswin.Send(selClose)
					poolPop(pool)
				}

				f.h.WindowFree(id)

				for i, w := range watched {
					if !weaks[i].gone(f.h, 2*time.Second) {
						t.Errorf("the %s is still alive", w.name)
					}
				}
			})
		})
	}
}

// deallocWatch holds a weak reference to an object owned by the object being watched.
type deallocWatch struct {
	slot *objc.ID
	load func(*objc.ID) objc.ID
}

func watchDealloc(t *testing.T, owner objc.ID) *deallocWatch {
	t.Helper()

	var (
		associate func(owner objc.ID, key unsafe.Pointer, value objc.ID, policy uintptr)
		store     func(slot *objc.ID, value objc.ID) objc.ID
		load      func(slot *objc.ID) objc.ID
	)

	for name, fn := range map[string]any{
		"objc_setAssociatedObject": &associate,
		"objc_storeWeak":           &store,
		"objc_loadWeak":            &load,
	} {
		p, err := purego.Dlsym(purego.RTLD_DEFAULT, name)
		if err != nil {
			t.Error(err)

			return nil
		}

		purego.RegisterFunc(fn, p)
	}

	const retainNonatomic = 1

	pool := poolPush()
	defer poolPop(pool)

	sentinel := objc.ID(objc.GetClass("NSObject")).Send(selAlloc).Send(selInit)
	slot := new(objc.ID)
	store(slot, sentinel)
	associate(owner, unsafe.Pointer(slot), sentinel, retainNonatomic)
	sentinel.Send(selRelease)

	return &deallocWatch{slot: slot, load: load}
}

// gone turns the event loop for a while, as AppKit may let go of a closed window later.
func (w *deallocWatch) gone(h *Host, patience time.Duration) bool {
	deadline := time.Now().Add(patience)

	for {
		pool := poolPush()

		h.pump(objc.ID(0))

		alive := w.load(w.slot) != 0

		poolPop(pool)

		if !alive {
			return true
		}

		if time.Now().After(deadline) {
			return false
		}

		time.Sleep(10 * time.Millisecond)
	}
}

// A resize reaches the delegate and the next render.
func TestResizeReachesTheRaster(t *testing.T) {
	requireDisplay(t)

	onMain(func() {
		f := newFixture()
		defer f.free()

		id := f.h.WindowNew(f.ctx, "bui cocoa test", 200, 100)
		defer f.h.WindowFree(id)

		win := f.h.find(id)
		f.h.WindowShow(id)
		win.render()

		win.nswin.Send(objc.RegisterName("setContentSize:"), nsSize{320, 240})
		win.render()

		if want := int(320 * win.px); win.rasterW != want {
			t.Errorf("raster width %d after a resize, want %d", win.rasterW, want)
		}

		if got := layerPixels(t, win); len(got) != win.rasterW*win.rasterH*pixelSize {
			t.Errorf("%d bytes from the layer", len(got))
		}
	})
}

// A burst longer than a turn's budget loses no event: what one turn leaves, the next takes.
// A lost release would leave the button held. The release comes right after the budget.
func TestEventsPastTheBudgetAreKept(t *testing.T) {
	requireDisplay(t)

	onMain(func() {
		f := openInput(t, "button", 200, 80)
		defer f.close()

		spy := &buttonSpy{Kit: f.h.kit}
		f.h.kit = spy

		defer func() { f.h.kit = spy.Kit }()

		pool := poolPush()
		defer poolPop(pool)

		// Nothing else may be queued ahead of the burst, or it would shift the release.
		for range 100 {
			if f.h.app.Send(selNextEvent, eventMaskAny, objc.ID(0), nsDefaultRunLoopMode, false) == 0 {
				break
			}

			f.h.pump(objc.ID(0))
		}

		// Filler: application-defined events that aren't the wake's, which AppKit passes over.
		for range maxEventsPerTurn - 1 {
			filler := clsNSEvent.Send(selOtherEvent, uint64(eventTypeAppDefined), nsPoint{}, uint64(0), float64(0),
				int64(0), objc.ID(0), int16(0), int64(0), int64(0))
			f.h.app.Send(selPostEvent, filler, false)
		}

		x, y := f.center()
		window := objc.Send[int64](f.win.nswin, selWindowNumber)

		for _, kind := range []uint64{eventLeftDown, eventLeftUp} {
			ev := clsNSEvent.Send(selMouseEvent, kind, f.windowPoint(x, y), uint64(0), f.now(), window, objc.ID(0),
				int64(0), int64(1), float32(1))
			f.h.app.Send(selPostEvent, ev, false)
		}

		f.h.pump(objc.ID(0))
		f.h.pump(objc.ID(0))

		want := []string{fmt.Sprintf("press %d", engine.ModButton1), "release 0"}
		if !slices.Equal(spy.calls, want) {
			t.Errorf("calls %q, want %q", spy.calls, want)
		}
	})
}

// present as the X11 host's benchmark does, on the same pixel counts. Core Animation's
// flush hands the frame to the window server, which draws it later.
func benchmarkPresent(b *testing.B, width, height int) {
	b.Helper()

	requireDisplay(b)

	onMain(func() {
		f := newFixture()
		defer f.free()

		id := f.h.WindowNew(f.ctx, "bui cocoa bench", 1, 1)
		defer f.h.WindowFree(id)

		win := f.h.find(id)
		win.nswin.Send(objc.RegisterName("setContentSize:"), nsSize{float64(width) / win.px, float64(height) / win.px})
		f.h.WindowShow(id)
		win.render()

		if win.rasterW != width || win.rasterH != height {
			b.Errorf("raster %d x %d, want %d x %d", win.rasterW, win.rasterH, width, height)

			return
		}

		for b.Loop() {
			pool := poolPush()

			win.present()
			poolPop(pool)
		}
	})
}

func BenchmarkPresentCounter(b *testing.B) { benchmarkPresent(b, 400, 300) }
func BenchmarkPresentFullHD(b *testing.B)  { benchmarkPresent(b, 1920, 1080) }
