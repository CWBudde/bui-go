// SPDX-License-Identifier: GPL-2.0-or-later

package wasmengine

import (
	"context"
	"errors"
	"math"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/CWBudde/bui-go/internal/engine"
	"github.com/tetratelabs/wazero/api"
)

const kitW, kitH = 300, 200

// forEachEngine runs f as a subtest on a fresh module of each wazero engine.
func forEachEngine(t *testing.T, f func(t *testing.T, k Kit)) {
	t.Helper()

	for _, tc := range []struct {
		name        string
		interpreter bool
	}{{"compiler", false}, {"interpreter", true}} {
		t.Run(tc.name, func(t *testing.T) {
			m := loadForTest(t, options{
				interpreter:      tc.interpreter,
				cacheDir:         sharedCache,
				memoryLimitPages: memoryLimitPages,
			})

			f(t, Kit{m: m})
		})
	}
}

func newNode(t *testing.T, k Kit, typ string, id uint32) engine.Node {
	t.Helper()

	n := k.NodeNew(typ)
	if n == 0 {
		t.Fatalf("NodeNew(%q) = 0", typ)
	}

	if id != 0 {
		k.NodeSetID(n, id)
	}

	return n
}

// addPacked adds a child that fills its parent's width.
func addPacked(t *testing.T, k Kit, parent, child engine.Node) {
	t.Helper()

	if !k.NodeAdd(parent, child) {
		t.Fatal("NodeAdd refused the child")
	}

	k.GeomSetPack(child)
	k.GeomSet(child, "fill", 1) // FillX
}

// attach gives root a context that emits and lays it out, for the rest of the test.
func attach(t *testing.T, k Kit, root engine.Node) (engine.Context, engine.Raster) {
	t.Helper()

	ctx := k.ContextNew(root)
	k.ContextSetEmitHost(ctx)

	r := k.RasterNew(kitW, kitH)
	k.Render(ctx, r, kitW, kitH)

	t.Cleanup(func() {
		k.RasterFree(r)
		k.ContextFree(ctx)
	})

	return ctx, r
}

func center(k Kit, n engine.Node) (int, int) {
	x, y, w, h := k.NodeRect(n)

	return x + w/2, y + h/2
}

func clickAt(k Kit, ctx engine.Context, x, y int) {
	k.Press(ctx, x, y)
	k.Release(ctx, x, y)
}

// clickableButton builds a frame holding a button with id 1, ready to click.
func clickableButton(t *testing.T, k Kit) (ctx engine.Context, button engine.Node, x, y int) {
	t.Helper()

	root := newNode(t, k, "frame", 0)
	button = newNode(t, k, "button", 1)

	if !k.SetStr(button, "text", "Press me") {
		t.Fatal("SetStr refused text")
	}

	addPacked(t, k, root, button)

	ctx, _ = attach(t, k, root)
	x, y = center(k, button)

	return ctx, button, x, y
}

// pattern is n bytes that depend on seed, so that one string in place of another
// shows, and no prefix of it matches.
func pattern(seed string, n int) string {
	return strings.Repeat(seed+"-", n/(len(seed)+1)+1)[:n]
}

// spy wraps an export, seeing what call passes to wazero before it runs.
type spy struct {
	api.Function

	onCall func(stack []uint64)
}

func (s spy) CallWithStack(ctx context.Context, stack []uint64) error {
	s.onCall(stack)

	return s.Function.CallWithStack(ctx, stack) //nolint:wrapcheck // a test double
}

// zeroResult wraps an export to make it answer 0.
type zeroResult struct {
	api.Function
}

func (z zeroResult) CallWithStack(ctx context.Context, stack []uint64) error {
	err := z.Function.CallWithStack(ctx, stack)
	stack[0] = 0

	return err //nolint:wrapcheck // a test double
}

// spyOn installs onCall on the export at the given nesting level.
func spyOn(m *module, lvl int, name string, onCall func(stack []uint64)) {
	saved := m.depth
	m.depth = lvl
	m.level().funcs[name] = spy{m.mod.ExportedFunction(name), onCall}
	m.depth = saved
}

// enumValue finds name's value in the enum that declares it in a C header.
func enumValue(t *testing.T, header, name string) int {
	t.Helper()

	header = regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(header, "")

	for _, body := range regexp.MustCompile(`enum\s*\w*\s*\{([^}]*)\}`).FindAllStringSubmatch(header, -1) {
		next := 0

		for item := range strings.SplitSeq(body[1], ",") {
			key, value, explicit := strings.Cut(item, "=")

			if explicit {
				var err error

				next, err = strconv.Atoi(strings.TrimSpace(value))
				if err != nil {
					break // an expression: not an enum of the kind looked for
				}
			}

			if strings.TrimSpace(key) == name {
				return next
			}

			next++
		}
	}

	t.Fatalf("%s is not in the header", name)

	return 0
}

