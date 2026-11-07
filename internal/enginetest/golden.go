// SPDX-License-Identifier: GPL-2.0-or-later

package enginetest

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/CWBudde/bui-go/internal/engine"
)

// Golden renders every tree on ref and other, and fails unless the pixels match at
// the tolerance. It logs each tree's comparison.
func Golden(t *testing.T, ref, other engine.Kit) {
	t.Helper()
	ref.SetEmit(ignoreEmit)
	other.SetEmit(ignoreEmit)

	for _, c := range goldenCases() {
		t.Run(c.name, func(t *testing.T) {
			t.Helper()

			if c.long && testing.Short() {
				t.Skip("skipping the full-HD tree with -short")
			}

			compareRuns(t, c, runCase(t, ref, c), runCase(t, other, c))
		})
	}
}

// PinnedPixels holds every stage of every case to the hash in file.
func PinnedPixels(t *testing.T, k engine.Kit, file string) {
	t.Helper()

	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]string{}

	for line := range strings.Lines(string(data)) {
		if name, hash, ok := strings.Cut(strings.TrimSpace(line), " "); ok {
			want[name] = hash
		}
	}

	for name, hash := range pixelHashes(t, k, testing.Short()) {
		if want[name] != hash {
			t.Errorf("%s: pixels hash to %s, pinned %q", name, hash, want[name])
		}
	}
}

// UpdatePinnedPixels rewrites file for PinnedPixels.
func UpdatePinnedPixels(t *testing.T, k engine.Kit, file string) {
	t.Helper()

	hashes := pixelHashes(t, k, false)
	lines := make([]string, 0, len(hashes))

	for name, hash := range hashes {
		lines = append(lines, name+" "+hash+"\n")
	}

	slices.Sort(lines)

	if err := os.WriteFile(file, []byte(strings.Join(lines, "")), 0o600); err != nil {
		t.Fatal(err)
	}
}

// pixelHashes draws every case and hashes each stage, by "case/stage".
func pixelHashes(t *testing.T, k engine.Kit, skipLong bool) map[string]string {
	t.Helper()
	k.SetEmit(ignoreEmit)

	hashes := map[string]string{}

	for _, c := range goldenCases() {
		if c.long && skipLong {
			continue
		}

		for i, pixels := range runCase(t, k, c) {
			hashes[c.name+"/"+c.stages[i].name] = pixelHash(pixels)
		}
	}

	return hashes
}

func pixelHash(pixels []uint32) string {
	h := sha256.New()

	if err := binary.Write(h, binary.LittleEndian, pixels); err != nil {
		panic(err) // a hash never fails to write
	}

	return hex.EncodeToString(h.Sum(nil))
}

// compareRuns compares two engines' pixels at every stage of a case.
func compareRuns(t *testing.T, c goldenCase, a, b [][]uint32) {
	t.Helper()

	for i, st := range c.stages {
		d := comparePixels(a[i], b[i])
		t.Logf("%s/%s: %v", c.name, st.name, d)

		if !d.within() {
			t.Errorf("%s/%s: outside the Phase 0 tolerance: %v", c.name, st.name, d)
			writeFailure(t, c.name+"-"+st.name, a[i], b[i], c.w, c.h)
		}
	}
}

// goldenCase is a tree, built afresh on each kit, and the states compared.
type goldenCase struct {
	name   string
	w, h   int
	long   bool
	build  func(*goldenRun)
	stages []goldenStage
}

// stageDrawn names the first stage of a case with no other meaning for it.
const stageDrawn = "drawn"

// goldenStage is one state of a tree. act leads to it from the stage before, and may
// be nil for the first stage. check proves the action did what the case claims: a
// comparison of two closed menus would pass and prove nothing.
type goldenStage struct {
	name  string
	act   func(*goldenRun)
	check func(r *goldenRun, before, after []uint32)
	// damageOnly draws through RenderDamage, as a window does, not a full render.
	damageOnly bool
}

