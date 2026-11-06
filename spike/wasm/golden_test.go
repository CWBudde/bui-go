// SPDX-License-Identifier: GPL-2.0-or-later

package wasmspike

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// The go criterion from PLAN.md: exact, or a per-channel difference of at
// most 2 on at most 0.1% of the pixels.
const (
	tolChannel  = 2
	tolFraction = 0.001
)

// PixelDiff summarizes two rasters of the same size.
type PixelDiff struct {
	Total, Exact, OverTol int
	MaxChannel            int
}

func (d PixelDiff) String() string {
	return fmt.Sprintf("%d/%d exact (%.4f%%), %d differ by more than %d, largest channel difference %d",
		d.Exact, d.Total, 100*float64(d.Exact)/float64(d.Total), d.OverTol, tolChannel, d.MaxChannel)
}

// Within reports whether d meets the go criterion.
func (d PixelDiff) Within() bool {
	differ := d.Total - d.Exact

	return d.MaxChannel == 0 || (d.MaxChannel <= tolChannel && float64(differ) <= tolFraction*float64(d.Total))
}

func comparePixels(a, b []uint32) PixelDiff {
	d := PixelDiff{Total: len(a)}

	for i := range a {
		if a[i] == b[i] {
			d.Exact++

			continue
		}

		worst := 0

		for shift := 0; shift < 24; shift += 8 {
			ca, cb := int(a[i]>>shift&0xff), int(b[i]>>shift&0xff)
			worst = max(worst, ca-cb, cb-ca)
		}

		d.MaxChannel = max(d.MaxChannel, worst)
		if worst > tolChannel {
			d.OverTol++
		}
	}

	return d
}

// writePNG stores 0x00RRGGBB pixels for a human to look at.
func writePNG(t *testing.T, name string, px []uint32, w, h int) {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i, p := range px {
		img.SetRGBA(i%w, i/w, color.RGBA{R: uint8(p >> 16), G: uint8(p >> 8), B: uint8(p), A: 255})
	}

	dir := filepath.Join(OutDir(), "golden")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

// kits returns the native kit (when cgo is available) and the wasm kit on
// both engines; it skips when bui_kit.wasm is not built.
func kits(tb testing.TB) []Kit {
	tb.Helper()

	bin, err := ReadModule("bui_kit.wasm")
	skipIfNotBuilt(tb, err)

	var ks []Kit

	if NativeAvailable {
		nk := NewNativeKit()
		tb.Cleanup(nk.Close)
		ks = append(ks, nk)
	} else {
		tb.Log("no cgo kit in this build: comparing the wasm engines only")
	}

	for _, engine := range Engines {
		wk, err := NewWasmKit(context.Background(), engine, nil, bin, nil)
		if err != nil {
			tb.Fatal(err)
		}

		tb.Cleanup(wk.Close)
		ks = append(ks, wk)
	}

	return ks
}

// renderTree builds a tree with build, renders it at w x h, and returns the
// pixels, after an optional action on the rendered tree.
func renderTree(k Kit, w, h int, build func(Kit) Node, act func(Kit, Headless)) []uint32 {
	root := build(k)
	hl := k.NewHeadless(root, w, h)

	defer func() {
		hl.Close()
		k.FreeTree(root)
	}()

	hl.Render()

	if act != nil {
		act(k, hl)
		hl.Render()
	}

	return hl.Pixels()
}

// goldenCase is one tree compared across the kits.
type goldenCase struct {
	name  string
	w, h  int
	build func(Kit) Node
	act   func(Kit, Headless)
}

func goldenCases() []goldenCase {
	counter := func(k Kit) Node { return BuildCounter(k).Root }

	return []goldenCase{
		{name: "counter", w: 400, h: 300, build: counter},
		{name: "counter-hover", w: 400, h: 300, build: counter, act: func(_ Kit, hl Headless) {
			hl.Motion(200, 100)
		}},
		{name: "icons", w: 400, h: 120, build: func(k Kit) Node {
			root := k.NewNode("frame")
			for _, icon := range []string{"ADD", "TRASH", "FILE_FOLDER", "PREFERENCES"} {
				b := k.NewNode("button")
				must(k.SetStr(b, "text", icon), "text")
				must(k.SetStr(b, "icon", icon), "icon")
				k.Add(root, b)
				k.Pack(b, PackOptions{Fill: fillX, PadY0: 2})
			}

			return root
		}},
		{name: "dense-1080p", w: 1920, h: 1080, build: func(k Kit) Node { return BuildDense(k, 8, 40) }},
	}
}

// TestGoldenPixels renders the same trees on the cgo libraries and on the
// wasm module, compares the pixels and writes PNGs to build/wasm/out/golden.
func TestGoldenPixels(t *testing.T) {
	ks := kits(t)

	for _, gc := range goldenCases() {
		t.Run(gc.name, func(t *testing.T) {
			ref := renderTree(ks[0], gc.w, gc.h, gc.build, gc.act)
			writePNG(t, gc.name+"-"+ks[0].Name()+".png", ref, gc.w, gc.h)

			for _, k := range ks[1:] {
				px := renderTree(k, gc.w, gc.h, gc.build, gc.act)
				writePNG(t, gc.name+"-"+k.Name()+".png", px, gc.w, gc.h)

				d := comparePixels(ref, px)
				t.Logf("%s vs %s: %v", ks[0].Name(), k.Name(), d)

				if !d.Within() {
					t.Errorf("%s vs %s: outside the go tolerance: %v", ks[0].Name(), k.Name(), d)
				}
			}
		})
	}
}

// TestEmitSetStrDuringRelease drives one press and release on the counter's
// button. Its handler calls SetStr on another node while the kit is still
// inside BK_release, which on wasm means a host function calling back into
// the module.
func TestEmitSetStrDuringRelease(t *testing.T) {
	var after [][]uint32

	ks := kits(t)
	for _, k := range ks {
		t.Run(k.Name(), func(t *testing.T) {
			c := BuildCounter(k)
			must(k.SetNum(c.Size, "value", 3), "value")

			hl := k.NewHeadless(c.Root, 400, 300)
			defer func() {
				hl.Close()
				k.FreeTree(c.Root)
			}()

			hl.Render()

			r := k.Rect(c.Button)
			if r[2] == 0 || r[3] == 0 {
				t.Fatalf("button has no rect after render: %v", r)
			}

			x, y := r[0]+r[2]/2, r[1]+r[3]/2
			hl.Press(x, y)
			hl.Release(x, y)

			if c.Total != 3 {
				t.Errorf("handler total = %d, want 3 (one click at step 3)", c.Total)
			}

			if got := k.Str(c.Count, "text"); got != "Count: 3" {
				t.Errorf("label after click = %q, want %q", got, "Count: 3")
			}

			hl.Render()
			after = append(after, hl.Pixels())
		})
	}

	for i := 1; i < len(after) && len(after) == len(ks); i++ {
		d := comparePixels(after[0], after[i])
		t.Logf("after the click, %s vs %s: %v", ks[0].Name(), ks[i].Name(), d)

		if !d.Within() {
			t.Errorf("after the click, %s vs %s: outside the go tolerance: %v", ks[0].Name(), ks[i].Name(), d)
		}
	}
}
