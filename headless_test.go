// SPDX-License-Identifier: GPL-2.0-or-later

package bui

import (
	"fmt"
	"image"
	"image/color"
	"slices"
	"testing"

	"github.com/CWBudde/bui-go/internal/engine"
)

// The raster comes out at the size of the last render, opaque, with the
// button drawn in other colours than the background below it.
func TestHeadlessImage(t *testing.T) {
	_, btn, h := buttonInFrame(t)

	img := h.Image()
	if got, want := img.Bounds(), image.Rect(0, 0, 320, 120); got != want {
		t.Fatalf("Image bounds = %v, want %v", got, want)
	}

	x, y, w, ht := btn.Rect()
	if inside, below := img.RGBAAt(x+w/2, y+ht/2), img.RGBAAt(x+w/2, y+ht+40); inside == below {
		t.Errorf("the button's centre and the background below it are both %v", inside)
	}
}

// Every pixel matches the kit's 0x00RRGGBB, alpha 255, on a slider, whose
// fill has colour, so that swapped channels show.
func TestHeadlessImageChannels(t *testing.T) {
	_, _, h := sliderInFrame(t)

	img, raw := h.Image(), kit.RasterPixels(h.r)
	coloured := 0

	for i, p := range raw {
		want := color.RGBA{R: byte(p >> 16), G: byte(p >> 8), B: byte(p), A: 0xff}
		if got := img.RGBAAt(i%320, i/320); got != want {
			t.Fatalf("pixel %d = %v, want %v from %#08x", i, got, want, p)
		}

		if want.R != want.B {
			coloured++
		}
	}

	if coloured == 0 {
		t.Fatal("no pixel with red unlike blue, so the test cannot see swapped channels")
	}
}

// Image is a copy: drawing into it changes nothing in the next one.
func TestHeadlessImageIsACopy(t *testing.T) {
	_, _, h := buttonInFrame(t)

	first := h.Image()
	first.Pix[0] ^= 0xff

	if second := h.Image(); second.Pix[0] == first.Pix[0] {
		t.Error("a change to one Image showed in the next")
	}
}

// Resize takes effect with the next render, and refuses what NewHeadless refuses.
func TestHeadlessResize(t *testing.T) {
	_, btn, h := buttonInFrame(t)

	h.Resize(200, 80)

	if got, want := h.Image().Bounds(), image.Rect(0, 0, 320, 120); got != want {
		t.Fatalf("Image bounds before the next render = %v, want %v", got, want)
	}

	h.Render()

	if got, want := h.Image().Bounds(), image.Rect(0, 0, 200, 80); got != want {
		t.Fatalf("Image bounds after Resize and Render = %v, want %v", got, want)
	}

	if _, _, w, _ := btn.Rect(); w > 200 {
		t.Errorf("the packed button is %d px wide in a 200 px raster", w)
	}

	for _, size := range [][2]int{{0, 10}, {10, -1}, {100000, 100000}} {
		mustPanic(t, "Resize with a bad size", func() { h.Resize(size[0], size[1]) })
	}
}

// A Resize waits for a render, even when nothing in the tree changed, so a host
// that draws only on NeedsRender still follows the new size.
func TestHeadlessNeedsRenderAfterResize(t *testing.T) {
	_, _, h := buttonInFrame(t)

	if h.NeedsRender() {
		t.Fatal("NeedsRender right after Render")
	}

	h.Resize(200, 80)

	if !h.NeedsRender() {
		t.Fatal("no NeedsRender after a Resize")
	}

	h.Resize(320, 120)

	if h.NeedsRender() {
		t.Error("NeedsRender after a Resize back to the rendered size")
	}

	h.Resize(200, 80)
	h.Render()

	if h.NeedsRender() {
		t.Error("NeedsRender after rendering the new size")
	}
}

// pressSpy records the modifiers that reach the kit's press and release.
type pressSpy struct {
	engine.Kit

	calls []string
}

func (s *pressSpy) PressMods(ctx engine.Context, x, y, mods int) {
	s.calls = append(s.calls, fmt.Sprintf("press %d", mods))
	s.Kit.PressMods(ctx, x, y, mods)
}

func (s *pressSpy) ReleaseMods(ctx engine.Context, x, y, mods int) {
	s.calls = append(s.calls, fmt.Sprintf("release %d", mods))
	s.Kit.ReleaseMods(ctx, x, y, mods)
}

// PressMods hands the kit the modifiers with ModButton1 added, and ReleaseMods
// without it, whatever the caller passes.
func TestHeadlessPressModsReachTheKit(t *testing.T) {
	_, btn, h := buttonInFrame(t)

	spy := &pressSpy{Kit: kit}
	kit = spy

	t.Cleanup(func() { kit = spy.Kit })

	x, y, w, ht := btn.Rect()
	cx, cy := x+w/2, y+ht/2

	h.PressMods(cx, cy, ModShift)
	h.ReleaseMods(cx, cy, ModShift|ModButton1)
	h.PressMods(cx, cy, ModCtrl|ModButton1)
	h.ReleaseMods(cx, cy, ModCtrl)

	want := []string{
		fmt.Sprintf("press %d", ModShift|ModButton1),
		fmt.Sprintf("release %d", ModShift),
		fmt.Sprintf("press %d", ModCtrl|ModButton1),
		fmt.Sprintf("release %d", ModCtrl),
	}
	if !slices.Equal(spy.calls, want) {
		t.Errorf("calls %q, want %q", spy.calls, want)
	}
}

