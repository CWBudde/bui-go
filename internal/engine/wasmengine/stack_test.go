// SPDX-License-Identifier: GPL-2.0-or-later

package wasmengine

import (
	"bytes"
	"encoding/binary"
	"io"
	"strconv"
	"testing"

	"github.com/CWBudde/bui-go/internal/engine"
)

// The module's stack is measured from here, so that it needs no exports for it. With
// --stack-first the stack is [0, top) at the bottom of memory and grows down, and top
// is where the stack pointer rests. Between export calls no frame is live, so all of
// that may be overwritten.
const (
	// stackPattern fills the unused stack.
	stackPattern = "\xDE\xC0\xFE\x5A"
	// stackTopWindow is how close to top the workload's first frames must land.
	stackTopWindow = 1024
	// minStackPeak is less than any render takes. Below it, the measuring is broken.
	minStackPeak = 1024
)

const (
	wasmHeaderSize    = 8
	wasmSectionGlobal = 6
	wasmTypeI32       = 0x7f
	wasmMutable       = 1
	wasmOpI32Const    = 0x41
)

// initialStackPointer reads where the stack starts, from the module's first global.
// wasm-ld defines __stack_pointer first. It is not exported, and wazero reaches only
// exported globals, so the value comes from the binary.
func initialStackPointer(t *testing.T, bin []byte) uint32 {
	t.Helper()

	r := bytes.NewReader(bin[wasmHeaderSize:])

	uvarint := func() uint64 {
		t.Helper()

		v, err := binary.ReadUvarint(r)
		if err != nil {
			t.Fatalf("reading the module: %v", err)
		}

		return v
	}

	for {
		id, err := r.ReadByte()
		if err != nil {
			t.Fatalf("looking for the global section: %v", err)
		}

		size := uvarint()

		if id != wasmSectionGlobal {
			if _, err := r.Seek(int64(size), io.SeekCurrent); err != nil {
				t.Fatal(err)
			}

			continue
		}

		uvarint() // the count of globals

		head := make([]byte, 3)
		if _, err := io.ReadFull(r, head); err != nil {
			t.Fatal(err)
		}

		if head[0] != wasmTypeI32 || head[1] != wasmMutable || head[2] != wasmOpI32Const {
			t.Fatalf("the first global is not a stack pointer: it starts % x", head)
		}

		// The constant is a signed LEB128. A pointer is not negative, and then the
		// unsigned reading gives the same value.
		return uint32(uvarint())
	}
}

// paintStack fills the whole stack with the pattern. No export call may be in progress.
func paintStack(t *testing.T, m *module, top uint32) {
	t.Helper()

	stack, ok := m.mod.Memory().Read(0, top)
	if !ok {
		t.Fatalf("the stack's %d bytes are out of range", top)
	}

	for i := range stack {
		stack[i] = stackPattern[i%len(stackPattern)]
	}
}

// stackUse finds the lowest and the highest overwritten address since paintStack.
// lowest == top means that nothing was.
func stackUse(t *testing.T, m *module, top uint32) (lowest, highest uint32) {
	t.Helper()

	stack, ok := m.mod.Memory().Read(0, top)
	if !ok {
		t.Fatalf("the stack's %d bytes are out of range", top)
	}

	lowest = top

	for i := range stack {
		if stack[i] != stackPattern[i%len(stackPattern)] {
			if lowest == top {
				lowest = uint32(i)
			}

			highest = uint32(i)
		}
	}

	return lowest, highest
}

// denseTree is the 1080p tree of the golden test: 8 columns of 40 widgets.
func denseTree(t *testing.T, k Kit) engine.Node {
	t.Helper()

	root := newNode(t, k, "frame", 0)
	k.SetNum(root, "padding", 4)

	for c := range 8 {
		col := newNode(t, k, "frame", 0)
		addPacked(t, k, root, col)
		k.GeomSet(col, "side", 2) // left
		k.GeomSet(col, "fill", 3) // both
		k.GeomSet(col, "expand", 1)
		k.GeomSet(col, "padx0", 2)

		for row := range 40 {
			n := denseWidget(t, k, c, row)
			addPacked(t, k, col, n)
			k.GeomSet(n, "pady0", 2)
		}
	}

	return root
}

func denseWidget(t *testing.T, k Kit, c, row int) engine.Node {
	t.Helper()

	text := func(typ, s string) engine.Node {
		n := newNode(t, k, typ, 0)
		k.SetStr(n, "text", s)

		return n
	}

	switch (row + c) % 4 {
	case 0:
		return text("label", "Label "+strconv.Itoa(c)+"."+strconv.Itoa(row)+": the quick brown fox")
	case 1:
		n := newNode(t, k, "slider", 0)
		k.SetStr(n, "label", "Value "+strconv.Itoa(row))
		k.SetNum(n, "max", 100)
		k.SetNum(n, "value", float64((row*7+c*13)%100))

		return n
	case 2:
		return text("button", "Button "+strconv.Itoa(row))
	default:
		return text("checkbox", "Option "+strconv.Itoa(row))
	}
}

// stackWorkload runs the heaviest things known: the 1080p tree, whose text goes through
// FreeType's large cell pool, and a click whose handler re-enters the module.
func stackWorkload(t *testing.T, k Kit) {
	t.Helper()

	const denseW, denseH = 1920, 1080

	denseCtx := k.ContextNew(denseTree(t, k))
	denseRaster := k.RasterNew(denseW, denseH)

	k.Render(denseCtx, denseRaster, denseW, denseH)
	k.RasterFree(denseRaster)
	k.ContextFree(denseCtx)

	root := newNode(t, k, "frame", 0)
	button := newNode(t, k, "button", 1)
	k.SetStr(button, "text", "Press me")
	addPacked(t, k, root, button)

	ctx, r := attach(t, k, root)
	x, y := center(k, button)

	clicks := 0

	k.SetEmit(func(id uint32, kind int, _ float64) {
		if id == 1 && kind == engine.EvClick {
			clicks++

			k.SetStr(button, "text", "Clicked "+strconv.Itoa(clicks))
		}
	})
	defer k.SetEmit(nil)

	clickAt(k, ctx, x, y)
	k.Render(ctx, r, kitW, kitH)

	if clicks != 1 {
		t.Errorf("%d clicks reached the handler, want 1", clicks)
	}
}

// TestStackPeak measures how much of the module's stack the heaviest workloads use. An
// overflow itself traps, so the limit here is half of the stack, to notice the margin
// going long before that.
func TestStackPeak(t *testing.T) {
	top := initialStackPointer(t, kitModule)
	m := loadForTest(t, options{cacheDir: sharedCache, memoryLimitPages: memoryLimitPages})

	paintStack(t, m, top)

	if lowest, _ := stackUse(t, m, top); lowest != top {
		t.Fatalf("the scan sees a write at %d right after painting", lowest)
	}

	stackWorkload(t, Kit{m: m})

	lowest, highest := stackUse(t, m, top)
	peak := top - lowest

	t.Logf("stack: peak %d bytes of %d (%.1f%%), highest write %d below the top",
		peak, top, 100*float64(peak)/float64(top), top-highest)

	if highest < top-stackTopWindow {
		t.Errorf("the highest write is at %d, far below the stack top %d: the bounds are wrong", highest, top)
	}

	if peak < minStackPeak {
		t.Errorf("a peak of %d bytes is less than any render takes: the measuring is broken", peak)
	}

	if peak > top/2 {
		t.Errorf("stack peak %d is over half of the %d bytes", peak, top)
	}
}
