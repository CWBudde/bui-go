// SPDX-License-Identifier: GPL-2.0-or-later

package enginetest

import (
	"github.com/CWBudde/bui-go/internal/engine"
)

func testEvents(h *harness) {
	h.sub("Click", testEventClick)
	h.sub("ClickNeedsReleaseInside", testEventClickInside)
	h.sub("ClickEdges", testEventClickEdges)
	h.sub("LargeID", testEventLargeID)
	h.sub("SliderSetValue", testEventSliderSetValue)
	h.sub("SliderPress", testEventSliderPress)
	h.sub("NumberArrow", testEventNumberArrow)
	h.sub("ZeroIDEmitsNothing", testEventZeroID)
	h.sub("ReceiverReplaced", testEventReceiver)
	h.sub("NoEmitHost", testEventNoHost)
	h.sub("MenuPick", testEventMenuPick)
}

// oneButton builds a frame whose only child is a button filling all of it.
func oneButton(h *harness) (*scene, engine.Node) {
	h.t.Helper()

	root, b := h.node(typeFrame), h.node(typeButton)
	h.str(b, keyText, "Press me")
	h.packIn(root, b, pack{fill: fillBoth, expand: true})

	s := h.scene(root, 200, 100)
	s.render()

	return s, b
}

func testEventClick(h *harness) {
	rec := h.record()

	s, b := oneButton(h)
	defer s.close()

	id := h.k.NodeID(b)
	x, y := s.center(b)

	h.k.Press(s.ctx, x, y)
	checkEq(h.t, "clicks after the press alone", len(rec.of(id, engine.EvClick)), 0)
	h.k.Release(s.ctx, x, y)

	clicks := rec.of(id, engine.EvClick)
	if len(clicks) != 1 {
		h.t.Fatalf("clicks after the release: %+v, want 1", clicks)
	}

	checkEq(h.t, "a of a click", clicks[0].a, 0)

	// A second one straight after takes the kit's double-click path.
	s.click(b)
	checkEq(h.t, "clicks after a second click", len(rec.of(id, engine.EvClick)), 2)
}

// testEventClickInside has a button that does not fill the frame, so a miss exists.
func testEventClickInside(h *harness) {
	rec := h.record()

	root, b := h.node(typeFrame), h.node(typeButton)
	h.packIn(root, b, pack{fill: fillX})

	s := h.scene(root, 200, 100)
	defer s.close()

	s.render()

	id := h.k.NodeID(b)
	x, y := s.center(b)
	missY := h.rectOf(b).h + 40

	h.k.Press(s.ctx, x, missY)
	h.k.Release(s.ctx, x, missY)
	checkEq(h.t, "clicks after a miss", len(rec.of(id, engine.EvClick)), 0)

	h.k.Press(s.ctx, x, y)
	h.k.Release(s.ctx, x, missY)
	checkEq(h.t, "clicks after a release outside", len(rec.of(id, engine.EvClick)), 0)

	h.k.Press(s.ctx, x, missY)
	h.k.Release(s.ctx, x, y)
	checkEq(h.t, "clicks after a press outside", len(rec.of(id, engine.EvClick)), 0)

	s.click(b)
	checkEq(h.t, "clicks after a hit", len(rec.of(id, engine.EvClick)), 1)
}

// testEventClickEdges finds the button's edges to the pixel, which a coordinate off by
// one in either direction, or swapped axes, cannot pass.
func testEventClickEdges(h *harness) {
	rec := h.record()

	root, b := h.node(typeFrame), h.node(typeButton)
	h.packIn(root, b, pack{side: sideLeft, padX0: 40, padY0: 10})

	s := h.scene(root, 300, 100)
	defer s.close()

	s.render()

	id := h.k.NodeID(b)
	r := h.rectOf(b)

	clickAt := func(x, y, want int) {
		h.t.Helper()

		before := len(rec.of(id, engine.EvClick))

		h.k.Press(s.ctx, x, y)
		h.k.Release(s.ctx, x, y)
		checkEq(h.t, "clicks at "+itoa(x)+","+itoa(y)+" for "+rectText(r), len(rec.of(id, engine.EvClick))-before, want)
	}

	left, right, top, bottom := r.x, r.x+r.w-1, r.y, r.y+r.h-1
	midX, midY := r.x+r.w/2, r.y+r.h/2

	clickAt(left, midY, 1)
	clickAt(right, midY, 1)
	clickAt(midX, top, 1)
	clickAt(midX, bottom, 1)
	clickAt(left-1, midY, 0)
	clickAt(right+1, midY, 0)
	clickAt(midX, top-1, 0)
	clickAt(midX, bottom+1, 0)
}

