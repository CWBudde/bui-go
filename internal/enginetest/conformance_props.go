// SPDX-License-Identifier: GPL-2.0-or-later

package enginetest

import (
	"strings"

	"github.com/CWBudde/bui-go/internal/engine"
)

const noSuchKey = "no_such_key"

func testProperties(h *harness) {
	h.sub("Num", testNumProps)
	h.sub("NumUnknownKey", testNumUnknown)
	h.sub("Str", testStrProps)
	h.sub("StrLong", testStrLong)
	h.sub("StrIsACopy", testStrIsACopy)
	h.sub("StrEmbeddedNUL", testStrEmbeddedNUL)
	h.sub("StrUnknownKey", testStrUnknown)
}

// testNumProps needs every bit of a float64 to survive the trip through the kit.
func testNumProps(h *harness) {
	n := h.bare(typeNumber)
	defer h.k.NodeFree(n)

	for _, v := range []float64{0, 1, -1, 0.1, 0.1 + 0.2, -2.5, 1.0 / 3.0, 1e15 + 0.5, -1e300, 1e-300} {
		h.num(n, keyValue, v)
		checkEq(h.t, "value", h.k.GetNum(n, keyValue), v)
	}

	s := h.bare(typeSlider)
	defer h.k.NodeFree(s)

	h.num(s, "min", -4)
	h.num(s, "max", 4)
	h.num(s, keyValue, 0.25)
	checkEq(h.t, "slider value", h.k.GetNum(s, keyValue), 0.25)
	h.num(s, keyValue, 99)
	checkEq(h.t, "value clamped to max", h.k.GetNum(s, keyValue), 4)
	h.num(s, keyValue, -99)
	checkEq(h.t, "value clamped to min", h.k.GetNum(s, keyValue), -4)
}

func testNumUnknown(h *harness) {
	n := h.bare(typeLabel)
	defer h.k.NodeFree(n)

	if h.k.SetNum(n, noSuchKey, 1) {
		h.t.Error("SetNum of an unknown key = true")
	}

	if h.k.SetNum(n, "", 1) {
		h.t.Error("SetNum of an empty key = true")
	}

	checkEq(h.t, "GetNum of an unknown key", h.k.GetNum(n, noSuchKey), 0)
	checkEq(h.t, "GetNum of an empty key", h.k.GetNum(n, ""), 0)
}

func testStrProps(h *harness) {
	n, other := h.bare(typeLabel), h.bare(typeLabel)
	defer h.k.NodeFree(n)
	defer h.k.NodeFree(other)

	checkStr(h.t, "text of a new label", h.k.GetStr(n, keyText), "")
	h.str(other, keyText, "other")

	for _, v := range []string{
		"plain",
		"",
		"Grüße, Straße",
		"Привет, мир",
		textCJK,
		textCJKMixed,
		"line one\nline two\ttab",
		"\xff\xfe not UTF-8",
		"short",
	} {
		h.str(n, keyText, v)
		checkStr(h.t, "text", h.k.GetStr(n, keyText), v)
	}

	checkStr(h.t, "text of another node", h.k.GetStr(other, keyText), "other")

	// Keys are separate properties of a node.
	h.str(n, keyLabel, "the label")
	h.str(n, keyText, "the text")
	checkStr(h.t, keyLabel, h.k.GetStr(n, keyLabel), "the label")
	checkStr(h.t, keyText, h.k.GetStr(n, keyText), "the text")
}

// testStrLong runs up and down in size: a string several hundred KB long does not
// fit what shorter ones used, and a shorter one must not read past its own end.
func testStrLong(h *harness) {
	n, witness := h.bare(typeLabel), h.bare(typeLabel)
	defer h.k.NodeFree(n)
	defer h.k.NodeFree(witness)

	h.str(witness, keyText, "witness")

	for _, size := range []int{1000, 70000, 300000, 900000, 300000, 10, 900001} {
		v := longText(size)
		h.str(n, keyText, v)

		got := h.k.GetStr(n, keyText)
		checkEq(h.t, "length of a long text", len(got), size)
		checkStr(h.t, "long text", got, v)
		checkStr(h.t, "text of another node", h.k.GetStr(witness, keyText), "witness")
	}

	// A long non-ASCII string, which is the same on the way back byte by byte.
	v := strings.Repeat(textCJKShort, 40000)
	h.str(n, keyText, v)
	checkStr(h.t, "long non-ASCII text", h.k.GetStr(n, keyText), v)

	// A long key is unknown, not cut off to a known one.
	if h.k.SetStr(n, keyText+longText(200000), "x") {
		h.t.Error("SetStr of a long unknown key = true")
	}
}

// testStrIsACopy fails if GetStr hands out a view of memory the kit can reuse.
func testStrIsACopy(h *harness) {
	n := h.bare(typeLabel)
	defer h.k.NodeFree(n)

	want := longText(5000)
	h.str(n, keyText, want)
	got := h.k.GetStr(n, keyText)

	for i := range 5 {
		h.str(n, keyText, strings.Repeat("x", 5000+i))
	}

	h.k.GetStr(n, keyText)
	checkStr(h.t, "an earlier GetStr result", got, want)
}