// The constants the cgo engine takes from the header by name, here against the header.
func TestKitConstantsMatchTheHeader(t *testing.T) {
	b, err := os.ReadFile("../../../include/BUI_kit.h")
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		got  int
	}{
		{"BK_BUTTON1", bkButton1},
		{"BK_GEOM_PACK", bkGeomPack},
		{"BK_EV_CHANGE", engine.EvChange},
		{"BK_EV_CLICK", engine.EvClick},
	} {
		if want := enumValue(t, string(b), tc.name); tc.got != want {
			t.Errorf("%s = %d, the header says %d", tc.name, tc.got, want)
		}
	}
}

func TestPressPassesTheButtonAndRenderIsFull(t *testing.T) {
	m := loadForTest(t, options{cacheDir: sharedCache, memoryLimitPages: memoryLimitPages})
	k := Kit{m: m}

	var mods []uint64

	spyOn(m, 0, "BK_press", func(stack []uint64) { mods = append(mods, stack[3]) })
	spyOn(m, 0, "BK_release", func(stack []uint64) { mods = append(mods, stack[3]) })

	var full uint64

	spyOn(m, 0, "BK_render", func(stack []uint64) { full = stack[4] })

	ctx, _, x, y := clickableButton(t, k)
	clickAt(k, ctx, x, y)

	if !slices.Equal(mods, []uint64{8, 8}) {
		t.Errorf("press and release passed modifiers %v, want BK_BUTTON1", mods)
	}

	if full != 1 {
		t.Errorf("BK_render's full = %d, want 1", full)
	}
}

func TestKitTree(t *testing.T) {
	forEachEngine(t, func(t *testing.T, k Kit) { //nolint:thelper // a subtest
		if n := k.NodeNew("no such type"); n != 0 {
			t.Errorf("NodeNew of an unknown type = %#x", n)
		}

		parent := newNode(t, k, "frame", 0)

		if id := k.NodeID(parent); id != 0 {
			t.Errorf("a new node's id = %d", id)
		}

		const bigID uint32 = 0xFFFFFFFE

		k.NodeSetID(parent, bigID)

		if id := k.NodeID(parent); id != bigID {
			t.Errorf("NodeID = %#x, want %#x", id, bigID)
		}

		k.NodeFree(parent)
	})
}

func TestKitChildren(t *testing.T) {
	forEachEngine(t, func(t *testing.T, k Kit) { //nolint:thelper // a subtest
		parent := newNode(t, k, "frame", 0)
		a, b, c := newNode(t, k, "label", 0), newNode(t, k, "label", 0), newNode(t, k, "label", 0)

		if k.NodeParent(a) != 0 {
			t.Error("a detached node has a parent")
		}

		// The insert index -1 appends. Taken for an unsigned or positive index, it
		// would be out of range or go first.
		for _, n := range []engine.Node{a, b, c} {
			if !k.NodeAdd(parent, n) {
				t.Fatal("NodeAdd refused a child")
			}
		}

		if k.NodeAdd(parent, parent) {
			t.Error("NodeAdd of a node to itself returned true")
		}

		if k.NodeAdd(parent, 0) {
			t.Error("NodeAdd of a null child returned true")
		}

		if got := k.NodeChildCount(parent); got != 3 {
			t.Fatalf("NodeChildCount = %d", got)
		}

		for i, want := range []engine.Node{a, b, c} {
			if got := k.NodeChild(parent, i); got != want {
				t.Errorf("NodeChild(%d) = %#x, want %#x", i, got, want)
			}
		}

		if k.NodeChild(parent, -1) != 0 || k.NodeChild(parent, 3) != 0 {
			t.Error("NodeChild out of range is not 0")
		}

		if k.NodeParent(b) != parent {
			t.Error("NodeParent of a child is not its parent")
		}

		// Moving goes through NodeAdd too.
		other := newNode(t, k, "frame", 0)
		if !k.NodeAdd(other, b) || k.NodeParent(b) != other || k.NodeChildCount(parent) != 2 {
			t.Error("NodeAdd did not move the child")
		}

		k.NodeFree(other)
		k.NodeFree(parent)
	})
}

func TestKitUnknownKeys(t *testing.T) {
	forEachEngine(t, func(t *testing.T, k Kit) { //nolint:thelper // a subtest
		n := newNode(t, k, "slider", 0)

		if k.SetNum(n, "no such key", 1) {
			t.Error("SetNum of an unknown key returned true")
		}

		if k.SetStr(n, "no such key", "x") {
			t.Error("SetStr of an unknown key returned true")
		}

		if k.SetStrs(n, "no such key", []string{"x"}) {
			t.Error("SetStrs of an unknown key returned true")
		}

		if got := k.GetNum(n, "no such key"); got != 0 {
			t.Errorf("GetNum of an unknown key = %v", got)
		}

		if got := k.GetStr(n, "no such key"); got != "" {
			t.Errorf("GetStr of an unknown key = %q", got)
		}

		k.NodeFree(n)
	})
}

