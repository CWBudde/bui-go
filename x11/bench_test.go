// SPDX-License-Identifier: GPL-2.0-or-later

package x11

import (
	"testing"

	bui "github.com/CWBudde/bui-go"
	"github.com/jezek/xgb/xproto"
)

// present to a server and wait, as without the wait only the client's half is timed.
func benchmarkPresent(b *testing.B, width, height int) {
	b.Helper()

	h := newHost()
	if !h.Supported() {
		b.Skip("no display")
	}

	defer h.c.Close()

	h.scale = 1
	root := bui.NewNode("frame")

	win, err := h.newWindow(root, "bui x11 bench", width, height)
	if err != nil {
		b.Fatal(err)
	}

	win.show()
	win.render()

	b.Cleanup(func() {
		win.free()
		root.Close()
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