// goldenRun is one case on one kit.
type goldenRun struct {
	*harness

	s     *scene
	nodes map[string]engine.Node
	state int
	first []uint32 // the first stage's pixels
	box   rect     // what a check found for a later stage
}

// named creates a node and remembers it for the stages.
func (r *goldenRun) named(name, typ string) engine.Node {
	r.t.Helper()

	n := r.node(typ)
	r.nodes[name] = n

	return n
}

// runCase builds the tree on k and returns the pixels of each stage.
func runCase(t *testing.T, k engine.Kit, c goldenCase) [][]uint32 {
	t.Helper()

	r := &goldenRun{harness: &harness{t: t, k: k}, nodes: map[string]engine.Node{}}
	r.nodes["root"] = r.node(typeFrame)
	c.build(r)

	// The receiver set by a case is process-wide: it goes before the other kit runs.
	defer k.SetEmit(ignoreEmit)

	r.s = r.scene(r.nodes["root"], c.w, c.h)
	defer r.s.close()

	out := make([][]uint32, 0, len(c.stages))

	var before []uint32

	for _, st := range c.stages {
		if st.act != nil {
			st.act(r)
		}

		var px []uint32

		if st.damageOnly {
			k.RenderDamage(r.s.ctx, r.s.r, c.w, c.h, false)
			px = r.s.pixels()
		} else {
			px = r.s.render()
		}

		if st.check != nil {
			st.check(r, before, px)
		}

		if r.first == nil {
			r.first = px
		}

		out = append(out, px)
		before = px
	}

	return out
}

// requireChanged is a check that at least minPixels changed outside node skip, or
// anywhere if skip is "".
func requireChanged(what, skip string, minPixels int) func(*goldenRun, []uint32, []uint32) {
	return func(r *goldenRun, before, after []uint32) {
		r.t.Helper()

		var outside rect
		if skip != "" {
			outside = r.rectOf(r.nodes[skip])
		}

		n := changedPixels(before, after, r.s.w, outside)
		r.t.Logf("%s changed %d pixels, need %d", what, n, minPixels)

		if n < minPixels {
			r.t.Errorf("%s changed %d pixels, want at least %d: the case proves nothing", what, n, minPixels)
		}
	}
}

func goldenCases() []goldenCase {
	return []goldenCase{
		goldenButtons(),
		goldenSliders(),
		goldenText(),
		goldenMenu(),
		goldenScroll(),
		goldenInput(),
		goldenClick(),
		goldenDense(),
	}
}

func goldenButtons() goldenCase {
	return goldenCase{
		name: "buttons", w: 400, h: 200,
		build: func(r *goldenRun) {
			root := r.nodes["root"]
			r.num(root, "padding", 4)

			for _, icon := range []string{"ADD", "TRASH", "FILE_FOLDER", "PREFERENCES"} {
				b := r.node(typeButton)
				r.str(b, keyText, icon)
				r.str(b, "icon", icon)
				r.packIn(root, b, pack{fill: fillX, padY0: 2})
			}

			r.packIn(root, r.textNode(typeButton, "Text only"), pack{fill: fillX, padY0: 2})

			iconOnly := r.named("iconOnly", typeButton)
			r.str(iconOnly, "icon", "ADD")
			r.packIn(root, iconOnly, pack{side: sideLeft, padY0: 2})
		},
		stages: []goldenStage{
			{name: stageDrawn},
			{
				name: "pressed",
				act: func(r *goldenRun) {
					x, y := r.s.center(r.nodes["iconOnly"])
					r.k.Press(r.s.ctx, x, y)
				},
				check: requireChanged("a press on the icon button", "", 1),
			},
			{
				name: "released",
				act: func(r *goldenRun) {
					x, y := r.s.center(r.nodes["iconOnly"])
					r.k.Release(r.s.ctx, x, y)
				},
				check: requireChanged("a release on the icon button", "", 1),
			},
		},
	}
}

