// SPDX-License-Identifier: GPL-2.0-or-later

package enginetest

import "github.com/CWBudde/bui-go/internal/engine"

// knownTypes is BUI_kit.h's list, plus "frame", which the binding uses.
var knownTypes = []string{
	"column", "row", "grid", "box", "panel", "tabs", "sidetabs", "scroll", "label", "separator",
	"progress", "button", "toggle", "checkbox", "decorator", "text", "search", "hotkey", "number",
	"slider", "dropdown", "enum", "swatch", "popover", "menubutton", "menubar", "list", "tree",
	"gridview", "ramp", "curve", "normal", "histogram", "radio", "textarea", "meter", "custom",
	typeFrame,
}

func testTree(h *harness) {
	h.sub("NodeNew", testNodeNew)
	h.sub("Add", testNodeAdd)
	h.sub("Move", testNodeMove)
	h.sub("FreeOrder", testFreeOrder)
}

func testNodeNew(h *harness) {
	for _, typ := range knownTypes {
		n := h.k.NodeNew(typ)
		if n == 0 {
			h.t.Errorf("NodeNew(%q) = 0", typ)

			continue
		}

		checkStr(h.t, "type of "+typ, h.k.GetStr(n, "type"), typ)
		checkEq(h.t, "parent of a new "+typ, h.k.NodeParent(n), 0)
		checkEq(h.t, "id of a new "+typ, h.k.NodeID(n), 0)
		h.k.NodeFree(n)
	}

	for _, typ := range []string{"no_such_type", "", "Button", "button ", "knöpfchen", longText(100000)} {
		if n := h.k.NodeNew(typ); n != 0 {
			h.t.Errorf("NodeNew(%s) = %v, want 0", abbrev(typ), n)
			h.k.NodeFree(n)
		}
	}

	// A type stops at a NUL, like every string that reaches C.
	n := h.k.NodeNew(typeButton + "\x00junk")
	if n == 0 {
		h.t.Fatal(`NodeNew("button\x00junk") = 0`)
	}

	h.k.NodeFree(n)
}

func testNodeAdd(h *harness) {
	root, a, b, c := h.node(typeFrame), h.node(typeButton), h.node(typeLabel), h.node(typeFrame)
	defer freeTree(h.k, root)

	checkEq(h.t, "children of a new frame", h.k.NodeChildCount(root), 0)
	checkEq(h.t, "NodeChild(0) of a new frame", h.k.NodeChild(root, 0), 0)

	h.add(root, a)
	h.add(root, b)
	h.add(root, c)

	checkEq(h.t, "child count", h.k.NodeChildCount(root), 3)

	for i, want := range []engine.Node{a, b, c} {
		checkEq(h.t, "NodeChild("+itoa(i)+")", h.k.NodeChild(root, i), want)
		checkEq(h.t, "NodeParent", h.k.NodeParent(want), root)
	}

	checkEq(h.t, "NodeChild(-1)", h.k.NodeChild(root, -1), 0)
	checkEq(h.t, "NodeChild(3)", h.k.NodeChild(root, 3), 0)
	checkEq(h.t, "NodeParent of the root", h.k.NodeParent(root), 0)

	if h.k.NodeAdd(root, root) {
		h.t.Error("NodeAdd of a node to itself = true")
	}

	checkEq(h.t, "child count after a refused add", h.k.NodeChildCount(root), 3)
}

func testNodeMove(h *harness) {
	p1, p2, a, b := h.node(typeFrame), h.node(typeFrame), h.node(typeButton), h.node(typeButton)
	defer freeTree(h.k, p1)
	defer freeTree(h.k, p2)

	h.add(p1, a)
	h.add(p1, b)
	h.add(p2, a)

	checkEq(h.t, "old parent's count", h.k.NodeChildCount(p1), 1)
	checkEq(h.t, "old parent's child", h.k.NodeChild(p1, 0), b)
	checkEq(h.t, "new parent's count", h.k.NodeChildCount(p2), 1)
	checkEq(h.t, "new parent's child", h.k.NodeChild(p2, 0), a)
	checkEq(h.t, "NodeParent after the move", h.k.NodeParent(a), p2)

	// Adding to the current parent moves the child to the end.
	h.add(p2, b)
	h.add(p2, a)

	checkEq(h.t, "first after the move to the end", h.k.NodeChild(p2, 0), b)
	checkEq(h.t, "last after the move to the end", h.k.NodeChild(p2, 1), a)
	checkEq(h.t, "count after the move to the end", h.k.NodeChildCount(p2), 2)
}

// testFreeOrder pins what Node.free relies on: NodeFree only detaches a child with
// an id, and frees one without, along with what the kit made itself.
func testFreeOrder(h *harness) {
	root, a, b := h.node(typeFrame), h.node(typeButton), h.node(typeLabel)

	h.add(root, a)
	h.add(root, b)
	h.k.NodeSetID(a, 0)
	h.k.NodeFree(a)
	checkEq(h.t, "count after freeing a child", h.k.NodeChildCount(root), 1)
	checkEq(h.t, "the other child after freeing one", h.k.NodeChild(root, 0), b)

	// b keeps its id and survives its parent's free, detached.
	bID := h.k.NodeID(b)
	h.str(b, keyText, "survives")
	h.k.NodeSetID(root, 0)
	h.k.NodeFree(root)
	checkEq(h.t, "parent of a child with an id after its parent's free", h.k.NodeParent(b), 0)
	checkEq(h.t, "id after the parent's free", h.k.NodeID(b), bID)
	checkStr(h.t, "text after the parent's free", h.k.GetStr(b, keyText), "survives")
	h.k.NodeSetID(b, 0)
	h.k.NodeFree(b)

	// Children without ids go with their parent; that must not crash or leave
	// anything a later tree can see. Package bui's order on a deep tree too.
	bare := h.bare(typeFrame)
	h.add(bare, h.bare(typeButton))
	h.add(bare, h.bare(typeLabel))
	h.k.NodeFree(bare)

	deep := h.node(typeFrame)
	for parent, i := deep, 0; i < 50; i++ {
		child := h.node(typeFrame)
		h.add(parent, child)
		h.add(child, h.node(typeButton))
		parent = child
	}

	freeTree(h.k, deep)
}

func testIDs(h *harness) {
	a, b := h.bare(typeButton), h.bare(typeButton)
	defer h.k.NodeFree(a)

	checkEq(h.t, "id of a new node", h.k.NodeID(a), 0)

	for _, id := range []uint32{1, 2, 0x7FFFFFFF, 0x80000000, 0xFFFFFFFE, 0xFFFFFFFF, 0} {
		h.k.NodeSetID(a, id)
		checkEq(h.t, "NodeID after NodeSetID", h.k.NodeID(a), id)
		checkEq(h.t, "NodeID of another node", h.k.NodeID(b), 0)
	}

	// An id is the node's, wherever it is reached from. Its parent frees b below, as
	// b has no id by then.
	root := h.bare(typeFrame)
	defer h.k.NodeFree(root)

	h.k.NodeSetID(b, 0xFFFFFFFF)
	h.add(root, b)
	checkEq(h.t, "id through NodeChild", h.k.NodeID(h.k.NodeChild(root, 0)), 0xFFFFFFFF)
	h.k.NodeSetID(b, 0)
	checkEq(h.t, "id through NodeChild after clearing", h.k.NodeID(h.k.NodeChild(root, 0)), 0)
}
