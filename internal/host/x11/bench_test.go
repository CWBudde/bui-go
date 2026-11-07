// SPDX-License-Identifier: GPL-2.0-or-later

package x11

import (
	"testing"

	"github.com/CWBudde/bui-go/internal/engine/wasmengine"
	"github.com/jezek/xgb/xproto"
)

// present to a server and wait, as without the wait only the client's half is timed.
func benchmarkPresent(b *testing.B, width, height int) {
	b.Helper()

	k := wasmengine.Kit{}
	k.SetEmit(func(uint32, int, float64) {})

	h := New(k)
	if !h.Supported() {
		b.Skip("no display")
	}

	defer h.c.Close()

	h.scale = 1
	root := k.NodeNew("frame")
	ctx := k.ContextNew(root)
	k.ContextSetEmitHost(ctx)

	win := h.find(h.WindowNew(ctx, "bui x11 bench", width, height))
	h.WindowShow(win.id)
	win.render()

	b.Cleanup(func() {
		h.WindowFree(win.id)
		k.ContextFree(ctx)
		k.NodeFree(root)
	})

	for b.Loop() {
		win.present(true)

		if _, err := xproto.GetInputFocus(h.c).Reply(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPresentCounter(b *testing.B) { benchmarkPresent(b, 400, 300) }
func BenchmarkPresentFullHD(b *testing.B)  { benchmarkPresent(b, 1920, 1080) }
