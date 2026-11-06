// SPDX-License-Identifier: GPL-2.0-or-later

//go:build cgo && linux && amd64

package wasmspike

import (
	"math"
	"testing"

	bui "github.com/CWBudde/bui-go"
)

// TestPublicAPIEvalNumber checks that package bui, through the replace to the
// repository root, links the same committed libraries as NativeKit.
func TestPublicAPIEvalNumber(t *testing.T) {
	if v, ok := bui.EvalNumber("2*pi"); !ok || math.Abs(v-2*math.Pi) > 1e-12 {
		t.Fatalf("bui.EvalNumber(2*pi) = %v, %v", v, ok)
	}

	k := NewNativeKit()
	defer k.Close()

	if v, ok := k.EvalNumber("2*pi"); !ok || v != 2*math.Pi {
		t.Fatalf("NativeKit.EvalNumber(2*pi) = %v, %v", v, ok)
	}
}

// BenchmarkRenderPublicAPI renders the counter tree through package bui's
// Headless, the cgo binding as users see it: the reference for the plan's
// earlier native number.
func BenchmarkRenderPublicAPI(b *testing.B) {
	root := bui.NewNode("frame")
	_ = root.SetNum("padding", 8)

	for _, s := range []struct{ typ, key, v string }{
		{"label", "text", "Running for 0 s"},
		{"slider", "label", "Step"},
		{"label", "text", "Count: 0"},
		{"button", "text", "Add step"},
	} {
		n := bui.NewNode(s.typ)
		_ = n.SetStr(s.key, s.v)
		root.Add(n)
		n.Pack(bui.PackOptions{Fill: bui.FillX, PadY0: 6})
	}

	hl := bui.NewHeadless(root, 400, 300)

	b.Cleanup(func() {
		hl.Close()
		root.Close()
	})
	hl.Render()

	b.ResetTimer()

	for range b.N {
		hl.Render()
	}
}
