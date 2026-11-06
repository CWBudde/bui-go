// SPDX-License-Identifier: GPL-2.0-or-later

package bui

/*
#include <stdlib.h>
#include "bridge.h"
*/
import "C"

import (
	"fmt"
	"runtime/cgo"
	"unsafe"
)

// Node is one element of the kit's retained tree. All methods are UI-thread
// only; see Do. A Node owns C memory: close the root of each tree when done,
// which closes its descendants too.
type Node struct {
	c        *C.BKNode
	typ      string
	handle   cgo.Handle
	closing  bool     // Close was requested; the C node may still exist
	owner    *context // the live context this node is the root of
	onClick  func()
	onChange func(float64)
}

// NewNode creates a detached node of a kit type such as "frame", "button",
// "label" or "slider". It panics on an unknown type.
func NewNode(typ string) *Node {
	cs := C.CString(typ)
	defer C.free(unsafe.Pointer(cs))
	c := C.BK_node_new(cs)
	if c == nil {
		panic(fmt.Sprintf("bui: unknown node type %q", typ))
	}
	n := &Node{c: c, typ: typ}
	n.handle = newHandle(n)
	C.bui_node_set_handle(c, C.uintptr_t(n.handle))
	return n
}

// live returns the C node, or panics instead of letting a dangling or NULL
// pointer reach C.
func (n *Node) live() *C.BKNode {
	if n.closing {
		panic(fmt.Sprintf("bui: use of closed %q node", n.typ))
	}
	return n.c
}

func (n *Node) closed() bool { return n.c == nil }

// SetNum sets a number property. The error reports a key unknown to the kit.
// A notifying key such as "set_value" runs handlers before SetNum returns.
func (n *Node) SetNum(key string, v float64) error {
	c := n.live()
	ck := C.CString(key)
	defer C.free(unsafe.Pointer(ck))
	ok := C.BK_set_num(c, ck, C.double(v)) != 0
	runDeferred()
	if !ok {
		return fmt.Errorf("bui: unknown number property %q", key)
	}
	return nil
}

// SetStr sets a string property. The error reports a key unknown to the kit.
func (n *Node) SetStr(key, v string) error {
	c := n.live()
	ck, cv := C.CString(key), C.CString(v)
	defer C.free(unsafe.Pointer(ck))
	defer C.free(unsafe.Pointer(cv))
	ok := C.BK_set_str(c, ck, cv) != 0
	runDeferred()
	if !ok {
		return fmt.Errorf("bui: unknown string property %q", key)
	}
	return nil
}

// Num returns a number property, 0 for an unknown key.
func (n *Node) Num(key string) float64 {
	c := n.live()
	ck := C.CString(key)
	defer C.free(unsafe.Pointer(ck))
	return float64(C.BK_get_num(c, ck))
}

// Str returns a copy of a string property, "" for an unknown key.
func (n *Node) Str(key string) string {
	c := n.live()
	ck := C.CString(key)
	defer C.free(unsafe.Pointer(ck))
	return C.GoString(C.BK_get_str(c, ck))
}

// Add appends child to n, moving it from any parent it has. It panics if child is
// n or an ancestor of n, as the kit would loop forever over the resulting cycle.
func (n *Node) Add(child *Node) {
	parent, c := n.live(), child.live()
	for p := parent; p != nil; p = C.BK_node_parent(p) {
		if p == c {
			panic(fmt.Sprintf("bui: Add of a %q node to itself or a descendant", child.typ))
		}
	}
	if C.BK_node_add(parent, c, -1) == 0 {
		panic("bui: BK_node_add refused the node")
	}
}

// Side is the edge of the frame a packed child attaches to.
type Side int

// Pack sides, numbered as in BK_geom_set's "side" key.
const (
	SideTop Side = iota
	SideBottom
	SideLeft
	SideRight
)

// Fill is the direction a packed child stretches in.
type Fill int

// Pack fills, numbered as in BK_geom_set's "fill" key.
const (
	FillNone Fill = iota
	FillX
	FillY
	FillBoth
)

