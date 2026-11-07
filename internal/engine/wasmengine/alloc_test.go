// SPDX-License-Identifier: GPL-2.0-or-later

package wasmengine

import (
	"strings"
	"testing"

	"github.com/CWBudde/bui-go/internal/engine"
)

// The calls a window makes per event or frame must not allocate per call: a moving
// pointer makes one per X event. RasterDamage has to build its result, and only Go
// strings cross back.
func TestHotCallsDoNotAllocate(t *testing.T) {
	if asanBuild {
		t.Skip("-asan's instrumentation allocates")
	}

	m := loadForTest(t, options{cacheDir: sharedCache, memoryLimitPages: memoryLimitPages})
	k := Kit{m: m}

	root := newNode(t, k, "frame", 0)
	button := newNode(t, k, "button", 1)
	addPacked(t, k, root, button)

	ctx, r := attach(t, k, root)
	x, y := center(k, button)

	for _, tc := range []struct {
		name string
		f    func()
	}{
		{"Motion", func() { k.Motion(ctx, x, y, 0) }},
		{"PressRelease", func() { clickAt(k, ctx, x, y) }},
		{"Scroll", func() { k.Scroll(ctx, x, y, 0, 1, 0) }},
		{"Key", func() { k.Key(ctx, "a", "a", 0) }},
		{"KeyRelease", func() { k.KeyRelease(ctx, "a", 0) }},
		{"Timer", func() { k.Timer(ctx, -2) }},
		{"RenderDamage", func() { k.RenderDamage(ctx, r, kitW, kitH, false) }},
		{"NeedsRender", func() { k.NeedsRender(ctx) }},
		{"Animating", func() { k.Animating(ctx) }},
		{"RasterView", func() { k.RasterView(r) }},
		{"SetNum", func() { k.SetNum(button, "padding", 1) }},
		{"SetStr", func() { k.SetStr(button, "text", "x") }},
		{"GetNum", func() { k.GetNum(button, "padding") }},
		{"NodeRect", func() { k.NodeRect(button) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A run warms the levels and buffers, which stay.
			tc.f()

			if n := testing.AllocsPerRun(100, tc.f); n != 0 {
				t.Errorf("%v allocations per call", n)
			}
		})
	}
}

// A click handler comes in through the module and back: emit, handler, a call from it.
func TestEmitDoesNotAllocate(t *testing.T) {
	if asanBuild {
		t.Skip("-asan's instrumentation allocates")
	}

	m := loadForTest(t, options{cacheDir: sharedCache, memoryLimitPages: memoryLimitPages})
	k := Kit{m: m}

	root := newNode(t, k, "frame", 0)
	button := newNode(t, k, "button", 1)
	addPacked(t, k, root, button)

	ctx, _ := attach(t, k, root)
	x, y := center(k, button)

	k.SetEmit(func(uint32, int, float64) { k.SetNum(button, "padding", 1) })
	defer k.SetEmit(nil)

	clickAt(k, ctx, x, y)

	if n := testing.AllocsPerRun(100, func() { clickAt(k, ctx, x, y) }); n != 0 {
		t.Errorf("%v allocations per click", n)
	}
}

// memoryAfterWarmup runs cycle until the module's memory settles, then many more times,
// and fails if it grew. Linear memory never shrinks, so growth is all a test can see, and
// the cycle has to outrun a page's worth of slack in the allocator: small leaks need long
// strings to show, as TestKnownLeakOfUndoHistory found out.
func memoryAfterWarmup(t *testing.T, m *module, warmup, runs int, cycle func()) {
	t.Helper()

	for range warmup {
		cycle()
	}

	before := m.mod.Memory().Size()

	for range runs {
		cycle()
	}

	if after := m.mod.Memory().Size(); after != before {
		t.Errorf("memory grew from %d to %d bytes over %d cycles: %d bytes per cycle",
			before, after, runs, (after-before)/uint32(runs))
	}
}