func TestKitProperties(t *testing.T) {
	forEachEngine(t, func(t *testing.T, k Kit) { //nolint:thelper // a subtest
		n := newNode(t, k, "slider", 0)

		if !k.SetNum(n, "min", -5) || !k.SetNum(n, "max", 10) || !k.SetNum(n, "value", -2.5) {
			t.Fatal("SetNum refused a known key")
		}

		if got := k.GetNum(n, "value"); got != -2.5 {
			t.Errorf(`GetNum("value") = %v, want -2.5`, got)
		}

		if got := k.GetStr(n, "type"); got != "slider" {
			t.Errorf(`GetStr("type") = %q`, got)
		}

		for _, s := range []string{"", "a", "Grüße", pattern("long", 5*minScratch)} {
			if !k.SetStr(n, "text", s) {
				t.Fatalf("SetStr refused text %q", s)
			}

			if got := k.GetStr(n, "text"); got != s {
				t.Errorf(`GetStr("text") = %q, want %q`, got, s)
			}
		}

		k.NodeFree(n)
	})
}

// As with C.CString, the bytes are copied and C stops at the NUL. A NULL from C is "".
func TestKitStringEnds(t *testing.T) {
	forEachEngine(t, func(t *testing.T, k Kit) { //nolint:thelper // a subtest
		n := newNode(t, k, "label", 0)

		if !k.SetStr(n, "text", "ab\x00cd") {
			t.Fatal("SetStr refused a string with a NUL")
		}

		if got := k.GetStr(n, "text"); got != "ab" {
			t.Errorf("a string with a NUL came back as %q", got)
		}

		if !k.SetStr(n, "text\x00junk", "keyed") || k.GetStr(n, "text") != "keyed" {
			t.Error("a key with a NUL did not stop there")
		}

		if got := k.get().goStr(0); got != "" {
			t.Errorf("NULL as a Go string = %q", got)
		}

		k.NodeFree(n)
	})
}

// TestSetStrsDeliversEveryString renders a dropdown, which shows the selected item
// only, so a list that arrives shifted or overlapping draws something else.
func TestSetStrsDeliversEveryString(t *testing.T) {
	forEachEngine(t, func(t *testing.T, k Kit) { //nolint:thelper // a subtest
		root := newNode(t, k, "frame", 0)
		d := newNode(t, k, "dropdown", 0)
		addPacked(t, k, root, d)

		ctx, r := attach(t, k, root)

		show := func(items []string, selected int) []uint32 {
			t.Helper()

			if !k.SetStrs(d, "items", items) {
				t.Fatalf("SetStrs refused %d items", len(items))
			}

			if !k.SetNum(d, "value", float64(selected)) {
				t.Fatal("SetNum refused the selection")
			}

			k.Render(ctx, r, kitW, kitH)

			return k.RasterPixels(r)
		}

		want := show([]string{"aa", "Beta"}, 1)

		if got := show([]string{"bb", "Beta"}, 1); !slices.Equal(got, want) {
			t.Error("another first item changed how the second is drawn")
		}

		if got := show([]string{"aa", "Gamma"}, 1); slices.Equal(got, want) {
			t.Error("another second item drew the same pixels")
		}

		if got := show([]string{"Beta", "aa"}, 0); !slices.Equal(got, want) {
			t.Error("the first item is not the first string")
		}

		// Past the first scratch buffer, so that the array and the strings move.
		long := make([]string, 2000)
		for i := range long {
			long[i] = "a"
		}

		long[1999] = "Beta"

		if got := show(long, 1999); !slices.Equal(got, want) {
			t.Error("the last of many items did not arrive")
		}

		// C stops at the NUL here as well.
		if got := show([]string{"aa", "Beta\x00junk"}, 1); !slices.Equal(got, want) {
			t.Error("an item with a NUL did not stop there")
		}

		for name, empty := range map[string][]string{"empty": {}, "nil": nil} {
			if !k.SetStrs(d, "items", empty) {
				t.Errorf("SetStrs refused a %s list", name)
			}
		}

		if got := k.GetNum(d, "value"); got != 0 {
			t.Errorf("an empty list left the selection at %v", got)
		}
	})
}

