// SPDX-License-Identifier: GPL-2.0-or-later

package cocoa

import (
	"bytes"
	"os"
	"runtime"
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

// newFixture makes a tree with something at the top and at the bottom of the window, so
// that flipped frames differ from right ones. It runs on the main thread.
func newFixture(tb testing.TB) *fixture {
	tb.Helper()

	k := wasmengine.Kit{}
	k.SetEmit(func(uint32, int, float64) {})

	h := New(k)
	if !h.Supported() {
		tb.Skip("no display")
	}

	root := k.NodeNew("frame")
	k.GeomSetPack(root)

	for _, text := range []string{"top", "middle", "bottom"} {
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

// A window opens with no error from AppKit, and its raster has backing-scale pixels.
func TestWindowSizeIsInBackingPixels(t *testing.T) {
	onMain(func() {
		f := newFixture(t)
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
	onMain(func() {
		f := newFixture(t)
		defer f.free()

		id := f.h.WindowNew(f.ctx, "bui cocoa test", 400, 300)
		defer f.h.WindowFree(id)

		win := f.h.find(id)
		f.h.WindowShow(id)
		win.render()

		want := append([]byte(nil), f.k.RasterView(win.raster)...)
		got := layerPixels(t, win)

		differ, distinct := 0, map[uint32]bool{}

		for i := 0; i+pixelSize <= len(want); i += pixelSize {
			w := uint32(want[i]) | uint32(want[i+1])<<8 | uint32(want[i+2])<<16
			distinct[w] = true

			if want[i] != got[i] || want[i+1] != got[i+1] || want[i+2] != got[i+2] {
				differ++
			}
		}

		if len(distinct) < 3 {
			t.Fatalf("the raster has only %d colours, so this proves nothing", len(distinct))
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
			t.Fatal(err)
		}

		purego.RegisterFunc(fn, p)
	}

	stride := win.rasterW * pixelSize

	ctx := create(0, win.rasterW, win.rasterH, 8, stride, srgbSpace, bitmapInfo)
	if ctx == 0 {
		t.Fatal("no bitmap context")
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

	onMain(func() {
		f = newFixture(t)
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

	if f == nil {
		return
	}

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
	onMain(func() {
		f := newFixture(t)
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

// A resize reaches the delegate and the next render.
func TestResizeReachesTheRaster(t *testing.T) {
	onMain(func() {
		f := newFixture(t)
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

// present as the X11 host's benchmark does, on the same pixel counts. Core Animation's
// flush hands the frame to the window server, which draws it later.
func benchmarkPresent(b *testing.B, width, height int) {
	b.Helper()

	onMain(func() {
		f := newFixture(b)
		defer f.free()

		id := f.h.WindowNew(f.ctx, "bui cocoa bench", 1, 1)
		defer f.h.WindowFree(id)

		win := f.h.find(id)
		win.nswin.Send(objc.RegisterName("setContentSize:"), nsSize{float64(width) / win.px, float64(height) / win.px})
		f.h.WindowShow(id)
		win.render()

		if win.rasterW != width || win.rasterH != height {
			b.Fatalf("raster %d x %d, want %d x %d", win.rasterW, win.rasterH, width, height)
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
