// SPDX-License-Identifier: GPL-2.0-or-later

package enginetest

import (
	"bytes"
	"encoding/binary"
	"slices"

	"github.com/CWBudde/bui-go/internal/engine"
)

const (
	// bkCursorDefault and bkCursorText are BK_CURSOR_DEFAULT and BK_CURSOR_TEXT.
	bkCursorDefault = 0
	bkCursorText    = 3
)

func testInput(h *harness) {
	h.sub("HoverDamagesOnlyWhatChanged", testInputHover)
	h.sub("RasterView", testInputRasterView)
	h.sub("SliderDrag", testInputSliderDrag)
	h.sub("Wheel", testInputWheel)
	h.sub("Typing", testInputTyping)
	h.sub("RawInputReturns", testInputRaw)
	h.sub("ContextMenu", testInputContextMenu)
	h.sub("Scale", testInputScale)
	h.sub("ImagePollIdle", testInputImagePoll)
}

func testWindowHost(h *harness) {
	h.sub("Request", testHostRequest)
	h.sub("Cursor", testHostCursor)
	h.sub("TooltipTimer", testHostTooltip)
	h.sub("Warp", testHostWarp)
	h.sub("ClipboardCopyPaste", testHostClipboard)
	h.sub("ClipboardEmpty", testHostClipboardEmpty)
	h.sub("EmitStillArrives", testHostEmit)
	h.sub("UninstalledHostGetsNothing", testHostUninstall)
}

func testInputHover(h *harness) {
	s, b := oneButton(h)
	defer s.close()

	x, y := s.center(b)
	before := s.pixels()

	h.k.Motion(s.ctx, x, y, 0)
	checkEq(h.t, "NeedsRender after hovering", h.k.NeedsRender(s.ctx), true)

	if n := h.k.RenderDamage(s.ctx, s.r, s.w, s.ht, false); n <= 0 {
		h.t.Fatalf("RenderDamage after hovering = %d, want regions", n)
	}

	damage := h.k.RasterDamage(s.r)
	if len(damage) == 0 {
		h.t.Fatal("RasterDamage is empty after a redraw")
	}

	after := s.pixels()
	if slices.Equal(before, after) {
		h.t.Fatal("hovering changed no pixels")
	}

	checkInDamage(h, s, before, after, damage)

	checkEq(h.t, "NeedsRender after the render", h.k.NeedsRender(s.ctx), false)
	checkEq(h.t, "RenderDamage with nothing changed", h.k.RenderDamage(s.ctx, s.r, s.w, s.ht, false), 0)
	checkEq(h.t, "Animating", h.k.Animating(s.ctx), false)

	h.k.Motion(s.ctx, -100000, -100000, 0)
	h.k.RenderDamage(s.ctx, s.r, s.w, s.ht, false)

	if !slices.Equal(before, s.pixels()) {
		h.t.Error("the pointer leaving did not restore the pixels")
	}
}

// checkInDamage checks that every changed pixel lies in a damage rectangle, as those
// are all a host presents.
func checkInDamage(h *harness, s *scene, before, after []uint32, damage []engine.Rect) {
	h.t.Helper()

	for i := range after {
		if after[i] == before[i] {
			continue
		}

		px, py := i%s.w, i/s.w
		if !slices.ContainsFunc(damage, func(d engine.Rect) bool {
			return px >= d.X && px < d.X+d.W && py >= d.Y && py < d.Y+d.H
		}) {
			h.t.Fatalf("pixel (%d, %d) changed outside the damage %v", px, py, damage)
		}
	}
}

func testInputRasterView(h *harness) {
	s, _ := oneButton(h)
	defer s.close()

	want := make([]byte, 0, 4*s.w*s.ht)
	for _, p := range s.pixels() {
		// The raster's words have alpha on top, which the raster may or may not set.
		want = binary.LittleEndian.AppendUint32(want, p)
	}

	got := bytes.Clone(h.k.RasterView(s.r))
	if len(got) != len(want) {
		h.t.Fatalf("RasterView: %d bytes, want %d", len(got), len(want))
	}

	for i := 0; i < len(got); i += 4 {
		if !bytes.Equal(got[i:i+3], want[i:i+3]) {
			h.t.Fatalf("RasterView at byte %d: % x, want % x", i, got[i:i+3], want[i:i+3])
		}
	}

	// The next size comes from RenderDamage, which is how a window resizes.
	h.k.RenderDamage(s.ctx, s.r, 50, 40, true)
	checkEq(h.t, "RasterView bytes after a resize", len(h.k.RasterView(s.r)), 4*50*40)
}