func rectText(r rect) string {
	return "rect " + itoa(r.x) + "," + itoa(r.y) + " " + itoa(r.w) + "x" + itoa(r.h)
}

func testEventLargeID(h *harness) {
	rec := h.record()

	s, b := oneButton(h)
	defer s.close()

	for _, id := range []uint32{0x80000000, 0xFFFFFFFF} {
		h.k.NodeSetID(b, id)

		s.click(b)
		checkEq(h.t, "clicks reported for the id", len(rec.of(id, engine.EvClick)), 1)
	}

	h.k.NodeSetID(b, 5)
}

func sliderScene(h *harness) (*scene, engine.Node) {
	h.t.Helper()

	root, v := h.node(typeFrame), h.node(typeSlider)
	h.str(v, keyLabel, "Size")
	h.num(v, "min", 0)
	h.num(v, "max", 10)
	h.num(v, keyValue, 2.5)
	h.packIn(root, v, pack{fill: fillX})

	s := h.scene(root, 320, 120)
	s.render()

	return s, v
}

func testEventSliderSetValue(h *harness) {
	rec := h.record()

	s, v := sliderScene(h)
	defer s.close()

	id := h.k.NodeID(v)

	h.num(v, keyValue, 3)
	checkEq(h.t, `events after plain "value"`, len(rec.of(id, engine.EvChange)), 0)

	h.num(v, keySetValue, 7.125)
	h.num(v, keySetValue, 7.125) // unchanged: silent
	h.num(v, keySetValue, 99)    // clamped: the event carries the clamped value
	h.num(v, keySetValue, 0.1)

	got := rec.of(id, engine.EvChange)
	want := []float64{7.125, 10, 0.1}

	if len(got) != len(want) {
		h.t.Fatalf("changes %+v, want the values %v", got, want)
	}

	for i := range want {
		checkEq(h.t, "change value", got[i].a, want[i])
	}

	checkEq(h.t, "value after the changes", h.k.GetNum(v, keyValue), 0.1)
}

// testEventSliderPress has no drag through the seam, which has no motion, but a
// slider in the "html" look takes its value from the press. The value thus depends
// on the coordinate, through the kit, and comes back in the event.
func testEventSliderPress(h *harness) {
	rec := h.record()

	s, v := sliderScene(h)
	defer s.close()

	h.num(v, "html", 1)
	s.render()

	id := h.k.NodeID(v)
	r := h.rectOf(v)
	y := r.y + r.h/2

	var last float64

	for _, frac := range []int{1, 2, 3} {
		x := r.x + r.w*frac/4
		h.k.Press(s.ctx, x, y)
		h.k.Release(s.ctx, x, y)

		evs := rec.of(id, engine.EvChange)
		if len(evs) != frac {
			h.t.Fatalf("after %d presses: %d changes", frac, len(evs))
		}

		a := evs[frac-1].a
		checkEq(h.t, "value and event", h.k.GetNum(v, keyValue), a)

		// 2.5, 5 and 7.5, within what the track's ends take from the width.
		if diff := a - float64(frac)*2.5; a <= last || diff < -0.5 || diff > 0.5 {
			h.t.Errorf("press at %d/4: value %v after %v", frac, a, last)
		}

		last = a
	}
}

// testEventNumberArrow clicks a number field's arrows, an EvChange from Release.
func testEventNumberArrow(h *harness) {
	rec := h.record()

	root, n := h.node(typeFrame), h.node(typeNumber)
	h.num(n, keyValue, 5)
	h.packIn(root, n, pack{fill: fillX})

	s := h.scene(root, 320, 120)
	defer s.close()

	s.render()

	id := h.k.NodeID(n)
	r := h.rectOf(n)
	y := r.y + r.h/2

	h.k.Press(s.ctx, r.x+r.w-2, y)
	h.k.Release(s.ctx, r.x+r.w-2, y)

	evs := rec.of(id, engine.EvChange)
	if len(evs) != 1 {
		h.t.Fatalf("changes after a click on the right arrow: %+v", evs)
	}

	checkEq(h.t, "value and event", h.k.GetNum(n, keyValue), evs[0].a)

	if evs[0].a <= 5 {
		h.t.Errorf("the right arrow took 5 to %v", evs[0].a)
	}
}