func TestKitGeometryAndRaster(t *testing.T) {
	forEachEngine(t, func(t *testing.T, k Kit) { //nolint:thelper // a subtest
		root := newNode(t, k, "frame", 0)
		a := newNode(t, k, "button", 0)
		b := newNode(t, k, "button", 0)
		k.SetStr(a, "text", "a")
		k.SetStr(b, "text", "b")
		addPacked(t, k, root, a)
		addPacked(t, k, root, b)
		k.GeomSet(b, "pady0", 30)
		k.GeomSet(b, "padx0", 7)

		ctx, r := attach(t, k, root)

		ax, ay, aw, ah := k.NodeRect(a)
		bx, by, bw, bh := k.NodeRect(b)

		if ax != 0 || ay != 0 || aw != kitW || ah <= 0 {
			t.Errorf("first button at %d,%d %dx%d", ax, ay, aw, ah)
		}

		// Stacked with its padding, and filling the width: only pack does this, so it
		// shows GeomSetPack selected that manager and GeomSet reached it.
		if bx != 7 || by != ah+30 || bw != kitW-7 || bh != ah {
			t.Errorf("second button at %d,%d %dx%d, first is %dx%d", bx, by, bw, bh, aw, ah)
		}

		px := k.RasterPixels(r)
		if len(px) != kitW*kitH {
			t.Fatalf("%d pixels, want %d", len(px), kitW*kitH)
		}

		distinct := map[uint32]bool{}

		for _, p := range px {
			if p>>24 != 0 {
				t.Fatalf("pixel %#08x is not 0x00RRGGBB", p)
			}

			distinct[p] = true
		}

		if len(distinct) < 2 {
			t.Errorf("the render drew %d colours", len(distinct))
		}

		// A copy: drawing again must not change what was returned.
		before := slices.Clone(px)

		k.NodeFree(k.NodeChild(root, 0))
		k.Render(ctx, r, kitW, kitH)

		if !slices.Equal(before, px) {
			t.Error("RasterPixels returned a view")
		}

		if slices.Equal(before, k.RasterPixels(r)) {
			t.Error("removing a button changed no pixel")
		}
	})
}

// The kit makes a raster at least 1 x 1, so what RasterNew(0, 0) gives is that one
// pixel on both engines, and only a kit answering with no pixels gets the nil.
func TestRasterPixelsOfAnEmptyRaster(t *testing.T) {
	forEachEngine(t, func(t *testing.T, k Kit) { //nolint:thelper // a subtest
		r := k.RasterNew(0, 0)

		m := k.get()
		w := int(api.DecodeI32(m.call("BUI_raster_width", ptr(r))))
		h := int(api.DecodeI32(m.call("BUI_raster_height", ptr(r))))

		if got := k.RasterPixels(r); len(got) != w*h {
			t.Errorf("%d pixels for a raster of %d x %d", len(got), w, h)
		}

		for _, name := range []string{"BUI_raster_width", "BUI_raster_height", "BUI_raster_pixels"} {
			saved := m.level().funcs[name]
			m.level().funcs[name] = zeroResult{saved}

			if got := k.RasterPixels(r); got != nil {
				t.Errorf("with %s answering 0: %d pixels, want nil", name, len(got))
			}

			m.level().funcs[name] = saved
		}
	})
}

func TestKitEvents(t *testing.T) {
	forEachEngine(t, func(t *testing.T, k Kit) { //nolint:thelper // a subtest
		type event struct {
			id   uint32
			kind int
			a    float64
		}

		var got []event

		k.SetEmit(func(id uint32, kind int, a float64) { got = append(got, event{id, kind, a}) })

		root := newNode(t, k, "frame", 0)
		button := newNode(t, k, "button", 1)
		slider := newNode(t, k, "slider", 2)
		silent := newNode(t, k, "button", 0)

		for _, n := range []engine.Node{button, slider, silent} {
			addPacked(t, k, root, n)
		}

		k.SetStr(button, "text", "b")
		k.SetStr(silent, "text", "s")
		k.SetNum(slider, "max", 10)

		ctx, _ := attach(t, k, root)

		x, y := center(k, button)
		k.Press(ctx, x, y)

		if len(got) != 0 {
			t.Fatalf("press emitted %v", got)
		}

		k.Release(ctx, x, y)

		if !slices.Equal(got, []event{{1, engine.EvClick, 0}}) {
			t.Fatalf("a click emitted %v", got)
		}

		got = nil

		// Pressed on the button, released elsewhere: no click.
		k.Press(ctx, x, y)
		k.Release(ctx, kitW-1, kitH-1)

		if len(got) != 0 {
			t.Fatalf("a release elsewhere emitted %v", got)
		}

		// A node without an id never reaches Go.
		sx, sy := center(k, silent)
		clickAt(k, ctx, sx, sy)

		if len(got) != 0 {
			t.Fatalf("a click on a node without an id emitted %v", got)
		}

		if !k.SetNum(slider, "set_value", 7) {
			t.Fatal("SetNum refused set_value")
		}

		if !slices.Equal(got, []event{{2, engine.EvChange, 7}}) {
			t.Fatalf("set_value emitted %v", got)
		}
	})
}

func TestKitEvalNumber(t *testing.T) {
	forEachEngine(t, func(t *testing.T, k Kit) { //nolint:thelper // a subtest
		if got, ok := k.EvalNumber("2*pi"); !ok || math.Abs(got-2*math.Pi) > 1e-9 {
			t.Errorf("2*pi = %v, %v", got, ok)
		}

		if got, ok := k.EvalNumber("-1.5"); !ok || got != -1.5 {
			t.Errorf("-1.5 = %v, %v", got, ok)
		}

		// A failure must not hand on what an earlier result left in the buffer.
		if got, ok := k.EvalNumber("2*"); ok || got != 0 {
			t.Errorf("an invalid expression = %v, %v", got, ok)
		}

		long := strings.Repeat("1+", 2*minScratch) + "1"
		if got, ok := k.EvalNumber(long); !ok || got != 2*minScratch+1 {
			t.Errorf("a long expression = %v, %v", got, ok)
		}

		if got, ok := k.EvalNumber("1+1\x00junk"); !ok || got != 2 {
			t.Errorf("an expression with a NUL = %v, %v", got, ok)
		}
	})
}