func testInputSliderDrag(h *harness) {
	rec := h.record()

	root, sl := h.node(typeFrame), h.node(typeSlider)
	h.num(sl, "min", 0)
	h.num(sl, "max", 100)
	h.num(sl, keyValue, 0)
	h.packIn(root, sl, pack{fill: fillBoth, expand: true})

	s := h.scene(root, 300, 40)
	defer s.close()

	s.render()

	r := h.rectOf(sl)
	y := r.y + r.h/2
	x0 := r.x + r.w/4

	h.k.PressMods(s.ctx, x0, y, engine.ModButton1)

	for _, x := range []int{x0 + 20, x0 + 60, x0 + 100} {
		h.k.Motion(s.ctx, x, y, engine.ModButton1)
	}

	h.k.ReleaseMods(s.ctx, x0+100, y, 0)

	changes := rec.of(h.k.NodeID(sl), engine.EvChange)
	if len(changes) < 2 {
		h.t.Fatalf("%d change events from a drag, want several", len(changes))
	}

	for i := 1; i < len(changes); i++ {
		if changes[i].a <= changes[i-1].a {
			h.t.Errorf("a rightward drag went from %v to %v", changes[i-1].a, changes[i].a)
		}
	}
}

func testInputWheel(h *harness) {
	rec := h.record()

	root, sl := h.node(typeFrame), h.node(typeSlider)
	h.num(sl, "min", 0)
	h.num(sl, "max", 100)
	h.num(sl, keyValue, 50)
	h.packIn(root, sl, pack{fill: fillBoth, expand: true})

	s := h.scene(root, 300, 40)
	defer s.close()

	s.render()

	x, y := s.center(sl)
	h.k.Motion(s.ctx, x, y, 0)

	// Ctrl + wheel is how Blender's value widgets step.
	h.k.Wheel(s.ctx, x, y, 1, engine.ModCtrl)

	up := h.k.GetNum(sl, keyValue)
	if up <= 50 {
		h.t.Errorf("Wheel up gave %v, from 50", up)
	}

	// A float delta goes through the same step path.
	h.k.Scroll(s.ctx, x, y, 0, -1, engine.ModCtrl)

	if down := h.k.GetNum(sl, keyValue); down >= up {
		h.t.Errorf("Scroll down gave %v, from %v", down, up)
	}

	if len(rec.of(h.k.NodeID(sl), engine.EvChange)) < 2 {
		h.t.Error("the wheel steps emitted no changes")
	}
}

func testInputTyping(h *harness) {
	root, txt := h.node(typeFrame), h.node(typeText)
	h.str(txt, keyText, "")
	h.packIn(root, txt, pack{fill: fillX})

	s := h.scene(root, 300, 40)
	defer s.close()

	s.render()

	x, y := s.center(txt)
	h.k.PressMods(s.ctx, x, y, engine.ModButton1)
	h.k.ReleaseMods(s.ctx, x, y, 0)

	for _, key := range []struct{ text, sym string }{
		{"a", "a"}, {"B", "B"}, {"ä", "adiaeresis"}, {"€", "EuroSign"}, {"", "BackSpace"}, {"c", "c"},
	} {
		h.k.Key(s.ctx, key.text, key.sym, 0)
	}

	h.k.Key(s.ctx, "", "Return", 0)
	checkStr(h.t, "typed text", h.k.GetStr(txt, keyText), "aBäc")
}

func testInputRaw(h *harness) {
	s, b := oneButton(h)
	defer s.close()

	x, y := s.center(b)

	// Raw input goes to "events" nodes only, so a button stays as it was.
	before := s.render()

	h.k.KeyRelease(s.ctx, "a", 0)
	h.k.Button(s.ctx, x, y, 2, false, 0)
	h.k.Button(s.ctx, x, y, 2, true, 0)

	if !slices.Equal(before, s.render()) {
		h.t.Error("raw input changed a button")
	}
}