// testEventZeroID has a witness with an id, so that silence means no id and not a
// receiver that never hears anything.
func testEventZeroID(h *harness) {
	rec := h.record()

	root, silent, slider, witness := h.node(typeFrame), h.bare(typeButton), h.bare(typeSlider), h.node(typeButton)
	h.packIn(root, silent, pack{fill: fillX})
	h.num(slider, "max", 10)
	h.packIn(root, slider, pack{fill: fillX})
	h.packIn(root, witness, pack{fill: fillX})

	s := h.scene(root, 200, 150)
	defer s.close()

	s.render()

	s.click(silent)
	h.num(slider, keySetValue, 4)
	checkEq(h.t, "events from nodes with id 0", len(rec.events), 0)
	s.click(witness)
	checkEq(h.t, "clicks from the witness", len(rec.of(h.k.NodeID(witness), engine.EvClick)), 1)

	// An id cleared later silences the node as well.
	h.k.NodeSetID(witness, 0)

	before := len(rec.events)

	s.click(witness)
	checkEq(h.t, "events after clearing the id", len(rec.events), before)
}

func testEventReceiver(h *harness) {
	first := h.record()

	s, b := oneButton(h)
	defer s.close()

	id := h.k.NodeID(b)

	s.click(b)
	checkEq(h.t, "clicks at the first receiver", len(first.of(id, engine.EvClick)), 1)

	second := h.record()

	s.click(b)
	checkEq(h.t, "clicks at the second receiver", len(second.of(id, engine.EvClick)), 1)
	checkEq(h.t, "clicks at the replaced receiver", len(first.of(id, engine.EvClick)), 1)
}

// testEventNoHost clicks in a context that never got the emit host.
func testEventNoHost(h *harness) {
	rec := h.record()

	root, b := h.node(typeFrame), h.node(typeButton)
	defer freeTree(h.k, root)

	h.packIn(root, b, pack{fill: fillBoth, expand: true})

	ctx := h.k.ContextNew(root)
	defer h.k.ContextFree(ctx)

	r := h.k.RasterNew(100, 50)
	defer h.k.RasterFree(r)

	h.k.Render(ctx, r, 100, 50)
	h.k.Press(ctx, 50, 25)
	h.k.Release(ctx, 50, 25)
	checkEq(h.t, "events without the emit host", len(rec.events), 0)
}

// testEventMenuPick clicks an item of a dropdown's menu, whose nodes are the kit's
// own and have no ids. Only the dropdown reports.
func testEventMenuPick(h *harness) {
	rec := h.record()

	root, d := h.node(typeFrame), h.node(typeDrop)
	h.packIn(root, d, pack{fill: fillX})
	setItems(h, d, []string{"Alpha", "Beta", "Gamma", "Delta"})

	s := h.scene(root, 300, 300)
	defer s.close()

	closed := s.render()
	dRect := h.rectOf(d)

	s.click(d)

	box, n := changedBox(closed, s.render(), s.w, dRect)
	if n == 0 {
		h.t.Fatal("the menu did not open")
	}

	id := h.k.NodeID(d)
	before := h.k.GetNum(d, keyValue)
	x, y := box.x+box.w/2, box.y+box.h/2
	h.k.Press(s.ctx, x, y)
	h.k.Release(s.ctx, x, y)

	evs := rec.of(id, engine.EvChange)
	if len(evs) != 1 {
		h.t.Fatalf("events after picking an item: %+v", rec.events)
	}

	value := h.k.GetNum(d, keyValue)
	checkEq(h.t, "event value", evs[0].a, value)

	if value == before || value < 0 || value > 3 {
		h.t.Errorf("value %v after picking, was %v", value, before)
	}

	for _, e := range rec.events {
		if e.id != id {
			h.t.Errorf("event from another node than the dropdown: %+v", e)
		}
	}

	checkEq(h.t, "pixels different from closed, outside the dropdown", changedPixels(closed, s.render(), s.w, dRect), 0)
}

// changedBox returns the bounding box and count of the pixels changed outside skip.
func changedBox(a, b []uint32, w int, skip rect) (rect, int) {
	x0, y0, x1, y1, n := w, len(a)/w, -1, -1, 0

	for i := range a {
		x, y := i%w, i/w
		if a[i]&rgbMask != b[i]&rgbMask && !skip.contains(x, y) {
			x0, y0, x1, y1 = min(x0, x), min(y0, y), max(x1, x), max(y1, y)
			n++
		}
	}

	return rect{x0, y0, x1 - x0 + 1, y1 - y0 + 1}, n
}