// textNode creates a node with a text.
func (r *goldenRun) textNode(typ, text string) engine.Node {
	r.t.Helper()

	n := r.node(typ)
	r.str(n, keyText, text)

	return n
}

func goldenSliders() goldenCase {
	return goldenCase{
		name: "sliders", w: 400, h: 300,
		build: func(r *goldenRun) {
			root := r.nodes["root"]
			r.num(root, "padding", 6)

			for i, v := range []float64{0, 1.5, 3.14159, 5, 7.77, 10} {
				s := r.node(typeSlider)
				r.str(s, keyLabel, "Value "+itoa(i))
				r.num(s, "min", 0)
				r.num(s, "max", 10)
				r.num(s, keyValue, v)
				r.packIn(root, s, pack{fill: fillX, padY0: 2})
			}

			r.nodes["slider"] = r.k.NodeChild(root, 2)

			off := r.named("check", typeCheck)
			r.str(off, keyLabel, "Starts off")
			r.packIn(root, off, pack{fill: fillX, padY0: 2})

			on := r.node(typeCheck)
			r.str(on, keyLabel, "Starts on")
			r.num(on, keyValue, 1)
			r.packIn(root, on, pack{fill: fillX, padY0: 2})
		},
		stages: []goldenStage{
			{name: stageDrawn},
			{
				name:  "slider-changed",
				act:   func(r *goldenRun) { r.num(r.nodes["slider"], keyValue, 8.25) },
				check: requireChanged("a new slider value", "", 100),
			},
			{
				name:  "checked",
				act:   func(r *goldenRun) { r.s.click(r.nodes["check"]) },
				check: requireChanged("a click on the checkbox", "", 10),
			},
		},
	}
}

func goldenText() goldenCase {
	return goldenCase{
		name: "text", w: 500, h: 360,
		build: func(r *goldenRun) {
			root := r.nodes["root"]
			r.num(root, "padding", 6)

			for _, s := range []string{
				"Plain ASCII: the quick brown fox",
				"Deutsch: Grüße aus der Straße, Äpfel über Öl",
				"Русский: съешь ещё этих мягких булок",
				textCJK,
			} {
				r.packIn(root, r.textNode(typeLabel, s), pack{fill: fillX, padY0: 2})
			}

			r.packIn(root, r.named("changing", typeLabel), pack{fill: fillX, padY0: 2})
			r.str(r.nodes["changing"], keyText, "Ändert sich")

			field := r.named("field", typeText)
			r.str(field, keyValue, "A value in a text field: äöü Ж")
			r.packIn(root, field, pack{fill: fillX, padY0: 2})

			area := r.named("area", typeArea)
			r.str(area, keyValue, "First line of the text area\nZweite Zeile: äöü\nТретья строка\n\nAfter an empty line")
			r.packIn(root, area, pack{fill: fillBoth, expand: true, padY0: 2})
		},
		stages: []goldenStage{
			{
				name: stageDrawn,
				check: func(r *goldenRun, _, _ []uint32) {
					checkStr(r.t, "text field", r.k.GetStr(r.nodes["field"], keyValue), "A value in a text field: äöü Ж")

					if h := r.rectOf(r.nodes["area"]).h; h < 80 {
						r.t.Errorf("text area is %d pixels high, too low for its lines", h)
					}
				},
			},
			{
				name:  "label-changed",
				act:   func(r *goldenRun) { r.str(r.nodes["changing"], keyText, textCJKLabel) },
				check: requireChanged("a new label text", "", 20),
			},
			{
				name:  "field-changed",
				act:   func(r *goldenRun) { r.str(r.nodes["field"], keyValue, "Ein anderer Wert") },
				check: requireChanged("a new field value", "", 20),
			},
			{
				name:  "area-changed",
				act:   func(r *goldenRun) { r.str(r.nodes["area"], keyValue, "Only one line now") },
				check: requireChanged("a new text area value", "", 20),
			},
		},
	}
}

