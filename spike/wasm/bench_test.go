// SPDX-License-Identifier: GPL-2.0-or-later

package wasmspike

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/experimental"
)

// benchTree is a tree to render, with a point on a button and one off it.
type benchTree struct {
	name  string
	w, h  int
	build func(Kit) (root, hot Node)
}

func benchTrees() []benchTree {
	return []benchTree{
		{"counter-400x300", 400, 300, func(k Kit) (Node, Node) {
			c := BuildCounter(k)

			return c.Root, c.Button
		}},
		{"dense-1920x1080", 1920, 1080, func(k Kit) (Node, Node) {
			root := BuildDense(k, 8, 40)

			return root, Node(-1) // resolved after layout, below
		}},
	}
}

// setupBench builds and renders a tree, and returns the headless and a point
// on a button plus a point on empty background, for hovering.
func setupBench(b *testing.B, k Kit, bt benchTree) (hl Headless, on, off [2]int) {
	b.Helper()

	root, hot := bt.build(k)
	hl = k.NewHeadless(root, bt.w, bt.h)

	b.Cleanup(func() {
		hl.Close()
		k.FreeTree(root)
	})
	hl.Render()

	if hot < 0 {
		// the dense tree: a node in the middle of the window is a button
		// every fourth row; take the third column's first button.
		hot = findButton(k, root)
	}

	r := k.Rect(hot)
	on = [2]int{r[0] + r[2]/2, r[1] + r[3]/2}
	off = [2]int{bt.w - 2, bt.h - 2}

	return hl, on, off
}

// findButton returns the first "button" in a depth-first walk of the dense
// tree, which BuildDense makes at column 0, row 2.
func findButton(k Kit, root Node) Node {
	// BuildDense creates nodes in order: root, then per column the column frame
	// and its rows. Column 0's row 2 is a button: root+1 (column) +3.
	n := root + 4
	if k.Str(n, "text") != "Button 2" {
		panic("dense tree layout changed: node is " + k.Str(n, "text"))
	}

	return n
}

// BenchmarkRender redraws the whole tree (BK_render with full = 1) per
// iteration, on the cgo kit and on both wazero engines.
func BenchmarkRender(b *testing.B) {
	ks := kits(b)

	for _, bt := range benchTrees() {
		for _, k := range ks {
			b.Run(bt.name+"/"+k.Name(), func(b *testing.B) {
				hl, _, _ := setupBench(b, k, bt)

				b.ReportAllocs()
				b.ResetTimer()

				for range b.N {
					hl.Render()
				}
			})
		}
	}
}

// BenchmarkHover moves the pointer onto a button and off it again, each
// followed by a damage-only render, as a window host does per motion event.
func BenchmarkHover(b *testing.B) {
	ks := kits(b)

	for _, bt := range benchTrees() {
		for _, k := range ks {
			b.Run(bt.name+"/"+k.Name(), func(b *testing.B) {
				hl, on, off := setupBench(b, k, bt)

				b.ReportAllocs()
				b.ResetTimer()

				for i := range b.N {
					p := off
					if i%2 == 0 {
						p = on
					}

					hl.Motion(p[0], p[1])
					hl.RenderDamage()
				}
			})
		}
	}
}

// BenchmarkStartup measures what the lazy module load in Phase 2 would cost:
// a new runtime, compile, instantiate and _initialize. "cold" has no cache,
// "cached" a new cache over a populated directory, as in os.UserCacheDir.
func BenchmarkStartup(b *testing.B) {
	bin, err := ReadModule("bui_kit.wasm")
	skipIfNotBuilt(b, err)

	for _, engine := range Engines {
		for _, workers := range []int{1, 4} {
			b.Run(string(engine)+"/cold/workers="+strconv.Itoa(workers), func(b *testing.B) {
				ctx := experimental.WithCompilationWorkers(context.Background(), workers)

				for range b.N {
					k, err := NewWasmKit(ctx, engine, nil, bin, nil)
					if err != nil {
						b.Fatal(err)
					}

					k.Close()
				}
			})
		}

		b.Run(string(engine)+"/cached", func(b *testing.B) {
			ctx := context.Background()
			dir := b.TempDir()

			// Warm the directory through a cache that is closed again. One cache
			// instance also shares compiled code in memory, so each iteration
			// opens a new one: only the files on disk carry over, as in a new
			// process.
			warmCache, err := wazero.NewCompilationCacheWithDir(dir)
			if err != nil {
				b.Fatal(err)
			}

			warm, err := NewWasmKit(ctx, engine, warmCache, bin, nil)
			if err != nil {
				b.Fatal(err)
			}

			warm.Close()

			if err := warmCache.Close(ctx); err != nil {
				b.Fatal(err)
			}

			b.ResetTimer()

			for range b.N {
				cache, err := wazero.NewCompilationCacheWithDir(dir)
				if err != nil {
					b.Fatal(err)
				}

				k, err := NewWasmKit(ctx, engine, cache, bin, nil)
				if err != nil {
					b.Fatal(err)
				}

				k.Close()

				if err := cache.Close(ctx); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkVariants renders with the cgo kit and every module saved in
// build/wasm/variants, in one process, so that their ratios share the
// machine's state. The variants are build experiments (see PLAN.md); run with
// BUI_SPIKE_VARIANTS=1.
func BenchmarkVariants(b *testing.B) {
	if os.Getenv("BUI_SPIKE_VARIANTS") == "" {
		b.Skip("diagnostic; set BUI_SPIKE_VARIANTS=1")
	}

	dir := filepath.Join(OutDir(), "..", "variants")

	files, err := filepath.Glob(filepath.Join(dir, "*.wasm"))
	if err != nil || len(files) == 0 {
		b.Skipf("no variants in %s", dir)
	}

	ks := []Kit{}

	if NativeAvailable {
		nk := NewNativeKit()
		b.Cleanup(nk.Close)
		ks = append(ks, nk)
	}

	names := map[Kit]string{}

	for _, f := range files {
		bin, err := os.ReadFile(f)
		if err != nil {
			b.Fatal(err)
		}

		wk, err := NewWasmKit(context.Background(), Compiler, nil, bin, nil)
		if err != nil {
			b.Fatal(err)
		}

		b.Cleanup(wk.Close)
		ks = append(ks, wk)
		names[wk] = strings.TrimSuffix(filepath.Base(f), ".wasm")
	}

	for _, bt := range benchTrees() {
		for _, k := range ks {
			name := k.Name()
			if n, ok := names[k]; ok {
				name = n
			}

			b.Run(bt.name+"/"+name, func(b *testing.B) {
				hl, _, _ := setupBench(b, k, bt)

				b.ResetTimer()

				for range b.N {
					hl.Render()
				}
			})
		}
	}
}