func testStrEmbeddedNUL(h *harness) {
	n := h.bare(typeLabel)
	defer h.k.NodeFree(n)

	h.str(n, keyText, "before\x00after")
	checkStr(h.t, "text with a NUL", h.k.GetStr(n, keyText), "before")
	h.str(n, keyText, "\x00after")
	checkStr(h.t, "text starting with a NUL", h.k.GetStr(n, keyText), "")

	// Keys stop at a NUL as well.
	h.str(n, keyText+"\x00junk", "by a key with a NUL")
	checkStr(h.t, "text set through a key with a NUL", h.k.GetStr(n, keyText), "by a key with a NUL")
	checkStr(h.t, "text read through a key with a NUL", h.k.GetStr(n, keyText+"\x00junk"), "by a key with a NUL")
}

func testStrUnknown(h *harness) {
	n := h.bare(typeLabel)
	defer h.k.NodeFree(n)

	h.str(n, keyText, "unchanged")

	if h.k.SetStr(n, noSuchKey, "x") {
		h.t.Error("SetStr of an unknown key = true")
	}

	if h.k.SetStr(n, "", "x") {
		h.t.Error("SetStr of an empty key = true")
	}

	checkStr(h.t, "GetStr of an unknown key", h.k.GetStr(n, noSuchKey), "")
	checkStr(h.t, "GetStr of an empty key", h.k.GetStr(n, ""), "")
	checkStr(h.t, "text after refused sets", h.k.GetStr(n, keyText), "unchanged")
}

func testSetStrs(h *harness) {
	h.sub("UnknownKey", testStrsUnknown)
	h.sub("ClampsValue", testStrsClamp)
	h.sub("OrderAndContent", testStrsContent)
	h.sub("EmptyListOpensNothing", testStrsEmpty)
}

func testStrsUnknown(h *harness) {
	n := h.bare(typeDrop)
	defer h.k.NodeFree(n)

	for _, vs := range [][]string{{"a"}, {}, nil} {
		if h.k.SetStrs(n, noSuchKey, vs) {
			h.t.Errorf("SetStrs of an unknown key with %d strings = true", len(vs))
		}
	}
}

// testStrsClamp sees the count of items through what the kit does with the value.
func testStrsClamp(h *harness) {
	n := h.bare(typeDrop)
	defer h.k.NodeFree(n)

	setItems(h, n, []string{"a", "b", "c", "d"})
	h.num(n, keyValue, 3)
	checkEq(h.t, "value with 4 items", h.k.GetNum(n, keyValue), 3)
	setItems(h, n, []string{"a", "b"})
	checkEq(h.t, "value clamped to 2 items", h.k.GetNum(n, keyValue), 1)
	setItems(h, n, []string{})
	checkEq(h.t, "value with an empty list", h.k.GetNum(n, keyValue), 0)
	setItems(h, n, nil)
	checkEq(h.t, "value with a nil list", h.k.GetNum(n, keyValue), 0)
}

func setItems(h *harness, n engine.Node, items []string) {
	h.t.Helper()

	if !h.k.SetStrs(n, keyItems, items) {
		h.t.Fatalf("SetStrs(items, %d strings) = false", len(items))
	}
}

// renderDropdown draws a closed dropdown showing items[value].
func renderDropdown(h *harness, items []string, value float64) []uint32 {
	h.t.Helper()

	root, d := h.node(typeFrame), h.node(typeDrop)
	h.packIn(root, d, pack{fill: fillX})
	setItems(h, d, items)
	h.num(d, keyValue, value)

	s := h.scene(root, 300, 60)
	defer s.close()

	return s.render()
}

// testStrsContent has no way to read the items back, so it looks at what is drawn.
// Which item the value selects shows both the order and each string's content.
func testStrsContent(h *harness) {
	const one, two, three = "First", "Second", "Third"

	second := renderDropdown(h, []string{one, two, three}, 1)
	same := renderDropdown(h, []string{two, three, one}, 0)
	first := renderDropdown(h, []string{one, two, three}, 0)
	long := renderDropdown(h, []string{one, two + "\x00cut", longText(70000)}, 1)
	many := make([]string, 2000)

	for i := range many {
		many[i] = "item " + itoa(i)
	}

	many[1234] = two
	far := renderDropdown(h, many, 1234)

	checkEq(h.t, "pixels differing between the same item at another index", changedPixels(second, same, 300, rect{}), 0)
	checkEq(h.t, "pixels differing with a NUL and a long neighbour", changedPixels(second, long, 300, rect{}), 0)
	checkEq(h.t, "pixels differing with 2000 items", changedPixels(second, far, 300, rect{}), 0)

	if changedPixels(second, first, 300, rect{}) == 0 {
		h.t.Error("selecting another item changes nothing, so this test proves nothing")
	}
}

// testStrsEmpty checks that items decide if a press opens a menu.
func testStrsEmpty(h *harness) {
	root, d := h.node(typeFrame), h.node(typeDrop)
	h.packIn(root, d, pack{fill: fillX})
	setItems(h, d, []string{"one", "two", "three", "four"})

	s := h.scene(root, 300, 200)
	defer s.close()

	closed := s.render()
	skip := h.rectOf(d)

	s.click(d)

	if changedPixels(closed, s.render(), s.w, skip) == 0 {
		h.t.Error("a press with items opened nothing")
	}

	s.click(d) // a press outside the open menu closes it
	setItems(h, d, []string{})

	closed = s.render()
	s.click(d)
	checkEq(h.t, "pixels changed outside by a press with no items", changedPixels(closed, s.render(), s.w, skip), 0)
}
