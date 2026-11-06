// SPDX-License-Identifier: GPL-2.0-or-later

package wasmspike

import (
	"fmt"
)

// Node is an index into a Kit's node table. Its id in the kit's ref field is
// Node+1, so 0 keeps meaning "no id".
type Node int

// Kit events, as in BUI_kit.h.
const (
	EvChange = 1
	EvClick  = 2
)

// Pack values, as in BUI_kit.h and node.go.
const (
	geomPack = 1
	fillX    = 1
	fillBoth = 3
	sideLeft = 2
	fitFill  = 0
	button1  = 8
)

// PackOptions mirrors bui.PackOptions.
type PackOptions struct {
	Side, Fill                 int
	Expand                     bool
	PadX0, PadX1, PadY0, PadY1 int
}

// Kit is the part of BUI's C kit the spike drives, implemented once on the
// committed cgo libraries and once on bui_kit.wasm. It is a throwaway preview
// of Phase 1's engine seam, not that seam. Methods panic on an engine error.
type Kit interface {
	Name() string
	NewNode(typ string) Node
	Add(parent, child Node)
	SetNum(n Node, key string, v float64) bool
	SetStr(n Node, key, v string) bool
	Num(n Node, key string) float64
	Str(n Node, key string) string
	Pack(n Node, o PackOptions)
	Rect(n Node) [4]int
	SetImageSource(n Node, src string)
	FreeTree(root Node)

	// OnEmit sets the handler for events of nodes with an id.
	OnEmit(f func(n Node, kind int, a float64))
	EvalNumber(expr string) (float64, bool)
	ImagePending() int
	ImagePoll() int

	NewHeadless(root Node, w, h int) Headless
	Close()
}

// Headless is a context with a raster, as bui.Headless.
type Headless interface {
	// Render lays out and redraws every pixel (BK_render with full = 1), as
	// bui.Headless.Render does.
	Render()
	// RenderDamage redraws only what changed (full = 0), as a window host does
	// after input.
	RenderDamage()
	// Invalidate marks the whole raster as damaged.
	Invalidate()
	Pixels() []uint32
	Press(x, y int)
	Release(x, y int)
	Motion(x, y int)
	Close()
}

func must(ok bool, what string) {
	if !ok {
		panic("kit refused " + what)
	}
}

// Counter is the tree of examples/counter, with its click handler.
type Counter struct {
	Root, Clock, Size, Count, Button Node
	Total                            int
}

// BuildCounter creates examples/counter's tree on k and installs the button
// handler, which calls SetStr while the kit is emitting.
func BuildCounter(k Kit) *Counter {
	c := &Counter{}
	c.Root = k.NewNode("frame")
	must(k.SetNum(c.Root, "padding", 8), "padding")

	c.Clock = k.NewNode("label")
	must(k.SetStr(c.Clock, "text", "Running for 0 s"), "text")
	k.Add(c.Root, c.Clock)
	k.Pack(c.Clock, PackOptions{Fill: fillX})

	c.Size = k.NewNode("slider")
	must(k.SetStr(c.Size, "label", "Step"), "label")
	must(k.SetNum(c.Size, "min", 1), "min")
	must(k.SetNum(c.Size, "max", 10), "max")
	must(k.SetNum(c.Size, "value", 1), "value")
	k.Add(c.Root, c.Size)
	k.Pack(c.Size, PackOptions{Fill: fillX, PadY0: 6})

	c.Count = k.NewNode("label")
	must(k.SetStr(c.Count, "text", "Count: 0"), "text")
	k.Add(c.Root, c.Count)
	k.Pack(c.Count, PackOptions{Fill: fillX, PadY0: 6})

	c.Button = k.NewNode("button")
	must(k.SetStr(c.Button, "text", "Add step"), "text")
	k.Add(c.Root, c.Button)
	k.Pack(c.Button, PackOptions{Fill: fillX, PadY0: 6})

	k.OnEmit(func(n Node, kind int, _ float64) {
		if n == c.Button && kind == EvClick {
			c.Total += int(k.Num(c.Size, "value"))
			must(k.SetStr(c.Count, "text", fmt.Sprintf("Count: %d", c.Total)), "text in emit")
		}
	})

	return c
}

// BuildDense creates a full-HD-sized tree: columns of labels, sliders,
// buttons and text fields, enough to fill 1920x1080.
func BuildDense(k Kit, cols, rows int) Node {
	root := k.NewNode("frame")
	must(k.SetNum(root, "padding", 4), "padding")

	for c := range cols {
		col := k.NewNode("frame")
		k.Add(root, col)
		k.Pack(col, PackOptions{Side: sideLeft, Fill: fillBoth, Expand: true, PadX0: 2})

		for r := range rows {
			var n Node

			switch (r + c) % 4 {
			case 0:
				n = k.NewNode("label")
				must(k.SetStr(n, "text", fmt.Sprintf("Label %d.%d: the quick brown fox", c, r)), "text")
			case 1:
				n = k.NewNode("slider")
				must(k.SetStr(n, "label", fmt.Sprintf("Value %d", r)), "label")
				must(k.SetNum(n, "min", 0), "min")
				must(k.SetNum(n, "max", 100), "max")
				must(k.SetNum(n, "value", float64((r*7+c*13)%100)), "value")
			case 2:
				n = k.NewNode("button")
				must(k.SetStr(n, "text", fmt.Sprintf("Button %d", r)), "text")
			default:
				n = k.NewNode("checkbox")
				must(k.SetStr(n, "text", fmt.Sprintf("Option %d", r)), "text")
			}

			k.Add(col, n)
			k.Pack(n, PackOptions{Fill: fillX, PadY0: 2})
		}
	}

	return root
}