// menuMinPixels is what the open menu must change outside the dropdown, at least
// eight rows of 20 pixels by 100. It proves that the popup opened, as a press also
// redraws the dropdown itself.
const menuMinPixels = 8 * 20 * 100

// checkMenuOpen finds the popup, by what it changed since the closed render.
func checkMenuOpen(r *goldenRun, _, after []uint32) {
	r.t.Helper()

	box, n := changedBox(r.first, after, r.s.w, r.rectOf(r.nodes["drop"]))
	r.t.Logf("the open menu changed %d pixels outside the dropdown, in %+v, need %d", n, box, menuMinPixels)

	if n < menuMinPixels {
		r.t.Errorf("the menu changed %d pixels, want at least %d: it did not open, so the case proves nothing",
			n, menuMinPixels)
	}

	r.box = box
}

func goldenMenu() goldenCase {
	const initial = 2

	press := func(r *goldenRun) {
		x, y := r.s.center(r.nodes["drop"])
		r.k.Press(r.s.ctx, x, y)
	}
	release := func(r *goldenRun) {
		x, y := r.s.center(r.nodes["drop"])
		r.k.Release(r.s.ctx, x, y)
	}
	pick := func(r *goldenRun) {
		r.t.Helper()

		x, y := r.box.x+r.box.w/2, r.box.y+r.box.h/2
		r.k.Press(r.s.ctx, x, y)
		r.k.Release(r.s.ctx, x, y)
	}

	return goldenCase{
		name: "menu", w: 400, h: 320,
		build: func(r *goldenRun) {
			root := r.nodes["root"]
			r.num(root, "padding", 8)

			d := r.named("drop", typeDrop)
			r.str(d, keyLabel, "Choose")

			if !r.k.SetStrs(d, keyItems, []string{
				"First item", "Second item", "Third: äöü", "Четвёртый", "Fifth item", "Sixth item", "Seventh", "Eighth item",
			}) {
				r.t.Fatal("SetStrs(items) = false")
			}

			r.num(d, keyValue, initial)
			r.packIn(root, d, pack{fill: fillX})
			r.packIn(root, r.textNode(typeLabel, "Below the dropdown"), pack{fill: fillX, padY0: 8})
		},
		stages: []goldenStage{
			{name: "closed"},
			{name: "pressed", act: press, check: checkMenuOpen},
			{name: "released", act: release, check: checkMenuOpen},
			{
				name: "picked",
				act:  pick,
				check: func(r *goldenRun, before, after []uint32) {
					r.t.Helper()

					v := r.k.GetNum(r.nodes["drop"], keyValue)
					r.t.Logf("picking an item took the value from %d to %v", initial, v)

					if v == initial {
						r.t.Errorf("picking an item left the value at %v", v)
					}

					requireChanged("closing the menu", "drop", menuMinPixels)(r, before, after)
				},
			},
		},
	}
}

func goldenScroll() goldenCase {
	const scrollBy = 137

	scrollTo := func(v float64) func(*goldenRun) {
		return func(r *goldenRun) { r.num(r.nodes["scroll"], keyScroll, v) }
	}

	return goldenCase{
		name: "scroll", w: 300, h: 240,
		build: func(r *goldenRun) {
			view := r.named("scroll", typeScroll)
			column := r.named("column", typeColumn)
			r.packIn(r.nodes["root"], view, pack{fill: fillBoth, expand: true})
			r.add(view, column)

			for i := range 40 {
				var n engine.Node

				if i%2 == 0 {
					n = r.textNode(typeButton, "Row "+itoa(i))
				} else {
					n = r.textNode(typeLabel, "Row "+itoa(i))
				}

				if i == 0 {
					r.nodes["first"] = n
				}

				r.add(column, n)
			}
		},
		stages: []goldenStage{
			{name: "top"},
			{
				name: "scrolled",
				act:  scrollTo(scrollBy),
				check: func(r *goldenRun, before, after []uint32) {
					r.t.Helper()
					requireChanged("scrolling", "", 5000)(r, before, after)
					checkEq(r.t, "scroll position", r.k.GetNum(r.nodes["scroll"], keyScroll), scrollBy)
					checkEq(r.t, "first row's y", r.rectOf(r.nodes["first"]).y, -scrollBy)
				},
			},
			{name: "back-at-top", act: scrollTo(0), check: requireChanged("scrolling back", "", 5000)},
			{
				name: "past-the-end",
				act:  scrollTo(1 << 20),
				check: func(r *goldenRun, before, after []uint32) {
					r.t.Helper()
					requireChanged("scrolling to the end", "", 5000)(r, before, after)

					if v := r.k.GetNum(r.nodes["scroll"], keyScroll); v >= 1<<20 || v <= scrollBy {
						r.t.Errorf("scroll position %v, want it clamped to the end", v)
					}
				},
			},
		},
	}
}

