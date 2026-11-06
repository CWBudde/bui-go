// SPDX-License-Identifier: GPL-2.0-or-later

package wasmspike

import (
	"context"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/experimental"
)

// countingListener counts calls per function.
type countingListener struct {
	counts map[string]int
	on     bool
}

func (l *countingListener) NewFunctionListener(def api.FunctionDefinition) experimental.FunctionListener {
	name := def.DebugName()

	return experimental.FunctionListenerFunc(func(context.Context, api.Module, api.FunctionDefinition, []uint64,
		experimental.StackIterator,
	) {
		if l.on {
			l.counts[name]++
		}
	})
}

// TestRenderCallCounts lists the functions called most during one full render
// of a tree. It is a diagnostic for the performance results in PLAN.md, run
// with BUI_SPIKE_PROFILE=1.
func TestRenderCallCounts(t *testing.T) {
	if os.Getenv("BUI_SPIKE_PROFILE") == "" {
		t.Skip("diagnostic; set BUI_SPIKE_PROFILE=1")
	}

	bin, err := ReadModule("bui_kit.wasm")
	skipIfNotBuilt(t, err)

	l := &countingListener{counts: map[string]int{}}
	ctx := experimental.WithFunctionListenerFactory(context.Background(), l)

	k, err := NewWasmKit(ctx, Interpreter, nil, bin, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()

	for _, tree := range []struct {
		name  string
		w, h  int
		build func(Kit) Node
	}{
		{"counter", 400, 300, func(k Kit) Node { return BuildCounter(k).Root }},
		{"dense", 1920, 1080, func(k Kit) Node { return BuildDense(k, 8, 40) }},
	} {
		root := tree.build(k)
		hl := k.NewHeadless(root, tree.w, tree.h)
		hl.Render() // warm caches: glyphs, icons, layout

		clear(l.counts)
		l.on = true

		hl.Render()

		l.on = false

		type fc struct {
			name string
			n    int
		}

		all := make([]fc, 0, len(l.counts))
		total := 0

		for name, n := range l.counts {
			all = append(all, fc{name, n})
			total += n
		}

		sort.Slice(all, func(i, j int) bool { return all[i].n > all[j].n })
		t.Logf("%s: %d calls in one full render", tree.name, total)

		for _, c := range all[:min(25, len(all))] {
			t.Logf("  %9d  %s", c.n, c.name)
		}

		hl.Close()
		k.FreeTree(root)
	}
}

// timingListener accumulates self time per function.
type timingListener struct {
	self  map[string]time.Duration
	stack []timingFrame
	on    bool
}

type timingFrame struct {
	name  string
	start time.Time
	child time.Duration
}

type timingFunc struct {
	l    *timingListener
	name string
}

func (l *timingListener) NewFunctionListener(def api.FunctionDefinition) experimental.FunctionListener {
	return &timingFunc{l: l, name: def.DebugName()}
}

func (f *timingFunc) Before(context.Context, api.Module, api.FunctionDefinition, []uint64, experimental.StackIterator) {
	if f.l.on {
		f.l.stack = append(f.l.stack, timingFrame{name: f.name, start: time.Now()})
	}
}

func (f *timingFunc) After(context.Context, api.Module, api.FunctionDefinition, []uint64) {
	f.pop()
}

func (f *timingFunc) Abort(context.Context, api.Module, api.FunctionDefinition, error) {
	f.pop()
}

func (f *timingFunc) pop() {
	l := f.l
	if !l.on || len(l.stack) == 0 {
		return
	}

	top := l.stack[len(l.stack)-1]
	l.stack = l.stack[:len(l.stack)-1]
	total := time.Since(top.start)
	l.self[top.name] += total - top.child

	if len(l.stack) > 0 {
		l.stack[len(l.stack)-1].child += total
	}
}

// TestRenderSelfTime lists where one full render spends its time, by self
// time per function, on the compiler. Listener overhead inflates functions
// that are called often. Run with BUI_SPIKE_PROFILE=1.
func TestRenderSelfTime(t *testing.T) {
	if os.Getenv("BUI_SPIKE_PROFILE") == "" {
		t.Skip("diagnostic; set BUI_SPIKE_PROFILE=1")
	}

	bin, err := ReadModule("bui_kit.wasm")
	skipIfNotBuilt(t, err)

	l := &timingListener{self: map[string]time.Duration{}}
	ctx := experimental.WithFunctionListenerFactory(context.Background(), l)

	k, err := NewWasmKit(ctx, Compiler, nil, bin, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()

	root := BuildCounter(k).Root
	hl := k.NewHeadless(root, 400, 300)
	hl.Render()

	const runs = 50

	l.on = true

	for range runs {
		hl.Render()
	}

	l.on = false

	type fd struct {
		name string
		d    time.Duration
	}

	all := make([]fd, 0, len(l.self))
	var total time.Duration

	for name, d := range l.self {
		all = append(all, fd{name, d})
		total += d
	}

	sort.Slice(all, func(i, j int) bool { return all[i].d > all[j].d })
	t.Logf("counter: %v per render (with listener overhead)", total/runs)

	for _, f := range all[:min(20, len(all))] {
		t.Logf("  %10v  %5.1f%%  %s", f.d/runs, 100*float64(f.d)/float64(total), f.name)
	}
}

// TestPairedFrameTimes renders the same tree alternately on the cgo kit and
// the wasm compiler, one frame each per round, so that both see the same
// machine load. It reports min and median per kit and the median of the
// per-round ratios. A diagnostic for PLAN.md; run with BUI_SPIKE_PROFILE=1.
func TestPairedFrameTimes(t *testing.T) {
	if os.Getenv("BUI_SPIKE_PROFILE") == "" {
		t.Skip("diagnostic; set BUI_SPIKE_PROFILE=1")
	}

	if !NativeAvailable {
		t.Skip("needs the cgo kit")
	}

	bin, err := ReadModule("bui_kit.wasm")
	skipIfNotBuilt(t, err)

	nk := NewNativeKit()
	defer nk.Close()

	wk, err := NewWasmKit(context.Background(), Compiler, nil, bin, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer wk.Close()

	for _, bt := range benchTrees() {
		var hls [2]Headless

		for i, k := range []Kit{nk, wk} {
			root, _ := bt.build(k)
			hls[i] = k.NewHeadless(root, bt.w, bt.h)
			hls[i].Render()

			defer func(k Kit, hl Headless, root Node) {
				hl.Close()
				k.FreeTree(root)
			}(k, hls[i], root)
		}

		const rounds = 200

		var times [2][]time.Duration

		ratios := make([]float64, 0, rounds)

		for range rounds {
			var d [2]time.Duration

			for i, hl := range hls {
				start := time.Now()
				hl.Render()
				d[i] = time.Since(start)
				times[i] = append(times[i], d[i])
			}

			ratios = append(ratios, float64(d[1])/float64(d[0]))
		}

		for i := range times {
			sort.Slice(times[i], func(a, b int) bool { return times[i][a] < times[i][b] })
		}

		sort.Float64s(ratios)
		t.Logf("%s: native min %v median %v; wasm-compiler min %v median %v; ratio median %.2f (p10 %.2f, p90 %.2f)",
			bt.name, times[0][0], times[0][rounds/2], times[1][0], times[1][rounds/2],
			ratios[rounds/2], ratios[rounds/10], ratios[rounds*9/10])
	}
}