// reentryNodes is a tree for handlers to reach into.
type reentryNodes struct {
	button, label, keep, slider, drop engine.Node
}

func reentryScene(h *harness) (*scene, reentryNodes) {
	h.t.Helper()

	root := h.node(typeFrame)
	n := reentryNodes{
		button: h.node(typeButton), label: h.node(typeLabel), keep: h.node(typeLabel),
		slider: h.node(typeSlider), drop: h.node(typeDrop),
	}

	h.str(n.button, keyText, "Go")
	h.str(n.label, keyText, "start")
	h.str(n.keep, keyText, "keep me")
	h.num(n.slider, "max", 10)
	h.num(n.slider, keyValue, 3.5)

	for _, c := range []engine.Node{n.button, n.label, n.keep, n.slider, n.drop} {
		h.packIn(root, c, pack{fill: fillX})
	}

	s := h.scene(root, 300, 200)
	s.render()

	return s, n
}

func testReentry(h *harness) {
	h.sub("InsideRelease", testReentryRelease)
	h.sub("NestedEmits", testReentryNested)
}

// testReentryRelease re-enters the kit in every way a handler may, inside Release,
// with strings long enough to need new memory there.
func testReentryRelease(h *harness) {
	s, n := reentryScene(h)
	defer s.close()

	before := s.render()
	clicks, size := 0, 0
	buttonID := h.k.NodeID(n.button)

	h.listen(func(id uint32, kind int, _ float64) {
		if id != buttonID || kind != engine.EvClick {
			return
		}

		clicks++
		reenter(h, n, size, "clicked "+itoa(clicks))
	})

	for i, sz := range []int{300000, 900000, 100, 1500000} {
		size = sz

		s.click(n.button)
		checkEq(h.t, "clicks", clicks, i+1)
		checkStr(h.t, "label's text after the handler", h.k.GetStr(n.label, keyText), "clicked "+itoa(i+1))
		checkStr(h.t, "long property after the handler", h.k.GetStr(n.label, keyLong), longText(sz))
		checkStr(h.t, "untouched label", h.k.GetStr(n.keep, keyText), "keep me")
		checkEq(h.t, "untouched slider", h.k.GetNum(n.slider, keyValue), 3.5)
		checkEq(h.t, "dropdown value", h.k.GetNum(n.drop, keyValue), 1)

		if changedPixels(before, s.render(), s.w, rect{}) == 0 {
			h.t.Error("the handler's new text changed no pixels")
		}
	}

	// With the visible text back and the pointer away, only the dropdown, which got
	// new items, may look different from the start.
	h.str(n.label, keyText, "start")
	h.k.Press(s.ctx, -1, -1)
	h.k.Release(s.ctx, -1, -1)

	if d := changedPixels(before, s.render(), s.w, h.rectOf(n.drop)); d != 0 {
		h.t.Errorf("%d pixels differ after restoring the tree", d)
	}
}

// reenter runs inside a handler, so it may only report with Errorf.
func reenter(h *harness, n reentryNodes, size int, text string) {
	t, k := h.t, h.k

	want := longText(size)

	checkEq(t, "SetStr of a long string in a handler", k.SetStr(n.label, keyLong, want), true)
	checkStr(t, "GetStr of the long string in a handler", k.GetStr(n.label, keyLong), want)
	checkEq(t, "SetStr in a handler", k.SetStr(n.label, keyText, text), true)
	checkStr(t, "GetStr of what the handler set", k.GetStr(n.label, keyText), text)
	checkStr(t, "GetStr of another node in a handler", k.GetStr(n.keep, keyText), "keep me")
	checkEq(t, "GetNum in a handler", k.GetNum(n.slider, keyValue), 3.5)
	checkEq(t, "SetStrs in a handler", k.SetStrs(n.drop, keyItems, []string{"a", want[:min(size, 40)]}), true)
	checkEq(t, "SetNum in a handler", k.SetNum(n.drop, keyValue, 1), true)

	if x, _, w, _ := k.NodeRect(n.button); x != 0 || w != 300 {
		t.Errorf("NodeRect in a handler: x %d, w %d", x, w)
	}

	if v, ok := k.EvalNumber("6*7"); !ok || v != 42 {
		t.Errorf("EvalNumber in a handler = %v, %v", v, ok)
	}

	// A node may be made during an emit. Freeing an attached one is what must wait.
	temp := k.NodeNew(typeLabel)
	checkEq(t, "SetStr on a new node in a handler", k.SetStr(temp, keyText, "temporary"), true)
	checkStr(t, "GetStr on a new node in a handler", k.GetStr(temp, keyText), "temporary")
	k.NodeFree(temp)

	// A long list on a detached node, as laying out a drawn item this long dominates
	// every later render: 45 s of this test on the interpreter.
	list := k.NodeNew(typeDrop)
	items := []string{"a", want[:min(size, 2000)]}
	checkEq(t, "SetStrs of a long list in a handler", k.SetStrs(list, keyItems, items), true)
	k.NodeFree(list)
}