func testInputContextMenu(h *harness) {
	root, txt := h.node(typeFrame), h.node(typeText)
	h.str(txt, keyText, "words")
	h.packIn(root, txt, pack{fill: fillBoth, expand: true})

	s := h.scene(root, 300, 200)
	defer s.close()

	before := s.render()
	x, y := s.center(txt)
	h.k.Motion(s.ctx, x, y, 0)
	h.k.ContextMenu(s.ctx, x, y, 0)

	if slices.Equal(before, s.render()) {
		h.t.Error("ContextMenu showed nothing on a text field")
	}

	h.k.Key(s.ctx, "", "Escape", 0)
}

func testInputScale(h *harness) {
	s, b := oneButton(h)
	defer s.close()

	defer h.k.SetScale(1)

	h.k.SetScale(1)

	small := h.rectOf(b)
	before := s.render()

	h.k.SetScale(2)

	if slices.Equal(before, s.render()) {
		h.t.Errorf("SetScale(2) drew the same, button %+v", small)
	}

	h.k.SetScale(1)

	if !slices.Equal(before, s.render()) {
		h.t.Error("SetScale(1) did not restore the pixels")
	}
}

func testInputImagePoll(h *harness) {
	checkEq(h.t, "ImagePoll with no images", h.k.ImagePoll(), 0)
}

// spyHost records what the kit asks of its window.
type spyHost struct {
	requests  int
	cursors   []int
	warps     [][2]int
	schedules [][2]int
	clipboard string
	hasClip   bool
	gets      int
}

func (s *spyHost) Request()           { s.requests++ }
func (s *spyHost) SetCursor(c int)    { s.cursors = append(s.cursors, c) }
func (s *spyHost) Warp(x, y int)      { s.warps = append(s.warps, [2]int{x, y}) }
func (s *spyHost) Schedule(ms, t int) { s.schedules = append(s.schedules, [2]int{ms, t}) }

func (s *spyHost) ClipboardGet() (string, bool) {
	s.gets++
	return s.clipboard, s.hasClip
}

func (s *spyHost) ClipboardSet(text string) { s.clipboard, s.hasClip = text, true }

// hosted is oneChild's scene with a spy as the window host.
func hosted(h *harness, typ string, build func(engine.Node)) (*scene, engine.Node, *spyHost) {
	h.t.Helper()

	root, n := h.node(typeFrame), h.node(typ)
	if build != nil {
		build(n)
	}

	h.packIn(root, n, pack{fill: fillBoth, expand: true})

	s := h.scene(root, 300, 120)
	spy := &spyHost{}
	h.k.ContextSetWindowHost(s.ctx, spy)
	s.render()

	return s, n, spy
}

func testHostRequest(h *harness) {
	s, b, spy := hosted(h, typeButton, nil)
	defer s.close()

	spy.requests = 0
	x, y := s.center(b)
	h.k.Motion(s.ctx, x, y, 0)

	if spy.requests == 0 {
		h.t.Error("hovering a button requested no render")
	}

	spy.requests = 0

	h.str(b, keyText, "changed")

	if spy.requests == 0 {
		h.t.Error("SetStr requested no render")
	}
}

func testHostCursor(h *harness) {
	s, txt, spy := hosted(h, typeText, nil)
	defer s.close()

	x, y := s.center(txt)
	h.k.Motion(s.ctx, x, y, 0)

	if !slices.Contains(spy.cursors, bkCursorText) {
		h.t.Errorf("cursors over a text field: %v, want %d", spy.cursors, bkCursorText)
	}

	h.k.Motion(s.ctx, -100000, -100000, 0)

	if len(spy.cursors) == 0 || spy.cursors[len(spy.cursors)-1] != bkCursorDefault {
		h.t.Errorf("cursors after leaving: %v, want the last to be %d", spy.cursors, bkCursorDefault)
	}
}