func TestZeroKitsAreEqualAndInterpreterKitsIndependent(t *testing.T) {
	first, second := Kit{}, Kit{}
	if first != second {
		t.Error("two zero Kits differ")
	}

	a, b := NewInterpreterKit(), NewInterpreterKit()
	t.Cleanup(a.m.close)
	t.Cleanup(b.m.close)

	if a.m == b.m {
		t.Fatal("two interpreter Kits share a module")
	}

	var clicksA, clicksB int

	a.SetEmit(func(uint32, int, float64) { clicksA++ })
	b.SetEmit(func(uint32, int, float64) { clicksB++ })

	ctx, _, x, y := clickableButton(t, a)
	clickAt(a, ctx, x, y)

	if clicksA != 1 || clicksB != 0 {
		t.Fatalf("clicks on a: a saw %d, b saw %d", clicksA, clicksB)
	}

	if loaded != nil {
		t.Fatal("an interpreter Kit used the process's module")
	}
}

// threeNodes builds a frame of three nodes with ids 1, 2 and 3, ready to emit.
func threeNodes(t *testing.T, k Kit, typ string) (a, b, c engine.Node) {
	t.Helper()

	root := newNode(t, k, "frame", 0)
	a, b, c = newNode(t, k, typ, 1), newNode(t, k, typ, 2), newNode(t, k, typ, 3)

	for _, n := range []engine.Node{a, b, c} {
		addPacked(t, k, root, n)

		if typ == "slider" {
			k.SetNum(n, "max", 100)
		}
	}

	attach(t, k, root)

	return a, b, c
}

func checkScratchesApart(t *testing.T, m *module) {
	t.Helper()

	for i, x := range m.levels {
		for _, y := range m.levels[i+1:] {
			if x.scratch != 0 && y.scratch != 0 && x.scratch < y.scratch+y.scratchSize && y.scratch < x.scratch+x.scratchSize {
				t.Errorf("the buffers at %#x and %#x overlap", x.scratch, y.scratch)
			}
		}
	}
}

// outerArguments checks what a SetStr of "set_value" at the given level put in its
// scratch.
func outerArguments(t *testing.T, m *module, lvl int, value string) {
	t.Helper()

	want := "set_value\x00" + value + "\x00"

	got, ok := m.mod.Memory().Read(m.levels[lvl].scratch, uint32(len(want)))
	if !ok {
		t.Fatalf("level %d: scratch out of range", lvl)
	}

	if string(got) != want {
		t.Errorf("level %d: a handler overwrote the arguments of the call it runs in", lvl)
	}
}

// TestScratchIsPerNestingLevel: while a handler runs, C may
// still read the strings of the call that emitted. The kit's setters happen not to
// read them after emitting today, so no result of the kit can show the overwrite,
// and this looks at the arguments themselves, while a real emit is in progress. The
// strings grow from level to level, so that a shared buffer would also be freed.
func TestScratchIsPerNestingLevel(t *testing.T) {
	forEachEngine(t, func(t *testing.T, k Kit) { //nolint:thelper // a subtest
		m := k.get()

		value0 := pattern("outer", 300)
		value1 := pattern("middle", 8*minScratch)
		value2 := pattern("inner", 256*minScratch)

		a, b, c := threeNodes(t, k, "text")

		var reached []uint32

		k.SetEmit(func(id uint32, _ int, _ float64) {
			reached = append(reached, id)

			switch id {
			case 1:
				if !k.SetStr(b, "set_value", value1) {
					t.Error("level 1: SetStr refused")
				}

				outerArguments(t, m, 0, value0)
			case 2:
				if !k.SetStr(c, "set_value", value2) {
					t.Error("level 2: SetStr refused")
				}

				outerArguments(t, m, 0, value0)
				outerArguments(t, m, 1, value1)
			case 3:
				if got := k.GetStr(c, "value"); got != value2 {
					t.Error("level 3: GetStr did not return the long string")
				}

				outerArguments(t, m, 0, value0)
				outerArguments(t, m, 1, value1)
				outerArguments(t, m, 2, value2)
			}
		})

		if !k.SetStr(a, "set_value", value0) {
			t.Fatal("level 0: SetStr refused")
		}

		if !slices.Equal(reached, []uint32{1, 2, 3}) {
			t.Fatalf("emits reached %v, so this did not nest", reached)
		}

		if k.GetStr(a, "value") != value0 || k.GetStr(b, "value") != value1 || k.GetStr(c, "value") != value2 {
			t.Error("a node did not store its value")
		}

		checkScratchesApart(t, m)
	})
}