// goldenInput walks a window's input paths, drawing only damage, so that hover, drag,
// wheel, typing and the context menu all have to leave the same pixels.
func goldenInput() goldenCase { //nolint:funlen // a list of stages
	center := func(r *goldenRun, name string) (x, y int) {
		r.t.Helper()

		return r.s.center(r.nodes[name])
	}

	changed := func(what string) func(*goldenRun, []uint32, []uint32) {
		return requireChanged(what, "", 20)
	}

	return goldenCase{
		name: "input", w: 400, h: 300,
		build: func(r *goldenRun) {
			root := r.nodes["root"]
			r.num(root, "padding", 8)

			button := r.named("button", typeButton)
			r.str(button, keyText, "Hover me")
			r.str(button, "description", "A tooltip")
			r.packIn(root, button, pack{fill: fillX})

			slider := r.named("slider", typeSlider)
			r.num(slider, "min", 0)
			r.num(slider, "max", 100)
			r.num(slider, keyValue, 20)
			r.packIn(root, slider, pack{fill: fillX, padY0: 6})

			text := r.named("text", typeText)
			r.str(text, keyText, "abc")
			r.packIn(root, text, pack{fill: fillX, padY0: 6})
		},
		stages: []goldenStage{
			{name: stageDrawn},
			{
				name: "hover", damageOnly: true, check: changed("hovering"),
				act: func(r *goldenRun) {
					x, y := center(r, "button")
					r.k.Motion(r.s.ctx, x, y, 0)
				},
			},
			{
				name: "slider-drag", damageOnly: true, check: changed("dragging"),
				act: func(r *goldenRun) {
					x, y := center(r, "slider")
					r.k.Motion(r.s.ctx, x, y, 0)
					r.k.PressMods(r.s.ctx, x, y, engine.ModButton1)
					r.k.Motion(r.s.ctx, x+40, y, engine.ModButton1)
					r.k.ReleaseMods(r.s.ctx, x+40, y, 0)
				},
			},
			{
				name: "wheel", damageOnly: true, check: changed("the wheel"),
				act: func(r *goldenRun) {
					x, y := center(r, "slider")
					r.k.Motion(r.s.ctx, x, y, 0)
					r.k.Wheel(r.s.ctx, x, y, 1, engine.ModCtrl)
					r.k.Scroll(r.s.ctx, x, y, 0, 1, engine.ModCtrl)
				},
			},
			{
				name: "typing", damageOnly: true, check: changed("typing"),
				act: func(r *goldenRun) {
					x, y := center(r, "text")
					r.k.Motion(r.s.ctx, x, y, 0)
					r.k.PressMods(r.s.ctx, x, y, engine.ModButton1)
					r.k.ReleaseMods(r.s.ctx, x, y, 0)
					r.k.Key(r.s.ctx, "ä", "adiaeresis", 0)
					r.k.Key(r.s.ctx, "€", "EuroSign", 0)
				},
			},
			{
				name: "committed", damageOnly: true, check: changed("committing"),
				act: func(r *goldenRun) { r.k.Key(r.s.ctx, "", "Return", 0) },
			},
			{
				name: "context-menu", damageOnly: true, check: changed("the context menu"),
				act: func(r *goldenRun) {
					x, y := center(r, "text")
					r.k.ContextMenu(r.s.ctx, x, y, 0)
				},
			},
		},
	}
}