// Hovering a button needs a render, which redraws part of the raster, and a
// second render then has nothing to do.
func TestHeadlessRenderDamage(t *testing.T) {
	root, _, h := buttonInFrame(t)

	// The kit damages rows 32 px high, so a button below y = 64 has damage that
	// starts well below 0, which shows a mix-up of width and right edge.
	var btn *Node

	for range 3 {
		btn = NewNode("button")
		root.Add(btn)
		btn.Pack(PackOptions{Fill: FillX})
	}

	h.Render()

	if h.NeedsRender() {
		t.Fatal("NeedsRender right after Render")
	}

	if got := h.RenderDamage(false); len(got) != 0 {
		t.Fatalf("RenderDamage with nothing changed = %v, want none", got)
	}

	x, y, w, ht := btn.Rect()
	if !h.Motion(x+w/2, y+ht/2, 0) {
		t.Fatal("Motion onto the button changed nothing")
	}

	if !h.NeedsRender() {
		t.Fatal("no NeedsRender after hovering the button")
	}

	damage := h.RenderDamage(false)
	if len(damage) == 0 {
		t.Fatal("RenderDamage after hovering = none, want regions")
	}

	var hovered image.Rectangle

	bounds := image.Rect(0, 0, 320, 120)
	for _, r := range damage {
		if r.Empty() || !r.In(bounds) {
			t.Errorf("damage %v is empty or outside %v", r, bounds)
		}

		hovered = hovered.Union(r)
	}

	if want := image.Rect(x, y, x+w, y+ht); y < 64 || !want.In(hovered) {
		t.Errorf("damage %v does not cover the hovered button at %v, below y = 64", damage, want)
	}

	if got := h.RenderDamage(false); len(got) != 0 {
		t.Errorf("second RenderDamage = %v, want none", got)
	}

	if got := h.RenderDamage(true); len(got) != 0 {
		t.Errorf("a full RenderDamage of the same pixels = %v, want none", got)
	}

	h.Resize(200, 80)

	var union image.Rectangle
	for _, r := range h.RenderDamage(false) {
		union = union.Union(r)
	}

	if want := image.Rect(0, 0, 200, 80); union != want {
		t.Errorf("damage after a Resize covers %v, want %v", union, want)
	}
}

// dragSlider drags s from a quarter of its width 60 px to the right, with mods
// held, and returns the value it ends at.
func dragSlider(h *Headless, s *Node, mods Mod) float64 {
	x, y, w, ht := s.Rect()
	cy, x0 := y+ht/2, x+w/4

	h.PressMods(x0, cy, mods)
	h.Motion(x0+60, cy, ModButton1|mods)
	h.ReleaseMods(x0+60, cy, mods)

	return s.Num("value")
}

// Shift drags a slider finer, as in Blender, so the modifiers reach the kit.
func TestHeadlessShiftDragIsFiner(t *testing.T) {
	_, s, h := sliderInFrame(t)

	plain := dragSlider(h, s, 0) - 2.5

	if err := s.SetNum("value", 2.5); err != nil {
		t.Fatal(err)
	}

	fine := dragSlider(h, s, ModShift) - 2.5

	if plain <= 0 || fine <= 0 || fine >= plain/2 {
		t.Errorf("a drag moved the value by %v, and with Shift by %v: want a far smaller step with Shift", plain, fine)
	}
}

// A press, motion with the button held, and a release drag a slider to the right.
func TestHeadlessDragSlider(t *testing.T) {
	_, s, h := sliderInFrame(t)

	var got []float64

	s.OnChange(func(v float64) { got = append(got, v) })

	x, y, w, ht := s.Rect()
	cy, x0 := y+ht/2, x+w/4

	h.PressMods(x0, cy, 0)

	for _, dx := range []int{20, 60, 100} {
		h.Motion(x0+dx, cy, ModButton1)
	}

	h.ReleaseMods(x0+100, cy, 0)

	if len(got) < 2 {
		t.Fatalf("%d change events from a drag, want several: %v", len(got), got)
	}

	for i := 1; i < len(got); i++ {
		if got[i] <= got[i-1] {
			t.Errorf("a rightward drag went from %v to %v", got[i-1], got[i])
		}
	}
}

// textInFrame builds a text field that fills a frame and renders it once.
func textInFrame(t *testing.T, text string) (txt *Node, h *Headless) {
	t.Helper()

	root := NewNode("frame")
	txt = NewNode("text")

	if err := txt.SetStr("text", text); err != nil {
		t.Fatal(err)
	}

	root.Add(txt)
	txt.Pack(PackOptions{Fill: FillBoth, Expand: true})
	t.Cleanup(root.Close)
	h = NewHeadless(root, 300, 200)
	t.Cleanup(h.Close)
	h.Render()

	return txt, h
}