// A handler on the input path: press and release have no strings of their own, but
// the handler's long strings make malloc grow the memory under the running export.
func TestHandlerWithLongStringsDuringAClick(t *testing.T) {
	forEachEngine(t, func(t *testing.T, k Kit) { //nolint:thelper // a subtest
		m := k.get()
		ctx, button, x, y := clickableButton(t, k)
		size := m.mod.Memory().Size()

		const pages = 2

		for i := range pages {
			value := pattern(string(rune('a'+i)), (i+1)*4*pageSize)
			clicks := 0

			k.SetEmit(func(id uint32, kind int, _ float64) {
				clicks++

				if id != 1 || kind != engine.EvClick {
					t.Errorf("emit of id %d, kind %d", id, kind)
				}

				if !k.SetStr(button, "text", value) {
					t.Error("SetStr in a handler refused")
				}

				if k.GetStr(button, "text") != value {
					t.Error("GetStr in a handler did not return the long string")
				}

				k.SetStr(button, "text", "Press me")
			})

			clickAt(k, ctx, x, y)

			if clicks != 1 {
				t.Fatalf("round %d: %d clicks", i, clicks)
			}
		}

		if m.mod.Memory().Size() <= size {
			t.Error("the memory did not grow, so this tested nothing")
		}

		// The tree still works after the module memory moved.
		clickAt(k, ctx, x, y)

		if k.GetStr(button, "text") != "Press me" {
			t.Errorf("text afterwards: %q", k.GetStr(button, "text"))
		}
	})
}

func TestScratchIsReusedWithinALevel(t *testing.T) {
	m := loadForTest(t, options{cacheDir: sharedCache, memoryLimitPages: memoryLimitPages})
	k := Kit{m: m}
	n := newNode(t, k, "label", 0)

	mallocs := 0

	spyOn(m, 0, "malloc", func([]uint64) { mallocs++ })

	for i := range 200 {
		k.SetStr(n, "text", pattern("x", i))
		k.GetStr(n, "text")
		k.GetNum(n, "value")
		k.NodeRect(n)
		k.EvalNumber("1+1")
	}

	if mallocs != 0 {
		t.Errorf("%d mallocs for 200 rounds of small calls, want none past the first buffer", mallocs)
	}

	// Growing keeps the one buffer, by doubling at least.
	const steps = 1000

	for i := range steps {
		k.SetStr(n, "text", pattern("y", minScratch+i*minScratch/10))
	}

	if mallocs > 8 {
		t.Errorf("%d mallocs for %d growing strings", mallocs, steps)
	}

	if m.levels[0].scratch == 0 || len(m.levels) < 1 {
		t.Error("no scratch buffer at level 0")
	}
}

// TestNestedCallsHaveTheirOwnStacks: wazero writes a call's result on
// return, so a shared stack would not show in a result today: this looks at the
// stacks handed over. See TestSameExportReenters for what sharing functions does.
func TestNestedCallsHaveTheirOwnStacks(t *testing.T) {
	forEachEngine(t, func(t *testing.T, k Kit) { //nolint:thelper // a subtest
		m := k.get()

		a, b, c := threeNodes(t, k, "slider")

		stacks := map[int]*uint64{}

		for lvl := range 3 {
			spyOn(m, lvl, "BK_set_num", func(stack []uint64) { stacks[lvl] = &stack[0] })
		}

		var inner []bool

		k.SetEmit(func(id uint32, _ int, _ float64) {
			switch id {
			case 1:
				inner = append(inner, k.SetNum(b, "set_value", 2))
				inner = append(inner, k.SetNum(b, "no such key", 2))
			case 2:
				inner = append(inner, k.SetNum(c, "set_value", 3))
			}
		})

		if !k.SetNum(a, "set_value", 1) {
			t.Fatal("the outer SetNum lost its result")
		}

		if !slices.Equal(inner, []bool{true, true, false}) {
			t.Fatalf("nested results: %v", inner)
		}

		if len(stacks) != 3 || stacks[0] == stacks[1] || stacks[1] == stacks[2] || stacks[0] == stacks[2] {
			t.Fatalf("stacks of levels 0 to 2: %v", stacks)
		}

		if m.depth != 0 {
			t.Errorf("depth after the calls = %d", m.depth)
		}
	})
}