// PackOptions are Tk-style pack settings for a child of a "frame".
type PackOptions struct {
	Side                       Side
	Fill                       Fill
	Expand                     bool
	PadX0, PadX1, PadY0, PadY1 int
}

// Pack places n with the pack manager. Children of a "frame" without a manager
// get a zero rect and are not hit-testable.
func (n *Node) Pack(o PackOptions) {
	c := n.live()
	C.BK_geom_set_manager(c, C.BK_GEOM_PACK)
	expand := 0
	if o.Expand {
		expand = 1
	}
	for _, kv := range []struct {
		key string
		v   int
	}{
		{"side", int(o.Side)},
		{"fill", int(o.Fill)},
		{"expand", expand},
		{"padx0", o.PadX0},
		{"padx1", o.PadX1},
		{"pady0", o.PadY0},
		{"pady1", o.PadY1},
	} {
		ck := C.CString(kv.key)
		C.BK_geom_set(c, ck, C.double(kv.v))
		C.free(unsafe.Pointer(ck))
	}
}

// Rect returns the node's rectangle in raster pixels. Layout only runs while
// rendering, so it is all zero before the first render.
func (n *Node) Rect() (x, y, w, h int) {
	var r [4]C.int
	C.BK_node_rect(n.live(), &r[0])
	return int(r[0]), int(r[1]), int(r[2]), int(r[3])
}

// OnClick sets the handler for button clicks.
func (n *Node) OnClick(f func()) {
	n.live()
	n.onClick = f
}

// OnChange sets the handler for value changes. It fires for interaction and for
// "set_value" (synchronously, inside SetNum), not for "value".
func (n *Node) OnChange(f func(float64)) {
	n.live()
	n.onChange = f
}

// Close frees the node and its descendants; they all become unusable. It is
// idempotent. Called from a handler, the free waits until the kit has returned
// to the outermost level. Until then the node itself is already unusable and
// silent, while its descendants keep working.
//
// The root of a Window or Headless must be closed after them, and so must any
// ancestor of that root: it panics otherwise, because the context would be left
// with dangling pointers.
func (n *Node) Close() {
	if n.closing {
		return
	}
	if root := liveRootIn(n.c); root != nil {
		panic(fmt.Sprintf("bui: Close of a %q node holding the root of a live Window or Headless", n.typ))
	}
	n.closing = true
	closeOrDefer(n.free)
}

// liveRootIn finds a node in the subtree that a context not yet being closed still
// uses as its root.
func liveRootIn(c *C.BKNode) *Node {
	if h := C.bui_node_handle(c); h != 0 {
		n := cgo.Handle(h).Value().(*Node)
		if n.owner != nil && !n.owner.closing {
			return n
		}
	}
	for i := range int(C.BK_node_child_count(c)) {
		if root := liveRootIn(C.BK_node_child(c, C.int(i))); root != nil {
			return root
		}
	}
	return nil
}

// free destroys the subtree post-order. BK_node_free only detaches a child with
// a non-NULL ref instead of freeing it, and every Go-made node has one, so the
// children go first; the free then finds none of ours left. No kit call may be
// on the stack.
func (n *Node) free() {
	if n.c == nil {
		return
	}
	freeChildren(n.c)
	c := n.c
	n.c = nil
	n.closing = true
	C.bui_node_set_handle(c, 0)
	deleteHandle(n.handle)
	C.BK_node_free(c)
}

// freeChildren walks back to front since each free detaches from the parent.
// Kit-internal children have no handle and stay with the kit, which frees them
// with their parent, but our nodes below them still need freeing.
func freeChildren(c *C.BKNode) {
	for i := int(C.BK_node_child_count(c)) - 1; i >= 0; i-- {
		child := C.BK_node_child(c, C.int(i))
		if h := C.bui_node_handle(child); h != 0 {
			cgo.Handle(h).Value().(*Node).free()
		} else {
			freeChildren(child)
		}
	}
}