func TestNoLeakFromCreateAndClose(t *testing.T) {
	m := loadForTest(t, options{cacheDir: sharedCache, memoryLimitPages: memoryLimitPages})
	k := Kit{m: m}

	k.SetEmit(func(uint32, int, float64) {})

	host := leakHost{}

	cycle := func() {
		root := k.NodeNew("frame")
		button := k.NodeNew("button")
		slider := k.NodeNew("slider")
		text := k.NodeNew("text")

		for i, n := range []engine.Node{button, slider, text} {
			k.NodeSetID(n, uint32(i)+1)
			k.NodeAdd(root, n)
			k.GeomSetPack(n)
			k.GeomSet(n, "fill", 1)
		}

		k.SetStr(button, "text", "A button with some text")
		k.SetStr(button, "description", "A tooltip with some text")
		k.SetStr(text, "text", "Text to edit")
		k.SetNum(slider, "max", 10)

		ctx := k.ContextNew(root)
		k.ContextSetWindowHost(ctx, host)

		r := k.RasterNew(320, 200)
		k.Render(ctx, r, 320, 200)

		x, y := center(k, button)
		k.Motion(ctx, x, y, 0)
		clickAt(k, ctx, x, y)

		x, y = center(k, text)
		clickAt(k, ctx, x, y)
		k.Key(ctx, "a", "a", 0)
		k.Key(ctx, "", "Return", 0)
		k.RenderDamage(ctx, r, 320, 200, false)

		k.ContextSetEmitHost(ctx)
		k.RasterFree(r)
		k.ContextFree(ctx)

		for _, n := range []engine.Node{text, slider, button, root} {
			k.NodeFree(n)
		}
	}

	memoryAfterWarmup(t, m, 200, 2000, cycle)

	if n := len(m.windowHosts); n != 0 {
		t.Errorf("%d window hosts left in the table", n)
	}

	if len(m.levels) > 3 {
		t.Errorf("%d levels from calls that never nest that deep", len(m.levels))
	}
}

// A window being dragged larger and smaller reallocates its raster every time, and that
// must reuse the memory it gave back. The peak stays, as linear memory never shrinks.
func TestResizeLoopReusesMemory(t *testing.T) {
	m := loadForTest(t, options{cacheDir: sharedCache, memoryLimitPages: memoryLimitPages})
	k := Kit{m: m}

	root := newNode(t, k, "frame", 0)
	button := newNode(t, k, "button", 1)
	addPacked(t, k, root, button)

	ctx, r := attach(t, k, root)

	const maxSide = 1200

	start := m.mod.Memory().Size()

	sizes := func() {
		for side := 100; side <= maxSide; side += 100 {
			k.RenderDamage(ctx, r, side, side*3/4, true)
		}

		for side := maxSide; side >= 100; side -= 100 {
			k.RenderDamage(ctx, r, side, side*3/4, true)
		}
	}

	memoryAfterWarmup(t, m, 2, 20, sizes)

	// The peak holds at least the largest raster.
	if size := m.mod.Memory().Size(); size < start && size < 4*maxSide*maxSide*3/4 {
		t.Errorf("memory is %d bytes after a %d-wide raster", size, maxSide)
	}

	k.RenderDamage(ctx, r, 10, 10, true)

	if got := len(k.RasterView(r)); got != 4*10*10 {
		t.Errorf("a 10 x 10 raster views %d bytes", got)
	}
}

// leakHost is a window host that does nothing.
type leakHost struct{}

func (leakHost) Request()                     {}
func (leakHost) SetCursor(int)                {}
func (leakHost) Warp(int, int)                {}
func (leakHost) ClipboardGet() (string, bool) { return "", false }
func (leakHost) ClipboardSet(string)          {}
func (leakHost) Schedule(int, int)            {}

// The kit keeps a copy of a text field's old text in the context's undo history, and
// BK_context_free does not release it (PLAN.md, Phase 6 notes). It is the kit's own code,
// and it costs that text once per context closed after an edit. A long string shows it.
// This pins the cost, and if a kit update fixes it this fails, so that the note can go.
func TestKnownLeakOfUndoHistory(t *testing.T) {
	m := loadForTest(t, options{cacheDir: sharedCache, memoryLimitPages: memoryLimitPages})
	k := Kit{m: m}

	k.SetEmit(func(uint32, int, float64) {})

	const size, cycles = 4096, 200

	long := strings.Repeat("x", size)

	cycle := func(edit bool) {
		root := k.NodeNew("frame")
		text := k.NodeNew("text")
		k.NodeSetID(text, 1)
		k.NodeAdd(root, text)
		k.GeomSetPack(text)
		k.GeomSet(text, "fill", 1)
		k.SetStr(text, "text", long)

		ctx := k.ContextNew(root)
		k.ContextSetEmitHost(ctx)

		r := k.RasterNew(kitW, kitH)
		k.Render(ctx, r, kitW, kitH)

		if edit {
			x, y := center(k, text)
			clickAt(k, ctx, x, y)
			k.Key(ctx, "a", "a", 0)
			k.Key(ctx, "", "Return", 0)
		}

		k.ContextFree(ctx)
		k.RasterFree(r)
		k.NodeFree(text)
		k.NodeFree(root)
	}

	// Without an edit there is no history, and nothing grows.
	memoryAfterWarmup(t, m, 50, cycles, func() { cycle(false) })

	before := m.mod.Memory().Size()

	for range cycles {
		cycle(true)
	}

	grew := m.mod.Memory().Size() - before
	t.Logf("memory grew %d bytes over %d contexts closed after editing a %d-byte string", grew, cycles, size)

	if grew < size*cycles/2 {
		t.Errorf("memory grew only %d bytes: the kit's leak looks fixed", grew)
	}
}
