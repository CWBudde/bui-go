// SPDX-License-Identifier: GPL-2.0-or-later

//go:build cgo && linux && amd64

package enginetest

import (
	"math"
	"strings"
	"testing"

	"github.com/CWBudde/bui-go/internal/engine"
	"github.com/CWBudde/bui-go/internal/engine/cgoengine"
	"github.com/CWBudde/bui-go/internal/engine/wasmengine"
)

// mayLoseFPUFlag says an expression can differ for the one documented reason: wasm has no
// floating-point exception flags, so an intermediate inf or nan that turns finite again
// succeeds there and fails natively. PLAN.md, "Known limits".
func mayLoseFPUFlag(native bool, wasm bool) bool { return !native && wasm }

// FuzzEvalNumber compares the expression evaluator on both engines. Its parser takes a
// user's text directly, so this is the widest input surface the binding has.
func FuzzEvalNumber(f *testing.F) {
	for _, seed := range []string{
		"", "1", "2*pi", "1/0", "1/(1/0)", "sqrt(-1)", "log(0)", "1e308*10", "(((((", "1+", "abs(-3)",
		"sin(1)+cos(1)", "floor(2.5)", "ä", "\xff\xfe", "1;2", "a=1", strings.Repeat("(", 200) + "1",
		strings.Repeat("1+", 500) + "1", "max(1,2,3)", "0x10", "1_000", "%", "pi/", "--1", "2**3", "2^3",
	} {
		f.Add(seed)
	}

	native, wasm := engine.Kit(cgoengine.Kit{}), engine.Kit(wasmengine.Kit{})

	f.Fuzz(func(t *testing.T, expr string) {
		a, aok := native.EvalNumber(expr)
		b, bok := wasm.EvalNumber(expr)

		if aok != bok {
			if mayLoseFPUFlag(aok, bok) {
				return
			}

			t.Fatalf("EvalNumber(%q): native %v %v, wasm %v %v", expr, a, aok, b, bok)
		}

		if !aok {
			return
		}

		// libm differs in the last bits, which no UI shows. A real bug is far from that.
		const relative = 1e-9

		if a != b && math.Abs(a-b) > relative*math.Max(math.Abs(a), math.Abs(b)) {
			t.Fatalf("EvalNumber(%q): native %v, wasm %v", expr, a, b)
		}
	})
}

// FuzzSetStr compares what a property holds and how it is reported, for any string.
// A NUL ends the string on both sides, as a C string does.
func FuzzSetStr(f *testing.F) {
	for _, seed := range []string{
		"", "text", "ä€😀", "\xff\xfe", "a\x00b", "\n\t", strings.Repeat("x", 5000), "%s%n%d", "../../etc",
	} {
		f.Add("text", seed)
		f.Add("label", seed)
		f.Add("description", seed)
		f.Add("no-such-key", seed)
		f.Add(seed, "value")
	}

	kits := []engine.Kit{cgoengine.Kit{}, wasmengine.Kit{}}

	type result struct {
		ok  bool
		got string
	}

	f.Fuzz(func(t *testing.T, key, value string) {
		var results [2]result

		for i, k := range kits {
			k.SetEmit(ignoreEmit)

			n := k.NodeNew(typeButton)
			results[i].ok = k.SetStr(n, key, value)
			results[i].got = k.GetStr(n, key)
			k.NodeFree(n)
		}

		if results[0] != results[1] {
			t.Fatalf("SetStr(%q, %q): native %+v, wasm %+v", key, value, results[0], results[1])
		}
	})
}

// FuzzKeyInput sends what a host cannot vouch for, as keysyms and text arrive from the
// server: into a focused text field, and the two engines must then hold the same text.
func FuzzKeyInput(f *testing.F) {
	for _, seed := range [][2]string{
		{"a", "a"},
		{"", "BackSpace"},
		{"ä", "adiaeresis"},
		{"\xff", "a"},
		{"", ""},
		{"", "NoSuchKey"},
		{"€", "U20AC"},
		{"a\x00b", "a"},
		{strings.Repeat("x", 300), "x"},
	} {
		f.Add(seed[0], seed[1])
	}

	kits := []engine.Kit{cgoengine.Kit{}, wasmengine.Kit{}}

	f.Fuzz(func(t *testing.T, text, keysym string) {
		var got [2]string

		for i, k := range kits {
			k.SetEmit(ignoreEmit)

			root, n := k.NodeNew(typeFrame), k.NodeNew(typeText)
			k.NodeAdd(root, n)
			k.GeomSetPack(n)
			k.GeomSet(n, "fill", float64(fillX))

			ctx := k.ContextNew(root)
			k.ContextSetEmitHost(ctx)

			r := k.RasterNew(200, 40)
			k.Render(ctx, r, 200, 40)

			x, y, w, h := k.NodeRect(n)
			k.PressMods(ctx, x+w/2, y+h/2, engine.ModButton1)
			k.ReleaseMods(ctx, x+w/2, y+h/2, 0)
			k.Key(ctx, text, keysym, 0)
			k.Key(ctx, "", "Return", 0)
			k.RenderDamage(ctx, r, 200, 40, false)

			got[i] = k.GetStr(n, keyText)

			k.ContextFree(ctx)
			k.RasterFree(r)
			k.NodeFree(root)
		}

		if got[0] != got[1] {
			t.Fatalf("Key(%q, %q): native %q, wasm %q", text, keysym, got[0], got[1])
		}
	})
}