// Clicking a text field and typing edits it, and Return commits the text.
func TestHeadlessTyping(t *testing.T) {
	txt, h := textInFrame(t, "")

	x, y, w, ht := txt.Rect()
	h.Press(x+w/2, y+ht/2)
	h.Release(x+w/2, y+ht/2)

	for _, k := range []struct{ text, sym string }{
		{"a", "a"}, {"B", "B"}, {"ä", "adiaeresis"}, {"", "BackSpace"}, {"c", "c"},
	} {
		if !h.Key(k.text, k.sym, 0) {
			t.Errorf("Key(%q, %q) changed nothing", k.text, k.sym)
		}
	}

	h.Key("", "Return", 0)

	if got := txt.Str("text"); got != "aBc" {
		t.Errorf("typed text = %q, want %q", got, "aBc")
	}
}

// A held Ctrl reaches the kit with a key: Ctrl+A selects the whole text, and
// typing then replaces it.
func TestHeadlessKeyMods(t *testing.T) {
	txt, h := textInFrame(t, "ab")

	x, y, w, ht := txt.Rect()
	h.Press(x+w/2, y+ht/2)
	h.Release(x+w/2, y+ht/2)
	h.Key("", "a", ModCtrl)

	if !h.Key("x", "x", 0) {
		t.Fatal("typing over the selection changed nothing")
	}

	h.Key("", "Return", 0)

	if got := txt.Str("text"); got != "x" {
		t.Errorf("text after Ctrl+A and typing = %q, want %q", got, "x")
	}
}

// The wheel and precise scrolling, with Ctrl held, step a slider up.
func TestHeadlessWheelAndScroll(t *testing.T) {
	_, s, h := sliderInFrame(t)

	if err := s.SetNum("max", 100); err != nil {
		t.Fatal(err)
	}

	if err := s.SetNum("value", 50); err != nil {
		t.Fatal(err)
	}

	x, y, w, ht := s.Rect()
	cx, cy := x+w/2, y+ht/2
	h.Motion(cx, cy, 0)

	if !h.Wheel(cx, cy, 1, ModCtrl) {
		t.Fatal("Ctrl+wheel up on a slider changed nothing")
	}

	afterWheel := s.Num("value")
	if afterWheel <= 50 {
		t.Fatalf("Ctrl+wheel up left the value at %v", afterWheel)
	}

	if !h.Scroll(cx, cy, 0, 1, ModCtrl) {
		t.Fatal("Ctrl+scroll up on a slider changed nothing")
	}

	if v := s.Num("value"); v <= afterWheel {
		t.Errorf("Ctrl+scroll up went from %v to %v", afterWheel, v)
	}
}

// A context menu on a text field draws a popup, and Escape closes it again.
func TestHeadlessContextMenu(t *testing.T) {
	txt, h := textInFrame(t, "words")

	before := h.Image()
	x, y, w, ht := txt.Rect()
	h.Motion(x+w/2, y+ht/2, 0)

	if !h.ContextMenu(x+w/2, y+ht/2, 0) {
		t.Fatal("ContextMenu on a text field changed nothing")
	}

	h.Render()

	if equalImages(before, h.Image()) {
		t.Error("ContextMenu showed nothing on a text field")
	}

	if !h.Key("", "Escape", 0) {
		t.Error("Escape did not close the context menu")
	}
}

func equalImages(a, b *image.RGBA) bool {
	if a.Bounds() != b.Bounds() {
		return false
	}

	for i := range a.Pix {
		if a.Pix[i] != b.Pix[i] {
			return false
		}
	}

	return true
}

// Every new method panics after Close, as Press does.
func TestHeadlessInputAfterClosePanics(t *testing.T) {
	_, _, h := buttonInFrame(t)
	h.Close()

	for name, f := range map[string]func(){
		"Image":        func() { h.Image() },
		"Resize":       func() { h.Resize(10, 10) },
		"RenderDamage": func() { h.RenderDamage(true) },
		"NeedsRender":  func() { h.NeedsRender() },
		"PressMods":    func() { h.PressMods(0, 0, 0) },
		"ReleaseMods":  func() { h.ReleaseMods(0, 0, 0) },
		"Motion":       func() { h.Motion(0, 0, 0) },
		"Wheel":        func() { h.Wheel(0, 0, 1, 0) },
		"Scroll":       func() { h.Scroll(0, 0, 0, 1, 0) },
		"Key":          func() { h.Key("a", "a", 0) },
		"ContextMenu":  func() { h.ContextMenu(0, 0, 0) },
		"SetScale":     func() { h.SetScale(2) },
		"CopyBGRA":     func() { h.CopyBGRA(nil, image.Rect(0, 0, 1, 1)) },
	} {
		mustPanic(t, "Headless."+name, f)
	}
}