// nestDepth is how many emits a click stacks up. Each level is inside the SetNum of
// the level above it, so a call's stack or scratch memory shared between levels is
// overwritten by the ones below.
const nestDepth = 16

func testReentryNested(h *harness) {
	root, button, label := h.node(typeFrame), h.node(typeButton), h.node(typeLabel)
	h.str(button, keyText, "Go")
	h.packIn(root, button, pack{fill: fillX})
	h.packIn(root, label, pack{fill: fillX})

	sliders := make([]engine.Node, nestDepth)
	for i := range sliders {
		sliders[i] = h.node(typeSlider)
		h.num(sliders[i], "max", 1000)
		h.packIn(root, sliders[i], pack{fill: fillX})
	}

	s := h.scene(root, 300, 600)
	defer s.close()

	s.render()

	nest := &nester{h: h, label: label, sliders: sliders, byID: map[uint32]int{}, button: h.k.NodeID(button)}
	for i, n := range sliders {
		nest.byID[h.k.NodeID(n)] = i
	}

	h.listen(nest.receive)

	for round := 1; round <= 3; round++ {
		nest.order = nil
		nest.base = float64(round) * 10

		s.click(button)
		checkEq(h.t, "emits on the way down", len(nest.order), nestDepth+1)

		for i, level := range nest.order {
			checkEq(h.t, "level of emit "+itoa(i), level, i-1)
		}

		checkEq(h.t, "levels reached", nest.maxDepth, nestDepth+1)
		checkEq(h.t, "levels left at the end", nest.depth, 0)

		for i := range sliders {
			checkEq(h.t, "slider "+itoa(i), h.k.GetNum(sliders[i], keyValue), nest.base+float64(i)+0.5)
		}

		checkStr(h.t, "long string set at the deepest level", h.k.GetStr(label, keyLong), nest.deepest())
		s.render()
	}
}

// nester reacts to a click by changing the first slider, and to each slider's change
// by changing the next one.
type nester struct {
	h               *harness
	label           engine.Node
	sliders         []engine.Node
	byID            map[uint32]int
	button          uint32
	base            float64
	order           []int
	depth, maxDepth int
}

func (n *nester) deepest() string { return longText(200000) + itoa(int(n.base)) }

func (n *nester) receive(id uint32, kind int, a float64) {
	level := -1

	switch {
	case id == n.button && kind == engine.EvClick:
	case kind == engine.EvChange:
		i, ok := n.byID[id]
		if !ok {
			n.h.t.Errorf("change from unknown id %d", id)

			return
		}

		level = i
		checkEq(n.h.t, "event value at level "+itoa(i), a, n.base+float64(i)+0.5)
	default:
		return
	}

	n.depth++
	n.maxDepth = max(n.maxDepth, n.depth)
	n.order = append(n.order, level)

	n.descend(level)
	n.depth--
}

// descend goes one level down, and checks that what this level read and made before
// that comes back undisturbed.
func (n *nester) descend(level int) {
	t, k := n.h.t, n.h.k

	if level+1 == len(n.sliders) {
		checkEq(t, "SetStr at the deepest level", k.SetStr(n.label, keyLong, n.deepest()), true)

		return
	}

	own := n.label
	if level >= 0 {
		own = n.sliders[level]
	}

	mine := "level " + itoa(level)
	checkEq(t, "SetStr on the way down", k.SetStr(own, keyLabel, mine), true)

	next := n.sliders[level+1]
	checkEq(t, "SetNum on the way down", k.SetNum(next, keySetValue, n.base+float64(level+1)+0.5), true)

	checkEq(t, "depth on the way back", n.depth, level+2)
	checkStr(t, "this level's string on the way back", k.GetStr(own, keyLabel), mine)

	if v, ok := k.EvalNumber("1+" + itoa(level)); !ok || v != float64(level+1) {
		t.Errorf("EvalNumber on the way back at level %d = %v, %v", level, v, ok)
	}
}