// TestSameExportReenters is what breaks for real without a level. A wazero
// api.Function keeps its native stack and where a Go call left it, so re-entering the
// same one runs over the frames of the call in progress. The handler's calls take a
// different path through the kit from the one that emitted, as an identical path
// lays out identical frames and hides this.
func TestSameExportReenters(t *testing.T) {
	forEachEngine(t, func(t *testing.T, k Kit) { //nolint:thelper // a subtest
		m := k.get()
		a, b, c := threeNodes(t, k, "slider")

		k.SetEmit(func(id uint32, _ int, _ float64) {
			switch id {
			case 1:
				if k.SetNum(b, "no such key", 1) {
					t.Error("level 1: an unknown key returned true")
				}

				if !k.SetNum(b, "value", 5) || k.GetNum(b, "value") != 5 {
					t.Error("level 1: a silent set did not arrive")
				}

				k.SetNum(b, "set_value", 20)
			case 2:
				if !k.SetNum(c, "padding", 3) {
					t.Error("level 2: SetNum returned false")
				}

				k.SetNum(c, "set_value", 30)
			}
		})

		if !k.SetNum(a, "set_value", 10) {
			t.Fatal("the outer SetNum returned false")
		}

		for i, tc := range []struct {
			n    engine.Node
			want float64
		}{{a, 10}, {b, 20}, {c, 30}} {
			if got := k.GetNum(tc.n, "value"); got != tc.want {
				t.Errorf("slider %d = %v, want %v", i, got, tc.want)
			}
		}

		if m.levels[0].funcs["BK_set_num"] == m.levels[1].funcs["BK_set_num"] {
			t.Error("two levels share an api.Function")
		}
	})
}

// Two levels of re-entry on the input path: release, then the click handler, then
// the change handler that its SetNum fires, each reading what the one below set.
func TestClickHandlerNestsTwoLevels(t *testing.T) {
	forEachEngine(t, func(t *testing.T, k Kit) { //nolint:thelper // a subtest
		root := newNode(t, k, "frame", 0)
		button := newNode(t, k, "button", 1)
		slider := newNode(t, k, "slider", 2)
		label := newNode(t, k, "label", 3)

		k.SetStr(button, "text", "b")
		k.SetNum(slider, "max", 10)

		for _, n := range []engine.Node{button, slider, label} {
			addPacked(t, k, root, n)
		}

		ctx, _ := attach(t, k, root)

		text := pattern("two levels down", 64*minScratch)

		var seen []int

		k.SetEmit(func(id uint32, kind int, a float64) {
			seen = append(seen, kind)

			switch id {
			case 1:
				if !k.SetNum(slider, "set_value", 4) {
					t.Error("SetNum in the click handler returned false")
				}

				if k.GetStr(label, "text") != text || k.GetNum(slider, "value") != 4 {
					t.Error("the click handler does not see what ran inside it")
				}
			case 2:
				if a != 4 || !k.SetStr(label, "text", text) || k.GetStr(label, "text") != text {
					t.Error("the change handler got the wrong value or string")
				}
			}
		})

		x, y := center(k, button)
		clickAt(k, ctx, x, y)

		if !slices.Equal(seen, []int{engine.EvClick, engine.EvChange}) {
			t.Fatalf("events %v", seen)
		}
	})
}

// TestNegativeIntsAreEncodedAs32Bits: wazero and C look at only the low
// bits, so a sign-extended parameter does not show in what the kit does: this looks
// at what is handed to wazero.
func TestNegativeIntsAreEncodedAs32Bits(t *testing.T) {
	m := loadForTest(t, options{cacheDir: sharedCache, memoryLimitPages: memoryLimitPages})
	k := Kit{m: m}

	const (
		minusOne  = 0xFFFFFFFF
		minusFive = 0xFFFFFFFB
		minusNine = 0xFFFFFFF7
	)

	var got []uint64

	record := func(indexes ...int) func([]uint64) {
		return func(stack []uint64) {
			for _, i := range indexes {
				got = append(got, stack[i])
			}
		}
	}

	spyOn(m, 0, "BK_press", record(1, 2))
	spyOn(m, 0, "BK_release", record(1, 2))
	spyOn(m, 0, "BK_node_add", record(2))
	spyOn(m, 0, "BK_node_child", record(1))

	ctx, button, _, _ := clickableButton(t, k)
	root := k.NodeParent(button)

	if !slices.Equal(got, []uint64{minusOne}) {
		t.Errorf("BK_node_add's index was %#x", got)
	}

	got = nil

	k.Press(ctx, -5, -9)
	k.Release(ctx, -5, -9)
	k.NodeChild(root, -1)

	if want := []uint64{minusFive, minusNine, minusFive, minusNine, minusOne}; !slices.Equal(got, want) {
		t.Errorf("parameters %#x, want %#x", got, want)
	}

	if i32(-1) != minusOne || i32(math.MinInt32) != 0x80000000 || i32(5) != 5 {
		t.Error("i32 is not a 32-bit encoding")
	}
}