func testHostTooltip(h *harness) {
	s, b, spy := hosted(h, typeButton, func(n engine.Node) { h.str(n, "description", "A tip") })
	defer s.close()

	before := s.render()
	x, y := s.center(b)
	h.k.Motion(s.ctx, x, y, 0)

	if len(spy.schedules) != 1 {
		h.t.Fatalf("schedules after hovering: %v, want one", spy.schedules)
	}

	hovered := s.render()
	if slices.Equal(before, hovered) {
		h.t.Fatal("hovering changed nothing")
	}

	h.k.Timer(s.ctx, spy.schedules[0][1])

	if slices.Equal(hovered, s.render()) {
		h.t.Error("the tooltip timer drew nothing")
	}
}

func testHostWarp(h *harness) {
	s, num, spy := hosted(h, typeNumber, func(n engine.Node) {
		h.num(n, "min", -1000)
		h.num(n, "max", 1000)
		h.num(n, keyValue, 0)
	})
	defer s.close()

	x, y := s.center(num)
	h.k.Motion(s.ctx, x, y, 0)
	h.k.PressMods(s.ctx, x, y, engine.ModButton1)

	// A number field hides the pointer and wraps it near the window's edges, so a
	// drag can go on forever.
	for dx := 20; dx < s.w; dx += 20 {
		h.k.Motion(s.ctx, x+dx, y, engine.ModButton1)
	}

	h.k.ReleaseMods(s.ctx, s.w, y, 0)

	if len(spy.warps) == 0 {
		h.t.Errorf("no warp from dragging a number field to the edge, value %v", h.k.GetNum(num, keyValue))
	}
}

func testHostClipboard(h *harness) {
	s, txt, spy := hosted(h, typeText, func(n engine.Node) { h.str(n, keyText, "copied") })
	defer s.close()

	x, y := s.center(txt)
	h.k.PressMods(s.ctx, x, y, engine.ModButton1)
	h.k.ReleaseMods(s.ctx, x, y, 0)
	h.k.Key(s.ctx, "", "a", engine.ModCtrl)
	h.k.Key(s.ctx, "", "c", engine.ModCtrl)
	checkStr(h.t, "clipboard after Ctrl+C", spy.clipboard, "copied")

	spy.clipboard = "pasted ä"

	h.k.Key(s.ctx, "", "a", engine.ModCtrl)
	h.k.Key(s.ctx, "", "v", engine.ModCtrl)
	h.k.Key(s.ctx, "", "Return", 0)
	checkStr(h.t, "text after Ctrl+V", h.k.GetStr(txt, keyText), "pasted ä")
}

// A host with no clipboard text makes the kit paste the last text it copied itself.
func testHostClipboardEmpty(h *harness) {
	s, txt, spy := hosted(h, typeText, func(n engine.Node) { h.str(n, keyText, "own") })
	defer s.close()

	x, y := s.center(txt)
	h.k.PressMods(s.ctx, x, y, engine.ModButton1)
	h.k.ReleaseMods(s.ctx, x, y, 0)
	h.k.Key(s.ctx, "", "a", engine.ModCtrl)
	h.k.Key(s.ctx, "", "c", engine.ModCtrl)

	spy.clipboard, spy.hasClip = "", false

	h.k.Key(s.ctx, "", "End", 0)
	h.k.Key(s.ctx, "", "v", engine.ModCtrl)
	h.k.Key(s.ctx, "", "Return", 0)
	checkEq(h.t, "ClipboardGet calls", spy.gets, 1)
	checkStr(h.t, "text after pasting with an empty host", h.k.GetStr(txt, keyText), "ownown")
}

func testHostEmit(h *harness) {
	rec := h.record()

	s, b, _ := hosted(h, typeButton, nil)
	defer s.close()

	s.click(b)
	checkEq(h.t, "clicks through the window host", len(rec.of(h.k.NodeID(b), engine.EvClick)), 1)
}

func testHostUninstall(h *harness) {
	rec := h.record()

	s, b, spy := hosted(h, typeButton, nil)
	defer s.close()

	h.k.ContextSetEmitHost(s.ctx)

	spy.requests = 0

	s.click(b)

	checkEq(h.t, "requests after uninstalling", spy.requests, 0)
	checkEq(h.t, "clicks after uninstalling", len(rec.of(h.k.NodeID(b), engine.EvClick)), 1)
}