// goldenClick is the state after input: the handler changes a label while the kit is
// inside Release.
func goldenClick() goldenCase {
	return goldenCase{
		name: "click", w: 400, h: 300,
		build: func(r *goldenRun) {
			root := r.nodes["root"]
			r.num(root, "padding", 8)

			size := r.named("size", typeSlider)
			r.str(size, keyLabel, "Step")
			r.num(size, "min", 1)
			r.num(size, "max", 10)
			r.num(size, keyValue, 3)

			count := r.named("count", typeLabel)
			r.str(count, keyText, "Count: 0")

			r.packIn(root, r.textNode(typeLabel, "Running for 0 s"), pack{fill: fillX})
			r.packIn(root, size, pack{fill: fillX, padY0: 6})
			r.packIn(root, count, pack{fill: fillX, padY0: 6})
			r.packIn(root, r.named("button", typeButton), pack{fill: fillX, padY0: 6})
			r.str(r.nodes["button"], keyText, "Add step")

			buttonID := r.k.NodeID(r.nodes["button"])
			r.k.SetEmit(func(id uint32, kind int, _ float64) {
				if id == buttonID && kind == engine.EvClick {
					r.state += int(r.k.GetNum(size, keyValue))
					checkEq(r.t, "SetStr in the handler", r.k.SetStr(count, keyText, "Count: "+itoa(r.state)), true)
				}
			})
		},
		stages: []goldenStage{
			{name: "before"},
			{
				name: "clicked",
				act:  func(r *goldenRun) { r.s.click(r.nodes["button"]) },
				check: func(r *goldenRun, before, after []uint32) {
					r.t.Helper()
					requireChanged("the click", "button", 10)(r, before, after)
					checkStr(r.t, "label after the click", r.k.GetStr(r.nodes["count"], keyText), "Count: 3")
				},
			},
			{
				name: "clicked-twice",
				act:  func(r *goldenRun) { r.s.click(r.nodes["button"]) },
				check: func(r *goldenRun, before, after []uint32) {
					r.t.Helper()
					requireChanged("the second click", "button", 5)(r, before, after)
					checkStr(r.t, "label after the second click", r.k.GetStr(r.nodes["count"], keyText), "Count: 6")
				},
			},
		},
	}
}

// goldenDense fills 1920x1080 with 8 columns of 40 widgets, as Phase 0's dense tree.
func goldenDense() goldenCase {
	return goldenCase{
		name: "dense-1080p", w: 1920, h: 1080, long: true,
		build: func(r *goldenRun) {
			root := r.nodes["root"]
			r.num(root, "padding", 4)

			for c := range 8 {
				col := r.node(typeFrame)
				r.packIn(root, col, pack{side: sideLeft, fill: fillBoth, expand: true, padX0: 2})

				for row := range 40 {
					r.packIn(col, r.denseWidget(c, row), pack{fill: fillX, padY0: 2})
				}
			}
		},
		stages: []goldenStage{{name: stageDrawn}},
	}
}

func (r *goldenRun) denseWidget(c, row int) engine.Node {
	r.t.Helper()

	switch (row + c) % 4 {
	case 0:
		return r.textNode(typeLabel, "Label "+itoa(c)+"."+itoa(row)+": the quick brown fox")
	case 1:
		n := r.node(typeSlider)
		r.str(n, keyLabel, "Value "+itoa(row))
		r.num(n, "min", 0)
		r.num(n, "max", 100)
		r.num(n, keyValue, float64((row*7+c*13)%100))

		return n
	case 2:
		return r.textNode(typeButton, "Button "+itoa(row))
	default:
		return r.textNode(typeCheck, "Option "+itoa(row))
	}
}