// Negative coordinates through the whole path: a rect that comes back negative, and
// input there.
func TestNegativeCoordinates(t *testing.T) {
	forEachEngine(t, func(t *testing.T, k Kit) { //nolint:thelper // a subtest
		clicks := 0

		k.SetEmit(func(uint32, int, float64) { clicks++ })

		root := newNode(t, k, "frame", 0)
		button := newNode(t, k, "button", 1)
		k.SetStr(button, "text", "Press me")
		addPacked(t, k, root, button)
		k.GeomSet(button, "padx0", -20)
		k.GeomSet(button, "pady0", -5)

		ctx, _ := attach(t, k, root)

		x, y, w, h := k.NodeRect(button)
		if x != -20 || y != -5 || w <= 0 || h <= 0 {
			t.Fatalf("rect of a node pulled off the raster: %d,%d %dx%d", x, y, w, h)
		}

		// Outside the raster nothing is hit, and nothing may trap.
		k.Press(ctx, -5, 2)
		k.Release(ctx, -5, 2)

		if clicks != 0 {
			t.Fatalf("a click off the raster emitted %d events", clicks)
		}

		// The release is hit-tested against the rect, which starts at -20,-5: only the
		// sign tells these apart, the first two points being on it.
		px, py := x+w/2, 2

		for _, tc := range []struct {
			x, y   int
			clicks int
		}{{-5, -5, 1}, {-20, -3, 1}, {-5, -6, 0}, {-21, 2, 0}} {
			clicks = 0

			k.Press(ctx, px, py)
			k.Release(ctx, tc.x, tc.y)

			if clicks != tc.clicks {
				t.Errorf("released at %d,%d: %d clicks, want %d", tc.x, tc.y, clicks, tc.clicks)
			}
		}
	})
}

const kitChildEnv = "BUI_WASMENGINE_KIT_TEST_CHILD"

// outOfRange is an address no module memory reaches, so C's first access traps.
const outOfRange = engine.Node(0xFFFFFFF0)

// TestKitChildProcess does what ends the process when TestKitFatalPathsExit asks.
// Everything runs under a recover, as a caller's or wazero's could be: only an
// os.Exit gets past it, and an unrecovered panic would exit with status 2 by itself.
func TestKitChildProcess(t *testing.T) {
	child := os.Getenv(kitChildEnv)
	if child == "" {
		t.Skip("only runs as a subprocess")
	}

	func() {
		defer func() { _ = recover() }()

		switch child {
		case "trap":
			m := loadForTest(t, options{cacheDir: sharedCache, memoryLimitPages: memoryLimitPages})
			Kit{m: m}.NodeChildCount(outOfRange)
		case "trap-in-handler":
			m := loadForTest(t, options{cacheDir: sharedCache, memoryLimitPages: memoryLimitPages})
			k := Kit{m: m}
			ctx, _, x, y := clickableButton(t, k)

			k.SetEmit(func(uint32, int, float64) { k.NodeChildCount(outOfRange) })
			clickAt(k, ctx, x, y)
		case "missing-export":
			m, err := load(wasiProbe(), options{memoryLimitPages: memoryLimitPages})
			if err != nil {
				t.Fatal(err)
			}

			Kit{m: m}.NodeFree(1)
		case "malloc-fails":
			m := smallModule(t)
			k := Kit{m: m}
			n := k.NodeNew("label")

			k.SetStr(n, "text", strings.Repeat("x", 64*pageSize))
		case "malloc-fails-for-small-scratch":
			m := smallModule(t)

			// Use up what is left, so that even a first small buffer fails.
			for range 1 << 16 {
				if callExport(t, m, "malloc", pageSize) == 0 {
					break
				}
			}

			for callExport(t, m, "malloc", 1) != 0 {
				continue
			}

			Kit{m: m}.NodeNew("label")
		}
	}()

	t.Fatal("the process survived")
}

// smallModule has room for only a little more than it starts with.
func smallModule(t *testing.T) *module {
	t.Helper()

	probe := loadForTest(t, options{cacheDir: sharedCache, memoryLimitPages: memoryLimitPages})
	limit := probe.mod.Memory().Size()/pageSize + 32

	return loadForTest(t, options{cacheDir: sharedCache, memoryLimitPages: limit})
}

// An error from any export, a missing export and a failed malloc
// all end the process with status 2 and say why, whatever recovers above them.
func TestKitFatalPathsExit(t *testing.T) {
	for _, tc := range []struct{ child, want string }{
		{"trap", "BK_node_child_count"},
		{"trap-in-handler", "BK_node_child_count"},
		{"missing-export", `no export "BK_node_free"`},
		{"malloc-fails", "out of memory"},
		{"malloc-fails-for-small-scratch", "out of memory"},
	} {
		t.Run(tc.child, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestKitChildProcess$", "-test.count=1")

			cmd.Env = append(os.Environ(), kitChildEnv+"="+tc.child)

			out, err := cmd.CombinedOutput()

			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 2 {
				t.Fatalf("child: %v, want exit status 2\n%s", err, out)
			}

			if !strings.Contains(string(out), "bui: ") || !strings.Contains(string(out), tc.want) {
				t.Fatalf("child did not report %q:\n%s", tc.want, out)
			}

			if strings.Contains(string(out), "goroutine ") {
				t.Fatalf("child panicked rather than exiting:\n%s", out)
			}
		})
	}
}
